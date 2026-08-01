package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	// Existing date ranges and covers intentionally remain null. Prior Moment data was
	// Curator-private and was never approved as common Event presentation metadata.
	collection.MustRegister(
		func(ctx context.Context, db *bun.DB) error {
			return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				for _, statement := range []string{
					`ALTER TABLE events
					 ADD COLUMN date_start date,
					 ADD COLUMN date_end date,
					 ADD COLUMN selected_cover_media_item_id uuid REFERENCES media_items(id) ON DELETE RESTRICT,
					 ADD CONSTRAINT events_date_range_check CHECK (
					  (date_start IS NULL AND date_end IS NULL) OR
					  (date_start IS NOT NULL AND date_end IS NOT NULL AND date_start <= date_end)
					 )`,
					`ALTER TABLE published_event_revisions
					 ADD COLUMN date_start date,
					 ADD COLUMN date_end date,
					 ADD COLUMN selected_cover_media_item_id uuid REFERENCES media_items(id) ON DELETE RESTRICT,
					 ADD CONSTRAINT published_event_revisions_date_range_check CHECK (
					  (date_start IS NULL AND date_end IS NULL) OR
					  (date_start IS NOT NULL AND date_end IS NOT NULL AND date_start <= date_end)
					 )`,
					`ALTER TABLE current_published_events
					 ADD COLUMN date_start date,
					 ADD COLUMN date_end date,
					 ADD COLUMN selected_cover_media_item_id uuid REFERENCES media_items(id) ON DELETE RESTRICT,
					 ADD CONSTRAINT current_published_events_date_range_check CHECK (
					  (date_start IS NULL AND date_end IS NULL) OR
					  (date_start IS NOT NULL AND date_end IS NOT NULL AND date_start <= date_end)
					 )`,
					`ALTER TABLE event_sources
					 ADD COLUMN initialized_start_at timestamptz,
					 ADD COLUMN initialized_end_at timestamptz`,
					`UPDATE event_sources AS event_source
					 SET initialized_start_at = source.source_start_at,
					     initialized_end_at = source.source_end_at
					 FROM source_albums AS source
					 WHERE source.id = event_source.source_album_id`,
					`ALTER TABLE staged_source_removals
					 ADD COLUMN was_event_cover boolean NOT NULL DEFAULT false`,
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
					`ALTER TABLE staged_source_removals DROP COLUMN was_event_cover`,
					`ALTER TABLE event_sources DROP COLUMN initialized_end_at, DROP COLUMN initialized_start_at`,
					`ALTER TABLE current_published_events DROP CONSTRAINT current_published_events_date_range_check, DROP COLUMN selected_cover_media_item_id, DROP COLUMN date_end, DROP COLUMN date_start`,
					`ALTER TABLE published_event_revisions DROP CONSTRAINT published_event_revisions_date_range_check, DROP COLUMN selected_cover_media_item_id, DROP COLUMN date_end, DROP COLUMN date_start`,
					`ALTER TABLE events DROP CONSTRAINT events_date_range_check, DROP COLUMN selected_cover_media_item_id, DROP COLUMN date_end, DROP COLUMN date_start`,
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
