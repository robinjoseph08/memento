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
	attendeeID, hiddenAttendeeID, unauthorizedAttendeeID, circleID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err := fixture.db.NewRaw(`
		UPDATE events SET title = 'Café Reunion', description = 'A summer gathering',
		       place_labels = ARRAY['São Paulo'] WHERE id = ?;
		UPDATE draft_moments SET place_labels = ARRAY['Jardín Central'] WHERE id = ?;
		INSERT INTO people (id, display_name, sort_name) VALUES
			(?, 'José Alvarez', 'jose alvarez'),
			(?, 'Private Match', 'private match'),
			(?, 'Unauthorized Boundary', 'unauthorized boundary');
		INSERT INTO attendance (moment_id, person_id, source, confirmed_by_person_id, confirmed_at)
		VALUES (?, ?, 'manual', ?, now()),
		       (?, ?, 'manual', ?, now()),
		       (?, ?, 'manual', ?, now());
		INSERT INTO visibility_circles (id, name) VALUES (?, 'Search circle');
		INSERT INTO visibility_circle_members (circle_id, person_id)
		VALUES (?, ?), (?, ?), (?, ?);
		INSERT INTO sessions (
			id, credential_hash, person_id, recipient_access_generation_id,
			security_epoch, session_type, idle_expires_at
		) SELECT ?, ?, ?, ?, security_epoch, 'trusted', now() + interval '1 hour'
		  FROM system_settings WHERE id = 1
	`, fixture.event, fixture.moments[0], attendeeID, hiddenAttendeeID, unauthorizedAttendeeID,
		fixture.moments[0], attendeeID, fixture.actor.PersonID,
		fixture.moments[0], hiddenAttendeeID, fixture.actor.PersonID,
		fixture.moments[1], unauthorizedAttendeeID, fixture.actor.PersonID,
		circleID, circleID, fixture.people["shared"], circleID, attendeeID, circleID, unauthorizedAttendeeID,
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
	assertSafeEmptySearchResponse(t, hiddenDateResult, "an unauthorized Moment date must disclose no search observable")

	personResult, err := service.Search(ctx, actor, searchdomain.Request{Query: "jose"})
	require.NoError(t, err)
	require.Len(t, personResult.People, 1)
	assert.Equal(t, "José Alvarez", personResult.People[0].PersonName)
	assert.Equal(t, "Café Reunion", personResult.People[0].EventTitle)

	unauthorizedPersonResult, err := service.Search(ctx, actor, searchdomain.Request{Query: "Unauthorized Boundary"})
	require.NoError(t, err)
	assertSafeEmptySearchResponse(t, unauthorizedPersonResult,
		"a same-circle Person attending only an unauthorized Moment must disclose no search observable")

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
	assertSafeEmptySearchResponse(t, selfResult, "a Recipient's own Person must disclose no search observable")

	hiddenResult, err := service.Search(ctx, actor, searchdomain.Request{Query: "Private Match"})
	require.NoError(t, err)
	assertSafeEmptySearchResponse(t, hiddenResult, "a non-discoverable Person match must disclose no search observable")

	stagedAttendeeID := uuid.New()
	_, err = fixture.db.NewRaw(`
		UPDATE events
		SET title = 'Staged Secret', place_labels = ARRAY['Copper Harbor'], version = 8
		WHERE id = ?;
		UPDATE draft_moments SET place_labels = ARRAY['Willow Terrace'] WHERE id = ?;
		DELETE FROM attendance WHERE moment_id = ? AND person_id = ?;
		INSERT INTO people (id, display_name, sort_name)
		VALUES (?, 'Future Attendee', 'future attendee');
		INSERT INTO attendance (moment_id, person_id, source, confirmed_by_person_id, confirmed_at)
		VALUES (?, ?, 'manual', ?, now());
		INSERT INTO visibility_circle_members (circle_id, person_id) VALUES (?, ?)
	`, fixture.event, fixture.moments[0], fixture.moments[0], attendeeID,
		stagedAttendeeID, fixture.moments[0], stagedAttendeeID, fixture.actor.PersonID,
		circleID, stagedAttendeeID).Exec(ctx)
	require.NoError(t, err)

	stagedResult, err := service.Search(ctx, actor, searchdomain.Request{Query: "staged"})
	require.NoError(t, err)
	assertSafeEmptySearchResponse(t, stagedResult, "staged Event text must not replace the current Publication")
	stagedAttendanceResult, err := service.Search(ctx, actor, searchdomain.Request{Query: "Future Attendee"})
	require.NoError(t, err)
	assertSafeEmptySearchResponse(t, stagedAttendanceResult, "staged Attendance must not enter the current Publication")
	publishedAttendanceResult, err := service.Search(ctx, actor, searchdomain.Request{Query: "jose"})
	require.NoError(t, err)
	require.Len(t, publishedAttendanceResult.People, 1)
	assert.Equal(t, "José Alvarez", publishedAttendanceResult.People[0].PersonName,
		"removing live Attendance must not alter the current Publication")
	for _, query := range []string{"sao", "jardin"} {
		publishedPlaceResult, searchErr := service.Search(ctx, actor, searchdomain.Request{Query: query})
		require.NoError(t, searchErr, query)
		assert.Equal(t, 1, publishedPlaceResult.TotalEvents, query)
		assert.Equal(t, 1, publishedPlaceResult.TotalPhotos, query)
	}
	for _, query := range []string{"copper", "willow"} {
		stagedPlaceResult, searchErr := service.Search(ctx, actor, searchdomain.Request{Query: query})
		require.NoError(t, searchErr, query)
		assertSafeEmptySearchResponse(t, stagedPlaceResult, "staged Place labels must not enter the current Publication: "+query)
	}

	_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: 8})
	require.NoError(t, err)
	for _, query := range []string{"cafe", "jose", "sao", "jardin"} {
		replacedResult, searchErr := service.Search(ctx, actor, searchdomain.Request{Query: query})
		require.NoError(t, searchErr, query)
		assertSafeEmptySearchResponse(t, replacedResult, "a replacement Publication must remove stale projection text: "+query)
	}
	newResult, err := service.Search(ctx, actor, searchdomain.Request{Query: "staged"})
	require.NoError(t, err)
	assert.Equal(t, 1, newResult.TotalEvents)
	newAttendanceResult, err := service.Search(ctx, actor, searchdomain.Request{Query: "Future Attendee"})
	require.NoError(t, err)
	assert.Equal(t, 1, newAttendanceResult.TotalEvents)
	assert.Equal(t, 1, newAttendanceResult.TotalPhotos)
	require.Len(t, newAttendanceResult.People, 1)
	assert.Equal(t, "Future Attendee", newAttendanceResult.People[0].PersonName)
	for _, query := range []string{"copper", "willow"} {
		newPlaceResult, searchErr := service.Search(ctx, actor, searchdomain.Request{Query: query})
		require.NoError(t, searchErr, query)
		assert.Equal(t, 1, newPlaceResult.TotalEvents, query)
		assert.Equal(t, 1, newPlaceResult.TotalPhotos, query)
	}

	_, err = fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
		TargetKind: WithdrawalTargetEvent, TargetID: fixture.event.String(), Reason: "Privacy correction",
	})
	require.NoError(t, err)
	withdrawnResult, err := service.Search(ctx, actor, searchdomain.Request{Query: "staged"})
	require.NoError(t, err)
	assertSafeEmptySearchResponse(t, withdrawnResult, "Withdrawal must remove every search observable")
}

