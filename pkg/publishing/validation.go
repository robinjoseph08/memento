package publishing

import (
	"strings"
	"unicode/utf8"

	"github.com/robinjoseph08/memento/pkg/errcodes"
)

func (SaveAlbumAccessRequest) ValidationMessage(field, _ string) string {
	switch field {
	case "people":
		return "Choose the people to review."
	case "person_id":
		return "Choose active non-Curator Persons."
	case "circles", "circle_id":
		return "Choose existing Circles."
	}
	return ""
}

func (PublishRequest) ValidationMessage(field, _ string) string {
	if field == "review_token" {
		return "Review this Album before publishing."
	}
	return ""
}

const deleteTitleMessage = "Type the Album title exactly to confirm deletion."

func (DeleteAlbumRequest) ValidationMessage(field, _ string) string {
	if field == "title" {
		return deleteTitleMessage
	}
	return ""
}

func validDecision(decision Decision) bool {
	return decision == DecisionAllow || decision == DecisionDeny
}

// validOfferDecision admits a Circle's offer, withhold, or inherit.
func validOfferDecision(decision OfferDecision) bool {
	return decision == OfferDecisionOffer || decision == OfferDecisionWithhold || decision == OfferDecisionInherit
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

func (SaveRulesRequest) ValidationMessage(field, _ string) string {
	switch field {
	case "decisions":
		return "Choose one rule for each Person."
	case "person_id":
		return "Choose a Person."
	case "circles", "circle_id":
		return "Choose one Offer for each Circle."
	case "decision":
		return "Choose one rule for each Person and Circle."
	}
	return ""
}

func (MoveEntriesRequest) ValidationMessage(field, _ string) string {
	switch field {
	case "entry_ids":
		return "Select media from this Moment."
	case "destination_moment_id":
		return "Choose another Moment in this Album."
	}
	return ""
}

func (SplitMomentRequest) ValidationMessage(field, rule string) string {
	if field == "new_title" && rule == "max" {
		return "Use 200 characters or fewer."
	}
	if field == "entry_ids" {
		return "Select media from this Moment."
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
	case "resolutions", "person_id", "decision":
		return "Choose one combined access decision for each conflict."
	case "circle_resolutions", "circle_id":
		return "Choose one combined Offer for each Circle."
	}
	return ""
}

func (SaveCoverOrderRequest) ValidationMessage(field, rule string) string {
	if field == "moment_ids" {
		return "Choose Moments from this Album."
	}
	return ""
}

func (UpdateVideoRequest) ValidationMessage(field, rule string) string {
	if field == "title" && rule == "max" {
		return "Use 200 characters or fewer."
	}
	return ""
}
