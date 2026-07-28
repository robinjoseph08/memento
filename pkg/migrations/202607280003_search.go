package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	collection.MustRegister(
		func(ctx context.Context, db *bun.DB) error {
			return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				for _, statement := range []string{
					`CREATE FUNCTION memento_normalize_search_text(value text) RETURNS text
					 LANGUAGE sql IMMUTABLE PARALLEL SAFE
					 SET search_path = public
					 RETURN trim(regexp_replace(public.unaccent(lower(COALESCE(value, ''))), '[^[:alnum:]]+', ' ', 'g'))`,
					`CREATE FUNCTION memento_local_capture_date(value text) RETURNS date
					 LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE
					 SET search_path = public
					 AS $$
					 BEGIN
						IF value IS NULL OR value !~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}' THEN
							RETURN NULL;
						END IF;
						RETURN substring(value FROM 1 FOR 10)::date;
					 EXCEPTION WHEN datetime_field_overflow OR invalid_datetime_format THEN
						RETURN NULL;
					 END
					 $$`,
					`ALTER TABLE events ADD COLUMN place_labels text[] NOT NULL DEFAULT '{}'::text[]`,
					`ALTER TABLE draft_moments ADD COLUMN place_labels text[] NOT NULL DEFAULT '{}'::text[]`,
					`ALTER TABLE published_event_revisions ADD COLUMN place_labels text[] NOT NULL DEFAULT '{}'::text[]`,
					`ALTER TABLE current_published_events ADD COLUMN place_labels text[] NOT NULL DEFAULT '{}'::text[]`,
					`ALTER TABLE published_moments ADD COLUMN place_labels text[] NOT NULL DEFAULT '{}'::text[]`,
					`CREATE TABLE published_attendance (
						published_moment_id uuid NOT NULL REFERENCES published_moments(id) ON DELETE RESTRICT,
						person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						PRIMARY KEY (published_moment_id, person_id)
					)`,
					`CREATE INDEX published_attendance_person_idx ON published_attendance (person_id, published_moment_id)`,
					`ALTER TABLE current_published_events
					 ADD COLUMN attendance_projection_ready boolean NOT NULL DEFAULT false`,
					`INSERT INTO published_attendance (published_moment_id, person_id)
					 SELECT published.id, attendance.person_id
					 FROM published_moments AS published
					 JOIN publications AS publication
					   ON publication.id = published.publication_id
					 JOIN events AS event
					   ON event.id = publication.event_id
					  AND event.current_publication_id = publication.id
					  AND event.version = publication.editable_version
					 JOIN attendance
					   ON attendance.moment_id = published.draft_moment_id`,
					`UPDATE current_published_events AS current
					 SET attendance_projection_ready = true
					 FROM publications AS publication, events AS event
					 WHERE publication.id = current.publication_id
					   AND event.id = current.event_id
					   AND event.current_publication_id = publication.id
					   AND event.version = publication.editable_version`,
					`ALTER TABLE published_search_documents DROP COLUMN search_vector`,
					`ALTER TABLE published_search_documents
						ADD COLUMN capture_date date,
						ADD COLUMN place_text text NOT NULL DEFAULT '',
						ADD COLUMN normalized_search_text text GENERATED ALWAYS AS (
							memento_normalize_search_text(search_text || ' ' || place_text)
						) STORED`,
					`UPDATE published_search_documents AS document
					 SET capture_date = memento_local_capture_date(published.local_date_time)
					 FROM current_published_placements AS placement
					 JOIN published_media_placements AS published
					   ON published.published_moment_id = placement.published_moment_id
					  AND published.media_item_id = placement.media_item_id
					 WHERE document.event_id = placement.event_id
					   AND document.publication_id = placement.publication_id
					   AND document.media_item_id = placement.media_item_id`,
					`ALTER TABLE published_search_documents ADD COLUMN search_vector tsvector GENERATED ALWAYS AS (
						to_tsvector('simple', memento_normalize_search_text(search_text || ' ' || place_text))
					) STORED`,
					`CREATE INDEX published_search_documents_vector_idx ON published_search_documents USING gin (search_vector)`,
					`CREATE INDEX published_search_documents_trigram_idx
					 ON published_search_documents USING gin (normalized_search_text public.gin_trgm_ops)`,
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
					`DROP INDEX published_search_documents_trigram_idx`,
					`ALTER TABLE published_search_documents DROP COLUMN search_vector`,
					`ALTER TABLE published_search_documents DROP COLUMN normalized_search_text, DROP COLUMN place_text, DROP COLUMN capture_date`,
					`ALTER TABLE published_search_documents ADD COLUMN search_vector tsvector GENERATED ALWAYS AS (to_tsvector('simple', search_text)) STORED`,
					`CREATE INDEX published_search_documents_vector_idx ON published_search_documents USING gin (search_vector)`,
					`ALTER TABLE current_published_events DROP COLUMN attendance_projection_ready`,
					`DROP INDEX published_attendance_person_idx`,
					`DROP TABLE published_attendance`,
					`ALTER TABLE published_moments DROP COLUMN place_labels`,
					`ALTER TABLE current_published_events DROP COLUMN place_labels`,
					`ALTER TABLE published_event_revisions DROP COLUMN place_labels`,
					`ALTER TABLE draft_moments DROP COLUMN place_labels`,
					`ALTER TABLE events DROP COLUMN place_labels`,
					`DROP FUNCTION memento_local_capture_date(text)`,
					`DROP FUNCTION memento_normalize_search_text(text)`,
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
