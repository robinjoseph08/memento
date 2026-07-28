//go:build integration

package sessions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/emaildelivery"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/smtp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type discardSender struct{}

func (discardSender) Send(context.Context, smtp.Message) error { return nil }

type fixture struct {
	db                            *bun.DB
	service                       *Service
	auth                          *setup.Service
	delivery                      *emaildelivery.Service
	personID, accessID, sessionID uuid.UUID
	credential, csrf              string
	now                           time.Time
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(ctx, db))
	personID, accessID, sessionID := uuid.New(), uuid.New(), uuid.New()
	_, err := db.NewRaw(`INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Alex', 'alex'); INSERT INTO person_roles (person_id, role) VALUES (?, 'recipient'); INSERT INTO recipient_access_generations (id, person_id, generation, state, is_current, onboarding_completed_at) VALUES (?, ?, 1, 'completed', true, now()); INSERT INTO recipient_emails (id, recipient_access_generation_id, email, normalized_email) VALUES (?, ?, 'alex@example.com', 'alex@example.com'); UPDATE system_settings SET setup_complete = true WHERE id = 1`, personID, personID, accessID, personID, uuid.New(), accessID).Exec(ctx)
	require.NoError(t, err)
	var epoch []byte
	require.NoError(t, db.NewRaw(`SELECT security_epoch FROM system_settings WHERE id = 1`).Scan(ctx, &epoch))
	raw := bytes.Repeat([]byte{0x11}, 32)
	hash := sha256.Sum256(raw)
	now := time.Date(2100, 1, 2, 3, 4, 5, 0, time.UTC)
	_, err = db.NewRaw(`INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, created_at, last_activity_at, idle_expires_at, browser, platform) VALUES (?, ?, ?, ?, ?, 'trusted', ?, ?, ?, 'Firefox', 'Linux')`, sessionID, hash[:], personID, accessID, epoch, now, now, now.Add(365*24*time.Hour)).Exec(ctx)
	require.NoError(t, err)
	security := config.SecurityConfig{Secret: "sessions-integration-secret-at-least-32-bytes", SignInRateWindow: 15 * time.Minute, SignInEmailLimit: 3, SignInIPLimit: 10}
	delivery := emaildelivery.New(db, config.SMTPConfig{Enabled: true, RetryWindow: time.Hour}, discardSender{}, security.Secret)
	auth := setup.New(db, delivery, security, setup.WithClock(func() time.Time { return now }))
	credential := hex.EncodeToString(raw)
	session, err := auth.Session(ctx, credential)
	require.NoError(t, err)
	service := New(db, delivery, auth, security)
	service.now = func() time.Time { return now }
	return fixture{db: db, service: service, auth: auth, delivery: delivery, personID: personID, accessID: accessID, sessionID: sessionID, credential: credential, csrf: session.CSRFToken, now: now}
}

func TestSignInStartIsNonEnumeratingAndStoresOnlyEligibleChallenge(t *testing.T) {
	f := newFixture(t)
	f.service.random = bytes.NewReader(bytes.Repeat([]byte{0x22}, 256))
	known, err := f.service.RequestSignIn(context.Background(), SignInRequest{Email: "ALEX@example.com"})
	require.NoError(t, err)
	f.service.random = bytes.NewReader(bytes.Repeat([]byte{0x33}, 256))
	unknown, err := f.service.RequestSignIn(context.Background(), SignInRequest{Email: "unknown@example.com"})
	require.NoError(t, err)
	assert.Equal(t, known.Status, unknown.Status)
	assert.Len(t, known.ChallengeID, 64)
	assert.Len(t, unknown.ChallengeID, 64)
	var challenges, sessions int
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM sign_in_challenges`).Scan(context.Background(), &challenges))
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM sessions`).Scan(context.Background(), &sessions))
	assert.Equal(t, 1, challenges)
	assert.Equal(t, 1, sessions, "requesting and sending a code must not create a Session")
	var body string
	require.NoError(t, f.db.NewRaw(`SELECT body FROM email_deliveries WHERE kind = 'sign_in_code'`).Scan(context.Background(), &body))
	assert.NotContains(t, body, "00000000")
	assert.Contains(t, body, "v1:")
}

