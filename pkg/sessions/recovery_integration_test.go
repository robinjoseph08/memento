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
	_, err = f.service.VerifySignIn(context.Background(), SignInVerifyRequest{ChallengeID: oldAddressChallenge, Code: "13572468", SessionType: "trusted"})
	assert.ErrorIs(t, err, ErrInvalidCode, "recovery must invalidate codes sent to the unavailable mailbox")
}
