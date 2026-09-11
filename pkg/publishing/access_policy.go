package publishing

import "sort"

// accessFacts is the input to structural access review: which Moment each
// Album Entry belongs to and each Moment's explicit decisions. Recommendations
// themselves are derived in SQL by accessByMoment from the same membership.
type accessFacts struct {
	EntryMoments map[string]string
	Decisions    map[string]map[string]Decision
}

func visibleEntries(facts accessFacts, personID string) []string {
	result := []string{}
	for entryID, momentID := range facts.EntryMoments {
		if facts.Decisions[momentID][personID] == DecisionAllow {
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
