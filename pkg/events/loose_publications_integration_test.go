//go:build integration

package events

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/archives"
	"github.com/robinjoseph08/memento/pkg/audiences"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/library"
	"github.com/robinjoseph08/memento/pkg/mediaaccess"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/restores"
	searchpkg "github.com/robinjoseph08/memento/pkg/search"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/staging"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type looseRecipientAuthorizer struct{ actor setup.SessionActor }

type looseOnlyArchiveSource struct{}

func (looseOnlyArchiveSource) ArchiveInfo(context.Context, []uuid.UUID) ([]immich.ArchivePart, error) {
	return nil, errors.New("archive source must not be reached")
}
func (looseOnlyArchiveSource) Archive(context.Context, []uuid.UUID) (immich.ArchiveResponse, error) {
	return immich.ArchiveResponse{}, errors.New("archive source must not be reached")
}
func (looseOnlyArchiveSource) Original(context.Context, uuid.UUID, immich.MediaRequest) (immich.MediaResponse, error) {
	return immich.MediaResponse{}, errors.New("archive source must not be reached")
}

func (authorizer looseRecipientAuthorizer) AuthorizeSession(context.Context, string, string, bool) (setup.SessionActor, error) {
	return authorizer.actor, nil
}

func looseRecipientHTTP(fixture loosePublicationFixture, actor setup.SessionActor) *echo.Echo {
	e := echo.New()
	library.RegisterRoutes(e, library.NewHandler(library.New(fixture.db, nil), looseRecipientAuthorizer{actor: actor}))
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	return e
}

type loosePublicationFixture struct {
	db               *bun.DB
	service          *Service
	audiences        *audiences.Service
	actor            setup.CuratorSession
	looseID          uuid.UUID
	mediaID          uuid.UUID
	completedPerson  uuid.UUID
	completedAccess  uuid.UUID
	completedSession uuid.UUID
	pendingPerson    uuid.UUID
	pendingAccess    uuid.UUID
	now              time.Time
}

func newLoosePublicationFixture(t *testing.T, recipients bool) loosePublicationFixture {
	t.Helper()
	ctx := context.Background()
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(ctx, db))
	f := loosePublicationFixture{db: db, looseID: uuid.New(), mediaID: uuid.New(),
		completedPerson: uuid.New(), completedAccess: uuid.New(), completedSession: uuid.New(), pendingPerson: uuid.New(),
		pendingAccess: uuid.New(), now: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)}
	f.actor = setup.CuratorSession{PersonID: uuid.New(), SessionID: uuid.New()}
	curatorAccessID := uuid.New()
	_, err := db.NewRaw(`
		UPDATE system_settings SET setup_complete=true WHERE id=1;
		INSERT INTO people (id,display_name,sort_name) VALUES (?, 'Curator', 'Curator'),
		 (?, 'Completed', 'Completed'), (?, 'Pending', 'Pending');
		INSERT INTO person_roles (person_id,role) VALUES (?, 'curator'), (?, 'recipient'), (?, 'recipient');
		INSERT INTO recipient_access_generations
		 (id,person_id,generation,state,is_current,onboarding_completed_at,created_at,updated_at)
		VALUES (?, ?, 1, 'completed', true, ?, ?, ?),
		 (?, ?, 1, 'completed', true, ?, ?, ?),
		 (?, ?, 1, 'pending', true, NULL, ?, ?);
		INSERT INTO sessions (id,credential_hash,person_id,recipient_access_generation_id,security_epoch,session_type,idle_expires_at)
		SELECT ?, decode(repeat('42',32),'hex'), ?, ?, security_epoch, 'trusted', ?::timestamptz + interval '1 day'
		FROM system_settings WHERE id=1;
		INSERT INTO sessions (id,credential_hash,person_id,recipient_access_generation_id,security_epoch,session_type,idle_expires_at)
		SELECT ?, decode(repeat('43',32),'hex'), ?, ?, security_epoch, 'trusted', ?::timestamptz + interval '1 day'
		FROM system_settings WHERE id=1;
		INSERT INTO media_items
		 (id,immich_asset_id,media_type,width,height,local_date_time,first_seen_at,last_seen_at)
		VALUES (?, ?, 'image', 1200, 800, '2026-08-01T09:00:00Z', ?, ?);
		INSERT INTO media_backings (id,media_item_id,immich_asset_id,linked_at)
		SELECT gen_random_uuid(), id, immich_asset_id, ? FROM media_items WHERE id = ?;
		INSERT INTO loose_items
		 (id,media_item_id,title,description,grouping_timezone,proposed_day,version,audience_complete,created_at,updated_at)
		VALUES (?, ?, 'Saved loose title', 'Private draft correction', 'UTC', '2026-08-01', 4, true, ?, ?)
	`, f.actor.PersonID, f.completedPerson, f.pendingPerson, f.actor.PersonID, f.completedPerson,
		f.pendingPerson, curatorAccessID, f.actor.PersonID, f.now, f.now, f.now,
		f.completedAccess, f.completedPerson, f.now, f.now, f.now,
		f.pendingAccess, f.pendingPerson, f.now, f.now,
		f.actor.SessionID, f.actor.PersonID, curatorAccessID, f.now,
		f.completedSession, f.completedPerson, f.completedAccess, f.now,
		f.mediaID, uuid.New(), f.now, f.now, f.now, f.mediaID,
		f.looseID, f.mediaID, f.now, f.now).Exec(ctx)
	require.NoError(t, err)
	snapshotID := uuid.New()
	label := "Curator only"
	if recipients {
		label = "Shared"
	}
	_, err = db.NewRaw(`INSERT INTO audience_snapshots
		(id,target_kind,target_id,approved_by_person_id,approved_at,label)
		VALUES (?, 'loose_item', ?, ?, ?, ?);
		INSERT INTO current_audience_snapshots (target_kind,target_id,snapshot_id)
		VALUES ('loose_item', ?, ?)`, snapshotID, f.looseID, f.actor.PersonID, f.now, label,
		f.looseID, snapshotID).Exec(ctx)
	require.NoError(t, err)
	if recipients {
		_, err = db.NewRaw(`INSERT INTO audience_snapshot_entries
			(snapshot_id,recipient_person_id,recipient_access_generation_id)
			VALUES (?, ?, ?), (?, ?, ?)`, snapshotID, f.completedPerson, f.completedAccess,
			snapshotID, f.pendingPerson, f.pendingAccess).Exec(ctx)
		require.NoError(t, err)
	}
	f.service = New(db)
	f.service.now = func() time.Time { return f.now }
	f.audiences = audiences.New(db, nil)
	return f
}

func (f loosePublicationFixture) publish(t *testing.T, version int64) LoosePublicationResponse {
	t.Helper()
	publication, err := f.service.PublishLooseItem(context.Background(), f.actor, f.looseID,
		PublishLooseItemRequest{Version: version})
	require.NoError(t, err)
	return publication
}

