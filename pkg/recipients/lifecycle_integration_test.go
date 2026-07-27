//go:build integration

package recipients

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSuspensionRevocationAndReinvitationPreserveAndIsolateGenerations(t *testing.T) {
	fixture := newRecipientFixture(t)
	first := designate(t, fixture)
	firstAccessID := uuid.MustParse(first.Access.ID)
	_, err := fixture.db.NewRaw(`UPDATE recipient_access_generations SET state = 'completed', onboarding_completed_at = now() WHERE id = ?`, first.Access.ID).Exec(context.Background())
	require.NoError(t, err)
	var epoch []byte
	require.NoError(t, fixture.db.NewRaw(`SELECT security_epoch FROM system_settings WHERE id = 1`).Scan(context.Background(), &epoch))
	sessionID := uuid.New()
	raw := bytes.Repeat([]byte{0x71}, 32)
	hash := sha256.Sum256(raw)
	_, err = fixture.db.NewRaw(`INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at) VALUES (?, ?, ?, ?, ?, 'trusted', now() + interval '1 year'); INSERT INTO push_subscriptions (id, session_id, person_id, endpoint_hash) VALUES (?, ?, ?, ?)`, sessionID, hash[:], fixture.personID, first.Access.ID, epoch, uuid.New(), sessionID, fixture.personID, bytes.Repeat([]byte{0x72}, 32)).Exec(context.Background())
	require.NoError(t, err)

	suspended, err := fixture.service.Suspend(context.Background(), fixture.actor, fixture.personID, firstAccessID)
	require.NoError(t, err)
	assert.Equal(t, "suspended", suspended.Access.State)
	assert.Equal(t, first.Access.ID, suspended.Access.ID)
	var revokedSession, disabledPush bool
	require.NoError(t, fixture.db.NewRaw(`SELECT revoked_at IS NOT NULL FROM sessions WHERE id = ?`, sessionID).Scan(context.Background(), &revokedSession))
	require.NoError(t, fixture.db.NewRaw(`SELECT disabled_at IS NOT NULL FROM push_subscriptions WHERE session_id = ?`, sessionID).Scan(context.Background(), &disabledPush))
	assert.True(t, revokedSession)
	assert.True(t, disabledPush)

	restored, err := fixture.service.Restore(context.Background(), fixture.actor, fixture.personID, firstAccessID)
	require.NoError(t, err)
	assert.Equal(t, "completed", restored.Access.State)
	assert.Equal(t, first.Access.ID, restored.Access.ID, "Suspension must preserve the access generation")

	revoked, err := fixture.service.RevokeAccess(context.Background(), fixture.actor, fixture.personID, firstAccessID)
	require.NoError(t, err)
	assert.Equal(t, "revoked", revoked.Access.State)
	var oldCurrent bool
	var oldState string
	require.NoError(t, fixture.db.NewRaw(`SELECT is_current, state FROM recipient_access_generations WHERE id = ?`, first.Access.ID).Scan(context.Background(), &oldCurrent, &oldState))
	assert.False(t, oldCurrent)
	assert.Equal(t, "revoked", oldState)

	reinvited, err := fixture.service.Designate(context.Background(), fixture.actor, fixture.personID, DesignateRequest{Email: "alex-again@example.com"})
	require.NoError(t, err)
	assert.Equal(t, 2, reinvited.Access.Generation)
	assert.NotEqual(t, first.Access.ID, reinvited.Access.ID)
	var generations int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM recipient_access_generations WHERE person_id = ?`, fixture.personID).Scan(context.Background(), &generations))
	assert.Equal(t, 2, generations, "old authorization history must remain but cannot match the new generation")
	_, err = fixture.service.RevokeAccess(context.Background(), fixture.actor, fixture.personID, firstAccessID)
	assert.ErrorIs(t, err, ErrLifecycleState, "a stale action must not target the replacement generation")
	var currentAccess uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT id FROM recipient_access_generations WHERE person_id = ? AND is_current`, fixture.personID).Scan(context.Background(), &currentAccess))
	assert.Equal(t, uuid.MustParse(reinvited.Access.ID), currentAccess)

	oldGenerationRaw := bytes.Repeat([]byte{0x73}, 32)
	oldGenerationHash := sha256.Sum256(oldGenerationRaw)
	_, err = fixture.db.NewRaw(`INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at) VALUES (?, ?, ?, ?, ?, 'trusted', now() + interval '1 year')`, uuid.New(), oldGenerationHash[:], fixture.personID, first.Access.ID, epoch).Exec(context.Background())
	require.NoError(t, err)
	_, err = fixture.auth.AuthorizeSession(context.Background(), hex.EncodeToString(oldGenerationRaw), "", false)
	assert.ErrorIs(t, err, setup.ErrUnauthenticated, "a Session bound to an ended generation cannot authorize after reinvitation")
}