func insertChallenge(t *testing.T, f fixture, fill byte, code string) string {
	t.Helper()
	raw := bytes.Repeat([]byte{fill}, 32)
	challengeID := hex.EncodeToString(raw)
	expires := f.now.Add(10 * time.Minute)
	require.NoError(t, f.db.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
		var emailID uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM recipient_emails WHERE recipient_access_generation_id = ? AND is_current`, f.accessID).Scan(ctx, &emailID); err != nil {
			return err
		}
		deliveryID, _, err := f.delivery.QueueRequired(ctx, tx, emaildelivery.RequiredMessage{Kind: emaildelivery.KindSignInCode, Recipient: "alex@example.com", Subject: "code", Body: "code", DeliverBefore: &expires})
		if err != nil {
			return err
		}
		_, err = tx.NewRaw(`INSERT INTO sign_in_challenges (id, challenge_hash, code_hash, recipient_access_generation_id, recipient_email_id, email_delivery_id, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, uuid.New(), digest(raw), f.service.codeHash("sign-in", raw, code), f.accessID, emailID, deliveryID, expires, f.now).Exec(ctx)
		return err
	}))
	return challengeID
}

func TestSignInCodeIsEightDigitSingleUseAndCreatesPolicyBoundSessions(t *testing.T) {
	for _, sessionType := range []string{"trusted", "public"} {
		t.Run(sessionType, func(t *testing.T) {
			f := newFixture(t)
			challenge := insertChallenge(t, f, 0x44, "12345678")
			result, err := f.service.VerifySignIn(context.Background(), SignInVerifyRequest{ChallengeID: challenge, Code: "12345678", SessionType: sessionType})
			require.NoError(t, err)
			assert.Len(t, result.session.Credential, 64)
			_, err = f.service.VerifySignIn(context.Background(), SignInVerifyRequest{ChallengeID: challenge, Code: "12345678", SessionType: sessionType})
			assert.ErrorIs(t, err, ErrInvalidCode)
			var idle, absolute *time.Time
			require.NoError(t, f.db.NewRaw(`SELECT idle_expires_at, absolute_expires_at FROM sessions WHERE id <> ?`, f.sessionID).Scan(context.Background(), &idle, &absolute))
			if sessionType == "trusted" {
				require.NotNil(t, idle)
				assert.Equal(t, f.now.Add(365*24*time.Hour), idle.UTC())
				assert.Nil(t, absolute)
			} else {
				assert.Nil(t, idle)
				require.NotNil(t, absolute)
				assert.Equal(t, f.now.Add(12*time.Hour), absolute.UTC())
			}
		})
	}
}

func TestSignInChallengeIsBoundToTheAddressThatReceivedIt(t *testing.T) {
	f := newFixture(t)
	challenge := insertChallenge(t, f, 0x46, "12345678")
	_, err := f.db.NewRaw(`
		UPDATE recipient_emails SET is_current = false, ended_at = ? WHERE recipient_access_generation_id = ? AND is_current;
		INSERT INTO recipient_emails (id, recipient_access_generation_id, email, normalized_email, is_current, created_at)
		VALUES (?, ?, 'alex-new@example.com', 'alex-new@example.com', true, ?)
	`, f.now, f.accessID, uuid.New(), f.accessID, f.now).Exec(context.Background())
	require.NoError(t, err)

	_, err = f.service.VerifySignIn(context.Background(), SignInVerifyRequest{ChallengeID: challenge, Code: "12345678", SessionType: "trusted"})
	assert.ErrorIs(t, err, ErrInvalidCode, "a code sent before identity rotation must not authenticate the replacement address")
}

func TestFiveFailedSignInAttemptsConsumeTheChallengeBudget(t *testing.T) {
	f := newFixture(t)
	challenge := insertChallenge(t, f, 0x45, "12345678")
	for range 5 {
		_, err := f.service.VerifySignIn(context.Background(), SignInVerifyRequest{ChallengeID: challenge, Code: "87654321", SessionType: "trusted"})
		assert.ErrorIs(t, err, ErrInvalidCode)
	}
	_, err := f.service.VerifySignIn(context.Background(), SignInVerifyRequest{ChallengeID: challenge, Code: "12345678", SessionType: "trusted"})
	assert.ErrorIs(t, err, ErrInvalidCode)
	var attempts int
	require.NoError(t, f.db.NewRaw(`SELECT attempts FROM sign_in_challenges`).Scan(context.Background(), &attempts))
	assert.Equal(t, 5, attempts)
}

