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
	"github.com/robinjoseph08/memento/pkg/audiences"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/staging"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type publicationNotificationCandidate struct {
	AccessID uuid.UUID `bun:"access_id"`
	MediaID  uuid.UUID `bun:"media_id"`
}

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

func assertPublicationHandoffSkipped(t *testing.T, fixture publicationFixture, publication PublicationResponse) {
	t.Helper()
	calls := 0
	fixture.service.SetPublicationHandoff(func(context.Context, uuid.UUID, uuid.UUID) error {
		calls++
		return nil
	})
	job := worker.Job{Payload: []byte(fmt.Sprintf(
		`{"event_id":%q,"publication_id":%q,"notify_recipients":%t}`,
		fixture.event.String(), publication.ID, publication.NotifyRecipients,
	))}
	require.NoError(t, fixture.service.HandlePublicationJob(context.Background(), job))
	assert.Zero(t, calls, "a Publication that does not qualify for optional delivery skips external handoff")
}

func (fixture publicationFixture) actorFor(name string) setup.SessionActor {
	return setup.SessionActor{PersonID: fixture.people[name], AccessID: fixture.access[name], SessionID: uuid.New()}
}

func publicationNotificationCandidates(t *testing.T, fixture publicationFixture, publicationID string) []publicationNotificationCandidate {
	t.Helper()
	var candidates []publicationNotificationCandidate
	require.NoError(t, fixture.db.NewRaw(`SELECT recipient_access_generation_id AS access_id, media_item_id AS media_id
		FROM publication_notification_media WHERE publication_id = ?
		ORDER BY recipient_access_generation_id, media_item_id`, publicationID).Scan(context.Background(), &candidates))
	return candidates
}

func assertRecipientPublicationSurfacesMatch(t *testing.T, fixture publicationFixture, publicationID string) {
	t.Helper()
	var mismatches int
	require.NoError(t, fixture.db.NewRaw(`
		SELECT count(*)
		FROM new_for_you_entries AS entry
		FULL OUTER JOIN publication_activity_items AS activity
		  ON activity.publication_id = entry.publication_id
		 AND activity.recipient_access_generation_id = entry.recipient_access_generation_id
		WHERE COALESCE(entry.publication_id, activity.publication_id) = ?
		  AND (entry.recipient_access_generation_id IS NULL OR activity.recipient_access_generation_id IS NULL)
	`, publicationID).Scan(context.Background(), &mismatches))
	assert.Zero(t, mismatches, "New for you and Recipient activity use the same qualifying set")
}

