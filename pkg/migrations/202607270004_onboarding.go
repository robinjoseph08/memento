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
					`ALTER TABLE onboarding_choices DROP CONSTRAINT onboarding_choices_check`,
					`ALTER TABLE onboarding_choices ADD COLUMN email_previews_acknowledged boolean NOT NULL DEFAULT false`,
					`ALTER TABLE onboarding_choices ADD COLUMN push_guidance_acknowledged boolean NOT NULL DEFAULT false`,
					`UPDATE onboarding_choices SET email_previews_acknowledged = true, push_guidance_acknowledged = true`,
					`ALTER TABLE onboarding_choices ALTER COLUMN email_previews_acknowledged DROP DEFAULT`,
					`ALTER TABLE onboarding_choices ALTER COLUMN push_guidance_acknowledged DROP DEFAULT`,
					`ALTER TABLE onboarding_choices ADD CONSTRAINT onboarding_choices_informed_check CHECK (
						privacy_acknowledged AND engagement_acknowledged AND interest_list_acknowledged AND
						email_previews_acknowledged AND push_guidance_acknowledged
					)`,
					`CREATE TABLE onboarding_progress (
						recipient_access_generation_id uuid PRIMARY KEY REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						privacy_acknowledged boolean NOT NULL DEFAULT false,
						engagement_acknowledged boolean NOT NULL DEFAULT false,
						interest_list_acknowledged boolean NOT NULL DEFAULT false,
						email_previews_acknowledged boolean NOT NULL DEFAULT false,
						push_guidance_acknowledged boolean NOT NULL DEFAULT false,
						email_preference text NOT NULL DEFAULT 'immediate' CHECK (email_preference IN ('immediate', 'weekly', 'none')),
						session_type text NOT NULL DEFAULT 'trusted' CHECK (session_type IN ('trusted', 'public')),
						updated_at timestamptz NOT NULL DEFAULT now()
					)`,
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
				statements := []string{
					`DROP TABLE IF EXISTS onboarding_progress`,
					`ALTER TABLE onboarding_choices DROP CONSTRAINT onboarding_choices_informed_check`,
					`ALTER TABLE onboarding_choices DROP COLUMN push_guidance_acknowledged`,
					`ALTER TABLE onboarding_choices DROP COLUMN email_previews_acknowledged`,
					`ALTER TABLE onboarding_choices ADD CONSTRAINT onboarding_choices_check CHECK (
						privacy_acknowledged AND engagement_acknowledged AND interest_list_acknowledged
					)`,
				}
				for _, statement := range statements {
					if _, err := tx.ExecContext(ctx, statement); err != nil {
						return err
					}
				}
				return nil
			})
		},
	)
}
