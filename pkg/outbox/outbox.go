// Package outbox durably hands transactional domain events to leased jobs.
package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/uptrace/bun"
)

var errLeaseOwnershipLost = errors.New("outbox lease ownership lost")

type event struct {
	ID      int64
	Kind    string
	Payload []byte
}

// Dispatcher claims one event at a time and creates an idempotent job.
type Dispatcher struct {
	db *bun.DB
}

func New(db *bun.DB) *Dispatcher { return &Dispatcher{db: db} }

// Dispatch claims and durably hands off one event. An interrupted claim remains
// reclaimable after its bounded lease expires.
func (d *Dispatcher) Dispatch(ctx context.Context, owner string, lease time.Duration) (bool, error) {
	var claimed event
	err := d.db.NewRaw(`
		WITH candidate AS (
			SELECT id FROM outbox_events
			WHERE delivered_at IS NULL AND available_at <= now()
			  AND (lease_owner IS NULL OR lease_expires_at <= now())
			ORDER BY available_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE outbox_events AS event
		SET lease_owner = ?, lease_expires_at = now() + (? * interval '1 microsecond'), attempts = attempts + 1
		FROM candidate
		WHERE event.id = candidate.id
		RETURNING event.id, event.kind, event.payload
	`, owner, lease.Microseconds()).Scan(ctx, &claimed.ID, &claimed.Kind, &claimed.Payload)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim outbox event: %w", err)
	}

	err = d.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		result, err := tx.NewRaw(`
			INSERT INTO jobs (kind, payload, idempotency_key)
			VALUES (?, ?::jsonb, ?)
			ON CONFLICT (idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
		`, claimed.Kind, string(claimed.Payload), fmt.Sprintf("outbox:%d", claimed.ID)).Exec(ctx)
		if err != nil {
			return err
		}
		if _, err := result.RowsAffected(); err != nil {
			return err
		}
		updated, err := tx.NewRaw(`
			UPDATE outbox_events
			SET delivered_at = now(), lease_owner = NULL, lease_expires_at = NULL
			WHERE id = ? AND delivered_at IS NULL AND lease_owner = ? AND lease_expires_at > now()
		`, claimed.ID, owner).Exec(ctx)
		if err != nil {
			return err
		}
		affected, _ := updated.RowsAffected()
		if affected != 1 {
			return errLeaseOwnershipLost
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("handoff outbox event: %w", err)
	}
	return true, nil
}
