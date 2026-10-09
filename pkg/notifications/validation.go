package notifications

import "strings"

// ValidationMessage keeps approval failures in Curator words. A stale or
// malformed row means the preview must be reviewed again, never edited by hand.
func (ApproveRequest) ValidationMessage(field, rule string) string {
	// List elements arrive indexed, such as excluded_album_ids[0].
	field, _, _ = strings.Cut(field, "[")
	switch field {
	case "note":
		if rule == "max" {
			return "Keep the note under 1000 characters."
		}
	case "people":
		if rule == "required" || rule == "min" {
			return "Include at least one person."
		}
		return "Review the updates again before continuing."
	case "person_id", "review_token", "excluded_album_ids", "excluded_offered_album_ids":
		return "Review the updates again before continuing."
	}
	return ""
}

func (DismissRequest) ValidationMessage(field, rule string) string {
	return (ApproveRequest{}).ValidationMessage(field, rule)
}
