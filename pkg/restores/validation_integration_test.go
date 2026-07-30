//go:build integration

package restores

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

func TestValidateUsesReadOnlySnapshotAndReturnsRepresentativeCounts(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	require.NoError(t, migrations.Apply(ctx, db))
	before := databaseFingerprint(t, db)

	result, err := Validate(ctx, db)
	require.NoError(t, err)
	assert.Equal(t, "valid", result.Status)
	assert.Equal(t, []string{"migrations", "extensions", "setup_and_sole_curator", "foreign_keys", "projections", "security_settings"}, result.Checks)
	assert.Equal(t, 1, result.Counts.Jobs)
	assert.Zero(t, result.Counts.People)
	_, err = json.Marshal(result)
	require.NoError(t, err)

	assert.Equal(t, before, databaseFingerprint(t, db), "validation must not mutate any table or sequence")
}

func TestValidateRejectsMissingForeignKeyAndNullCurrentPublicationProjection(t *testing.T) {
	t.Run("missing foreign key", func(t *testing.T) {
		db := testdb.Open(t)
		ctx := context.Background()
		require.NoError(t, migrations.Apply(ctx, db))
		_, err := db.NewRaw(`ALTER TABLE sessions DROP CONSTRAINT sessions_person_id_fkey`).Exec(ctx)
		require.NoError(t, err)
		_, err = Validate(ctx, db)
		assert.ErrorIs(t, err, ErrForeignKeys)
	})

	t.Run("null current publication pointer", func(t *testing.T) {
		db := testdb.Open(t)
		ctx := context.Background()
		require.NoError(t, migrations.Apply(ctx, db))
		personID, eventID, publicationID := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"
		_, err := db.NewRaw(`
			INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Publisher', 'publisher');
			INSERT INTO events (id, lifecycle, title, grouping_timezone) VALUES (?, 'published', 'Restored Event', 'UTC');
			INSERT INTO publications
			 (id, event_id, revision, editable_version, published_by_person_id, notify_recipients, committed_at)
			VALUES (?, ?, 1, 1, ?, false, now());
			INSERT INTO published_event_revisions
			 (publication_id, event_id, title, description, grouping_timezone, created_at)
			VALUES (?, ?, 'Restored Event', '', 'UTC', now());
			INSERT INTO current_published_events
			 (event_id, publication_id, title, description, grouping_timezone, committed_at)
			VALUES (?, ?, 'Restored Event', '', 'UTC', now())
		`, personID, eventID, publicationID, eventID, personID, publicationID, eventID, eventID, publicationID).Exec(ctx)
		require.NoError(t, err)
		_, err = Validate(ctx, db)
		assert.ErrorIs(t, err, ErrProjections)
	})
}

func databaseFingerprint(t *testing.T, db *bun.DB) map[string]string {
	t.Helper()
	ctx := context.Background()
	var tables []string
	require.NoError(t, db.NewRaw(`SELECT table_name FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_type = 'BASE TABLE' ORDER BY table_name`).Scan(ctx, &tables))
	result := make(map[string]string, len(tables)+1)
	for _, table := range tables {
		identifier := `"` + strings.ReplaceAll(table, `"`, `""`) + `"`
		var fingerprint string
		require.NoError(t, db.NewRaw(fmt.Sprintf(`SELECT COALESCE(md5(string_agg(row_to_json(snapshot)::text, E'\n'
			ORDER BY row_to_json(snapshot)::text)), '') FROM %s AS snapshot`, identifier)).Scan(ctx, &fingerprint))
		result[table] = fingerprint
	}
	var sequences string
	require.NoError(t, db.NewRaw(`SELECT COALESCE(md5(string_agg(sequencename || ':' || COALESCE(last_value::text, 'null'), E'\n'
		ORDER BY sequencename)), '') FROM pg_sequences WHERE schemaname = current_schema()`).Scan(ctx, &sequences))
	result["<sequences>"] = sequences
	return result
}
