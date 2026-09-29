package migrations

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			// A Linked Email is one address on one Person (ADR 0014). The upgrade
			// stops rather than guess which sign-in keeps a shared address, or
			// which of two approvals for one address a Curator meant.
			var problems []string
			shared, err := addressesWhere(ctx, tx, "identities", "unlinked_at IS NULL", "count(*) > 1")
			if err != nil {
				return err
			}
			if len(shared) > 0 {
				problems = append(problems, fmt.Sprintf("more than one linked sign-in holds %s; unlink the duplicates on the previous release", strings.Join(shared, ", ")))
			}
			approved, err := addressesWhere(ctx, tx, "preauthorizations", "consumed_at IS NULL AND revoked_at IS NULL", "count(*) > 1")
			if err != nil {
				return err
			}
			if len(approved) > 0 {
				problems = append(problems, fmt.Sprintf("unused preauthorizations for %s differ only by letter case; revoke one on the previous release", strings.Join(approved, ", ")))
			}
			if len(problems) > 0 {
				return errors.New(strings.Join(problems, "; "))
			}
			// Multi-statement schema DDL stays raw; Bun has no builder for these
			// constraints or renames, and PostgreSQL wants each RENAME on its
			// own. Dropping provider and subject also drops the unique constraint
			// and partial index built on them. Open join requests that differ
			// only by case keep one: a pending one over a denied one, then the
			// most recent. The identities table
			// becomes linked_emails, and the browser-to-app code becomes a
			// Hand-off Code, since a Sign-in Code is the emailed verification.
			_, err = tx.ExecContext(ctx, `
DELETE FROM access_requests AS older USING access_requests AS newer
 WHERE older.kind = 'join' AND older.status <> 'approved' AND newer.kind = 'join' AND newer.status <> 'approved'
 AND lower(older.email) = lower(newer.email)
 AND (older.status = 'pending', older.updated_at, older.id) < (newer.status = 'pending', newer.updated_at, newer.id);
UPDATE identities SET email = lower(email);
UPDATE preauthorizations SET email = lower(email);
UPDATE access_requests SET email = lower(email);
ALTER TABLE identities RENAME TO linked_emails;
ALTER TABLE linked_emails RENAME CONSTRAINT identities_pkey TO linked_emails_pkey;
ALTER TABLE linked_emails RENAME CONSTRAINT identities_person_id_fkey TO linked_emails_person_id_fkey;
ALTER TABLE linked_emails RENAME CONSTRAINT identities_id_person_id_key TO linked_emails_id_person_id_key;
ALTER TABLE linked_emails RENAME CONSTRAINT identities_email_check TO linked_emails_email_check;
ALTER INDEX identities_person_id_idx RENAME TO linked_emails_person_id_idx;
ALTER TABLE linked_emails DROP COLUMN provider, DROP COLUMN subject,
 ADD CONSTRAINT linked_emails_email_lower_check CHECK (email = lower(email));
CREATE UNIQUE INDEX linked_emails_email_idx ON linked_emails(email) WHERE unlinked_at IS NULL;
ALTER TABLE persons RENAME COLUMN update_identity_id TO update_email_id;
ALTER TABLE persons RENAME CONSTRAINT persons_update_identity_fk TO persons_update_email_fk;
ALTER TABLE sessions RENAME COLUMN identity_id TO linked_email_id;
ALTER TABLE sessions RENAME CONSTRAINT sessions_identity_id_fkey TO sessions_linked_email_id_fkey;
ALTER INDEX sessions_identity_id_idx RENAME TO sessions_linked_email_id_idx;
ALTER TABLE mobile_sign_in_codes RENAME TO handoff_codes;
ALTER TABLE handoff_codes RENAME COLUMN identity_id TO linked_email_id;
ALTER TABLE handoff_codes RENAME CONSTRAINT mobile_sign_in_codes_pkey TO handoff_codes_pkey;
ALTER TABLE handoff_codes RENAME CONSTRAINT mobile_sign_in_codes_identity_id_fkey TO handoff_codes_linked_email_id_fkey;
ALTER TABLE handoff_codes RENAME CONSTRAINT mobile_sign_in_codes_code_hash_check TO handoff_codes_code_hash_check;
ALTER INDEX mobile_sign_in_codes_identity_id_idx RENAME TO handoff_codes_linked_email_id_idx;
ALTER TABLE preauthorizations ADD CONSTRAINT preauthorizations_email_lower_check CHECK (email = lower(email));
ALTER TABLE access_requests DROP COLUMN provider, DROP COLUMN subject,
 ADD CONSTRAINT access_requests_email_lower_check CHECK (email = lower(email));
CREATE UNIQUE INDEX access_requests_open_email_idx ON access_requests(email) WHERE status <> 'approved' AND kind = 'join';
`)
			return errorstack.CaptureContext(ctx, err)
		})
		return errorstack.CaptureContext(ctx, err)
	}, func(ctx context.Context, db *bun.DB) error {
		// Provider subjects cannot be recovered, so each row gets its own ID
		// as a placeholder subject. The previous release then treats every
		// sign-in as a new identity until a Curator preauthorizes it again.
		err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			_, err := tx.ExecContext(ctx, `
DROP INDEX access_requests_open_email_idx;
ALTER TABLE access_requests DROP CONSTRAINT access_requests_email_lower_check,
 ADD COLUMN provider text NOT NULL DEFAULT 'google', ADD COLUMN subject text NOT NULL DEFAULT '';
UPDATE access_requests SET subject = id::text;
ALTER TABLE access_requests ALTER COLUMN provider DROP DEFAULT, ALTER COLUMN subject DROP DEFAULT,
 ADD CONSTRAINT access_requests_provider_check CHECK (provider <> ''),
 ADD CONSTRAINT access_requests_subject_check CHECK (length(subject) BETWEEN 1 AND 255);
CREATE UNIQUE INDEX access_requests_open_identity_idx ON access_requests(provider, subject) WHERE status <> 'approved' AND kind = 'join';
ALTER TABLE preauthorizations DROP CONSTRAINT preauthorizations_email_lower_check;
ALTER INDEX handoff_codes_linked_email_id_idx RENAME TO mobile_sign_in_codes_identity_id_idx;
ALTER TABLE handoff_codes RENAME CONSTRAINT handoff_codes_pkey TO mobile_sign_in_codes_pkey;
ALTER TABLE handoff_codes RENAME CONSTRAINT handoff_codes_linked_email_id_fkey TO mobile_sign_in_codes_identity_id_fkey;
ALTER TABLE handoff_codes RENAME CONSTRAINT handoff_codes_code_hash_check TO mobile_sign_in_codes_code_hash_check;
ALTER TABLE handoff_codes RENAME COLUMN linked_email_id TO identity_id;
ALTER TABLE handoff_codes RENAME TO mobile_sign_in_codes;
ALTER INDEX sessions_linked_email_id_idx RENAME TO sessions_identity_id_idx;
ALTER TABLE sessions RENAME CONSTRAINT sessions_linked_email_id_fkey TO sessions_identity_id_fkey;
ALTER TABLE sessions RENAME COLUMN linked_email_id TO identity_id;
ALTER TABLE persons RENAME CONSTRAINT persons_update_email_fk TO persons_update_identity_fk;
ALTER TABLE persons RENAME COLUMN update_email_id TO update_identity_id;
DROP INDEX linked_emails_email_idx;
ALTER INDEX linked_emails_person_id_idx RENAME TO identities_person_id_idx;
ALTER TABLE linked_emails DROP CONSTRAINT linked_emails_email_lower_check,
 ADD COLUMN provider text NOT NULL DEFAULT 'google', ADD COLUMN subject text NOT NULL DEFAULT '';
ALTER TABLE linked_emails RENAME CONSTRAINT linked_emails_pkey TO identities_pkey;
ALTER TABLE linked_emails RENAME CONSTRAINT linked_emails_person_id_fkey TO identities_person_id_fkey;
ALTER TABLE linked_emails RENAME CONSTRAINT linked_emails_id_person_id_key TO identities_id_person_id_key;
ALTER TABLE linked_emails RENAME CONSTRAINT linked_emails_email_check TO identities_email_check;
ALTER TABLE linked_emails RENAME TO identities;
UPDATE identities SET subject = id::text;
ALTER TABLE identities ALTER COLUMN provider DROP DEFAULT, ALTER COLUMN subject DROP DEFAULT,
 ADD CONSTRAINT identities_provider_check CHECK (provider <> ''),
 ADD CONSTRAINT identities_subject_check CHECK (length(subject) BETWEEN 1 AND 255),
 ADD CONSTRAINT identities_provider_subject_key UNIQUE (provider, subject);
`)
			return errorstack.CaptureContext(ctx, err)
		})
		return errorstack.CaptureContext(ctx, err)
	})
}

// addressesWhere lists the lowercased addresses in a table whose matching rows
// violate the having condition, for the upgrade's refusal message.
func addressesWhere(ctx context.Context, tx bun.Tx, table, where, having string) ([]string, error) {
	var addresses []string
	err := tx.NewSelect().TableExpr(table).ColumnExpr("lower(email)").
		Where(where).GroupExpr("lower(email)").Having(having).OrderExpr("lower(email)").Scan(ctx, &addresses)
	return addresses, errorstack.CaptureContext(ctx, err)
}
