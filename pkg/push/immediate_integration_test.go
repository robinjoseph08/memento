//go:build integration

package push

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/binder"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/emaildelivery"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/notificationactivity"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/smtp"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type integrationResolver struct{}

func (integrationResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
}

type integrationEmailSender struct {
	mu       sync.Mutex
	messages []smtp.Message
}

func (s *integrationEmailSender) Send(_ context.Context, message smtp.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, message)
	return nil
}

func (s *integrationEmailSender) sent() []smtp.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]smtp.Message(nil), s.messages...)
}

type integrationPushSender struct {
	mu       sync.Mutex
	statuses map[string]int
	payloads map[string][]byte
	entered  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (s *integrationPushSender) Send(ctx context.Context, subscription BrowserSubscription, payload []byte) (DeliveryResult, error) {
	if s.entered != nil {
		s.once.Do(func() { close(s.entered) })
		select {
		case <-s.release:
		case <-ctx.Done():
			return DeliveryResult{}, ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.payloads == nil {
		s.payloads = make(map[string][]byte)
	}
	s.payloads[subscription.Endpoint] = append([]byte(nil), payload...)
	return DeliveryResult{StatusCode: s.statuses[subscription.Endpoint]}, nil
}

type pushFixture struct {
	db          *bun.DB
	email       *emaildelivery.Service
	emailSender *integrationEmailSender
	push        *Service
	sender      *integrationPushSender
	curator     uuid.UUID
	alex        uuid.UUID
	blair       uuid.UUID
	access      uuid.UUID
	blairAccess uuid.UUID
	event       uuid.UUID
	publication uuid.UUID
	moment      uuid.UUID
	media       uuid.UUID
	asset       uuid.UUID
	comment     uuid.UUID
	base        time.Time
}

func newPushFixture(t *testing.T) pushFixture {
	t.Helper()
	ctx := context.Background()
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(ctx, db))
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	f := pushFixture{db: db, sender: &integrationPushSender{statuses: map[string]int{}}, curator: uuid.New(), alex: uuid.New(), blair: uuid.New(), access: uuid.New(), blairAccess: uuid.New(), event: uuid.New(), publication: uuid.New(), moment: uuid.New(), media: uuid.New(), asset: uuid.New(), comment: uuid.New(), base: base}
	emailCfg := config.SMTPConfig{Enabled: true, RetryBase: time.Second, RetryMax: time.Minute, RetryWindow: time.Hour}
	f.emailSender = new(integrationEmailSender)
	f.email = emaildelivery.New(db, emailCfg, f.emailSender, "test-only-security-secret-32-bytes")
	f.email.SetPublicURL("https://memento.example")
	pushCfg := config.PushConfig{Enabled: true, PublicKey: "test", PrivateKey: "test", Subject: "mailto:test@example.com", Timeout: time.Second, RetryBase: time.Second, RetryMax: time.Minute, RetryWindow: time.Hour, TTL: 15 * time.Minute}
	policy := NewEndpointPolicy(integrationResolver{})
	f.push = New(db, pushCfg, "test-only-security-secret-32-bytes", f.sender, policy)

	_, err := db.NewRaw(`
		INSERT INTO people (id, display_name, sort_name) VALUES
		 (?, 'Curator', 'curator'), (?, 'Alex', 'alex'), (?, 'Blair', 'blair');
		INSERT INTO person_roles (person_id, role) VALUES (?, 'recipient'), (?, 'curator'), (?, 'recipient'), (?, 'recipient');
		INSERT INTO recipient_access_generations (id, person_id, generation, state, is_current, onboarding_completed_at)
		 VALUES (?, ?, 1, 'completed', true, ?), (?, ?, 1, 'completed', true, ?);
		INSERT INTO onboarding_choices (recipient_access_generation_id, privacy_acknowledged, engagement_acknowledged,
		 interest_list_acknowledged, email_previews_acknowledged, push_guidance_acknowledged, informed_choices_version,
		 email_preference, completed_at) VALUES
		 (?, true, true, true, true, true, 2, 'immediate', ?),
		 (?, true, true, true, true, true, 2, 'immediate', ?);
		INSERT INTO recipient_emails (id, recipient_access_generation_id, email, normalized_email, is_current) VALUES
		 (gen_random_uuid(), ?, 'alex@example.com', 'alex@example.com', true),
		 (gen_random_uuid(), ?, 'blair@example.com', 'blair@example.com', true);
		INSERT INTO notification_preferences (recipient_access_generation_id, email_preference, updated_at) VALUES
		 (?, 'immediate', ?), (?, 'immediate', ?);
		UPDATE system_settings SET setup_complete = true WHERE id = 1;
		INSERT INTO events (id, lifecycle, title, description, grouping_timezone, version, created_at, updated_at)
		 VALUES (?, 'published', 'Family picnic', '', 'UTC', 1, ?, ?);
		INSERT INTO publications (id, event_id, revision, editable_version, published_by_person_id, notify_recipients, committed_at)
		 VALUES (?, ?, 1, 1, ?, true, ?);
		UPDATE events SET current_publication_id = ? WHERE id = ?;
		INSERT INTO current_published_events (event_id, publication_id, title, description, grouping_timezone, committed_at)
		 VALUES (?, ?, 'Family picnic', '', 'UTC', ?);
		INSERT INTO published_event_revisions (publication_id, event_id, title, description, grouping_timezone, created_at)
		 VALUES (?, ?, 'Family picnic', '', 'UTC', ?);
		INSERT INTO audience_snapshots (id, target_kind, target_id, approved_by_person_id, approved_at, label)
		 VALUES (gen_random_uuid(), 'moment', ?, ?, ?, 'Shared')`,
		f.curator, f.alex, f.blair, f.curator, f.curator, f.alex, f.blair,
		f.access, f.alex, base, f.blairAccess, f.blair, base,
		f.access, base, f.blairAccess, base, f.access, f.blairAccess,
		f.access, base, f.blairAccess, base,
		f.event, base, base, f.publication, f.event, f.curator, base, f.publication, f.event,
		f.event, f.publication, base, f.publication, f.event, base, f.moment, f.curator, base).Exec(ctx)
	require.NoError(t, err)
	var snapshot uuid.UUID
	require.NoError(t, db.NewRaw(`SELECT id FROM audience_snapshots WHERE target_id = ?`, f.moment).Scan(ctx, &snapshot))
	_, err = db.NewRaw(`
		INSERT INTO draft_moments (id, event_id, position, proposed_day, grouping_timezone, source_days, title, attendance_complete, audience_complete)
		 VALUES (?, ?, 0, '2026-07-28', 'UTC', ARRAY['2026-07-28'::date], '', true, true);
		INSERT INTO current_audience_snapshots (target_kind, target_id, snapshot_id) VALUES ('moment', ?, ?);
		INSERT INTO audience_snapshot_entries (snapshot_id, recipient_person_id, recipient_access_generation_id) VALUES (?, ?, ?);
		INSERT INTO published_moments (id, publication_id, draft_moment_id, audience_snapshot_id, position, title, proposed_day)
		 VALUES (?, ?, ?, ?, 0, '', '2026-07-28');
		INSERT INTO media_items (id, immich_asset_id, media_type, width, height, local_date_time, availability, first_seen_at, last_seen_at)
		 VALUES (?, ?, 'image', 1200, 800, '2026-07-28T12:00:00Z', 'current', ?, ?);
		INSERT INTO media_backings (id, media_item_id, immich_asset_id, capture_at, filename, linked_at)
		 VALUES (gen_random_uuid(), ?, ?, '2026-07-28T12:00:00Z', 'photo.jpg', ?);
		INSERT INTO draft_media_placements (event_id, media_item_id, draft_moment_id, position, created_at) VALUES (?, ?, ?, 0, ?);
		INSERT INTO published_media_placements (published_moment_id, media_item_id, position, media_type, width, height, local_date_time)
		 VALUES (?, ?, 0, 'image', 1200, 800, '2026-07-28T12:00:00Z');
		INSERT INTO current_published_placements (event_id, publication_id, published_moment_id, media_item_id, position)
		 VALUES (?, ?, ?, ?, 0);
		INSERT INTO current_audience_entitlements (event_id, publication_id, recipient_person_id, recipient_access_generation_id, media_item_id)
		 VALUES (?, ?, ?, ?, ?);
		INSERT INTO publication_activity_items (publication_id, recipient_access_generation_id, created_at) VALUES (?, ?, ?);
		INSERT INTO publication_notification_media (publication_id, recipient_access_generation_id, media_item_id) VALUES (?, ?, ?);
		INSERT INTO comments (id, media_item_id, author_person_id, author_access_generation_id, idempotency_key, body, created_at, updated_at)
		 VALUES (?, ?, ?, ?, gen_random_uuid(), 'Hello', ?, ?);
		INSERT INTO comment_subscriptions (media_item_id, recipient_access_generation_id, muted, created_at, updated_at)
		 VALUES (?, ?, false, ?, ?)`,
		f.moment, f.event, f.moment, snapshot, snapshot, f.alex, f.access,
		f.moment, f.publication, f.moment, snapshot,
		f.media, f.asset, base, base, f.media, f.asset, base,
		f.event, f.media, f.moment, base, f.moment, f.media,
		f.event, f.publication, f.moment, f.media,
		f.event, f.publication, f.alex, f.access, f.media,
		f.publication, f.access, base, f.publication, f.access, f.media,
		f.comment, f.media, f.blair, f.blairAccess, base.Add(time.Minute), base.Add(time.Minute),
		f.media, f.access, base, base).Exec(ctx)
	require.NoError(t, err)
	return f
}

func (f pushFixture) addLoosePublicationActivity(t *testing.T) (uuid.UUID, uuid.UUID) {
	t.Helper()
	looseID, publicationID, snapshotID := uuid.New(), uuid.New(), uuid.New()
	_, err := f.db.NewRaw(`
		INSERT INTO loose_items (id, media_item_id, lifecycle, title, description, grouping_timezone,
			version, audience_complete, created_at, updated_at)
		VALUES (?, ?, 'published', 'Loose portrait', '', 'UTC', 1, true, ?, ?);
		INSERT INTO audience_snapshots (id, target_kind, target_id, approved_by_person_id, approved_at, label)
		VALUES (?, 'loose_item', ?, ?, ?, 'Shared');
		INSERT INTO audience_snapshot_entries (snapshot_id, recipient_person_id, recipient_access_generation_id)
		VALUES (?, ?, ?);
		INSERT INTO current_audience_snapshots (target_kind, target_id, snapshot_id)
		VALUES ('loose_item', ?, ?);
		INSERT INTO publications (id, loose_item_id, revision, editable_version, published_by_person_id,
			notify_recipients, committed_at)
		VALUES (?, ?, 1, 1, ?, true, ?);
		UPDATE loose_items SET current_publication_id = ? WHERE id = ?;
		INSERT INTO published_loose_item_revisions (publication_id, loose_item_id, media_item_id,
			audience_snapshot_id, title, description, grouping_timezone, place_labels, media_type,
			width, height, local_date_time, created_at)
		VALUES (?, ?, ?, ?, 'Loose portrait', '', 'UTC', '{}', 'image', 1200, 800,
			'2026-07-28T12:00:00Z', ?);
		INSERT INTO published_loose_audience_entries
			(publication_id, recipient_person_id, recipient_access_generation_id)
		VALUES (?, ?, ?);
		INSERT INTO current_published_loose_items (loose_item_id, publication_id, media_item_id,
			title, description, grouping_timezone, place_labels, media_type, width, height,
			local_date_time, committed_at)
		VALUES (?, ?, ?, 'Loose portrait', '', 'UTC', '{}', 'image', 1200, 800,
			'2026-07-28T12:00:00Z', ?);
		INSERT INTO current_loose_item_entitlements (loose_item_id, publication_id,
			recipient_person_id, recipient_access_generation_id, media_item_id)
		VALUES (?, ?, ?, ?, ?);
		INSERT INTO publication_activity_items (publication_id, recipient_access_generation_id, created_at)
		VALUES (?, ?, ?);
		INSERT INTO publication_notification_media (publication_id, recipient_access_generation_id, media_item_id)
		VALUES (?, ?, ?)
	`, looseID, f.media, f.base, f.base,
		snapshotID, looseID, f.curator, f.base, snapshotID, f.alex, f.access, looseID, snapshotID,
		publicationID, looseID, f.curator, f.base, publicationID, looseID,
		publicationID, looseID, f.media, snapshotID, f.base,
		publicationID, f.alex, f.access,
		looseID, publicationID, f.media, f.base,
		looseID, publicationID, f.alex, f.access, f.media,
		publicationID, f.access, f.base,
		publicationID, f.access, f.media).Exec(context.Background())
	require.NoError(t, err)
	return looseID, publicationID
}

func browserSubscription(t *testing.T, endpoint string) BrowserSubscription {
	t.Helper()
	userKey, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	auth := make([]byte, 16)
	_, err = rand.Read(auth)
	require.NoError(t, err)
	return BrowserSubscription{Endpoint: endpoint, Keys: BrowserSubscriptionKeys{
		P256DH: base64.RawURLEncoding.EncodeToString(userKey.PublicKey().Bytes()),
		Auth:   base64.RawURLEncoding.EncodeToString(auth),
	}}
}

func (f pushFixture) trustedActor(t *testing.T) setup.SessionActor {
	t.Helper()
	actor := setup.SessionActor{PersonID: f.alex, AccessID: f.access, SessionID: uuid.New()}
	credential := make([]byte, 32)
	_, err := rand.Read(credential)
	require.NoError(t, err)
	_, err = f.db.NewRaw(`INSERT INTO sessions
		(id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at)
		SELECT ?, ?, ?, ?, security_epoch, 'trusted', ? FROM system_settings WHERE id = 1`,
		actor.SessionID, credential, actor.PersonID, actor.AccessID, f.base.Add(24*time.Hour)).Exec(context.Background())
	require.NoError(t, err)
	return actor
}

func (f pushFixture) enroll(t *testing.T, endpoint string) setup.SessionActor {
	t.Helper()
	actor := f.trustedActor(t)
	_, err := f.push.Enroll(context.Background(), actor, browserSubscription(t, endpoint))
	require.NoError(t, err)
	return actor
}

func (f pushFixture) leasedJob(t *testing.T, batchID int64) worker.Job {
	t.Helper()
	payload, err := json.Marshal(notificationactivity.JobPayload{BatchID: batchID})
	require.NoError(t, err)
	var id int64
	require.NoError(t, f.db.NewRaw(`INSERT INTO jobs (kind, payload, status, lease_owner, lease_expires_at)
		VALUES (?, ?::jsonb, 'running', 'push-test', clock_timestamp() + interval '1 hour') RETURNING id`, JobKind, string(payload)).Scan(context.Background(), &id))
	return worker.Job{ID: id, Kind: JobKind, Payload: payload, LeaseOwner: "push-test"}
}

func (f pushFixture) leasedEmailJob(t *testing.T, batchID int64) worker.Job {
	t.Helper()
	payload, err := json.Marshal(notificationactivity.JobPayload{BatchID: batchID})
	require.NoError(t, err)
	var id int64
	require.NoError(t, f.db.NewRaw(`INSERT INTO jobs (kind, payload, status, lease_owner, lease_expires_at)
		VALUES (?, ?::jsonb, 'running', 'email-test', clock_timestamp() + interval '1 hour') RETURNING id`,
		emaildelivery.ImmediateJobKind, string(payload)).Scan(context.Background(), &id))
	return worker.Job{ID: id, Kind: emaildelivery.ImmediateJobKind, Payload: payload, LeaseOwner: "email-test"}
}

func TestPushRoutesEnforceSafeGETCSRFContentTypeAndPublicSessionPolicy(t *testing.T) {
	f := newPushFixture(t)
	security := config.SecurityConfig{Secret: "test-only-security-secret-32-bytes"}
	auth := setup.New(f.db, f.email, security)
	issueSession := func(t *testing.T, sessionType string) setup.BrowserSession {
		t.Helper()
		var browser setup.BrowserSession
		require.NoError(t, f.db.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
			var err error
			browser, _, err = auth.NewBrowserSessionIn(ctx, tx, f.alex, f.access, sessionType, time.Now().UTC())
			return err
		}))
		return browser
	}
	requestBinder, err := binder.New()
	require.NoError(t, err)
	e := echo.New()
	e.Binder = requestBinder
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	RegisterRoutes(e, NewHandler(f.push, auth))
	material, err := json.Marshal(browserSubscription(t, "https://push.example/route"))
	require.NoError(t, err)
	exercise := func(method, path string, body []byte, contentType, credential, csrf string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, bytes.NewReader(body))
		if contentType != "" {
			request.Header.Set(echo.HeaderContentType, contentType)
		}
		if csrf != "" {
			request.Header.Set(setup.CSRFHeader, csrf)
		}
		request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: credential})
		response := httptest.NewRecorder()
		e.ServeHTTP(response, request)
		return response
	}

	trusted := issueSession(t, "trusted")
	response := exercise(http.MethodGet, "/api/push", nil, "", trusted.Credential, "")
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "no-store", response.Header().Get(echo.HeaderCacheControl))
	var subscriptions int
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM push_subscriptions`).Scan(context.Background(), &subscriptions))
	assert.Zero(t, subscriptions, "safe GET must not enroll a device")

	response = exercise(http.MethodPost, "/api/push", material, echo.MIMEApplicationJSON, trusted.Credential, "")
	assert.Equal(t, http.StatusForbidden, response.Code)
	response = exercise(http.MethodPost, "/api/push", material, echo.MIMETextPlain, trusted.Credential, trusted.CSRFToken)
	assert.Equal(t, http.StatusUnsupportedMediaType, response.Code)

	public := issueSession(t, "public")
	response = exercise(http.MethodPost, "/api/push", material, echo.MIMEApplicationJSON, public.Credential, public.CSRFToken)
	assert.Equal(t, http.StatusForbidden, response.Code)
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM push_subscriptions`).Scan(context.Background(), &subscriptions))
	assert.Zero(t, subscriptions)
}

