//go:build integration

package events

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type draftFixture struct {
	db         *bun.DB
	service    *Service
	actor      setup.CuratorSession
	credential string
	sources    map[string]uuid.UUID
	media      map[string]uuid.UUID
	immich     map[string]uuid.UUID
}

type draftQueryCounter struct {
	count atomic.Int64
}

func (counter *draftQueryCounter) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context {
	counter.count.Add(1)
	return ctx
}

func (*draftQueryCounter) AfterQuery(context.Context, *bun.QueryEvent) {}

func newDraftFixture(t *testing.T) draftFixture {
	t.Helper()
	ctx := context.Background()
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(ctx, db))
	now := time.Date(2026, 5, 3, 4, 5, 6, 0, time.UTC)
	personID := uuid.New()
	accessID := uuid.New()
	sessionID := uuid.New()
	credential := sha256.Sum256([]byte("draft-session"))
	credentialHash := sha256.Sum256(credential[:])
	_, err := db.NewRaw(`
		UPDATE system_settings SET setup_complete = true WHERE id = 1;
		INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Recipient', 'Recipient');
		INSERT INTO person_roles (person_id, role) VALUES (?, 'recipient');
		INSERT INTO recipient_access_generations (
			id, person_id, generation, state, is_current, onboarding_completed_at, created_at, updated_at
		) VALUES (?, ?, 1, 'completed', true, ?, ?, ?);
		INSERT INTO sessions (
			id, credential_hash, person_id, recipient_access_generation_id, security_epoch,
			session_type, idle_expires_at
		) SELECT ?, ?, ?, ?, security_epoch, 'trusted', now() + interval '1 day'
		FROM system_settings WHERE id = 1
	`, personID, personID, accessID, personID, now, now, now,
		sessionID, credentialHash[:], personID, accessID).Exec(ctx)
	require.NoError(t, err)

	fixture := draftFixture{
		db: db, service: New(db), actor: setup.CuratorSession{PersonID: personID, SessionID: sessionID},
		credential: hex.EncodeToString(credential[:]),
		sources:    map[string]uuid.UUID{"first": uuid.New(), "second": uuid.New(), "empty": uuid.New(), "ignored": uuid.New()},
		media:      map[string]uuid.UUID{"shared": uuid.New(), "first_only": uuid.New(), "second_only": uuid.New(), "unknown": uuid.New()},
		immich:     make(map[string]uuid.UUID),
	}
	fixture.service.now = func() time.Time { return now }
	for name, sourceID := range fixture.sources {
		disposition := "unreviewed"
		var ignoredAt any
		if name == "ignored" {
			disposition = "ignored"
			ignoredAt = now
		}
		immichID := uuid.New()
		fixture.immich["source:"+name] = immichID
		_, err := db.NewRaw(`
			INSERT INTO source_albums (
				id, immich_album_id, name, description, asset_count, source_created_at,
				source_updated_at, disposition, ignored_at, first_seen_at, last_seen_at,
				source_fingerprint, next_reconciliation_at
			) VALUES (?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, decode(repeat('00', 32), 'hex'), ?)
		`, sourceID, immichID, name+" source", name+" description", now, now,
			disposition, ignoredAt, now, now, now).Exec(ctx)
		require.NoError(t, err)
	}

	fixture.addMedia(t, "shared", "2026-05-02T01:30:00Z", "first", "second")
	fixture.addMedia(t, "first_only", "2026-05-01T22:00:00", "first")
	fixture.addMedia(t, "second_only", "2026-05-02T08:00:00Z", "second")
	fixture.addMedia(t, "unknown", "", "first")
	return fixture
}

func (fixture draftFixture) addMedia(t *testing.T, name, capture string, sourceNames ...string) {
	t.Helper()
	ctx := context.Background()
	mediaID := fixture.media[name]
	immichID := uuid.New()
	fixture.immich["media:"+name] = immichID
	var captureValue any
	if capture != "" {
		captureValue = capture
	}
	_, err := fixture.db.NewRaw(`
		INSERT INTO media_items (
			id, immich_asset_id, media_type, width, height, local_date_time, first_seen_at, last_seen_at
		) VALUES (?, ?, 'image', 1200, 800, ?, now(), now())
	`, mediaID, immichID, captureValue).Exec(ctx)
	require.NoError(t, err)
	for _, sourceName := range sourceNames {
		_, err := fixture.db.NewRaw(`
			INSERT INTO source_album_memberships (
				source_album_id, immich_asset_id, media_item_id, first_seen_at,
				last_seen_at, source_fingerprint
			) VALUES (?, ?, ?, now(), now(), decode(repeat('11', 32), 'hex'))
		`, fixture.sources[sourceName], immichID, mediaID).Exec(ctx)
		require.NoError(t, err)
	}
}

