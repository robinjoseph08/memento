// Package placementlock coordinates access to the current published placement snapshot.
package placementlock

import (
	"context"

	"github.com/uptrace/bun"
)

// Key is the stable PostgreSQL advisory lock identity for current placements.
const Key = "events:current_published_placements"

// Mode selects shared access or exclusive placement mutation.
type Mode int

const (
	Exclusive Mode = iota
	Shared
)

// Acquire holds the requested placement lock until the transaction ends.
func Acquire(ctx context.Context, tx bun.Tx, mode Mode) error {
	query := `SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`
	if mode == Shared {
		query = `SELECT pg_advisory_xact_lock_shared(hashtextextended(?, 0))`
	}
	_, err := tx.NewRaw(query, Key).Exec(ctx)
	return err
}
