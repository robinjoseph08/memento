//go:build integration

package events

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type publicationFixture struct {
	db      *bun.DB
	service *Service
	actor   setup.CuratorSession
	event   uuid.UUID
	moments []uuid.UUID
	media   []uuid.UUID
	people  map[string]uuid.UUID
	access  map[string]uuid.UUID
}

func newPublicationFixture(t *testing.T) publicationFixture {
	t.Helper()
	ctx := context.Background()
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(ctx, db))
	now := time.Date(2026, 7, 27, 10, 11, 12, 0, time.UTC)
	fixture := publicationFixture{
		db: db, event: uuid.New(), moments: []uuid.UUID{uuid.New(), uuid.New(), uuid.New()},
		media: []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}, people: make(map[string]uuid.UUID),
		access: make(map[string]uuid.UUID),
	}
	fixture.actor = setup.CuratorSession{PersonID: uuid.New(), SessionID: uuid.New()}
	curatorAccessID := uuid.New()
	_, err := db.NewRaw(`
		UPDATE system_settings SET setup_complete = true WHERE id = 1;
		INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Curator', 'curator');
		INSERT INTO person_roles (person_id, role) VALUES (?, 'curator');
		INSERT INTO recipient_access_generations (id, person_id, generation, state, onboarding_completed_at)
		VALUES (?, ?, 1, 'completed', ?);
		INSERT INTO sessions (
			id, credential_hash, person_id, recipient_access_generation_id,
			security_epoch, session_type, idle_expires_at
		) SELECT ?, decode(repeat('42', 32), 'hex'), ?, ?, security_epoch, 'trusted', ?::timestamptz + interval '1 hour'
		FROM system_settings WHERE id = 1
	`, fixture.actor.PersonID, fixture.actor.PersonID, curatorAccessID, fixture.actor.PersonID, now,
		fixture.actor.SessionID, fixture.actor.PersonID, curatorAccessID, now).Exec(ctx)
	require.NoError(t, err)
	for _, recipient := range []struct{ name, state string }{{"shared", "completed"}, {"pending", "pending"}, {"hidden", "completed"}, {"none", "completed"}} {
		personID, accessID := uuid.New(), uuid.New()
		fixture.people[recipient.name], fixture.access[recipient.name] = personID, accessID
		_, err := db.NewRaw(`
			INSERT INTO people (id, display_name, sort_name) VALUES (?, ?, ?);
			INSERT INTO person_roles (person_id, role) VALUES (?, 'recipient');
			INSERT INTO recipient_access_generations (
				id, person_id, generation, state, is_current, onboarding_completed_at, created_at, updated_at
			) VALUES (?, ?, 1, ?, true, CASE WHEN ? = 'completed' THEN ?::timestamptz ELSE NULL END, ?, ?)
		`, personID, recipient.name, recipient.name, personID, accessID, personID,
			recipient.state, recipient.state, now, now, now).Exec(ctx)
		require.NoError(t, err)
	}
	_, err = db.NewRaw(`
		INSERT INTO events (
			id, lifecycle, title, description, grouping_timezone, version,
			final_review_complete, created_at, updated_at
		) VALUES (?, 'draft', 'Family weekend', 'A private Event', 'UTC', 7, true, ?, ?)
	`, fixture.event, now, now).Exec(ctx)
	require.NoError(t, err)
	for index := range fixture.media {
		_, err := db.NewRaw(`
			INSERT INTO media_items (
				id, immich_asset_id, media_type, width, height, local_date_time,
				first_seen_at, last_seen_at
			) VALUES (?, ?, 'image', 1200, 800, ?, ?, ?);
			INSERT INTO draft_moments (
				id, event_id, position, proposed_day, grouping_timezone, source_days,
				title, cover_media_item_id, attendance_complete, audience_complete
			) VALUES (?, ?, ?, ?::date, 'UTC', ARRAY[?::date], ?, ?, true, true);
			INSERT INTO draft_media_placements (
				event_id, media_item_id, draft_moment_id, position, created_at
			) VALUES (?, ?, ?, ?, ?)
		`, fixture.media[index], uuid.New(), "2026-07-2"+string(rune('7'+index))+"T10:00:00Z", now, now,
			fixture.moments[index], fixture.event, index, "2026-07-2"+string(rune('7'+index)),
			"2026-07-2"+string(rune('7'+index)), "Moment", fixture.media[index],
			fixture.event, fixture.media[index], fixture.moments[index], index, now).Exec(ctx)
		require.NoError(t, err)
		snapshotID := uuid.New()
		_, err = db.NewRaw(`
			INSERT INTO audience_snapshots (
				id, target_kind, target_id, approved_by_person_id, approved_at, label
			) VALUES (?, 'moment', ?, ?, ?, ?);
			INSERT INTO current_audience_snapshots (target_kind, target_id, snapshot_id)
			VALUES ('moment', ?, ?)
		`, snapshotID, fixture.moments[index], fixture.actor.PersonID, now,
			map[bool]string{true: "Shared", false: "Curator only"}[index < 2],
			fixture.moments[index], snapshotID).Exec(ctx)
		require.NoError(t, err)
		if index == 0 {
			for _, name := range []string{"shared", "pending"} {
				_, err = db.NewRaw(`INSERT INTO audience_snapshot_entries (snapshot_id, recipient_person_id, recipient_access_generation_id) VALUES (?, ?, ?)`, snapshotID, fixture.people[name], fixture.access[name]).Exec(ctx)
				require.NoError(t, err)
			}
		}
		if index == 1 {
			_, err = db.NewRaw(`INSERT INTO audience_snapshot_entries (snapshot_id, recipient_person_id, recipient_access_generation_id) VALUES (?, ?, ?)`, snapshotID, fixture.people["hidden"], fixture.access["hidden"]).Exec(ctx)
			require.NoError(t, err)
		}
	}
	fixture.service = New(db)
	fixture.service.now = func() time.Time { return now }
	return fixture
}