func TestSignInExpiryBoundaryAndConcurrentSingleUse(t *testing.T) {
	t.Run("expires at ten minutes", func(t *testing.T) {
		f := newFixture(t)
		challenge := insertChallenge(t, f, 0x49, "12345678")
		f.service.now = func() time.Time { return f.now.Add(codeLifetime) }
		_, err := f.service.VerifySignIn(context.Background(), SignInVerifyRequest{ChallengeID: challenge, Code: "12345678", SessionType: "trusted"})
		assert.ErrorIs(t, err, ErrInvalidCode)
		var sessions int
		require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM sessions`).Scan(context.Background(), &sessions))
		assert.Equal(t, 1, sessions)
	})

	t.Run("one concurrent verification wins", func(t *testing.T) {
		f := newFixture(t)
		challenge := insertChallenge(t, f, 0x4a, "12345678")
		results := make(chan error, 2)
		for range 2 {
			go func() {
				_, err := f.service.VerifySignIn(context.Background(), SignInVerifyRequest{ChallengeID: challenge, Code: "12345678", SessionType: "trusted"})
				results <- err
			}()
		}
		successes, rejected := 0, 0
		for range 2 {
			err := <-results
			if err == nil {
				successes++
			} else if errors.Is(err, ErrInvalidCode) {
				rejected++
			}
		}
		assert.Equal(t, 1, successes)
		assert.Equal(t, 1, rejected)
		var sessions int
		require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM sessions`).Scan(context.Background(), &sessions))
		assert.Equal(t, 2, sessions)
	})
}

func TestSignInStartDoesNotDeliverForIneligibleAccess(t *testing.T) {
	tests := []struct {
		name       string
		invalidate string
	}{
		{name: "suspended", invalidate: `UPDATE recipient_access_generations SET state = 'suspended'`},
		{name: "revoked", invalidate: `UPDATE recipient_access_generations SET state = 'revoked', is_current = false, ended_at = now()`},
		{name: "noncurrent", invalidate: `UPDATE recipient_access_generations SET is_current = false`},
		{name: "archived Person", invalidate: `UPDATE people SET archived_at = now() WHERE display_name = 'Alex'`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			_, err := f.db.ExecContext(context.Background(), test.invalidate)
			require.NoError(t, err)
			f.service.random = bytes.NewReader(bytes.Repeat([]byte{0x4b}, 128))
			response, err := f.service.RequestSignIn(context.Background(), SignInRequest{Email: "alex@example.com"})
			require.NoError(t, err)
			assert.Equal(t, "accepted", response.Status)
			var challenges, deliveries int
			require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM sign_in_challenges`).Scan(context.Background(), &challenges))
			require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM email_deliveries WHERE kind = 'sign_in_code'`).Scan(context.Background(), &deliveries))
			assert.Zero(t, challenges)
			assert.Zero(t, deliveries)
		})
	}
}

func TestPushSubscriptionRequiresOwnedTrustedSession(t *testing.T) {
	f := newFixture(t)
	var epoch []byte
	require.NoError(t, f.db.NewRaw(`SELECT security_epoch FROM system_settings WHERE id = 1`).Scan(context.Background(), &epoch))
	publicID := uuid.New()
	_, err := f.db.NewRaw(`INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, absolute_expires_at) VALUES (?, ?, ?, ?, ?, 'public', ?)`, publicID, bytes.Repeat([]byte{0x64}, 32), f.personID, f.accessID, epoch, f.now.Add(12*time.Hour)).Exec(context.Background())
	require.NoError(t, err)
	_, err = f.db.NewRaw(`INSERT INTO push_subscriptions (id, session_id, person_id, endpoint_hash) VALUES (?, ?, ?, ?)`, uuid.New(), publicID, f.personID, bytes.Repeat([]byte{0x65}, 32)).Exec(context.Background())
	require.Error(t, err, "Public-computer Sessions must not persist push subscriptions")
	otherPersonID := uuid.New()
	_, err = f.db.NewRaw(`INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Other', 'other')`, otherPersonID).Exec(context.Background())
	require.NoError(t, err)
	_, err = f.db.NewRaw(`INSERT INTO push_subscriptions (id, session_id, person_id, endpoint_hash) VALUES (?, ?, ?, ?)`, uuid.New(), f.sessionID, otherPersonID, bytes.Repeat([]byte{0x66}, 32)).Exec(context.Background())
	require.Error(t, err, "a push subscription Person must own the linked Session")
}

