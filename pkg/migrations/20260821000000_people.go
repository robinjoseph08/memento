package migrations

import (
	"context"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			// Keep the multi-statement schema DDL raw; Bun has no general ALTER TABLE builder.
			_, err := tx.ExecContext(ctx, `
ALTER TABLE persons ADD COLUMN onboarding_completed_at timestamptz,
 ADD COLUMN deactivated_at timestamptz,
 ADD COLUMN update_identity_id uuid,
 ADD COLUMN email_updates boolean NOT NULL DEFAULT false,
 ADD CONSTRAINT persons_email_updates_check CHECK (NOT email_updates OR update_identity_id IS NOT NULL);
ALTER TABLE identities ADD CONSTRAINT identities_id_person_id_key UNIQUE (id, person_id);
ALTER TABLE persons ADD CONSTRAINT persons_update_identity_fk FOREIGN KEY (update_identity_id, id) REFERENCES identities(id, person_id);
CREATE TABLE preauthorizations (
 id uuid PRIMARY KEY,
 person_id uuid NOT NULL REFERENCES persons(id),
 email text COLLATE "C" NOT NULL CHECK (length(email) BETWEEN 3 AND 254),
 created_at timestamptz NOT NULL,
 consumed_at timestamptz,
 revoked_at timestamptz,
 CHECK (consumed_at IS NULL OR revoked_at IS NULL)
);
CREATE UNIQUE INDEX preauthorizations_unused_email_idx ON preauthorizations(email) WHERE consumed_at IS NULL AND revoked_at IS NULL;
CREATE INDEX preauthorizations_person_id_idx ON preauthorizations(person_id);
CREATE INDEX identities_person_id_idx ON identities(person_id);
ALTER TABLE identities ADD COLUMN unlinked_at timestamptz;
ALTER TABLE sessions ADD COLUMN id uuid UNIQUE, ADD COLUMN device text NOT NULL DEFAULT 'Unknown browser';
`)
			if err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
			// Preserve existing browser sessions while assigning public, non-credential IDs.
			var rows []struct{ TokenHash []byte }
			if err := tx.NewSelect().Table("sessions").Column("token_hash").Scan(ctx, &rows); err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
			for _, row := range rows {
				if _, err := tx.NewUpdate().Table("sessions").Set("id = ?", models.NewUUIDv7()).
					Where("token_hash = ?", row.TokenHash).Exec(ctx); err != nil {
					return errorstack.CaptureContext(ctx, err)
				}
			}
			// Bun cannot express ALTER COLUMN; require IDs only after every session is backfilled.
			_, err = tx.ExecContext(ctx, "ALTER TABLE sessions ALTER COLUMN id SET NOT NULL")
			return errorstack.CaptureContext(ctx, err)
		})
		return errorstack.CaptureContext(ctx, err)
	}, func(ctx context.Context, db *bun.DB) error {
		// The rollback also uses raw DDL for its ALTER TABLE statements.
		_, err := db.ExecContext(ctx, `
DROP TABLE preauthorizations;
DROP INDEX identities_person_id_idx;
ALTER TABLE sessions DROP COLUMN id, DROP COLUMN device;
ALTER TABLE identities DROP COLUMN unlinked_at;
ALTER TABLE persons DROP CONSTRAINT persons_update_identity_fk, DROP COLUMN onboarding_completed_at, DROP COLUMN deactivated_at, DROP COLUMN update_identity_id, DROP COLUMN email_updates;
ALTER TABLE identities DROP CONSTRAINT identities_id_person_id_key;
`)
		return errorstack.CaptureContext(ctx, err)
	})
}
