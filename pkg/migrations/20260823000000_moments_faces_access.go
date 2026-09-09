package migrations

import (
	"context"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			// Raw DDL is required for deferred composite foreign keys and the
			// constraint changes that make split Moments possible.
			_, err := tx.ExecContext(ctx, `
ALTER TABLE moments DROP CONSTRAINT moments_album_id_capture_date_key;
ALTER TABLE moments ADD COLUMN title text CHECK (title IS NULL OR length(title) BETWEEN 1 AND 200);
ALTER TABLE moments ADD COLUMN sort_order bigint;
WITH ordered AS (
 SELECT id, row_number() OVER (PARTITION BY album_id ORDER BY capture_date, id) AS position
 FROM moments
)
UPDATE moments SET sort_order = ordered.position FROM ordered WHERE ordered.id = moments.id;
ALTER TABLE moments ALTER COLUMN sort_order SET NOT NULL;
ALTER TABLE moments ADD CONSTRAINT moments_album_sort_order_key UNIQUE (album_id,sort_order);
ALTER TABLE moments DROP COLUMN label;

CREATE TABLE moment_access_decisions (
 moment_id uuid NOT NULL,
 album_id uuid NOT NULL,
 person_id uuid NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
 decision text NOT NULL CHECK (decision IN ('allow','deny')),
 updated_at timestamptz NOT NULL,
 PRIMARY KEY (moment_id,person_id),
 FOREIGN KEY (moment_id,album_id) REFERENCES moments(id,album_id) ON DELETE CASCADE
);
CREATE INDEX moment_access_decisions_album_person_idx ON moment_access_decisions(album_id,person_id);

CREATE TABLE immich_face_links (
 source_id text PRIMARY KEY CHECK (length(source_id) > 0),
 person_id uuid REFERENCES persons(id) ON DELETE CASCADE,
 ignored boolean NOT NULL DEFAULT false,
 updated_at timestamptz NOT NULL,
 CHECK ((person_id IS NOT NULL AND NOT ignored) OR (person_id IS NULL AND ignored)),
 UNIQUE (source_id,person_id)
);
ALTER TABLE persons ADD COLUMN avatar_face_id text;
ALTER TABLE persons ADD CONSTRAINT persons_avatar_face_fk FOREIGN KEY (avatar_face_id,id)
 REFERENCES immich_face_links(source_id,person_id) DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE media_face_associations (
 media_item_id uuid NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
 source_face_id text NOT NULL CHECK (length(source_face_id) > 0),
 source_name text NOT NULL,
 PRIMARY KEY (media_item_id,source_face_id)
);
CREATE INDEX media_face_associations_source_face_idx ON media_face_associations(source_face_id);
CREATE TABLE media_face_refreshes (
 media_item_id uuid PRIMARY KEY REFERENCES media_items(id) ON DELETE CASCADE,
 refreshed_at timestamptz NOT NULL
);
`)
			return errorstack.CaptureContext(ctx, err)
		})
	}, func(ctx context.Context, db *bun.DB) error {
		return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			// Restore generated labels for rollback to the import-only schema.
			_, err := tx.ExecContext(ctx, `
DROP TABLE media_face_refreshes;
DROP TABLE media_face_associations;
ALTER TABLE persons DROP CONSTRAINT persons_avatar_face_fk;
ALTER TABLE persons DROP COLUMN avatar_face_id;
DROP TABLE immich_face_links;
DROP TABLE moment_access_decisions;
ALTER TABLE moments ADD COLUMN label text;
UPDATE moments SET label = to_char(capture_date::date, 'FMMonth FMDD, YYYY');
ALTER TABLE moments ALTER COLUMN label SET NOT NULL;
ALTER TABLE moments DROP CONSTRAINT moments_album_sort_order_key;
ALTER TABLE moments DROP COLUMN sort_order;
ALTER TABLE moments DROP COLUMN title;
UPDATE album_entries entry
 SET moment_id = kept.id
 FROM moments duplicate
 JOIN LATERAL (
  SELECT candidate.id
  FROM moments candidate
  WHERE candidate.album_id = duplicate.album_id AND candidate.capture_date = duplicate.capture_date
  ORDER BY candidate.id
  LIMIT 1
 ) kept ON true
 WHERE entry.moment_id = duplicate.id AND duplicate.id <> kept.id;
DELETE FROM moments duplicate USING moments kept
 WHERE duplicate.album_id = kept.album_id AND duplicate.capture_date = kept.capture_date AND duplicate.id > kept.id;
SET CONSTRAINTS ALL IMMEDIATE;
ALTER TABLE moments ADD CONSTRAINT moments_album_id_capture_date_key UNIQUE (album_id,capture_date);
`)
			return errorstack.CaptureContext(ctx, err)
		})
	})
}