func TestConcurrentEndpointEnrollmentReturnsOneConflict(t *testing.T) {
	f := newPushFixture(t)
	first := f.trustedActor(t)
	second := f.trustedActor(t)
	material := browserSubscription(t, "https://push.example/concurrent")
	results := make(chan error, 2)
	go func() {
		_, err := f.push.Enroll(context.Background(), first, material)
		results <- err
	}()
	go func() {
		_, err := f.push.Enroll(context.Background(), second, material)
		results <- err
	}()
	errorsSeen := []error{<-results, <-results}
	var successes, conflicts int
	for _, err := range errorsSeen {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrEndpointConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent enrollment result: %v", err)
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, conflicts)
	var subscriptions int
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM push_subscriptions WHERE disabled_at IS NULL`).Scan(context.Background(), &subscriptions))
	assert.Equal(t, 1, subscriptions)
}

func TestPublicSessionCannotEnrollOrReconcilePush(t *testing.T) {
	f := newPushFixture(t)
	actor := setup.SessionActor{PersonID: f.alex, AccessID: f.access, SessionID: uuid.New()}
	credential := make([]byte, 32)
	_, err := rand.Read(credential)
	require.NoError(t, err)
	_, err = f.db.NewRaw(`INSERT INTO sessions
		(id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, absolute_expires_at)
		SELECT ?, ?, ?, ?, security_epoch, 'public', clock_timestamp() + interval '1 hour'
		FROM system_settings WHERE id = 1`, actor.SessionID, credential, actor.PersonID, actor.AccessID).Exec(context.Background())
	require.NoError(t, err)
	material := browserSubscription(t, "https://push.example/public")
	_, err = f.push.Enroll(context.Background(), actor, material)
	require.ErrorIs(t, err, ErrTrustedRequired)
	reconciled, err := f.push.Reconcile(context.Background(), actor, ReconcileRequest{Subscription: &material})
	require.NoError(t, err)
	assert.True(t, reconciled.RemoveLocal)
	assert.False(t, reconciled.Enrolled)
	var count int
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM push_subscriptions`).Scan(context.Background(), &count))
	assert.Zero(t, count)
}

