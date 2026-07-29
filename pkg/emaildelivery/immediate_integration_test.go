//go:build integration

package emaildelivery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/outbox"
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
	accepted int
}

func (s *immediateSender) Send(_ context.Context, message smtp.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, message)
	if len(s.results) == 0 {
		s.accepted++
		return nil
	}
	result := s.results[0]
	s.results = s.results[1:]
	if result == nil {
		s.accepted++
	}
	return result
}

func (s *immediateSender) sent() []smtp.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]smtp.Message(nil), s.messages...)
}

func (s *immediateSender) acceptedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accepted
}

type blockingImmediateSender struct {
	entered  chan struct{}
	release  chan struct{}
	mu       sync.Mutex
	accepted []smtp.Message
}

func (s *blockingImmediateSender) Send(ctx context.Context, message smtp.Message) error {
	close(s.entered)
	select {
	case <-s.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accepted = append(s.accepted, message)
	return nil
}

func (s *blockingImmediateSender) acceptedMessages() []smtp.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]smtp.Message(nil), s.accepted...)
}

type immediatePreviewSource struct {
	mu        sync.Mutex
	contents  []byte
	allowed   map[uuid.UUID]bool
	requested []uuid.UUID
}

func (source *immediatePreviewSource) EmailThumbnail(_ context.Context, assetID uuid.UUID, _ immich.MediaRequest) (immich.MediaResponse, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.requested = append(source.requested, assetID)
	if source.allowed != nil && !source.allowed[assetID] {
		return immich.MediaResponse{Body: http.NoBody, StatusCode: http.StatusNotFound}, nil
	}
	return immich.MediaResponse{
		Body: io.NopCloser(bytes.NewReader(source.contents)), StatusCode: http.StatusOK,
		ContentType: "image/jpeg", ContentLength: int64(len(source.contents)),
	}, nil
}

func (source *immediatePreviewSource) allowOnly(assetID uuid.UUID) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.allowed = map[uuid.UUID]bool{assetID: true}
}

func (source *immediatePreviewSource) requests() []uuid.UUID {
	source.mu.Lock()
	defer source.mu.Unlock()
	return append([]uuid.UUID(nil), source.requested...)
}

type immediateFixture struct {
	db          *bun.DB
	service     *Service
	sender      *immediateSender
	preview     *immediatePreviewSource
	people      map[string]uuid.UUID
	access      map[string]uuid.UUID
	event       uuid.UUID
	publication uuid.UUID
	moment      uuid.UUID
	media       []uuid.UUID
	assets      []uuid.UUID
	base        time.Time
}