func TestLoosePublicationSupportsEmptyAudienceAndGenerationAwarePreview(t *testing.T) {
	t.Run("explicitly empty Audience publishes curator-only history", func(t *testing.T) {
		f := newLoosePublicationFixture(t, false)
		preview, err := f.service.PreviewLooseItem(context.Background(), f.actor, f.looseID, f.completedPerson)
		require.NoError(t, err)
		assert.False(t, preview.Authorized)
		assert.True(t, preview.Preview)
		assert.Empty(t, preview.Title)
		publication := f.publish(t, 4)
		var history, entitlements int
		require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM published_loose_item_revisions WHERE publication_id=?`, publication.ID).Scan(context.Background(), &history))
		require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM current_loose_item_entitlements WHERE loose_item_id=?`, f.looseID).Scan(context.Background(), &entitlements))
		assert.Equal(t, 1, history)
		assert.Zero(t, entitlements)
		var auditAction, auditTargetKind, auditTargetID, auditPublicationID string
		var auditRevision int64
		require.NoError(t, f.db.NewRaw(`SELECT action, target_kind, target_id::text,
			metadata->>'publication_id', (metadata->>'revision')::bigint
			FROM publication_audit_events WHERE target_kind = 'loose_item' AND target_id = ?
			ORDER BY id DESC LIMIT 1`, f.looseID).Scan(context.Background(), &auditAction,
			&auditTargetKind, &auditTargetID, &auditPublicationID, &auditRevision))
		assert.Equal(t, "loose_item_published", auditAction)
		assert.Equal(t, "loose_item", auditTargetKind)
		assert.Equal(t, f.looseID.String(), auditTargetID)
		assert.Equal(t, publication.ID, auditPublicationID)
		assert.Equal(t, int64(1), auditRevision)

		var outboxKind, aggregateKind, aggregateID, outboxLooseID, outboxPublicationID, outboxPayload string
		var aggregateVersion int64
		var notify bool
		require.NoError(t, f.db.NewRaw(`SELECT kind, aggregate_kind, aggregate_id, aggregate_version,
			payload->>'loose_item_id', payload->>'publication_id',
			(payload->>'notify_recipients')::boolean, payload::text
			FROM outbox_events WHERE aggregate_kind = 'loose_item_publication' AND aggregate_id = ?`,
			f.looseID.String()).Scan(context.Background(), &outboxKind, &aggregateKind, &aggregateID,
			&aggregateVersion, &outboxLooseID, &outboxPublicationID, &notify, &outboxPayload))
		assert.Equal(t, PublicationJobKind, outboxKind)
		assert.Equal(t, "loose_item_publication", aggregateKind)
		assert.Equal(t, f.looseID.String(), aggregateID)
		assert.Equal(t, int64(1), aggregateVersion)
		assert.Equal(t, f.looseID.String(), outboxLooseID)
		assert.Equal(t, publication.ID, outboxPublicationID)
		assert.True(t, notify)
		require.NoError(t, f.service.HandlePublicationJob(context.Background(), worker.Job{Payload: []byte(outboxPayload)}))
		_, err = f.service.RecipientLooseItem(context.Background(), setup.SessionActor{PersonID: f.completedPerson, AccessID: f.completedAccess}, f.looseID)
		assert.ErrorIs(t, err, ErrNoPublication)
	})

	t.Run("Pending can be previewed but cannot use a real Recipient read", func(t *testing.T) {
		f := newLoosePublicationFixture(t, true)
		preview, err := f.service.PreviewLooseItem(context.Background(), f.actor, f.looseID, f.pendingPerson)
		require.NoError(t, err)
		assert.True(t, preview.Preview)
		assert.Equal(t, PreviewCapabilities{}, preview.Capabilities)
		var previews, recipientActivity int
		require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM loose_publication_preview_audit_events WHERE loose_item_id=?`, f.looseID).Scan(context.Background(), &previews))
		require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM publication_activity_items`).Scan(context.Background(), &recipientActivity))
		assert.Equal(t, 1, previews)
		assert.Zero(t, recipientActivity)

		f.publish(t, 4)
		var pendingEntitlement, pendingActivity, pendingNewForYou int
		require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM current_loose_item_entitlements
			WHERE loose_item_id = ? AND recipient_access_generation_id = ?`, f.looseID, f.pendingAccess).Scan(context.Background(), &pendingEntitlement))
		require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM publication_activity_items
			WHERE recipient_access_generation_id = ?`, f.pendingAccess).Scan(context.Background(), &pendingActivity))
		require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM new_for_you_entries
			WHERE recipient_access_generation_id = ?`, f.pendingAccess).Scan(context.Background(), &pendingNewForYou))
		assert.Equal(t, 1, pendingEntitlement, "approved Pending authority remains available after Onboarding")
		assert.Zero(t, pendingActivity)
		assert.Zero(t, pendingNewForYou)
		_, err = f.service.RecipientLooseItem(context.Background(), setup.SessionActor{PersonID: f.pendingPerson, AccessID: f.pendingAccess}, f.looseID)
		assert.ErrorIs(t, err, ErrNoPublication)
		view, err := f.service.RecipientLooseItem(context.Background(), setup.SessionActor{PersonID: f.completedPerson, AccessID: f.completedAccess}, f.looseID)
		require.NoError(t, err)
		assert.Equal(t, "Saved loose title", view.Title)

		_, err = f.db.NewRaw(`UPDATE recipient_access_generations SET is_current=false WHERE id=?`, f.completedAccess).Exec(context.Background())
		require.NoError(t, err)
		_, err = f.service.RecipientLooseItem(context.Background(), setup.SessionActor{PersonID: f.completedPerson, AccessID: f.completedAccess}, f.looseID)
		assert.ErrorIs(t, err, ErrNoPublication)
	})
}

func TestLooseOnlyEntitlementDoesNotGrantEventDetailOrArchive(t *testing.T) {
	f := newLoosePublicationFixture(t, true)
	f.publish(t, 4)
	actor := setup.SessionActor{PersonID: f.completedPerson, AccessID: f.completedAccess, SessionID: f.completedSession}
	_, err := f.service.RecipientEvent(context.Background(), actor, f.looseID)
	assert.ErrorIs(t, err, ErrNoPublication)
	eventID := f.looseID.String()
	_, err = archives.New(f.db, looseOnlyArchiveSource{}).Plan(context.Background(), actor,
		archives.PlanRequest{Scope: "event", EventID: &eventID})
	assert.ErrorIs(t, err, archives.ErrNotFound)
}

func TestLoosePublicationProjectsAuthorizedRecipientLibraryAndSearch(t *testing.T) {
	f := newLoosePublicationFixture(t, true)
	publication := f.publish(t, 4)
	actor := setup.SessionActor{PersonID: f.completedPerson, AccessID: f.completedAccess, SessionID: f.completedSession}
	libraryService := library.New(f.db, nil)

	photos, err := libraryService.Photos(context.Background(), actor, "", "", false)
	require.NoError(t, err)
	require.Len(t, photos.Media, 1)
	assert.Equal(t, f.mediaID.String(), photos.Media[0].ID)
	chronology, err := libraryService.Chronology(context.Background(), actor, false)
	require.NoError(t, err)
	require.Len(t, chronology.Dates, 1)
	require.NotNil(t, chronology.Dates[0].CaptureDate)
	assert.Equal(t, "2026-08-01", *chronology.Dates[0].CaptureDate)
	assert.Equal(t, 1, chronology.Dates[0].MediaCount)

	newForYou, err := libraryService.NewForYou(context.Background(), actor)
	require.NoError(t, err)
	require.Len(t, newForYou.LooseItems, 1)
	assert.Equal(t, publication.ID, newForYou.LooseItems[0].PublicationID)

	item, err := libraryService.LooseItem(context.Background(), actor, f.looseID)
	require.NoError(t, err)
	assert.Equal(t, "Saved loose title", item.Title)
	assert.Equal(t, f.mediaID.String(), item.Media.ID)
	recipientHTTP := looseRecipientHTTP(f, actor)
	opened := draftRequest(recipientHTTP, http.MethodGet, "/api/me/loose-items/"+f.looseID.String(), "")
	require.Equal(t, http.StatusOK, opened.Code)
	assert.Contains(t, opened.Body.String(), "Saved loose title")

	results, err := searchpkg.New(f.db).Search(context.Background(), actor, searchpkg.Request{Query: "Saved loose"})
	require.NoError(t, err)
	assert.Zero(t, results.TotalEvents)
	assert.Equal(t, 1, results.TotalPhotos)
	require.Len(t, results.Photos, 1)
	assert.Equal(t, f.mediaID.String(), results.Photos[0].ID)

	require.NoError(t, libraryService.MarkSeen(context.Background(), actor, uuid.MustParse(publication.ID)))
	newForYou, err = libraryService.NewForYou(context.Background(), actor)
	require.NoError(t, err)
	assert.Empty(t, newForYou.LooseItems)

	_, err = f.service.Withdraw(context.Background(), f.actor, WithdrawRequest{
		TargetKind: WithdrawalTargetLooseItem, TargetID: f.looseID.String(), Reason: "Immediate access proof",
	})
	require.NoError(t, err)
	photos, err = libraryService.Photos(context.Background(), actor, "", "", false)
	require.NoError(t, err)
	assert.Empty(t, photos.Media)
	chronology, err = libraryService.Chronology(context.Background(), actor, false)
	require.NoError(t, err)
	assert.Empty(t, chronology.Dates)
	_, err = libraryService.LooseItem(context.Background(), actor, f.looseID)
	assert.ErrorIs(t, err, library.ErrNotFound)
	results, err = searchpkg.New(f.db).Search(context.Background(), actor, searchpkg.Request{Query: "Saved loose"})
	require.NoError(t, err)
	assert.Zero(t, results.TotalPhotos)
}

func TestLooseCuratorServicesRejectNonCuratorBeforeTargetLookup(t *testing.T) {
	f := newLoosePublicationFixture(t, true)
	nonCurator := setup.CuratorSession{PersonID: f.completedPerson, SessionID: f.completedSession}

	_, err := f.service.ListLooseItems(context.Background(), nonCurator)
	assert.ErrorIs(t, err, setup.ErrNotCurator)
	_, err = f.service.GetLooseItem(context.Background(), nonCurator, uuid.New())
	assert.ErrorIs(t, err, setup.ErrNotCurator)
	_, err = f.service.UpdateLooseItem(context.Background(), nonCurator, f.looseID, UpdateLooseItemRequest{
		Version: 4, Title: "Unauthorized", GroupingTimezone: "UTC", ProposedDay: ptr("2026-08-01"), PlaceLabels: []string{},
	})
	assert.ErrorIs(t, err, setup.ErrNotCurator)
	_, err = f.service.PreviewRecipients(context.Background(), nonCurator, uuid.New())
	assert.ErrorIs(t, err, setup.ErrNotCurator)
	_, err = f.service.PreviewLooseItem(context.Background(), nonCurator, f.looseID, f.completedPerson)
	assert.ErrorIs(t, err, setup.ErrNotCurator)
	_, err = f.service.PublishLooseItem(context.Background(), nonCurator, uuid.New(), PublishLooseItemRequest{Version: 4})
	assert.ErrorIs(t, err, setup.ErrNotCurator)

	f.publish(t, 4)
	_, err = f.service.Withdraw(context.Background(), nonCurator, WithdrawRequest{
		TargetKind: WithdrawalTargetLooseItem, TargetID: f.looseID.String(), Reason: "Unauthorized",
	})
	assert.ErrorIs(t, err, setup.ErrNotCurator)
	var entitlement bool
	require.NoError(t, f.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM current_loose_item_entitlements
		WHERE loose_item_id = ? AND recipient_access_generation_id = ?)`, f.looseID, f.completedAccess).Scan(context.Background(), &entitlement))
	assert.True(t, entitlement)
}