func TestPushHoldsSessionPreferenceLockThroughProviderAcceptance(t *testing.T) {
	f := newPushFixture(t)
	f.sender.entered = make(chan struct{})
	f.sender.release = make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-f.sender.release:
		default:
			close(f.sender.release)
		}
	})
	actor := f.enroll(t, "https://push.example/locking")
	f.sender.statuses["https://push.example/locking"] = 201
	require.NoError(t, f.push.QueuePublication(context.Background(), f.event, f.publication))
	var batchID int64
	require.NoError(t, f.db.NewRaw(`SELECT id FROM notification_batches WHERE channel = 'push'`).Scan(context.Background(), &batchID))
	job := f.leasedJob(t, batchID)
	delivered := make(chan error, 1)
	go func() { delivered <- f.push.Handle(context.Background(), job) }()
	select {
	case <-f.sender.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("push sender was not reached")
	}

	revoked := make(chan error, 1)
	go func() {
		revoked <- f.db.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
			if _, err := tx.NewRaw(`UPDATE sessions SET revoked_at = clock_timestamp() WHERE id = ?`, actor.SessionID).Exec(ctx); err != nil {
				return err
			}
			_, err := tx.NewRaw(`UPDATE push_subscriptions SET disabled_at = clock_timestamp() WHERE session_id = ?`, actor.SessionID).Exec(ctx)
			return err
		})
	}()
	waitForBlockedPushMutation(t, f.db, `%UPDATE sessions SET revoked_at%`)
	select {
	case err := <-revoked:
		t.Fatalf("Session revocation finished before provider acceptance: %v", err)
	default:
	}
	close(f.sender.release)
	require.NoError(t, <-delivered)
	require.NoError(t, <-revoked)
	var disabled bool
	require.NoError(t, f.db.NewRaw(`SELECT disabled_at IS NOT NULL FROM push_subscriptions WHERE session_id = ?`, actor.SessionID).Scan(context.Background(), &disabled))
	assert.True(t, disabled)
}