func (fixture publicationFixture) request() PublishEventRequest {
	return PublishEventRequest{Version: 7}
}

func (fixture publicationFixture) actorFor(name string) setup.SessionActor {
	return setup.SessionActor{PersonID: fixture.people[name], AccessID: fixture.access[name], SessionID: uuid.New()}
}

func TestPreviewRendersSavedEditableResultBeforePublication(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	previewRecipients, err := fixture.service.PreviewRecipients(ctx, fixture.event)
	require.NoError(t, err)
	assert.Len(t, previewRecipients.Recipients, 4)
	firstPreview, err := fixture.service.PreviewEvent(ctx, fixture.actor, fixture.event, fixture.people["shared"])
	require.NoError(t, err)
	assert.True(t, firstPreview.Authorized)
	assert.Empty(t, firstPreview.PublicationID)
	assert.Equal(t, "Family weekend", firstPreview.Title)
	require.Len(t, firstPreview.Media, 1)
	assert.Equal(t, fixture.media[0].String(), firstPreview.Media[0].ID)

	publication, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)
	_, err = fixture.db.NewRaw(`
		UPDATE events SET title = 'Proposed correction', version = 8 WHERE id = ?;
		DELETE FROM audience_snapshot_entries
		WHERE snapshot_id = (SELECT snapshot_id FROM current_audience_snapshots WHERE target_kind = 'moment' AND target_id = ?)
		  AND recipient_access_generation_id = ?;
		INSERT INTO audience_snapshot_entries (snapshot_id, recipient_person_id, recipient_access_generation_id)
		SELECT snapshot_id, ?, ? FROM current_audience_snapshots WHERE target_kind = 'moment' AND target_id = ?
	`, fixture.event, fixture.moments[0], fixture.access["shared"],
		fixture.people["shared"], fixture.access["shared"], fixture.moments[1]).Exec(ctx)
	require.NoError(t, err)

	proposed, err := fixture.service.PreviewEvent(ctx, fixture.actor, fixture.event, fixture.people["shared"])
	require.NoError(t, err)
	assert.Equal(t, publication.ID, proposed.PublicationID, "audit context retains the current Publication identity")
	assert.Equal(t, "Proposed correction", proposed.Title)
	require.Len(t, proposed.Media, 1)
	assert.Equal(t, fixture.media[1].String(), proposed.Media[0].ID)
	current, err := fixture.service.RecipientEvent(ctx, fixture.actorFor("shared"), fixture.event)
	require.NoError(t, err)
	assert.Equal(t, "Family weekend", current.Title, "Recipients keep the prior projection until Publication")
	require.Len(t, current.Media, 1)
	assert.Equal(t, fixture.media[0].String(), current.Media[0].ID)
	var versions []int64
	require.NoError(t, fixture.db.NewRaw(`SELECT editable_version FROM publication_preview_audit_events WHERE event_id = ? ORDER BY id`, fixture.event).Scan(ctx, &versions))
	assert.Equal(t, []int64{7, 8}, versions)
}