func TestLooseAudienceServiceSeamsReauthorizeCuratorRole(t *testing.T) {
	f := newLoosePublicationFixture(t, true)
	_, err := f.db.NewRaw(`DELETE FROM person_roles WHERE person_id = ? AND role = 'curator'`, f.actor.PersonID).
		Exec(context.Background())
	require.NoError(t, err)
	_, err = f.audiences.ReviewLooseItem(context.Background(), f.actor, f.looseID)
	assert.ErrorIs(t, err, audiences.ErrNotFound)
	_, err = f.audiences.Recalculate(context.Background(), f.actor, "loose_item", f.looseID, 1)
	assert.ErrorIs(t, err, audiences.ErrNotFound)
	_, err = f.audiences.SetOverride(context.Background(), f.actor, "loose_item", f.looseID, 1,
		audiences.OverrideRequest{RecipientPersonID: f.completedPerson.String(), State: "included"})
	assert.ErrorIs(t, err, audiences.ErrNotFound)
	_, err = f.audiences.Approve(context.Background(), f.actor, "loose_item", f.looseID, 1)
	assert.ErrorIs(t, err, audiences.ErrNotFound)
}

func TestLoosePublicationRejectsStaleAudienceGeneration(t *testing.T) {
	f := newLoosePublicationFixture(t, true)
	replacementAccess := uuid.New()
	_, err := f.db.NewRaw(`UPDATE recipient_access_generations SET is_current = false WHERE id = ?;
		INSERT INTO recipient_access_generations
		(id, person_id, generation, state, is_current, onboarding_completed_at, created_at, updated_at)
		VALUES (?, ?, 2, 'completed', true, ?, ?, ?)`, f.completedAccess,
		replacementAccess, f.completedPerson, f.now, f.now, f.now).Exec(context.Background())
	require.NoError(t, err)

	_, err = f.service.PublishLooseItem(context.Background(), f.actor, f.looseID, PublishLooseItemRequest{Version: 4})
	assert.ErrorIs(t, err, ErrAudienceNotCurrent)

	authorized, err := mediaaccess.GenerationCanAccess(context.Background(), f.db, replacementAccess, f.mediaID)
	require.NoError(t, err)
	assert.False(t, authorized)
}

func TestLooseRecipientProductionQueryRejectsPendingAndReplacementGenerations(t *testing.T) {
	f := newLoosePublicationFixture(t, true)
	f.publish(t, 4)
	service := library.New(f.db, nil)
	pendingSession := uuid.New()
	_, err := f.db.NewRaw(`INSERT INTO sessions
		(id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at)
		SELECT ?, decode(repeat('44', 32), 'hex'), ?, ?, security_epoch, 'trusted', ?::timestamptz + interval '1 day'
		FROM system_settings WHERE id = 1`, pendingSession, f.pendingPerson, f.pendingAccess, f.now).Exec(context.Background())
	require.NoError(t, err)
	pendingActor := setup.SessionActor{PersonID: f.pendingPerson, AccessID: f.pendingAccess, SessionID: pendingSession}
	_, err = service.LooseItem(context.Background(), pendingActor, f.looseID)
	assert.ErrorIs(t, err, library.ErrNotFound)
	pendingHTTP := looseRecipientHTTP(f, pendingActor)
	pendingResponse := draftRequest(pendingHTTP, http.MethodGet, "/api/me/loose-items/"+f.looseID.String(), "")
	guessedResponse := draftRequest(pendingHTTP, http.MethodGet, "/api/me/loose-items/"+uuid.NewString(), "")
	assert.Equal(t, http.StatusNotFound, pendingResponse.Code)
	assert.Equal(t, guessedResponse.Body.String(), pendingResponse.Body.String())

	replacementAccess, replacementSession := uuid.New(), uuid.New()
	_, err = f.db.NewRaw(`UPDATE recipient_access_generations SET is_current = false WHERE id = ?;
		INSERT INTO recipient_access_generations
		(id, person_id, generation, state, is_current, onboarding_completed_at, created_at, updated_at)
		VALUES (?, ?, 2, 'completed', true, ?, ?, ?);
		INSERT INTO sessions
		(id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at)
		SELECT ?, decode(repeat('45', 32), 'hex'), ?, ?, security_epoch, 'trusted', ?::timestamptz + interval '1 day'
		FROM system_settings WHERE id = 1`, f.completedAccess,
		replacementAccess, f.completedPerson, f.now, f.now, f.now,
		replacementSession, f.completedPerson, replacementAccess, f.now).Exec(context.Background())
	require.NoError(t, err)
	_, err = service.LooseItem(context.Background(), setup.SessionActor{
		PersonID: f.completedPerson, AccessID: f.completedAccess, SessionID: f.completedSession,
	}, f.looseID)
	assert.ErrorIs(t, err, library.ErrNotFound)
	replacementActor := setup.SessionActor{
		PersonID: f.completedPerson, AccessID: replacementAccess, SessionID: replacementSession,
	}
	_, err = service.LooseItem(context.Background(), replacementActor, f.looseID)
	assert.ErrorIs(t, err, library.ErrNotFound)
	replacementHTTP := looseRecipientHTTP(f, replacementActor)
	replacementResponse := draftRequest(replacementHTTP, http.MethodGet, "/api/me/loose-items/"+f.looseID.String(), "")
	guessedResponse = draftRequest(replacementHTTP, http.MethodGet, "/api/me/loose-items/"+uuid.NewString(), "")
	assert.Equal(t, http.StatusNotFound, replacementResponse.Code)
	assert.Equal(t, guessedResponse.Body.String(), replacementResponse.Body.String())
}

