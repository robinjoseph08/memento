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
					`CREATE TABLE comments (
						id uuid PRIMARY KEY,
						media_item_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
						author_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						author_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						idempotency_key uuid NOT NULL,
						body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 2000),
						state text NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'deleted', 'moderated')),
						version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
						created_at timestamptz NOT NULL DEFAULT now(),
						updated_at timestamptz NOT NULL DEFAULT now(),
						edited_at timestamptz,
						deleted_at timestamptz,
						moderated_at timestamptz,
						moderated_by_person_id uuid REFERENCES people(id) ON DELETE RESTRICT,
						CHECK (
							(state = 'active' AND deleted_at IS NULL AND moderated_at IS NULL AND moderated_by_person_id IS NULL) OR
							(state = 'deleted' AND deleted_at IS NOT NULL AND moderated_at IS NULL AND moderated_by_person_id IS NULL) OR
							(state = 'moderated' AND deleted_at IS NULL AND moderated_at IS NOT NULL AND moderated_by_person_id IS NOT NULL)
						)
					)`,
					`CREATE INDEX comments_media_time_idx ON comments (media_item_id, created_at, id)`,
					`CREATE INDEX comments_author_time_idx ON comments (author_person_id, created_at DESC, id DESC)`,
					`CREATE UNIQUE INDEX comments_author_idempotency_idx ON comments (author_access_generation_id, idempotency_key)`,
					`CREATE TABLE comment_moderation_history (
						id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
						comment_id uuid NOT NULL REFERENCES comments(id) ON DELETE RESTRICT,
						actor_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						prior_state text NOT NULL CHECK (prior_state IN ('active', 'deleted')),
						prior_body text NOT NULL,
						reason text NOT NULL CHECK (char_length(reason) BETWEEN 1 AND 500),
						created_at timestamptz NOT NULL DEFAULT now()
					)`,
					`CREATE INDEX comment_moderation_history_comment_idx ON comment_moderation_history (comment_id, created_at, id)`,
					`CREATE TABLE comment_subscriptions (
						media_item_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						muted boolean NOT NULL DEFAULT false,
						created_at timestamptz NOT NULL DEFAULT now(),
						updated_at timestamptz NOT NULL DEFAULT now(),
						PRIMARY KEY (media_item_id, recipient_access_generation_id)
					)`,
					`CREATE INDEX comment_subscriptions_generation_idx ON comment_subscriptions (recipient_access_generation_id, media_item_id)`,
					`CREATE TABLE comment_activity_items (
						id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
						comment_id uuid NOT NULL REFERENCES comments(id) ON DELETE RESTRICT,
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						created_at timestamptz NOT NULL DEFAULT now(),
						dispatched_at timestamptz,
						suppressed_at timestamptz,
						UNIQUE (comment_id, recipient_access_generation_id),
						CHECK (dispatched_at IS NULL OR suppressed_at IS NULL)
					)`,
					`CREATE INDEX comment_activity_recipient_idx ON comment_activity_items (recipient_access_generation_id, created_at DESC, id DESC)`,
					`CREATE INDEX favorites_media_idx ON favorites (media_item_id, recipient_person_id) WHERE is_current`,
					`CREATE TABLE interaction_activity_items (
						id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
						kind text NOT NULL CHECK (kind IN ('comment', 'favorite')),
						recipient_access_generation_id uuid REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						actor_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						media_item_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
						comment_id uuid REFERENCES comments(id) ON DELETE RESTRICT,
						favorite_recipient_person_id uuid REFERENCES people(id) ON DELETE RESTRICT,
						action text NOT NULL CHECK (action IN ('comment_created', 'favorite_added', 'favorite_removed')),
						read_at timestamptz,
						created_at timestamptz NOT NULL DEFAULT now(),
						CHECK (
							(kind = 'comment' AND recipient_access_generation_id IS NOT NULL AND comment_id IS NOT NULL AND favorite_recipient_person_id IS NULL AND action = 'comment_created') OR
							(kind = 'favorite' AND recipient_access_generation_id IS NULL AND comment_id IS NULL AND favorite_recipient_person_id IS NOT NULL AND action IN ('favorite_added', 'favorite_removed'))
						)
					)`,
					`CREATE UNIQUE INDEX interaction_comment_recipient_idx ON interaction_activity_items (comment_id, recipient_access_generation_id) WHERE kind = 'comment'`,
					`CREATE INDEX interaction_recipient_time_idx ON interaction_activity_items (recipient_access_generation_id, created_at DESC, id DESC) WHERE recipient_access_generation_id IS NOT NULL`,
					`CREATE INDEX interaction_curator_time_idx ON interaction_activity_items (created_at DESC, id DESC)`,
					`CREATE INDEX interaction_curator_unread_idx ON interaction_activity_items (created_at, id) WHERE read_at IS NULL`,
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
					`DROP TABLE IF EXISTS interaction_activity_items`,
					`DROP INDEX IF EXISTS favorites_media_idx`,
					`DROP TABLE IF EXISTS comment_activity_items`,
					`DROP TABLE IF EXISTS comment_subscriptions`,
					`DROP TABLE IF EXISTS comment_moderation_history`,
					`DROP TABLE IF EXISTS comments`,
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
