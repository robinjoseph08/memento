//go:build integration

package engagement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

func TestMeaningfulEngagementIsIdempotentAggregatedAndCuratorOnly(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	require.NoError(t, migrations.Apply(ctx, db))
	now := time.Date(2026, time.July, 30, 14, 0, 0, 0, time.UTC)
	recipient := seedEngagementActor(t, db, false, now)
	curator := seedEngagementActor(t, db, true, now)
	service := New(db, WithClock(func() time.Time { return now }))

	claimID := uuid.New().String()
	require.NoError(t, service.RecordBrowserEvent(ctx, recipient, BrowserEventRequest{
		Kind: KindVisit, ClientClaimID: claimID, DocumentVisible: true,
	}))
	require.NoError(t, service.RecordBrowserEvent(ctx, recipient, BrowserEventRequest{
		Kind: KindVisit, ClientClaimID: claimID, DocumentVisible: true,
	}), "a browser retry is idempotent")
	require.NoError(t, service.RecordBrowserEvent(ctx, recipient, BrowserEventRequest{
		Kind: KindVisit, ClientClaimID: uuid.New().String(), DocumentVisible: true,
	}), "visits in one active window coalesce")

	require.NoError(t, service.RecordBrowserEvent(ctx, curator, BrowserEventRequest{
		Kind: KindVisit, ClientClaimID: uuid.New().String(), DocumentVisible: true,
	}), "the dual-role Curator's explicit Recipient browsing is meaningful")
	curatorEventID := uuid.New()
	_, err := db.NewRaw(`INSERT INTO events
		(id, lifecycle, title, description, grouping_timezone, created_at, updated_at)
		VALUES (?, 'published', 'Curator Event', '', 'UTC', ?, ?)`, curatorEventID, now, now).Exec(ctx)
	require.NoError(t, err)
	curatorEventRaw := curatorEventID.String()
	require.NoError(t, service.RecordBrowserEvent(ctx, curator, BrowserEventRequest{
		Kind: KindEventOpened, ClientClaimID: uuid.New().String(), EventID: &curatorEventRaw, DocumentVisible: true,
	}), "Curator authority does not depend on Audience membership")
	assert.ErrorIs(t, service.RecordBrowserEvent(ctx, recipient, BrowserEventRequest{
		Kind: KindVisit, ClientClaimID: uuid.New().String(), DocumentVisible: false,
	}), ErrInvalid)

	var details, aggregate int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM engagement_events WHERE recipient_person_id = ?`, recipient.PersonID).Scan(ctx, &details))
	require.NoError(t, db.NewRaw(`SELECT event_count FROM engagement_daily_aggregates WHERE recipient_person_id = ? AND activity_date = ? AND kind = ?`, recipient.PersonID, now.Format(time.DateOnly), KindVisit).Scan(ctx, &aggregate))
	assert.Equal(t, 2, details, "Session creation and the explicit visit are meaningful")
	assert.Equal(t, 1, aggregate)
	var curatorDetails int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM engagement_events WHERE recipient_person_id = ?`, curator.PersonID).Scan(ctx, &curatorDetails))
	assert.Equal(t, 3, curatorDetails, "Session, visit, and Event open are retained for the dual-role Curator")

	now = now.Add(30 * time.Minute)
	require.NoError(t, service.RecordBrowserEvent(ctx, recipient, BrowserEventRequest{
		Kind: KindVisit, ClientClaimID: uuid.New().String(), DocumentVisible: true,
	}))
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM engagement_events WHERE recipient_person_id = ?`, recipient.PersonID).Scan(ctx, &details))
	require.NoError(t, db.NewRaw(`SELECT event_count FROM engagement_daily_aggregates WHERE recipient_person_id = ? AND activity_date = ? AND kind = ?`, recipient.PersonID, now.Format(time.DateOnly), KindVisit).Scan(ctx, &aggregate))
	assert.Equal(t, 3, details)
	assert.Equal(t, 2, aggregate)
}

func TestSessionEngagementUsesUTCDayAcrossDatabaseTimezones(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	require.NoError(t, migrations.Apply(ctx, db))
	now := time.Date(2026, time.July, 30, 14, 0, 0, 0, time.UTC)
	actor := seedEngagementActor(t, db, false, now)
	nextSessionID := uuid.New()
	credentialHash := sha256.Sum256(nextSessionID[:])
	occurredAt := time.Date(2026, time.July, 31, 0, 30, 0, 0, time.UTC)
	require.NoError(t, db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `SET LOCAL TIME ZONE 'America/Los_Angeles'`); err != nil {
			return err
		}
		_, err := tx.NewRaw(`INSERT INTO sessions
			(id, credential_hash, person_id, recipient_access_generation_id, security_epoch,
			 session_type, created_at, last_activity_at, idle_expires_at)
			SELECT ?, ?, ?, ?, security_epoch, 'trusted', ?, ?, ?
			FROM system_settings WHERE id = 1`, nextSessionID, credentialHash[:], actor.PersonID,
			actor.AccessID, occurredAt, occurredAt, occurredAt.Add(time.Hour)).Exec(ctx)
		return err
	}))
	var count int
	require.NoError(t, db.NewRaw(`SELECT event_count FROM engagement_daily_aggregates
		WHERE recipient_person_id = ? AND activity_date = '2026-07-31' AND kind = ?`,
		actor.PersonID, KindSessionStarted).Scan(ctx, &count))
	assert.Equal(t, 1, count)
}

func TestRecipientEngagementRoutesAreCuratorOnlyWithPersistedSessions(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	require.NoError(t, migrations.Apply(ctx, db))
	now := time.Date(2026, time.July, 30, 15, 0, 0, 0, time.UTC)
	recipient := seedEngagementActor(t, db, false, now)
	curator := seedEngagementActor(t, db, true, now)
	recipientCredential := persistEngagementCredential(t, db, recipient)
	curatorCredential := persistEngagementCredential(t, db, curator)

	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	RegisterRoutes(e, NewHandler(New(db), setup.New(db, nil, config.SecurityConfig{})))
	serve := func(credential string) *httptest.ResponseRecorder {
		request := httptest.NewRequestWithContext(ctx, http.MethodGet,
			"/api/engagement/recipients/"+recipient.PersonID.String(), nil)
		request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: credential})
		response := httptest.NewRecorder()
		e.ServeHTTP(response, request)
		return response
	}

	assert.Equal(t, http.StatusNotFound, serve(recipientCredential).Code,
		"a Recipient cannot discover any Recipient engagement, including their own")
	assert.Equal(t, http.StatusOK, serve(curatorCredential).Code)
}

func TestRecipientAdministrationReportsOnlyMeaningfulExplicitDetails(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	require.NoError(t, migrations.Apply(ctx, db))
	now := time.Date(2026, time.July, 30, 16, 0, 0, 0, time.UTC)
	actor := seedEngagementActor(t, db, false, now)
	service := New(db, WithClock(func() time.Time { return now }))
	mediaID, eventID := uuid.New(), uuid.New()
	_, err := db.NewRaw(`
		INSERT INTO media_items
			(id, immich_asset_id, media_type, width, height, local_date_time, first_seen_at, last_seen_at)
		VALUES (?, ?, 'image', 100, 100, '2026-07-30T12:00:00Z', ?, ?);
		INSERT INTO events (id, lifecycle, title, description, grouping_timezone, created_at, updated_at)
		VALUES (?, 'draft', 'Explicit Event', '', 'UTC', ?, ?)
	`, mediaID, uuid.New(), now, now, eventID, now, now).Exec(ctx)
	require.NoError(t, err)

	require.NoError(t, db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		mediaOpen := "explicit-media-open:" + uuid.NewString()
		if err := service.recordServerEvent(ctx, tx, eventRecord{Actor: actor, Kind: KindMediaOpened, MediaID: &mediaID, OriginKey: &mediaOpen, OccurredAt: now.Add(-7 * time.Minute)}); err != nil {
			return err
		}
		eventOpen := "explicit-event-open:" + uuid.NewString()
		if err := service.recordServerEvent(ctx, tx, eventRecord{Actor: actor, Kind: KindEventOpened, EventID: &eventID, OriginKey: &eventOpen, OccurredAt: now.Add(-6 * time.Minute)}); err != nil {
			return err
		}
		if err := service.RecordComment(ctx, tx, actor, uuid.New(), mediaID, now.Add(-5*time.Minute)); err != nil {
			return err
		}
		if err := service.RecordFavorite(ctx, tx, actor, mediaID, "added", now.Add(-4*time.Minute)); err != nil {
			return err
		}
		if err := service.RecordSuggestion(ctx, tx, actor, uuid.New(), "submitted", now.Add(-3*time.Minute)); err != nil {
			return err
		}
		return service.RecordArchiveDownload(ctx, tx, actor, uuid.New(), &eventID, now.Add(-2*time.Minute))
	}))

	firstPage, err := service.GetRecipientEngagement(ctx, actor.PersonID, "", 2)
	require.NoError(t, err)
	require.Len(t, firstPage.Timeline, 2)
	require.NotNil(t, firstPage.NextCursor)
	secondPage, err := service.GetRecipientEngagement(ctx, actor.PersonID, *firstPage.NextCursor, 2)
	require.NoError(t, err)
	require.Len(t, secondPage.Timeline, 2)
	assert.NotEqual(t, firstPage.Timeline[1].ID, secondPage.Timeline[0].ID)

	detail, err := service.GetRecipientEngagement(ctx, actor.PersonID, "", 100)
	require.NoError(t, err)
	assert.Equal(t, now, *detail.LatestMeaningfulActivity)
	assert.Equal(t, 1, detail.ActiveDays.Days7)
	assert.Equal(t, 1, detail.Counts90Days.EventOpens)
	assert.Equal(t, 1, detail.Counts90Days.MediaOpens)
	assert.Equal(t, 1, detail.Counts90Days.Downloads)
	assert.Equal(t, 1, detail.Counts90Days.Comments)
	assert.Equal(t, 1, detail.Counts90Days.FavoriteChanges)
	assert.Equal(t, 1, detail.Counts90Days.InvitationSuggestions)
	require.Len(t, detail.Timeline, 7)

	openers, err := service.MediaOpeners(ctx, mediaID)
	require.NoError(t, err)
	require.Len(t, openers.Openers, 1)
	assert.Equal(t, actor.PersonID.String(), openers.Openers[0].RecipientPersonID)
	assert.Equal(t, 1, openers.Openers[0].OpenCount)
}

func TestDetailedEngagementExpiresWithoutRemovingAggregatesOrSecurityAudit(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	require.NoError(t, migrations.Apply(ctx, db))
	now := time.Date(2026, time.July, 30, 14, 0, 0, 0, time.UTC)
	actor := seedEngagementActor(t, db, false, now)
	service := New(db, WithClock(func() time.Time { return now }))
	cutoff := now.AddDate(-1, 0, 0)

	for index, occurredAt := range []time.Time{cutoff.Add(-time.Nanosecond), cutoff, cutoff.Add(time.Nanosecond)} {
		_, err := db.NewRaw(`INSERT INTO engagement_events
			(recipient_person_id, recipient_access_generation_id, session_id, kind, origin_key, occurred_at)
			VALUES (?, ?, ?, ?, ?, ?) RETURNING id`, actor.PersonID, actor.AccessID, actor.SessionID,
			KindVisit, "retention:"+uuid.NewString(), occurredAt).Exec(ctx)
		require.NoError(t, err, index)
	}
	_, err := db.NewRaw(`INSERT INTO engagement_daily_aggregates
		(recipient_person_id, activity_date, kind, event_count, first_occurred_at, last_occurred_at)
		VALUES (?, ?, ?, 1, ?, ?)`, actor.PersonID, cutoff.Format(time.DateOnly), KindVisit, cutoff, cutoff).Exec(ctx)
	require.NoError(t, err)
	_, err = db.NewRaw(`INSERT INTO security_audit_events
		(actor_person_id, subject_person_id, action, outcome, session_id, created_at)
		VALUES (?, ?, 'retention_proof', 'success', ?, ?)`, actor.PersonID, actor.PersonID, actor.SessionID, cutoff.Add(-time.Hour)).Exec(ctx)
	require.NoError(t, err)

	deleted, err := service.DeleteExpiredDetails(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)

	var details, aggregates, audits, curatorDetails int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM engagement_events WHERE recipient_person_id = ?`, actor.PersonID).Scan(ctx, &details))
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM engagement_daily_aggregates WHERE recipient_person_id = ?`, actor.PersonID).Scan(ctx, &aggregates))
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM security_audit_events WHERE action = 'retention_proof'`, actor.PersonID).Scan(ctx, &audits))
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM curator_activity_items WHERE source_kind = 'engagement' AND actor_person_id = ?`, actor.PersonID).Scan(ctx, &curatorDetails))
	assert.Equal(t, 3, details)
	assert.Equal(t, 2, aggregates)
	assert.Equal(t, 1, audits)
	assert.Equal(t, 3, curatorDetails, "expired per-Recipient activity projections are removed with details")
}

func persistEngagementCredential(t *testing.T, db *bun.DB, actor setup.SessionActor) string {
	t.Helper()
	raw := sha256.Sum256(actor.SessionID[:])
	hash := sha256.Sum256(raw[:])
	_, err := db.NewRaw(`UPDATE sessions SET credential_hash = ? WHERE id = ?`, hash[:], actor.SessionID).Exec(context.Background())
	require.NoError(t, err)
	return hex.EncodeToString(raw[:])
}

func seedEngagementActor(t *testing.T, db *bun.DB, curator bool, now time.Time) setup.SessionActor {
	t.Helper()
	personID, accessID, sessionID := uuid.New(), uuid.New(), uuid.New()
	credentialHash := sha256.Sum256(sessionID[:])
	// Engagement timestamps use a fixed clock, while Session authorization uses PostgreSQL's clock.
	sessionExpiresAt := time.Date(9999, time.December, 31, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()
	_, err := db.NewRaw(`
		UPDATE system_settings SET setup_complete = true;
		INSERT INTO people (id, display_name, sort_name) VALUES (?, ?, ?);
		INSERT INTO person_roles (person_id, role) VALUES (?, 'recipient');
		INSERT INTO recipient_access_generations
			(id, person_id, generation, state, is_current, onboarding_completed_at)
		VALUES (?, ?, 1, 'completed', true, ?);
		INSERT INTO sessions
			(id, credential_hash, person_id, recipient_access_generation_id, security_epoch,
			 session_type, created_at, last_activity_at, idle_expires_at)
		SELECT ?, ?, ?, ?, security_epoch, 'trusted', ?, ?, ?
		FROM system_settings WHERE id = 1;
	`, personID, map[bool]string{true: "Curator", false: "Recipient"}[curator], uuid.NewString(),
		personID, accessID, personID, now, sessionID, credentialHash[:], personID, accessID, now, now,
		sessionExpiresAt).Exec(ctx)
	require.NoError(t, err)
	if curator {
		_, err = db.NewRaw(`INSERT INTO person_roles (person_id, role) VALUES (?, 'curator')`, personID).Exec(ctx)
		require.NoError(t, err)
	}
	return setup.SessionActor{PersonID: personID, AccessID: accessID, SessionID: sessionID, Curator: curator}
}