func TestDraftsCombineAndDivideSourcesWhileReusingStableMediaIdentities(t *testing.T) {
	fixture := newDraftFixture(t)
	ctx := context.Background()

	combined, err := fixture.service.CreateEvent(ctx, fixture.actor, CreateEventRequest{
		SourceAlbumIDs: []string{fixture.sources["first"].String(), fixture.sources["second"].String()},
		Timezone:       "America/Los_Angeles",
		Title:          "Combined family days",
	})
	require.NoError(t, err)
	assert.Equal(t, "draft", combined.Lifecycle)
	assert.Equal(t, "Combined family days", combined.Title)
	require.Len(t, combined.Sources, 2)
	assert.Equal(t, fixture.sources["first"].String(), combined.Sources[0].ID)
	assert.Equal(t, fixture.sources["second"].String(), combined.Sources[1].ID)
	encoded, err := json.Marshal(combined)
	require.NoError(t, err)
	for _, immichID := range fixture.immich {
		assert.NotContains(t, string(encoded), immichID.String(), "draft responses must not expose Immich provenance IDs")
	}
	require.Len(t, combined.Moments, 2)
	assert.Equal(t, "2026-05-01", combined.Moments[0].ProposedDay)
	assert.Equal(t, "2026-05-02", combined.Moments[1].ProposedDay)
	assert.Equal(t, "America/Los_Angeles", combined.Moments[0].GroupingTimezone)
	assert.Equal(t, []string{fixture.media["first_only"].String()}, mediaIDs(combined.Moments[0].MediaItems))
	assert.ElementsMatch(t, []string{fixture.media["shared"].String(), fixture.media["second_only"].String()}, mediaIDs(combined.Moments[1].MediaItems))
	assert.Equal(t, []string{fixture.media["unknown"].String()}, mediaIDs(combined.UnassignedMedia))

	divided, err := fixture.service.CreateEvent(ctx, fixture.actor, CreateEventRequest{
		SourceAlbumIDs: []string{fixture.sources["first"].String()},
		MediaItemIDs:   []string{fixture.media["shared"].String(), fixture.media["first_only"].String()},
		Timezone:       "America/Los_Angeles",
	})
	require.NoError(t, err)
	assert.NotEqual(t, combined.ID, divided.ID)
	assert.Equal(t, "first source", divided.Title, "single-Source metadata initializes portal-owned presentation")
	require.Len(t, divided.Moments, 2)
	assert.ElementsMatch(t, []string{fixture.media["shared"].String(), fixture.media["first_only"].String()}, allEventMediaIDs(divided))
	assert.Empty(t, divided.UnassignedMedia, "unselected Source Media remains unpublished")

	stableCombined, err := fixture.service.GetEvent(ctx, uuid.MustParse(combined.ID))
	require.NoError(t, err)
	stableDivided, err := fixture.service.GetEvent(ctx, uuid.MustParse(divided.ID))
	require.NoError(t, err)
	assert.Equal(t, combined.ID, stableCombined.ID)
	assert.Equal(t, divided.ID, stableDivided.ID)
	assert.Contains(t, allEventMediaIDs(stableCombined), fixture.media["shared"].String())
	assert.Contains(t, allEventMediaIDs(stableDivided), fixture.media["shared"].String())

	var eventPlacements, mediaIdentities int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM draft_media_placements`).Scan(ctx, &eventPlacements))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM media_items`).Scan(ctx, &mediaIdentities))
	assert.Equal(t, 6, eventPlacements)
	assert.Equal(t, 4, mediaIdentities, "drafting reuses reconciled Media identities instead of copying Source assets")

	var dispositions []string
	require.NoError(t, fixture.db.NewRaw(`
		SELECT disposition FROM source_albums WHERE id IN (?, ?) ORDER BY id
	`, fixture.sources["first"], fixture.sources["second"]).Scan(ctx, &dispositions))
	assert.Equal(t, []string{"drafted", "drafted"}, dispositions)
}

