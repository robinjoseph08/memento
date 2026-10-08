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

// undecided reports that none of a Person's own rules applies to an entry,
// which is the only case where an Offer counts (ADR 0015).
func undecided(album, moment, entry Decision) bool {
	for _, decision := range []Decision{entry, moment, album} {
		if decision == DecisionAllow || decision == DecisionDeny {
			return false
		}
	}
	return true
}

// accessFacts supplies membership, saved rules, and Offers to structural
// audience review. Offers are reviewed as they apply after publication.
type accessFacts struct {
	EntryMoments   map[string]string
	Decisions      map[string]map[string]Decision
	AlbumDecisions map[string]Decision
	EntryDecisions map[string]map[string]Decision
	// AlbumOffers holds the Circles the Album is offered to.
	AlbumOffers map[string]bool
	// MomentOffers holds each Moment's offers and withholds by Circle.
	MomentOffers map[string]map[string]OfferDecision
	// Circles maps each active Person to the Circles they belong to.
	Circles map[string][]string
	// Joins holds the active People who joined the Album.
	Joins map[string]bool
}

// circleOffered resolves one Circle's Offer for a Moment: the Moment's own
// offer or withhold first, then the Album Offer.
func (facts accessFacts) circleOffered(momentID, circleID string) bool {
	return offerResolved(facts.MomentOffers[momentID][circleID], facts.AlbumOffers[circleID])
}

// offerResolved applies a Moment's own offer or withhold, if any, over the
// Album Offer.
func offerResolved(moment OfferDecision, albumOffered bool) bool {
	if moment == OfferDecisionOffer || moment == OfferDecisionWithhold {
		return moment == OfferDecisionOffer
	}
	return albumOffered
}

// offered reports whether any of the Person's Circles is offered the Moment,
// so a withhold for one Circle never blocks another Circle's Offer.
func (facts accessFacts) offered(personID, momentID string) bool {
	for _, circleID := range facts.Circles[personID] {
		if facts.circleOffered(momentID, circleID) {
			return true
		}
	}
	return false
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

// offeredEntries lists the entries an Offer reaches for the Person: those no
// rule of their own decides.
func offeredEntries(facts accessFacts, personID string) []string {
	result := []string{}
	for entryID, momentID := range facts.EntryMoments {
		if facts.offered(personID, momentID) && undecided(facts.AlbumDecisions[personID], facts.Decisions[momentID][personID], facts.EntryDecisions[entryID][personID]) {
			result = append(result, entryID)
		}
	}
	sort.Strings(result)
	return result
}

// ownEntries lists the entries in the Person's own Album: those granted
// directly, and offered ones once they joined.
func ownEntries(facts accessFacts, personID string) []string {
	result := visibleEntries(facts, personID)
	if facts.Joins[personID] {
		result = append(result, offeredEntries(facts, personID)...)
		sort.Strings(result)
	}
	return result
}

// browsableEntries lists the offered entries the Person has not joined,
// which they browse under "More albums".
func browsableEntries(facts accessFacts, personID string) []string {
	if facts.Joins[personID] {
		return []string{}
	}
	return offeredEntries(facts, personID)
}

// difference lists the entries in after that are missing from before.
func difference(before, after []string) []string {
	seen := make(map[string]bool, len(before))
	for _, id := range before {
		seen[id] = true
	}
	result := []string{}
	for _, id := range after {
		if !seen[id] {
			result = append(result, id)
		}
	}
	return result
}

func audienceChanges(before, after accessFacts, personIDs []string) []AudienceChange {
	result := []AudienceChange{}
	for _, personID := range personIDs {
		oldEntries, newEntries := ownEntries(before, personID), ownEntries(after, personID)
		oldOffered, newOffered := browsableEntries(before, personID), browsableEntries(after, personID)
		change := AudienceChange{PersonID: personID,
			GainedEntryIDs: difference(oldEntries, newEntries), LostEntryIDs: difference(newEntries, oldEntries),
			OfferedGainedEntryIDs: difference(oldOffered, newOffered), OfferedLostEntryIDs: difference(newOffered, oldOffered)}
		if len(change.GainedEntryIDs)+len(change.LostEntryIDs)+len(change.OfferedGainedEntryIDs)+len(change.OfferedLostEntryIDs) > 0 {
			result = append(result, change)
		}
	}
	return result
}
