package events

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCaptureDayUsesExplicitTimezoneAndLeavesUnknownCaptureDatesUnassigned(t *testing.T) {
	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	zoned := "2026-05-02T00:30:00+14:00"
	day, instant := captureDay(&zoned, losAngeles)
	require.NotNil(t, day)
	assert.Equal(t, "2026-05-02", *day)
	require.NotNil(t, instant)
	assert.Equal(t, "2026-05-01T10:30:00Z", instant.Format(time.RFC3339))

	unzoned := "2026-05-01T22:30:00"
	day, instant = captureDay(&unzoned, losAngeles)
	require.NotNil(t, day)
	assert.Equal(t, "2026-05-01", *day)
	require.NotNil(t, instant)
	assert.Equal(t, "2026-05-02T05:30:00Z", instant.Format(time.RFC3339))

	for _, unknown := range []string{"not-a-capture-date", "0000-01-01T00:00:00Z", "0000-01-01T00:00:00"} {
		day, instant = captureDay(&unknown, losAngeles)
		assert.Nil(t, day)
		assert.Nil(t, instant)
	}
	day, instant = captureDay(nil, losAngeles)
	assert.Nil(t, day)
	assert.Nil(t, instant)
}

func TestCreateEventRejectsTooManySourcesBeforeDatabaseAccess(t *testing.T) {
	sourceIDs := make([]string, maxDraftSourceAlbums+1)
	for index := range sourceIDs {
		sourceIDs[index] = uuid.NewString()
	}
	_, err := new(Service).CreateEvent(t.Context(), setup.CuratorSession{}, CreateEventRequest{
		SourceAlbumIDs: sourceIDs,
		Timezone:       "UTC",
	})
	require.ErrorIs(t, err, ErrInvalid)
}

func TestParseUniqueIDsRejectsMalformedAndDuplicatePortalIdentities(t *testing.T) {
	_, err := parseUniqueIDs([]string{"not-an-id"})
	require.ErrorIs(t, err, ErrInvalid)
	id := "11111111-1111-4111-8111-111111111111"
	_, err = parseUniqueIDs([]string{id, id})
	require.ErrorIs(t, err, ErrInvalid)
	ids, err := parseUniqueIDs(nil)
	require.NoError(t, err)
	assert.Empty(t, ids)
}
