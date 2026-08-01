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
					`ALTER TABLE publications ALTER COLUMN event_id DROP NOT NULL`,
					`ALTER TABLE publications ADD COLUMN loose_item_id uuid REFERENCES loose_items(id) ON DELETE RESTRICT`,
					`ALTER TABLE publications ADD CONSTRAINT publications_exactly_one_target_check
						CHECK ((event_id IS NOT NULL)::integer + (loose_item_id IS NOT NULL)::integer = 1)`,
					`CREATE UNIQUE INDEX publications_loose_revision_idx ON publications (loose_item_id, revision) WHERE loose_item_id IS NOT NULL`,
					`CREATE UNIQUE INDEX publications_loose_editable_version_idx ON publications (loose_item_id, editable_version) WHERE loose_item_id IS NOT NULL`,
					`ALTER TABLE loose_items ADD COLUMN place_labels text[] NOT NULL DEFAULT '{}'::text[]`,
					`ALTER TABLE loose_items ADD COLUMN current_publication_id uuid REFERENCES publications(id) ON DELETE RESTRICT`,
					`CREATE TABLE published_loose_item_revisions (
						publication_id uuid PRIMARY KEY REFERENCES publications(id) ON DELETE RESTRICT,
						loose_item_id uuid NOT NULL REFERENCES loose_items(id) ON DELETE RESTRICT,
						media_item_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
						audience_snapshot_id uuid NOT NULL REFERENCES audience_snapshots(id) ON DELETE RESTRICT,
						title text NOT NULL,
						description text NOT NULL,
						grouping_timezone text NOT NULL,
						proposed_day date,
						place_labels text[] NOT NULL DEFAULT '{}'::text[],
						media_type text NOT NULL CHECK (media_type IN ('image', 'video')),
						width integer CHECK (width IS NULL OR width >= 0),
						height integer CHECK (height IS NULL OR height >= 0),
						local_date_time text,
						created_at timestamptz NOT NULL,
						UNIQUE (loose_item_id, publication_id)
					)`,
					`CREATE INDEX published_loose_history_media_idx ON published_loose_item_revisions (media_item_id, publication_id)`,
					`CREATE TABLE published_loose_audience_entries (
						publication_id uuid NOT NULL REFERENCES publications(id) ON DELETE RESTRICT,
						recipient_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						PRIMARY KEY (publication_id, recipient_access_generation_id)
					)`,
					`CREATE INDEX published_loose_audience_generation_idx ON published_loose_audience_entries (recipient_access_generation_id, publication_id)`,
					`CREATE TABLE current_published_loose_items (
						loose_item_id uuid PRIMARY KEY REFERENCES loose_items(id) ON DELETE RESTRICT,
						publication_id uuid NOT NULL UNIQUE REFERENCES publications(id) ON DELETE RESTRICT,
						media_item_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
						title text NOT NULL,
						description text NOT NULL,
						grouping_timezone text NOT NULL,
						proposed_day date,
						place_labels text[] NOT NULL DEFAULT '{}'::text[],
						media_type text NOT NULL CHECK (media_type IN ('image', 'video')),
						width integer CHECK (width IS NULL OR width >= 0),
						height integer CHECK (height IS NULL OR height >= 0),
						local_date_time text,
						committed_at timestamptz NOT NULL
					)`,
					`CREATE INDEX current_published_loose_media_idx ON current_published_loose_items (media_item_id, loose_item_id)`,
					`CREATE TABLE current_loose_item_entitlements (
						loose_item_id uuid NOT NULL REFERENCES loose_items(id) ON DELETE RESTRICT,
						publication_id uuid NOT NULL REFERENCES publications(id) ON DELETE RESTRICT,
						recipient_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						media_item_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
						PRIMARY KEY (loose_item_id, recipient_access_generation_id)
					)`,
					`CREATE INDEX current_loose_entitlements_generation_idx ON current_loose_item_entitlements (recipient_access_generation_id, media_item_id, loose_item_id)`,
					`CREATE INDEX current_loose_entitlements_media_idx ON current_loose_item_entitlements (media_item_id, recipient_access_generation_id)`,
					`CREATE TABLE published_loose_search_documents (
						loose_item_id uuid PRIMARY KEY REFERENCES loose_items(id) ON DELETE RESTRICT,
						publication_id uuid NOT NULL REFERENCES publications(id) ON DELETE RESTRICT,
						media_item_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
						search_text text NOT NULL,
						capture_date date,
						place_text text NOT NULL DEFAULT '',
						normalized_search_text text GENERATED ALWAYS AS (memento_normalize_search_text(search_text || ' ' || place_text)) STORED,
						search_vector tsvector GENERATED ALWAYS AS (to_tsvector('simple', memento_normalize_search_text(search_text || ' ' || place_text))) STORED
					)`,
					`CREATE INDEX published_loose_search_vector_idx ON published_loose_search_documents USING gin (search_vector)`,
					`CREATE INDEX published_loose_search_trigram_idx ON published_loose_search_documents USING gin (normalized_search_text public.gin_trgm_ops)`,
					`CREATE TABLE loose_staged_updates (
						id uuid PRIMARY KEY,
						loose_item_id uuid NOT NULL UNIQUE REFERENCES loose_items(id) ON DELETE RESTRICT,
						base_publication_id uuid NOT NULL REFERENCES publications(id) ON DELETE RESTRICT,
						net_changes jsonb NOT NULL,
						created_at timestamptz NOT NULL,
						updated_at timestamptz NOT NULL
					)`,
					`ALTER TABLE loose_items ADD COLUMN current_staged_update_id uuid UNIQUE REFERENCES loose_staged_updates(id) ON DELETE RESTRICT`,
					`CREATE TABLE loose_publication_preview_audit_events (
						id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
						loose_item_id uuid NOT NULL REFERENCES loose_items(id) ON DELETE RESTRICT,
						publication_id uuid REFERENCES publications(id) ON DELETE RESTRICT,
						editable_version bigint NOT NULL CHECK (editable_version > 0),
						actor_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						recipient_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						created_at timestamptz NOT NULL
					)`,
					`CREATE INDEX loose_preview_audit_item_idx ON loose_publication_preview_audit_events (loose_item_id, created_at DESC)`,
					`ALTER TABLE content_withdrawals DROP CONSTRAINT content_withdrawals_target_kind_check`,
					`ALTER TABLE content_withdrawals ADD CONSTRAINT content_withdrawals_target_kind_check CHECK (target_kind IN ('event', 'moment', 'media', 'loose_item'))`,
					`CREATE VIEW current_media_entitlements AS
					 SELECT 'event'::text AS origin_kind, entitlement.event_id AS origin_id,
					        entitlement.publication_id, entitlement.recipient_person_id,
					        entitlement.recipient_access_generation_id, entitlement.media_item_id,
					        placement.position
					 FROM current_audience_entitlements AS entitlement
					 JOIN events AS event ON event.id = entitlement.event_id AND event.lifecycle = 'published'
					 JOIN current_published_placements AS placement
					   ON placement.event_id = entitlement.event_id AND placement.publication_id = entitlement.publication_id
					  AND placement.media_item_id = entitlement.media_item_id
					 JOIN published_moments AS moment ON moment.id = placement.published_moment_id
					 WHERE NOT content_is_withdrawn(placement.event_id, moment.draft_moment_id, placement.media_item_id)
					 UNION ALL
					 SELECT 'loose_item', entitlement.loose_item_id, entitlement.publication_id,
					        entitlement.recipient_person_id, entitlement.recipient_access_generation_id, entitlement.media_item_id,
					        0 AS position
					 FROM current_loose_item_entitlements AS entitlement
					 JOIN loose_items AS loose ON loose.id = entitlement.loose_item_id AND loose.lifecycle = 'published'
					 JOIN current_published_loose_items AS current
					   ON current.loose_item_id = entitlement.loose_item_id AND current.publication_id = entitlement.publication_id
					 WHERE NOT EXISTS (SELECT 1 FROM content_withdrawals AS withdrawal
					   WHERE withdrawal.restored_at IS NULL AND ((withdrawal.target_kind = 'loose_item' AND withdrawal.target_id = entitlement.loose_item_id)
					    OR (withdrawal.target_kind = 'media' AND withdrawal.target_id = entitlement.media_item_id)))`,
					`CREATE OR REPLACE FUNCTION memento_project_publication_activity() RETURNS trigger LANGUAGE plpgsql AS $$
					DECLARE target_id uuid; target_title text; target_kind text; revision bigint;
					BEGIN
					 SELECT publication.revision, COALESCE(publication.event_id, publication.loose_item_id),
					  CASE WHEN publication.event_id IS NOT NULL THEN 'event' ELSE 'loose_item' END,
					  COALESCE(event.title, loose.title)
					 INTO revision, target_id, target_kind, target_title
					 FROM publications AS publication
					 LEFT JOIN events AS event ON event.id = publication.event_id
					 LEFT JOIN loose_items AS loose ON loose.id = publication.loose_item_id
					 WHERE publication.id = NEW.publication_id;
					 INSERT INTO curator_activity_items (actor_person_id, action, created_at, source_kind, source_id, version, category, target_kind, target_id, target_label)
					 VALUES (NEW.actor_person_id, CASE WHEN target_kind = 'event' THEN 'event_published' ELSE 'loose_item_published' END,
					  NEW.created_at, 'publication', NEW.publication_id::text, 'publication ' || revision::text,
					  'publication', target_kind, target_id::text, COALESCE(NULLIF(target_title, ''), 'Loose item'))
					 ON CONFLICT (source_kind, source_id, version) DO NOTHING;
					 RETURN NEW;
					END $$`,
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
					`DO $$ BEGIN
						IF EXISTS (SELECT 1 FROM publications WHERE loose_item_id IS NOT NULL) THEN
							RAISE EXCEPTION 'cannot roll back Loose item Publication schema after Publication history exists';
						END IF;
					END $$`,
					`DROP VIEW current_media_entitlements`,
					`CREATE OR REPLACE FUNCTION memento_project_publication_activity() RETURNS trigger LANGUAGE plpgsql AS $$
					DECLARE event_id uuid; event_title text; revision bigint;
					BEGIN
						SELECT publication.event_id, event.title, publication.revision
						INTO event_id, event_title, revision
						FROM publications AS publication JOIN events AS event ON event.id = publication.event_id
						WHERE publication.id = NEW.publication_id;
						INSERT INTO curator_activity_items
							(actor_person_id, action, created_at, source_kind, source_id, version, category,
							 target_kind, target_id, target_label)
						VALUES (NEW.actor_person_id, 'event_published', NEW.created_at, 'publication', NEW.publication_id::text,
							'publication ' || revision::text, 'publication', 'event', event_id::text, event_title)
						ON CONFLICT (source_kind, source_id, version) DO NOTHING;
						RETURN NEW;
					END $$`,
					`ALTER TABLE content_withdrawals DROP CONSTRAINT content_withdrawals_target_kind_check`,
					`ALTER TABLE content_withdrawals ADD CONSTRAINT content_withdrawals_target_kind_check CHECK (target_kind IN ('event', 'moment', 'media'))`,
					`DROP TABLE loose_publication_preview_audit_events`,
					`ALTER TABLE loose_items DROP COLUMN current_staged_update_id`,
					`DROP TABLE loose_staged_updates`,
					`DROP TABLE published_loose_search_documents`,
					`DROP TABLE current_loose_item_entitlements`,
					`DROP TABLE current_published_loose_items`,
					`DROP TABLE published_loose_audience_entries`,
					`DROP TABLE published_loose_item_revisions`,
					`ALTER TABLE loose_items DROP COLUMN current_publication_id, DROP COLUMN place_labels`,
					`DROP INDEX publications_loose_editable_version_idx`,
					`DROP INDEX publications_loose_revision_idx`,
					`ALTER TABLE publications DROP CONSTRAINT publications_exactly_one_target_check`,
					`ALTER TABLE publications DROP COLUMN loose_item_id`,
					`ALTER TABLE publications ALTER COLUMN event_id SET NOT NULL`,
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