func TestEventProposalOrderingUsesTheRequestedTimezoneForUnzonedTimestamps(t *testing.T) {
	fixture := newDraftFixture(t)
	ctx := context.Background()
	fixture.media["zoned_order"] = uuid.New()
	fixture.media["unzoned_order"] = uuid.New()
	fixture.addMedia(t, "zoned_order", "2026-05-02T04:00:00Z", "first")
	fixture.addMedia(t, "unzoned_order", "2026-05-02T01:00:00", "first")
	selected := []string{fixture.media["zoned_order"].String(), fixture.media["unzoned_order"].String()}

	utc, err := fixture.service.CreateEvent(ctx, fixture.actor, CreateEventRequest{
		SourceAlbumIDs: []string{fixture.sources["first"].String()}, MediaItemIDs: selected, Timezone: "UTC",
	})
	require.NoError(t, err)
	require.Len(t, utc.Moments, 1)
	assert.Equal(t, []string{fixture.media["unzoned_order"].String(), fixture.media["zoned_order"].String()}, mediaIDs(utc.Moments[0].MediaItems))

	losAngeles, err := fixture.service.CreateEvent(ctx, fixture.actor, CreateEventRequest{
		SourceAlbumIDs: []string{fixture.sources["first"].String()}, MediaItemIDs: selected, Timezone: "America/Los_Angeles",
	})
	require.NoError(t, err)
	require.Len(t, losAngeles.Moments, 1)
	assert.Equal(t, []string{fixture.media["zoned_order"].String(), fixture.media["unzoned_order"].String()}, mediaIDs(losAngeles.Moments[0].MediaItems))
}

func TestConcurrentEventReadsDoNotStarveTheConnectionPool(t *testing.T) {
	fixture := newDraftFixture(t)
	ctx := context.Background()
	event, err := fixture.service.CreateEvent(ctx, fixture.actor, CreateEventRequest{
		SourceAlbumIDs: []string{fixture.sources["first"].String(), fixture.sources["second"].String()},
		Timezone:       "UTC",
	})
	require.NoError(t, err)
	fixture.db.SetMaxOpenConns(2)
	start := make(chan struct{})
	results := make(chan error, 4)
	for range 4 {
		go func() {
			<-start
			_, err := fixture.service.GetEvent(ctx, uuid.MustParse(event.ID))
			results <- err
		}()
	}
	close(start)
	for range 4 {
		select {
		case err := <-results:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent Event reads starved the database connection pool")
		}
	}
}

func TestEventReadsLoadAllMomentMediaWithOnePlacementQuery(t *testing.T) {
	fixture := newDraftFixture(t)
	ctx := context.Background()
	event, err := fixture.service.CreateEvent(ctx, fixture.actor, CreateEventRequest{
		SourceAlbumIDs: []string{fixture.sources["first"].String(), fixture.sources["second"].String()},
		Timezone:       "UTC",
	})
	require.NoError(t, err)

	counter := new(draftQueryCounter)
	fixture.db.AddQueryHook(counter)
	loaded, err := fixture.service.GetEvent(ctx, uuid.MustParse(event.ID))
	require.NoError(t, err)
	assert.Len(t, loaded.Moments, 2)
	assert.LessOrEqual(t, counter.count.Load(), int64(4), "Event reads must not issue one query per Moment")
}

func TestSourceMediaListsOnlySelectableStableMediaIdentities(t *testing.T) {
	fixture := newDraftFixture(t)
	ctx := context.Background()
	first, err := fixture.service.SourceMedia(ctx, fixture.sources["first"])
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		fixture.media["shared"].String(), fixture.media["first_only"].String(), fixture.media["unknown"].String(),
	}, mediaIDs(first.MediaItems))
	assert.NotContains(t, mediaIDs(first.MediaItems), fixture.media["second_only"].String())

	second, err := fixture.service.SourceMedia(ctx, fixture.sources["second"])
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		fixture.media["shared"].String(), fixture.media["second_only"].String(),
	}, mediaIDs(second.MediaItems))

	_, err = fixture.service.SourceMedia(ctx, fixture.sources["ignored"])
	require.ErrorIs(t, err, ErrSourceUnavailable)
}