func TestSearchDateFiltersEnforceInclusiveUpperAndLowerBounds(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)
	actor := createSearchSession(t, fixture)
	service := searchdomain.New(fixture.db)

	var publicationID, publishedMomentID uuid.UUID
	err = fixture.db.NewRaw(`
		SELECT event.current_publication_id, moment.id
		FROM events AS event
		JOIN published_moments AS moment ON moment.publication_id = event.current_publication_id
		WHERE event.id = ? AND moment.draft_moment_id = ?
	`, fixture.event, fixture.moments[0]).Scan(ctx, &publicationID, &publishedMomentID)
	require.NoError(t, err)

	mediaByBoundary := map[string]uuid.UUID{"existing": fixture.media[0]}
	for position, boundary := range []struct {
		name string
		date string
	}{
		{name: "before year", date: "2025-12-31"},
		{name: "year start", date: "2026-01-01"},
		{name: "before month", date: "2026-06-30"},
		{name: "month start", date: "2026-07-01"},
		{name: "before range", date: "2026-07-19"},
		{name: "range start", date: "2026-07-20"},
		{name: "range end", date: "2026-07-29"},
		{name: "after range", date: "2026-07-30"},
		{name: "month end", date: "2026-07-31"},
		{name: "after month", date: "2026-08-01"},
		{name: "year end", date: "2026-12-31"},
		{name: "after year", date: "2027-01-01"},
	} {
		mediaID := uuid.New()
		mediaByBoundary[boundary.name] = mediaID
		_, err = fixture.db.NewRaw(`
			INSERT INTO media_items (
				id, immich_asset_id, media_type, width, height, local_date_time,
				first_seen_at, last_seen_at
			) VALUES (?, ?, 'image', 1200, 800, ?::date + time '10:00:00', now(), now());
			INSERT INTO published_media_placements (
				published_moment_id, media_item_id, position, media_type, width, height, local_date_time
			) VALUES (?, ?, ?, 'image', 1200, 800, ?::date + time '10:00:00');
			INSERT INTO current_published_placements (
				event_id, publication_id, published_moment_id, media_item_id, position
			) VALUES (?, ?, ?, ?, ?);
			INSERT INTO current_audience_entitlements (
				event_id, publication_id, recipient_person_id, recipient_access_generation_id, media_item_id
			) VALUES (?, ?, ?, ?, ?);
			INSERT INTO published_search_documents (
				event_id, publication_id, recipient_access_generation_id, media_item_id, search_text, capture_date
			) VALUES (?, ?, ?, ?, 'date boundary', ?::date)
		`, mediaID, uuid.New(), boundary.date,
			publishedMomentID, mediaID, position+10, boundary.date,
			fixture.event, publicationID, publishedMomentID, mediaID, position+10,
			fixture.event, publicationID, actor.PersonID, actor.AccessID, mediaID,
			fixture.event, publicationID, actor.AccessID, mediaID, boundary.date).Exec(ctx)
		require.NoError(t, err, boundary.name)
	}

	boundaryIDs := func(names ...string) []string {
		ids := make([]string, 0, len(names))
		for _, name := range names {
			ids = append(ids, mediaByBoundary[name].String())
		}
		return ids
	}
	year := 2026
	month, rangeStart, rangeEnd := "2026-07", "2026-07-20", "2026-07-29"
	for _, test := range []struct {
		name     string
		filter   searchdomain.DateFilter
		expected []string
		start    string
		end      string
	}{
		{
			name: "year", filter: searchdomain.DateFilter{Kind: "year", Year: &year},
			expected: boundaryIDs("year start", "before month", "month start", "before range", "range start", "existing", "range end", "after range", "month end", "after month", "year end"),
			start:    "2026-01-01", end: "2026-12-31",
		},
		{
			name: "month", filter: searchdomain.DateFilter{Kind: "month", Month: &month},
			expected: boundaryIDs("month start", "before range", "range start", "existing", "range end", "after range", "month end"),
			start:    "2026-07-01", end: "2026-07-31",
		},
		{
			name: "range", filter: searchdomain.DateFilter{Kind: "range", StartDate: &rangeStart, EndDate: &rangeEnd},
			expected: boundaryIDs("range start", "existing", "range end"),
			start:    "2026-07-20", end: "2026-07-29",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, searchErr := service.Search(ctx, actor, searchdomain.Request{Date: &test.filter})
			require.NoError(t, searchErr)
			assert.Equal(t, 1, result.TotalEvents)
			assert.Equal(t, len(test.expected), result.TotalPhotos)
			assert.False(t, result.HasMore)
			require.Len(t, result.Events, 1)
			assert.Equal(t, fixture.event.String(), result.Events[0].ID)
			assert.Equal(t, len(test.expected), result.Events[0].MediaCount)
			require.NotNil(t, result.Events[0].DateStart)
			require.NotNil(t, result.Events[0].DateEnd)
			assert.Equal(t, test.start, *result.Events[0].DateStart)
			assert.Equal(t, test.end, *result.Events[0].DateEnd)
			require.Len(t, result.Photos, len(test.expected))
			actualIDs := make([]string, 0, len(result.Photos))
			for _, photo := range result.Photos {
				actualIDs = append(actualIDs, photo.ID)
			}
			assert.ElementsMatch(t, test.expected, actualIDs)
		})
	}
}

