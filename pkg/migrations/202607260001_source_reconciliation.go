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
					`ALTER TABLE source_albums ADD COLUMN candidate_membership_fingerprint bytea`,
					`ALTER TABLE source_albums ADD COLUMN candidate_membership_passes integer NOT NULL DEFAULT 0 CHECK (candidate_membership_passes >= 0)`,
					`ALTER TABLE source_albums ADD COLUMN last_reconciled_at timestamptz`,
					`ALTER TABLE source_albums ADD CONSTRAINT source_albums_candidate_evidence_check CHECK (
						(candidate_membership_fingerprint IS NULL AND candidate_membership_passes = 0) OR
						(octet_length(candidate_membership_fingerprint) = 32 AND candidate_membership_passes > 0)
					)`,
					`CREATE TABLE media_items (
						id uuid PRIMARY KEY,
						immich_asset_id uuid NOT NULL UNIQUE,
						media_type text NOT NULL CHECK (media_type IN ('image', 'video', 'audio', 'other')),
						width integer CHECK (width IS NULL OR width >= 0),
						height integer CHECK (height IS NULL OR height >= 0),
						local_date_time text NOT NULL CHECK (local_date_time <> '' AND char_length(local_date_time) <= 64),
						first_seen_at timestamptz NOT NULL,
						last_seen_at timestamptz NOT NULL,
						created_at timestamptz NOT NULL DEFAULT now(),
						updated_at timestamptz NOT NULL DEFAULT now(),
						CHECK (last_seen_at >= first_seen_at)
					)`,
					`CREATE TABLE source_album_memberships (
						source_album_id uuid NOT NULL REFERENCES source_albums(id) ON DELETE RESTRICT,
						immich_asset_id uuid NOT NULL,
						media_item_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
						first_seen_at timestamptz NOT NULL,
						last_seen_at timestamptz NOT NULL,
						source_fingerprint bytea NOT NULL CHECK (octet_length(source_fingerprint) = 32),
						PRIMARY KEY (source_album_id, immich_asset_id),
						UNIQUE (source_album_id, media_item_id),
						CHECK (last_seen_at >= first_seen_at)
					)`,
					`CREATE INDEX source_album_memberships_asset_idx ON source_album_memberships (immich_asset_id, source_album_id)`,
					`CREATE TABLE reconciliation_runs (
						id uuid PRIMARY KEY,
						source_album_id uuid NOT NULL REFERENCES source_albums(id) ON DELETE RESTRICT,
						status text NOT NULL CHECK (status IN ('validated', 'unstable', 'failed')),
						diagnostic text CHECK (diagnostic IS NULL OR diagnostic IN ('dependency_unavailable', 'summary_changed', 'pagination_incomplete')),
						before_summary_fingerprint bytea CHECK (before_summary_fingerprint IS NULL OR octet_length(before_summary_fingerprint) = 32),
						after_summary_fingerprint bytea CHECK (after_summary_fingerprint IS NULL OR octet_length(after_summary_fingerprint) = 32),
						membership_fingerprint bytea CHECK (membership_fingerprint IS NULL OR octet_length(membership_fingerprint) = 32),
						stable_passes integer NOT NULL DEFAULT 0 CHECK (stable_passes >= 0),
						addition_count integer NOT NULL DEFAULT 0 CHECK (addition_count >= 0),
						removal_count integer NOT NULL DEFAULT 0 CHECK (removal_count >= 0),
						started_at timestamptz NOT NULL,
						completed_at timestamptz NOT NULL,
						CHECK ((status = 'validated') = (membership_fingerprint IS NOT NULL)),
						CHECK ((status = 'validated') = (diagnostic IS NULL))
					)`,
					`CREATE INDEX reconciliation_runs_album_time_idx ON reconciliation_runs (source_album_id, started_at DESC)`,
					`INSERT INTO jobs (kind, payload, idempotency_key)
					 SELECT 'reconcile_source_album', jsonb_build_object('source_album_id', id), 'source-reconcile:' || id::text
					 FROM source_albums
					 ON CONFLICT (idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING`,
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
			return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				statements := []string{
					`DELETE FROM jobs WHERE kind = 'reconcile_source_album'`,
					`DROP TABLE IF EXISTS reconciliation_runs`,
					`DROP TABLE IF EXISTS source_album_memberships`,
					`DROP TABLE IF EXISTS media_items`,
					`ALTER TABLE source_albums DROP CONSTRAINT IF EXISTS source_albums_candidate_evidence_check`,
					`ALTER TABLE source_albums DROP COLUMN IF EXISTS last_reconciled_at`,
					`ALTER TABLE source_albums DROP COLUMN IF EXISTS candidate_membership_passes`,
					`ALTER TABLE source_albums DROP COLUMN IF EXISTS candidate_membership_fingerprint`,
				}
				for _, statement := range statements {
					if _, err := tx.ExecContext(ctx, statement); err != nil {
						return err
					}
				}
				return nil
			})
		},
	)
}
