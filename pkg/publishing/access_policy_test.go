package publishing

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAudienceChangesUseTheSameMomentDecisionEvaluator(t *testing.T) {
	t.Parallel()
	decisions := map[string]map[string]Decision{
		"first":  {"alex": DecisionAllow},
		"second": {"sam": DecisionAllow},
	}
	before := accessFacts{
		EntryMoments: map[string]string{"one": "first", "two": "first", "three": "second"},
		Decisions:    decisions,
	}
	after := accessFacts{
		EntryMoments: map[string]string{"one": "first", "two": "second", "three": "second"},
		Decisions:    decisions,
	}

	assert.Equal(t, []AudienceChange{
		{PersonID: "alex", GainedEntryIDs: []string{}, LostEntryIDs: []string{"two"}, OfferedGainedEntryIDs: []string{}, OfferedLostEntryIDs: []string{}},
		{PersonID: "sam", GainedEntryIDs: []string{"two"}, LostEntryIDs: []string{}, OfferedGainedEntryIDs: []string{}, OfferedLostEntryIDs: []string{}},
	}, audienceChanges(before, after, []string{"alex", "sam", "taylor"}))
}

func TestAlbumOffersReachOnlyEntriesNoPersonRuleDecides(t *testing.T) {
	t.Parallel()
	facts := accessFacts{
		EntryMoments:   map[string]string{"one": "first", "two": "second", "three": "second"},
		Decisions:      map[string]map[string]Decision{"second": {"denied": DecisionDeny, "granted": DecisionAllow}},
		AlbumDecisions: map[string]Decision{},
		EntryDecisions: map[string]map[string]Decision{"one": {"skipped": DecisionDeny}},
		AlbumOffers:    map[string]bool{"family": true},
		Circles:        map[string][]string{"denied": {"friends", "family"}, "granted": {"family"}, "skipped": {"family"}, "outside": {"friends"}},
	}
	assert.Equal(t, []string{"one"}, offeredEntries(facts, "denied"))
	assert.Equal(t, []string{"one"}, offeredEntries(facts, "granted"))
	assert.Equal(t, []string{"three", "two"}, offeredEntries(facts, "skipped"))
	assert.Empty(t, offeredEntries(facts, "outside"))
	withdrawn := facts
	withdrawn.AlbumOffers = map[string]bool{}
	assert.Equal(t, []AudienceChange{
		{PersonID: "granted", GainedEntryIDs: []string{}, LostEntryIDs: []string{}, OfferedGainedEntryIDs: []string{}, OfferedLostEntryIDs: []string{"one"}},
	}, audienceChanges(facts, withdrawn, []string{"granted", "outside"}))
}