func newImmediateFixture(t *testing.T) immediateFixture {
	t.Helper()
	ctx := context.Background()
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(ctx, db))
	sender := new(immediateSender)
	preview := &immediatePreviewSource{contents: previewWithPrivateMetadata(t)}
	service := New(db, deliveryConfig(), sender)
	service.SetPublicURL("https://memento.example")
	service.SetPreviewSource(preview)
	fixture := immediateFixture{
		db: db, service: service, sender: sender, preview: preview, people: map[string]uuid.UUID{}, access: map[string]uuid.UUID{},
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
			INSERT INTO onboarding_choices
				(recipient_access_generation_id, privacy_acknowledged, engagement_acknowledged,
				 interest_list_acknowledged, email_previews_acknowledged, push_guidance_acknowledged,
				 informed_choices_version, email_preference, completed_at)
			VALUES (?, true, true, true, true, true, 2, 'immediate', ?);
			INSERT INTO recipient_emails
				(id, recipient_access_generation_id, email, normalized_email, is_current)
			VALUES (gen_random_uuid(), ?, ?, ?, true);
			INSERT INTO notification_preferences (recipient_access_generation_id, email_preference, updated_at)
			VALUES (?, 'immediate', ?)
		`, fixture.people[name], name, name, fixture.people[name], fixture.access[name], fixture.people[name], fixture.base,
			fixture.access[name], fixture.base, fixture.access[name], name+"@example.com", name+"@example.com",
			fixture.access[name], fixture.base).Exec(ctx)
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
		fixture.assets = append(fixture.assets, assetID)
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
	fixture.addPublicationActivityFor(t, "alex", at, mediaIDs...)
}

func (fixture immediateFixture) addPublicationActivityFor(t *testing.T, recipient string, at time.Time, mediaIDs ...uuid.UUID) {
	t.Helper()
	_, err := fixture.db.NewRaw(`INSERT INTO publication_activity_items
		(publication_id, recipient_access_generation_id, created_at) VALUES (?, ?, ?)
		ON CONFLICT (publication_id, recipient_access_generation_id) DO UPDATE SET created_at = EXCLUDED.created_at`,
		fixture.publication, fixture.access[recipient], at).Exec(context.Background())
	require.NoError(t, err)
	for _, mediaID := range mediaIDs {
		_, err = fixture.db.NewRaw(`INSERT INTO publication_notification_media
			(publication_id, recipient_access_generation_id, media_item_id) VALUES (?, ?, ?)
			ON CONFLICT DO NOTHING`, fixture.publication, fixture.access[recipient], mediaID).Exec(context.Background())
		require.NoError(t, err)
	}
}

func (fixture immediateFixture) addLargePublicationActivity(t *testing.T, count int) {
	t.Helper()
	fixture.addPublicationActivity(t, fixture.base)
	for position := range count {
		mediaID := uuid.NewMD5(uuid.Nil, []byte(fmt.Sprintf("large-publication-media-%04d", position)))
		assetID := uuid.NewMD5(uuid.Nil, []byte(fmt.Sprintf("large-publication-asset-%04d", position)))
		_, err := fixture.db.NewRaw(`
			INSERT INTO media_items
				(id, immich_asset_id, media_type, width, height, local_date_time, availability, first_seen_at, last_seen_at)
			VALUES (?, ?, 'image', 1200, 800, '2026-07-28T12:00:00Z', 'current', ?, ?);
			INSERT INTO published_media_placements
				(published_moment_id, media_item_id, position, media_type, width, height, local_date_time)
			VALUES (?, ?, ?, 'image', 1200, 800, '2026-07-28T12:00:00Z');
			INSERT INTO current_published_placements
				(event_id, publication_id, published_moment_id, media_item_id, position)
			VALUES (?, ?, ?, ?, ?);
			INSERT INTO current_audience_entitlements
				(event_id, publication_id, recipient_person_id, recipient_access_generation_id, media_item_id)
			VALUES (?, ?, ?, ?, ?);
			INSERT INTO publication_notification_media
				(publication_id, recipient_access_generation_id, media_item_id)
			VALUES (?, ?, ?)
		`, mediaID, assetID, fixture.base, fixture.base,
			fixture.moment, mediaID, position+100,
			fixture.event, fixture.publication, fixture.moment, mediaID, position+100,
			fixture.event, fixture.publication, fixture.people["alex"], fixture.access["alex"], mediaID,
			fixture.publication, fixture.access["alex"], mediaID).Exec(context.Background())
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

func TestImmediateEmailBoundsBatchWorkAndMessageSize(t *testing.T) {
	fixture := newImmediateFixture(t)
	for index := range maxImmediateBatchItems + 1 {
		commentID := fixture.addComment(t, fixture.base.Add(time.Duration(index)*time.Microsecond), fmt.Sprintf("Comment %d", index))
		fixture.queueComment(t, commentID)
	}

	var batchID int64
	var itemCount int
	var truncated bool
	require.NoError(t, fixture.db.NewRaw(`SELECT id, truncated FROM notification_batches`).Scan(context.Background(), &batchID, &truncated))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM notification_batch_items WHERE batch_id = ?`, batchID).Scan(context.Background(), &itemCount))
	assert.Equal(t, maxImmediateBatchItems, itemCount)
	assert.True(t, truncated, "overflow is durably summarized instead of growing delivery work")

	require.NoError(t, fixture.service.HandleImmediate(context.Background(), fixture.leasedBatchJob(t, batchID)))
	messages := fixture.sender.sent()
	require.Len(t, messages, 1)
	assert.Equal(t, maxImmediateBatchItems, strings.Count(messages[0].Body, "Blair commented on an item you can access."))
	assert.Contains(t, messages[0].Body, "Additional activity is available in Memento.")
	assert.LessOrEqual(t, len(messages[0].Body), immediateBodyLineBudget+2048)
}

