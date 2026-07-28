package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	collection.MustRegister(
		func(ctx context.Context, db *bun.DB) error {
			return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				for _, statement := range []string{
					`CREATE TABLE archive_plans (
						id uuid PRIMARY KEY,
						token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
						recipient_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						session_id uuid NOT NULL REFERENCES sessions(id) ON DELETE RESTRICT,
						scope text NOT NULL CHECK (scope IN ('event', 'subset')),
						event_id uuid REFERENCES events(id) ON DELETE RESTRICT,
						name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 180),
						item_count integer NOT NULL CHECK (item_count > 0),
						total_size bigint NOT NULL CHECK (total_size >= 0),
						created_at timestamptz NOT NULL,
						expires_at timestamptz NOT NULL,
						CHECK ((scope = 'event') = (event_id IS NOT NULL)),
						CHECK (expires_at > created_at)
					)`,
					`CREATE INDEX archive_plans_expiry_idx ON archive_plans (expires_at)`,
					`CREATE INDEX archive_plans_actor_idx ON archive_plans (recipient_person_id, session_id, expires_at)`,
					`CREATE TABLE archive_parts (
						id uuid PRIMARY KEY,
						archive_plan_id uuid NOT NULL REFERENCES archive_plans(id) ON DELETE CASCADE,
						part_number integer NOT NULL CHECK (part_number > 0),
						size bigint NOT NULL CHECK (size >= 0),
						consumed_at timestamptz,
						UNIQUE (archive_plan_id, part_number)
					)`,
					`CREATE TABLE archive_part_items (
						archive_part_id uuid NOT NULL REFERENCES archive_parts(id) ON DELETE CASCADE,
						position integer NOT NULL CHECK (position >= 0),
						media_item_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
						event_id uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
						draft_moment_id uuid NOT NULL,
						media_backing_id uuid NOT NULL REFERENCES media_backings(id) ON DELETE RESTRICT,
						immich_asset_id uuid NOT NULL,
						entry_name text NOT NULL CHECK (
							char_length(entry_name) BETWEEN 1 AND 300
							AND entry_name !~ '(^|/)\\.\\.?(/|$)'
							AND entry_name !~ '[\\\\]'
						),
						is_live_photo_companion boolean NOT NULL DEFAULT false,
						PRIMARY KEY (archive_part_id, position),
						UNIQUE (archive_part_id, immich_asset_id)
					)`,
					`CREATE INDEX archive_part_items_media_idx ON archive_part_items (media_item_id)`,
					`INSERT INTO jobs (kind, idempotency_key)
					 VALUES ('cleanup_archive_plans', 'archive-plans-cleanup')
					 ON CONFLICT (idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING`,
				} {
					if _, err := tx.ExecContext(ctx, statement); err != nil {
						return err
					}
				}
				return nil
			})
		},
		func(ctx context.Context, db *bun.DB) error {
			return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				for _, statement := range []string{
					`DELETE FROM jobs WHERE kind = 'cleanup_archive_plans' AND idempotency_key = 'archive-plans-cleanup'`,
					`DROP TABLE archive_part_items`,
					`DROP TABLE archive_parts`,
					`DROP TABLE archive_plans`,
				} {
					if _, err := tx.ExecContext(ctx, statement); err != nil {
						return err
					}
				}
				return nil
			})
		},
	)
}
