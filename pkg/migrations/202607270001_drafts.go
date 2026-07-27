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
					`ALTER TABLE source_albums DROP CONSTRAINT source_albums_disposition_check`,
					`ALTER TABLE source_albums ADD CONSTRAINT source_albums_disposition_check
						CHECK (disposition IN ('unreviewed', 'ignored', 'drafted'))`,
					`ALTER TABLE media_items ALTER COLUMN local_date_time DROP NOT NULL`,
					`ALTER TABLE media_items DROP CONSTRAINT media_items_local_date_time_check`,
					`ALTER TABLE media_items ADD CONSTRAINT media_items_local_date_time_check
						CHECK (local_date_time IS NULL OR (local_date_time <> '' AND char_length(local_date_time) <= 64))`,
					`CREATE TABLE events (
						id uuid PRIMARY KEY,
						lifecycle text NOT NULL DEFAULT 'draft' CHECK (lifecycle IN ('draft', 'published', 'withdrawn')),
						title text NOT NULL CHECK (title <> '' AND char_length(title) <= 240),
						description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 2000),
						grouping_timezone text NOT NULL CHECK (grouping_timezone <> '' AND char_length(grouping_timezone) <= 100),
						version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
						created_at timestamptz NOT NULL DEFAULT now(),
						updated_at timestamptz NOT NULL DEFAULT now()
					)`,
					`CREATE INDEX events_lifecycle_updated_idx ON events (lifecycle, updated_at DESC, id)`,
					`CREATE TABLE event_sources (
						event_id uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
						source_album_id uuid NOT NULL REFERENCES source_albums(id) ON DELETE RESTRICT,
						source_order integer NOT NULL CHECK (source_order >= 0),
						initialized_name text NOT NULL,
						initialized_description text NOT NULL,
						initialized_at timestamptz NOT NULL,
						PRIMARY KEY (event_id, source_album_id),
						UNIQUE (event_id, source_order)
					)`,
					`CREATE INDEX event_sources_source_idx ON event_sources (source_album_id, event_id)`,
					`CREATE TABLE draft_moments (
						id uuid PRIMARY KEY,
						event_id uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
						position integer NOT NULL CHECK (position >= 0),
						proposed_day date NOT NULL,
						grouping_timezone text NOT NULL CHECK (grouping_timezone <> ''),
						proposal_kind text NOT NULL DEFAULT 'local_day' CHECK (proposal_kind = 'local_day'),
						created_at timestamptz NOT NULL DEFAULT now(),
						UNIQUE (event_id, id),
						UNIQUE (event_id, position),
						UNIQUE (event_id, proposed_day)
					)`,
					`CREATE TABLE draft_media_placements (
						event_id uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
						media_item_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
						draft_moment_id uuid,
						position integer NOT NULL CHECK (position >= 0),
						created_at timestamptz NOT NULL DEFAULT now(),
						PRIMARY KEY (event_id, media_item_id),
						UNIQUE (event_id, position),
						FOREIGN KEY (event_id, draft_moment_id) REFERENCES draft_moments(event_id, id) ON DELETE RESTRICT
					)`,
					`CREATE INDEX draft_media_placements_moment_idx
						ON draft_media_placements (draft_moment_id, position) WHERE draft_moment_id IS NOT NULL`,
					`CREATE INDEX draft_media_placements_media_idx ON draft_media_placements (media_item_id, event_id)`,
					`CREATE TABLE loose_items (
						id uuid PRIMARY KEY,
						media_item_id uuid NOT NULL UNIQUE REFERENCES media_items(id) ON DELETE RESTRICT,
						lifecycle text NOT NULL DEFAULT 'draft' CHECK (lifecycle IN ('draft', 'published', 'withdrawn')),
						title text NOT NULL DEFAULT '' CHECK (char_length(title) <= 240),
						description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 2000),
						grouping_timezone text NOT NULL CHECK (grouping_timezone <> '' AND char_length(grouping_timezone) <= 100),
						proposed_day date,
						version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
						created_at timestamptz NOT NULL DEFAULT now(),
						updated_at timestamptz NOT NULL DEFAULT now()
					)`,
					`CREATE INDEX loose_items_lifecycle_updated_idx ON loose_items (lifecycle, updated_at DESC, id)`,
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
					`DROP TABLE IF EXISTS loose_items`,
					`DROP TABLE IF EXISTS draft_media_placements`,
					`DROP TABLE IF EXISTS draft_moments`,
					`DROP TABLE IF EXISTS event_sources`,
					`DROP TABLE IF EXISTS events`,
					`ALTER TABLE media_items DROP CONSTRAINT media_items_local_date_time_check`,
					`ALTER TABLE media_items ADD CONSTRAINT media_items_local_date_time_check
						CHECK (local_date_time <> '' AND char_length(local_date_time) <= 64)`,
					`UPDATE source_albums SET disposition = 'unreviewed' WHERE disposition = 'drafted'`,
					`ALTER TABLE source_albums DROP CONSTRAINT source_albums_disposition_check`,
					`ALTER TABLE source_albums ADD CONSTRAINT source_albums_disposition_check
						CHECK (disposition IN ('unreviewed', 'ignored'))`,
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