func TestPreviewRendersSavedEditableResultBeforePublication(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	previewRecipients, err := fixture.service.PreviewRecipients(ctx, fixture.actor, fixture.event)
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
	assertRecipientPublicationSurfacesMatch(t, fixture, publication.ID)
	assert.ElementsMatch(t, []publicationNotificationCandidate{
		{AccessID: fixture.access["shared"], MediaID: fixture.media[0]},
		{AccessID: fixture.access["hidden"], MediaID: fixture.media[1]},
	}, publicationNotificationCandidates(t, fixture, publication.ID), "initial Publication materializes only exact completed-Recipient candidates")
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
	previewRecipients, err := fixture.service.PreviewRecipients(ctx, fixture.actor, fixture.event)
	require.NoError(t, err)
	assert.Len(t, previewRecipients.Recipients, 4)
	_, err = fixture.service.PreviewRecipients(ctx, fixture.actor, uuid.New())
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
		"new_for_you_entries": 2, "published_search_documents": 2,
		"publication_activity_items": 2, "publication_curator_activity_items": 1,
		"publication_audit_events": 1, "outbox_events": 1,
		"publication_preview_audit_events": 3,
	} {
		var count int
		require.NoError(t, fixture.db.NewRaw("SELECT count(*) FROM "+table).Scan(ctx, &count), table)
		assert.Equal(t, expected, count, table)
	}
	var placementProjectionMismatches int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*)
		FROM current_published_placements AS current
		JOIN published_media_placements AS published
		  ON published.published_moment_id = current.published_moment_id
		 AND published.media_item_id = current.media_item_id
		WHERE (current.media_type, current.width, current.height, current.local_date_time, current.capture_date)
		  IS DISTINCT FROM (
			published.media_type, published.width, published.height, published.local_date_time,
			memento_local_capture_date(published.local_date_time)
		  )`).Scan(ctx, &placementProjectionMismatches))
	assert.Zero(t, placementProjectionMismatches, "Publication atomically projects immutable Media chronology fields")

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
	assertRecipientPublicationSurfacesMatch(t, fixture, second.ID)
	assert.Equal(t, []publicationNotificationCandidate{
		{AccessID: fixture.access["hidden"], MediaID: fixture.media[0]},
	}, publicationNotificationCandidates(t, fixture, second.ID), "mixed revision excludes retained Media and materializes the exact newly accessible candidate per Recipient")
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
		UPDATE media_items SET availability = 'source_missing', missing_since = now() WHERE id = ?
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
	assertPublicationHandoffSkipped(t, fixture, publication)
	var searchText string
	require.NoError(t, fixture.db.NewRaw(`SELECT search_text FROM published_search_documents WHERE media_item_id = ?`, fixture.media[1]).Scan(ctx, &searchText))
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

func TestCuratorCanStageEventMetadataAndMediaRemovalCorrections(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)

	title := "Corrected family weekend"
	description := "A portal-owned correction"
	timezone := "America/New_York"
	secondCover, thirdCover := fixture.media[1].String(), fixture.media[2].String()
	corrected, err := fixture.service.OrganizeEvent(ctx, fixture.actor, fixture.event, OrganizeEventRequest{
		Version: 7, Title: &title, Description: &description, GroupingTimezone: &timezone,
		Moments: []OrganizeMoment{
			{ID: fixture.moments[1].String(), Title: "Moment", ProposedDay: "2026-07-28", CoverMediaItemID: &secondCover, MediaItemIDs: []string{fixture.media[1].String()}},
			{ID: fixture.moments[2].String(), Title: "Moment", ProposedDay: "2026-07-29", CoverMediaItemID: &thirdCover, MediaItemIDs: []string{fixture.media[2].String()}},
		},
		FinalReviewComplete: true,
	})
	require.NoError(t, err)
	assert.Equal(t, title, corrected.Title)
	assert.Equal(t, description, corrected.Description)
	assert.Equal(t, timezone, corrected.GroupingTimezone)
	assert.False(t, corrected.FinalReviewComplete)
	assert.Len(t, corrected.Moments, 2)
	require.NotNil(t, corrected.StagedUpdate)

	changes := make(map[staging.ChangeKind]StagedChange)
	for _, change := range corrected.StagedUpdate.Changes {
		changes[change.Kind] = change
	}
	assert.Equal(t, []string{fixture.media[0].String()}, changes[staging.ChangeKindRemoval].MediaItemIDs)
	assert.ElementsMatch(t, []string{"title", "description", "grouping_timezone"}, changes[staging.ChangeKindMetadata].EventMetadataFields)
	assert.Equal(t, []string{fixture.moments[0].String()}, changes[staging.ChangeKindMomentStructure].MomentIDs)
	_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: corrected.Version})
	assert.ErrorIs(t, err, ErrPublicationNotReady, "Curator corrections require a fresh final review")
}

func TestPlaceLabelOnlyCorrectionsRemainStagedAndPublishExactly(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)

	base, err := fixture.service.GetEvent(ctx, fixture.event)
	require.NoError(t, err)
	requestFor := func(event Event, eventLabels, firstMomentLabels []string, finalReview bool) OrganizeEventRequest {
		moments := make([]OrganizeMoment, 0, len(event.Moments))
		for index, moment := range event.Moments {
			labels := moment.PlaceLabels
			if index == 0 {
				labels = firstMomentLabels
			}
			mediaIDs := make([]string, 0, len(moment.MediaItems))
			for _, item := range moment.MediaItems {
				mediaIDs = append(mediaIDs, item.ID)
			}
			moments = append(moments, OrganizeMoment{
				ID: moment.ID, Title: moment.Title, PlaceLabels: labels,
				ProposedDay: moment.ProposedDay, CoverMediaItemID: moment.CoverMediaItemID,
				MediaItemIDs: mediaIDs,
			})
		}
		return OrganizeEventRequest{
			Version: event.Version, PlaceLabels: eventLabels,
			Moments: moments, FinalReviewComplete: finalReview,
		}
	}
	correctedEventLabels := []string{"Coastal overlook"}
	correctedMomentLabels := []string{"Breakfast room", "Harbor view"}

	corrected, err := fixture.service.OrganizeEvent(ctx, fixture.actor, fixture.event, requestFor(base, correctedEventLabels, correctedMomentLabels, true))
	require.NoError(t, err)
	assert.False(t, corrected.FinalReviewComplete)
	require.NotNil(t, corrected.StagedUpdate, "label-only corrections remain publishable Staged work")
	require.Len(t, corrected.StagedUpdate.Changes, 1)
	metadata := corrected.StagedUpdate.Changes[0]
	assert.Equal(t, staging.ChangeKindMetadata, metadata.Kind)
	assert.Equal(t, 2, metadata.Count, "one Event and one Moment have label metadata changes")
	assert.Equal(t, []string{"place_labels"}, metadata.EventMetadataFields)
	assert.Equal(t, []string{fixture.moments[0].String()}, metadata.MomentIDs)
	var stagedRows int
	var stagedPointerMatches bool
	require.NoError(t, fixture.db.NewRaw(`
		SELECT count(*), COALESCE(bool_and(event.current_staged_update_id = staged.id), false)
		FROM staged_updates AS staged
		JOIN events AS event ON event.id = staged.event_id
		WHERE staged.event_id = ?
	`, fixture.event).Scan(ctx, &stagedRows, &stagedPointerMatches))
	assert.Equal(t, 1, stagedRows)
	assert.True(t, stagedPointerMatches)

	cancelled, err := fixture.service.OrganizeEvent(ctx, fixture.actor, fixture.event, requestFor(corrected, base.PlaceLabels, base.Moments[0].PlaceLabels, false))
	require.NoError(t, err)
	assert.Equal(t, base.PlaceLabels, cancelled.PlaceLabels)
	assert.Equal(t, base.Moments[0].PlaceLabels, cancelled.Moments[0].PlaceLabels)
	assert.Nil(t, cancelled.StagedUpdate, "restoring exact published Event and Moment labels cancels the Staged update")
	var remainingStagedRows int
	var stagedPointerCleared bool
	require.NoError(t, fixture.db.NewRaw(`
		SELECT (SELECT count(*) FROM staged_updates WHERE event_id = ?),
		       current_staged_update_id IS NULL
		FROM events WHERE id = ?
	`, fixture.event, fixture.event).Scan(ctx, &remainingStagedRows, &stagedPointerCleared))
	assert.Zero(t, remainingStagedRows)
	assert.True(t, stagedPointerCleared)

	reapplied, err := fixture.service.OrganizeEvent(ctx, fixture.actor, fixture.event, requestFor(cancelled, correctedEventLabels, correctedMomentLabels, false))
	require.NoError(t, err)
	reviewed, err := fixture.service.OrganizeEvent(ctx, fixture.actor, fixture.event, requestFor(reapplied, correctedEventLabels, correctedMomentLabels, true))
	require.NoError(t, err)
	assert.True(t, reviewed.FinalReviewComplete)
	publication, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: reviewed.Version})
	require.NoError(t, err)

	var eventLabelsExact, momentLabelsExact bool
	require.NoError(t, fixture.db.NewRaw(`
		SELECT current.place_labels = ARRAY['Coastal overlook']::text[],
		       moment.place_labels = ARRAY['Breakfast room', 'Harbor view']::text[]
		FROM current_published_events AS current
		JOIN published_moments AS moment
		  ON moment.publication_id = current.publication_id AND moment.draft_moment_id = ?
		WHERE current.event_id = ? AND current.publication_id = ?
	`, fixture.moments[0], fixture.event, publication.ID).Scan(ctx, &eventLabelsExact, &momentLabelsExact))
	assert.True(t, eventLabelsExact)
	assert.True(t, momentLabelsExact)
	published, err := fixture.service.GetEvent(ctx, fixture.event)
	require.NoError(t, err)
	assert.Nil(t, published.StagedUpdate)
}

func TestCuratorCanRestoreAutosavedPublishedMediaRemoval(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	_, err := fixture.db.NewRaw(`UPDATE draft_moments SET place_labels = ARRAY['Garden terrace', 'Harbor view'] WHERE id = ?`, fixture.moments[0]).Exec(ctx)
	require.NoError(t, err)
	_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)

	sourceID := uuid.New()
	_, err = fixture.db.NewRaw(`
		INSERT INTO source_albums (
			id, immich_album_id, name, description, asset_count, source_created_at,
			source_updated_at, disposition, first_seen_at, last_seen_at,
			source_fingerprint, next_reconciliation_at
		) VALUES (?, ?, 'Published source', '', 1, now(), now(), 'drafted', now(), now(), decode(repeat('00', 32), 'hex'), now());
		INSERT INTO event_sources (
			event_id, source_album_id, source_order, initialized_name,
			initialized_description, initialized_at
		) VALUES (?, ?, 0, 'Published source', '', now());
		INSERT INTO source_album_memberships (
			source_album_id, immich_asset_id, media_item_id, first_seen_at,
			last_seen_at, source_fingerprint
		) SELECT ?, immich_asset_id, id, now(), now(), decode(repeat('11', 32), 'hex')
		  FROM media_items WHERE id = ?
	`, sourceID, uuid.New(), fixture.event, sourceID, sourceID, fixture.media[0]).Exec(ctx)
	require.NoError(t, err)

	var originalSnapshotID uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`
		SELECT snapshot_id FROM current_audience_snapshots
		WHERE target_kind = 'moment' AND target_id = ?
	`, fixture.moments[0]).Scan(ctx, &originalSnapshotID))
	_, err = fixture.db.NewRaw(`
		INSERT INTO attendance (moment_id, person_id, source, confirmed_by_person_id, confirmed_at)
		VALUES (?, ?, 'manual', ?, now());
		INSERT INTO audience_overrides (
			target_kind, target_id, recipient_person_id, state, updated_by_person_id, updated_at
		) VALUES ('moment', ?, ?, 'included', ?, now());
		INSERT INTO audience_proposals (
			target_kind, target_id, recipient_person_id, recipient_access_generation_id, included, recalculated_at
		) VALUES ('moment', ?, ?, ?, true, now());
		INSERT INTO audience_reasons (target_kind, target_id, recipient_person_id, kind)
		VALUES ('moment', ?, ?, 'manually_included')
	`, fixture.moments[0], fixture.people["shared"], fixture.actor.PersonID,
		fixture.moments[0], fixture.people["shared"], fixture.actor.PersonID,
		fixture.moments[0], fixture.people["shared"], fixture.access["shared"],
		fixture.moments[0], fixture.people["shared"]).Exec(ctx)
	require.NoError(t, err)

	secondCover, thirdCover := fixture.media[1].String(), fixture.media[2].String()
	removed, err := fixture.service.OrganizeEvent(ctx, fixture.actor, fixture.event, OrganizeEventRequest{
		Version: 7,
		Moments: []OrganizeMoment{
			{ID: fixture.moments[1].String(), Title: "Moment", ProposedDay: "2026-07-28", CoverMediaItemID: &secondCover, MediaItemIDs: []string{fixture.media[1].String()}},
			{ID: fixture.moments[2].String(), Title: "Moment", ProposedDay: "2026-07-29", CoverMediaItemID: &thirdCover, MediaItemIDs: []string{fixture.media[2].String()}},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, removed.StagedUpdate)

	_, err = fixture.db.NewRaw(`DELETE FROM source_album_memberships WHERE source_album_id = ? AND media_item_id = ?`, sourceID, fixture.media[0]).Exec(ctx)
	require.NoError(t, err)
	_, err = fixture.service.RestorePublishedMedia(ctx, fixture.actor, fixture.event, RestorePublishedMediaRequest{
		Version: removed.Version, MediaItemID: fixture.media[0].String(),
	})
	assert.ErrorIs(t, err, ErrMediaUnavailable, "restoration requires the published identity to remain in an Event-linked Source")
	_, err = fixture.db.NewRaw(`
		INSERT INTO source_album_memberships (
			source_album_id, immich_asset_id, media_item_id, first_seen_at,
			last_seen_at, source_fingerprint
		) SELECT ?, immich_asset_id, id, now(), now(), decode(repeat('11', 32), 'hex')
		  FROM media_items WHERE id = ?
	`, sourceID, fixture.media[0]).Exec(ctx)
	require.NoError(t, err)

	restored, err := fixture.service.RestorePublishedMedia(ctx, fixture.actor, fixture.event, RestorePublishedMediaRequest{
		Version: removed.Version, MediaItemID: fixture.media[0].String(),
	})
	require.NoError(t, err)
	assert.Nil(t, restored.StagedUpdate, "restoring the published result cancels the private removal")
	require.Len(t, restored.Moments, 3)
	assert.Equal(t, fixture.moments[0].String(), restored.Moments[0].ID)
	assert.Equal(t, fixture.media[0].String(), restored.Moments[0].MediaItems[0].ID)
	assert.Equal(t, []string{"Garden terrace", "Harbor view"}, restored.Moments[0].PlaceLabels)
	assert.True(t, restored.Moments[0].AttendanceComplete)
	assert.True(t, restored.Moments[0].AudienceComplete)
	var restoredSnapshotID uuid.UUID
	var attendanceRows, overrideRows, proposalRows, reasonRows, restorationRows int
	require.NoError(t, fixture.db.NewRaw(`SELECT snapshot_id FROM current_audience_snapshots WHERE target_kind = 'moment' AND target_id = ?`, fixture.moments[0]).Scan(ctx, &restoredSnapshotID))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM attendance WHERE moment_id = ?`, fixture.moments[0]).Scan(ctx, &attendanceRows))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM audience_overrides WHERE target_kind = 'moment' AND target_id = ?`, fixture.moments[0]).Scan(ctx, &overrideRows))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM audience_proposals WHERE target_kind = 'moment' AND target_id = ?`, fixture.moments[0]).Scan(ctx, &proposalRows))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM audience_reasons WHERE target_kind = 'moment' AND target_id = ?`, fixture.moments[0]).Scan(ctx, &reasonRows))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM staged_moment_review_restorations WHERE event_id = ?`, fixture.event).Scan(ctx, &restorationRows))
	assert.Equal(t, originalSnapshotID, restoredSnapshotID)
	assert.Equal(t, 1, attendanceRows)
	assert.Equal(t, 1, overrideRows)
	assert.Equal(t, 1, proposalRows)
	assert.Equal(t, 1, reasonRows)
	assert.Zero(t, restorationRows)

	mergedCover := fixture.media[1].String()
	merged, err := fixture.service.OrganizeEvent(ctx, fixture.actor, fixture.event, OrganizeEventRequest{
		Version: restored.Version,
		Moments: []OrganizeMoment{{
			ID: fixture.moments[1].String(), Title: "Merged Moment", ProposedDay: "2026-07-28",
			PlaceLabels: []string{"Harbor view", "Garden terrace"}, CoverMediaItemID: &mergedCover,
			MediaItemIDs: []string{fixture.media[1].String(), fixture.media[2].String()},
		}},
		UnassignedMediaIDs: []string{fixture.media[0].String()},
	})
	require.NoError(t, err, "the delayed local merge persists after restoration recreated the deleted Moment")
	require.Len(t, merged.Moments, 1)
	assert.Equal(t, fixture.moments[1].String(), merged.Moments[0].ID)
	assert.Equal(t, []string{"Harbor view", "Garden terrace"}, merged.Moments[0].PlaceLabels)
	require.Len(t, merged.Moments[0].MediaItems, 2)
	assert.Equal(t, fixture.media[1].String(), merged.Moments[0].MediaItems[0].ID)
	assert.Equal(t, fixture.media[2].String(), merged.Moments[0].MediaItems[1].ID)
	require.Len(t, merged.UnassignedMedia, 1)
	assert.Equal(t, fixture.media[0].String(), merged.UnassignedMedia[0].ID)
	require.NotNil(t, merged.StagedUpdate)

	persisted, err := fixture.service.GetEvent(ctx, fixture.event)
	require.NoError(t, err)
	require.Len(t, persisted.Moments, 1)
	assert.Equal(t, fixture.moments[1].String(), persisted.Moments[0].ID)
	require.Len(t, persisted.UnassignedMedia, 1)
	assert.Equal(t, fixture.media[0].String(), persisted.UnassignedMedia[0].ID)
	var recreatedMomentRows int
	require.NoError(t, fixture.db.NewRaw(`
		SELECT count(*) FROM draft_moments WHERE event_id = ? AND id = ?
	`, fixture.event, fixture.moments[0]).Scan(ctx, &recreatedMomentRows))
	assert.Zero(t, recreatedMomentRows)
}