func TestRecipientSessionsCannotSeeDraftRoutesBeforePublication(t *testing.T) {
	fixture := newDraftFixture(t)
	ctx := context.Background()
	event, err := fixture.service.CreateEvent(ctx, fixture.actor, CreateEventRequest{
		SourceAlbumIDs: []string{fixture.sources["first"].String()},
		MediaItemIDs:   []string{fixture.media["first_only"].String()},
		Timezone:       "UTC",
	})
	require.NoError(t, err)
	looseItem, _, err := fixture.service.CreateLooseItem(ctx, fixture.actor, CreateLooseItemRequest{
		MediaItemID: fixture.media["unknown"].String(), Timezone: "UTC",
	})
	require.NoError(t, err)

	setupService := setup.New(fixture.db, nil, config.SecurityConfig{Secret: "recipient-visibility-test-secret"})
	session, err := setupService.Session(ctx, fixture.credential)
	require.NoError(t, err)
	e := draftHTTP(fixture.service, setupService)
	for _, test := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/events/" + event.ID, ""},
		{http.MethodPost, "/api/events", `{}`},
		{http.MethodGet, "/api/loose-items/" + looseItem.ID, ""},
		{http.MethodPost, "/api/loose-items", `{}`},
		{http.MethodGet, "/api/sources/" + fixture.sources["first"].String() + "/media-items", ""},
	} {
		request := httptest.NewRequestWithContext(ctx, test.method, test.path, strings.NewReader(test.body))
		request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: fixture.credential})
		request.Header.Set(setup.CSRFHeader, session.CSRFToken)
		if test.body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		e.ServeHTTP(response, request)
		assert.Equal(t, http.StatusNotFound, response.Code, "%s %s", test.method, test.path)
		assert.NotContains(t, response.Body.String(), event.ID)
		assert.NotContains(t, response.Body.String(), looseItem.ID)
	}
}

