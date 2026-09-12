package publishing

import "sort"

// entryAllowed resolves the most specific saved rule. Missing rules inherit.
func entryAllowed(album, moment, entry Decision) bool {
	for _, decision := range []Decision{entry, moment, album} {
		if decision == DecisionAllow {
			return true
		}
		if decision == DecisionDeny {
			return false
		}
	}
	return false
}

// accessFacts supplies membership and saved rules to structural audience review.
type accessFacts struct {
	EntryMoments   map[string]string
	Decisions      map[string]map[string]Decision
	AlbumDecisions map[string]Decision
	EntryDecisions map[string]map[string]Decision
}

func visibleEntries(facts accessFacts, personID string) []string {
	result := []string{}
	for entryID, momentID := range facts.EntryMoments {
		if entryAllowed(facts.AlbumDecisions[personID], facts.Decisions[momentID][personID], facts.EntryDecisions[entryID][personID]) {
			result = append(result, entryID)
		}
	}
	sort.Strings(result)
	return result
}

func audienceChanges(before, after accessFacts, personIDs []string) []AudienceChange {
	result := []AudienceChange{}
	for _, personID := range personIDs {
		oldEntries := visibleEntries(before, personID)
		newEntries := visibleEntries(after, personID)
		oldSet, newSet := map[string]bool{}, map[string]bool{}
		for _, id := range oldEntries {
			oldSet[id] = true
		}
		for _, id := range newEntries {
			newSet[id] = true
		}
		change := AudienceChange{PersonID: personID, GainedEntryIDs: []string{}, LostEntryIDs: []string{}}
		for _, id := range newEntries {
			if !oldSet[id] {
				change.GainedEntryIDs = append(change.GainedEntryIDs, id)
			}
		}
		for _, id := range oldEntries {
			if !newSet[id] {
				change.LostEntryIDs = append(change.LostEntryIDs, id)
			}
		}
		if len(change.GainedEntryIDs) > 0 || len(change.LostEntryIDs) > 0 {
			result = append(result, change)
		}
	}
	return result
}