func TestLooseCorrectionsCoalesceAndCancellationLeavesNoResidue(t *testing.T) {
	f := newLoosePublicationFixture(t, true)
	f.publish(t, 4)
	first, err := f.service.UpdateLooseItem(context.Background(), f.actor, f.looseID, UpdateLooseItemRequest{
		Version: 4, Title: "First private correction", Description: "Private draft correction",
		GroupingTimezone: "UTC", ProposedDay: ptr("2026-08-01"), PlaceLabels: []string{},
	})
	require.NoError(t, err)
	var firstStagedID uuid.UUID
	require.NoError(t, f.db.NewRaw(`SELECT id FROM loose_staged_updates WHERE loose_item_id = ?`, f.looseID).Scan(context.Background(), &firstStagedID))

	second, err := f.service.UpdateLooseItem(context.Background(), f.actor, f.looseID, UpdateLooseItemRequest{
		Version: first.Version, Title: "Second private correction", Description: "Coalesced privately",
		GroupingTimezone: "UTC", ProposedDay: ptr("2026-08-01"), PlaceLabels: []string{"Home"},
	})
	require.NoError(t, err)
	var secondStagedID uuid.UUID
	require.NoError(t, f.db.NewRaw(`SELECT id FROM loose_staged_updates WHERE loose_item_id = ?`, f.looseID).Scan(context.Background(), &secondStagedID))
	assert.Equal(t, firstStagedID, secondStagedID)

	cancelled, err := f.service.UpdateLooseItem(context.Background(), f.actor, f.looseID, UpdateLooseItemRequest{
		Version: second.Version, Title: "Saved loose title", Description: "Private draft correction",
		GroupingTimezone: "UTC", ProposedDay: ptr("2026-08-01"), PlaceLabels: []string{},
	})
	require.NoError(t, err)
	assert.False(t, cancelled.HasStagedUpdate)
	var stagedCount int
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM loose_staged_updates WHERE loose_item_id = ?`, f.looseID).Scan(context.Background(), &stagedCount))
	assert.Zero(t, stagedCount)
}

func TestLooseAudienceCorrectionCancellationLeavesNoStagedResidue(t *testing.T) {
	f := newLoosePublicationFixture(t, true)
	publication := f.publish(t, 4)
	var reviewVersion int64
	require.NoError(t, f.db.NewRaw(`SELECT review_version FROM loose_items WHERE id = ?`, f.looseID).
		Scan(context.Background(), &reviewVersion))
	review, err := f.audiences.SetOverride(context.Background(), f.actor, "loose_item", f.looseID,
		reviewVersion, audiences.OverrideRequest{RecipientPersonID: f.completedPerson.String(), State: "included"})
	require.NoError(t, err)
	review, err = f.audiences.SetOverride(context.Background(), f.actor, "loose_item", f.looseID,
		review.Version, audiences.OverrideRequest{RecipientPersonID: f.pendingPerson.String(), State: "included"})
	require.NoError(t, err)
	approval, err := f.audiences.Approve(context.Background(), f.actor, "loose_item", f.looseID, review.Version)
	require.NoError(t, err)
	var publishedSnapshotID uuid.UUID
	require.NoError(t, f.db.NewRaw(`SELECT audience_snapshot_id FROM published_loose_item_revisions
		WHERE publication_id = ?`, publication.ID).Scan(context.Background(), &publishedSnapshotID))
	assert.NotEqual(t, publishedSnapshotID.String(), approval.Audience.ID)
	item, err := f.service.GetLooseItem(context.Background(), f.actor, f.looseID)
	require.NoError(t, err)
	assert.False(t, item.HasStagedUpdate)
	var stagedCount int
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM loose_staged_updates WHERE loose_item_id = ?`, f.looseID).
		Scan(context.Background(), &stagedCount))
	assert.Zero(t, stagedCount)
}