func TestImmediateEmailBoundsLargePublicationCandidateWork(t *testing.T) {
	fixture := newImmediateFixture(t)
	fixture.addLargePublicationActivity(t, maxImmediatePublicationMedia+25)
	require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))
	var batchID int64
	require.NoError(t, fixture.db.NewRaw(`SELECT id FROM notification_batches`).Scan(context.Background(), &batchID))

	require.NoError(t, fixture.service.HandleImmediate(context.Background(), fixture.leasedBatchJob(t, batchID)))
	messages := fixture.sender.sent()
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Body, "Original title: 100+ new items")
	assert.NotContains(t, messages[0].Body, "125 new items", "delivery does not count or expose candidates beyond its bounded authorization set")
}

func TestImmediateEmailReanchorsOverlappingOutOfOrderActivityAtTheExactBoundary(t *testing.T) {
	fixture := newImmediateFixture(t)
	first := fixture.addComment(t, fixture.base.Add(10*time.Minute), "First observed")
	fixture.queueComment(t, first)
	overlappingOlder := fixture.addComment(t, fixture.base, "Older overlapping")
	fixture.queueComment(t, overlappingOlder)
	exactOlderBoundary := fixture.addComment(t, fixture.base.Add(-coalescingWindow), "Exact older boundary")
	fixture.queueComment(t, exactOlderBoundary)

	type batch struct {
		Started time.Time `bun:"window_started_at"`
		Closes  time.Time `bun:"closes_at"`
		Items   int
	}
	var batches []batch
	require.NoError(t, fixture.db.NewRaw(`SELECT batch.window_started_at, batch.closes_at, count(item.id) AS items
		FROM notification_batches AS batch JOIN notification_batch_items AS item ON item.batch_id = batch.id
		GROUP BY batch.id ORDER BY batch.window_started_at`).Scan(context.Background(), &batches))
	require.Len(t, batches, 2)
	assert.Equal(t, fixture.base.Add(-coalescingWindow), batches[0].Started)
	assert.Equal(t, fixture.base, batches[0].Closes)
	assert.Equal(t, 1, batches[0].Items, "an exact older close boundary starts a separate window")
	assert.Equal(t, fixture.base, batches[1].Started)
	assert.Equal(t, fixture.base.Add(coalescingWindow), batches[1].Closes)
	assert.Equal(t, 2, batches[1].Items, "overlapping out-of-order activity reanchors the existing window")

	var availableAt time.Time
	require.NoError(t, fixture.db.NewRaw(`SELECT available_at FROM outbox_events
		WHERE kind = ? AND aggregate_id = (SELECT public_id::text FROM notification_batches WHERE window_started_at = ?)`,
		ImmediateJobKind, fixture.base).Scan(context.Background(), &availableAt))
	assert.Equal(t, fixture.base.Add(coalescingWindow), availableAt, "the durable send schedule follows the reanchored close")
}