func TestPublicationBuildsImmutableHistoryAndFilteredCurrentProjections(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	publication, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)
	assert.Equal(t, int64(1), publication.Revision)
	assert.True(t, publication.NotifyRecipients, "notifications default on")
	require.NoError(t, fixture.service.HandlePublicationJob(ctx, worker.Job{Payload: []byte(`{"event_id":"` + fixture.event.String() + `","publication_id":"` + publication.ID + `"}`)}))
	unknownJob := worker.Job{Payload: []byte(`{"event_id":"` + fixture.event.String() + `","publication_id":"` + uuid.NewString() + `"}`)}
	require.EqualError(t, fixture.service.HandlePublicationJob(ctx, unknownJob), "unknown_publication")

	shared, err := fixture.service.RecipientEvent(ctx, fixture.actorFor("shared"), fixture.event)
	require.NoError(t, err)
	assert.True(t, shared.Authorized)
	assert.Equal(t, "Family weekend", shared.Title)
	assert.Equal(t, 1, shared.MediaCount)
	assert.Equal(t, fixture.media[0].String(), shared.Media[0].ID)
	require.NotNil(t, shared.CoverMediaID)
	assert.Equal(t, fixture.media[0].String(), *shared.CoverMediaID)
	assert.NotEmpty(t, shared.PublicationID)

	hidden, err := fixture.service.RecipientEvent(ctx, fixture.actorFor("hidden"), fixture.event)
	require.NoError(t, err)
	require.Len(t, hidden.Media, 1)
	assert.Equal(t, fixture.media[1].String(), hidden.Media[0].ID)
	assert.NotContains(t, hidden.Media[0].ID, fixture.media[2].String(), "empty Audience material has no Recipient-visible hint")

	_, err = fixture.service.RecipientEvent(ctx, fixture.actorFor("pending"), fixture.event)
	assert.ErrorIs(t, err, ErrNoPublication)
	previewRecipients, err := fixture.service.PreviewRecipients(ctx, fixture.event)
	require.NoError(t, err)
	assert.Len(t, previewRecipients.Recipients, 4)
	_, err = fixture.service.PreviewRecipients(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrNoPublication)

	preview, err := fixture.service.PreviewEvent(ctx, fixture.actor, fixture.event, fixture.people["pending"])
	require.NoError(t, err)
	assert.True(t, preview.Preview)
	assert.Equal(t, 1, preview.MediaCount, "Preview simulates the selected current Audience before Onboarding")
	assert.Equal(t, PreviewCapabilities{}, preview.Capabilities)
	var sharedSeenAt *time.Time
	var sharedActivityCount int
	require.NoError(t, fixture.db.NewRaw(`SELECT seen_at FROM new_for_you_entries WHERE recipient_access_generation_id = ?`, fixture.access["shared"]).Scan(ctx, &sharedSeenAt))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM publication_activity_items WHERE recipient_access_generation_id = ?`, fixture.access["shared"]).Scan(ctx, &sharedActivityCount))
	sharedPreview, err := fixture.service.PreviewEvent(ctx, fixture.actor, fixture.event, fixture.people["shared"])
	require.NoError(t, err)
	assert.True(t, sharedPreview.Authorized)
	var sharedSeenAfter *time.Time
	var sharedActivityAfter int
	require.NoError(t, fixture.db.NewRaw(`SELECT seen_at FROM new_for_you_entries WHERE recipient_access_generation_id = ?`, fixture.access["shared"]).Scan(ctx, &sharedSeenAfter))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM publication_activity_items WHERE recipient_access_generation_id = ?`, fixture.access["shared"]).Scan(ctx, &sharedActivityAfter))
	assert.Equal(t, sharedSeenAt, sharedSeenAfter, "Curator preview never marks Recipient engagement seen")
	assert.Equal(t, sharedActivityCount, sharedActivityAfter, "Curator preview never creates Recipient activity")
	emptyPreview, err := fixture.service.PreviewEvent(ctx, fixture.actor, fixture.event, fixture.people["none"])
	require.NoError(t, err)
	assert.False(t, emptyPreview.Authorized)
	assert.Empty(t, emptyPreview.Title, "an empty Audience must not reveal Event metadata")
	assert.Empty(t, emptyPreview.Media)
	assert.Equal(t, PreviewCapabilities{}, emptyPreview.Capabilities)
	var pendingNewForYou bool
	require.NoError(t, fixture.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM new_for_you_entries WHERE recipient_access_generation_id = ?)`, fixture.access["pending"]).Scan(ctx, &pendingNewForYou))
	assert.False(t, pendingNewForYou)

	work, err := fixture.service.ListEvents(ctx)
	require.NoError(t, err)
	require.Len(t, work.Events, 1, "published Events remain available for corrected revisions and Recipient preview")
	assert.Equal(t, "published", work.Events[0].Lifecycle)

	for table, expected := range map[string]int{
		"publications": 1, "published_event_revisions": 1, "published_moments": 3,
		"published_media_placements": 3, "audience_entries": 3,
		"current_published_events": 1, "current_published_placements": 3,
		"current_audience_entitlements": 3, "current_recipient_event_covers": 3,
		"new_for_you_entries": 2, "published_search_documents": 3,
		"publication_activity_items": 2, "publication_curator_activity_items": 1,
		"publication_audit_events": 1, "outbox_events": 1,
		"publication_preview_audit_events": 3,
	} {
		var count int
		require.NoError(t, fixture.db.NewRaw("SELECT count(*) FROM "+table).Scan(ctx, &count), table)
		assert.Equal(t, expected, count, table)
	}

	_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	assert.ErrorIs(t, err, ErrPublicationNotReady, "one editable version creates at most one Publication")

	replacementSnapshotID := uuid.New()
	_, err = fixture.db.NewRaw(`
		UPDATE events SET title = 'Corrected weekend', version = 8 WHERE id = ?;
		INSERT INTO audience_snapshots (id, target_kind, target_id, approved_by_person_id, approved_at, label)
		VALUES (?, 'moment', ?, ?, now(), 'Shared');
		INSERT INTO audience_snapshot_entries (snapshot_id, recipient_person_id, recipient_access_generation_id)
		VALUES (?, ?, ?);
		UPDATE current_audience_snapshots SET snapshot_id = ? WHERE target_kind = 'moment' AND target_id = ?;
		UPDATE draft_media_placements SET draft_moment_id = CASE
			WHEN media_item_id = ? THEN ?::uuid
			WHEN media_item_id = ? THEN ?::uuid
			ELSE draft_moment_id END
		WHERE event_id = ?
	`, fixture.event, replacementSnapshotID, fixture.moments[0], fixture.actor.PersonID,
		replacementSnapshotID, fixture.people["hidden"], fixture.access["hidden"], replacementSnapshotID, fixture.moments[0],
		fixture.media[0], fixture.moments[1], fixture.media[1], fixture.moments[0], fixture.event).Exec(ctx)
	require.NoError(t, err)
	second, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: 8})
	require.NoError(t, err)
	assert.Equal(t, int64(2), second.Revision)
	var historicalTitles []string
	require.NoError(t, fixture.db.NewRaw(`SELECT title FROM published_event_revisions ORDER BY created_at, publication_id`).Scan(ctx, &historicalTitles))
	assert.ElementsMatch(t, []string{"Family weekend", "Corrected weekend"}, historicalTitles)
	var historyMoments, historyPlacements, historyAudiences, currentPlacements, unseen int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM published_moments`).Scan(ctx, &historyMoments))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM published_media_placements`).Scan(ctx, &historyPlacements))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM audience_entries`).Scan(ctx, &historyAudiences))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM current_published_placements`).Scan(ctx, &currentPlacements))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM new_for_you_entries`).Scan(ctx, &unseen))
	assert.Equal(t, 6, historyMoments)
	assert.Equal(t, 6, historyPlacements)
	assert.Equal(t, 5, historyAudiences)
	assert.Equal(t, 3, currentPlacements)
	assert.Equal(t, 1, unseen, "only authorized completed Recipients receive current Publication candidates")
	var firstMomentMedia, secondMomentMedia uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`
		SELECT placement.media_item_id FROM published_media_placements AS placement
		JOIN published_moments AS moment ON moment.id = placement.published_moment_id
		WHERE moment.publication_id = ? AND moment.draft_moment_id = ?
	`, publication.ID, fixture.moments[0]).Scan(ctx, &firstMomentMedia))
	require.NoError(t, fixture.db.NewRaw(`
		SELECT placement.media_item_id FROM published_media_placements AS placement
		JOIN published_moments AS moment ON moment.id = placement.published_moment_id
		WHERE moment.publication_id = ? AND moment.draft_moment_id = ?
	`, second.ID, fixture.moments[0]).Scan(ctx, &secondMomentMedia))
	assert.Equal(t, fixture.media[0], firstMomentMedia, "revision 1 placement remains immutable")
	assert.Equal(t, fixture.media[1], secondMomentMedia, "revision 2 records the corrected placement")
	var firstAudience, secondAudience []uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`
		SELECT audience.recipient_person_id FROM audience_entries AS audience
		JOIN published_moments AS moment ON moment.id = audience.published_moment_id
		WHERE moment.publication_id = ? AND moment.draft_moment_id = ? ORDER BY audience.recipient_person_id
	`, publication.ID, fixture.moments[0]).Scan(ctx, &firstAudience))
	require.NoError(t, fixture.db.NewRaw(`
		SELECT audience.recipient_person_id FROM audience_entries AS audience
		JOIN published_moments AS moment ON moment.id = audience.published_moment_id
		WHERE moment.publication_id = ? AND moment.draft_moment_id = ? ORDER BY audience.recipient_person_id
	`, second.ID, fixture.moments[0]).Scan(ctx, &secondAudience))
	assert.ElementsMatch(t, []uuid.UUID{fixture.people["shared"], fixture.people["pending"]}, firstAudience)
	assert.Equal(t, []uuid.UUID{fixture.people["hidden"]}, secondAudience)
}

