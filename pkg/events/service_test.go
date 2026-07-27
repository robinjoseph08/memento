package events

import (
	"testing"
	"time"

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

	unknown := "not-a-capture-date"
	day, instant = captureDay(&unknown, losAngeles)
	assert.Nil(t, day)
	assert.Nil(t, instant)
	day, instant = captureDay(nil, losAngeles)
	assert.Nil(t, day)
	assert.Nil(t, instant)
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