func TestImmediateEmailSendsEachRecipientBatchOnlyToItsExactCurrentAddress(t *testing.T) {
	fixture := newImmediateFixture(t)
	fixture.addPublicationActivityFor(t, "alex", fixture.base, fixture.media[0])
	fixture.addPublicationActivityFor(t, "blair", fixture.base, fixture.media[0])
	require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))

	type recipientBatch struct {
		ID      int64
		Address string
	}
	var batches []recipientBatch
	require.NoError(t, fixture.db.NewRaw(`SELECT batch.id, email.email AS address
		FROM notification_batches AS batch
		JOIN recipient_emails AS email
		  ON email.recipient_access_generation_id = batch.recipient_access_generation_id AND email.is_current
		ORDER BY email.email`).Scan(context.Background(), &batches))
	require.Len(t, batches, 2)
	require.Equal(t, []recipientBatch{
		{ID: batches[0].ID, Address: "alex@example.com"},
		{ID: batches[1].ID, Address: "blair@example.com"},
	}, batches)

	for _, batch := range batches {
		require.NoError(t, fixture.service.HandleImmediate(context.Background(), fixture.leasedBatchJob(t, batch.ID)))
	}
	messages := fixture.sender.sent()
	require.Len(t, messages, 2)
	assert.Equal(t, "alex@example.com", messages[0].To)
	assert.Equal(t, "blair@example.com", messages[1].To)
}