func TestDraftRoutesRecordRequestAttributionAndReportLooseItemCreationStatus(t *testing.T) {
	fixture := newDraftFixture(t)
	ctx := context.Background()
	_, err := fixture.db.NewRaw(`INSERT INTO person_roles (person_id, role) VALUES (?, 'curator')`, fixture.actor.PersonID).Exec(ctx)
	require.NoError(t, err)
	setupService := setup.New(fixture.db, nil, config.SecurityConfig{Secret: "draft-route-audit-test-secret"})
	session, err := setupService.Session(ctx, fixture.credential)
	require.NoError(t, err)
	e := draftHTTP(fixture.service, setupService)
	post := func(path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(body))
		request.RemoteAddr = "203.0.113.10:1234"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("User-Agent", "memento-integration-test")
		request.Header.Set(setup.CSRFHeader, session.CSRFToken)
		request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: fixture.credential})
		response := httptest.NewRecorder()
		e.ServeHTTP(response, request)
		return response
	}

	get := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil)
		request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: fixture.credential})
		response := httptest.NewRecorder()
		e.ServeHTTP(response, request)
		return response
	}
	eventResponse := post("/api/events", fmt.Sprintf(
		`{"source_album_ids":[%q],"media_item_ids":[%q],"timezone":"UTC"}`,
		fixture.sources["first"], fixture.media["first_only"],
	))
	require.Equal(t, http.StatusCreated, eventResponse.Code)
	var createdEvent Event
	require.NoError(t, json.Unmarshal(eventResponse.Body.Bytes(), &createdEvent))
	assert.NotEmpty(t, createdEvent.ID)
	require.Len(t, createdEvent.Moments, 1)
	require.Len(t, createdEvent.Moments[0].MediaItems, 1)
	assert.Equal(t, fixture.media["first_only"].String(), createdEvent.Moments[0].MediaItems[0].ID)
	eventGet := get("/api/events/" + createdEvent.ID)
	require.Equal(t, http.StatusOK, eventGet.Code)
	var retrievedEvent Event
	require.NoError(t, json.Unmarshal(eventGet.Body.Bytes(), &retrievedEvent))
	assert.Equal(t, createdEvent.ID, retrievedEvent.ID)
	assert.Equal(t, allEventMediaIDs(createdEvent), allEventMediaIDs(retrievedEvent))

	looseBody := fmt.Sprintf(`{"media_item_id":%q,"timezone":"UTC"}`, fixture.media["unknown"])
	looseResponse := post("/api/loose-items", looseBody)
	require.Equal(t, http.StatusCreated, looseResponse.Code)
	var createdLoose LooseItem
	require.NoError(t, json.Unmarshal(looseResponse.Body.Bytes(), &createdLoose))
	assert.NotEmpty(t, createdLoose.ID)
	assert.Equal(t, fixture.media["unknown"].String(), createdLoose.MediaItem.ID)
	looseGet := get("/api/loose-items/" + createdLoose.ID)
	require.Equal(t, http.StatusOK, looseGet.Code)
	var retrievedLoose LooseItem
	require.NoError(t, json.Unmarshal(looseGet.Body.Bytes(), &retrievedLoose))
	assert.Equal(t, createdLoose.ID, retrievedLoose.ID)
	assert.Equal(t, createdLoose.MediaItem.ID, retrievedLoose.MediaItem.ID)
	assert.Equal(t, http.StatusOK, post("/api/loose-items", looseBody).Code)

	var audits []struct {
		ClientIP  string `bun:"client_ip"`
		UserAgent string `bun:"user_agent"`
	}
	require.NoError(t, fixture.db.NewRaw(`
		SELECT host(client_ip) AS client_ip, user_agent FROM security_audit_events
		WHERE action IN ('event_draft_created', 'loose_item_draft_created') ORDER BY action
	`).Scan(ctx, &audits))
	require.Len(t, audits, 2)
	for _, audit := range audits {
		assert.Equal(t, "203.0.113.10", audit.ClientIP)
		assert.Equal(t, "memento-integration-test", audit.UserAgent)
	}
}

func TestDraftRoutesMapSemanticServiceErrors(t *testing.T) {
	fixture := newDraftFixture(t)
	ctx := context.Background()
	_, err := fixture.db.NewRaw(`INSERT INTO person_roles (person_id, role) VALUES (?, 'curator')`, fixture.actor.PersonID).Exec(ctx)
	require.NoError(t, err)
	setupService := setup.New(fixture.db, nil, config.SecurityConfig{Secret: "draft-route-errors-test-secret"})
	session, err := setupService.Session(ctx, fixture.credential)
	require.NoError(t, err)
	e := draftHTTP(fixture.service, setupService)
	for _, test := range []struct {
		method  string
		path    string
		body    string
		status  int
		code    string
		message string
	}{
		{http.MethodGet, "/api/events/" + uuid.NewString(), "", http.StatusNotFound, `"code":"not_found"`, "Event not found"},
		{http.MethodGet, "/api/sources/" + fixture.sources["ignored"].String() + "/media-items", "", http.StatusConflict, `"code":"conflict"`, "available and not ignored"},
		{http.MethodPost, "/api/events", fmt.Sprintf(`{"source_album_ids":[%q],"timezone":"UTC"}`, fixture.sources["empty"]), http.StatusConflict, `"code":"conflict"`, "Select at least one available Media item"},
		{http.MethodPost, "/api/events", fmt.Sprintf(`{"source_album_ids":[%q],"timezone":"Mars/Olympus"}`, fixture.sources["first"]), http.StatusUnprocessableEntity, `"code":"validation_error"`, "Draft fields must be valid"},
		{http.MethodPost, "/api/loose-items", fmt.Sprintf(`{"media_item_id":%q,"timezone":"UTC"}`, uuid.New()), http.StatusConflict, `"code":"conflict"`, "selected Media item is unavailable"},
	} {
		request := httptest.NewRequestWithContext(ctx, test.method, test.path, strings.NewReader(test.body))
		request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: fixture.credential})
		request.Header.Set(setup.CSRFHeader, session.CSRFToken)
		if test.body != "" {
			request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		}
		response := httptest.NewRecorder()
		e.ServeHTTP(response, request)
		assert.Equal(t, test.status, response.Code, "%s %s: %s", test.method, test.path, response.Body.String())
		assert.Contains(t, response.Body.String(), test.code)
		assert.Contains(t, response.Body.String(), test.message)
	}
}