func TestLooseCorrectionsStayPrivateAndWithdrawalRequiresApprovalAndPublication(t *testing.T) {
	f := newLoosePublicationFixture(t, true)
	first := f.publish(t, 4)
	updated, err := f.service.UpdateLooseItem(context.Background(), f.actor, f.looseID, UpdateLooseItemRequest{
		Version: 4, Title: "Corrected title", Description: "Still private", GroupingTimezone: "UTC",
		ProposedDay: ptr("2026-08-02"), PlaceLabels: []string{"Garden"},
	})
	require.NoError(t, err)
	assert.True(t, updated.HasStagedUpdate)
	before, err := f.service.RecipientLooseItem(context.Background(), setup.SessionActor{PersonID: f.completedPerson, AccessID: f.completedAccess}, f.looseID)
	require.NoError(t, err)
	assert.Equal(t, "Saved loose title", before.Title)

	second := f.publish(t, updated.Version)
	assert.Equal(t, int64(2), second.Revision)
	after, err := f.service.RecipientLooseItem(context.Background(), setup.SessionActor{PersonID: f.completedPerson, AccessID: f.completedAccess}, f.looseID)
	require.NoError(t, err)
	assert.Equal(t, "Corrected title", after.Title)
	assert.NotEqual(t, first.ID, second.ID)

	withdrawal, err := f.service.Withdraw(context.Background(), f.actor, WithdrawRequest{
		TargetKind: WithdrawalTargetLooseItem, TargetID: f.looseID.String(), Reason: "Privacy correction",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, withdrawal.AffectedRecipientCount)
	_, err = f.service.RecipientLooseItem(context.Background(), setup.SessionActor{PersonID: f.completedPerson, AccessID: f.completedAccess}, f.looseID)
	assert.ErrorIs(t, err, ErrNoPublication)

	item, err := f.service.GetLooseItem(context.Background(), f.actor, f.looseID)
	require.NoError(t, err)
	_, err = f.service.PublishLooseItem(context.Background(), f.actor, f.looseID, PublishLooseItemRequest{Version: item.Version})
	assert.ErrorIs(t, err, ErrPublicationNotReady)

	// Audience review_version is intentionally independent from editable version.
	var reviewVersion int64
	require.NoError(t, f.db.NewRaw(`SELECT review_version FROM loose_items WHERE id=?`, f.looseID).Scan(context.Background(), &reviewVersion))
	review, err := f.audiences.Recalculate(context.Background(), f.actor, "loose_item", f.looseID, reviewVersion)
	require.NoError(t, err)
	review, err = f.audiences.SetOverride(context.Background(), f.actor, "loose_item", f.looseID, review.Version,
		audiences.OverrideRequest{RecipientPersonID: f.completedPerson.String(), State: "included"})
	require.NoError(t, err)
	_, err = f.audiences.Approve(context.Background(), f.actor, "loose_item", f.looseID, review.Version)
	require.NoError(t, err)
	item, err = f.service.GetLooseItem(context.Background(), f.actor, f.looseID)
	require.NoError(t, err)
	f.publish(t, item.Version)
	_, err = f.service.RecipientLooseItem(context.Background(), setup.SessionActor{PersonID: f.completedPerson, AccessID: f.completedAccess}, f.looseID)
	require.NoError(t, err)
}

func TestLoosePublicationDeniesSourceMissingMedia(t *testing.T) {
	f := newLoosePublicationFixture(t, true)
	_, err := f.db.NewRaw(`UPDATE media_items SET availability = 'source_missing', missing_since = ? WHERE id = ?`, f.now, f.mediaID).Exec(context.Background())
	require.NoError(t, err)

	_, err = f.service.PublishLooseItem(context.Background(), f.actor, f.looseID, PublishLooseItemRequest{Version: 4})
	assert.ErrorIs(t, err, ErrMediaUnavailable)

	var publicationCount int
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM publications WHERE loose_item_id = ?`, f.looseID).Scan(context.Background(), &publicationCount))
	assert.Zero(t, publicationCount)
}

func TestLooseMetadataStagingWaitsForAccessSummaryReplacement(t *testing.T) {
	f := newLoosePublicationFixture(t, true)
	f.publish(t, 4)
	ctx := context.Background()
	blocker, err := f.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = blocker.Rollback() }()
	var blockerPID int
	require.NoError(t, blocker.NewRaw(`SELECT pg_backend_pid()`).Scan(ctx, &blockerPID))
	require.NoError(t, staging.LockAccessSummaryReplacement(ctx, blocker))

	updated := make(chan error, 1)
	go func() {
		_, updateErr := f.service.UpdateLooseItem(ctx, f.actor, f.looseID, UpdateLooseItemRequest{
			Version: 4, Title: "Serialized correction", Description: "Private", GroupingTimezone: "UTC",
			ProposedDay: ptr("2026-08-01"), PlaceLabels: []string{"Garden"},
		})
		updated <- updateErr
	}()
	testdb.WaitForBlockedQueries(t, ctx, f.db, blockerPID, `%pg_advisory_xact_lock_shared%`, 1)
	require.NoError(t, blocker.Commit())
	require.NoError(t, testdb.WaitForErrorResult(t, updated, "Loose metadata update after access-summary replacement"))
}

func TestLoosePublicationSerializesConcurrentAccessGenerationChange(t *testing.T) {
	f := newLoosePublicationFixture(t, true)
	reached := make(chan struct{})
	release := make(chan struct{})
	released := false
	t.Cleanup(func() {
		if !released {
			close(release)
		}
	})
	f.service.failPublicationStep = func(step PublicationStep) error {
		if step == PublicationStepValidated {
			close(reached)
			<-release
		}
		return nil
	}
	publicationDone := make(chan error, 1)
	go func() {
		_, publishErr := f.service.PublishLooseItem(context.Background(), f.actor, f.looseID,
			PublishLooseItemRequest{Version: 4})
		publicationDone <- publishErr
	}()
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("Publication did not lock its Audience generations")
	}

	lockErr := f.db.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `SET LOCAL lock_timeout = '100ms'`); err != nil {
			return err
		}
		_, err := tx.NewRaw(`UPDATE recipient_access_generations SET is_current = false WHERE id = ?`, f.completedAccess).Exec(ctx)
		return err
	})
	require.Error(t, lockErr)
	assert.Contains(t, lockErr.Error(), "lock timeout")

	close(release)
	released = true
	select {
	case err := <-publicationDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Publication did not commit after the access change was released")
	}
	f.service.failPublicationStep = nil
}

func TestLoosePublicationAndWithdrawalSerializeInBothOrders(t *testing.T) {
	waitBlocked := func(t *testing.T, db *bun.DB) {
		t.Helper()
		require.Eventually(t, func() bool {
			var blocked bool
			err := db.NewRaw(`SELECT EXISTS (SELECT 1 FROM pg_stat_activity
				WHERE datname = current_database() AND pid <> pg_backend_pid()
				 AND cardinality(pg_blocking_pids(pid)) > 0
				 AND query LIKE '%pg_advisory_xact_lock%')`).Scan(context.Background(), &blocked)
			return err == nil && blocked
		}, 5*time.Second, 10*time.Millisecond)
	}

	t.Run("Withdrawal commits first", func(t *testing.T) {
		f := newLoosePublicationFixture(t, true)
		f.publish(t, 4)
		updated, err := f.service.UpdateLooseItem(context.Background(), f.actor, f.looseID, UpdateLooseItemRequest{
			Version: 4, Title: "Concurrent correction", Description: "Private", GroupingTimezone: "UTC",
			ProposedDay: ptr("2026-08-01"), PlaceLabels: []string{"Garden"},
		})
		require.NoError(t, err)
		reached, release := make(chan struct{}), make(chan struct{})
		f.service.failWithdrawalStep = func(step WithdrawalStep) error {
			if step == WithdrawalStepLocked {
				close(reached)
				<-release
			}
			return nil
		}
		withdrawn := make(chan error, 1)
		go func() {
			_, withdrawErr := f.service.Withdraw(context.Background(), f.actor, WithdrawRequest{
				TargetKind: WithdrawalTargetLooseItem, TargetID: f.looseID.String(), Reason: "Concurrent privacy change",
			})
			withdrawn <- withdrawErr
		}()
		<-reached
		published := make(chan error, 1)
		go func() {
			_, publishErr := f.service.PublishLooseItem(context.Background(), f.actor, f.looseID,
				PublishLooseItemRequest{Version: updated.Version})
			published <- publishErr
		}()
		waitBlocked(t, f.db)
		close(release)
		require.NoError(t, <-withdrawn)
		assert.ErrorIs(t, <-published, ErrVersionConflict)
	})

	t.Run("Publication commits first", func(t *testing.T) {
		f := newLoosePublicationFixture(t, true)
		f.publish(t, 4)
		updated, err := f.service.UpdateLooseItem(context.Background(), f.actor, f.looseID, UpdateLooseItemRequest{
			Version: 4, Title: "Concurrent correction", Description: "Private", GroupingTimezone: "UTC",
			ProposedDay: ptr("2026-08-01"), PlaceLabels: []string{"Garden"},
		})
		require.NoError(t, err)
		reached, release := make(chan struct{}), make(chan struct{})
		f.service.failPublicationStep = func(step PublicationStep) error {
			if step == PublicationStepLocked {
				close(reached)
				<-release
			}
			return nil
		}
		published := make(chan error, 1)
		go func() {
			_, publishErr := f.service.PublishLooseItem(context.Background(), f.actor, f.looseID,
				PublishLooseItemRequest{Version: updated.Version})
			published <- publishErr
		}()
		<-reached
		withdrawn := make(chan error, 1)
		go func() {
			_, withdrawErr := f.service.Withdraw(context.Background(), f.actor, WithdrawRequest{
				TargetKind: WithdrawalTargetLooseItem, TargetID: f.looseID.String(), Reason: "Concurrent privacy change",
			})
			withdrawn <- withdrawErr
		}()
		waitBlocked(t, f.db)
		close(release)
		require.NoError(t, <-published)
		require.NoError(t, <-withdrawn)
	})
}

func TestLoosePublicationNeverExposesMixedRevision(t *testing.T) {
	f := newLoosePublicationFixture(t, true)
	f.publish(t, 4)
	updated, err := f.service.UpdateLooseItem(context.Background(), f.actor, f.looseID, UpdateLooseItemRequest{
		Version: 4, Title: "Atomic corrected title", Description: "Private until commit", GroupingTimezone: "UTC",
		ProposedDay: ptr("2026-08-03"), PlaceLabels: []string{"Courtyard"},
	})
	require.NoError(t, err)

	reached := make(chan struct{})
	release := make(chan struct{})
	f.service.failPublicationStep = func(step PublicationStep) error {
		if step == PublicationStepMetadata {
			close(reached)
			<-release
		}
		return nil
	}
	publicationDone := make(chan error, 1)
	go func() {
		_, publishErr := f.service.PublishLooseItem(context.Background(), f.actor, f.looseID,
			PublishLooseItemRequest{Version: updated.Version})
		publicationDone <- publishErr
	}()
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("Publication did not reach the current-projection boundary")
	}

	actor := setup.SessionActor{PersonID: f.completedPerson, AccessID: f.completedAccess}
	whileUncommitted, err := f.service.RecipientLooseItem(context.Background(), actor, f.looseID)
	require.NoError(t, err)
	assert.Equal(t, "Saved loose title", whileUncommitted.Title)
	assert.Equal(t, "Private draft correction", whileUncommitted.Description)

	close(release)
	select {
	case err := <-publicationDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Publication did not commit after the boundary was released")
	}
	f.service.failPublicationStep = nil

	afterCommit, err := f.service.RecipientLooseItem(context.Background(), actor, f.looseID)
	require.NoError(t, err)
	assert.Equal(t, "Atomic corrected title", afterCommit.Title)
	assert.Equal(t, "Private until commit", afterCommit.Description)
}

func TestLooseCorrectionPublicationRollsBackAtNamedBoundaries(t *testing.T) {
	for _, step := range []PublicationStep{
		PublicationStepLocked,
		PublicationStepValidated,
		PublicationStepHistory,
		PublicationStepAudiences,
		PublicationStepMetadata,
		PublicationStepEntitlements,
		PublicationStepActivity,
		PublicationStepAudit,
		PublicationStepOutbox,
		PublicationStepStaged,
	} {
		t.Run(string(step), func(t *testing.T) {
			f := newLoosePublicationFixture(t, true)
			first := f.publish(t, 4)
			updated, err := f.service.UpdateLooseItem(context.Background(), f.actor, f.looseID, UpdateLooseItemRequest{
				Version: 4, Title: "Uncommitted correction", Description: "Private correction",
				GroupingTimezone: "UTC", ProposedDay: ptr("2026-08-02"), PlaceLabels: []string{"Garden"},
			})
			require.NoError(t, err)
			var stagedID uuid.UUID
			require.NoError(t, f.db.NewRaw(`SELECT id FROM loose_staged_updates WHERE loose_item_id = ?`, f.looseID).
				Scan(context.Background(), &stagedID))
			injected := errors.New("injected boundary failure")
			f.service.failPublicationStep = func(candidate PublicationStep) error {
				if candidate == step {
					return injected
				}
				return nil
			}
			_, err = f.service.PublishLooseItem(context.Background(), f.actor, f.looseID,
				PublishLooseItemRequest{Version: updated.Version})
			require.ErrorIs(t, err, injected)

			var currentPublicationID, survivingStagedID uuid.UUID
			require.NoError(t, f.db.NewRaw(`SELECT current_publication_id, current_staged_update_id
				FROM loose_items WHERE id = ?`, f.looseID).Scan(context.Background(), &currentPublicationID, &survivingStagedID))
			assert.Equal(t, uuid.MustParse(first.ID), currentPublicationID)
			assert.Equal(t, stagedID, survivingStagedID)
			view, viewErr := f.service.RecipientLooseItem(context.Background(), setup.SessionActor{
				PersonID: f.completedPerson, AccessID: f.completedAccess,
			}, f.looseID)
			require.NoError(t, viewErr)
			assert.Equal(t, "Saved loose title", view.Title)
			for table, expected := range map[string]int{
				"publications": 1, "published_loose_item_revisions": 1,
				"current_published_loose_items": 1, "current_loose_item_entitlements": 2,
				"published_loose_search_documents": 1, "publication_audit_events": 1,
				"outbox_events": 1, "loose_staged_updates": 1,
			} {
				var count int
				require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM `+table).Scan(context.Background(), &count))
				assert.Equal(t, expected, count, table)
			}
		})
	}
}

