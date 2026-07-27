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
					`ALTER TABLE media_items ADD COLUMN availability text NOT NULL DEFAULT 'current' CHECK (availability IN ('current', 'source_missing'))`,
					`CREATE TABLE media_backings (
						id uuid PRIMARY KEY,
						media_item_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
						immich_asset_id uuid NOT NULL,
						checksum text CHECK (checksum IS NULL OR checksum ~ '^[0-9a-f]{40}$'),
						capture_at text CHECK (capture_at IS NULL OR char_length(capture_at) <= 64),
						filename text NOT NULL DEFAULT '' CHECK (char_length(filename) <= 1024),
						original_path text NOT NULL DEFAULT '' CHECK (char_length(original_path) <= 4096),
						state text NOT NULL DEFAULT 'addition' CHECK (state IN ('addition', 'confirmed')),
						active boolean NOT NULL DEFAULT true,
						linked_at timestamptz NOT NULL,
						ended_at timestamptz,
						confirmed_at timestamptz,
						CHECK (active = (ended_at IS NULL)),
						CHECK ((state = 'confirmed') = (confirmed_at IS NOT NULL))
					)`,
					`CREATE UNIQUE INDEX media_backings_current_item_idx ON media_backings (media_item_id) WHERE active`,
					`CREATE UNIQUE INDEX media_backings_current_asset_idx ON media_backings (immich_asset_id) WHERE active`,
					`CREATE INDEX media_backings_checksum_idx ON media_backings (checksum) WHERE active`,
					`INSERT INTO media_backings (id, media_item_id, immich_asset_id, linked_at)
					 SELECT gen_random_uuid(), id, immich_asset_id, first_seen_at FROM media_items`,
					`CREATE TABLE immich_people_inventory (
						immich_person_id uuid PRIMARY KEY,
						name text NOT NULL DEFAULT '' CHECK (char_length(name) <= 240),
						hidden boolean NOT NULL DEFAULT false,
						present boolean NOT NULL DEFAULT true,
						first_seen_at timestamptz NOT NULL,
						last_seen_at timestamptz NOT NULL,
						CHECK (last_seen_at >= first_seen_at)
					)`,
					`CREATE TABLE immich_person_links (
						person_id uuid PRIMARY KEY REFERENCES people(id) ON DELETE RESTRICT,
						immich_person_id uuid NOT NULL,
						state text NOT NULL CHECK (state IN ('linked', 'needs_review')),
						last_seen_at timestamptz,
						confirmed_at timestamptz NOT NULL,
						confirmed_by_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						version bigint NOT NULL DEFAULT 1 CHECK (version > 0)
					)`,
					`CREATE UNIQUE INDEX immich_person_links_source_idx ON immich_person_links (immich_person_id)`,
					`CREATE INDEX immich_person_links_review_idx ON immich_person_links (state)`,
					`CREATE TABLE immich_face_anchors (
						id uuid PRIMARY KEY,
						person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						immich_face_id uuid NOT NULL,
						immich_asset_id uuid NOT NULL,
						asset_checksum text CHECK (asset_checksum IS NULL OR asset_checksum ~ '^[0-9a-f]{40}$'),
						image_width integer NOT NULL CHECK (image_width >= 0),
						image_height integer NOT NULL CHECK (image_height >= 0),
						x1 integer NOT NULL CHECK (x1 >= 0), y1 integer NOT NULL CHECK (y1 >= 0),
						x2 integer NOT NULL CHECK (x2 >= x1), y2 integer NOT NULL CHECK (y2 >= y1),
						last_linked_immich_person_id uuid,
						last_seen_at timestamptz NOT NULL,
						UNIQUE (person_id, immich_face_id)
					)`,
					`CREATE INDEX immich_face_anchors_person_idx ON immich_face_anchors (person_id, last_seen_at DESC)`,
					`CREATE INDEX immich_face_anchors_asset_idx ON immich_face_anchors (immich_asset_id)`,
					`CREATE TABLE person_repair_candidates (
						id uuid PRIMARY KEY,
						person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						previous_immich_person_id uuid NOT NULL,
						candidate_immich_person_id uuid,
						state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'rejected', 'confirmed')),
						anchor_count integer NOT NULL DEFAULT 0 CHECK (anchor_count >= 0),
						anchor_evidence jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(anchor_evidence) = 'array'),
						conflict_evidence jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(conflict_evidence) = 'array'),
						created_at timestamptz NOT NULL,
						resolved_at timestamptz,
						resolved_by_person_id uuid REFERENCES people(id) ON DELETE RESTRICT,
						CHECK ((state = 'pending') = (resolved_at IS NULL AND resolved_by_person_id IS NULL))
					)`,
					`CREATE UNIQUE INDEX person_repair_candidates_pending_idx ON person_repair_candidates (person_id) WHERE state = 'pending'`,
					`CREATE INDEX person_repair_candidates_state_idx ON person_repair_candidates (state, created_at DESC)`,
					`CREATE TABLE media_repair_candidates (
						id uuid PRIMARY KEY,
						media_item_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
						candidate_media_item_id uuid REFERENCES media_items(id) ON DELETE SET NULL,
						previous_immich_asset_id uuid NOT NULL,
						candidate_immich_asset_id uuid NOT NULL,
						state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'rejected', 'confirmed')),
						conflict_evidence jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(conflict_evidence) = 'array'),
						created_at timestamptz NOT NULL,
						resolved_at timestamptz,
						resolved_by_person_id uuid REFERENCES people(id) ON DELETE RESTRICT,
						CHECK ((state = 'pending') = (resolved_at IS NULL AND resolved_by_person_id IS NULL))
					)`,
					`CREATE UNIQUE INDEX media_repair_candidates_pending_pair_idx ON media_repair_candidates (media_item_id, candidate_immich_asset_id) WHERE state = 'pending'`,
					`CREATE INDEX media_repair_candidates_state_idx ON media_repair_candidates (state, created_at DESC)`,
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
				for _, statement := range []string{
					`DROP TABLE IF EXISTS media_repair_candidates`,
					`DROP TABLE IF EXISTS person_repair_candidates`,
					`DROP TABLE IF EXISTS immich_face_anchors`,
					`DROP TABLE IF EXISTS immich_person_links`,
					`DROP TABLE IF EXISTS immich_people_inventory`,
					`DROP TABLE IF EXISTS media_backings`,
					`ALTER TABLE media_items DROP COLUMN IF EXISTS availability`,
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
