//go:build integration

package events

import (
	"context"
	"errors"
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
	_, err := db.NewRaw(`
		UPDATE system_settings SET setup_complete = true WHERE id = 1;
		INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Curator', 'curator');
		INSERT INTO person_roles (person_id, role) VALUES (?, 'curator')
	`, fixture.actor.PersonID, fixture.actor.PersonID).Exec(ctx)
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
	emptyPreview, err := fixture.service.PreviewEvent(ctx, fixture.actor, fixture.event, fixture.people["none"])
	require.NoError(t, err)
	assert.False(t, emptyPreview.Authorized)
	assert.Empty(t, emptyPreview.Title, "an empty Audience must not reveal Event metadata")
	assert.Empty(t, emptyPreview.Media)
	assert.Equal(t, PreviewCapabilities{}, emptyPreview.Capabilities)
	var pendingNewForYou bool
	require.NoError(t, fixture.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM new_for_you_entries WHERE recipient_access_generation_id = ?)`, fixture.access["pending"]).Scan(ctx, &pendingNewForYou))
	assert.False(t, pendingNewForYou)

	for table, expected := range map[string]int{
		"publications": 1, "published_event_revisions": 1, "published_moments": 3,
		"published_media_placements": 3, "audience_entries": 3,
		"current_published_events": 1, "current_published_placements": 3,
		"current_audience_entitlements": 3, "current_recipient_event_covers": 3,
		"new_for_you_entries": 2, "published_search_documents": 3,
		"publication_activity_items": 2, "publication_curator_activity_items": 1,
		"publication_audit_events": 1, "outbox_events": 1,
		"publication_preview_audit_events": 2,
	} {
		var count int
		require.NoError(t, fixture.db.NewRaw("SELECT count(*) FROM "+table).Scan(ctx, &count), table)
		assert.Equal(t, expected, count, table)
	}

	_, err = fixture.db.NewRaw(`UPDATE events SET title = 'Corrected weekend', version = 8 WHERE id = ?`, fixture.event).Exec(ctx)
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
	assert.Equal(t, 6, historyAudiences)
	assert.Equal(t, 3, currentPlacements)
	assert.Equal(t, 2, unseen, "only authorized completed Recipients receive current Publication candidates")
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

	_, err = fixture.db.NewRaw(`UPDATE recipient_access_generations SET state = 'suspended' WHERE id = ?`, fixture.access["shared"]).Exec(ctx)
	require.NoError(t, err)
	_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	assert.ErrorIs(t, err, ErrAudienceNotCurrent)
	assertUnpublished()
}

func TestFailureAtEveryPublicationBoundaryRollsBackEverything(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	steps := []PublicationStep{
		PublicationStepLocked, PublicationStepValidated, PublicationStepHistory,
		PublicationStepMetadata, PublicationStepPlacements, PublicationStepAudiences,
		PublicationStepEntitlements, PublicationStepActivity, PublicationStepAudit,
		PublicationStepOutbox,
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
		for _, table := range []string{
			"publications", "published_event_revisions", "published_moments",
			"published_media_placements", "audience_entries", "current_published_events",
			"current_published_placements", "current_audience_entitlements",
			"current_recipient_event_covers", "new_for_you_entries", "published_search_documents",
			"publication_activity_items", "publication_curator_activity_items",
			"publication_audit_events", "outbox_events",
		} {
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
}

func TestConcurrentRecipientReadSeesCompletePriorProjectionUntilCommit(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)
	_, err = fixture.db.NewRaw(`UPDATE events SET title = 'Atomic correction', version = 8 WHERE id = ?`, fixture.event).Exec(ctx)
	require.NoError(t, err)

	reached := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	fixture.service.failPublicationStep = func(step PublicationStep) error {
		if step == PublicationStepEntitlements {
			once.Do(func() { close(reached) })
			<-release
		}
		return nil
	}
	published := make(chan error, 1)
	go func() {
		_, publishErr := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: 8})
		published <- publishErr
	}()
	<-reached
	prior, err := fixture.service.RecipientEvent(ctx, fixture.actorFor("shared"), fixture.event)
	require.NoError(t, err)
	assert.Equal(t, "Family weekend", prior.Title)
	assert.Equal(t, 1, prior.MediaCount)
	close(release)
	require.NoError(t, <-published)
	current, err := fixture.service.RecipientEvent(ctx, fixture.actorFor("shared"), fixture.event)
	require.NoError(t, err)
	assert.Equal(t, "Atomic correction", current.Title)
	assert.Equal(t, 1, current.MediaCount)
}
