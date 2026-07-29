//go:build integration

package emaildelivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/smtp"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type immediateSender struct {
	mu       sync.Mutex
	results  []error
	messages []smtp.Message
}

func (s *immediateSender) Send(_ context.Context, message smtp.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, message)
	if len(s.results) == 0 {
		return nil
	}
	result := s.results[0]
	s.results = s.results[1:]
	return result
}

func (s *immediateSender) sent() []smtp.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]smtp.Message(nil), s.messages...)
}

type immediatePreviewSource struct{ contents []byte }

func (source immediatePreviewSource) Thumbnail(context.Context, uuid.UUID, immich.MediaRequest) (immich.MediaResponse, error) {
	return immich.MediaResponse{
		Body: io.NopCloser(bytes.NewReader(source.contents)), StatusCode: http.StatusOK,
		ContentType: "image/jpeg", ContentLength: int64(len(source.contents)),
	}, nil
}

type immediateFixture struct {
	db          *bun.DB
	service     *Service
	sender      *immediateSender
	people      map[string]uuid.UUID
	access      map[string]uuid.UUID
	event       uuid.UUID
	publication uuid.UUID
	moment      uuid.UUID
	media       []uuid.UUID
	base        time.Time
}

func newImmediateFixture(t *testing.T) immediateFixture {
	t.Helper()
	ctx := context.Background()
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(ctx, db))
	sender := new(immediateSender)
	service := New(db, deliveryConfig(), sender)
	service.SetPreviewSource(immediatePreviewSource{contents: previewWithPrivateMetadata(t)})
	fixture := immediateFixture{
		db: db, service: service, sender: sender, people: map[string]uuid.UUID{}, access: map[string]uuid.UUID{},
		event: uuid.New(), publication: uuid.New(), moment: uuid.New(), media: []uuid.UUID{uuid.New(), uuid.New()},
		base: time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond),
	}
	for _, name := range []string{"curator", "alex", "blair"} {
		fixture.people[name], fixture.access[name] = uuid.New(), uuid.New()
		_, err := db.NewRaw(`
			INSERT INTO people (id, display_name, sort_name) VALUES (?, ?, ?);
			INSERT INTO person_roles (person_id, role) VALUES (?, 'recipient');
			INSERT INTO recipient_access_generations
				(id, person_id, generation, state, is_current, onboarding_completed_at)
			VALUES (?, ?, 1, 'completed', true, ?);
			INSERT INTO recipient_emails
				(id, recipient_access_generation_id, email, normalized_email, is_current)
			VALUES (gen_random_uuid(), ?, ?, ?, true);
			INSERT INTO notification_preferences (recipient_access_generation_id, email_preference, updated_at)
			VALUES (?, 'immediate', ?)
		`, fixture.people[name], name, name, fixture.people[name], fixture.access[name], fixture.people[name], fixture.base,
			fixture.access[name], name+"@example.com", name+"@example.com", fixture.access[name], fixture.base).Exec(ctx)
		require.NoError(t, err)
	}
	_, err := db.NewRaw(`
		INSERT INTO person_roles (person_id, role) VALUES (?, 'curator');
		UPDATE system_settings SET setup_complete = true WHERE id = 1;
		INSERT INTO events (id, lifecycle, title, description, grouping_timezone, version, created_at, updated_at)
		VALUES (?, 'published', 'Original title', '', 'UTC', 1, ?, ?);
		INSERT INTO publications
			(id, event_id, revision, editable_version, published_by_person_id, notify_recipients, committed_at)
		VALUES (?, ?, 1, 1, ?, true, ?);
		UPDATE events SET current_publication_id = ? WHERE id = ?;
		INSERT INTO current_published_events
			(event_id, publication_id, title, description, grouping_timezone, committed_at)
		VALUES (?, ?, 'Original title', '', 'UTC', ?);
		INSERT INTO published_event_revisions
			(publication_id, event_id, title, description, grouping_timezone, created_at)
		VALUES (?, ?, 'Original title', '', 'UTC', ?);
		INSERT INTO audience_snapshots (id, target_kind, target_id, approved_by_person_id, approved_at, label)
		VALUES (gen_random_uuid(), 'moment', ?, ?, ?, 'Shared')
	`, fixture.people["curator"], fixture.event, fixture.base, fixture.base,
		fixture.publication, fixture.event, fixture.people["curator"], fixture.base,
		fixture.publication, fixture.event,
		fixture.event, fixture.publication, fixture.base, fixture.publication, fixture.event, fixture.base,
		fixture.moment, fixture.people["curator"], fixture.base).Exec(ctx)
	require.NoError(t, err)
	var snapshot uuid.UUID
	require.NoError(t, db.NewRaw(`SELECT id FROM audience_snapshots WHERE target_id = ?`, fixture.moment).Scan(ctx, &snapshot))
	_, err = db.NewRaw(`
		INSERT INTO draft_moments
			(id, event_id, position, proposed_day, grouping_timezone, source_days, title, attendance_complete, audience_complete)
		VALUES (?, ?, 0, '2026-07-28', 'UTC', ARRAY['2026-07-28'::date], '', true, true);
		INSERT INTO current_audience_snapshots (target_kind, target_id, snapshot_id) VALUES ('moment', ?, ?);
		INSERT INTO audience_snapshot_entries (snapshot_id, recipient_person_id, recipient_access_generation_id)
		VALUES (?, ?, ?), (?, ?, ?);
		INSERT INTO published_moments
			(id, publication_id, draft_moment_id, audience_snapshot_id, position, title, proposed_day)
		VALUES (?, ?, ?, ?, 0, '', '2026-07-28')
	`, fixture.moment, fixture.event, fixture.moment, snapshot,
		snapshot, fixture.people["alex"], fixture.access["alex"], snapshot, fixture.people["blair"], fixture.access["blair"],
		fixture.moment, fixture.publication, fixture.moment, snapshot).Exec(ctx)
	require.NoError(t, err)
	for position, mediaID := range fixture.media {
		assetID := uuid.New()
		_, err := db.NewRaw(`
			INSERT INTO media_items
				(id, immich_asset_id, media_type, width, height, local_date_time, availability, first_seen_at, last_seen_at)
			VALUES (?, ?, 'image', 1200, 800, '2026-07-28T12:00:00Z', 'current', ?, ?);
			INSERT INTO media_backings (id, media_item_id, immich_asset_id, capture_at, filename, linked_at)
			VALUES (gen_random_uuid(), ?, ?, '2026-07-28T12:00:00Z', 'photo.jpg', ?);
			INSERT INTO draft_media_placements (event_id, media_item_id, draft_moment_id, position, created_at)
			VALUES (?, ?, ?, ?, ?);
			INSERT INTO published_media_placements
				(published_moment_id, media_item_id, position, media_type, width, height, local_date_time)
			VALUES (?, ?, ?, 'image', 1200, 800, '2026-07-28T12:00:00Z');
			INSERT INTO current_published_placements (event_id, publication_id, published_moment_id, media_item_id, position)
			VALUES (?, ?, ?, ?, ?)
		`, mediaID, assetID, fixture.base, fixture.base, mediaID, assetID, fixture.base,
			fixture.event, mediaID, fixture.moment, position, fixture.base,
			fixture.moment, mediaID, position, fixture.event, fixture.publication, fixture.moment, mediaID, position).Exec(ctx)
		require.NoError(t, err)
		for _, recipient := range []string{"alex", "blair"} {
			_, err = db.NewRaw(`INSERT INTO current_audience_entitlements
				(event_id, publication_id, recipient_person_id, recipient_access_generation_id, media_item_id)
				VALUES (?, ?, ?, ?, ?)`, fixture.event, fixture.publication, fixture.people[recipient], fixture.access[recipient], mediaID).Exec(ctx)
			require.NoError(t, err)
		}
	}
	return fixture
}