func TestPublishedMediaRestorationCancelsInReversePublishedOrder(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	_, err := fixture.db.NewRaw(`
		UPDATE draft_media_placements SET draft_moment_id = ? WHERE event_id = ?;
		UPDATE draft_moments SET cover_media_item_id = ? WHERE id = ?;
		DELETE FROM draft_moments WHERE event_id = ? AND id <> ?
	`, fixture.moments[0], fixture.event, fixture.media[2], fixture.moments[0], fixture.event, fixture.moments[0]).Exec(ctx)
	require.NoError(t, err)
	_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)

	sourceID := uuid.New()
	_, err = fixture.db.NewRaw(`
		INSERT INTO source_albums (
			id, immich_album_id, name, description, asset_count, source_created_at,
			source_updated_at, disposition, first_seen_at, last_seen_at,
			source_fingerprint, next_reconciliation_at
		) VALUES (?, ?, 'Published source', '', 3, now(), now(), 'drafted', now(), now(), decode(repeat('00', 32), 'hex'), now());
		INSERT INTO event_sources (
			event_id, source_album_id, source_order, initialized_name,
			initialized_description, initialized_at
		) VALUES (?, ?, 0, 'Published source', '', now());
		INSERT INTO source_album_memberships (
			source_album_id, immich_asset_id, media_item_id, first_seen_at,
			last_seen_at, source_fingerprint
		) SELECT ?, immich_asset_id, id, now(), now(), decode(repeat('11', 32), 'hex')
		  FROM media_items WHERE id IN (?, ?, ?)
	`, sourceID, uuid.New(), fixture.event, sourceID, sourceID, fixture.media[0], fixture.media[1], fixture.media[2]).Exec(ctx)
	require.NoError(t, err)

	cover := fixture.media[2].String()
	removed, err := fixture.service.OrganizeEvent(ctx, fixture.actor, fixture.event, OrganizeEventRequest{
		Version: 7,
		Moments: []OrganizeMoment{{
			ID: fixture.moments[0].String(), Title: "Moment", ProposedDay: "2026-07-27",
			CoverMediaItemID: &cover, MediaItemIDs: []string{fixture.media[2].String()},
		}},
	})
	require.NoError(t, err)
	require.NotNil(t, removed.StagedUpdate)

	partial, err := fixture.service.RestorePublishedMedia(ctx, fixture.actor, fixture.event, RestorePublishedMediaRequest{
		Version: removed.Version, MediaItemID: fixture.media[1].String(),
	})
	require.NoError(t, err)
	require.Len(t, partial.Moments, 1)
	require.Len(t, partial.Moments[0].MediaItems, 2)
	assert.Equal(t, fixture.media[1].String(), partial.Moments[0].MediaItems[0].ID)
	assert.Equal(t, fixture.media[2].String(), partial.Moments[0].MediaItems[1].ID)

	cancelled, err := fixture.service.RestorePublishedMedia(ctx, fixture.actor, fixture.event, RestorePublishedMediaRequest{
		Version: partial.Version, MediaItemID: fixture.media[0].String(),
	})
	require.NoError(t, err)
	require.Len(t, cancelled.Moments, 1)
	require.Len(t, cancelled.Moments[0].MediaItems, 3)
	for index, media := range cancelled.Moments[0].MediaItems {
		assert.Equal(t, fixture.media[index].String(), media.ID)
	}
	assert.Nil(t, cancelled.StagedUpdate, "reverse-order restoration exactly cancels the private removals")
}