func TestLooseCorrectionRejectsStaleVersionAndDuplicateRetry(t *testing.T) {
	f := newLoosePublicationFixture(t, true)
	f.publish(t, 4)
	updated, err := f.service.UpdateLooseItem(context.Background(), f.actor, f.looseID, UpdateLooseItemRequest{
		Version: 4, Title: "Retry-safe correction", Description: "Private", GroupingTimezone: "UTC",
		ProposedDay: ptr("2026-08-02"), PlaceLabels: []string{"Garden"},
	})
	require.NoError(t, err)
	_, err = f.service.PublishLooseItem(context.Background(), f.actor, f.looseID, PublishLooseItemRequest{Version: 4})
	assert.ErrorIs(t, err, ErrVersionConflict)
	second := f.publish(t, updated.Version)
	_, err = f.service.PublishLooseItem(context.Background(), f.actor, f.looseID,
		PublishLooseItemRequest{Version: updated.Version})
	assert.ErrorIs(t, err, ErrPublicationNotReady)
	var publications, outboxRows int
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM publications WHERE loose_item_id = ?`, f.looseID).
		Scan(context.Background(), &publications))
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM outbox_events WHERE aggregate_kind = 'loose_item_publication'
		AND aggregate_id = ?`, f.looseID.String()).Scan(context.Background(), &outboxRows))
	assert.Equal(t, 2, publications)
	assert.Equal(t, 2, outboxRows)
	assert.Equal(t, int64(2), second.Revision)
}

func TestLooseWithdrawalRollsBackAtNamedBoundaries(t *testing.T) {
	for _, step := range []WithdrawalStep{
		WithdrawalStepTargeted,
		WithdrawalStepLocked,
		WithdrawalStepRecorded,
		WithdrawalStepProjections,
		WithdrawalStepActivity,
		WithdrawalStepDelivery,
		WithdrawalStepReviews,
		WithdrawalStepAudit,
	} {
		t.Run(string(step), func(t *testing.T) {
			f := newLoosePublicationFixture(t, true)
			f.publish(t, 4)
			injected := errors.New("injected withdrawal failure")
			f.service.failWithdrawalStep = func(candidate WithdrawalStep) error {
				if candidate == step {
					return injected
				}
				return nil
			}
			_, err := f.service.Withdraw(context.Background(), f.actor, WithdrawRequest{
				TargetKind: WithdrawalTargetLooseItem, TargetID: f.looseID.String(), Reason: "Rollback proof",
			})
			require.ErrorIs(t, err, injected)
			var activeWithdrawal, entitlement, snapshot bool
			require.NoError(t, f.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM content_withdrawals
				WHERE target_kind = 'loose_item' AND target_id = ? AND restored_at IS NULL)`, f.looseID).Scan(context.Background(), &activeWithdrawal))
			require.NoError(t, f.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM current_loose_item_entitlements
				WHERE loose_item_id = ? AND recipient_access_generation_id = ?)`, f.looseID, f.completedAccess).Scan(context.Background(), &entitlement))
			require.NoError(t, f.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM current_audience_snapshots
				WHERE target_kind = 'loose_item' AND target_id = ?)`, f.looseID).Scan(context.Background(), &snapshot))
			assert.False(t, activeWithdrawal)
			assert.True(t, entitlement)
			assert.True(t, snapshot)
		})
	}
}

func TestEffectiveEntitlementsAndRestoreValidationRejectNonPublishedOrigins(t *testing.T) {
	t.Run("Loose item", func(t *testing.T) {
		f := newLoosePublicationFixture(t, true)
		f.publish(t, 4)
		authorized, err := mediaaccess.GenerationCanAccess(context.Background(), f.db, f.completedAccess, f.mediaID)
		require.NoError(t, err)
		assert.True(t, authorized)
		_, err = f.db.NewRaw(`UPDATE loose_items SET lifecycle = 'draft' WHERE id = ?`, f.looseID).
			Exec(context.Background())
		require.NoError(t, err)
		authorized, err = mediaaccess.GenerationCanAccess(context.Background(), f.db, f.completedAccess, f.mediaID)
		require.NoError(t, err)
		assert.False(t, authorized)
		_, err = restores.Validate(context.Background(), f.db)
		assert.ErrorIs(t, err, restores.ErrProjections)
	})

	t.Run("Event", func(t *testing.T) {
		f := newPublicationFixture(t)
		_, err := f.service.PublishEvent(context.Background(), f.actor, f.event, f.request())
		require.NoError(t, err)
		actor := f.actorFor("shared")
		authorized, err := mediaaccess.GenerationCanAccess(context.Background(), f.db, actor.AccessID, f.media[0])
		require.NoError(t, err)
		assert.True(t, authorized)
		_, err = f.db.NewRaw(`UPDATE events SET lifecycle = 'draft' WHERE id = ?`, f.event).Exec(context.Background())
		require.NoError(t, err)
		authorized, err = mediaaccess.GenerationCanAccess(context.Background(), f.db, actor.AccessID, f.media[0])
		require.NoError(t, err)
		assert.False(t, authorized)
		_, err = restores.Validate(context.Background(), f.db)
		assert.ErrorIs(t, err, restores.ErrProjections)
	})
}

func TestMediaWithdrawalSupportsLooseOnlyOriginAndRequiresFreshPublication(t *testing.T) {
	f := newLoosePublicationFixture(t, true)
	f.publish(t, 4)
	withdrawal, err := f.service.Withdraw(context.Background(), f.actor, WithdrawRequest{
		TargetKind: WithdrawalTargetMedia, TargetID: f.mediaID.String(), Reason: "Withdraw stable Media identity",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, withdrawal.AffectedMediaCount)
	assert.Zero(t, withdrawal.AffectedEventCount)
	_, err = f.service.Withdraw(context.Background(), f.actor, WithdrawRequest{
		TargetKind: WithdrawalTargetLooseItem, TargetID: f.looseID.String(), Reason: "Overlapping withdrawal",
	})
	assert.ErrorIs(t, err, ErrAlreadyWithdrawn)
	var activeWithdrawalCount int
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM content_withdrawals
		WHERE restored_at IS NULL AND ((target_kind = 'media' AND target_id = ?)
		 OR (target_kind = 'loose_item' AND target_id = ?))`, f.mediaID, f.looseID).Scan(context.Background(), &activeWithdrawalCount))
	assert.Equal(t, 1, activeWithdrawalCount)
	_, err = f.service.RecipientLooseItem(context.Background(), setup.SessionActor{
		PersonID: f.completedPerson, AccessID: f.completedAccess,
	}, f.looseID)
	assert.ErrorIs(t, err, ErrNoPublication)

	item, err := f.service.GetLooseItem(context.Background(), f.actor, f.looseID)
	require.NoError(t, err)
	assert.True(t, item.PendingWithdrawalPublication)
	assert.False(t, item.AudienceComplete)
	var reviewVersion int64
	require.NoError(t, f.db.NewRaw(`SELECT review_version FROM loose_items WHERE id = ?`, f.looseID).Scan(context.Background(), &reviewVersion))
	review, err := f.audiences.Recalculate(context.Background(), f.actor, "loose_item", f.looseID, reviewVersion)
	require.NoError(t, err)
	review, err = f.audiences.SetOverride(context.Background(), f.actor, "loose_item", f.looseID, review.Version,
		audiences.OverrideRequest{RecipientPersonID: f.completedPerson.String(), State: "included"})
	require.NoError(t, err)
	_, err = f.audiences.Approve(context.Background(), f.actor, "loose_item", f.looseID, review.Version)
	require.NoError(t, err)
	item, err = f.service.GetLooseItem(context.Background(), f.actor, f.looseID)
	require.NoError(t, err)
	f.publish(t, item.Version)
	_, err = f.service.RecipientLooseItem(context.Background(), setup.SessionActor{
		PersonID: f.completedPerson, AccessID: f.completedAccess,
	}, f.looseID)
	require.NoError(t, err)
}

