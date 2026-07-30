//go:build integration

package restores

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateUsesReadOnlySnapshotAndReturnsRepresentativeCounts(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	require.NoError(t, migrations.Apply(ctx, db))

	var beforeSettings, beforeJobs, beforeOutbox string
	require.NoError(t, db.NewRaw(`SELECT row_to_json(settings)::text FROM system_settings AS settings WHERE id = 1`).Scan(ctx, &beforeSettings))
	require.NoError(t, db.NewRaw(`SELECT COALESCE(json_agg(job ORDER BY id)::text, '[]') FROM jobs AS job`).Scan(ctx, &beforeJobs))
	require.NoError(t, db.NewRaw(`SELECT COALESCE(json_agg(event ORDER BY id)::text, '[]') FROM outbox_events AS event`).Scan(ctx, &beforeOutbox))

	result, err := Validate(ctx, db)
	require.NoError(t, err)
	assert.Equal(t, "valid", result.Status)
	assert.Equal(t, []string{"migrations", "extensions", "setup_and_sole_curator", "foreign_keys", "projections", "security_settings"}, result.Checks)
	assert.Equal(t, 1, result.Counts.Jobs)
	assert.Zero(t, result.Counts.People)
	_, err = json.Marshal(result)
	require.NoError(t, err)

	var afterSettings, afterJobs, afterOutbox string
	require.NoError(t, db.NewRaw(`SELECT row_to_json(settings)::text FROM system_settings AS settings WHERE id = 1`).Scan(ctx, &afterSettings))
	require.NoError(t, db.NewRaw(`SELECT COALESCE(json_agg(job ORDER BY id)::text, '[]') FROM jobs AS job`).Scan(ctx, &afterJobs))
	require.NoError(t, db.NewRaw(`SELECT COALESCE(json_agg(event ORDER BY id)::text, '[]') FROM outbox_events AS event`).Scan(ctx, &afterOutbox))
	assert.Equal(t, beforeSettings, afterSettings)
	assert.Equal(t, beforeJobs, afterJobs)
	assert.Equal(t, beforeOutbox, afterOutbox)
}