func TestDirectRevocationInvalidatesGenerationSessionsAndPush(t *testing.T) {
	fixture := newRecipientFixture(t)
	recipient := designate(t, fixture)
	accessID := uuid.MustParse(recipient.Access.ID)
	_, err := fixture.db.NewRaw(`UPDATE recipient_access_generations SET state = 'completed', onboarding_completed_at = now() WHERE id = ?`, accessID).Exec(context.Background())
	require.NoError(t, err)
	var epoch []byte
	require.NoError(t, fixture.db.NewRaw(`SELECT security_epoch FROM system_settings WHERE id = 1`).Scan(context.Background(), &epoch))
	for i, sessionType := range []string{"trusted", "public"} {
		raw := bytes.Repeat([]byte{byte(0x74 + i)}, 32)
		hash := sha256.Sum256(raw)
		sessionID := uuid.New()
		_, err = fixture.db.NewRaw(`INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at, absolute_expires_at) VALUES (?, ?, ?, ?, ?, ?, CASE WHEN ? = 'trusted' THEN now() + interval '1 year' END, CASE WHEN ? = 'public' THEN now() + interval '12 hours' END)`, sessionID, hash[:], fixture.personID, accessID, epoch, sessionType, sessionType, sessionType).Exec(context.Background())
		require.NoError(t, err)
		if sessionType == "trusted" {
			_, err = fixture.db.NewRaw(`INSERT INTO push_subscriptions (id, session_id, person_id, endpoint_hash) VALUES (?, ?, ?, ?)`, uuid.New(), sessionID, fixture.personID, bytes.Repeat([]byte{byte(0x78 + i)}, 32)).Exec(context.Background())
			require.NoError(t, err)
		}
	}

	_, err = fixture.service.RevokeAccess(context.Background(), fixture.actor, fixture.personID, accessID)
	require.NoError(t, err)
	var activeSessions, activePush int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM sessions WHERE recipient_access_generation_id = ? AND revoked_at IS NULL`, accessID).Scan(context.Background(), &activeSessions))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM push_subscriptions WHERE person_id = ? AND disabled_at IS NULL`, fixture.personID).Scan(context.Background(), &activePush))
	assert.Zero(t, activeSessions)
	assert.Zero(t, activePush)
}

func TestCuratorLifecycleGuardPreservesCuratorSession(t *testing.T) {
	fixture := newRecipientFixture(t)
	var accessID uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT id FROM recipient_access_generations WHERE person_id = ? AND is_current`, fixture.actor.PersonID).Scan(context.Background(), &accessID))
	_, err := fixture.service.RevokeAccess(context.Background(), fixture.actor, fixture.actor.PersonID, accessID)
	assert.ErrorIs(t, err, ErrCuratorLifecycle)
	_, err = fixture.auth.AuthorizeSession(context.Background(), fixture.credential, fixture.csrf, false)
	require.NoError(t, err)
}