func TestPublishedMediaRestorationPreservesValidOrderAfterFreshReview(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	baselinePersonID, freshPersonID := uuid.New(), uuid.New()
	_, err := fixture.db.NewRaw(`
		INSERT INTO people (id, display_name, sort_name)
		VALUES (?, 'Baseline witness', 'baseline witness'), (?, 'Fresh witness', 'fresh witness');
		UPDATE draft_media_placements SET draft_moment_id = ?
		WHERE event_id = ? AND media_item_id = ?;
		DELETE FROM draft_moments WHERE id = ?;
		DELETE FROM audience_snapshot_entries
		WHERE snapshot_id = (
			SELECT snapshot_id FROM current_audience_snapshots
			WHERE target_kind = 'moment' AND target_id = ?
		);
		INSERT INTO attendance (moment_id, person_id, source, confirmed_by_person_id, confirmed_at)
		VALUES (?, ?, 'manual', ?, now())
	`, baselinePersonID, freshPersonID,
		fixture.moments[0], fixture.event, fixture.media[1], fixture.moments[1], fixture.moments[0],
		fixture.moments[0], baselinePersonID, fixture.actor.PersonID).Exec(ctx)
	require.NoError(t, err)
	_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)

	sourceID := uuid.New()
	_, err = fixture.db.NewRaw(`
		INSERT INTO source_albums (
			id, immich_album_id, name, description, asset_count, source_created_at,
			source_updated_at, disposition, first_seen_at, last_seen_at,
			source_fingerprint, next_reconciliation_at
		) VALUES (?, ?, 'Published source', '', 2, now(), now(), 'drafted', now(), now(), decode(repeat('00', 32), 'hex'), now());
		INSERT INTO event_sources (
			event_id, source_album_id, source_order, initialized_name,
			initialized_description, initialized_at
		) VALUES (?, ?, 0, 'Published source', '', now());
		INSERT INTO source_album_memberships (
			source_album_id, immich_asset_id, media_item_id, first_seen_at,
			last_seen_at, source_fingerprint
		) SELECT ?, immich_asset_id, id, now(), now(), decode(repeat('11', 32), 'hex')
		  FROM media_items WHERE id IN (?, ?)
	`, sourceID, uuid.New(), fixture.event, sourceID, sourceID, fixture.media[0], fixture.media[1]).Exec(ctx)
	require.NoError(t, err)

	nonCover, thirdCover := fixture.media[1].String(), fixture.media[2].String()
	firstRemoval, err := fixture.service.OrganizeEvent(ctx, fixture.actor, fixture.event, OrganizeEventRequest{
		Version: 7,
		Moments: []OrganizeMoment{
			{ID: fixture.moments[0].String(), Title: "Moment", ProposedDay: "2026-07-27", CoverMediaItemID: &nonCover, MediaItemIDs: []string{nonCover}},
			{ID: fixture.moments[2].String(), Title: "Moment", ProposedDay: "2026-07-29", CoverMediaItemID: &thirdCover, MediaItemIDs: []string{fixture.media[2].String()}},
		},
	})
	require.NoError(t, err)

	var reviewVersion int64
	require.NoError(t, fixture.db.NewRaw(`SELECT review_version FROM draft_moments WHERE id = ?`, fixture.moments[0]).Scan(ctx, &reviewVersion))
	freshAttendance := []string{freshPersonID.String()}
	audienceService := audiences.New(fixture.db, nil)
	_, err = audienceService.ConfirmAttendance(ctx, fixture.actor, fixture.moments[0], reviewVersion, audiences.AttendanceRequest{PersonIDs: &freshAttendance})
	require.NoError(t, err)
	var superseded bool
	require.NoError(t, fixture.db.NewRaw(`
		SELECT superseded FROM staged_moment_review_restorations
		WHERE event_id = ? AND draft_moment_id = ?
	`, fixture.event, fixture.moments[0]).Scan(ctx, &superseded))
	assert.True(t, superseded, "fresh review keeps a no-restore marker for the published baseline")

	freshEvent, err := fixture.service.GetEvent(ctx, fixture.event)
	require.NoError(t, err)
	secondRemoval, err := fixture.service.OrganizeEvent(ctx, fixture.actor, fixture.event, OrganizeEventRequest{
		Version: freshEvent.Version,
		Moments: []OrganizeMoment{
			{ID: fixture.moments[2].String(), Title: "Moment", ProposedDay: "2026-07-29", CoverMediaItemID: &thirdCover, MediaItemIDs: []string{fixture.media[2].String()}},
		},
	})
	require.NoError(t, err)
	assert.Greater(t, secondRemoval.Version, firstRemoval.Version)

	partial, err := fixture.service.RestorePublishedMedia(ctx, fixture.actor, fixture.event, RestorePublishedMediaRequest{
		Version: secondRemoval.Version, MediaItemID: fixture.media[1].String(),
	})
	require.NoError(t, err)
	require.Len(t, partial.Moments, 2)
	assert.Equal(t, fixture.moments[0].String(), partial.Moments[0].ID)
	assert.Equal(t, fixture.media[1].String(), partial.Moments[0].MediaItems[0].ID)
	assert.Nil(t, partial.Moments[0].CoverMediaItemID, "restoring a non-cover cannot reference the still-removed published cover")
	var restorationRows int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM staged_moment_review_restorations WHERE event_id = ? AND superseded`, fixture.event).Scan(ctx, &restorationRows))
	assert.Equal(t, 1, restorationRows, "partial restoration retains the no-restore marker")

	cancelled, err := fixture.service.RestorePublishedMedia(ctx, fixture.actor, fixture.event, RestorePublishedMediaRequest{
		Version: partial.Version, MediaItemID: fixture.media[0].String(),
	})
	require.NoError(t, err)
	require.Len(t, cancelled.Moments, 2)
	require.NotNil(t, cancelled.Moments[0].CoverMediaItemID)
	assert.Equal(t, fixture.media[0].String(), *cancelled.Moments[0].CoverMediaItemID)
	assert.Nil(t, cancelled.StagedUpdate, "restoring both Media items completely cancels the organization change")
	var baselineRows, freshRows int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM attendance WHERE moment_id = ? AND person_id = ?`, fixture.moments[0], baselinePersonID).Scan(ctx, &baselineRows))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM attendance WHERE moment_id = ? AND person_id = ?`, fixture.moments[0], freshPersonID).Scan(ctx, &freshRows))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM staged_moment_review_restorations WHERE event_id = ?`, fixture.event).Scan(ctx, &restorationRows))
	assert.Zero(t, baselineRows, "superseded published review evidence is not restored")
	assert.Zero(t, freshRows, "intermediate review evidence is not captured and restored")
	assert.Zero(t, restorationRows, "complete cancellation clears the no-restore marker")
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
	require.NoError(t, fixture.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`
			UPDATE events SET title = 'Replacement candidate', description = 'Replacement description',
				grouping_timezone = 'America/New_York', version = 8
			WHERE id = ?
		`, fixture.event).Exec(ctx); err != nil {
			return err
		}
		if _, err := refreshStagedUpdate(ctx, tx, fixture.event, fixture.service.now().UTC()); err != nil {
			return err
		}
		_, err := tx.NewRaw(`
			INSERT INTO staged_source_removals (
				event_id, media_item_id, draft_moment_id, position, was_cover, created_at
			) VALUES (?, ?, ?, 17, true, ?)
		`, fixture.event, fixture.media[0], fixture.moments[0], fixture.service.now().UTC()).Exec(ctx)
		return err
	}))
	editable, err := fixture.service.GetEvent(ctx, fixture.event)
	require.NoError(t, err)
	require.NotNil(t, editable.StagedUpdate)
	var metadataFields []string
	for _, change := range editable.StagedUpdate.Changes {
		if change.Kind == "metadata" {
			metadataFields = change.EventMetadataFields
		}
	}
	assert.ElementsMatch(t, []string{"title", "description", "grouping_timezone"}, metadataFields)
	replacementTables := append([]string{"events", "staged_updates", "staged_source_removals"}, publicationTables...)
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
	assertListedStaged := func(expected bool, phase string) {
		t.Helper()
		work, listErr := fixture.service.ListEvents(ctx)
		require.NoError(t, listErr)
		require.Len(t, work.Events, 1)
		assert.Equal(t, expected, work.Events[0].HasStagedUpdate, phase)
	}

	concurrentCtx, cancelConcurrent := context.WithTimeout(ctx, 10*time.Second)
	defer cancelConcurrent()
	start := make(chan struct{})
	errorsByEdit := make(chan error, 2)
	for _, title := range []string{"Concurrent title A", "Concurrent title B"} {
		title := title
		go func() {
			<-start
			errorsByEdit <- fixture.db.RunInTx(concurrentCtx, nil, func(ctx context.Context, tx bun.Tx) error {
				if _, err := tx.NewRaw(`UPDATE events SET title = ?, version = version + 1 WHERE id = ?`, title, fixture.event).Exec(ctx); err != nil {
					return err
				}
				_, err := refreshStagedUpdate(ctx, tx, fixture.event, fixture.service.now().UTC())
				return err
			})
		}()
	}
	close(start)
	for completed := 0; completed < cap(errorsByEdit); completed++ {
		select {
		case editErr := <-errorsByEdit:
			require.NoErrorf(t, editErr, "concurrent Staged edit %d/%d failed; database stats: %+v", completed+1, cap(errorsByEdit), fixture.db.Stats())
		case <-concurrentCtx.Done():
			t.Fatalf("concurrent Staged edits completed %d/%d operations before timeout: %v; database stats: %+v", completed, cap(errorsByEdit), concurrentCtx.Err(), fixture.db.Stats())
		}
	}

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
	assertListedStaged(true, "coalesced edits appear as Staged in the Event list")

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
	assertListedStaged(false, "cancellation clears the Staged marker from the Event list")

	var publishVersion int64
	require.NoError(t, fixture.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw(`
			UPDATE events SET title = 'Published correction', version = version + 1
			WHERE id = ? RETURNING version
		`, fixture.event).Scan(ctx, &publishVersion); err != nil {
			return err
		}
		_, refreshErr := refreshStagedUpdate(ctx, tx, fixture.event, fixture.service.now().UTC())
		return refreshErr
	}))
	assertListedStaged(true, "the replacement remains Staged before Publication")
	_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: publishVersion})
	require.NoError(t, err)
	assertListedStaged(false, "successful Publication clears the Staged marker from the Event list")
}