func TestEventMetadataRemainsPortalOwnedWhileSourceChangesBecomeSuggestions(t *testing.T) {
	fixture := newDraftFixture(t)
	ctx := context.Background()
	event, err := fixture.service.CreateEvent(ctx, fixture.actor, CreateEventRequest{
		SourceAlbumIDs: []string{fixture.sources["first"].String()},
		MediaItemIDs:   []string{fixture.media["first_only"].String()},
		Timezone:       "UTC",
	})
	require.NoError(t, err)
	require.Len(t, event.Sources, 1)
	assert.Nil(t, event.Sources[0].MetadataSuggestion)

	_, err = fixture.db.NewRaw(`UPDATE source_albums SET name = 'Later Immich title' WHERE id = ?`, fixture.sources["first"]).Exec(ctx)
	require.NoError(t, err)
	nameChanged, err := fixture.service.GetEvent(ctx, uuid.MustParse(event.ID))
	require.NoError(t, err)
	assert.Equal(t, "first source", nameChanged.Title)
	assert.Equal(t, "first description", nameChanged.Description)
	require.NotNil(t, nameChanged.Sources[0].MetadataSuggestion)
	assert.Equal(t, "Later Immich title", nameChanged.Sources[0].MetadataSuggestion.Name)
	assert.Equal(t, "first description", nameChanged.Sources[0].MetadataSuggestion.Description)

	_, err = fixture.db.NewRaw(`
		UPDATE source_albums SET name = 'first source', description = 'Later Immich description' WHERE id = ?
	`, fixture.sources["first"]).Exec(ctx)
	require.NoError(t, err)
	descriptionChanged, err := fixture.service.GetEvent(ctx, uuid.MustParse(event.ID))
	require.NoError(t, err)
	assert.Equal(t, "first source", descriptionChanged.Title)
	assert.Equal(t, "first description", descriptionChanged.Description)
	require.NotNil(t, descriptionChanged.Sources[0].MetadataSuggestion)
	assert.Equal(t, "first source", descriptionChanged.Sources[0].MetadataSuggestion.Name)
	assert.Equal(t, "Later Immich description", descriptionChanged.Sources[0].MetadataSuggestion.Description)
}

func TestLooseItemsReuseMediaIdentityAndKeepUnknownDatesUnassigned(t *testing.T) {
	fixture := newDraftFixture(t)
	ctx := context.Background()
	created, inserted, err := fixture.service.CreateLooseItem(ctx, fixture.actor, CreateLooseItemRequest{
		MediaItemID: fixture.media["unknown"].String(), Timezone: "Pacific/Auckland", Title: "A loose photo",
	})
	require.NoError(t, err)
	assert.True(t, inserted)
	assert.Equal(t, fixture.media["unknown"].String(), created.MediaItem.ID)
	assert.Nil(t, created.ProposedDay)
	assert.Equal(t, "draft", created.Lifecycle)

	retried, inserted, err := fixture.service.CreateLooseItem(ctx, fixture.actor, CreateLooseItemRequest{
		MediaItemID: fixture.media["unknown"].String(), Timezone: "UTC", Title: "Retry must not overwrite",
	})
	require.NoError(t, err)
	assert.False(t, inserted)
	assert.Equal(t, created.ID, retried.ID)
	assert.Equal(t, "A loose photo", retried.Title)
	assert.Equal(t, "Pacific/Auckland", retried.GroupingTimezone)

	var looseRows, auditRows int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM loose_items`).Scan(ctx, &looseRows))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM security_audit_events WHERE action = 'loose_item_draft_created'`).Scan(ctx, &auditRows))
	assert.Equal(t, 1, looseRows)
	assert.Equal(t, 1, auditRows)
}