func TestPushHoldsLooseAuthorizationThroughProviderAcceptance(t *testing.T) {
	f := newPushFixture(t)
	f.sender.entered = make(chan struct{})
	f.sender.release = make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-f.sender.release:
		default:
			close(f.sender.release)
		}
	})
	f.enroll(t, "https://push.example/loose-locking")
	f.sender.statuses["https://push.example/loose-locking"] = 201
	looseID, publicationID := f.addLoosePublicationActivity(t)
	require.NoError(t, f.push.QueuePublication(context.Background(), uuid.Nil, publicationID))
	var batchID int64
	require.NoError(t, f.db.NewRaw(`SELECT id FROM notification_batches WHERE channel = 'push'`).Scan(context.Background(), &batchID))

	delivered := make(chan error, 1)
	go func() { delivered <- f.push.Handle(context.Background(), f.leasedJob(t, batchID)) }()
	select {
	case <-f.sender.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("push sender was not reached")
	}

	revoked := make(chan error, 1)
	go func() {
		revoked <- f.db.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
			if _, err := tx.NewRaw(`UPDATE loose_items SET title = title WHERE id = ?`, looseID).Exec(ctx); err != nil {
				return err
			}
			_, err := tx.NewRaw(`DELETE FROM current_loose_item_entitlements WHERE loose_item_id = ?`, looseID).Exec(ctx)
			return err
		})
	}()
	waitForBlockedPushMutation(t, f.db, `%UPDATE loose_items SET title = title%`)
	select {
	case err := <-revoked:
		t.Fatalf("Loose revocation finished before provider acceptance: %v", err)
	default:
	}

	close(f.sender.release)
	require.NoError(t, <-delivered)
	require.NoError(t, <-revoked)
	assert.Contains(t, f.sender.payloads, "https://push.example/loose-locking")
	var entitlements int
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM current_loose_item_entitlements WHERE loose_item_id = ?`, looseID).Scan(context.Background(), &entitlements))
	assert.Zero(t, entitlements)
}

func waitForBlockedPushMutation(t *testing.T, db *bun.DB, pattern string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastBlocked bool
	for time.Now().Before(deadline) {
		err := db.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid()
			 AND cardinality(pg_blocking_pids(pid)) > 0 AND query LIKE ?
		)`, pattern).Scan(context.Background(), &lastBlocked)
		require.NoError(t, err)
		if lastBlocked {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("push mutation did not block before deadline; last blocked state: %t", lastBlocked)
}

