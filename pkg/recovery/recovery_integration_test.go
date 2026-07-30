//go:build integration

package recovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecoveryNonceRotationReviewAndFreshCuratorRelease(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	require.NoError(t, migrations.Apply(ctx, db))

	personID, accessID, oldSessionID := uuid.New(), uuid.New(), uuid.New()
	_, err := db.NewRaw(`
		INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Curator', 'curator');
		INSERT INTO person_roles (person_id, role) VALUES (?, 'curator'), (?, 'recipient');
		INSERT INTO recipient_access_generations
			(id, person_id, generation, state, is_current, onboarding_completed_at)
		VALUES (?, ?, 1, 'completed', true, now());
		INSERT INTO recipient_emails
			(id, recipient_access_generation_id, email, normalized_email, is_current)
		VALUES (?, ?, 'curator@example.com', 'curator@example.com', true);
		INSERT INTO sessions
			(id, credential_hash, person_id, recipient_access_generation_id, security_epoch,
			 session_type, idle_expires_at)
		SELECT ?, decode(repeat('11', 32), 'hex'), ?, ?, security_epoch, 'trusted', now() + interval '1 day'
		FROM system_settings WHERE id = 1;
		UPDATE system_settings SET setup_complete = true WHERE id = 1
	`, personID, personID, personID, accessID, personID, uuid.New(), accessID,
		oldSessionID, personID, accessID).Exec(ctx)
	require.NoError(t, err)

	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	randomEpochs := append(bytes.Repeat([]byte{0x41}, 32), bytes.Repeat([]byte{0x42}, 32)...)
	randomEpochs = append(randomEpochs, bytes.Repeat([]byte{0x43}, 32)...)
	service := New(db, WithClock(func() time.Time { return now }), WithRandom(bytes.NewReader(randomEpochs)))
	var originalEpoch []byte
	require.NoError(t, db.NewRaw(`SELECT security_epoch FROM system_settings WHERE id = 1`).Scan(ctx, &originalEpoch))

	const nonce = "first-fresh-recovery-nonce-with-more-than-32-bytes"
	activated, err := service.Activate(ctx, nonce)
	require.NoError(t, err)
	assert.True(t, activated)

	var held bool
	var epoch, nonceHash []byte
	var auditCount int
	require.NoError(t, db.NewRaw(`SELECT recovery_hold, security_epoch, recovery_nonce_hash
		FROM system_settings WHERE id = 1`).Scan(ctx, &held, &epoch, &nonceHash))
	assert.True(t, held)
	assert.NotEqual(t, originalEpoch, epoch)
	expectedHash := sha256.Sum256([]byte(nonce))
	assert.Equal(t, expectedHash[:], nonceHash)
	assert.NotEqual(t, []byte(nonce), nonceHash)
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM security_audit_events
		WHERE action = 'recovery_hold_started'`).Scan(ctx, &auditCount))
	assert.Equal(t, 1, auditCount)
	var leaked bool
	require.NoError(t, db.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM security_audit_events WHERE metadata::text LIKE '%' || ? || '%'
	)`, nonce).Scan(ctx, &leaked))
	assert.False(t, leaked)

	activated, err = service.Activate(ctx, nonce)
	require.NoError(t, err)
	assert.False(t, activated)
	var idempotentEpoch []byte
	require.NoError(t, db.NewRaw(`SELECT security_epoch FROM system_settings WHERE id = 1`).Scan(ctx, &idempotentEpoch))
	assert.Equal(t, epoch, idempotentEpoch)

	activated, err = service.Activate(ctx, "second-fresh-recovery-nonce-with-more-than-32-bytes")
	require.NoError(t, err)
	assert.True(t, activated)
	var secondEpoch []byte
	require.NoError(t, db.NewRaw(`SELECT security_epoch FROM system_settings WHERE id = 1`).Scan(ctx, &secondEpoch))
	assert.NotEqual(t, epoch, secondEpoch)

	activated, err = service.Activate(ctx, nonce)
	require.NoError(t, err)
	assert.False(t, activated, "every consumed nonce remains idempotent after newer restores")
	var afterReplayEpoch []byte
	require.NoError(t, db.NewRaw(`SELECT security_epoch FROM system_settings WHERE id = 1`).Scan(ctx, &afterReplayEpoch))
	assert.Equal(t, secondEpoch, afterReplayEpoch)

	releaseFence, err := service.Acquire(ctx)
	require.NoError(t, err)
	activationResult := make(chan error, 1)
	go func() {
		_, activationErr := service.Activate(context.Background(), "third-fresh-recovery-nonce-with-more-than-32-bytes")
		activationResult <- activationErr
	}()
	require.Eventually(t, func() bool {
		var waiting bool
		queryErr := db.NewRaw(`SELECT EXISTS (SELECT 1 FROM pg_locks
			WHERE locktype = 'advisory' AND NOT granted)`).Scan(ctx, &waiting)
		return queryErr == nil && waiting
	}, time.Second, 5*time.Millisecond, "Recovery activation never waited for in-flight traffic")
	select {
	case activationErr := <-activationResult:
		require.Failf(t, "Recovery activation crossed the traffic fence", "error: %v", activationErr)
	default:
	}
	releaseFence()
	require.NoError(t, <-activationResult)

	activated, err = service.Activate(ctx, "")
	require.NoError(t, err)
	assert.False(t, activated)
	held, err = service.Held(ctx)
	require.NoError(t, err)
	assert.True(t, held, "a normal restart never clears persisted Recovery hold")

	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	e.GET("/api/me/private-content", func(c echo.Context) error { return c.NoContent(http.StatusOK) })
	RegisterRoutes(e, NewHandler(service, nil))
	e.Use(service.Middleware())
	protectedResponse := httptest.NewRecorder()
	e.ServeHTTP(protectedResponse, httptest.NewRequest(http.MethodGet, "/api/me/private-content", nil))
	assert.Equal(t, http.StatusServiceUnavailable, protectedResponse.Code)
	statusResponse := httptest.NewRecorder()
	e.ServeHTTP(statusResponse, httptest.NewRequest(http.MethodGet, "/api/recovery/status", nil))
	assert.Equal(t, http.StatusOK, statusResponse.Code)
	assert.JSONEq(t, `{"held":true}`, statusResponse.Body.String())

	oldActor := setup.CuratorSession{PersonID: personID, SessionID: oldSessionID}
	_, err = service.Review(ctx, oldActor)
	assert.ErrorIs(t, err, ErrFreshCurator)
	assert.ErrorIs(t, service.Release(ctx, oldActor), ErrFreshCurator)

	freshSessionID := uuid.New()
	freshCredential := sha256.Sum256([]byte("fresh-curator-session"))
	_, err = db.NewRaw(`INSERT INTO sessions
		(id, credential_hash, person_id, recipient_access_generation_id, security_epoch,
		 session_type, idle_expires_at)
		SELECT ?, ?, ?, ?, security_epoch, 'trusted', now() + interval '1 day'
		FROM system_settings WHERE id = 1`, freshSessionID, freshCredential[:], personID, accessID).Exec(ctx)
	require.NoError(t, err)
	freshActor := setup.CuratorSession{PersonID: personID, SessionID: freshSessionID}

	review, err := service.Review(ctx, freshActor)
	require.NoError(t, err)
	assert.True(t, review.Held)
	assert.Equal(t, 1, review.Counts.People)
	assert.Equal(t, 1, review.Counts.RestoredSessions)
	assert.Equal(t, 1, review.Counts.FreshSessions)

	assert.ErrorIs(t, service.Release(ctx, freshActor), ErrReviewRequired)
	require.NoError(t, service.AcknowledgeReview(ctx, freshActor))
	require.NoError(t, service.Release(ctx, freshActor))
	held, err = service.Held(ctx)
	require.NoError(t, err)
	assert.False(t, held)
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM security_audit_events
		WHERE action = 'recovery_hold_released' AND actor_person_id = ? AND session_id = ?`,
		personID, freshSessionID).Scan(ctx, &auditCount))
	assert.Equal(t, 1, auditCount)
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM security_audit_events
		WHERE action = 'recovery_state_reviewed' AND actor_person_id = ? AND session_id = ?`,
		personID, freshSessionID).Scan(ctx, &auditCount))
	assert.Equal(t, 1, auditCount)

	assert.NotContains(t, hex.EncodeToString(nonceHash), nonce)
}