func TestStagedRemovalDoesNotReportCompactedRetainedMediaAsMoved(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)

	var update *StagedUpdate
	require.NoError(t, fixture.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`
			DELETE FROM draft_media_placements WHERE event_id = ? AND media_item_id = ?;
			UPDATE draft_media_placements SET position = position - 1
			WHERE event_id = ? AND position > 0
		`, fixture.event, fixture.media[0], fixture.event).Exec(ctx); err != nil {
			return err
		}
		var err error
		update, err = refreshStagedUpdate(ctx, tx, fixture.event, fixture.service.now().UTC())
		return err
	}))
	require.NotNil(t, update)
	var removal *StagedChange
	for index := range update.Changes {
		change := &update.Changes[index]
		if change.Kind == "move" {
			t.Fatalf("position compaction reported retained Media as moved: %#v", change.MediaItemIDs)
		}
		if change.Kind == "removal" {
			removal = change
		}
	}
	require.NotNil(t, removal)
	assert.Equal(t, []string{fixture.media[0].String()}, removal.MediaItemIDs)
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
	assertPublicationHandoffSkipped(t, fixture, second)
}

func TestPublicationActivityRequiresGloballyNewEffectiveMedia(t *testing.T) {
	for _, test := range []struct {
		name            string
		withdrawOverlap bool
		expectedAccess  []string
	}{
		{name: "effective overlap stays quiet", expectedAccess: []string{"hidden"}},
		{name: "withdrawn overlap qualifies", withdrawOverlap: true, expectedAccess: []string{"shared", "hidden"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPublicationFixture(t)
			ctx := context.Background()
			secondEvent, _, _ := publishReusedMediaInSecondEvent(t, fixture)
			if test.withdrawOverlap {
				_, err := fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
					TargetKind: WithdrawalTargetEvent,
					TargetID:   secondEvent.String(),
					Reason:     "Prior overlap is no longer effective",
				})
				require.NoError(t, err)
			}

			publication, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
			require.NoError(t, err)
			var activityAccess, newForYouAccess []uuid.UUID
			require.NoError(t, fixture.db.NewRaw(`
				SELECT recipient_access_generation_id
				FROM publication_activity_items
				WHERE publication_id = ? ORDER BY recipient_access_generation_id
			`, publication.ID).Scan(ctx, &activityAccess))
			require.NoError(t, fixture.db.NewRaw(`
				SELECT recipient_access_generation_id
				FROM new_for_you_entries
				WHERE publication_id = ? ORDER BY recipient_access_generation_id
			`, publication.ID).Scan(ctx, &newForYouAccess))
			expectedAccess := make([]uuid.UUID, len(test.expectedAccess))
			for index, name := range test.expectedAccess {
				expectedAccess[index] = fixture.access[name]
			}
			assert.ElementsMatch(t, expectedAccess, activityAccess)
			assert.Equal(t, activityAccess, newForYouAccess)
		})
	}
}

