// Package recovery owns the system-wide Recovery hold used after database restoration.
package recovery

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/uptrace/bun"
)

var (
	ErrSetupIncomplete = errors.New("recovery hold requires completed, sole-Curator setup")
	ErrNotHeld         = errors.New("recovery hold is not active")
	ErrFreshCurator    = errors.New("a fresh Curator Session is required")
	ErrReviewRequired  = errors.New("restored state review is required")
)

// StatusResponse is the public, non-sensitive Recovery state.
type StatusResponse struct {
	Held bool `json:"held"`
}

// ReviewCounts summarizes restored authorization state for Curator review.
type ReviewCounts struct {
	People                  int `json:"people"`
	CurrentRecipients       int `json:"current_recipients"`
	CompletedRecipients     int `json:"completed_recipients"`
	SuspendedRecipients     int `json:"suspended_recipients"`
	RevokedGenerations      int `json:"revoked_generations"`
	RestoredSessions        int `json:"restored_sessions"`
	FreshSessions           int `json:"fresh_sessions"`
	AudienceEntitlements    int `json:"audience_entitlements"`
	PublishedEvents         int `json:"published_events"`
	PublishedMediaItems     int `json:"published_media_items"`
	ActiveWithdrawals       int `json:"active_withdrawals"`
	PendingEmailBatches     int `json:"pending_email_batches"`
	ActivePushSubscriptions int `json:"active_push_subscriptions"`
}

// ReviewResponse contains the bounded state a fresh Curator must review before release.
type ReviewResponse struct {
	Held      bool         `json:"held"`
	StartedAt time.Time    `json:"started_at"`
	Counts    ReviewCounts `json:"counts"`
}

// Service atomically activates, reports, and releases Recovery hold.
type Service struct {
	db      *bun.DB
	fenceDB *bun.DB
	random  io.Reader
	now     func() time.Time
}

// Option configures deterministic Service dependencies.
type Option func(*Service)

// WithFenceDB uses a dedicated connection pool for traffic locks so fenced
// handlers never compete with their own application queries.
func WithFenceDB(db *bun.DB) Option {
	return func(service *Service) {
		if db != nil {
			service.fenceDB = db
		}
	}
}

// WithRandom configures the cryptographic epoch source.
func WithRandom(source io.Reader) Option {
	return func(service *Service) {
		if source != nil {
			service.random = source
		}
	}
}

// WithClock configures transition timestamps.
func WithClock(now func() time.Time) Option {
	return func(service *Service) {
		if now != nil {
			service.now = now
		}
	}
}

func New(db *bun.DB, options ...Option) *Service {
	service := &Service{db: db, fenceDB: db, random: rand.Reader, now: time.Now}
	for _, option := range options {
		option(service)
	}
	return service
}

