package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	collection.MustRegister(
		func(ctx context.Context, db *bun.DB) error {
			return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				statements := []string{
					`CREATE TABLE source_albums (
						id uuid PRIMARY KEY,
						immich_album_id uuid NOT NULL UNIQUE,
						name text NOT NULL CHECK (name <> '' AND char_length(name) <= 240),
						description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 2000),
						asset_count integer NOT NULL CHECK (asset_count >= 0),
						source_created_at timestamptz NOT NULL,
						source_updated_at timestamptz NOT NULL,
						source_start_at timestamptz,
						source_end_at timestamptz,
						source_last_modified_asset_at timestamptz,
						disposition text NOT NULL DEFAULT 'unreviewed' CHECK (disposition IN ('unreviewed', 'ignored')),
						version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
						ignored_at timestamptz,
						first_seen_at timestamptz NOT NULL,
						last_seen_at timestamptz NOT NULL,
						source_missing boolean NOT NULL DEFAULT false,
						missing_since timestamptz,
						source_fingerprint bytea NOT NULL CHECK (octet_length(source_fingerprint) = 32),
						next_reconciliation_at timestamptz NOT NULL,
						created_at timestamptz NOT NULL DEFAULT now(),
						updated_at timestamptz NOT NULL DEFAULT now(),
						CHECK ((disposition = 'ignored') = (ignored_at IS NOT NULL)),
						CHECK (source_missing = (missing_since IS NOT NULL)),
						CHECK (last_seen_at >= first_seen_at)
					)`,
					`CREATE INDEX source_albums_disposition_idx ON source_albums (disposition, first_seen_at DESC, id)`,
					`CREATE INDEX source_albums_missing_idx ON source_albums (source_missing, last_seen_at)`,
					`CREATE INDEX source_albums_reconciliation_due_idx ON source_albums (next_reconciliation_at, id)`,
				}
				for _, statement := range statements {
					if _, err := tx.ExecContext(ctx, statement); err != nil {
						return err
					}
				}
				return nil
			})
		},
		func(ctx context.Context, db *bun.DB) error {
			_, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS source_albums`)
			return err
		},
	)
}