func TestPublicationInvalidatesAnotherEventsChangedStagedAccessImpact(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)

	secondEvent, secondMoment, secondSnapshot := uuid.New(), uuid.New(), uuid.New()
	_, err = fixture.db.NewRaw(`
		INSERT INTO events (
			id, lifecycle, title, description, grouping_timezone, version,
			final_review_complete, created_at, updated_at
		) VALUES (?, 'draft', 'Overlapping Event', '', 'UTC', 7, true, ?, ?);
		INSERT INTO draft_moments (
			id, event_id, position, proposed_day, grouping_timezone, source_days,
			title, cover_media_item_id, attendance_complete, audience_complete
		) VALUES (?, ?, 0, '2026-07-27', 'UTC', ARRAY['2026-07-27'::date],
			'Overlap', ?, true, true);
		INSERT INTO draft_media_placements (
			event_id, media_item_id, draft_moment_id, position, created_at
		) VALUES (?, ?, ?, 0, ?);
		INSERT INTO audience_snapshots (
			id, target_kind, target_id, approved_by_person_id, approved_at, label
		) VALUES (?, 'moment', ?, ?, ?, 'Shared');
		INSERT INTO audience_snapshot_entries (
			snapshot_id, recipient_person_id, recipient_access_generation_id
		) VALUES (?, ?, ?);
		INSERT INTO current_audience_snapshots (target_kind, target_id, snapshot_id)
		VALUES ('moment', ?, ?)
	`, secondEvent, fixture.service.now(), fixture.service.now(), secondMoment, secondEvent,
		fixture.media[0], secondEvent, fixture.media[0], secondMoment, fixture.service.now(),
		secondSnapshot, secondMoment, fixture.actor.PersonID, fixture.service.now(), secondSnapshot,
		fixture.people["shared"], fixture.access["shared"], secondMoment, secondSnapshot).Exec(ctx)
	require.NoError(t, err)
	_, err = fixture.service.PublishEvent(ctx, fixture.actor, secondEvent, fixture.request())
	require.NoError(t, err)

	firstReplacementSnapshot := uuid.New()
	var firstUpdate *staging.Update
	require.NoError(t, fixture.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, updateErr := tx.NewRaw(`
			INSERT INTO audience_snapshots (
				id, target_kind, target_id, approved_by_person_id, approved_at, label
			) VALUES (?, 'moment', ?, ?, ?, 'Shared');
			INSERT INTO audience_snapshot_entries (
				snapshot_id, recipient_person_id, recipient_access_generation_id
			) VALUES (?, ?, ?);
			UPDATE current_audience_snapshots SET snapshot_id = ?
			WHERE target_kind = 'moment' AND target_id = ?;
			UPDATE events SET version = 8, final_review_complete = true WHERE id = ?
		`, firstReplacementSnapshot, fixture.moments[0], fixture.actor.PersonID, fixture.service.now(),
			firstReplacementSnapshot, fixture.people["pending"], fixture.access["pending"],
			firstReplacementSnapshot, fixture.moments[0], fixture.event).Exec(ctx); updateErr != nil {
			return updateErr
		}
		var refreshErr error
		firstUpdate, refreshErr = staging.Refresh(ctx, tx, fixture.event, fixture.service.now().UTC())
		return refreshErr
	}))
	require.NotNil(t, firstUpdate)
	var priorAccess staging.Change
	for _, change := range firstUpdate.Changes {
		if change.Kind == staging.ChangeKindAccess {
			priorAccess = change
		}
	}
	assert.Equal(t, staging.ChangeKindAccess, priorAccess.Kind)
	assert.Zero(t, priorAccess.Count, "the overlapping Publication initially masks the shared Recipient revocation")
	assert.Empty(t, priorAccess.RecipientAccess)

	secondReplacementSnapshot := uuid.New()
	require.NoError(t, fixture.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, updateErr := tx.NewRaw(`
			INSERT INTO audience_snapshots (
				id, target_kind, target_id, approved_by_person_id, approved_at, label
			) VALUES (?, 'moment', ?, ?, ?, 'Curator only');
			UPDATE current_audience_snapshots SET snapshot_id = ?
			WHERE target_kind = 'moment' AND target_id = ?;
			UPDATE events SET version = 8, final_review_complete = true WHERE id = ?
		`, secondReplacementSnapshot, secondMoment, fixture.actor.PersonID, fixture.service.now(),
			secondReplacementSnapshot, secondMoment, secondEvent).Exec(ctx); updateErr != nil {
			return updateErr
		}
		update, refreshErr := staging.Refresh(ctx, tx, secondEvent, fixture.service.now().UTC())
		require.NotNil(t, update)
		return refreshErr
	}))
	_, err = fixture.service.PublishEvent(ctx, fixture.actor, secondEvent, PublishEventRequest{Version: 8})
	require.NoError(t, err)

	dependent, err := staging.Load(ctx, fixture.db, fixture.event)
	require.NoError(t, err)
	require.NotNil(t, dependent)
	var changedAccess staging.Change
	for _, change := range dependent.Changes {
		if change.Kind == staging.ChangeKindAccess {
			changedAccess = change
		}
	}
	require.Equal(t, staging.ChangeKindAccess, changedAccess.Kind)
	assert.Equal(t, 1, changedAccess.Count)
	require.Len(t, changedAccess.RecipientAccess, 1)
	assert.Equal(t, fixture.people["shared"].String(), changedAccess.RecipientAccess[0].RecipientPersonID)
	assert.Zero(t, changedAccess.RecipientAccess[0].GrantedMediaCount)
	assert.Equal(t, 1, changedAccess.RecipientAccess[0].RevokedMediaCount)

	var dependentVersion int64
	var dependentFinalReview bool
	require.NoError(t, fixture.db.NewRaw(`
		SELECT version, final_review_complete FROM events WHERE id = ?
	`, fixture.event).Scan(ctx, &dependentVersion, &dependentFinalReview))
	assert.Equal(t, int64(9), dependentVersion)
	assert.False(t, dependentFinalReview)
	_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: 8})
	assert.ErrorIs(t, err, ErrVersionConflict, "the reviewed version became stale when another Publication changed its access impact")
	_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: 9})
	assert.ErrorIs(t, err, ErrPublicationNotReady, "the changed access impact requires fresh final review")
}