func TestConcurrentLooseItemCreationReturnsOneStableIdentity(t *testing.T) {
	fixture := newDraftFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type result struct {
		item     LooseItem
		inserted bool
		err      error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			item, inserted, err := fixture.service.CreateLooseItem(ctx, fixture.actor, CreateLooseItemRequest{
				MediaItemID: fixture.media["first_only"].String(), Timezone: "UTC",
			})
			results <- result{item: item, inserted: inserted, err: err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	require.NoError(t, first.err)
	require.NoError(t, second.err)
	assert.Equal(t, first.item.ID, second.item.ID)
	assert.NotEqual(t, first.inserted, second.inserted)

	var looseRows, auditRows int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM loose_items`).Scan(ctx, &looseRows))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM security_audit_events WHERE action = 'loose_item_draft_created'`).Scan(ctx, &auditRows))
	assert.Equal(t, 1, looseRows)
	assert.Equal(t, 1, auditRows)
}

func TestDraftMetadataLimitsCountUnicodeCharacters(t *testing.T) {
	fixture := newDraftFixture(t)
	ctx := context.Background()
	title := strings.Repeat("🎉", 240)
	description := strings.Repeat("家", 2000)
	event, err := fixture.service.CreateEvent(ctx, fixture.actor, CreateEventRequest{
		SourceAlbumIDs: []string{fixture.sources["first"].String()},
		MediaItemIDs:   []string{fixture.media["first_only"].String()},
		Timezone:       "UTC",
		Title:          title,
		Description:    description,
	})
	require.NoError(t, err)
	assert.Equal(t, title, event.Title)
	assert.Equal(t, description, event.Description)

	looseItem, inserted, err := fixture.service.CreateLooseItem(ctx, fixture.actor, CreateLooseItemRequest{
		MediaItemID: fixture.media["unknown"].String(), Timezone: "UTC", Title: title, Description: description,
	})
	require.NoError(t, err)
	assert.True(t, inserted)
	assert.Equal(t, title, looseItem.Title)
	assert.Equal(t, description, looseItem.Description)
}

func TestDraftingTreatsPersistedYearZeroCaptureDatesAsUnknown(t *testing.T) {
	fixture := newDraftFixture(t)
	ctx := context.Background()
	_, err := fixture.db.NewRaw(`UPDATE media_items SET local_date_time = '0000-01-01T00:00:00Z' WHERE id = ?`, fixture.media["first_only"]).Exec(ctx)
	require.NoError(t, err)
	event, err := fixture.service.CreateEvent(ctx, fixture.actor, CreateEventRequest{
		SourceAlbumIDs: []string{fixture.sources["first"].String()},
		MediaItemIDs:   []string{fixture.media["first_only"].String()},
		Timezone:       "UTC",
	})
	require.NoError(t, err)
	assert.Empty(t, event.Moments)
	assert.Equal(t, []string{fixture.media["first_only"].String()}, mediaIDs(event.UnassignedMedia))

	loose, _, err := fixture.service.CreateLooseItem(ctx, fixture.actor, CreateLooseItemRequest{
		MediaItemID: fixture.media["first_only"].String(), Timezone: "UTC",
	})
	require.NoError(t, err)
	assert.Nil(t, loose.ProposedDay)
}

func TestInvalidDraftInputsRollBackWithoutCreatingPrivateState(t *testing.T) {
	fixture := newDraftFixture(t)
	ctx := context.Background()
	tests := []struct {
		name    string
		request CreateEventRequest
		target  error
	}{
		{"invalid timezone", CreateEventRequest{SourceAlbumIDs: []string{fixture.sources["first"].String()}, Timezone: "Mars/Olympus"}, ErrInvalid},
		{"ignored Source", CreateEventRequest{SourceAlbumIDs: []string{fixture.sources["ignored"].String()}, Timezone: "UTC"}, ErrSourceUnavailable},
		{"Media outside selection", CreateEventRequest{SourceAlbumIDs: []string{fixture.sources["first"].String()}, MediaItemIDs: []string{fixture.media["second_only"].String()}, Timezone: "UTC"}, ErrMediaUnavailable},
		{"missing Source", CreateEventRequest{SourceAlbumIDs: []string{uuid.NewString()}, Timezone: "UTC"}, ErrSourceUnavailable},
		{"duplicate Source", CreateEventRequest{SourceAlbumIDs: []string{fixture.sources["first"].String(), fixture.sources["first"].String()}, Timezone: "UTC"}, ErrInvalid},
		{"Source without Media", CreateEventRequest{SourceAlbumIDs: []string{fixture.sources["empty"].String()}, Timezone: "UTC"}, ErrNoMediaAvailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := fixture.service.CreateEvent(ctx, fixture.actor, test.request)
			require.ErrorIs(t, err, test.target)
		})
	}
	var events, moments, placements, audits int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM events`).Scan(ctx, &events))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM draft_moments`).Scan(ctx, &moments))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM draft_media_placements`).Scan(ctx, &placements))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM security_audit_events WHERE action = 'event_draft_created'`).Scan(ctx, &audits))
	assert.Zero(t, events)
	assert.Zero(t, moments)
	assert.Zero(t, placements)
	assert.Zero(t, audits)
}

