package migrations_test

import (
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun/migrate"
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
	var target *migrate.Migration
	registered := migrations.Migrations.Sorted()
	for i := range registered {
		if registered[i].Name == linkedEmailsMigration {
			target = &registered[i]
		}
	}
	require.NotNil(t, target, "Linked Email migration must be registered")
	require.NoError(t, target.Down(ctx, migrator, target))

	now := time.Now().UTC()
	earlier := now.Add(-time.Hour)
	alex, sam, dup1, dup2 := models.NewUUIDv7(), models.NewUUIDv7(), models.NewUUIDv7(), models.NewUUIDv7()
	alexGoogle, samGoogle, samFake, samOld, dupOne, dupTwo := models.NewUUIDv7(), models.NewUUIDv7(), models.NewUUIDv7(), models.NewUUIDv7(), models.NewUUIDv7(), models.NewUUIDv7()
	samApproval, olderRequest, newerRequest := models.NewUUIDv7(), models.NewUUIDv7(), models.NewUUIDv7()
	// Raw SQL on purpose: the models no longer know about provider or subject.
	// Sam holds one address through Google and fake sign-in; Dup One and Dup
	// Two are different Persons holding one address.
	_, err := db.ExecContext(ctx, `
INSERT INTO persons (id, display_name, is_curator, created_at) VALUES
 (?, 'Alex', true, ?), (?, 'Sam', false, ?), (?, 'Dup One', false, ?), (?, 'Dup Two', false, ?);
INSERT INTO identities (id, person_id, provider, subject, email, created_at, unlinked_at) VALUES
 (?, ?, 'google', 'alex-subject', 'Alex@Example.test', ?, NULL),
 (?, ?, 'google', 'sam-subject', 'sam@example.test', ?, NULL),
 (?, ?, 'fake', 'sam@example.test', 'Sam@Example.test', ?, NULL),
 (?, ?, 'google', 'sam-old', 'sam@example.test', ?, ?),
 (?, ?, 'google', 'dup-one', 'dup@example.test', ?, NULL),
 (?, ?, 'google', 'dup-two', 'Dup@example.test', ?, NULL);
UPDATE persons SET update_identity_id = ?, email_updates = true WHERE id = ?;
UPDATE persons SET update_identity_id = ?, email_updates = true WHERE id = ?;
INSERT INTO sessions (id, token_hash, identity_id, device, created_at, renewed_at, expires_at) VALUES
 (?, ?, ?, 'Chrome on Mac', ?, ?, ?), (?, ?, ?, 'Safari on iPhone', ?, ?, ?), (?, ?, ?, 'Firefox on Linux', ?, ?, ?);
INSERT INTO mobile_sign_in_codes (code_hash, identity_id, expires_at) VALUES (?, ?, ?), (?, ?, ?);
INSERT INTO preauthorizations (id, person_id, email, created_at) VALUES (?, ?, 'Second@Example.test', ?), (?, ?, 'second@example.test', ?);
INSERT INTO access_requests (id, kind, provider, subject, email, email_verified, display_name, status, created_at, updated_at, resolved_at) VALUES
 (?, 'join', 'google', 'stranger-old', 'Stranger@Example.test', true, 'Stranger', 'denied', ?, ?, ?),
 (?, 'join', 'google', 'stranger-new', 'stranger@example.test', true, 'Stranger Again', 'pending', ?, ?, NULL);
INSERT INTO access_requests (id, kind, provider, subject, email, email_verified, display_name, person_id, status, created_at, updated_at) VALUES
 (?, 'album', 'google', 'sam-subject', 'sam@example.test', true, 'Sam', ?, 'pending', ?, ?);
`,
		alex, now, sam, now, dup1, now, dup2, now,
		alexGoogle, alex, now, samGoogle, sam, earlier, samFake, sam, now, samOld, sam, earlier, earlier, dupOne, dup1, now, dupTwo, dup2, now,
		alexGoogle, alex, samGoogle, sam,
		models.NewUUIDv7(), hash(1), alexGoogle, now, now, now.Add(time.Hour), models.NewUUIDv7(), hash(2), samGoogle, now, now, now.Add(time.Hour), models.NewUUIDv7(), hash(3), samFake, now, now, now.Add(time.Hour),
		hash(4), alexGoogle, now.Add(time.Hour), hash(5), samFake, now.Add(time.Hour),
		samApproval, sam, now, models.NewUUIDv7(), sam, now,
		olderRequest, earlier, earlier, earlier, newerRequest, now, now,
		models.NewUUIDv7(), sam, now, now,
	)
	require.NoError(t, err)

	err = target.Up(ctx, migrator, target)
	require.ErrorContains(t, err, "more than one Person holds dup@example.test", "two Persons holding one address must stop the upgrade")
	require.ErrorContains(t, err, "preauthorizations for second@example.test differ only by letter case")
	columns, err := db.NewSelect().Table("information_schema.columns").Where("table_schema = current_schema() AND table_name = 'identities' AND column_name IN ('provider', 'subject')").Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, columns, "a refused migration changes nothing")

	_, err = db.ExecContext(ctx, `DELETE FROM identities WHERE id = ?; UPDATE preauthorizations SET revoked_at = ? WHERE id = ?`, dupTwo, now, samApproval)
	require.NoError(t, err)
	require.NoError(t, target.Up(ctx, migrator, target))

	var emails []string
	require.NoError(t, db.NewSelect().Table("identities").Column("email").Order("email").Scan(ctx, &emails))
	assert.Equal(t, []string{"alex@example.test", "dup@example.test", "sam@example.test", "sam@example.test", "sam@example.test"}, emails)
	var linked []models.UUID
	require.NoError(t, db.NewSelect().Table("identities").Column("id").Where("unlinked_at IS NULL").Order("email").Scan(ctx, &linked))
	assert.Equal(t, []models.UUID{alexGoogle, dupOne, samGoogle}, linked, "one Person's duplicate sign-ins fold into the Linked Email their update email points at")
	var sessionIdentities []models.UUID
	require.NoError(t, db.NewSelect().Table("sessions").Column("identity_id").Order("created_at", "id").Scan(ctx, &sessionIdentities))
	assert.ElementsMatch(t, []models.UUID{alexGoogle, samGoogle, samGoogle}, sessionIdentities, "folding keeps every session signed in")
	codes, err := db.NewSelect().Table("mobile_sign_in_codes").Where("identity_id = ?", samGoogle).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, codes)
	var person models.Person
	require.NoError(t, db.NewSelect().Model(&person).Column("update_identity_id", "email_updates").Where("id = ?", alex).Scan(ctx))
	require.NotNil(t, person.UpdateIdentityID)
	assert.Equal(t, alexGoogle, *person.UpdateIdentityID)
	assert.True(t, person.EmailUpdates)
	codes, err = db.NewSelect().Table("mobile_sign_in_codes").Where("identity_id = ?", alexGoogle).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, codes)
	var approved []string
	require.NoError(t, db.NewSelect().Table("preauthorizations").Column("email").Scan(ctx, &approved))
	assert.Equal(t, []string{"second@example.test", "second@example.test"}, approved)
	var requested []models.UUID
	require.NoError(t, db.NewSelect().Table("access_requests").Column("id").Where("kind = 'join'").Scan(ctx, &requested))
	assert.Equal(t, []models.UUID{newerRequest}, requested, "open join requests for one address keep the most recent")
	var albumRequest string
	require.NoError(t, db.NewSelect().Table("access_requests").Column("email").Where("kind = 'album'").Scan(ctx, &albumRequest))
	assert.Equal(t, "sam@example.test", albumRequest)
	for _, table := range []string{"identities", "access_requests"} {
		columns, err := db.NewSelect().Table("information_schema.columns").Where("table_schema = current_schema() AND table_name = ? AND column_name IN ('provider', 'subject')", table).Count(ctx)
		require.NoError(t, err)
		assert.Zero(t, columns, table)
	}

	// The database, not Go, keeps every linked address lowercased and unique.
	_, err = db.ExecContext(ctx, `INSERT INTO identities (id, person_id, email, created_at) VALUES (?, ?, 'sam@example.test', ?)`, models.NewUUIDv7(), alex, now)
	require.Error(t, err, "a second Person cannot link an address someone holds")
	_, err = db.ExecContext(ctx, `INSERT INTO identities (id, person_id, email, created_at) VALUES (?, ?, 'Mixed@example.test', ?)`, models.NewUUIDv7(), alex, now)
	require.Error(t, err, "addresses are stored lowercased")
	_, err = db.ExecContext(ctx, `INSERT INTO access_requests (id, kind, email, email_verified, display_name, status, created_at, updated_at) VALUES (?, 'join', 'stranger@example.test', true, 'Again', 'pending', ?, ?)`, models.NewUUIDv7(), now, now)
	require.Error(t, err, "one open join request per address")
}

func hash(seed byte) []byte {
	value := make([]byte, 32)
	value[0] = seed
	return value
}
