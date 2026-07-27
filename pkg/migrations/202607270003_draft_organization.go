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
					`ALTER TABLE draft_moments DROP CONSTRAINT draft_moments_event_id_proposed_day_key`,
					`ALTER TABLE draft_moments DROP CONSTRAINT draft_moments_proposal_kind_check`,
					`ALTER TABLE draft_moments ADD CONSTRAINT draft_moments_proposal_kind_check CHECK (proposal_kind IN ('local_day', 'merged_days', 'split_day', 'manual'))`,
					`ALTER TABLE draft_moments ADD COLUMN source_days date[] NOT NULL DEFAULT '{}'::date[]`,
					`UPDATE draft_moments SET source_days = ARRAY[proposed_day]`,
					`ALTER TABLE draft_moments ADD COLUMN title text NOT NULL DEFAULT '' CHECK (char_length(title) <= 240)`,
					`ALTER TABLE draft_moments ADD COLUMN cover_media_item_id uuid REFERENCES media_items(id) ON DELETE RESTRICT`,
					`ALTER TABLE draft_moments ADD COLUMN attendance_complete boolean NOT NULL DEFAULT false`,
					`ALTER TABLE draft_moments ADD COLUMN audience_complete boolean NOT NULL DEFAULT false`,
					`ALTER TABLE events ADD COLUMN final_review_complete boolean NOT NULL DEFAULT false`,
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
					`ALTER TABLE events DROP COLUMN final_review_complete`,
					`ALTER TABLE draft_moments DROP COLUMN audience_complete`,
					`ALTER TABLE draft_moments DROP COLUMN attendance_complete`,
					`ALTER TABLE draft_moments DROP COLUMN cover_media_item_id`,
					`ALTER TABLE draft_moments DROP COLUMN title`,
					`ALTER TABLE draft_moments DROP COLUMN source_days`,
					`ALTER TABLE draft_moments DROP CONSTRAINT draft_moments_proposal_kind_check`,
					`UPDATE draft_moments SET proposal_kind = 'local_day'`,
					`ALTER TABLE draft_moments ADD CONSTRAINT draft_moments_proposal_kind_check CHECK (proposal_kind = 'local_day')`,
					`ALTER TABLE draft_moments ADD CONSTRAINT draft_moments_event_id_proposed_day_key UNIQUE (event_id, proposed_day)`,
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
