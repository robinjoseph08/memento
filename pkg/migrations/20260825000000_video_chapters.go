package migrations

import (
	"context"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		// DDL adds the optional global video title and the per-Media-Item
		// chapter result that caches ffprobe output by source checksum.
		_, err := db.ExecContext(ctx, `
ALTER TABLE media_items ADD COLUMN video_title text
 CHECK (video_title IS NULL OR (kind = 'VIDEO' AND length(video_title) > 0));
CREATE TABLE media_chapter_results (
 media_item_id uuid PRIMARY KEY REFERENCES media_items(id) ON DELETE CASCADE,
 checksum text NOT NULL,
 status text NOT NULL CHECK (status IN ('queued','processing','complete','failed')),
 message text NOT NULL DEFAULT '',
 chapters jsonb NOT NULL DEFAULT '[]'::jsonb,
 updated_at timestamptz NOT NULL
);
`)
		return errorstack.CaptureContext(ctx, err)
	}, func(ctx context.Context, db *bun.DB) error {
		// DDL reverses the chapter table and the title column.
		_, err := db.ExecContext(ctx, `DROP TABLE media_chapter_results; ALTER TABLE media_items DROP COLUMN video_title;`)
		return errorstack.CaptureContext(ctx, err)
	})
}
