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
					`ALTER TABLE system_settings
						ADD COLUMN content_revision bigint NOT NULL DEFAULT 0 CHECK (content_revision >= 0)`,
					`ALTER TABLE publications
						ADD COLUMN content_revision bigint NOT NULL DEFAULT 0 CHECK (content_revision >= 0)`,
					`CREATE UNIQUE INDEX publications_content_revision_idx
						ON publications (content_revision) WHERE content_revision > 0`,
					`ALTER TABLE content_withdrawals
						ADD COLUMN reason text NOT NULL DEFAULT 'Legacy withdrawal'
						CHECK (char_length(reason) BETWEEN 1 AND 1000),
						ADD COLUMN content_revision bigint NOT NULL DEFAULT 0 CHECK (content_revision >= 0)`,
					`CREATE UNIQUE INDEX content_withdrawals_content_revision_idx
						ON content_withdrawals (content_revision) WHERE content_revision > 0`,
					`CREATE FUNCTION content_is_withdrawn(
						event_id uuid, moment_id uuid, media_id uuid
					) RETURNS boolean
					LANGUAGE sql STABLE PARALLEL SAFE
					SET search_path = public
					RETURN EXISTS (
						SELECT 1 FROM content_withdrawals AS withdrawal
						WHERE withdrawal.restored_at IS NULL AND (
							(withdrawal.target_kind = 'event' AND withdrawal.target_id = event_id)
							OR (withdrawal.target_kind = 'moment' AND withdrawal.target_id = moment_id)
							OR (withdrawal.target_kind = 'media' AND withdrawal.target_id = media_id)
						)
					)`,
					`ALTER TABLE publication_audit_events
						DROP CONSTRAINT publication_audit_events_target_kind_check,
						ADD CONSTRAINT publication_audit_events_target_kind_check
						CHECK (target_kind IN ('event', 'moment', 'media', 'loose_item'))`,
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
					`ALTER TABLE publication_audit_events
						DROP CONSTRAINT publication_audit_events_target_kind_check,
						ADD CONSTRAINT publication_audit_events_target_kind_check
						CHECK (target_kind IN ('moment', 'loose_item')) NOT VALID`,
					`DROP FUNCTION content_is_withdrawn(uuid, uuid, uuid)`,
					`DROP INDEX content_withdrawals_content_revision_idx`,
					`ALTER TABLE content_withdrawals DROP COLUMN content_revision, DROP COLUMN reason`,
					`DROP INDEX publications_content_revision_idx`,
					`ALTER TABLE publications DROP COLUMN content_revision`,
					`ALTER TABLE system_settings DROP COLUMN content_revision`,
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