func TestSearchTransactionReauthorizesSessionAtAccessStateBoundaries(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)
	actor := createSearchSession(t, fixture)
	service := searchdomain.New(fixture.db)
	baseline, err := service.Search(ctx, actor, searchdomain.Request{Query: "Family"})
	require.NoError(t, err)
	assert.Equal(t, 1, baseline.TotalEvents)

	replacementAccessID := uuid.New()
	for _, test := range []struct {
		name           string
		invalidate     string
		invalidateArgs []any
		restore        string
		restoreArgs    []any
	}{
		{
			name:           "suspension",
			invalidate:     `UPDATE recipient_access_generations SET state = 'suspended' WHERE id = ?`,
			invalidateArgs: []any{actor.AccessID},
			restore:        `UPDATE recipient_access_generations SET state = 'completed' WHERE id = ?`,
			restoreArgs:    []any{actor.AccessID},
		},
		{
			name:           "Session revocation",
			invalidate:     `UPDATE sessions SET revoked_at = now() WHERE id = ?`,
			invalidateArgs: []any{actor.SessionID},
			restore:        `UPDATE sessions SET revoked_at = NULL WHERE id = ?`,
			restoreArgs:    []any{actor.SessionID},
		},
		{
			name: "access generation replacement",
			invalidate: `
				UPDATE recipient_access_generations SET is_current = false WHERE id = ?;
				INSERT INTO recipient_access_generations (
					id, person_id, generation, state, is_current, onboarding_completed_at, created_at, updated_at
				) VALUES (?, ?, 2, 'completed', true, now(), now(), now())
			`,
			invalidateArgs: []any{actor.AccessID, replacementAccessID, actor.PersonID},
			restore: `
				DELETE FROM recipient_access_generations WHERE id = ?;
				UPDATE recipient_access_generations SET is_current = true WHERE id = ?
			`,
			restoreArgs: []any{replacementAccessID, actor.AccessID},
		},
		{
			name:           "Session expiry",
			invalidate:     `UPDATE sessions SET idle_expires_at = now() - interval '1 second' WHERE id = ?`,
			invalidateArgs: []any{actor.SessionID},
			restore:        `UPDATE sessions SET idle_expires_at = now() + interval '1 hour' WHERE id = ?`,
			restoreArgs:    []any{actor.SessionID},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, mutationErr := fixture.db.NewRaw(test.invalidate, test.invalidateArgs...).Exec(ctx)
			require.NoError(t, mutationErr)
			t.Cleanup(func() {
				_, cleanupErr := fixture.db.NewRaw(test.restore, test.restoreArgs...).Exec(context.Background())
				require.NoError(t, cleanupErr)
			})

			result, searchErr := service.Search(ctx, actor, searchdomain.Request{Query: "Family"})
			assert.ErrorIs(t, searchErr, searchdomain.ErrNotFound)
			assert.Equal(t, searchdomain.Response{}, result, "denial must return no search response")
		})
	}
}

func createSearchSession(t *testing.T, fixture publicationFixture) setup.SessionActor {
	t.Helper()
	actor := fixture.actorFor("shared")
	credentialHash := sha256.Sum256([]byte(actor.SessionID.String()))
	_, err := fixture.db.NewRaw(`
		INSERT INTO sessions (
			id, credential_hash, person_id, recipient_access_generation_id,
			security_epoch, session_type, idle_expires_at
		) SELECT ?, ?, ?, ?, security_epoch, 'trusted', now() + interval '1 hour'
		  FROM system_settings WHERE id = 1
	`, actor.SessionID, credentialHash[:], actor.PersonID, actor.AccessID).Exec(context.Background())
	require.NoError(t, err)
	return actor
}

func assertSafeEmptySearchResponse(t *testing.T, result searchdomain.Response, message string) {
	t.Helper()
	assert.Empty(t, result.Events, message)
	assert.Empty(t, result.Photos, message)
	assert.Empty(t, result.People, message)
	assert.Zero(t, result.TotalEvents, message)
	assert.Zero(t, result.TotalPhotos, message)
	assert.False(t, result.HasMore, message)
}