func (fixture immediateFixture) addPublicationActivity(t *testing.T, at time.Time, mediaIDs ...uuid.UUID) {
	t.Helper()
	_, err := fixture.db.NewRaw(`INSERT INTO publication_activity_items
		(publication_id, recipient_access_generation_id, created_at) VALUES (?, ?, ?)
		ON CONFLICT (publication_id, recipient_access_generation_id) DO UPDATE SET created_at = EXCLUDED.created_at`,
		fixture.publication, fixture.access["alex"], at).Exec(context.Background())
	require.NoError(t, err)
	for _, mediaID := range mediaIDs {
		_, err = fixture.db.NewRaw(`INSERT INTO publication_notification_media
			(publication_id, recipient_access_generation_id, media_item_id) VALUES (?, ?, ?)
			ON CONFLICT DO NOTHING`, fixture.publication, fixture.access["alex"], mediaID).Exec(context.Background())
		require.NoError(t, err)
	}
}

func (fixture immediateFixture) addComment(t *testing.T, at time.Time, body string) uuid.UUID {
	t.Helper()
	commentID := uuid.New()
	_, err := fixture.db.NewRaw(`
		INSERT INTO comments
			(id, media_item_id, author_person_id, author_access_generation_id, idempotency_key, body, created_at, updated_at)
		VALUES (?, ?, ?, ?, gen_random_uuid(), ?, ?, ?);
		INSERT INTO comment_subscriptions
			(media_item_id, recipient_access_generation_id, muted, created_at, updated_at)
		VALUES (?, ?, false, ?, ?) ON CONFLICT (media_item_id, recipient_access_generation_id)
		DO UPDATE SET muted = false, updated_at = EXCLUDED.updated_at
	`, commentID, fixture.media[0], fixture.people["blair"], fixture.access["blair"], body, at, at,
		fixture.media[0], fixture.access["alex"], at, at).Exec(context.Background())
	require.NoError(t, err)
	return commentID
}