func TestPushReauthorizesWithdrawalImmediatelyBeforeSend(t *testing.T) {
	f := newPushFixture(t)
	f.enroll(t, "https://push.example/withdrawn")
	f.sender.statuses["https://push.example/withdrawn"] = 201
	require.NoError(t, f.push.QueuePublication(context.Background(), f.event, f.publication))
	var batchID int64
	require.NoError(t, f.db.NewRaw(`SELECT id FROM notification_batches WHERE channel = 'push'`).Scan(context.Background(), &batchID))
	_, err := f.db.NewRaw(`INSERT INTO content_withdrawals
		(id, target_kind, target_id, withdrawn_by_person_id, withdrawn_at, reason, content_revision)
		VALUES (?, 'media', ?, ?, clock_timestamp(), 'Recipient requested removal', 1)`,
		uuid.New(), f.media, f.curator).Exec(context.Background())
	require.NoError(t, err)

	require.NoError(t, f.push.Handle(context.Background(), f.leasedJob(t, batchID)))
	assert.Empty(t, f.sender.payloads)
	var status string
	require.NoError(t, f.db.NewRaw(`SELECT status FROM notification_batches WHERE id = ?`, batchID).Scan(context.Background(), &status))
	assert.Equal(t, "suppressed", status)
}

func TestPushRequiresCurrentSecurityEpochAtSend(t *testing.T) {
	f := newPushFixture(t)
	f.enroll(t, "https://push.example/restored")
	f.sender.statuses["https://push.example/restored"] = 201
	require.NoError(t, f.push.QueuePublication(context.Background(), f.event, f.publication))
	var batchID int64
	require.NoError(t, f.db.NewRaw(`SELECT id FROM notification_batches WHERE channel = 'push'`).Scan(context.Background(), &batchID))
	epoch := make([]byte, 32)
	_, err := rand.Read(epoch)
	require.NoError(t, err)
	_, err = f.db.NewRaw(`UPDATE system_settings SET security_epoch = ? WHERE id = 1`, epoch).Exec(context.Background())
	require.NoError(t, err)

	require.NoError(t, f.push.Handle(context.Background(), f.leasedJob(t, batchID)))
	assert.Empty(t, f.sender.payloads)
	var batchStatus string
	var disabled bool
	require.NoError(t, f.db.NewRaw(`SELECT batch.status, subscription.disabled_at IS NOT NULL
		FROM notification_batches AS batch JOIN push_subscriptions AS subscription ON subscription.id = batch.push_subscription_id
		WHERE batch.id = ?`, batchID).Scan(context.Background(), &batchStatus, &disabled))
	assert.Equal(t, "suppressed", batchStatus)
	assert.True(t, disabled)
}