func TestWithdrawingLooseOriginPreservesReusedEventAuthorization(t *testing.T) {
	f := newLoosePublicationFixture(t, true)
	f.publish(t, 4)
	eventID, momentID, eventPublicationID, snapshotID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err := f.db.NewRaw(`
		INSERT INTO events (id,lifecycle,title,grouping_timezone,current_publication_id) VALUES (?, 'published', 'Reused Event', 'UTC', NULL);
		INSERT INTO audience_snapshots (id,target_kind,target_id,approved_by_person_id,approved_at,label) VALUES (?, 'moment', ?, ?, ?, 'Shared');
		INSERT INTO publications (id,event_id,revision,editable_version,published_by_person_id,notify_recipients,committed_at,content_revision)
		VALUES (?, ?, 1, 1, ?, false, ?, 100);
		UPDATE events SET current_publication_id=? WHERE id=?;
		INSERT INTO published_moments (id,publication_id,draft_moment_id,audience_snapshot_id,position,title,proposed_day)
		VALUES (?, ?, ?, ?, 0, '', '2026-08-01');
		INSERT INTO current_published_events (event_id,publication_id,title,description,grouping_timezone,committed_at)
		VALUES (?, ?, 'Reused Event', '', 'UTC', ?);
		INSERT INTO current_published_placements (event_id,publication_id,published_moment_id,media_item_id,position)
		VALUES (?, ?, ?, ?, 0);
		INSERT INTO current_audience_entitlements
		(event_id,publication_id,recipient_person_id,recipient_access_generation_id,media_item_id)
		VALUES (?, ?, ?, ?, ?)
	`, eventID, snapshotID, momentID, f.actor.PersonID, f.now, eventPublicationID, eventID, f.actor.PersonID, f.now,
		eventPublicationID, eventID, momentID, eventPublicationID, momentID, snapshotID,
		eventID, eventPublicationID, f.now, eventID, eventPublicationID, momentID, f.mediaID,
		eventID, eventPublicationID, f.completedPerson, f.completedAccess, f.mediaID).Exec(context.Background())
	require.NoError(t, err)
	_, err = f.service.Withdraw(context.Background(), f.actor, WithdrawRequest{TargetKind: WithdrawalTargetLooseItem,
		TargetID: f.looseID.String(), Reason: "Withdraw independent share"})
	require.NoError(t, err)
	authorized, err := mediaaccess.GenerationCanAccess(context.Background(), f.db, f.completedAccess, f.mediaID)
	require.NoError(t, err)
	assert.True(t, authorized)
}

func TestSharedMediaRestorationRequeuesFirstOriginForDisjointAudienceInBothOrders(t *testing.T) {
	for _, firstLoose := range []bool{false, true} {
		name := "Event first"
		if firstLoose {
			name = "Loose first"
		}
		t.Run(name, func(t *testing.T) {
			f := newLoosePublicationFixture(t, true)
			f.publish(t, 4)
			eventID, eventPublicationID, loosePublicationID := uuid.New(), uuid.New(), uuid.New()
			finalPublicationID, firstPublicationID := loosePublicationID, eventPublicationID
			eventPersonID, eventAccessID := uuid.New(), uuid.New()
			firstAccessID := eventAccessID
			if firstLoose {
				finalPublicationID, firstPublicationID = eventPublicationID, loosePublicationID
				firstAccessID = f.completedAccess
			}
			_, err := f.db.NewRaw(`
				INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Event recipient', 'Event recipient');
				INSERT INTO person_roles (person_id, role) VALUES (?, 'recipient');
				INSERT INTO recipient_access_generations
				 (id, person_id, generation, state, is_current, onboarding_completed_at, created_at, updated_at)
				VALUES (?, ?, 1, 'completed', true, ?, ?, ?);
				INSERT INTO events (id, lifecycle, title, grouping_timezone) VALUES (?, 'published', 'Shared Event', 'UTC');
				INSERT INTO publications (id, event_id, revision, editable_version, published_by_person_id,
				 notify_recipients, committed_at, content_revision)
				VALUES (?, ?, 1, 1, ?, true, ?, 101);
				INSERT INTO publications (id, loose_item_id, revision, editable_version, prior_publication_id,
				 published_by_person_id, notify_recipients, committed_at, content_revision)
				VALUES (?, ?, 2, 5, (SELECT current_publication_id FROM loose_items WHERE id = ?),
				 ?, true, ?, 102);
				UPDATE current_loose_item_entitlements SET publication_id = ? WHERE loose_item_id = ?;
				INSERT INTO current_audience_entitlements
				 (event_id, publication_id, recipient_person_id, recipient_access_generation_id, media_item_id)
				VALUES (?, ?, ?, ?, ?);
				INSERT INTO content_withdrawals (id, target_kind, target_id, reason, withdrawn_by_person_id,
				 withdrawn_at, restored_by_publication_id, restored_at, content_revision)
				VALUES (gen_random_uuid(), 'media', ?, 'Shared restoration', ?, ?, ?, ?, 100);
				INSERT INTO outbox_events (kind, aggregate_kind, aggregate_id, aggregate_version,
				 payload, available_at, delivered_at, created_at)
				SELECT 'publication_committed',
				 CASE WHEN event_id IS NOT NULL THEN 'event_publication' ELSE 'loose_item_publication' END,
				 COALESCE(event_id, loose_item_id)::text, 50,
				 jsonb_build_object('publication_id', id), ?, ?, ? FROM publications WHERE id = ?;
				INSERT INTO new_for_you_entries
				 (recipient_access_generation_id, publication_id, seen_at) VALUES (?, ?, ?);
				INSERT INTO jobs (kind, payload, status, lease_owner, lease_expires_at)
				VALUES ('publication_committed', jsonb_build_object('publication_id', ?),
				 'running', 'restoration-race-test', clock_timestamp() + interval '1 hour')
			`, eventPersonID, eventPersonID, eventAccessID, eventPersonID, f.now, f.now, f.now,
				eventID, eventPublicationID, eventID, f.actor.PersonID, f.now,
				loosePublicationID, f.looseID, f.looseID, f.actor.PersonID, f.now,
				loosePublicationID, f.looseID,
				eventID, eventPublicationID, eventPersonID, firstAccessID, f.mediaID,
				f.mediaID, f.actor.PersonID, f.now, finalPublicationID, f.now,
				f.now, f.now, f.now, firstPublicationID,
				firstAccessID, firstPublicationID, f.now, firstPublicationID).Exec(context.Background())
			require.NoError(t, err)
			require.NoError(t, f.db.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
				if _, createErr := tx.ExecContext(ctx, `CREATE TEMPORARY TABLE memento_prior_effective_entitlements
					(access_id uuid, media_item_id uuid) ON COMMIT DROP`); createErr != nil {
					return createErr
				}
				return projectDeferredRestorationActivity(ctx, tx, finalPublicationID, f.now)
			}))
			var outboxes, activity, newForYou int
			var unseen bool
			require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM outbox_events
				WHERE payload->>'publication_id' = ?`, firstPublicationID.String()).Scan(context.Background(), &outboxes))
			require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM publication_activity_items
				WHERE publication_id = ? AND recipient_access_generation_id = ?`, firstPublicationID, firstAccessID).
				Scan(context.Background(), &activity))
			require.NoError(t, f.db.NewRaw(`SELECT count(*), bool_and(seen_at IS NULL)
				FROM new_for_you_entries WHERE publication_id = ? AND recipient_access_generation_id = ?`,
				firstPublicationID, firstAccessID).Scan(context.Background(), &newForYou, &unseen))
			assert.Equal(t, 2, outboxes)
			assert.Equal(t, 1, activity)
			assert.Equal(t, 1, newForYou)
			assert.True(t, unseen)
		})
	}
}