func TestPublicationSupportsManuallyPlacedUndatedMedia(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	_, err := fixture.db.NewRaw(`UPDATE media_items SET local_date_time = NULL WHERE id = ?`, fixture.media[0]).Exec(ctx)
	require.NoError(t, err)
	_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)
	view, err := fixture.service.RecipientEvent(ctx, fixture.actorFor("shared"), fixture.event)
	require.NoError(t, err)
	require.Len(t, view.Media, 1)
	assert.Nil(t, view.Media[0].LocalDateTime)
}

func TestPublishedCapturePresentationChangesOnlyWithANewPublication(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)
	_, err = fixture.db.NewRaw(`
		UPDATE media_items
		SET media_type = 'video', width = 1920, height = 1080,
		    local_date_time = '2030-01-02T03:04:05Z'
		WHERE id = ?;
		UPDATE events SET version = 8 WHERE id = ?
	`, fixture.media[0], fixture.event).Exec(ctx)
	require.NoError(t, err)

	prior, err := fixture.service.RecipientEvent(ctx, fixture.actorFor("shared"), fixture.event)
	require.NoError(t, err)
	require.Len(t, prior.Media, 1)
	assert.Equal(t, "image", prior.Media[0].MediaType)
	require.NotNil(t, prior.Media[0].Width)
	require.NotNil(t, prior.Media[0].Height)
	require.NotNil(t, prior.Media[0].LocalDateTime)
	assert.Equal(t, 1200, *prior.Media[0].Width)
	assert.Equal(t, 800, *prior.Media[0].Height)
	assert.Equal(t, "2026-07-27T10:00:00Z", *prior.Media[0].LocalDateTime)

	_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: 8})
	require.NoError(t, err)
	current, err := fixture.service.RecipientEvent(ctx, fixture.actorFor("shared"), fixture.event)
	require.NoError(t, err)
	require.Len(t, current.Media, 1)
	assert.Equal(t, "video", current.Media[0].MediaType)
	require.NotNil(t, current.Media[0].Width)
	require.NotNil(t, current.Media[0].Height)
	require.NotNil(t, current.Media[0].LocalDateTime)
	assert.Equal(t, 1920, *current.Media[0].Width)
	assert.Equal(t, 1080, *current.Media[0].Height)
	assert.Equal(t, "2030-01-02T03:04:05Z", *current.Media[0].LocalDateTime)
}

