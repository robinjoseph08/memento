package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	collection.MustRegister(
		func(ctx context.Context, db *bun.DB) error {
			_, err := db.ExecContext(ctx, `
				ALTER TABLE current_published_placements
					ADD COLUMN media_type text NOT NULL DEFAULT 'image' CHECK (media_type IN ('image', 'video')),
					ADD COLUMN width integer CHECK (width IS NULL OR width >= 0),
					ADD COLUMN height integer CHECK (height IS NULL OR height >= 0),
					ADD COLUMN local_date_time text CHECK (local_date_time IS NULL OR (local_date_time <> '' AND char_length(local_date_time) <= 64)),
					ADD COLUMN capture_date date;
				UPDATE current_published_placements AS current
				SET media_type = published.media_type,
					width = published.width,
					height = published.height,
					local_date_time = published.local_date_time,
					capture_date = memento_local_capture_date(published.local_date_time)
				FROM published_media_placements AS published
				WHERE published.published_moment_id = current.published_moment_id
				  AND published.media_item_id = current.media_item_id
			`)
			return err
		},
		func(ctx context.Context, db *bun.DB) error {
			_, err := db.ExecContext(ctx, `
				ALTER TABLE current_published_placements
					DROP COLUMN capture_date,
					DROP COLUMN local_date_time,
					DROP COLUMN height,
					DROP COLUMN width,
					DROP COLUMN media_type
			`)
			return err
		},
	)
}
