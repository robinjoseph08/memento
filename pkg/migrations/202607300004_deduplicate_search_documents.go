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
					`CREATE TABLE published_search_documents_compact (
						event_id uuid NOT NULL CONSTRAINT published_search_documents_compact_event_id_fkey REFERENCES events(id) ON DELETE RESTRICT,
						publication_id uuid NOT NULL CONSTRAINT published_search_documents_compact_publication_id_fkey REFERENCES publications(id) ON DELETE RESTRICT,
						media_item_id uuid NOT NULL CONSTRAINT published_search_documents_compact_media_item_id_fkey REFERENCES media_items(id) ON DELETE RESTRICT,
						search_text text NOT NULL,
						capture_date date,
						place_text text NOT NULL DEFAULT '',
						normalized_search_text text GENERATED ALWAYS AS (
							memento_normalize_search_text(search_text || ' ' || place_text)
						) STORED,
						search_vector tsvector GENERATED ALWAYS AS (
							to_tsvector('simple', memento_normalize_search_text(search_text || ' ' || place_text))
						) STORED,
						CONSTRAINT published_search_documents_compact_pkey PRIMARY KEY (event_id, media_item_id)
					)`,
					`INSERT INTO published_search_documents_compact (
						event_id, publication_id, media_item_id, search_text, capture_date, place_text
					)
					SELECT DISTINCT ON (event_id, media_item_id)
						event_id, publication_id, media_item_id, search_text, capture_date, place_text
					FROM published_search_documents
					ORDER BY event_id, media_item_id, recipient_access_generation_id`,
					`DROP TABLE published_search_documents`,
					`ALTER TABLE published_search_documents_compact RENAME TO published_search_documents`,
					`ALTER TABLE published_search_documents RENAME CONSTRAINT published_search_documents_compact_event_id_fkey TO published_search_documents_event_id_fkey`,
					`ALTER TABLE published_search_documents RENAME CONSTRAINT published_search_documents_compact_publication_id_fkey TO published_search_documents_publication_id_fkey`,
					`ALTER TABLE published_search_documents RENAME CONSTRAINT published_search_documents_compact_media_item_id_fkey TO published_search_documents_media_item_id_fkey`,
					`ALTER TABLE published_search_documents RENAME CONSTRAINT published_search_documents_compact_pkey TO published_search_documents_pkey`,
					`CREATE INDEX published_search_documents_vector_idx ON published_search_documents USING gin (search_vector)`,
					`CREATE INDEX published_search_documents_trigram_idx ON published_search_documents USING gin (normalized_search_text public.gin_trgm_ops)`,
					`CREATE INDEX published_search_documents_publication_idx ON published_search_documents (publication_id, event_id, media_item_id)`,
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
					`CREATE TABLE published_search_documents_expanded (
						event_id uuid NOT NULL CONSTRAINT published_search_documents_expanded_event_id_fkey REFERENCES events(id) ON DELETE RESTRICT,
						publication_id uuid NOT NULL CONSTRAINT published_search_documents_expanded_publication_id_fkey REFERENCES publications(id) ON DELETE RESTRICT,
						recipient_access_generation_id uuid NOT NULL CONSTRAINT published_search_documents_expanded_recipient_access_generation_id_fkey REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						media_item_id uuid NOT NULL CONSTRAINT published_search_documents_expanded_media_item_id_fkey REFERENCES media_items(id) ON DELETE RESTRICT,
						search_text text NOT NULL,
						capture_date date,
						place_text text NOT NULL DEFAULT '',
						normalized_search_text text GENERATED ALWAYS AS (
							memento_normalize_search_text(search_text || ' ' || place_text)
						) STORED,
						search_vector tsvector GENERATED ALWAYS AS (
							to_tsvector('simple', memento_normalize_search_text(search_text || ' ' || place_text))
						) STORED,
						CONSTRAINT published_search_documents_expanded_pkey PRIMARY KEY (event_id, recipient_access_generation_id, media_item_id)
					)`,
					`INSERT INTO published_search_documents_expanded (
						event_id, publication_id, recipient_access_generation_id, media_item_id,
						search_text, capture_date, place_text
					)
					SELECT document.event_id, document.publication_id,
						entitlement.recipient_access_generation_id, document.media_item_id,
						document.search_text, document.capture_date, document.place_text
					FROM published_search_documents AS document
					JOIN current_audience_entitlements AS entitlement
					  ON entitlement.event_id = document.event_id
					 AND entitlement.publication_id = document.publication_id
					 AND entitlement.media_item_id = document.media_item_id`,
					`DROP TABLE published_search_documents`,
					`ALTER TABLE published_search_documents_expanded RENAME TO published_search_documents`,
					`ALTER TABLE published_search_documents RENAME CONSTRAINT published_search_documents_expanded_event_id_fkey TO published_search_documents_event_id_fkey`,
					`ALTER TABLE published_search_documents RENAME CONSTRAINT published_search_documents_expanded_publication_id_fkey TO published_search_documents_publication_id_fkey`,
					`ALTER TABLE published_search_documents RENAME CONSTRAINT published_search_documents_expanded_recipient_access_generation_id_fkey TO published_search_documents_recipient_access_generation_id_fkey`,
					`ALTER TABLE published_search_documents RENAME CONSTRAINT published_search_documents_expanded_media_item_id_fkey TO published_search_documents_media_item_id_fkey`,
					`ALTER TABLE published_search_documents RENAME CONSTRAINT published_search_documents_expanded_pkey TO published_search_documents_pkey`,
					`CREATE INDEX published_search_documents_vector_idx ON published_search_documents USING gin (search_vector)`,
					`CREATE INDEX published_search_documents_trigram_idx ON published_search_documents USING gin (normalized_search_text public.gin_trgm_ops)`,
					`CREATE INDEX published_search_documents_recipient_idx ON published_search_documents (recipient_access_generation_id, event_id)`,
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
