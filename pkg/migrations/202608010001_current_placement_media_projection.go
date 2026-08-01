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
					ADD COLUMN media_type text,
					ADD COLUMN width integer,
					ADD COLUMN height integer,
					ADD COLUMN local_date_time text,
					ADD COLUMN capture_date date;
				UPDATE current_published_placements AS current
				SET media_type = published.media_type,
					width = published.width,
					height = published.height,
					local_date_time = published.local_date_time,
					capture_date = memento_local_capture_date(published.local_date_time)
				FROM published_media_placements AS published
				WHERE published.published_moment_id = current.published_moment_id
				  AND published.media_item_id = current.media_item_id;
				CREATE INDEX current_placements_chronology_idx
					ON current_published_placements (
						(COALESCE(local_date_time, '')) DESC, media_item_id DESC
					)
					INCLUDE (event_id, publication_id, published_moment_id, media_type, width, height, capture_date)
			`)
			return err
		},
		func(ctx context.Context, db *bun.DB) error {
			_, err := db.ExecContext(ctx, `
				DROP INDEX current_placements_chronology_idx;
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