func TestImmediateEmailIncludesOneClickUnsubscribeAndPrivacyDisclosure(t *testing.T) {
	fixture := newImmediateFixture(t)
	fixture.addPublicationActivity(t, fixture.base, fixture.media[0])
	require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))
	var batchID int64
	require.NoError(t, fixture.db.NewRaw(`SELECT id FROM notification_batches`).Scan(context.Background(), &batchID))

	require.NoError(t, fixture.service.HandleImmediate(context.Background(), fixture.leasedBatchJob(t, batchID)))
	messages := fixture.sender.sent()
	require.Len(t, messages, 1)
	message := messages[0]
	assert.Contains(t, message.Body, "This email excludes hidden item counts and Moment details.")
	assert.Contains(t, message.Body, "Manage optional email or unsubscribe: "+message.UnsubscribeURL)
	parsed, err := url.Parse(message.UnsubscribeURL)
	require.NoError(t, err)
	assert.Equal(t, "https://memento.example", parsed.Scheme+"://"+parsed.Host)
	assert.Equal(t, "/api/email/preferences/unsubscribe", parsed.Path)
	encoded := parsed.Query().Get("token")
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	require.NoError(t, err)
	require.Len(t, raw, 32, "unsubscribe credentials contain at least 256 bits of random material")
	hash := sha256.Sum256(raw)
	var storedHash []byte
	require.NoError(t, fixture.db.NewRaw(`SELECT token_hash FROM notification_preference_tokens
		WHERE notification_batch_id = ?`, batchID).Scan(context.Background(), &storedHash))
	assert.Equal(t, hash[:], storedHash)
	assert.NotEqual(t, raw, storedHash, "only the one-way token hash is persisted")

	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	RegisterRoutes(e, NewHandler(fixture.service))

	invalidToken := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x5a}, 32))
	invalidRequest := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/email/preferences/unsubscribe?token="+invalidToken, nil)
	invalidResponse := httptest.NewRecorder()
	e.ServeHTTP(invalidResponse, invalidRequest)
	assert.Equal(t, http.StatusNotFound, invalidResponse.Code)

	_, err = fixture.db.NewRaw(`UPDATE notification_preference_tokens SET expires_at = clock_timestamp() - interval '1 second'
		WHERE notification_batch_id = ?`, batchID).Exec(context.Background())
	require.NoError(t, err)
	expiredRequest := httptest.NewRequestWithContext(context.Background(), http.MethodGet, parsed.RequestURI(), nil)
	expiredResponse := httptest.NewRecorder()
	e.ServeHTTP(expiredResponse, expiredRequest)
	assert.Equal(t, http.StatusNotFound, expiredResponse.Code)

	_, err = fixture.db.NewRaw(`UPDATE notification_preference_tokens SET expires_at = clock_timestamp() + interval '1 year'
		WHERE notification_batch_id = ?`, batchID).Exec(context.Background())
	require.NoError(t, err)
	validRequest := httptest.NewRequestWithContext(context.Background(), http.MethodGet, parsed.RequestURI(), nil)
	validResponse := httptest.NewRecorder()
	e.ServeHTTP(validResponse, validRequest)
	assert.Equal(t, http.StatusOK, validResponse.Code)
	assert.Contains(t, validResponse.Body.String(), "Unsubscribe")
	assert.NotContains(t, validResponse.Body.String(), encoded)
	var preference string
	require.NoError(t, fixture.db.NewRaw(`SELECT email_preference FROM notification_preferences
		WHERE recipient_access_generation_id = ?`, fixture.access["alex"]).Scan(context.Background(), &preference))
	assert.Equal(t, "immediate", preference, "authenticated GET confirmation must not mutate durable preference")

	for range 2 {
		request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, parsed.RequestURI(),
			strings.NewReader("List-Unsubscribe=One-Click"))
		request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
		response := httptest.NewRecorder()
		e.ServeHTTP(response, request)
		assert.Equal(t, http.StatusOK, response.Code, "one-click unsubscribe is safely idempotent")
		assert.Equal(t, "no-store", response.Header().Get(echo.HeaderCacheControl))
		assert.Equal(t, "no-referrer", response.Header().Get("Referrer-Policy"))
	}
	require.NoError(t, fixture.db.NewRaw(`SELECT email_preference FROM notification_preferences
		WHERE recipient_access_generation_id = ?`, fixture.access["alex"]).Scan(context.Background(), &preference))
	assert.Equal(t, "none", preference)
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
	`, fixture.event, deleted, fixture.access["alex"], fixture.media[0]).Exec(context.Background())
	require.NoError(t, err)
	fixture.preview.allowOnly(fixture.assets[1])
	var batchID int64
	require.NoError(t, fixture.db.NewRaw(`SELECT id FROM notification_batches ORDER BY id LIMIT 1`).Scan(context.Background(), &batchID))

	require.NoError(t, fixture.service.HandleImmediate(context.Background(), fixture.leasedBatchJob(t, batchID)))
	messages := fixture.sender.sent()
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Body, "Corrected safe title: 1 new item")
	assert.NotContains(t, messages[0].Body, "Blair commented")
	require.NotNil(t, messages[0].Embedded)
	assert.Equal(t, []uuid.UUID{fixture.assets[1]}, fixture.preview.requests(), "only the currently authorized surviving asset is fetched")
	assert.NotContains(t, string(messages[0].Embedded.Data), "private-gps-metadata")
	decoded, err := jpeg.Decode(bytes.NewReader(messages[0].Embedded.Data))
	require.NoError(t, err)
	assert.LessOrEqual(t, decoded.Bounds().Dx(), maxPreviewPixels)
	assert.LessOrEqual(t, decoded.Bounds().Dy(), maxPreviewPixels)
}

func TestImmediateEmailOmitsPreviewWithoutPersistedDisclosureAcknowledgment(t *testing.T) {
	fixture := newImmediateFixture(t)
	fixture.addPublicationActivity(t, fixture.base, fixture.media[0])
	require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))
	_, err := fixture.db.NewRaw(`UPDATE onboarding_choices
		SET email_previews_acknowledged = false, push_guidance_acknowledged = false, informed_choices_version = 1
		WHERE recipient_access_generation_id = ?`, fixture.access["alex"]).Exec(context.Background())
	require.NoError(t, err)
	var batchID int64
	require.NoError(t, fixture.db.NewRaw(`SELECT id FROM notification_batches`).Scan(context.Background(), &batchID))

	require.NoError(t, fixture.service.HandleImmediate(context.Background(), fixture.leasedBatchJob(t, batchID)))
	messages := fixture.sender.sent()
	require.Len(t, messages, 1, "a migrated recipient remains eligible for immediate text email")
	assert.Nil(t, messages[0].Embedded)
	assert.Empty(t, fixture.preview.requests(), "private image bytes are not fetched without preview disclosure acknowledgment")
}

func TestImmediateCommentEligibilityIsSharedByQueueAndSend(t *testing.T) {
	fixture := newImmediateFixture(t)
	commentID := fixture.addComment(t, fixture.base, "Private eligibility")
	_, err := fixture.db.NewRaw(`UPDATE comment_subscriptions SET muted = true
		WHERE media_item_id = ? AND recipient_access_generation_id = ?`,
		fixture.media[0], fixture.access["alex"]).Exec(context.Background())
	require.NoError(t, err)
	fixture.queueComment(t, commentID)
	var batches int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM notification_batches`).Scan(context.Background(), &batches))
	assert.Zero(t, batches, "muted Comment activity is ineligible at queue time")

	_, err = fixture.db.NewRaw(`UPDATE comment_subscriptions SET muted = false
		WHERE media_item_id = ? AND recipient_access_generation_id = ?`,
		fixture.media[0], fixture.access["alex"]).Exec(context.Background())
	require.NoError(t, err)
	fixture.queueComment(t, commentID)
	var batchID int64
	require.NoError(t, fixture.db.NewRaw(`SELECT id FROM notification_batches`).Scan(context.Background(), &batchID))

	_, err = fixture.db.NewRaw(`UPDATE comment_subscriptions SET muted = true
		WHERE media_item_id = ? AND recipient_access_generation_id = ?`,
		fixture.media[0], fixture.access["alex"]).Exec(context.Background())
	require.NoError(t, err)
	require.NoError(t, fixture.service.HandleImmediate(context.Background(), fixture.leasedBatchJob(t, batchID)))
	assert.Empty(t, fixture.sender.sent(), "muted Comment activity is ineligible at send time")
	var status string
	require.NoError(t, fixture.db.NewRaw(`SELECT status FROM notification_batches WHERE id = ?`, batchID).Scan(context.Background(), &status))
	assert.Equal(t, "suppressed", status)
}