func TestPublicationPersistsNotificationChoiceAndSelectsASafeAvailableCover(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	var secondSnapshot uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT snapshot_id FROM current_audience_snapshots WHERE target_kind = 'moment' AND target_id = ?`, fixture.moments[1]).Scan(ctx, &secondSnapshot))
	_, err := fixture.db.NewRaw(`
		INSERT INTO audience_snapshot_entries (snapshot_id, recipient_person_id, recipient_access_generation_id)
		VALUES (?, ?, ?);
		UPDATE media_items SET availability = 'source_missing' WHERE id = ?
	`, secondSnapshot, fixture.people["shared"], fixture.access["shared"], fixture.media[0]).Exec(ctx)
	require.NoError(t, err)
	notify := false
	publication, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: 7, NotifyRecipients: &notify})
	require.NoError(t, err)
	assert.False(t, publication.NotifyRecipients)
	var storedNotify bool
	var outboxKind string
	var outboxVersion int64
	var outboxPayload string
	require.NoError(t, fixture.db.NewRaw(`SELECT notify_recipients FROM publications WHERE id = ?`, publication.ID).Scan(ctx, &storedNotify))
	require.NoError(t, fixture.db.NewRaw(`SELECT kind, aggregate_version, payload::text FROM outbox_events WHERE aggregate_id = ?`, fixture.event.String()).Scan(ctx, &outboxKind, &outboxVersion, &outboxPayload))
	assert.False(t, storedNotify)
	assert.Equal(t, PublicationJobKind, outboxKind)
	assert.Equal(t, int64(1), outboxVersion)
	assert.JSONEq(t, `{"event_id":"`+fixture.event.String()+`","notify_recipients":false,"publication_id":"`+publication.ID+`"}`, outboxPayload)
	var searchText string
	require.NoError(t, fixture.db.NewRaw(`SELECT search_text FROM published_search_documents WHERE recipient_access_generation_id = ? AND media_item_id = ?`, fixture.access["shared"], fixture.media[1]).Scan(ctx, &searchText))
	assert.Contains(t, searchText, "Family weekend")
	assert.Contains(t, searchText, "A private Event")
	view, err := fixture.service.RecipientEvent(ctx, fixture.actorFor("shared"), fixture.event)
	require.NoError(t, err)
	require.NotNil(t, view.CoverMediaID)
	assert.Equal(t, fixture.media[1].String(), *view.CoverMediaID, "an available authorized Media item wins over an unavailable preferred cover")
}

func TestPublicationRejectsStaleAndUnreviewedEditableState(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	assertUnpublished := func() {
		t.Helper()
		var count int
		require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM publications`).Scan(ctx, &count))
		assert.Zero(t, count)
	}

	_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: 6})
	assert.ErrorIs(t, err, ErrVersionConflict)
	assertUnpublished()

	_, err = fixture.db.NewRaw(`UPDATE draft_moments SET audience_complete = false WHERE id = ?`, fixture.moments[0]).Exec(ctx)
	require.NoError(t, err)
	_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	assert.ErrorIs(t, err, ErrPublicationNotReady)
	assertUnpublished()
	_, err = fixture.db.NewRaw(`UPDATE draft_moments SET audience_complete = true WHERE id = ?`, fixture.moments[0]).Exec(ctx)
	require.NoError(t, err)

	_, err = fixture.db.NewRaw(`UPDATE events SET final_review_complete = false WHERE id = ?`, fixture.event).Exec(ctx)
	require.NoError(t, err)
	_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	assert.ErrorIs(t, err, ErrPublicationNotReady)
	assertUnpublished()
	_, err = fixture.db.NewRaw(`UPDATE events SET final_review_complete = true WHERE id = ?`, fixture.event).Exec(ctx)
	require.NoError(t, err)

	_, err = fixture.db.NewRaw(`UPDATE draft_media_placements SET draft_moment_id = NULL WHERE media_item_id = ?`, fixture.media[0]).Exec(ctx)
	require.NoError(t, err)
	_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	assert.ErrorIs(t, err, ErrPublicationNotReady)
	assertUnpublished()
	_, err = fixture.db.NewRaw(`UPDATE draft_media_placements SET draft_moment_id = ? WHERE media_item_id = ?`, fixture.moments[0], fixture.media[0]).Exec(ctx)
	require.NoError(t, err)

	var snapshotID uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT snapshot_id FROM current_audience_snapshots WHERE target_kind = 'moment' AND target_id = ?`, fixture.moments[0]).Scan(ctx, &snapshotID))
	_, err = fixture.db.NewRaw(`DELETE FROM current_audience_snapshots WHERE target_kind = 'moment' AND target_id = ?`, fixture.moments[0]).Exec(ctx)
	require.NoError(t, err)
	_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	assert.ErrorIs(t, err, ErrPublicationNotReady)
	assertUnpublished()
	_, err = fixture.db.NewRaw(`INSERT INTO current_audience_snapshots (target_kind, target_id, snapshot_id) VALUES ('moment', ?, ?)`, fixture.moments[0], snapshotID).Exec(ctx)
	require.NoError(t, err)

	for _, accessState := range []struct {
		name      string
		state     string
		isCurrent bool
	}{
		{name: "suspended", state: "suspended", isCurrent: true},
		{name: "revoked", state: "revoked", isCurrent: true},
		{name: "superseded", state: "completed", isCurrent: false},
	} {
		t.Run(accessState.name, func(t *testing.T) {
			_, err = fixture.db.NewRaw(`UPDATE recipient_access_generations SET state = ?, is_current = ?, ended_at = CASE WHEN ? = 'revoked' THEN now() ELSE NULL END WHERE id = ?`, accessState.state, accessState.isCurrent, accessState.state, fixture.access["shared"]).Exec(ctx)
			require.NoError(t, err)
			_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
			assert.ErrorIs(t, err, ErrAudienceNotCurrent)
			assertUnpublished()
			_, err = fixture.db.NewRaw(`UPDATE recipient_access_generations SET state = 'completed', is_current = true, ended_at = NULL WHERE id = ?`, fixture.access["shared"]).Exec(ctx)
			require.NoError(t, err)
		})
	}

	_, err = fixture.db.NewRaw(`UPDATE events SET lifecycle = 'withdrawn' WHERE id = ?`, fixture.event).Exec(ctx)
	require.NoError(t, err)
	_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	assert.ErrorIs(t, err, ErrPublicationNotReady)
	assertUnpublished()
}

func TestOrganizationChangesInvalidateReviewedAudienceAndFinalReview(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	firstCover, secondCover, thirdCover := fixture.media[1].String(), fixture.media[0].String(), fixture.media[2].String()
	organized, err := fixture.service.OrganizeEvent(ctx, fixture.actor, fixture.event, OrganizeEventRequest{
		Version: 7,
		Moments: []OrganizeMoment{
			{ID: fixture.moments[0].String(), Title: "Moment", ProposedDay: "2026-07-27", CoverMediaItemID: &firstCover, MediaItemIDs: []string{fixture.media[1].String()}},
			{ID: fixture.moments[1].String(), Title: "Moment", ProposedDay: "2026-07-28", CoverMediaItemID: &secondCover, MediaItemIDs: []string{fixture.media[0].String()}},
			{ID: fixture.moments[2].String(), Title: "Moment", ProposedDay: "2026-07-29", CoverMediaItemID: &thirdCover, MediaItemIDs: []string{fixture.media[2].String()}},
		},
		FinalReviewComplete: true,
	})
	require.NoError(t, err)
	assert.False(t, organized.FinalReviewComplete)
	assert.False(t, organized.Moments[0].AttendanceComplete)
	assert.False(t, organized.Moments[0].AudienceComplete)
	assert.False(t, organized.Moments[1].AttendanceComplete)
	assert.False(t, organized.Moments[1].AudienceComplete)
	_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: organized.Version})
	assert.ErrorIs(t, err, ErrPublicationNotReady)
}

func TestFailureAtEveryPublicationBoundaryRollsBackEverything(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	steps := []PublicationStep{
		PublicationStepLocked, PublicationStepValidated, PublicationStepHistory,
		PublicationStepMetadata, PublicationStepPlacements, PublicationStepAudiences,
		PublicationStepEntitlements, PublicationStepActivity, PublicationStepAudit,
		PublicationStepOutbox, PublicationStepStaged,
	}
	publicationTables := []string{
		"publications", "published_event_revisions", "published_moments",
		"published_media_placements", "audience_entries", "current_published_events",
		"current_published_placements", "current_audience_entitlements",
		"current_recipient_event_covers", "new_for_you_entries", "published_search_documents",
		"publication_activity_items", "publication_curator_activity_items",
		"publication_audit_events", "outbox_events",
	}
	snapshotTables := func(tables []string) map[string]string {
		state := make(map[string]string, len(tables))
		for _, table := range tables {
			var serialized string
			require.NoError(t, fixture.db.NewRaw(fmt.Sprintf(`
				SELECT COALESCE(
					jsonb_agg(to_jsonb(row_value) ORDER BY to_jsonb(row_value)::text),
					'[]'::jsonb
				)::text FROM %s AS row_value
			`, table)).Scan(ctx, &serialized), table)
			state[table] = serialized
		}
		return state
	}
	injected := errors.New("injected Publication failure")
	for _, failedStep := range steps {
		fixture.service.failPublicationStep = func(step PublicationStep) error {
			if step == failedStep {
				return injected
			}
			return nil
		}
		_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
		assert.ErrorIs(t, err, injected, failedStep)
		for _, table := range publicationTables {
			var count int
			require.NoError(t, fixture.db.NewRaw("SELECT count(*) FROM "+table).Scan(ctx, &count))
			assert.Zero(t, count, "%s after %s", table, failedStep)
		}
		var lifecycle string
		require.NoError(t, fixture.db.NewRaw(`SELECT lifecycle FROM events WHERE id = ?`, fixture.event).Scan(ctx, &lifecycle))
		assert.Equal(t, "draft", lifecycle)
	}
	fixture.service.failPublicationStep = nil
	_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)
	_, err = fixture.db.NewRaw(`UPDATE events SET title = 'Replacement candidate', version = 8 WHERE id = ?`, fixture.event).Exec(ctx)
	require.NoError(t, err)
	replacementTables := append([]string{"events"}, publicationTables...)
	priorState := snapshotTables(replacementTables)
	for _, failedStep := range steps {
		fixture.service.failPublicationStep = func(step PublicationStep) error {
			if step == failedStep {
				return injected
			}
			return nil
		}
		_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: 8})
		assert.ErrorIs(t, err, injected, failedStep)
		assert.Equal(t, priorState, snapshotTables(replacementTables), "failed replacement preserves all history and projections at %s", failedStep)
	}
	fixture.service.failPublicationStep = nil
}

func TestStagedUpdateCoalescesConcurrentEditsRetriesAndCancellation(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)

	start := make(chan struct{})
	errorsByEdit := make(chan error, 2)
	for _, title := range []string{"Concurrent title A", "Concurrent title B"} {
		title := title
		go func() {
			<-start
			errorsByEdit <- fixture.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				if _, err := tx.NewRaw(`UPDATE events SET title = ?, version = version + 1 WHERE id = ?`, title, fixture.event).Exec(ctx); err != nil {
					return err
				}
				_, err := refreshStagedUpdate(ctx, tx, fixture.event, fixture.service.now().UTC())
				return err
			})
		}()
	}
	close(start)
	require.NoError(t, <-errorsByEdit)
	require.NoError(t, <-errorsByEdit)

	var stagedID uuid.UUID
	var stagedCount int
	require.NoError(t, fixture.db.NewRaw(`SELECT id FROM staged_updates WHERE event_id = ?`, fixture.event).Scan(ctx, &stagedID))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM staged_updates WHERE event_id = ?`, fixture.event).Scan(ctx, &stagedCount))
	assert.Equal(t, 1, stagedCount, "concurrent edits retain one mutable Staged update")
	require.NoError(t, fixture.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := refreshStagedUpdate(ctx, tx, fixture.event, fixture.service.now().UTC())
		return err
	}))
	var retriedID uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT id FROM staged_updates WHERE event_id = ?`, fixture.event).Scan(ctx, &retriedID))
	assert.Equal(t, stagedID, retriedID, "retry coalesces into the same mutable update")

	require.NoError(t, fixture.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`UPDATE events SET title = 'Family weekend', version = version + 1 WHERE id = ?`, fixture.event).Exec(ctx); err != nil {
			return err
		}
		update, err := refreshStagedUpdate(ctx, tx, fixture.event, fixture.service.now().UTC())
		require.Nil(t, update)
		return err
	}))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM staged_updates WHERE event_id = ?`, fixture.event).Scan(ctx, &stagedCount))
	assert.Zero(t, stagedCount, "cancelled changes leave no Staged work residue")
}

