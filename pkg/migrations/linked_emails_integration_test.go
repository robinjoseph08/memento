package migrations_test

import (
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const linkedEmailsMigration = "20260928000000"

// TestLinkedEmailsMigrationKeepsSessionsAndRefusesSharedAddresses rolls the
// Linked Email migration back, seeds provider-and-subject identities the way
// the previous release wrote them, and applies it again.
func TestLinkedEmailsMigrationKeepsSessionsAndRefusesSharedAddresses(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := t.Context()
	migrator := migrations.NewMigrator(db)
	registered := migrations.Migrations.Sorted()
	targetIndex := -1
	for i := range registered {
		if registered[i].Name == linkedEmailsMigration {
			targetIndex = i
		}
	}
	require.NotEqual(t, -1, targetIndex, "Linked Email migration must be registered")
	// Roll back to just before the target so the seed matches the previous release.
	for i := len(registered) - 1; i >= targetIndex; i-- {
		require.NoError(t, registered[i].Down(ctx, migrator, &registered[i]))
	}
	// up applies the target and everything after it, as an upgrade would.
	up := func() error {
		for i := targetIndex; i < len(registered); i++ {
			if err := registered[i].Up(ctx, migrator, &registered[i]); err != nil {
				return err
			}
		}
		return nil
	}

	now := time.Now().UTC()
	earlier := now.Add(-time.Hour)
	alex, sam, dup1, dup2 := models.NewUUIDv7(), models.NewUUIDv7(), models.NewUUIDv7(), models.NewUUIDv7()
	alexGoogle, samGoogle, samOld, dupOne, dupTwo := models.NewUUIDv7(), models.NewUUIDv7(), models.NewUUIDv7(), models.NewUUIDv7(), models.NewUUIDv7()
	samApproval, olderRequest, newerRequest := models.NewUUIDv7(), models.NewUUIDv7(), models.NewUUIDv7()
	// Raw SQL on purpose: the models no longer know about provider or subject.
	// Dup One and Dup Two are different Persons holding one address.
	_, err := db.ExecContext(ctx, `
INSERT INTO persons (id, display_name, is_curator, created_at) VALUES
 (?, 'Alex', true, ?), (?, 'Sam', false, ?), (?, 'Dup One', false, ?), (?, 'Dup Two', false, ?);
INSERT INTO identities (id, person_id, provider, subject, email, created_at, unlinked_at) VALUES
 (?, ?, 'google', 'alex-subject', 'Alex@Example.test', ?, NULL),
 (?, ?, 'google', 'sam-subject', 'sam@example.test', ?, NULL),
 (?, ?, 'google', 'sam-old', 'sam@example.test', ?, ?),
 (?, ?, 'google', 'dup-one', 'dup@example.test', ?, NULL),
 (?, ?, 'google', 'dup-two', 'Dup@example.test', ?, NULL);
UPDATE persons SET update_identity_id = ?, email_updates = true WHERE id = ?;
UPDATE persons SET update_identity_id = ?, email_updates = true WHERE id = ?;
INSERT INTO sessions (id, token_hash, identity_id, device, created_at, renewed_at, expires_at) VALUES
 (?, ?, ?, 'Chrome on Mac', ?, ?, ?), (?, ?, ?, 'Safari on iPhone', ?, ?, ?);
INSERT INTO mobile_sign_in_codes (code_hash, identity_id, expires_at) VALUES (?, ?, ?);
INSERT INTO preauthorizations (id, person_id, email, created_at) VALUES (?, ?, 'Second@Example.test', ?), (?, ?, 'second@example.test', ?);
INSERT INTO access_requests (id, kind, provider, subject, email, email_verified, display_name, status, created_at, updated_at, resolved_at) VALUES
 (?, 'join', 'google', 'stranger-old', 'Stranger@Example.test', true, 'Stranger', 'denied', ?, ?, ?),
 (?, 'join', 'google', 'stranger-new', 'stranger@example.test', true, 'Stranger Again', 'pending', ?, ?, NULL);
INSERT INTO access_requests (id, kind, provider, subject, email, email_verified, display_name, person_id, status, created_at, updated_at) VALUES
 (?, 'album', 'google', 'sam-subject', 'sam@example.test', true, 'Sam', ?, 'pending', ?, ?);
`,
		alex, now, sam, now, dup1, now, dup2, now,
		alexGoogle, alex, now, samGoogle, sam, earlier, samOld, sam, earlier, earlier, dupOne, dup1, now, dupTwo, dup2, now,
		alexGoogle, alex, samGoogle, sam,
		models.NewUUIDv7(), hash(1), alexGoogle, now, now, now.Add(time.Hour), models.NewUUIDv7(), hash(2), samGoogle, now, now, now.Add(time.Hour),
		hash(3), alexGoogle, now.Add(time.Hour),
		samApproval, sam, now, models.NewUUIDv7(), sam, now,
		olderRequest, earlier, earlier, earlier, newerRequest, now, now,
		models.NewUUIDv7(), sam, now, now,
	)
	require.NoError(t, err)

	err = up()
	require.ErrorContains(t, err, "more than one linked sign-in holds dup@example.test", "two sign-ins holding one address must stop the upgrade")
	require.ErrorContains(t, err, "preauthorizations for second@example.test differ only by letter case")
	columns, err := db.NewSelect().Table("information_schema.columns").Where("table_schema = current_schema() AND table_name = 'identities' AND column_name IN ('provider', 'subject')").Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, columns, "a refused migration changes nothing")

	_, err = db.ExecContext(ctx, `DELETE FROM identities WHERE id = ?; UPDATE preauthorizations SET revoked_at = ? WHERE id = ?`, dupTwo, now, samApproval)
	require.NoError(t, err)
	require.NoError(t, up())

	var emails []string
	require.NoError(t, db.NewSelect().Table("linked_emails").Column("email").Order("email").Scan(ctx, &emails))
	assert.Equal(t, []string{"alex@example.test", "dup@example.test", "sam@example.test", "sam@example.test"}, emails)
	var linked []models.UUID
	require.NoError(t, db.NewSelect().Table("linked_emails").Column("id").Where("unlinked_at IS NULL").Order("email").Scan(ctx, &linked))
	assert.Equal(t, []models.UUID{alexGoogle, dupOne, samGoogle}, linked, "an unlinked sign-in may share its address with a linked one")
	var sessionEmails []models.UUID
	require.NoError(t, db.NewSelect().Table("sessions").Column("linked_email_id").Order("created_at", "id").Scan(ctx, &sessionEmails))
	assert.ElementsMatch(t, []models.UUID{alexGoogle, samGoogle}, sessionEmails, "every session keeps its Linked Email")
	var person models.Person
	require.NoError(t, db.NewSelect().Model(&person).Column("update_email_id", "email_updates").Where("id = ?", alex).Scan(ctx))
	require.NotNil(t, person.UpdateEmailID)
	assert.Equal(t, alexGoogle, *person.UpdateEmailID)
	assert.True(t, person.EmailUpdates)
	codes, err := db.NewSelect().Table("handoff_codes").Where("linked_email_id = ?", alexGoogle).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, codes)
	var approved []string
	require.NoError(t, db.NewSelect().Table("preauthorizations").Column("email").Scan(ctx, &approved))
	assert.Equal(t, []string{"second@example.test", "second@example.test"}, approved)
	var requested []models.AccessRequest
	require.NoError(t, db.NewSelect().Model(&requested).Where("kind = 'join'").Scan(ctx))
	require.Len(t, requested, 1, "open join requests for one address keep the most recent")
	assert.Equal(t, newerRequest, requested[0].ID)
	assert.Equal(t, "pending", requested[0].Status)
	var albumRequest string
	require.NoError(t, db.NewSelect().Table("access_requests").Column("email").Where("kind = 'album'").Scan(ctx, &albumRequest))
	assert.Equal(t, "sam@example.test", albumRequest)
	for _, table := range []string{"linked_emails", "access_requests"} {
		columns, err := db.NewSelect().Table("information_schema.columns").Where("table_schema = current_schema() AND table_name = ? AND column_name IN ('provider', 'subject')", table).Count(ctx)
		require.NoError(t, err)
		assert.Zero(t, columns, table)
	}

	// The database, not Go, keeps every linked address lowercased and unique.
	_, err = db.ExecContext(ctx, `INSERT INTO linked_emails (id, person_id, email, created_at) VALUES (?, ?, 'sam@example.test', ?)`, models.NewUUIDv7(), alex, now)
	require.Error(t, err, "a second Person cannot link an address someone holds")
	_, err = db.ExecContext(ctx, `INSERT INTO linked_emails (id, person_id, email, created_at) VALUES (?, ?, 'Mixed@example.test', ?)`, models.NewUUIDv7(), alex, now)
	require.Error(t, err, "addresses are stored lowercased")
	_, err = db.ExecContext(ctx, `INSERT INTO access_requests (id, kind, email, email_verified, display_name, status, created_at, updated_at) VALUES (?, 'join', 'stranger@example.test', true, 'Again', 'pending', ?, ?)`, models.NewUUIDv7(), now, now)
	require.Error(t, err, "one open join request per address")
}

func hash(seed byte) []byte {
	value := make([]byte, 32)
	value[0] = seed
	return value
}
