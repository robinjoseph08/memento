//go:build integration

package sessions

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCuratorRecoveryStartQueuesOnlyANewMailboxProof(t *testing.T) {
	f := newFixture(t)
	curatorID := uuid.New()
	_, err := f.db.NewRaw(`INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Robin', 'robin'); INSERT INTO person_roles (person_id, role) VALUES (?, 'curator')`, curatorID, curatorID).Exec(context.Background())
	require.NoError(t, err)
	response, err := f.service.StartRecovery(context.Background(), setup.CuratorSession{PersonID: curatorID, SessionID: f.sessionID}, f.personID, RecoveryRequest{NewEmail: "recovered@example.com"})
	require.NoError(t, err)
	assert.NotEmpty(t, response.RecoveryID)
	var recipients, encrypted int
	require.NoError(t, f.db.NewRaw(`SELECT count(DISTINCT recipient), count(*) FILTER (WHERE body LIKE 'v1:%') FROM email_deliveries WHERE kind = 'curator_recovery_code'`).Scan(context.Background(), &recipients, &encrypted))
	assert.Equal(t, 1, recipients)
	assert.Equal(t, 1, encrypted)
}

func TestRecoveryProofExpiresAtBoundary(t *testing.T) {
	f := newFixture(t)
	curatorID := uuid.New()
	_, err := f.db.NewRaw(`INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Robin', 'robin'); INSERT INTO person_roles (person_id, role) VALUES (?, 'curator')`, curatorID, curatorID).Exec(context.Background())
	require.NoError(t, err)
	recoveryID := uuid.New()
	code := "24681357"
	_, err = f.db.NewRaw(`INSERT INTO curator_recovery_requests (id, person_id, recipient_access_generation_id, new_email, new_normalized_email, code_hash, expires_at, created_by_person_id) VALUES (?, ?, ?, 'recovered@example.com', 'recovered@example.com', ?, ?, ?)`, recoveryID, f.personID, f.accessID, f.service.codeHash("recovery", recoveryID[:], code), f.now, curatorID).Exec(context.Background())
	require.NoError(t, err)
	err = f.service.CompleteRecovery(context.Background(), setup.CuratorSession{PersonID: curatorID, SessionID: f.sessionID}, f.personID, RecoveryCompleteRequest{RecoveryID: recoveryID.String(), Code: code})
	assert.ErrorIs(t, err, ErrInvalidCode)
	var email string
	require.NoError(t, f.db.NewRaw(`SELECT normalized_email FROM recipient_emails WHERE recipient_access_generation_id = ? AND is_current`, f.accessID).Scan(context.Background(), &email))
	assert.Equal(t, "alex@example.com", email)
}

func TestCuratorRecoveryChangesOnlyEmailAndRevokesAllSessions(t *testing.T) {
	f := newFixture(t)
	oldAddressChallenge := insertChallenge(t, f, 0x54, "13572468")
	curatorID := uuid.New()
	_, err := f.db.NewRaw(`INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Robin', 'robin'); INSERT INTO person_roles (person_id, role) VALUES (?, 'curator'); INSERT INTO onboarding_choices (recipient_access_generation_id, privacy_acknowledged, engagement_acknowledged, interest_list_acknowledged, email_previews_acknowledged, push_guidance_acknowledged, informed_choices_version, email_preference, completed_at) VALUES (?, true, true, true, true, true, 2, 'weekly', now()); INSERT INTO notification_preferences (recipient_access_generation_id, email_preference) VALUES (?, 'weekly')`, curatorID, curatorID, f.accessID, f.accessID).Exec(context.Background())
	require.NoError(t, err)
	recoveryID := uuid.New()
	code := "24681357"
	_, err = f.db.NewRaw(`INSERT INTO curator_recovery_requests (id, person_id, recipient_access_generation_id, new_email, new_normalized_email, code_hash, expires_at, created_by_person_id) VALUES (?, ?, ?, 'recovered@example.com', 'recovered@example.com', ?, ?, ?)`, recoveryID, f.personID, f.accessID, f.service.codeHash("recovery", recoveryID[:], code), f.now.Add(codeLifetime), curatorID).Exec(context.Background())
	require.NoError(t, err)
	_, err = f.db.NewRaw(`INSERT INTO push_subscriptions (id, session_id, person_id, endpoint_hash) VALUES (?, ?, ?, ?)`, uuid.New(), f.sessionID, f.personID, bytes.Repeat([]byte{0x53}, 32)).Exec(context.Background())
	require.NoError(t, err)
	actor := setup.CuratorSession{PersonID: curatorID, SessionID: f.sessionID}
	require.NoError(t, f.service.CompleteRecovery(context.Background(), actor, f.personID, RecoveryCompleteRequest{RecoveryID: recoveryID.String(), Code: code}))
	var currentEmail, state string
	var currentAccess uuid.UUID
	var onboarding, preference int
	require.NoError(t, f.db.NewRaw(`SELECT email.normalized_email, access.id, access.state FROM recipient_emails AS email JOIN recipient_access_generations AS access ON access.id = email.recipient_access_generation_id WHERE email.is_current`).Scan(context.Background(), &currentEmail, &currentAccess, &state))
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM onboarding_choices WHERE recipient_access_generation_id = ?`, f.accessID).Scan(context.Background(), &onboarding))
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM notification_preferences WHERE recipient_access_generation_id = ?`, f.accessID).Scan(context.Background(), &preference))
	assert.Equal(t, "recovered@example.com", currentEmail)
	assert.Equal(t, f.accessID, currentAccess)
	assert.Equal(t, "completed", state)
	assert.Equal(t, 1, onboarding)
	assert.Equal(t, 1, preference)
	var activeSessions, activePush int
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM sessions WHERE person_id = ? AND revoked_at IS NULL`, f.personID).Scan(context.Background(), &activeSessions))
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM push_subscriptions WHERE person_id = ? AND disabled_at IS NULL`, f.personID).Scan(context.Background(), &activePush))
	assert.Zero(t, activeSessions)
	assert.Zero(t, activePush)
	err = f.service.CompleteRecovery(context.Background(), actor, f.personID, RecoveryCompleteRequest{RecoveryID: recoveryID.String(), Code: code})
	assert.ErrorIs(t, err, ErrInvalidCode, "recovery proof must be single-use")
	_, err = f.service.VerifySignIn(context.Background(), SignInVerifyRequest{ChallengeID: oldAddressChallenge, Code: "13572468", SessionType: "trusted"})
	assert.ErrorIs(t, err, ErrInvalidCode, "recovery must invalidate codes sent to the unavailable mailbox")
}

