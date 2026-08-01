package localcapture

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePreservesWrittenLocalDayAndUsesChosenTimezoneForUnzonedInstant(t *testing.T) {
	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	zoned := "2026-01-02T23:00:00-10:00"
	day, instant := Parse(&zoned, losAngeles)
	require.NotNil(t, day)
	require.NotNil(t, instant)
	assert.Equal(t, "2026-01-02", *day)
	assert.Equal(t, "2026-01-03T09:00:00Z", instant.Format(time.RFC3339))

	unzoned := "2026-01-02T01:00:00"
	day, instant = Parse(&unzoned, losAngeles)
	require.NotNil(t, day)
	require.NotNil(t, instant)
	assert.Equal(t, "2026-01-02", *day)
	assert.Equal(t, "2026-01-02T09:00:00Z", instant.Format(time.RFC3339))
}

func TestParseLeavesUnknownAndUnusableValuesUnassigned(t *testing.T) {
	for _, raw := range []*string{nil, pointer(""), pointer("yesterday"), pointer("0000-01-01T00:00:00Z")} {
		day, instant := Parse(raw, time.UTC)
		assert.Nil(t, day)
		assert.Nil(t, instant)
	}
	day, instant := Parse(pointer("2026-01-01T10:00:00"), nil)
	assert.Nil(t, day)
	assert.Nil(t, instant)
}

func pointer(value string) *string { return &value }