func TestDraftAuditFailuresRollBackEventAndLooseItemCreation(t *testing.T) {
	for _, kind := range []string{"event", "loose_item"} {
		t.Run(kind, func(t *testing.T) {
			fixture := newDraftFixture(t)
			ctx := context.Background()
			_, err := fixture.db.ExecContext(ctx, `
				CREATE FUNCTION reject_draft_audit() RETURNS trigger LANGUAGE plpgsql AS $$
				BEGIN
					IF NEW.action IN ('event_draft_created', 'loose_item_draft_created') THEN
						RAISE EXCEPTION 'rejected draft audit';
					END IF;
					RETURN NEW;
				END $$;
				CREATE TRIGGER reject_draft_audit BEFORE INSERT ON security_audit_events
				FOR EACH ROW EXECUTE FUNCTION reject_draft_audit()`)
			require.NoError(t, err)

			if kind == "event" {
				_, err = fixture.service.CreateEvent(ctx, fixture.actor, CreateEventRequest{
					SourceAlbumIDs: []string{fixture.sources["first"].String()},
					MediaItemIDs:   []string{fixture.media["first_only"].String()},
					Timezone:       "UTC",
				})
			} else {
				_, _, err = fixture.service.CreateLooseItem(ctx, fixture.actor, CreateLooseItemRequest{
					MediaItemID: fixture.media["first_only"].String(), Timezone: "UTC",
				})
			}
			require.ErrorContains(t, err, "rejected draft audit")

			var events, looseItems int
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM events`).Scan(ctx, &events))
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM loose_items`).Scan(ctx, &looseItems))
			assert.Zero(t, events)
			assert.Zero(t, looseItems)
			var disposition string
			require.NoError(t, fixture.db.NewRaw(`SELECT disposition FROM source_albums WHERE id = ?`, fixture.sources["first"]).Scan(ctx, &disposition))
			assert.Equal(t, "unreviewed", disposition)
		})
	}
}

func TestDraftLookupsAndLooseItemValidationFailClosed(t *testing.T) {
	fixture := newDraftFixture(t)
	ctx := context.Background()
	_, err := fixture.service.GetEvent(ctx, uuid.New())
	require.ErrorIs(t, err, ErrNotFound)
	_, err = fixture.service.GetLooseItem(ctx, uuid.New())
	require.ErrorIs(t, err, ErrNotFound)
	_, err = fixture.service.SourceMedia(ctx, uuid.New())
	require.ErrorIs(t, err, ErrNotFound)

	for _, request := range []CreateLooseItemRequest{
		{MediaItemID: "not-an-id", Timezone: "UTC"},
		{MediaItemID: fixture.media["first_only"].String(), Timezone: "Mars/Olympus"},
		{MediaItemID: uuid.NewString(), Timezone: "UTC"},
	} {
		_, _, err := fixture.service.CreateLooseItem(ctx, fixture.actor, request)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalid) || errors.Is(err, ErrMediaUnavailable))
	}
	var count int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM loose_items`).Scan(ctx, &count))
	assert.Zero(t, count)
}

func mediaIDs(media []MediaItem) []string {
	result := make([]string, 0, len(media))
	for _, item := range media {
		result = append(result, item.ID)
	}
	return result
}

func allEventMediaIDs(event Event) []string {
	result := mediaIDs(event.UnassignedMedia)
	for _, moment := range event.Moments {
		result = append(result, mediaIDs(moment.MediaItems)...)
	}
	return result
}