// Activate starts Recovery hold for a fresh nonce. Reusing any consumed nonce is an idempotent no-op.
func (s *Service) Activate(ctx context.Context, nonce string) (bool, error) {
	if nonce == "" {
		return false, nil
	}
	nonceHash := sha256.Sum256([]byte(nonce))
	activated := false
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`SELECT pg_advisory_xact_lock(` + recoveryTrafficLockSQL + `)`).Exec(ctx); err != nil {
			return err
		}
		var setupComplete bool
		var curatorCount, recoverableCuratorCount int
		if err := tx.NewRaw(`SELECT setup_complete,
			(SELECT count(*) FROM person_roles WHERE role = 'curator'),
			(SELECT count(*) FROM person_roles AS role
			 JOIN people AS person ON person.id = role.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
			 JOIN recipient_access_generations AS access ON access.person_id = person.id
			  AND access.is_current AND access.state = 'completed'
			 JOIN recipient_emails AS email ON email.recipient_access_generation_id = access.id AND email.is_current
			 WHERE role.role = 'curator')
			FROM system_settings WHERE id = 1 FOR UPDATE`).
			Scan(ctx, &setupComplete, &curatorCount, &recoverableCuratorCount); err != nil {
			return err
		}
		if !setupComplete || curatorCount != 1 || recoverableCuratorCount != 1 {
			return ErrSetupIncomplete
		}
		var consumed bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM recovery_nonce_history WHERE nonce_hash = ?)`, nonceHash[:]).Scan(ctx, &consumed); err != nil {
			return err
		}
		if consumed {
			return nil
		}
		epoch := make([]byte, 32)
		if _, err := io.ReadFull(s.random, epoch); err != nil {
			return err
		}
		now := s.now().UTC()
		if _, err := tx.NewRaw(`UPDATE system_settings
			SET recovery_hold = true, recovery_nonce_hash = ?, recovery_started_at = ?,
			    recovery_reviewed_at = NULL, recovery_reviewed_by_person_id = NULL,
			    recovery_reviewed_by_session_id = NULL, recovery_released_at = NULL,
			    security_epoch = ?, updated_at = ?
			WHERE id = 1`, nonceHash[:], now, epoch, now).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO recovery_nonce_history (nonce_hash, consumed_at) VALUES (?, ?)`, nonceHash[:], now).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO security_audit_events (action, outcome, metadata, created_at)
			VALUES ('recovery_hold_started', 'success', '{}'::jsonb, ?)`, now).Exec(ctx); err != nil {
			return err
		}
		activated = true
		return nil
	})
	return activated, err
}

// Held reports the persisted Recovery hold state.
func (s *Service) Held(ctx context.Context) (bool, error) {
	var held bool
	err := s.db.NewRaw(`SELECT recovery_hold FROM system_settings WHERE id = 1`).Scan(ctx, &held)
	return held, err
}

// Status returns the public Recovery state.
func (s *Service) Status(ctx context.Context) (StatusResponse, error) {
	held, err := s.Held(ctx)
	return StatusResponse{Held: held}, err
}

// Review returns representative restored authorization counts to a fresh Curator.
func (s *Service) Review(ctx context.Context, actor setup.CuratorSession) (ReviewResponse, error) {
	var response ReviewResponse
	err := s.db.RunInTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(ctx context.Context, tx bun.Tx) error {
		if err := requireFreshCurator(ctx, tx, actor, false); err != nil {
			return err
		}
		if err := tx.NewRaw(`SELECT recovery_hold, recovery_started_at FROM system_settings WHERE id = 1`).
			Scan(ctx, &response.Held, &response.StartedAt); err != nil {
			return err
		}
		if !response.Held {
			return ErrNotHeld
		}
		return tx.NewRaw(`SELECT
			(SELECT count(*) FROM people WHERE archived_at IS NULL AND merged_at IS NULL),
			(SELECT count(*) FROM recipient_access_generations WHERE is_current),
			(SELECT count(*) FROM recipient_access_generations WHERE is_current AND state = 'completed'),
			(SELECT count(*) FROM recipient_access_generations WHERE is_current AND state = 'suspended'),
			(SELECT count(*) FROM recipient_access_generations WHERE state = 'revoked'),
			(SELECT count(*) FROM sessions WHERE security_epoch <> (SELECT security_epoch FROM system_settings WHERE id = 1)),
			(SELECT count(*) FROM sessions WHERE security_epoch = (SELECT security_epoch FROM system_settings WHERE id = 1)),
			(SELECT count(*) FROM current_audience_entitlements),
			(SELECT count(*) FROM current_published_events),
			(SELECT count(DISTINCT media_item_id) FROM current_published_placements),
			(SELECT count(*) FROM content_withdrawals WHERE restored_at IS NULL),
			(SELECT count(*) FROM notification_batches WHERE channel = 'email' AND status = 'pending'),
			(SELECT count(*) FROM push_subscriptions WHERE disabled_at IS NULL)`).Scan(ctx,
			&response.Counts.People, &response.Counts.CurrentRecipients,
			&response.Counts.CompletedRecipients, &response.Counts.SuspendedRecipients,
			&response.Counts.RevokedGenerations, &response.Counts.RestoredSessions,
			&response.Counts.FreshSessions, &response.Counts.AudienceEntitlements,
			&response.Counts.PublishedEvents, &response.Counts.PublishedMediaItems,
			&response.Counts.ActiveWithdrawals, &response.Counts.PendingEmailBatches, &response.Counts.ActivePushSubscriptions)
	})
	return response, err
}

// AcknowledgeReview records the fresh Curator's explicit restored-state review.
func (s *Service) AcknowledgeReview(ctx context.Context, actor setup.CuratorSession) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := requireFreshCurator(ctx, tx, actor, true); err != nil {
			return err
		}
		now := s.now().UTC()
		result, err := tx.NewRaw(`UPDATE system_settings
			SET recovery_reviewed_at = ?, recovery_reviewed_by_person_id = ?,
			    recovery_reviewed_by_session_id = ?, updated_at = ?
			WHERE id = 1 AND recovery_hold`, now, actor.PersonID, actor.SessionID, now).Exec(ctx)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return ErrNotHeld
		}
		metadata := setup.RequestMetadataFromContext(ctx)
		_, err = tx.NewRaw(`INSERT INTO security_audit_events
			(actor_person_id, subject_person_id, action, outcome, client_ip, user_agent, session_id, metadata, created_at)
			VALUES (?, ?, 'recovery_state_reviewed', 'success', NULLIF(?, '')::inet, ?, ?, '{}'::jsonb, ?)`,
			actor.PersonID, actor.PersonID, metadata.ClientIP, metadata.UserAgent, actor.SessionID, now).Exec(ctx)
		return err
	})
}

// Release clears Recovery hold only for the fresh Curator Session that acknowledged review.
func (s *Service) Release(ctx context.Context, actor setup.CuratorSession) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := requireFreshCurator(ctx, tx, actor, true); err != nil {
			return err
		}
		now := s.now().UTC()
		result, err := tx.NewRaw(`UPDATE system_settings SET recovery_hold = false,
			recovery_released_at = ?, updated_at = ?
			WHERE id = 1 AND recovery_hold
			 AND recovery_reviewed_at IS NOT NULL
			 AND recovery_reviewed_by_person_id = ? AND recovery_reviewed_by_session_id = ?`,
			now, now, actor.PersonID, actor.SessionID).Exec(ctx)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			var held bool
			if err := tx.NewRaw(`SELECT recovery_hold FROM system_settings WHERE id = 1`).Scan(ctx, &held); err != nil {
				return err
			}
			if held {
				return ErrReviewRequired
			}
			return ErrNotHeld
		}
		metadata := setup.RequestMetadataFromContext(ctx)
		_, err = tx.NewRaw(`INSERT INTO security_audit_events
			(actor_person_id, subject_person_id, action, outcome, client_ip, user_agent, session_id, metadata, created_at)
			VALUES (?, ?, 'recovery_hold_released', 'success', NULLIF(?, '')::inet, ?, ?, '{}'::jsonb, ?)`,
			actor.PersonID, actor.PersonID, metadata.ClientIP, metadata.UserAgent, actor.SessionID, now).Exec(ctx)
		return err
	})
}

func requireFreshCurator(ctx context.Context, db bun.IDB, actor setup.CuratorSession, lock bool) error {
	lockClause := ""
	if lock {
		lockClause = " FOR UPDATE OF settings, session"
	}
	var sessionID uuid.UUID
	err := db.NewRaw(`SELECT session.id FROM system_settings AS settings
		JOIN sessions AS session ON session.security_epoch = settings.security_epoch
		JOIN recipient_access_generations AS access
		  ON access.id = session.recipient_access_generation_id AND access.person_id = session.person_id
		 AND access.is_current AND access.state = 'completed'
		JOIN people AS person ON person.id = session.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
		JOIN person_roles AS role ON role.person_id = person.id AND role.role = 'curator'
		WHERE settings.id = 1 AND settings.recovery_hold AND session.id = ? AND session.person_id = ?
		  AND session.revoked_at IS NULL
		  AND ((session.session_type = 'trusted' AND session.idle_expires_at > clock_timestamp()) OR
		       (session.session_type = 'public' AND session.absolute_expires_at > clock_timestamp()))`+lockClause,
		actor.SessionID, actor.PersonID).Scan(ctx, &sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrFreshCurator
	}
	return err
}