func TestSessionInspectionRenameRevocationAndSignOutAllDisablePush(t *testing.T) {
	f := newFixture(t)
	actor, err := f.auth.AuthorizeSession(context.Background(), f.credential, f.csrf, true)
	require.NoError(t, err)
	secondID, publicID := uuid.New(), uuid.New()
	trustedRaw := bytes.Repeat([]byte{0x66}, 32)
	trustedHash := sha256.Sum256(trustedRaw)
	publicRaw := bytes.Repeat([]byte{0x67}, 32)
	publicHash := sha256.Sum256(publicRaw)
	var epoch []byte
	require.NoError(t, f.db.NewRaw(`SELECT security_epoch FROM system_settings WHERE id = 1`).Scan(context.Background(), &epoch))
	_, err = f.db.NewRaw(`INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at) VALUES (?, ?, ?, ?, ?, 'trusted', ?); INSERT INTO push_subscriptions (id, session_id, person_id, endpoint_hash) VALUES (?, ?, ?, ?); INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, absolute_expires_at) VALUES (?, ?, ?, ?, ?, 'public', ?)`, secondID, trustedHash[:], f.personID, f.accessID, epoch, f.now.Add(365*24*time.Hour), uuid.New(), secondID, f.personID, bytes.Repeat([]byte{0x77}, 32), publicID, publicHash[:], f.personID, f.accessID, epoch, f.now.Add(12*time.Hour)).Exec(context.Background())
	require.NoError(t, err)
	list, err := f.service.ListSelf(context.Background(), actor)
	require.NoError(t, err)
	require.Len(t, list.Sessions, 3)
	assert.True(t, list.Sessions[0].Current || list.Sessions[1].Current || list.Sessions[2].Current)
	for _, item := range list.Sessions {
		if item.ID == publicID.String() {
			assert.Equal(t, "public", item.SessionType)
			assert.Equal(t, "active", item.Status)
			assert.False(t, item.PushAllowed)
			assert.Equal(t, f.now.Add(12*time.Hour), item.ExpiresAt.UTC())
		}
	}
	require.NoError(t, f.service.Rename(context.Background(), actor, secondID, RenameRequest{Label: "Shared laptop"}))
	var label string
	require.NoError(t, f.db.NewRaw(`SELECT label FROM sessions WHERE id = ?`, secondID).Scan(context.Background(), &label))
	assert.Equal(t, "Shared laptop", label)
	current, err := f.service.Revoke(context.Background(), actor, secondID)
	require.NoError(t, err)
	assert.False(t, current)
	var revoked, pushDisabled bool
	require.NoError(t, f.db.NewRaw(`SELECT revoked_at IS NOT NULL FROM sessions WHERE id = ?`, secondID).Scan(context.Background(), &revoked))
	require.NoError(t, f.db.NewRaw(`SELECT disabled_at IS NOT NULL FROM push_subscriptions WHERE session_id = ?`, secondID).Scan(context.Background(), &pushDisabled))
	assert.True(t, revoked)
	assert.True(t, pushDisabled)
	require.NoError(t, f.service.SignOutAll(context.Background(), actor))
	var activeSessions, activePush int
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM sessions WHERE revoked_at IS NULL`).Scan(context.Background(), &activeSessions))
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM push_subscriptions WHERE disabled_at IS NULL`).Scan(context.Background(), &activePush))
	assert.Zero(t, activeSessions)
	assert.Zero(t, activePush)
}