func TestImmediateEmailHoldsAuthorizationLocksThroughSMTPAcceptance(t *testing.T) {
	fixture := newImmediateFixture(t)
	blocking := &blockingImmediateSender{entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-blocking.release:
		default:
			close(blocking.release)
		}
	})
	fixture.service.sender = blocking
	fixture.addPublicationActivity(t, fixture.base, fixture.media[0])
	require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))
	var batchID int64
	require.NoError(t, fixture.db.NewRaw(`SELECT id FROM notification_batches`).Scan(context.Background(), &batchID))

	job := fixture.leasedBatchJob(t, batchID)
	delivered := make(chan error, 1)
	go func() {
		delivered <- fixture.service.HandleImmediate(context.Background(), job)
	}()
	select {
	case <-blocking.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("immediate sender was not reached")
	}

	preferenceChanged := make(chan error, 1)
	go func() {
		_, err := fixture.db.NewRaw(`UPDATE notification_preferences
			SET email_preference = 'none' WHERE recipient_access_generation_id = ?`, fixture.access["alex"]).Exec(context.Background())
		preferenceChanged <- err
	}()
	waitForImmediateLockWait(t, fixture.db, `%UPDATE notification_preferences%email_preference = 'none'%`)

	close(blocking.release)
	require.NoError(t, <-delivered)
	require.NoError(t, <-preferenceChanged)
	messages := blocking.acceptedMessages()
	require.Len(t, messages, 1)
	assert.Equal(t, "alex@example.com", messages[0].To)
	var preference string
	require.NoError(t, fixture.db.NewRaw(`SELECT email_preference FROM notification_preferences
		WHERE recipient_access_generation_id = ?`, fixture.access["alex"]).Scan(context.Background(), &preference))
	assert.Equal(t, "none", preference)
}

