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
					`CREATE TABLE engagement_events (
						id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
						recipient_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						session_id uuid NOT NULL REFERENCES sessions(id) ON DELETE RESTRICT,
						kind text NOT NULL CHECK (kind IN (
							'visit', 'destination_opened', 'event_opened', 'media_opened', 'video_started',
							'original_download_started', 'archive_download_started', 'comment_created',
							'favorite_added', 'favorite_removed', 'invitation_suggestion_submitted',
							'invitation_suggestion_withdrawn'
						)),
						destination text CHECK (destination IN ('photos', 'events', 'favorites', 'search')),
						event_id uuid REFERENCES events(id) ON DELETE RESTRICT,
						media_item_id uuid REFERENCES media_items(id) ON DELETE RESTRICT,
						client_claim_id uuid,
						origin_key text,
						occurred_at timestamptz NOT NULL,
						CHECK (
							(kind = 'visit' AND destination IS NULL AND event_id IS NULL AND media_item_id IS NULL) OR
							(kind = 'destination_opened' AND destination IS NOT NULL AND event_id IS NULL AND media_item_id IS NULL) OR
							(kind = 'event_opened' AND destination IS NULL AND event_id IS NOT NULL AND media_item_id IS NULL) OR
							(kind IN ('media_opened', 'video_started', 'original_download_started', 'comment_created', 'favorite_added', 'favorite_removed')
								AND destination IS NULL AND event_id IS NULL AND media_item_id IS NOT NULL) OR
							(kind IN ('archive_download_started', 'invitation_suggestion_submitted', 'invitation_suggestion_withdrawn')
								AND destination IS NULL AND media_item_id IS NULL)
						),
						CHECK ((client_claim_id IS NULL) <> (origin_key IS NULL))
					)`,
					`CREATE UNIQUE INDEX engagement_events_client_claim_idx
						ON engagement_events (session_id, client_claim_id) WHERE client_claim_id IS NOT NULL`,
					`CREATE UNIQUE INDEX engagement_events_origin_idx
						ON engagement_events (origin_key) WHERE origin_key IS NOT NULL`,
					`CREATE INDEX engagement_events_recipient_time_idx
						ON engagement_events (recipient_person_id, occurred_at DESC, id DESC)`,
					`CREATE INDEX engagement_events_recipient_kind_time_idx
						ON engagement_events (recipient_person_id, kind, occurred_at DESC, id DESC)`,
					`CREATE INDEX engagement_events_media_openers_idx
						ON engagement_events (media_item_id, occurred_at DESC, id DESC) WHERE kind = 'media_opened'`,
					`CREATE INDEX engagement_events_retention_idx ON engagement_events (occurred_at, id)`,
					`CREATE TABLE engagement_daily_aggregates (
						recipient_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						activity_date date NOT NULL,
						kind text NOT NULL CHECK (kind IN (
							'visit', 'destination_opened', 'event_opened', 'media_opened', 'video_started',
							'original_download_started', 'archive_download_started', 'comment_created',
							'favorite_added', 'favorite_removed', 'invitation_suggestion_submitted',
							'invitation_suggestion_withdrawn'
						)),
						event_count bigint NOT NULL CHECK (event_count > 0),
						first_occurred_at timestamptz NOT NULL,
						last_occurred_at timestamptz NOT NULL,
						PRIMARY KEY (recipient_person_id, activity_date, kind),
						CHECK (last_occurred_at >= first_occurred_at)
					)`,
					`CREATE INDEX engagement_daily_aggregates_date_idx
						ON engagement_daily_aggregates (activity_date, recipient_person_id)`,
					`CREATE TABLE curator_item_read_states (
						surface text NOT NULL CHECK (surface IN ('work', 'activity')),
						source_kind text NOT NULL CHECK (source_kind <> ''),
						source_id text NOT NULL CHECK (source_id <> ''),
						read_version text NOT NULL CHECK (read_version <> ''),
						read_by_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						read_at timestamptz NOT NULL,
						PRIMARY KEY (surface, source_kind, source_id)
					)`,
					`INSERT INTO jobs (kind, idempotency_key)
						VALUES ('retain_engagement', 'engagement-retention')
						ON CONFLICT (idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING`,
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
					`DELETE FROM jobs WHERE kind = 'retain_engagement' AND idempotency_key = 'engagement-retention'`,
					`DROP TABLE curator_item_read_states`,
					`DROP TABLE engagement_daily_aggregates`,
					`DROP TABLE engagement_events`,
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
