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
					`CREATE TABLE invitation_suggestions (
						id uuid PRIMARY KEY,
						requester_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						requester_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						requester_session_id uuid NOT NULL REFERENCES sessions(id) ON DELETE RESTRICT,
						name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
						email text NOT NULL CHECK (char_length(email) BETWEEN 3 AND 320),
						normalized_email text NOT NULL CHECK (normalized_email = lower(normalized_email)),
						relationship_context text NOT NULL CHECK (char_length(relationship_context) BETWEEN 1 AND 1000),
						spoke_with_person boolean NOT NULL,
						status text NOT NULL DEFAULT 'submitted' CHECK (status IN ('submitted', 'accepted', 'rejected')),
						withdrawn_at timestamptz,
						resolved_at timestamptz,
						resolved_by_person_id uuid REFERENCES people(id) ON DELETE RESTRICT,
						matched_person_id uuid REFERENCES people(id) ON DELETE RESTRICT,
						created_at timestamptz NOT NULL DEFAULT now(),
						updated_at timestamptz NOT NULL DEFAULT now(),
						CHECK (
							(status = 'submitted' AND resolved_at IS NULL AND resolved_by_person_id IS NULL AND matched_person_id IS NULL) OR
							(status = 'accepted' AND resolved_at IS NOT NULL AND resolved_by_person_id IS NOT NULL AND matched_person_id IS NOT NULL AND withdrawn_at IS NULL) OR
							(status = 'rejected' AND resolved_at IS NOT NULL AND resolved_by_person_id IS NOT NULL AND matched_person_id IS NULL AND withdrawn_at IS NULL)
						),
						CHECK (withdrawn_at IS NULL OR status = 'submitted')
					)`,
					`CREATE INDEX invitation_suggestions_requester_idx ON invitation_suggestions (requester_person_id, created_at DESC, id DESC)`,
					`CREATE INDEX invitation_suggestions_submitted_idx ON invitation_suggestions (created_at, id) WHERE status = 'submitted' AND withdrawn_at IS NULL`,
					`CREATE INDEX invitation_suggestions_email_idx ON invitation_suggestions (normalized_email, created_at DESC)`,
					`CREATE TABLE recipient_activity_items (
						id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
						recipient_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						actor_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						invitation_suggestion_id uuid NOT NULL REFERENCES invitation_suggestions(id) ON DELETE RESTRICT,
						action text NOT NULL CHECK (action IN ('invitation_suggestion_submitted', 'invitation_suggestion_withdrawn', 'invitation_suggestion_accepted', 'invitation_suggestion_rejected')),
						created_at timestamptz NOT NULL DEFAULT now()
					)`,
					`CREATE INDEX recipient_activity_items_recipient_idx ON recipient_activity_items (recipient_person_id, created_at DESC, id DESC)`,
					`CREATE TABLE curator_activity_items (
						id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
						actor_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						invitation_suggestion_id uuid NOT NULL REFERENCES invitation_suggestions(id) ON DELETE RESTRICT,
						action text NOT NULL CHECK (action IN ('invitation_suggestion_submitted', 'invitation_suggestion_withdrawn', 'invitation_suggestion_accepted', 'invitation_suggestion_rejected')),
						read_at timestamptz,
						created_at timestamptz NOT NULL DEFAULT now()
					)`,
					`CREATE INDEX curator_activity_items_time_idx ON curator_activity_items (created_at DESC, id DESC)`,
					`CREATE INDEX curator_activity_items_unread_idx ON curator_activity_items (created_at, id) WHERE read_at IS NULL`,
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
					`DROP TABLE IF EXISTS curator_activity_items`,
					`DROP TABLE IF EXISTS recipient_activity_items`,
					`DROP TABLE IF EXISTS invitation_suggestions`,
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