func (fixture immediateFixture) queueComment(t *testing.T, commentID uuid.UUID) {
	t.Helper()
	require.NoError(t, fixture.db.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
		return fixture.service.QueueComment(ctx, tx, fixture.access["alex"], commentID)
	}))
}

func (fixture immediateFixture) leasedBatchJob(t *testing.T, batchID int64) worker.Job {
	t.Helper()
	payload := []byte(`{"batch_id":` + fmt.Sprint(batchID) + `}`)
	var id int64
	require.NoError(t, fixture.db.NewRaw(`INSERT INTO jobs
		(kind, payload, status, lease_owner, lease_expires_at)
		VALUES (?, ?::jsonb, 'running', 'immediate-test', clock_timestamp() + interval '1 hour') RETURNING id`,
		ImmediateJobKind, string(payload)).Scan(context.Background(), &id))
	return worker.Job{ID: id, Kind: ImmediateJobKind, Payload: payload, LeaseOwner: "immediate-test"}
}

func TestImmediateEmailUsesOneRecipientWindowWithExactBoundary(t *testing.T) {
	fixture := newImmediateFixture(t)
	fixture.addPublicationActivity(t, fixture.base, fixture.media[0])
	require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))
	inside := fixture.addComment(t, fixture.base.Add(coalescingWindow-time.Microsecond), "Inside")
	fixture.queueComment(t, inside)
	boundary := fixture.addComment(t, fixture.base.Add(coalescingWindow), "Boundary")
	fixture.queueComment(t, boundary)

	type batch struct {
		ID      int64
		Started time.Time `bun:"window_started_at"`
		Closes  time.Time `bun:"closes_at"`
		Items   int
	}
	var batches []batch
	require.NoError(t, fixture.db.NewRaw(`SELECT batch.id, batch.window_started_at, batch.closes_at, count(item.id) AS items
		FROM notification_batches AS batch JOIN notification_batch_items AS item ON item.batch_id = batch.id
		GROUP BY batch.id ORDER BY batch.window_started_at`).Scan(context.Background(), &batches))
	require.Len(t, batches, 2)
	assert.Equal(t, fixture.base, batches[0].Started)
	assert.Equal(t, fixture.base.Add(coalescingWindow), batches[0].Closes)
	assert.Equal(t, 2, batches[0].Items)
	assert.Equal(t, fixture.base.Add(coalescingWindow), batches[1].Started)
	assert.Equal(t, fixture.base.Add(2*coalescingWindow), batches[1].Closes)
	assert.Equal(t, 1, batches[1].Items)
	var outbox int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM outbox_events WHERE kind = ?`, ImmediateJobKind).Scan(context.Background(), &outbox))
	assert.Equal(t, 2, outbox, "each durable window has exactly one delayed delivery event")
}

func TestImmediateEmailRecomputesSurvivorsAndStripsPreviewMetadata(t *testing.T) {
	fixture := newImmediateFixture(t)
	fixture.addPublicationActivity(t, fixture.base, fixture.media...)
	require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))
	deleted := fixture.addComment(t, fixture.base.Add(time.Minute), "Delete before delivery")
	fixture.queueComment(t, deleted)
	_, err := fixture.db.NewRaw(`
		UPDATE current_published_events SET title = 'Corrected safe title' WHERE event_id = ?;
		UPDATE comments SET state = 'deleted', deleted_at = clock_timestamp() WHERE id = ?;
		DELETE FROM current_audience_entitlements
		WHERE recipient_access_generation_id = ? AND media_item_id = ?
	`, fixture.event, deleted, fixture.access["alex"], fixture.media[1]).Exec(context.Background())
	require.NoError(t, err)
	var batchID int64
	require.NoError(t, fixture.db.NewRaw(`SELECT id FROM notification_batches ORDER BY id LIMIT 1`).Scan(context.Background(), &batchID))

	require.NoError(t, fixture.service.HandleImmediate(context.Background(), fixture.leasedBatchJob(t, batchID)))
	messages := fixture.sender.sent()
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Body, "Corrected safe title: 1 new item")
	assert.NotContains(t, messages[0].Body, "Blair commented")
	require.NotNil(t, messages[0].Embedded)
	assert.NotContains(t, string(messages[0].Embedded.Data), "private-gps-metadata")
	decoded, err := jpeg.Decode(bytes.NewReader(messages[0].Embedded.Data))
	require.NoError(t, err)
	assert.LessOrEqual(t, decoded.Bounds().Dx(), maxPreviewPixels)
	assert.LessOrEqual(t, decoded.Bounds().Dy(), maxPreviewPixels)
}

func TestImmediateEmailReauthorizesBetweenAssemblyAndSendAndRetriesDurably(t *testing.T) {
	t.Run("authorization loss suppresses SMTP", func(t *testing.T) {
		fixture := newImmediateFixture(t)
		fixture.addPublicationActivity(t, fixture.base, fixture.media[0])
		require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))
		var batchID int64
		require.NoError(t, fixture.db.NewRaw(`SELECT id FROM notification_batches`).Scan(context.Background(), &batchID))
		fixture.service.beforeImmediateSend = func() {
			_, err := fixture.db.NewRaw(`DELETE FROM current_audience_entitlements
				WHERE recipient_access_generation_id = ?`, fixture.access["alex"]).Exec(context.Background())
			require.NoError(t, err)
		}

		require.NoError(t, fixture.service.HandleImmediate(context.Background(), fixture.leasedBatchJob(t, batchID)))
		assert.Empty(t, fixture.sender.sent())
		var status string
		require.NoError(t, fixture.db.NewRaw(`SELECT status FROM notification_batches WHERE id = ?`, batchID).Scan(context.Background(), &status))
		assert.Equal(t, "suppressed", status)
	})

	t.Run("temporary SMTP failure preserves retryable batch", func(t *testing.T) {
		fixture := newImmediateFixture(t)
		fixture.sender.results = []error{errors.New("private provider failure"), nil}
		fixture.addPublicationActivity(t, fixture.base, fixture.media[0])
		require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))
		var batchID int64
		require.NoError(t, fixture.db.NewRaw(`SELECT id FROM notification_batches`).Scan(context.Background(), &batchID))
		job := fixture.leasedBatchJob(t, batchID)

		err := fixture.service.HandleImmediate(context.Background(), job)
		require.EqualError(t, err, "smtp_unavailable")
		var status, diagnostic string
		var attempts int
		require.NoError(t, fixture.db.NewRaw(`SELECT status, attempts, last_safe_error FROM notification_batches WHERE id = ?`, batchID).Scan(context.Background(), &status, &attempts, &diagnostic))
		assert.Equal(t, "pending", status)
		assert.Equal(t, 1, attempts)
		assert.Equal(t, "smtp_unavailable", diagnostic)

		require.NoError(t, fixture.service.HandleImmediate(context.Background(), job))
		require.NoError(t, fixture.db.NewRaw(`SELECT status, attempts FROM notification_batches WHERE id = ?`, batchID).Scan(context.Background(), &status, &attempts))
		assert.Equal(t, "sent", status)
		assert.Equal(t, 2, attempts)
	})

	t.Run("expired retry window becomes a durable failure without another SMTP attempt", func(t *testing.T) {
		fixture := newImmediateFixture(t)
		fixture.service.cfg.RetryWindow = 30 * time.Minute
		fixture.addPublicationActivity(t, fixture.base, fixture.media[0])
		require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))
		var batchID int64
		require.NoError(t, fixture.db.NewRaw(`SELECT id FROM notification_batches`).Scan(context.Background(), &batchID))

		err := fixture.service.HandleImmediate(context.Background(), fixture.leasedBatchJob(t, batchID))
		require.EqualError(t, err, "retry_window_exhausted")
		assert.Empty(t, fixture.sender.sent())
		var status, diagnostic string
		require.NoError(t, fixture.db.NewRaw(`SELECT status, last_safe_error FROM notification_batches WHERE id = ?`, batchID).Scan(context.Background(), &status, &diagnostic))
		assert.Equal(t, "failed", status)
		assert.Equal(t, "retry_window_exhausted", diagnostic)
	})

	t.Run("permanent SMTP rejection disables later optional email", func(t *testing.T) {
		fixture := newImmediateFixture(t)
		fixture.sender.results = []error{&smtp.DeliveryError{Diagnostic: "recipient_rejected", Temporary: false}}
		fixture.addPublicationActivity(t, fixture.base, fixture.media[0])
		require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))
		var batchID int64
		require.NoError(t, fixture.db.NewRaw(`SELECT id FROM notification_batches`).Scan(context.Background(), &batchID))

		err := fixture.service.HandleImmediate(context.Background(), fixture.leasedBatchJob(t, batchID))
		require.EqualError(t, err, "recipient_rejected")
		var status, preference string
		require.NoError(t, fixture.db.NewRaw(`SELECT status FROM notification_batches WHERE id = ?`, batchID).Scan(context.Background(), &status))
		require.NoError(t, fixture.db.NewRaw(`SELECT email_preference FROM notification_preferences
			WHERE recipient_access_generation_id = ?`, fixture.access["alex"]).Scan(context.Background(), &preference))
		assert.Equal(t, "failed", status)
		assert.Equal(t, "none", preference)
	})
}

func TestImmediateEmailCreatesNoBacklogAfterEligibilityIsRestored(t *testing.T) {
	fixture := newImmediateFixture(t)
	fixture.addPublicationActivity(t, fixture.base, fixture.media[0])
	_, err := fixture.db.NewRaw(`UPDATE recipient_access_generations SET state = 'onboarding', onboarding_completed_at = NULL WHERE id = ?`, fixture.access["alex"]).Exec(context.Background())
	require.NoError(t, err)
	require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))
	_, err = fixture.db.NewRaw(`UPDATE recipient_access_generations SET state = 'completed', onboarding_completed_at = clock_timestamp() WHERE id = ?`, fixture.access["alex"]).Exec(context.Background())
	require.NoError(t, err)
	var batches int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM notification_batches`).Scan(context.Background(), &batches))
	assert.Zero(t, batches, "Onboarding completion does not sweep historical Publication activity")

	commentID := fixture.addComment(t, fixture.base.Add(time.Minute), "Preference disabled")
	_, err = fixture.db.NewRaw(`UPDATE notification_preferences SET email_preference = 'none' WHERE recipient_access_generation_id = ?`, fixture.access["alex"]).Exec(context.Background())
	require.NoError(t, err)
	fixture.queueComment(t, commentID)
	_, err = fixture.db.NewRaw(`UPDATE notification_preferences SET email_preference = 'immediate' WHERE recipient_access_generation_id = ?`, fixture.access["alex"]).Exec(context.Background())
	require.NoError(t, err)
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM notification_batches`).Scan(context.Background(), &batches))
	assert.Zero(t, batches, "restoring optional delivery does not sweep missed Comment activity")
}

func previewWithPrivateMetadata(t *testing.T) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 800, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 800; x++ {
			picture.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 90, A: 255})
		}
	}
	var encoded bytes.Buffer
	require.NoError(t, jpeg.Encode(&encoded, picture, &jpeg.Options{Quality: 90}))
	contents := encoded.Bytes()
	metadata := []byte("private-gps-metadata")
	segment := append([]byte{0xff, 0xe1, 0, byte(len(metadata) + 2)}, metadata...)
	return append(append([]byte{}, contents[:2]...), append(segment, contents[2:]...)...)
}