func TestFirstStagedInsertSerializesWithEntitlementReplacement(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)
	secondEvent, secondMoment, _ := publishReusedMediaInSecondEvent(t, fixture)

	secondReplacementSnapshot := uuid.New()
	require.NoError(t, fixture.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := staging.LockAccessSummaryRefresh(ctx, tx); err != nil {
			return err
		}
		if _, updateErr := tx.NewRaw(`
			INSERT INTO audience_snapshots (
				id, target_kind, target_id, approved_by_person_id, approved_at, label
			) VALUES (?, 'moment', ?, ?, ?, 'Curator only');
			UPDATE current_audience_snapshots SET snapshot_id = ?
			WHERE target_kind = 'moment' AND target_id = ?;
			UPDATE events SET version = 8, final_review_complete = true WHERE id = ?
		`, secondReplacementSnapshot, secondMoment, fixture.actor.PersonID, fixture.service.now(),
			secondReplacementSnapshot, secondMoment, secondEvent).Exec(ctx); updateErr != nil {
			return updateErr
		}
		_, refreshErr := staging.Refresh(ctx, tx, secondEvent, fixture.service.now().UTC())
		return refreshErr
	}))

	firstInsert, err := fixture.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	committed := false
	defer func() {
		if !committed {
			_ = firstInsert.Rollback()
		}
	}()
	require.NoError(t, staging.LockAccessSummaryRefresh(ctx, firstInsert))
	firstReplacementSnapshot := uuid.New()
	_, err = firstInsert.NewRaw(`
		INSERT INTO audience_snapshots (
			id, target_kind, target_id, approved_by_person_id, approved_at, label
		) VALUES (?, 'moment', ?, ?, ?, 'Shared');
		INSERT INTO audience_snapshot_entries (
			snapshot_id, recipient_person_id, recipient_access_generation_id
		) VALUES (?, ?, ?), (?, ?, ?);
		UPDATE current_audience_snapshots SET snapshot_id = ?
		WHERE target_kind = 'moment' AND target_id = ?;
		UPDATE events SET version = 8, final_review_complete = true WHERE id = ?
	`, firstReplacementSnapshot, fixture.moments[0], fixture.actor.PersonID, fixture.service.now(),
		firstReplacementSnapshot, fixture.people["pending"], fixture.access["pending"],
		firstReplacementSnapshot, fixture.people["none"], fixture.access["none"],
		firstReplacementSnapshot, fixture.moments[0], fixture.event).Exec(ctx)
	require.NoError(t, err)
	firstUpdate, err := staging.Refresh(ctx, firstInsert, fixture.event, fixture.service.now().UTC())
	require.NoError(t, err)
	require.NotNil(t, firstUpdate, "the regression requires an uncommitted first Staged insert")

	published := make(chan error, 1)
	go func() {
		_, publishErr := fixture.service.PublishEvent(ctx, fixture.actor, secondEvent, PublishEventRequest{Version: 8})
		published <- publishErr
	}()
	waitForAdvisoryLockWaiter(t, fixture, staging.AccessSummaryLockKey, "ExclusiveLock")
	require.NoError(t, firstInsert.Commit())
	committed = true
	select {
	case publishErr := <-published:
		require.NoError(t, publishErr)
	case <-ctx.Done():
		t.Fatalf("Publication did not finish after the first Staged insert committed: %v; database stats: %+v", ctx.Err(), fixture.db.Stats())
	}

	dependent, err := staging.Load(ctx, fixture.db, fixture.event)
	require.NoError(t, err)
	require.NotNil(t, dependent)
	var access staging.Change
	for _, change := range dependent.Changes {
		if change.Kind == staging.ChangeKindAccess {
			access = change
		}
	}
	require.Equal(t, staging.ChangeKindAccess, access.Kind)
	require.Len(t, access.RecipientAccess, 2, "the dependent scan must observe and refresh the committed first insert")
	var version int64
	var finalReview bool
	require.NoError(t, fixture.db.NewRaw(`SELECT version, final_review_complete FROM events WHERE id = ?`, fixture.event).Scan(ctx, &version, &finalReview))
	assert.Equal(t, int64(9), version)
	assert.False(t, finalReview)
}

