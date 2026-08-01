//go:build integration

package migrations

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventPresentationDateAndCoverConstraints(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, db))
	eventID := uuid.New()
	_, err := db.NewRaw(`INSERT INTO events (id, title, grouping_timezone)
		VALUES (?, 'Presentation constraints', 'UTC')`, eventID).Exec(ctx)
	require.NoError(t, err)

	_, err = db.NewRaw(`UPDATE events SET date_start = '2026-08-01' WHERE id = ?`, eventID).Exec(ctx)
	assert.Error(t, err, "a partial date range must be rejected")
	_, err = db.NewRaw(`UPDATE events SET date_start = '2026-08-03', date_end = '2026-08-01' WHERE id = ?`, eventID).Exec(ctx)
	assert.Error(t, err, "a reversed date range must be rejected")
	_, err = db.NewRaw(`UPDATE events SET selected_cover_media_item_id = ? WHERE id = ?`, uuid.New(), eventID).Exec(ctx)
	assert.Error(t, err, "the selected cover must retain a real Media identity")

	var dateStart, dateEnd, cover *string
	require.NoError(t, db.NewRaw(`SELECT date_start::text, date_end::text,
		selected_cover_media_item_id::text FROM events WHERE id = ?`, eventID).Scan(ctx, &dateStart, &dateEnd, &cover))
	assert.Nil(t, dateStart)
	assert.Nil(t, dateEnd)
	assert.Nil(t, cover)
}
