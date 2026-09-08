package migrations

import (
	"context"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			// Raw DDL expresses deferred composite foreign keys, which Bun's table builder cannot.
			_, err := tx.ExecContext(ctx, `
CREATE TABLE albums (
 id uuid PRIMARY KEY,
 source_id text NOT NULL UNIQUE CHECK (length(source_id) > 0),
 title text NOT NULL CHECK (length(title) > 0),
 description text NOT NULL,
 published_at timestamptz,
 import_status text NOT NULL CHECK (import_status IN ('queued','processing','complete','interrupted','failed')),
 import_message text NOT NULL DEFAULT '',
 import_processed integer NOT NULL DEFAULT 0 CHECK (import_processed >= 0),
 import_total integer NOT NULL CHECK (import_total >= 0),
 import_updated_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL,
 CHECK (published_at IS NULL OR import_status = 'complete')
);
CREATE TABLE media_items (
 id uuid PRIMARY KEY,
 source_id text NOT NULL UNIQUE CHECK (length(source_id) > 0),
 checksum text NOT NULL,
 filename text NOT NULL,
 kind text NOT NULL CHECK (kind IN ('IMAGE','VIDEO')),
 captured_at timestamp without time zone NOT NULL,
 source_created_at timestamptz NOT NULL,
 source_updated_at timestamptz NOT NULL,
 offline boolean NOT NULL,
 trashed boolean NOT NULL,
 width integer,
 height integer,
 duration integer,
 thumbhash text,
 live_photo_video_id text,
 source_stack_id text,
 source_stack_primary_id text,
 source_stack_count integer,
 exif jsonb,
 content_version text NOT NULL
);
CREATE TABLE moments (
 id uuid PRIMARY KEY,
 album_id uuid NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
 capture_date text NOT NULL CHECK (capture_date ~ '^\d{4}-\d{2}-\d{2}$'),
 label text NOT NULL,
 cover_entry_id uuid NOT NULL,
 UNIQUE (id,album_id),
 UNIQUE (album_id,capture_date)
);
CREATE TABLE album_entries (
 id uuid PRIMARY KEY,
 album_id uuid NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
 media_item_id uuid NOT NULL REFERENCES media_items(id),
 moment_id uuid,
 removed_at timestamptz,
 CHECK ((removed_at IS NULL) = (moment_id IS NOT NULL)),
 UNIQUE (album_id,media_item_id),
 UNIQUE (id,album_id,moment_id),
 FOREIGN KEY (moment_id,album_id) REFERENCES moments(id,album_id) DEFERRABLE INITIALLY DEFERRED
);
ALTER TABLE moments ADD CONSTRAINT moments_cover_entry_fk FOREIGN KEY (cover_entry_id,album_id,id)
 REFERENCES album_entries(id,album_id,moment_id) DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX album_entries_media_item_idx ON album_entries(media_item_id);
CREATE INDEX album_entries_moment_idx ON album_entries(moment_id);
`)
			return errorstack.CaptureContext(ctx, err)
		})
	}, func(ctx context.Context, db *bun.DB) error {
		// Break the circular cover foreign key before dropping the tables.
		_, err := db.ExecContext(ctx, `ALTER TABLE moments DROP CONSTRAINT moments_cover_entry_fk; DROP TABLE album_entries; DROP TABLE moments; DROP TABLE media_items; DROP TABLE albums;`)
		return errorstack.CaptureContext(ctx, err)
	})
}