func TestSessionMutationsCannotCrossRecipientBoundary(t *testing.T) {
	f := newFixture(t)
	actor, err := f.auth.AuthorizeSession(context.Background(), f.credential, f.csrf, true)
	require.NoError(t, err)
	otherPersonID, otherAccessID, otherSessionID := uuid.New(), uuid.New(), uuid.New()
	var epoch []byte
	require.NoError(t, f.db.NewRaw(`SELECT security_epoch FROM system_settings WHERE id = 1`).Scan(context.Background(), &epoch))
	_, err = f.db.NewRaw(`INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Other', 'other'); INSERT INTO recipient_access_generations (id, person_id, generation, state, is_current, onboarding_completed_at) VALUES (?, ?, 1, 'completed', true, now()); INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at) VALUES (?, ?, ?, ?, ?, 'trusted', now() + interval '1 year')`, otherPersonID, otherAccessID, otherPersonID, otherSessionID, bytes.Repeat([]byte{0x4c}, 32), otherPersonID, otherAccessID, epoch).Exec(context.Background())
	require.NoError(t, err)

	list, err := f.service.ListSelf(context.Background(), actor)
	require.NoError(t, err)
	require.Len(t, list.Sessions, 1)
	assert.Equal(t, f.sessionID.String(), list.Sessions[0].ID)
	assert.ErrorIs(t, f.service.Rename(context.Background(), actor, otherSessionID, RenameRequest{Label: "Mine"}), ErrInvalidSession)
	_, err = f.service.Revoke(context.Background(), actor, otherSessionID)
	assert.ErrorIs(t, err, ErrInvalidSession)
	var label string
	var active bool
	require.NoError(t, f.db.NewRaw(`SELECT label, revoked_at IS NULL FROM sessions WHERE id = ?`, otherSessionID).Scan(context.Background(), &label, &active))
	assert.Empty(t, label)
	assert.True(t, active)
}

func TestEmailChangeConsumesSiblingRequestsFromThePreviousAddress(t *testing.T) {
	f := newFixture(t)
	actor, err := f.auth.AuthorizeSession(context.Background(), f.credential, f.csrf, true)
	require.NoError(t, err)
	firstID, staleID := uuid.New(), uuid.New()
	firstOld, firstNew := "11112222", "33334444"
	staleOld, staleNew := "55556666", "77778888"
	_, err = f.db.NewRaw(`INSERT INTO email_change_requests (id, person_id, recipient_access_generation_id, session_id, old_email, new_email, new_normalized_email, old_code_hash, new_code_hash, expires_at) VALUES (?, ?, ?, ?, 'alex@example.com', 'first@example.com', 'first@example.com', ?, ?, ?), (?, ?, ?, ?, 'alex@example.com', 'stale@example.com', 'stale@example.com', ?, ?, ?)`, firstID, f.personID, f.accessID, f.sessionID, f.service.codeHash("email-change-old", firstID[:], firstOld), f.service.codeHash("email-change-new", firstID[:], firstNew), f.now.Add(codeLifetime), staleID, f.personID, f.accessID, f.sessionID, f.service.codeHash("email-change-old", staleID[:], staleOld), f.service.codeHash("email-change-new", staleID[:], staleNew), f.now.Add(codeLifetime)).Exec(context.Background())
	require.NoError(t, err)
	_, err = f.service.CompleteEmailChange(context.Background(), actor, EmailChangeCompleteRequest{RequestID: firstID.String(), OldCode: firstOld, NewCode: firstNew})
	require.NoError(t, err)
	_, err = f.service.CompleteEmailChange(context.Background(), actor, EmailChangeCompleteRequest{RequestID: staleID.String(), OldCode: staleOld, NewCode: staleNew})
	assert.ErrorIs(t, err, ErrChangeNotFound)
	var email string
	require.NoError(t, f.db.NewRaw(`SELECT normalized_email FROM recipient_emails WHERE recipient_access_generation_id = ? AND is_current`, f.accessID).Scan(context.Background(), &email))
	assert.Equal(t, "first@example.com", email)
}