func TestPushMatchesEmailSurvivorsAndTerminalOutcomeIsDeviceOnly(t *testing.T) {
	f := newPushFixture(t)
	f.enroll(t, "https://push.example/expired")
	f.enroll(t, "https://push.example/current")
	f.sender.statuses["https://push.example/expired"] = 410
	f.sender.statuses["https://push.example/current"] = 201
	var ciphertext []byte
	require.NoError(t, f.db.NewRaw(`SELECT material_ciphertext FROM push_subscriptions ORDER BY id LIMIT 1`).Scan(context.Background(), &ciphertext))
	assert.NotContains(t, string(ciphertext), "https://push.example")

	survivingComment := uuid.New()
	_, err := f.db.NewRaw(`INSERT INTO comments
		(id, media_item_id, author_person_id, author_access_generation_id, idempotency_key, body, created_at, updated_at)
		VALUES (?, ?, ?, ?, gen_random_uuid(), 'Still current', ?, ?)`, survivingComment, f.media,
		f.blair, f.blairAccess, f.base.Add(2*time.Minute), f.base.Add(2*time.Minute)).Exec(context.Background())
	require.NoError(t, err)
	require.NoError(t, f.email.QueuePublication(context.Background(), f.event, f.publication))
	require.NoError(t, f.push.QueuePublication(context.Background(), f.event, f.publication))
	require.NoError(t, f.db.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
		for _, commentID := range []uuid.UUID{f.comment, survivingComment} {
			if err := f.email.QueueComment(ctx, tx, f.access, commentID); err != nil {
				return err
			}
			if err := f.push.QueueComment(ctx, tx, f.access, commentID); err != nil {
				return err
			}
		}
		return nil
	}))
	_, err = f.db.NewRaw(`UPDATE comments SET state = 'deleted', deleted_at = clock_timestamp(), updated_at = clock_timestamp() WHERE id = ?`, f.comment).Exec(context.Background())
	require.NoError(t, err)

	var emailBatch int64
	require.NoError(t, f.db.NewRaw(`SELECT id FROM notification_batches WHERE channel = 'email' AND cadence = 'immediate'`).Scan(context.Background(), &emailBatch))
	emailSet, err := notificationactivity.AuthorizeBatch(context.Background(), f.db, emailBatch, false)
	require.NoError(t, err)
	require.Len(t, emailSet.Activities, 2)
	assert.Equal(t, survivingComment, emailSet.Activities[1].SourceID)
	assert.Equal(t, "Blair", emailSet.Activities[1].Author)
	require.NoError(t, f.email.HandleImmediate(context.Background(), f.leasedEmailJob(t, emailBatch)))
	emailMessages := f.emailSender.sent()
	require.Len(t, emailMessages, 1)
	assert.Contains(t, emailMessages[0].Body, "Family picnic: 1 new item")
	assert.Contains(t, emailMessages[0].Body, "Blair commented on an item you can access.")
	var pushBatches []int64
	require.NoError(t, f.db.NewRaw(`SELECT id FROM notification_batches WHERE channel = 'push' ORDER BY id`).Scan(context.Background(), &pushBatches))
	require.Len(t, pushBatches, 2)
	for _, batchID := range pushBatches {
		pushSet, err := notificationactivity.AuthorizeBatch(context.Background(), f.db, batchID, false)
		require.NoError(t, err)
		assert.Equal(t, emailSet.Activities, pushSet.Activities)
		require.NoError(t, f.push.Handle(context.Background(), f.leasedJob(t, batchID)))
	}

	for endpoint, payload := range f.sender.payloads {
		var decoded pushPayload
		require.NoError(t, json.Unmarshal(payload, &decoded))
		assert.Equal(t, 1, decoded.Version)
		require.Len(t, decoded.Activities, 2)
		assert.Equal(t, notificationactivity.Publication, decoded.Activities[0].Kind)
		assert.Equal(t, "Family picnic", decoded.Activities[0].Title)
		assert.Equal(t, 1, decoded.Activities[0].AdditionCount)
		assert.Equal(t, notificationactivity.Comment, decoded.Activities[1].Kind)
		assert.Equal(t, "Blair", decoded.Activities[1].Author)
		assert.NotContains(t, string(payload), f.media.String())
		assert.NotContains(t, string(payload), f.asset.String())
		assert.NotContains(t, string(payload), endpoint)
	}
	var active, disabled int
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FILTER (WHERE disabled_at IS NULL), count(*) FILTER (WHERE disabled_at IS NOT NULL)
		FROM push_subscriptions WHERE person_id = ?`, f.alex).Scan(context.Background(), &active, &disabled))
	assert.Equal(t, 1, active)
	assert.Equal(t, 1, disabled)
	var preference string
	require.NoError(t, f.db.NewRaw(`SELECT email_preference FROM notification_preferences WHERE recipient_access_generation_id = ?`, f.access).Scan(context.Background(), &preference))
	assert.Equal(t, "immediate", preference)
	var emailStatus string
	require.NoError(t, f.db.NewRaw(`SELECT status FROM notification_batches WHERE id = ?`, emailBatch).Scan(context.Background(), &emailStatus))
	assert.Equal(t, "sent", emailStatus)
}