func TestRecoveryCannotCrossAccessGenerationReplacement(t *testing.T) {
	f := newFixture(t)
	curatorID := uuid.New()
	_, err := f.db.NewRaw(`INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Robin', 'robin'); INSERT INTO person_roles (person_id, role) VALUES (?, 'curator')`, curatorID, curatorID).Exec(context.Background())
	require.NoError(t, err)
	recoveryID := uuid.New()
	code := "24681357"
	_, err = f.db.NewRaw(`INSERT INTO curator_recovery_requests (id, person_id, recipient_access_generation_id, new_email, new_normalized_email, code_hash, expires_at, created_by_person_id) VALUES (?, ?, ?, 'stale@example.com', 'stale@example.com', ?, ?, ?)`, recoveryID, f.personID, f.accessID, f.service.codeHash("recovery", recoveryID[:], code), f.now.Add(codeLifetime), curatorID).Exec(context.Background())
	require.NoError(t, err)
	newAccessID, newSessionID := uuid.New(), uuid.New()
	var epoch []byte
	require.NoError(t, f.db.NewRaw(`SELECT security_epoch FROM system_settings WHERE id = 1`).Scan(context.Background(), &epoch))
	_, err = f.db.NewRaw(`UPDATE recipient_access_generations SET state = 'revoked', is_current = false, ended_at = now() WHERE id = ?; UPDATE recipient_emails SET is_current = false, ended_at = now() WHERE recipient_access_generation_id = ? AND is_current; INSERT INTO recipient_access_generations (id, person_id, generation, state, is_current, onboarding_completed_at) VALUES (?, ?, 2, 'completed', true, now()); INSERT INTO recipient_emails (id, recipient_access_generation_id, email, normalized_email) VALUES (?, ?, 'replacement@example.com', 'replacement@example.com'); INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at) VALUES (?, ?, ?, ?, ?, 'trusted', now() + interval '1 year'); INSERT INTO push_subscriptions (id, session_id, person_id, endpoint_hash) VALUES (?, ?, ?, ?)`, f.accessID, f.accessID, newAccessID, f.personID, uuid.New(), newAccessID, newSessionID, bytes.Repeat([]byte{0x61}, 32), f.personID, newAccessID, epoch, uuid.New(), newSessionID, f.personID, bytes.Repeat([]byte{0x62}, 32)).Exec(context.Background())
	require.NoError(t, err)

	err = f.service.CompleteRecovery(context.Background(), setup.CuratorSession{PersonID: curatorID, SessionID: f.sessionID}, f.personID, RecoveryCompleteRequest{RecoveryID: recoveryID.String(), Code: code})
	assert.ErrorIs(t, err, ErrRecoveryNotFound)
	var currentEmail string
	var sessionActive, pushActive bool
	require.NoError(t, f.db.NewRaw(`SELECT normalized_email FROM recipient_emails WHERE recipient_access_generation_id = ? AND is_current`, newAccessID).Scan(context.Background(), &currentEmail))
	require.NoError(t, f.db.NewRaw(`SELECT revoked_at IS NULL FROM sessions WHERE id = ?`, newSessionID).Scan(context.Background(), &sessionActive))
	require.NoError(t, f.db.NewRaw(`SELECT disabled_at IS NULL FROM push_subscriptions WHERE session_id = ?`, newSessionID).Scan(context.Background(), &pushActive))
	assert.Equal(t, "replacement@example.com", currentEmail)
	assert.True(t, sessionActive)
	assert.True(t, pushActive)
}
