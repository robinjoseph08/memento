//go:build integration

package events

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	searchdomain "github.com/robinjoseph08/memento/pkg/search"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchUsesOnlyAuthorizedCurrentPublicationAndDiscoverableAttendance(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	sessionID := uuid.New()
	credentialRaw := bytes.Repeat([]byte{0x73}, 32)
	credential := hex.EncodeToString(credentialRaw)
	credentialHash := sha256.Sum256(credentialRaw)
	attendeeID, hiddenAttendeeID, circleID := uuid.New(), uuid.New(), uuid.New()
	_, err := fixture.db.NewRaw(`
		UPDATE events SET title = 'Café Reunion', description = 'A summer gathering',
		       place_labels = ARRAY['São Paulo'] WHERE id = ?;
		UPDATE draft_moments SET place_labels = ARRAY['Jardín Central'] WHERE id = ?;
		INSERT INTO people (id, display_name, sort_name) VALUES
			(?, 'José Alvarez', 'jose alvarez'), (?, 'Private Match', 'private match');
		INSERT INTO attendance (moment_id, person_id, source, confirmed_by_person_id, confirmed_at)
		VALUES (?, ?, 'manual', ?, now()), (?, ?, 'manual', ?, now());
		INSERT INTO visibility_circles (id, name) VALUES (?, 'Search circle');
		INSERT INTO visibility_circle_members (circle_id, person_id)
		VALUES (?, ?), (?, ?);
		INSERT INTO sessions (
			id, credential_hash, person_id, recipient_access_generation_id,
			security_epoch, session_type, idle_expires_at
		) SELECT ?, ?, ?, ?, security_epoch, 'trusted', now() + interval '1 hour'
		  FROM system_settings WHERE id = 1
	`, fixture.event, fixture.moments[0], attendeeID, hiddenAttendeeID,
		fixture.moments[0], attendeeID, fixture.actor.PersonID,
		fixture.moments[0], hiddenAttendeeID, fixture.actor.PersonID,
		circleID, circleID, fixture.people["shared"], circleID, attendeeID,
		sessionID, credentialHash[:], fixture.people["shared"], fixture.access["shared"]).Exec(ctx)
	require.NoError(t, err)

	actor := setup.SessionActor{
		PersonID: fixture.people["shared"], AccessID: fixture.access["shared"], SessionID: sessionID,
	}
	service := searchdomain.New(fixture.db)

	unpublished, err := service.Search(ctx, actor, searchdomain.Request{Query: "cafe"})
	require.NoError(t, err)
	assert.Zero(t, unpublished.TotalEvents, "an unpublished draft has no search projection")

	_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)

	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	searchdomain.RegisterRoutes(e, searchdomain.NewHandler(service, setup.New(fixture.db, nil, config.SecurityConfig{})))
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/search", strings.NewReader(`{"query":"CAFÉ"}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: credential})
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "private, no-store", response.Header().Get(echo.HeaderCacheControl))
	assert.Empty(t, request.URL.RawQuery, "free text is not sent through the URL")

	for _, query := range []string{"CAFÉ", "reun", "reunoin", "sao", "jardin"} {
		result, searchErr := service.Search(ctx, actor, searchdomain.Request{Query: query})
		require.NoError(t, searchErr, query)
		assert.Equal(t, 1, result.TotalEvents, query)
		assert.Equal(t, 1, result.TotalPhotos, query)
		require.Len(t, result.Events, 1, query)
		assert.Equal(t, 1, result.Events[0].MediaCount, query)
		assert.Equal(t, "2026-07-27", *result.Events[0].DateStart, query)
		assert.Equal(t, "2026-07-27", *result.Events[0].DateEnd, query)
		assert.Len(t, result.Photos, 1, query)
	}

	duplicateEvent, duplicatePublication, duplicateMoment, duplicateSnapshot := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err = fixture.db.NewRaw(`
		INSERT INTO events (id, lifecycle, title, grouping_timezone, version, created_at, updated_at)
		VALUES (?, 'published', 'Duplicate summer', 'UTC', 1, now(), now());
		INSERT INTO publications (id, event_id, revision, editable_version, published_by_person_id, notify_recipients, committed_at)
		VALUES (?, ?, 1, 1, ?, false, now());
		UPDATE events SET current_publication_id = ? WHERE id = ?;
		INSERT INTO published_event_revisions (publication_id, event_id, title, description, grouping_timezone, created_at)
		VALUES (?, ?, 'Duplicate summer', '', 'UTC', now());
		INSERT INTO audience_snapshots (id, target_kind, target_id, approved_by_person_id, approved_at, label)
		VALUES (?, 'moment', ?, ?, now(), 'Shared');
		INSERT INTO published_moments (id, publication_id, draft_moment_id, audience_snapshot_id, position, title, proposed_day)
		VALUES (?, ?, ?, ?, 0, '', '2026-07-27');
		INSERT INTO published_media_placements (published_moment_id, media_item_id, position, media_type, width, height, local_date_time)
		SELECT ?, id, 0, media_type, width, height, local_date_time FROM media_items WHERE id = ?;
		INSERT INTO current_published_events (event_id, publication_id, title, description, grouping_timezone, committed_at)
		VALUES (?, ?, 'Duplicate summer', '', 'UTC', now());
		INSERT INTO current_published_placements (event_id, publication_id, published_moment_id, media_item_id, position)
		VALUES (?, ?, ?, ?, 0);
		INSERT INTO current_audience_entitlements (event_id, publication_id, recipient_person_id, recipient_access_generation_id, media_item_id)
		VALUES (?, ?, ?, ?, ?);
		INSERT INTO published_search_documents (event_id, publication_id, recipient_access_generation_id, media_item_id, search_text, capture_date)
		VALUES (?, ?, ?, ?, 'Summer duplicate', '2026-07-27')
	`, duplicateEvent, duplicatePublication, duplicateEvent, fixture.actor.PersonID,
		duplicatePublication, duplicateEvent, duplicatePublication, duplicateEvent,
		duplicateSnapshot, uuid.New(), fixture.actor.PersonID,
		duplicateMoment, duplicatePublication, uuid.New(), duplicateSnapshot,
		duplicateMoment, fixture.media[0], duplicateEvent, duplicatePublication,
		duplicateEvent, duplicatePublication, duplicateMoment, fixture.media[0],
		duplicateEvent, duplicatePublication, fixture.people["shared"], fixture.access["shared"], fixture.media[0],
		duplicateEvent, duplicatePublication, fixture.access["shared"], fixture.media[0]).Exec(ctx)
	require.NoError(t, err)
	deduplicated, err := service.Search(ctx, actor, searchdomain.Request{Query: "summer"})
	require.NoError(t, err)
	assert.Equal(t, 2, deduplicated.TotalEvents)
	assert.Equal(t, 1, deduplicated.TotalPhotos)
	assert.Len(t, deduplicated.Photos, 1)

	year := 2026
	month, exact, rangeStart, rangeEnd := "2026-07", "2026-07-27", "2026-07-27", "2026-07-27"
	for name, filter := range map[string]searchdomain.DateFilter{
		"year":  {Kind: "year", Year: &year},
		"month": {Kind: "month", Month: &month},
		"date":  {Kind: "date", Date: &exact},
		"range": {Kind: "range", StartDate: &rangeStart, EndDate: &rangeEnd},
	} {
		dateResult, dateErr := service.Search(ctx, actor, searchdomain.Request{Date: &filter})
		require.NoError(t, dateErr, name)
		assert.Equal(t, 1, dateResult.TotalPhotos, name)
	}
	hiddenDate := "2026-07-28"
	hiddenDateResult, err := service.Search(ctx, actor, searchdomain.Request{Date: &searchdomain.DateFilter{Kind: "date", Date: &hiddenDate}})
	require.NoError(t, err)
	assert.Zero(t, hiddenDateResult.TotalEvents)
	assert.Zero(t, hiddenDateResult.TotalPhotos)

	personResult, err := service.Search(ctx, actor, searchdomain.Request{Query: "jose"})
	require.NoError(t, err)
	require.Len(t, personResult.People, 1)
	assert.Equal(t, "José Alvarez", personResult.People[0].PersonName)
	assert.Equal(t, "Café Reunion", personResult.People[0].EventTitle)

	_, err = fixture.db.NewRaw(`
		UPDATE people SET display_name = 'SelfOnlyToken' WHERE id = ?;
		INSERT INTO published_attendance (published_moment_id, person_id)
		SELECT id, ? FROM published_moments
		WHERE publication_id = (SELECT current_publication_id FROM events WHERE id = ?)
		  AND draft_moment_id = ?
	`, actor.PersonID, actor.PersonID, fixture.event, fixture.moments[0]).Exec(ctx)
	require.NoError(t, err)
	selfResult, err := service.Search(ctx, actor, searchdomain.Request{Query: "SelfOnlyToken"})
	require.NoError(t, err)
	assert.Zero(t, selfResult.TotalEvents, "a Recipient's own Person is not discoverable")
	assert.Empty(t, selfResult.People)

	hiddenResult, err := service.Search(ctx, actor, searchdomain.Request{Query: "Private Match"})
	require.NoError(t, err)
	assert.Empty(t, hiddenResult.Events)
	assert.Empty(t, hiddenResult.Photos)
	assert.Empty(t, hiddenResult.People)

	_, err = fixture.db.NewRaw(`UPDATE events SET title = 'Staged Secret', version = 8 WHERE id = ?`, fixture.event).Exec(ctx)
	require.NoError(t, err)
	stagedResult, err := service.Search(ctx, actor, searchdomain.Request{Query: "staged"})
	require.NoError(t, err)
	assert.Zero(t, stagedResult.TotalEvents)

	_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: 8})
	require.NoError(t, err)
	oldResult, err := service.Search(ctx, actor, searchdomain.Request{Query: "cafe"})
	require.NoError(t, err)
	assert.Zero(t, oldResult.TotalEvents)
	newResult, err := service.Search(ctx, actor, searchdomain.Request{Query: "staged"})
	require.NoError(t, err)
	assert.Equal(t, 1, newResult.TotalEvents)

	_, err = fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
		TargetKind: WithdrawalTargetEvent, TargetID: fixture.event.String(), Reason: "Privacy correction",
	})
	require.NoError(t, err)
	withdrawnResult, err := service.Search(ctx, actor, searchdomain.Request{Query: "staged"})
	require.NoError(t, err)
	assert.Zero(t, withdrawnResult.TotalEvents)
	assert.Zero(t, withdrawnResult.TotalPhotos)
	assert.Empty(t, withdrawnResult.People)

}
