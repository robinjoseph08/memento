package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	collection.MustRegister(
		func(ctx context.Context, db *bun.DB) error {
			_, err := db.ExecContext(ctx, `
				CREATE TABLE staged_moment_review_restorations (
					event_id uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
					draft_moment_id uuid NOT NULL,
					base_publication_id uuid NOT NULL REFERENCES publications(id) ON DELETE RESTRICT,
					attendance_complete boolean NOT NULL,
					audience_complete boolean NOT NULL,
					moment_state jsonb NOT NULL,
					review_context jsonb NOT NULL,
					current_snapshot_id uuid REFERENCES audience_snapshots(id) ON DELETE RESTRICT,
					created_at timestamptz NOT NULL,
					PRIMARY KEY (event_id, draft_moment_id)
				)
			`)
			return err
		},
		func(ctx context.Context, db *bun.DB) error {
			_, err := db.ExecContext(ctx, `DROP TABLE staged_moment_review_restorations`)
			return err
		},
	)
}
