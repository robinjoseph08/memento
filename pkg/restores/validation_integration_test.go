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
	assert.Equal(t, 2, result.Counts.Jobs)
	assert.Zero(t, result.Counts.People)
	_, err = json.Marshal(result)
	require.NoError(t, err)

	assert.Equal(t, before, databaseFingerprint(t, db), "validation must not mutate any table or sequence")
}

func TestValidateRejectsCorruptRestoreState(t *testing.T) {
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(context.Background(), db))

	t.Run("missing foreign key", func(t *testing.T) {
		validateMutation(t, db, ErrForeignKeys, func(ctx context.Context, tx bun.Tx) error {
			_, err := tx.NewRaw(`ALTER TABLE sessions DROP CONSTRAINT sessions_person_id_fkey`).Exec(ctx)
			return err
		})
	})

	t.Run("null current publication pointer", func(t *testing.T) {
		validateMutation(t, db, ErrProjections, func(ctx context.Context, tx bun.Tx) error {
			personID, eventID, publicationID := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"
			_, err := tx.NewRaw(`
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
			return err
		})
	})

	t.Run("selected Event cover outside its presentation", func(t *testing.T) {
		validateMutation(t, db, ErrProjections, func(ctx context.Context, tx bun.Tx) error {
			_, err := tx.NewRaw(`
				INSERT INTO media_items (id, immich_asset_id, media_type, first_seen_at, last_seen_at)
				VALUES
				 ('11111111-1111-4111-8111-111111111111', '21111111-1111-4111-8111-111111111111', 'image', now(), now()),
				 ('12222222-2222-4222-8222-222222222222', '22222222-2222-4222-8222-222222222222', 'image', now(), now());
				INSERT INTO events (id, title, grouping_timezone)
				VALUES ('33333333-3333-4333-8333-333333333333', 'Invalid cover', 'UTC');
				INSERT INTO draft_media_placements (event_id, media_item_id, position, created_at)
				VALUES ('33333333-3333-4333-8333-333333333333', '11111111-1111-4111-8111-111111111111', 0, now());
				UPDATE events SET selected_cover_media_item_id = '12222222-2222-4222-8222-222222222222'
				WHERE id = '33333333-3333-4333-8333-333333333333'
			`).Exec(ctx)
			return err
		})
	})

	t.Run("stale staged net change", func(t *testing.T) {
		validateMutation(t, db, ErrProjections, func(ctx context.Context, tx bun.Tx) error {
			personID, eventID := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
			publicationID, stagedID := "33333333-3333-4333-8333-333333333333", "44444444-4444-4444-8444-444444444444"
			_, err := tx.NewRaw(`
				INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Publisher', 'publisher');
				INSERT INTO events (id, lifecycle, title, grouping_timezone, current_publication_id)
				 VALUES (?, 'published', 'Edited title', 'UTC', NULL);
				INSERT INTO publications
				 (id, event_id, revision, editable_version, published_by_person_id, notify_recipients, committed_at)
				VALUES (?, ?, 1, 1, ?, false, now());
				UPDATE events SET current_publication_id = ? WHERE id = ?;
				INSERT INTO published_event_revisions
				 (publication_id, event_id, title, description, grouping_timezone, created_at)
				VALUES (?, ?, 'Published title', '', 'UTC', now());
				INSERT INTO current_published_events
				 (event_id, publication_id, title, description, grouping_timezone, committed_at)
				VALUES (?, ?, 'Published title', '', 'UTC', now());
				INSERT INTO staged_updates (id, event_id, base_publication_id, net_changes, created_at, updated_at)
				VALUES (?, ?, ?, '[]'::jsonb, now(), now());
				UPDATE events SET current_staged_update_id = ? WHERE id = ?
			`, personID, eventID, publicationID, eventID, personID, publicationID, eventID,
				publicationID, eventID, eventID, publicationID, stagedID, eventID, publicationID, stagedID, eventID).Exec(ctx)
			return err
		})
	})

	t.Run("missing current Loose revision", func(t *testing.T) {
		validateMutation(t, db, ErrProjections, func(ctx context.Context, tx bun.Tx) error {
			_, err := tx.NewRaw(`
				INSERT INTO people (id, display_name, sort_name)
				VALUES ('11111111-1111-4111-8111-111111111111', 'Publisher', 'publisher');
				INSERT INTO media_items (id, immich_asset_id, media_type, width, height,
					local_date_time, availability, first_seen_at, last_seen_at)
				VALUES ('22222222-2222-4222-8222-222222222222',
				 '33333333-3333-4333-8333-333333333333', 'image', 1, 1,
				 '2026-08-01T00:00:00Z', 'current', now(), now());
				INSERT INTO loose_items (id, media_item_id, lifecycle, title, grouping_timezone)
				VALUES ('44444444-4444-4444-8444-444444444444',
				 '22222222-2222-4222-8222-222222222222', 'published', 'Loose', 'UTC');
				INSERT INTO publications (id, loose_item_id, revision, editable_version,
				 published_by_person_id, notify_recipients, committed_at)
				VALUES ('55555555-5555-4555-8555-555555555555',
				 '44444444-4444-4444-8444-444444444444', 1, 1,
				 '11111111-1111-4111-8111-111111111111', false, now());
				UPDATE loose_items SET current_publication_id =
				 '55555555-5555-4555-8555-555555555555' WHERE id =
				 '44444444-4444-4444-8444-444444444444';
				INSERT INTO current_published_loose_items (loose_item_id, publication_id,
				 media_item_id, title, description, grouping_timezone, media_type, committed_at)
				VALUES ('44444444-4444-4444-8444-444444444444',
				 '55555555-5555-4555-8555-555555555555',
				 '22222222-2222-4222-8222-222222222222', 'Loose', '', 'UTC', 'image', now())`).Exec(ctx)
			return err
		})
	})

	t.Run("corrupt current Loose commit timestamp", func(t *testing.T) {
		validateMutation(t, db, ErrProjections, func(ctx context.Context, tx bun.Tx) error {
			_, err := tx.NewRaw(`
				INSERT INTO people (id, display_name, sort_name)
				VALUES ('11111111-1111-4111-8111-111111111111', 'Publisher', 'publisher');
				INSERT INTO media_items (id, immich_asset_id, media_type, width, height,
					local_date_time, availability, first_seen_at, last_seen_at)
				VALUES ('22222222-2222-4222-8222-222222222222',
				 '33333333-3333-4333-8333-333333333333', 'image', 1, 1,
				 '2026-08-01T00:00:00Z', 'current', now(), now());
				INSERT INTO loose_items (id, media_item_id, lifecycle, title, grouping_timezone)
				VALUES ('44444444-4444-4444-8444-444444444444',
				 '22222222-2222-4222-8222-222222222222', 'published', 'Loose', 'UTC');
				INSERT INTO audience_snapshots (id, target_kind, target_id, approved_by_person_id,
				 approved_at, label) VALUES ('66666666-6666-4666-8666-666666666666',
				 'loose_item', '44444444-4444-4444-8444-444444444444',
				 '11111111-1111-4111-8111-111111111111', now(), 'Curator only');
				INSERT INTO publications (id, loose_item_id, revision, editable_version,
				 published_by_person_id, notify_recipients, committed_at)
				VALUES ('55555555-5555-4555-8555-555555555555',
				 '44444444-4444-4444-8444-444444444444', 1, 1,
				 '11111111-1111-4111-8111-111111111111', false, '2026-08-01T00:00:00Z');
				UPDATE loose_items SET current_publication_id =
				 '55555555-5555-4555-8555-555555555555' WHERE id =
				 '44444444-4444-4444-8444-444444444444';
				INSERT INTO published_loose_item_revisions (publication_id, loose_item_id,
				 media_item_id, audience_snapshot_id, title, description, grouping_timezone,
				 media_type, created_at) VALUES ('55555555-5555-4555-8555-555555555555',
				 '44444444-4444-4444-8444-444444444444',
				 '22222222-2222-4222-8222-222222222222',
				 '66666666-6666-4666-8666-666666666666', 'Loose', '', 'UTC', 'image', now());
				INSERT INTO current_published_loose_items (loose_item_id, publication_id,
				 media_item_id, title, description, grouping_timezone, media_type, committed_at)
				VALUES ('44444444-4444-4444-8444-444444444444',
				 '55555555-5555-4555-8555-555555555555',
				 '22222222-2222-4222-8222-222222222222', 'Loose', '', 'UTC', 'image',
				 '2026-08-02T00:00:00Z')`).Exec(ctx)
			return err
		})
	})

	t.Run("effective entitlement view", func(t *testing.T) {
		validateMutation(t, db, ErrProjections, func(ctx context.Context, tx bun.Tx) error {
			_, err := tx.NewRaw(`CREATE OR REPLACE VIEW current_media_entitlements AS SELECT
				'loose_item'::text AS origin_kind,
				'11111111-1111-4111-8111-111111111111'::uuid AS origin_id,
				'22222222-2222-4222-8222-222222222222'::uuid AS publication_id,
				'33333333-3333-4333-8333-333333333333'::uuid AS recipient_person_id,
				'44444444-4444-4444-8444-444444444444'::uuid AS recipient_access_generation_id,
				'55555555-5555-4555-8555-555555555555'::uuid AS media_item_id,
				0::integer AS position`).Exec(ctx)
			return err
		})
	})

	t.Run("Recovery delivery view", func(t *testing.T) {
		validateMutation(t, db, ErrSecurity, func(ctx context.Context, tx bun.Tx) error {
			_, err := tx.NewRaw(`CREATE OR REPLACE VIEW recovery_curator_sign_in_deliveries AS
				SELECT id, public_id FROM email_deliveries`).Exec(ctx)
			return err
		})
	})

	t.Run("Withdrawal function", func(t *testing.T) {
		validateMutation(t, db, ErrSecurity, func(ctx context.Context, tx bun.Tx) error {
			_, err := tx.NewRaw(`CREATE OR REPLACE FUNCTION content_is_withdrawn(event_id uuid, moment_id uuid, media_id uuid)
				RETURNS boolean LANGUAGE sql STABLE PARALLEL SAFE RETURN false`).Exec(ctx)
			return err
		})
	})
}

func validateMutation(t *testing.T, db *bun.DB, expected error, mutate func(context.Context, bun.Tx) error) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, tx.Rollback()) }()
	require.NoError(t, mutate(ctx, tx))
	_, err = validateSnapshot(ctx, tx)
	assert.ErrorIs(t, err, expected)
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