func TestEmailChangeProofExpiresAtBoundary(t *testing.T) {
	f := newFixture(t)
	actor, err := f.auth.AuthorizeSession(context.Background(), f.credential, f.csrf, true)
	require.NoError(t, err)
	requestID := uuid.New()
	oldCode, newCode := "11112222", "33334444"
	_, err = f.db.NewRaw(`INSERT INTO email_change_requests (id, person_id, recipient_access_generation_id, session_id, old_email, new_email, new_normalized_email, old_code_hash, new_code_hash, expires_at) VALUES (?, ?, ?, ?, 'alex@example.com', 'new@example.com', 'new@example.com', ?, ?, ?)`, requestID, f.personID, f.accessID, f.sessionID, f.service.codeHash("email-change-old", requestID[:], oldCode), f.service.codeHash("email-change-new", requestID[:], newCode), f.now).Exec(context.Background())
	require.NoError(t, err)
	_, err = f.service.CompleteEmailChange(context.Background(), actor, EmailChangeCompleteRequest{RequestID: requestID.String(), OldCode: oldCode, NewCode: newCode})
	assert.ErrorIs(t, err, ErrInvalidCode)
	var email string
	require.NoError(t, f.db.NewRaw(`SELECT normalized_email FROM recipient_emails WHERE recipient_access_generation_id = ? AND is_current`, f.accessID).Scan(context.Background(), &email))
	assert.Equal(t, "alex@example.com", email)
}

func TestEmailChangeDoesNotExtendPublicSessionAbsoluteLifetime(t *testing.T) {
	f := newFixture(t)
	originalExpiry := f.now.Add(12 * time.Hour)
	_, err := f.db.NewRaw(`UPDATE sessions SET session_type = 'public', idle_expires_at = NULL, absolute_expires_at = ? WHERE id = ?`, originalExpiry, f.sessionID).Exec(context.Background())
	require.NoError(t, err)
	requestID := uuid.New()
	oldCode, newCode := "11112222", "33334444"
	_, err = f.db.NewRaw(`INSERT INTO email_change_requests (id, person_id, recipient_access_generation_id, session_id, old_email, new_email, new_normalized_email, old_code_hash, new_code_hash, expires_at) VALUES (?, ?, ?, ?, 'alex@example.com', 'new@example.com', 'new@example.com', ?, ?, ?)`, requestID, f.personID, f.accessID, f.sessionID, f.service.codeHash("email-change-old", requestID[:], oldCode), f.service.codeHash("email-change-new", requestID[:], newCode), originalExpiry).Exec(context.Background())
	require.NoError(t, err)
	f.service.now = func() time.Time { return f.now.Add(11 * time.Hour) }
	actor := setup.SessionActor{PersonID: f.personID, AccessID: f.accessID, SessionID: f.sessionID}

	_, err = f.service.CompleteEmailChange(context.Background(), actor, EmailChangeCompleteRequest{RequestID: requestID.String(), OldCode: oldCode, NewCode: newCode})
	require.NoError(t, err)
	var rotatedExpiry time.Time
	require.NoError(t, f.db.NewRaw(`SELECT absolute_expires_at FROM sessions WHERE id = ?`, f.sessionID).Scan(context.Background(), &rotatedExpiry))
	assert.Equal(t, originalExpiry, rotatedExpiry.UTC())
}

func TestEmailChangeStartQueuesFreshProofsToBothAddresses(t *testing.T) {
	f := newFixture(t)
	actor, err := f.auth.AuthorizeSession(context.Background(), f.credential, f.csrf, true)
	require.NoError(t, err)
	response, err := f.service.StartEmailChange(context.Background(), actor, EmailChangeRequest{NewEmail: "new@example.com"})
	require.NoError(t, err)
	assert.NotEmpty(t, response.RequestID)
	assert.Equal(t, f.now.Add(codeLifetime), response.ExpiresAt)
	var recipients, encrypted int
	require.NoError(t, f.db.NewRaw(`SELECT count(DISTINCT recipient), count(*) FILTER (WHERE body LIKE 'v1:%') FROM email_deliveries WHERE kind IN ('email_change_old_code', 'email_change_new_code')`).Scan(context.Background(), &recipients, &encrypted))
	assert.Equal(t, 2, recipients)
	assert.Equal(t, 2, encrypted)
}

