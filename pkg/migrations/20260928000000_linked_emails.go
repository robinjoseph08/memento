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
			// constraints. Dropping provider and subject also drops the unique
			// constraint and partial index built on them. Open join requests that
			// differ only by case keep the most recent one.
			_, err = tx.ExecContext(ctx, `
DELETE FROM access_requests AS older USING access_requests AS newer
 WHERE older.kind = 'join' AND older.status <> 'approved' AND newer.kind = 'join' AND newer.status <> 'approved'
 AND lower(older.email) = lower(newer.email) AND (older.updated_at, older.id) < (newer.updated_at, newer.id);
UPDATE identities SET email = lower(email);
UPDATE preauthorizations SET email = lower(email);
UPDATE access_requests SET email = lower(email);
ALTER TABLE identities DROP COLUMN provider, DROP COLUMN subject,
 ADD CONSTRAINT identities_email_lower_check CHECK (email = lower(email));
CREATE UNIQUE INDEX identities_linked_email_idx ON identities(email) WHERE unlinked_at IS NULL;
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
DROP INDEX identities_linked_email_idx;
ALTER TABLE identities DROP CONSTRAINT identities_email_lower_check,
 ADD COLUMN provider text NOT NULL DEFAULT 'google', ADD COLUMN subject text NOT NULL DEFAULT '';
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
