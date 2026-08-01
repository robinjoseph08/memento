// Package localcapture parses normalized local capture timestamps without
// inventing timezone information.
package localcapture

import (
	"strings"
	"time"

	// Embed IANA timezone data for the minimal production image.
	_ "time/tzdata"
)

// Parse returns the capture's written local day and comparable instant. Zoned
// values retain their written day. Unzoned values use the Curator-selected
// location, and unusable values remain unknown.
func Parse(raw *string, location *time.Location) (*string, *time.Time) {
	if raw == nil || location == nil || strings.TrimSpace(*raw) == "" {
		return nil, nil
	}
	value := strings.TrimSpace(*raw)
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil && parsed.Year() > 0 {
		day := parsed.Format(time.DateOnly)
		instant := parsed.UTC()
		return &day, &instant
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05", "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, value, location); err == nil && parsed.Year() > 0 {
			day := parsed.Format(time.DateOnly)
			instant := parsed.UTC()
			return &day, &instant
		}
	}
	return nil, nil
}