func TestSignedInEmailChangeRequiresBothCodesAndPreservesIdentity(t *testing.T) {
	f := newFixture(t)
	actor, err := f.auth.AuthorizeSession(context.Background(), f.credential, f.csrf, true)
	require.NoError(t, err)
	oldAddressChallenge := insertChallenge(t, f, 0x46, "87654321")
	siblingID := uuid.New()
	siblingRaw := bytes.Repeat([]byte{0x47}, 32)
	siblingHash := sha256.Sum256(siblingRaw)
	var epoch []byte
	require.NoError(t, f.db.NewRaw(`SELECT security_epoch FROM system_settings WHERE id = 1`).Scan(context.Background(), &epoch))
	_, err = f.db.NewRaw(`INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at) VALUES (?, ?, ?, ?, ?, 'trusted', ?); INSERT INTO push_subscriptions (id, session_id, person_id, endpoint_hash) VALUES (?, ?, ?, ?)`, siblingID, siblingHash[:], f.personID, f.accessID, epoch, f.now.Add(365*24*time.Hour), uuid.New(), siblingID, f.personID, bytes.Repeat([]byte{0x48}, 32)).Exec(context.Background())
	require.NoError(t, err)
	requestID := uuid.New()
	oldCode, newCode := "11112222", "33334444"
	_, err = f.db.NewRaw(`INSERT INTO email_change_requests (id, person_id, recipient_access_generation_id, session_id, old_email, new_email, new_normalized_email, old_code_hash, new_code_hash, expires_at) VALUES (?, ?, ?, ?, 'alex@example.com', 'new@example.com', 'new@example.com', ?, ?, ?)`, requestID, f.personID, f.accessID, f.sessionID, f.service.codeHash("email-change-old", requestID[:], oldCode), f.service.codeHash("email-change-new", requestID[:], newCode), f.now.Add(10*time.Minute)).Exec(context.Background())
	require.NoError(t, err)
	_, err = f.service.CompleteEmailChange(context.Background(), actor, EmailChangeCompleteRequest{RequestID: requestID.String(), OldCode: oldCode, NewCode: "00000000"})
	assert.ErrorIs(t, err, ErrInvalidCode)
	result, err := f.service.CompleteEmailChange(context.Background(), actor, EmailChangeCompleteRequest{RequestID: requestID.String(), OldCode: oldCode, NewCode: newCode})
	require.NoError(t, err)
	assert.NotEqual(t, f.csrf, result.CSRFToken)
	var personID, accessID uuid.UUID
	var email string
	require.NoError(t, f.db.NewRaw(`SELECT access.person_id, email.recipient_access_generation_id, email.normalized_email FROM recipient_emails AS email JOIN recipient_access_generations AS access ON access.id = email.recipient_access_generation_id WHERE email.is_current`).Scan(context.Background(), &personID, &accessID, &email))
	assert.Equal(t, f.personID, personID)
	assert.Equal(t, f.accessID, accessID)
	assert.Equal(t, "new@example.com", email)
	_, err = f.auth.AuthorizeSession(context.Background(), f.credential, result.CSRFToken, false)
	assert.ErrorIs(t, err, setup.ErrUnauthenticated, "email change must retire the previous credential")
	_, err = f.auth.AuthorizeSession(context.Background(), result.session.Credential, result.CSRFToken, false)
	require.NoError(t, err)
	var siblingRevoked, siblingPushDisabled bool
	require.NoError(t, f.db.NewRaw(`SELECT revoked_at IS NOT NULL FROM sessions WHERE id = ?`, siblingID).Scan(context.Background(), &siblingRevoked))
	require.NoError(t, f.db.NewRaw(`SELECT disabled_at IS NOT NULL FROM push_subscriptions WHERE session_id = ?`, siblingID).Scan(context.Background(), &siblingPushDisabled))
	assert.True(t, siblingRevoked)
	assert.True(t, siblingPushDisabled)
	_, err = f.service.CompleteEmailChange(context.Background(), actor, EmailChangeCompleteRequest{RequestID: requestID.String(), OldCode: oldCode, NewCode: newCode})
	assert.ErrorIs(t, err, ErrChangeNotFound, "email-change proofs must be single-use")
	_, err = f.service.VerifySignIn(context.Background(), SignInVerifyRequest{ChallengeID: oldAddressChallenge, Code: "87654321", SessionType: "trusted"})
	assert.ErrorIs(t, err, ErrInvalidCode, "changing email must invalidate codes sent to the old mailbox")
}