func TestMediaRestorationHandlesEitherPublicationOrderWhenEventRemovesOrigin(t *testing.T) {
	for _, looseFirst := range []bool{false, true} {
		name := "Event removal before Loose Publication"
		if looseFirst {
			name = "Loose Publication before Event removal"
		}
		t.Run(name, func(t *testing.T) {
			f := newLoosePublicationFixture(t, true)
			f.publish(t, 4)
			eventID, momentID, eventPublicationID, snapshotID, publishedMomentID :=
				uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
			_, err := f.db.NewRaw(`
				INSERT INTO events (id, lifecycle, title, description, grouping_timezone, version,
					final_review_complete, created_at, updated_at)
				VALUES (?, 'published', 'Shared Event', '', 'UTC', 1, true, ?, ?);
				INSERT INTO draft_moments (id, event_id, position, proposed_day, grouping_timezone,
					source_days, title, cover_media_item_id, attendance_complete, audience_complete)
				VALUES (?, ?, 0, '2026-08-01', 'UTC', ARRAY['2026-08-01'::date],
					'Shared', ?, true, true);
				INSERT INTO draft_media_placements (event_id, media_item_id, draft_moment_id, position, created_at)
				VALUES (?, ?, ?, 0, ?);
				INSERT INTO audience_snapshots (id, target_kind, target_id, approved_by_person_id, approved_at, label)
				VALUES (?, 'moment', ?, ?, ?, 'Shared');
				INSERT INTO audience_snapshot_entries (snapshot_id, recipient_person_id, recipient_access_generation_id)
				VALUES (?, ?, ?);
				INSERT INTO current_audience_snapshots (target_kind, target_id, snapshot_id)
				VALUES ('moment', ?, ?);
				INSERT INTO publications (id, event_id, revision, editable_version, published_by_person_id,
					notify_recipients, committed_at, content_revision)
				VALUES (?, ?, 1, 1, ?, false, ?, 0);
				UPDATE events SET current_publication_id = ? WHERE id = ?;
				INSERT INTO published_event_revisions (publication_id, event_id, title, description,
					grouping_timezone, created_at)
				VALUES (?, ?, 'Shared Event', '', 'UTC', ?);
				INSERT INTO published_moments (id, publication_id, draft_moment_id, audience_snapshot_id,
					position, title, proposed_day, cover_media_item_id)
				VALUES (?, ?, ?, ?, 0, 'Shared', '2026-08-01', ?);
				INSERT INTO published_media_placements (published_moment_id, media_item_id, position,
					media_type, width, height, local_date_time)
				VALUES (?, ?, 0, 'image', 1200, 800, '2026-08-01T12:00:00Z');
				INSERT INTO current_published_events (event_id, publication_id, title, description,
					grouping_timezone, committed_at)
				VALUES (?, ?, 'Shared Event', '', 'UTC', ?);
				INSERT INTO current_published_placements (event_id, publication_id, published_moment_id,
					media_item_id, position)
				VALUES (?, ?, ?, ?, 0);
				INSERT INTO current_audience_entitlements (event_id, publication_id, recipient_person_id,
					recipient_access_generation_id, media_item_id)
				VALUES (?, ?, ?, ?, ?)
			`, eventID, f.now, f.now, momentID, eventID, f.mediaID,
				eventID, f.mediaID, momentID, f.now, snapshotID, momentID, f.actor.PersonID, f.now,
				snapshotID, f.completedPerson, f.completedAccess, momentID, snapshotID,
				eventPublicationID, eventID, f.actor.PersonID, f.now, eventPublicationID, eventID,
				eventPublicationID, eventID, f.now, publishedMomentID, eventPublicationID, momentID,
				snapshotID, f.mediaID, publishedMomentID, f.mediaID,
				eventID, eventPublicationID, f.now, eventID, eventPublicationID, publishedMomentID, f.mediaID,
				eventID, eventPublicationID, f.completedPerson, f.completedAccess, f.mediaID).Exec(context.Background())
			require.NoError(t, err)

			withdrawal, err := f.service.Withdraw(context.Background(), f.actor, WithdrawRequest{
				TargetKind: WithdrawalTargetMedia, TargetID: f.mediaID.String(), Reason: "Review both origins",
			})
			require.NoError(t, err)
			isActive := func() bool {
				var active bool
				require.NoError(t, f.db.NewRaw(`SELECT restored_at IS NULL FROM content_withdrawals WHERE id = ?`,
					withdrawal.ID).Scan(context.Background(), &active))
				return active
			}
			publishLoose := func() {
				var reviewVersion int64
				require.NoError(t, f.db.NewRaw(`SELECT review_version FROM loose_items WHERE id = ?`, f.looseID).
					Scan(context.Background(), &reviewVersion))
				review, reviewErr := f.audiences.Recalculate(context.Background(), f.actor, "loose_item", f.looseID, reviewVersion)
				require.NoError(t, reviewErr)
				review, reviewErr = f.audiences.SetOverride(context.Background(), f.actor, "loose_item", f.looseID,
					review.Version, audiences.OverrideRequest{RecipientPersonID: f.completedPerson.String(), State: "included"})
				require.NoError(t, reviewErr)
				_, reviewErr = f.audiences.Approve(context.Background(), f.actor, "loose_item", f.looseID, review.Version)
				require.NoError(t, reviewErr)
				item, itemErr := f.service.GetLooseItem(context.Background(), f.actor, f.looseID)
				require.NoError(t, itemErr)
				f.publish(t, item.Version)
			}
			removeEventOrigin := func() {
				newPublicationID := uuid.New()
				require.NoError(t, f.db.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
					var contentRevision int64
					if err := tx.NewRaw(`UPDATE system_settings SET content_revision = content_revision + 1
						WHERE id = 1 RETURNING content_revision`).Scan(ctx, &contentRevision); err != nil {
						return err
					}
					if _, err := tx.NewRaw(`INSERT INTO publications (id, event_id, revision, editable_version,
						prior_publication_id, published_by_person_id, notify_recipients, committed_at, content_revision)
						VALUES (?, ?, 2, 2, ?, ?, false, ?, ?)`, newPublicationID, eventID,
						eventPublicationID, f.actor.PersonID, f.now, contentRevision).Exec(ctx); err != nil {
						return err
					}
					if _, err := tx.NewRaw(`INSERT INTO published_event_revisions
						(publication_id, event_id, title, description, grouping_timezone, created_at)
						VALUES (?, ?, 'Shared Event', '', 'UTC', ?);
						UPDATE events SET current_publication_id = ? WHERE id = ?;
						UPDATE current_published_events SET publication_id = ? WHERE event_id = ?;
						DELETE FROM current_audience_entitlements WHERE event_id = ?;
						DELETE FROM current_published_placements WHERE event_id = ?`, newPublicationID, eventID, f.now,
						newPublicationID, eventID, newPublicationID, eventID, eventID, eventID).Exec(ctx); err != nil {
						return err
					}
					return restoreEligibleWithdrawals(ctx, tx, eventID, newPublicationID, f.now, f.actor)
				}))
			}

			if looseFirst {
				publishLoose()
				assert.True(t, isActive(), "stale Event origin keeps Media withdrawn")
				removeEventOrigin()
			} else {
				removeEventOrigin()
				assert.True(t, isActive(), "stale Loose origin keeps Media withdrawn")
				publishLoose()
			}
			assert.False(t, isActive(), "the final advanced or removed origin restores Media")
		})
	}
}

func ptr(value string) *string { return &value }