func TestNoNewMediaCorrectionIsQuietAndClearsStageAtomically(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)
	require.NoError(t, fixture.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`UPDATE events SET title = 'Quiet correction', version = 8, final_review_complete = true WHERE id = ?`, fixture.event).Exec(ctx); err != nil {
			return err
		}
		_, err := refreshStagedUpdate(ctx, tx, fixture.event, fixture.service.now().UTC())
		return err
	}))

	second, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: 8})
	require.NoError(t, err)
	var stagedCount, secondActivity, newForYou int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM staged_updates WHERE event_id = ?`, fixture.event).Scan(ctx, &stagedCount))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM publication_activity_items WHERE publication_id = ?`, second.ID).Scan(ctx, &secondActivity))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM new_for_you_entries`).Scan(ctx, &newForYou))
	assert.Zero(t, stagedCount)
	assert.Zero(t, secondActivity, "a correction that grants no new Media creates no Recipient activity")
	assert.Zero(t, newForYou, "a quiet correction does not become New for you")
}

func TestFailedPublicationPreservesPriorProjectionAndStagedUpdate(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)
	require.NoError(t, fixture.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`UPDATE events SET title = 'Rollback candidate', version = 8, final_review_complete = true WHERE id = ?`, fixture.event).Exec(ctx); err != nil {
			return err
		}
		_, err := refreshStagedUpdate(ctx, tx, fixture.event, fixture.service.now().UTC())
		return err
	}))
	var stagedID uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT id FROM staged_updates WHERE event_id = ?`, fixture.event).Scan(ctx, &stagedID))

	injected := errors.New("fail while clearing Staged update")
	fixture.service.failPublicationStep = func(step PublicationStep) error {
		if step == PublicationStepStaged {
			return injected
		}
		return nil
	}
	_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: 8})
	require.ErrorIs(t, err, injected)
	var retainedID uuid.UUID
	var publications, outbox int
	require.NoError(t, fixture.db.NewRaw(`SELECT id FROM staged_updates WHERE event_id = ?`, fixture.event).Scan(ctx, &retainedID))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM publications`).Scan(ctx, &publications))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM outbox_events`).Scan(ctx, &outbox))
	assert.Equal(t, stagedID, retainedID)
	assert.Equal(t, 1, publications)
	assert.Equal(t, 1, outbox)
	prior, err := fixture.service.RecipientEvent(ctx, fixture.actorFor("shared"), fixture.event)
	require.NoError(t, err)
	assert.Equal(t, "Family weekend", prior.Title)

	fixture.service.failPublicationStep = nil
	_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: 8})
	require.NoError(t, err)
	var stagedCount int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM staged_updates WHERE event_id = ?`, fixture.event).Scan(ctx, &stagedCount))
	assert.Zero(t, stagedCount)
}

func TestPublicationLocksReviewedAudienceAndAccessGenerationsUntilCommit(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	reached := make(chan struct{})
	release := make(chan struct{})
	fixture.service.failPublicationStep = func(step PublicationStep) error {
		if step == PublicationStepValidated {
			close(reached)
			<-release
		}
		return nil
	}
	published := make(chan error, 1)
	go func() {
		_, publishErr := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
		published <- publishErr
	}()
	<-reached

	assertLocked := func(name, statement string, argument uuid.UUID) {
		t.Helper()
		err := fixture.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			if _, err := tx.ExecContext(ctx, `SET LOCAL lock_timeout = '250ms'`); err != nil {
				return err
			}
			_, err := tx.NewRaw(statement, argument).Exec(ctx)
			return err
		})
		require.Error(t, err, "%s mutation unexpectedly completed before Publication commit", name)
		assert.Contains(t, err.Error(), "lock timeout", name)
	}
	assertLocked("Audience", `UPDATE draft_moments SET audience_complete = false WHERE id = ?`, fixture.moments[0])
	assertLocked("access", `UPDATE recipient_access_generations SET state = 'suspended' WHERE id = ?`, fixture.access["shared"])
	close(release)
	require.NoError(t, <-published)
}

func TestConcurrentRecipientReadSeesCompletePriorProjectionUntilCommit(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)
	_, err = fixture.db.NewRaw(`
		UPDATE events SET title = 'Atomic correction', version = 8 WHERE id = ?;
		DELETE FROM audience_snapshot_entries
		WHERE snapshot_id = (SELECT snapshot_id FROM current_audience_snapshots WHERE target_kind = 'moment' AND target_id = ?)
		  AND recipient_access_generation_id = ?;
		INSERT INTO audience_snapshot_entries (snapshot_id, recipient_person_id, recipient_access_generation_id)
		SELECT snapshot_id, ?, ? FROM current_audience_snapshots WHERE target_kind = 'moment' AND target_id = ?
	`, fixture.event, fixture.moments[0], fixture.access["shared"], fixture.people["shared"], fixture.access["shared"], fixture.moments[1]).Exec(ctx)
	require.NoError(t, err)

	readReached := make(chan struct{})
	releaseRead := make(chan struct{})
	var once sync.Once
	fixture.service.recipientReadBoundary = func() {
		once.Do(func() { close(readReached) })
		<-releaseRead
	}
	priorResult := make(chan PublishedEventView, 1)
	priorError := make(chan error, 1)
	go func() {
		prior, readErr := fixture.service.RecipientEvent(ctx, fixture.actorFor("shared"), fixture.event)
		priorResult <- prior
		priorError <- readErr
	}()
	<-readReached
	_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: 8})
	require.NoError(t, err)
	close(releaseRead)
	prior := <-priorResult
	require.NoError(t, <-priorError)
	assert.Equal(t, "Family weekend", prior.Title)
	require.Len(t, prior.Media, 1)
	assert.Equal(t, fixture.media[0].String(), prior.Media[0].ID, "the prior metadata and prior entitlement stay in one snapshot")

	fixture.service.recipientReadBoundary = nil
	current, err := fixture.service.RecipientEvent(ctx, fixture.actorFor("shared"), fixture.event)
	require.NoError(t, err)
	assert.Equal(t, "Atomic correction", current.Title)
	require.Len(t, current.Media, 1)
	assert.Equal(t, fixture.media[1].String(), current.Media[0].ID)
}
