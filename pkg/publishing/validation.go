package publishing

import (
	"strings"
	"unicode/utf8"

	"github.com/robinjoseph08/memento/pkg/errcodes"
)

func validDecision(decision Decision) bool {
	return decision == DecisionAllow || decision == DecisionDeny
}

func structureField(field, message string) error {
	return errcodes.ValidationFields("Check the highlighted fields.", map[string]string{field: message})
}

func normalizedStructureTitle(field, title string) (string, error) {
	title = strings.TrimSpace(title)
	if utf8.RuneCountInString(title) > 200 {
		return "", structureField(field, "Enter a title of no more than 200 characters.")
	}
	return title, nil
}

func (UpdateMomentRequest) ValidationMessage(field, rule string) string {
	if field == "title" && rule == "max" {
		return "Use 200 characters or fewer."
	}
	return ""
}

func (SetMomentCoverRequest) ValidationMessage(field, _ string) string {
	if field == "entry_id" {
		return "Choose media from this Moment."
	}
	return ""
}

func (SetMomentAccessRequest) ValidationMessage(field, _ string) string {
	switch field {
	case "person_id":
		return "Choose a Person."
	case "decision":
		return "Choose allow or exclude."
	}
	return ""
}

func (MoveEntriesRequest) ValidationMessage(field, _ string) string {
	switch field {
	case "entry_ids":
		return "Select media from this Moment."
	case "destination_moment_id":
		return "Choose another Moment in this Album."
	case "replacement_cover_entry_id":
		return "Choose a replacement cover from the media staying in this Moment."
	}
	return ""
}

func (SplitMomentRequest) ValidationMessage(field, rule string) string {
	if field == "new_title" && rule == "max" {
		return "Use 200 characters or fewer."
	}
	switch field {
	case "entry_ids":
		return "Select media from this Moment."
	case "new_cover_entry_id":
		return "Choose a cover from the selected media."
	case "replacement_cover_entry_id":
		return "Choose a replacement cover from the media staying in this Moment."
	}
	return ""
}

func (MergeMomentsRequest) ValidationMessage(field, rule string) string {
	if field == "title" && rule == "max" {
		return "Use 200 characters or fewer."
	}
	switch field {
	case "target_moment_id":
		return "Choose another Moment in this Album."
	case "cover_entry_id":
		return "Choose a cover from the merged media."
	case "resolutions":
		return "Choose one combined access decision for each conflict."
	}
	return ""
}
