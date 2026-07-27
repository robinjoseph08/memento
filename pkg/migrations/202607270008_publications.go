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
					`CREATE TABLE publications (
						id uuid PRIMARY KEY,
						event_id uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
						revision bigint NOT NULL CHECK (revision > 0),
						editable_version bigint NOT NULL CHECK (editable_version > 0),
						prior_publication_id uuid REFERENCES publications(id) ON DELETE RESTRICT,
						published_by_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						notify_recipients boolean NOT NULL,
						committed_at timestamptz NOT NULL,
						UNIQUE (event_id, revision),
						CHECK ((revision = 1) = (prior_publication_id IS NULL))
					)`,
					`CREATE INDEX publications_committed_idx ON publications (committed_at DESC, id)`,
					`ALTER TABLE events ADD COLUMN current_publication_id uuid REFERENCES publications(id) ON DELETE RESTRICT`,
					`CREATE TABLE published_event_revisions (
						publication_id uuid PRIMARY KEY REFERENCES publications(id) ON DELETE RESTRICT,
						event_id uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
						title text NOT NULL,
						description text NOT NULL,
						grouping_timezone text NOT NULL,
						created_at timestamptz NOT NULL
					)`,
					`CREATE TABLE published_moments (
						id uuid PRIMARY KEY,
						publication_id uuid NOT NULL REFERENCES publications(id) ON DELETE RESTRICT,
						draft_moment_id uuid NOT NULL,
						audience_snapshot_id uuid NOT NULL REFERENCES audience_snapshots(id) ON DELETE RESTRICT,
						position integer NOT NULL CHECK (position >= 0),
						title text NOT NULL,
						proposed_day date NOT NULL,
						cover_media_item_id uuid REFERENCES media_items(id) ON DELETE RESTRICT,
						UNIQUE (publication_id, position),
						UNIQUE (publication_id, draft_moment_id)
					)`,
					`CREATE TABLE published_media_placements (
						published_moment_id uuid NOT NULL REFERENCES published_moments(id) ON DELETE RESTRICT,
						media_item_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
						position integer NOT NULL CHECK (position >= 0),
						PRIMARY KEY (published_moment_id, media_item_id),
						UNIQUE (published_moment_id, position)
					)`,
					`CREATE INDEX published_media_history_idx ON published_media_placements (media_item_id, published_moment_id)`,
					`CREATE TABLE audience_entries (
						published_moment_id uuid NOT NULL REFERENCES published_moments(id) ON DELETE RESTRICT,
						recipient_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						PRIMARY KEY (published_moment_id, recipient_access_generation_id)
					)`,
					`CREATE INDEX audience_entries_generation_idx ON audience_entries (recipient_access_generation_id, published_moment_id)`,
					`CREATE TABLE current_published_events (
						event_id uuid PRIMARY KEY REFERENCES events(id) ON DELETE RESTRICT,
						publication_id uuid NOT NULL UNIQUE REFERENCES publications(id) ON DELETE RESTRICT,
						title text NOT NULL,
						description text NOT NULL,
						grouping_timezone text NOT NULL,
						committed_at timestamptz NOT NULL
					)`,
					`CREATE TABLE current_published_placements (
						event_id uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
						publication_id uuid NOT NULL REFERENCES publications(id) ON DELETE RESTRICT,
						published_moment_id uuid NOT NULL REFERENCES published_moments(id) ON DELETE RESTRICT,
						media_item_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
						position integer NOT NULL CHECK (position >= 0),
						PRIMARY KEY (event_id, media_item_id),
						UNIQUE (event_id, position)
					)`,
					`CREATE INDEX current_placements_media_idx ON current_published_placements (media_item_id, event_id)`,
					`CREATE TABLE current_audience_entitlements (
						event_id uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
						publication_id uuid NOT NULL REFERENCES publications(id) ON DELETE RESTRICT,
						recipient_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						media_item_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
						PRIMARY KEY (event_id, recipient_access_generation_id, media_item_id)
					)`,
					`CREATE INDEX current_entitlements_recipient_idx ON current_audience_entitlements (recipient_access_generation_id, event_id, media_item_id)`,
					`CREATE INDEX current_entitlements_media_idx ON current_audience_entitlements (media_item_id, recipient_access_generation_id)`,
					`CREATE TABLE current_recipient_event_covers (
						event_id uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						media_item_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
						PRIMARY KEY (event_id, recipient_access_generation_id)
					)`,
					`CREATE TABLE new_for_you_entries (
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						publication_id uuid NOT NULL REFERENCES publications(id) ON DELETE RESTRICT,
						seen_at timestamptz,
						PRIMARY KEY (recipient_access_generation_id, publication_id)
					)`,
					`CREATE INDEX new_for_you_unseen_idx ON new_for_you_entries (recipient_access_generation_id, publication_id) WHERE seen_at IS NULL`,
					`CREATE TABLE published_search_documents (
						event_id uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
						publication_id uuid NOT NULL REFERENCES publications(id) ON DELETE RESTRICT,
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						media_item_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
						search_text text NOT NULL,
						search_vector tsvector GENERATED ALWAYS AS (to_tsvector('simple', search_text)) STORED,
						PRIMARY KEY (event_id, recipient_access_generation_id, media_item_id)
					)`,
					`CREATE INDEX published_search_documents_vector_idx ON published_search_documents USING gin (search_vector)`,
					`CREATE INDEX published_search_documents_recipient_idx ON published_search_documents (recipient_access_generation_id, event_id)`,
					`CREATE TABLE publication_activity_items (
						id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
						publication_id uuid NOT NULL REFERENCES publications(id) ON DELETE RESTRICT,
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						created_at timestamptz NOT NULL,
						UNIQUE (publication_id, recipient_access_generation_id)
					)`,
					`CREATE INDEX publication_activity_recipient_idx ON publication_activity_items (recipient_access_generation_id, created_at DESC)`,
					`CREATE TABLE publication_curator_activity_items (
						id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
						publication_id uuid NOT NULL UNIQUE REFERENCES publications(id) ON DELETE RESTRICT,
						actor_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						created_at timestamptz NOT NULL
					)`,
					`CREATE TABLE publication_preview_audit_events (
						id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
						event_id uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
						publication_id uuid NOT NULL REFERENCES publications(id) ON DELETE RESTRICT,
						actor_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						recipient_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						created_at timestamptz NOT NULL
					)`,
					`CREATE INDEX publication_preview_audit_event_idx ON publication_preview_audit_events (event_id, created_at DESC)`,
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
				for _, statement := range []string{
					`DROP TABLE publication_preview_audit_events`,
					`DROP TABLE publication_curator_activity_items`,
					`DROP TABLE publication_activity_items`,
					`DROP TABLE published_search_documents`,
					`DROP TABLE new_for_you_entries`,
					`DROP TABLE current_recipient_event_covers`,
					`DROP TABLE current_audience_entitlements`,
					`DROP TABLE current_published_placements`,
					`DROP TABLE current_published_events`,
					`DROP TABLE audience_entries`,
					`DROP TABLE published_media_placements`,
					`DROP TABLE published_moments`,
					`DROP TABLE published_event_revisions`,
					`ALTER TABLE events DROP COLUMN current_publication_id`,
					`DROP TABLE publications`,
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
