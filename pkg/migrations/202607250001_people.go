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
					`CREATE FUNCTION memento_normalize_person_name(value text) RETURNS text
					 LANGUAGE sql IMMUTABLE PARALLEL SAFE
					 RETURN trim(regexp_replace(lower(public.unaccent('public.unaccent', value)), '\s+', ' ', 'g'))`,
					`ALTER TABLE people ADD COLUMN version bigint NOT NULL DEFAULT 1 CHECK (version > 0)`,
					`ALTER TABLE people ADD COLUMN merged_into_person_id uuid REFERENCES people(id) ON DELETE RESTRICT`,
					`ALTER TABLE people ADD COLUMN merged_at timestamptz`,
					`ALTER TABLE people ADD CONSTRAINT people_merge_state_check CHECK (
						(merged_into_person_id IS NULL AND merged_at IS NULL) OR
						(merged_into_person_id IS NOT NULL AND merged_at IS NOT NULL AND merged_into_person_id <> id)
					)`,
					`CREATE INDEX people_search_name_idx ON people USING gin
						(memento_normalize_person_name(display_name || ' ' || sort_name) public.gin_trgm_ops)`,
					`CREATE INDEX people_current_sort_idx ON people
						(memento_normalize_person_name(sort_name), id) WHERE archived_at IS NULL AND merged_at IS NULL`,
					`CREATE INDEX people_merged_into_idx ON people (merged_into_person_id) WHERE merged_into_person_id IS NOT NULL`,
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
					`DROP INDEX IF EXISTS people_merged_into_idx`,
					`DROP INDEX IF EXISTS people_current_sort_idx`,
					`DROP INDEX IF EXISTS people_search_name_idx`,
					`ALTER TABLE people DROP CONSTRAINT IF EXISTS people_merge_state_check`,
					`ALTER TABLE people DROP COLUMN IF EXISTS merged_at`,
					`ALTER TABLE people DROP COLUMN IF EXISTS merged_into_person_id`,
					`ALTER TABLE people DROP COLUMN IF EXISTS version`,
					`DROP FUNCTION IF EXISTS memento_normalize_person_name(text)`,
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
