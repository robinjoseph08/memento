package migrations

import (
	"context"
	"crypto/rand"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		// The installation's secret for signing media URLs a TV fetches without
		// a cookie. It is generated here, once, so no installation ever runs
		// without one; replacing it only invalidates outstanding URLs. The key
		// comes from Go because PostgreSQL 14 has no random bytes without
		// pgcrypto. DDL stays raw; Bun has no builder for columns and checks.
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return errorstack.Capture(err)
		}
		return errorstack.CaptureContext(ctx, db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE installation ADD COLUMN signing_key bytea`); err != nil {
				return err
			}
			if _, err := tx.NewUpdate().Table("installation").Set("signing_key = ?", key).Where("singleton").Exec(ctx); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `
ALTER TABLE installation
 ALTER COLUMN signing_key SET NOT NULL,
 ADD CONSTRAINT installation_signing_key_length CHECK (octet_length(signing_key) = 32)`)
			return err
		}))
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `ALTER TABLE installation DROP COLUMN signing_key`)
		return errorstack.CaptureContext(ctx, err)
	})
}