func TestNoNewMediaStructuralAndAccessCorrectionsAreQuiet(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(context.Context, bun.Tx, publicationFixture) error
	}{
		{
			name: "removal",
			mutate: func(ctx context.Context, tx bun.Tx, fixture publicationFixture) error {
				_, err := tx.NewRaw(`
					UPDATE draft_moments SET cover_media_item_id = NULL WHERE id = ?;
					DELETE FROM draft_media_placements WHERE event_id = ? AND media_item_id = ?
				`, fixture.moments[0], fixture.event, fixture.media[0]).Exec(ctx)
				return err
			},
		},
		{
			name: "reorder",
			mutate: func(ctx context.Context, tx bun.Tx, fixture publicationFixture) error {
				_, err := tx.NewRaw(`
					UPDATE draft_moments SET position = position + 10 WHERE event_id = ?;
					UPDATE draft_moments SET position = CASE id WHEN ? THEN 2 WHEN ? THEN 1 ELSE 0 END
					WHERE event_id = ?
				`, fixture.event, fixture.moments[0], fixture.moments[1], fixture.event).Exec(ctx)
				return err
			},
		},
		{
			name: "relink",
			mutate: func(ctx context.Context, tx bun.Tx, fixture publicationFixture) error {
				_, err := tx.NewRaw(`
					UPDATE draft_moments SET cover_media_item_id = NULL WHERE id = ?;
					UPDATE draft_media_placements SET draft_moment_id = ?
					WHERE event_id = ? AND media_item_id = ?
				`, fixture.moments[0], fixture.moments[2], fixture.event, fixture.media[0]).Exec(ctx)
				return err
			},
		},
		{
			name: "access revocation",
			mutate: func(ctx context.Context, tx bun.Tx, fixture publicationFixture) error {
				_, err := tx.NewRaw(`
					DELETE FROM audience_snapshot_entries
					WHERE snapshot_id = (
						SELECT snapshot_id FROM current_audience_snapshots
						WHERE target_kind = 'moment' AND target_id = ?
					) AND recipient_access_generation_id = ?
				`, fixture.moments[0], fixture.access["shared"]).Exec(ctx)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPublicationFixture(t)
			ctx := context.Background()
			_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
			require.NoError(t, err)
			require.NoError(t, fixture.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				if err := test.mutate(ctx, tx, fixture); err != nil {
					return err
				}
				if _, err := tx.NewRaw(`UPDATE events SET version = 8, final_review_complete = true WHERE id = ?`, fixture.event).Exec(ctx); err != nil {
					return err
				}
				update, err := refreshStagedUpdate(ctx, tx, fixture.event, fixture.service.now().UTC())
				require.NotNil(t, update, "the correction must exercise replacement Publication")
				return err
			}))

			second, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: 8})
			require.NoError(t, err)
			var activityRows, newForYouRows int
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM publication_activity_items WHERE publication_id = ?`, second.ID).Scan(ctx, &activityRows))
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM new_for_you_entries WHERE publication_id = ?`, second.ID).Scan(ctx, &newForYouRows))
			assert.Zero(t, activityRows, "a correction with no globally new Media creates no Recipient activity")
			assert.Zero(t, newForYouRows, "a correction with no globally new Media creates no New for you entry")
			assertPublicationHandoffSkipped(t, fixture, second)
		})
	}
}

func TestStagedMoveAcrossAudiencesIdentifiesAffectedMediaAndMoments(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)

	var update *staging.Update
	require.NoError(t, fixture.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, updateErr := tx.NewRaw(`
			UPDATE draft_media_placements SET draft_moment_id = ?
			WHERE event_id = ? AND media_item_id = ?
		`, fixture.moments[1], fixture.event, fixture.media[0]).Exec(ctx); updateErr != nil {
			return updateErr
		}
		var refreshErr error
		update, refreshErr = staging.Refresh(ctx, tx, fixture.event, fixture.service.now().UTC())
		return refreshErr
	}))
	require.NotNil(t, update)
	var access *staging.Change
	for index := range update.Changes {
		if update.Changes[index].Kind == staging.ChangeKindAccess {
			access = &update.Changes[index]
			break
		}
	}
	require.NotNil(t, access, "moving Media between different unchanged Audiences changes access")
	assert.Equal(t, []string{fixture.media[0].String()}, access.MediaItemIDs)
	assert.Equal(t, []string{fixture.moments[0].String(), fixture.moments[1].String()}, access.MomentIDs)
	assert.Equal(t, 3, access.Count, "two prior Recipients lose access and one destination Recipient gains it")
}

func TestStagedCompositeSummaryReportsExactBackendNetChanges(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	first, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)

	var update *staging.Update
	require.NoError(t, fixture.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`
			UPDATE events SET title = 'Composite correction', description = 'Corrected description',
				grouping_timezone = 'America/New_York', version = 8 WHERE id = ?;
			UPDATE draft_moments SET title = 'Corrected Moment', proposed_day = '2026-08-01' WHERE id = ?;
			UPDATE media_items SET width = 1600 WHERE id = ?;
			DELETE FROM audience_snapshot_entries
			WHERE snapshot_id = (
				SELECT snapshot_id FROM current_audience_snapshots
				WHERE target_kind = 'moment' AND target_id = ?
			) AND recipient_access_generation_id = ?;
			INSERT INTO audience_snapshot_entries (
				snapshot_id, recipient_person_id, recipient_access_generation_id
			) SELECT snapshot_id, ?, ? FROM current_audience_snapshots
			WHERE target_kind = 'moment' AND target_id = ?
		`, fixture.event, fixture.moments[0], fixture.media[0],
			fixture.moments[0], fixture.access["shared"],
			fixture.people["none"], fixture.access["none"], fixture.moments[2]).Exec(ctx); err != nil {
			return err
		}
		var refreshErr error
		update, refreshErr = staging.Refresh(ctx, tx, fixture.event, fixture.service.now().UTC())
		return refreshErr
	}))
	require.NotNil(t, update)
	assert.Equal(t, first.ID, update.BasePublicationID)
	require.Len(t, update.Changes, 2, "the composite result contains exact metadata and access categories")
	changes := make(map[staging.ChangeKind]staging.Change, len(update.Changes))
	for _, change := range update.Changes {
		changes[change.Kind] = change
	}

	metadata := changes[staging.ChangeKindMetadata]
	assert.Equal(t, 3, metadata.Count, "one Event, one Moment, and one Media metadata record changed")
	assert.Equal(t, []string{fixture.media[0].String()}, metadata.MediaItemIDs)
	assert.Equal(t, []string{fixture.moments[0].String()}, metadata.MomentIDs)
	assert.Equal(t, []string{"title", "description", "grouping_timezone"}, metadata.EventMetadataFields)

	access := changes[staging.ChangeKindAccess]
	assert.Equal(t, 2, access.Count)
	assert.Equal(t, []string{fixture.media[0].String(), fixture.media[2].String()}, access.MediaItemIDs)
	assert.Equal(t, []string{fixture.moments[0].String(), fixture.moments[2].String()}, access.MomentIDs)
	require.Len(t, access.RecipientAccess, 2)
	accessByPerson := make(map[string]staging.RecipientAccessChange, len(access.RecipientAccess))
	for _, recipient := range access.RecipientAccess {
		accessByPerson[recipient.RecipientPersonID] = recipient
	}
	assert.Equal(t, staging.RecipientAccessChange{
		RecipientPersonID: fixture.people["none"].String(), RecipientName: "none", GrantedMediaCount: 1,
	}, accessByPerson[fixture.people["none"].String()])
	assert.Equal(t, staging.RecipientAccessChange{
		RecipientPersonID: fixture.people["shared"].String(), RecipientName: "shared", RevokedMediaCount: 1,
	}, accessByPerson[fixture.people["shared"].String()])
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
