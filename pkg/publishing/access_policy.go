package publishing

import (
	"maps"
	"sort"
)

// accessFacts is the complete input needed by recommendation and structural
// access review. Detections contain linked, active Memento Person IDs only.
type accessFacts struct {
	EntryMoments map[string]string
	Detections   map[string][]string
	Decisions    map[string]map[string]Decision
}

func (f accessFacts) clone() accessFacts {
	result := accessFacts{
		EntryMoments: make(map[string]string, len(f.EntryMoments)),
		Detections:   make(map[string][]string, len(f.Detections)),
		Decisions:    make(map[string]map[string]Decision, len(f.Decisions)),
	}
	maps.Copy(result.EntryMoments, f.EntryMoments)
	for id, people := range f.Detections {
		result.Detections[id] = append([]string(nil), people...)
	}
	for momentID, decisions := range f.Decisions {
		result.Decisions[momentID] = make(map[string]Decision, len(decisions))
		maps.Copy(result.Decisions[momentID], decisions)
	}
	return result
}

func recommendations(facts accessFacts, momentID string) []string {
	seen := map[string]bool{}
	for entryID, entryMomentID := range facts.EntryMoments {
		if entryMomentID != momentID {
			continue
		}
		for _, personID := range facts.Detections[entryID] {
			if _, decided := facts.Decisions[momentID][personID]; !decided {
				seen[personID] = true
			}
		}
	}
	result := make([]string, 0, len(seen))
	for personID := range seen {
		result = append(result, personID)
	}
	sort.Strings(result)
	return result
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
