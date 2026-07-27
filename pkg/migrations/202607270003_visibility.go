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
					`CREATE TABLE visibility_circles (
						id uuid PRIMARY KEY,
						name text NOT NULL CHECK (name <> '' AND char_length(name) <= 120),
						version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
						created_at timestamptz NOT NULL DEFAULT now(),
						updated_at timestamptz NOT NULL DEFAULT now(),
						archived_at timestamptz
					)`,
					`CREATE UNIQUE INDEX visibility_circles_current_name_idx ON visibility_circles (lower(name)) WHERE archived_at IS NULL`,
					`CREATE TABLE visibility_circle_members (
						circle_id uuid NOT NULL REFERENCES visibility_circles(id) ON DELETE RESTRICT,
						person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						created_at timestamptz NOT NULL DEFAULT now(),
						PRIMARY KEY (circle_id, person_id)
					)`,
					`CREATE INDEX visibility_circle_members_person_idx ON visibility_circle_members (person_id, circle_id)`,
					`CREATE TABLE interest_list_entries (
						recipient_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						selected_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						state text NOT NULL CHECK (state IN ('active', 'ineligible')),
						chosen_at timestamptz NOT NULL,
						updated_at timestamptz NOT NULL,
						PRIMARY KEY (recipient_person_id, selected_person_id),
						CHECK (recipient_person_id <> selected_person_id)
					)`,
					`CREATE INDEX interest_list_entries_active_recipient_idx ON interest_list_entries (recipient_person_id, selected_person_id) WHERE state = 'active'`,
					`CREATE INDEX interest_list_entries_selected_idx ON interest_list_entries (selected_person_id, recipient_person_id)`,
					`CREATE TABLE interest_list_history (
						id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
						recipient_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						selected_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						actor_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						action text NOT NULL CHECK (action IN ('selected', 'deselected', 'deactivated', 'moved')),
						result text NOT NULL CHECK (result IN ('active', 'deselected', 'ineligible')),
						reason text NOT NULL CHECK (reason IN ('explicit', 'visibility_lost', 'person_merged')),
						created_at timestamptz NOT NULL DEFAULT now()
					)`,
					`CREATE INDEX interest_list_history_recipient_time_idx ON interest_list_history (recipient_person_id, created_at DESC, id DESC)`,
					`CREATE INDEX interest_list_history_selected_time_idx ON interest_list_history (selected_person_id, created_at DESC, id DESC)`,
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
					`DROP TABLE IF EXISTS interest_list_history`,
					`DROP TABLE IF EXISTS interest_list_entries`,
					`DROP TABLE IF EXISTS visibility_circle_members`,
					`DROP TABLE IF EXISTS visibility_circles`,
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