func TestImmediateEmailReauthorizesAndHandlesTerminalFailures(t *testing.T) {
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

		var problem struct {
			Diagnostic      string
			EmailDeliveryID *int64 `bun:"email_delivery_id"`
			ResolvedAt      *time.Time
		}
		require.NoError(t, fixture.db.NewRaw(`SELECT diagnostic, email_delivery_id, resolved_at
			FROM delivery_problems WHERE notification_batch_id = ?`, batchID).Scan(context.Background(), &problem))
		assert.Equal(t, "recipient_rejected", problem.Diagnostic)
		assert.Nil(t, problem.EmailDeliveryID)
		assert.Nil(t, problem.ResolvedAt, "the Curator work queue sees an unresolved optional-email delivery problem")
	})
}

func TestImmediateEmailWorkerPersistsRetryAcrossRestartAndSendsOnce(t *testing.T) {
	fixture := newImmediateFixture(t)
	fixture.service.cfg.RetryBase = time.Second
	fixture.service.cfg.RetryMax = time.Second
	fixture.sender.results = []error{errors.New("private provider failure"), nil}
	fixture.addPublicationActivity(t, fixture.base, fixture.media[0])
	require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))

	first, err := worker.New(fixture.db, workerConfig(), "immediate-retry-first", map[string]worker.Handler{
		ImmediateJobKind: fixture.service.HandleImmediate,
	}, worker.WithDispatcher(outbox.New(fixture.db)))
	require.NoError(t, err)
	first.Start(context.Background())
	t.Cleanup(func() { stopWorker(first) })

	var jobID int64
	var retrySeconds float64
	require.Eventually(t, func() bool {
		var retryable bool
		err := fixture.db.NewRaw(`SELECT id,
			status = 'pending' AND attempts = 1 AND last_safe_error = 'smtp_unavailable'
			AND lease_owner IS NULL AND lease_expires_at IS NULL,
			EXTRACT(EPOCH FROM (available_at - updated_at))
			FROM jobs WHERE kind = ?`, ImmediateJobKind).Scan(context.Background(), &jobID, &retryable, &retrySeconds)
		return err == nil && retryable
	}, asynchronousCompletionTimeout, 5*time.Millisecond)
	assert.GreaterOrEqual(t, retrySeconds, 0.8)
	assert.LessOrEqual(t, retrySeconds, 1.0)
	assert.Len(t, fixture.sender.sent(), 1)
	assert.Zero(t, fixture.sender.acceptedCount())
	stopWorker(first)

	second, err := worker.New(fixture.db, workerConfig(), "immediate-retry-second", map[string]worker.Handler{
		ImmediateJobKind: fixture.service.HandleImmediate,
	})
	require.NoError(t, err)
	second.Start(context.Background())
	t.Cleanup(func() { stopWorker(second) })

	require.Eventually(t, func() bool {
		var completed bool
		err := fixture.db.NewRaw(`SELECT job.status = 'completed' AND job.attempts = 1
			AND job.lease_owner IS NULL AND job.lease_expires_at IS NULL
			AND batch.status = 'sent' AND batch.attempts = 2
			FROM jobs AS job
			JOIN notification_batches AS batch ON batch.id = (job.payload->>'batch_id')::bigint
			WHERE job.id = ?`, jobID).Scan(context.Background(), &completed)
		return err == nil && completed
	}, asynchronousCompletionTimeout, 5*time.Millisecond)
	assert.Len(t, fixture.sender.sent(), 2)
	assert.Equal(t, 1, fixture.sender.acceptedCount(), "only the eventual successful SMTP acceptance is recorded")
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

