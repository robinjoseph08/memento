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
					`CREATE TABLE family_relationships (
						id uuid PRIMARY KEY,
						relationship_type text NOT NULL CHECK (relationship_type IN ('parent_child', 'sibling', 'partner')),
						person_a_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						person_b_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						partner_status text,
						version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
						created_at timestamptz NOT NULL DEFAULT now(),
						updated_at timestamptz NOT NULL DEFAULT now(),
						archived_at timestamptz,
						CHECK (person_a_id <> person_b_id),
						CHECK (
							(relationship_type = 'partner' AND partner_status IN ('current', 'former')) OR
							(relationship_type <> 'partner' AND partner_status IS NULL)
						),
						CHECK (relationship_type = 'parent_child' OR person_a_id < person_b_id)
					)`,
					`CREATE UNIQUE INDEX family_relationships_active_unique_idx
						ON family_relationships (relationship_type, person_a_id, person_b_id)
						WHERE archived_at IS NULL`,
					`CREATE INDEX family_relationships_active_a_idx ON family_relationships (person_a_id)
						WHERE archived_at IS NULL`,
					`CREATE INDEX family_relationships_active_b_idx ON family_relationships (person_b_id)
						WHERE archived_at IS NULL`,
					`CREATE INDEX family_relationships_current_partner_a_idx ON family_relationships (person_a_id, person_b_id)
						WHERE archived_at IS NULL AND relationship_type = 'partner' AND partner_status = 'current'`,
					`CREATE INDEX family_relationships_current_partner_b_idx ON family_relationships (person_b_id, person_a_id)
						WHERE archived_at IS NULL AND relationship_type = 'partner' AND partner_status = 'current'`,
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
			_, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS family_relationships`)
			return err
		},
	)
}
