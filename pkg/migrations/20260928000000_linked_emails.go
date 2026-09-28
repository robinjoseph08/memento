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
			// stops rather than guess which Person keeps a shared address, or
			// which of two approvals for one address a Curator meant.
			var problems []string
			shared, err := addressesWhere(ctx, tx, "identities", "unlinked_at IS NULL", "count(DISTINCT person_id) > 1")
			if err != nil {
				return err
			}
			if len(shared) > 0 {
				problems = append(problems, fmt.Sprintf("more than one Person holds %s; unlink the duplicates on the previous release", strings.Join(shared, ", ")))
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
			// constraint and partial index built on them.
			//
			// One Person holding one address through several sign-ins (a Google
			// account recreated, or a development database with fake and Google
			// sign-ins) folds into a single Linked Email: the one their update
			// email points at, else the newest. Its sessions move over so nobody
			// is signed out, and the rest are unlinked. Open join requests that
			// differ only by case fold into the most recent one, which keeps the
			// combined sign-in count and the earliest first sign-in.
			_, err = tx.ExecContext(ctx, `
CREATE TEMPORARY TABLE folded_identities ON COMMIT DROP AS
SELECT i.id, first_value(i.id) OVER (PARTITION BY i.person_id, lower(i.email) ORDER BY coalesce(i.id = p.update_identity_id, false) DESC, i.created_at DESC, i.id DESC) AS kept
FROM identities AS i JOIN persons AS p ON p.id = i.person_id WHERE i.unlinked_at IS NULL;
DELETE FROM folded_identities WHERE id = kept;
UPDATE sessions SET identity_id = f.kept FROM folded_identities AS f WHERE sessions.identity_id = f.id;
UPDATE mobile_sign_in_codes SET identity_id = f.kept FROM folded_identities AS f WHERE mobile_sign_in_codes.identity_id = f.id;
UPDATE identities SET unlinked_at = now() FROM folded_identities AS f WHERE identities.id = f.id;
CREATE TEMPORARY TABLE folded_requests ON COMMIT DROP AS
SELECT id, first_value(id) OVER (PARTITION BY lower(email) ORDER BY updated_at DESC, id DESC) AS kept, sign_in_count, created_at
FROM access_requests WHERE kind = 'join' AND status <> 'approved';
DELETE FROM folded_requests WHERE id = kept;
UPDATE access_requests SET sign_in_count = access_requests.sign_in_count + f.count, created_at = least(access_requests.created_at, f.first)
 FROM (SELECT kept, sum(sign_in_count) AS count, min(created_at) AS first FROM folded_requests GROUP BY kept) AS f
 WHERE access_requests.id = f.kept;
DELETE FROM access_requests USING folded_requests AS f WHERE access_requests.id = f.id;
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
		// sign-in as a new identity until a Curator preauthorizes it again,
		// and the Linked Emails that creates fold together on the next upgrade.
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