func TestImmediateEmailSuppressedBatchCannotReplayAfterEligibilityIsRestored(t *testing.T) {
	fixture := newImmediateFixture(t)
	fixture.addPublicationActivity(t, fixture.base, fixture.media[0])
	require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))
	_, err := fixture.db.NewRaw(`UPDATE notification_preferences SET email_preference = 'none'
		WHERE recipient_access_generation_id = ?`, fixture.access["alex"]).Exec(context.Background())
	require.NoError(t, err)

	dispatched, err := outbox.New(fixture.db).Dispatch(context.Background(), "immediate-suppression-dispatch", time.Minute)
	require.NoError(t, err)
	require.True(t, dispatched)
	var job worker.Job
	require.NoError(t, fixture.db.NewRaw(`SELECT id, kind, payload, attempts FROM jobs WHERE kind = ?`, ImmediateJobKind).
		Scan(context.Background(), &job.ID, &job.Kind, &job.Payload, &job.Attempts))
	leaseJob(t, fixture.db, &job, "interrupted-suppression-worker")
	var batchID int64
	require.NoError(t, fixture.db.NewRaw(`SELECT id FROM notification_batches`).Scan(context.Background(), &batchID))
	require.NoError(t, fixture.service.HandleImmediate(context.Background(), job))
	var batchStatus string
	require.NoError(t, fixture.db.NewRaw(`SELECT status FROM notification_batches WHERE id = ?`, batchID).
		Scan(context.Background(), &batchStatus))
	assert.Equal(t, "suppressed", batchStatus)
	assert.Empty(t, fixture.sender.sent())

	_, err = fixture.db.NewRaw(`UPDATE notification_preferences SET email_preference = 'immediate'
		WHERE recipient_access_generation_id = ?`, fixture.access["alex"]).Exec(context.Background())
	require.NoError(t, err)
	require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))
	var batches, items, jobs, outboxEvents int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM notification_batches`).Scan(context.Background(), &batches))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM notification_batch_items`).Scan(context.Background(), &items))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM jobs WHERE kind = ?`, ImmediateJobKind).Scan(context.Background(), &jobs))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM outbox_events WHERE kind = ?`, ImmediateJobKind).Scan(context.Background(), &outboxEvents))
	assert.Equal(t, 1, batches)
	assert.Equal(t, 1, items)
	assert.Equal(t, 1, jobs)
	assert.Equal(t, 1, outboxEvents, "restoring eligibility does not enqueue the historical activity again")

	_, err = fixture.db.NewRaw(`UPDATE jobs SET lease_expires_at = now() - interval '1 second' WHERE id = ?`, job.ID).
		Exec(context.Background())
	require.NoError(t, err)
	restarted, err := worker.New(fixture.db, workerConfig(), "immediate-suppression-replay", map[string]worker.Handler{
		ImmediateJobKind: fixture.service.HandleImmediate,
	})
	require.NoError(t, err)
	restarted.Start(context.Background())
	t.Cleanup(func() { stopWorker(restarted) })
	require.Eventually(t, func() bool {
		var completed bool
		err := fixture.db.NewRaw(`SELECT job.status = 'completed' AND batch.status = 'suppressed'
			FROM jobs AS job JOIN notification_batches AS batch ON batch.id = ?
			WHERE job.id = ?`, batchID, job.ID).Scan(context.Background(), &completed)
		return err == nil && completed
	}, asynchronousCompletionTimeout, 5*time.Millisecond)
	assert.Empty(t, fixture.sender.sent(), "a replayed suppressed batch cannot send after eligibility is restored")
}

func waitForImmediateLockWait(t *testing.T, db *bun.DB, pattern string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(5 * time.Millisecond)
	defer poll.Stop()
	lastWaiting := 0
	for {
		require.NoError(t, db.NewRaw(`SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'
			  AND cardinality(pg_blocking_pids(pid)) > 0 AND query LIKE ?`, pattern).Scan(context.Background(), &lastWaiting))
		if lastWaiting > 0 {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("authorization mutation did not wait for SMTP transaction lock; last waiting count: %d", lastWaiting)
		case <-poll.C:
		}
	}
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
