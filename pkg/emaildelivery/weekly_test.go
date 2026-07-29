package emaildelivery

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNextWeeklyDeliveryUsesRecipientLocalSchedule(t *testing.T) {
	t.Parallel()

	schedule, err := parseWeeklySchedule("sunday", "09:00", "America/New_York")
	require.NoError(t, err)

	assert.Equal(t,
		time.Date(2026, time.March, 8, 13, 0, 0, 0, time.UTC),
		schedule.Next(time.Date(2026, time.March, 7, 18, 0, 0, 0, time.UTC)),
		"the spring daylight transition changes the UTC offset without changing 09:00 local time",
	)
	assert.Equal(t,
		time.Date(2026, time.March, 15, 13, 0, 0, 0, time.UTC),
		schedule.Next(time.Date(2026, time.March, 8, 13, 0, 0, 0, time.UTC)),
		"activity exactly at a digest boundary belongs to the next weekly digest",
	)
}

func TestNextWeeklyDeliveryHandlesSkippedAndRepeatedWallTimes(t *testing.T) {
	t.Parallel()

	skipped, err := parseWeeklySchedule("sunday", "02:30", "America/New_York")
	require.NoError(t, err)
	assert.Equal(t,
		time.Date(2026, time.March, 8, 7, 0, 0, 0, time.UTC),
		skipped.Next(time.Date(2026, time.March, 7, 12, 0, 0, 0, time.UTC)),
		"a nonexistent wall time runs at the first valid local minute after the gap",
	)

	repeated, err := parseWeeklySchedule("sunday", "01:30", "America/New_York")
	require.NoError(t, err)
	assert.Equal(t,
		time.Date(2026, time.November, 1, 5, 30, 0, 0, time.UTC),
		repeated.Next(time.Date(2026, time.October, 31, 12, 0, 0, 0, time.UTC)),
		"a repeated wall time runs once at its first occurrence",
	)
}

func TestWeeklyScheduleValidationRejectsAmbiguousInput(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, day, localTime, timezone string
	}{
		{name: "day", day: "funday", localTime: "09:00", timezone: "UTC"},
		{name: "time", day: "sunday", localTime: "9:00", timezone: "UTC"},
		{name: "seconds", day: "sunday", localTime: "09:00:00", timezone: "UTC"},
		{name: "timezone", day: "sunday", localTime: "09:00", timezone: "Local"},
		{name: "fixed offset", day: "sunday", localTime: "09:00", timezone: "GMT+5"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseWeeklySchedule(test.day, test.localTime, test.timezone)
			assert.ErrorIs(t, err, ErrNotificationPreference)
		})
	}
}
