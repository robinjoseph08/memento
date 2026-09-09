package publishing

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRecommendationsFollowMembershipWithoutChangingDecisions(t *testing.T) {
	t.Parallel()
	facts := accessFacts{
		EntryMoments: map[string]string{"photo": "first", "supporting": "first", "other": "second"},
		Detections: map[string][]string{
			"photo":      {"alex", "alex"},
			"supporting": {"sam"},
			"other":      {"taylor"},
		},
		Decisions: map[string]map[string]Decision{
			"first":  {"sam": DecisionDeny},
			"second": {"jamie": DecisionAllow},
		},
	}

	assert.Equal(t, []string{"alex"}, recommendations(facts, "first"))

	facts.EntryMoments["photo"] = "second"
	assert.Empty(t, recommendations(facts, "first"))
	assert.Equal(t, []string{"alex", "taylor"}, recommendations(facts, "second"))
	assert.Equal(t, DecisionDeny, facts.Decisions["first"]["sam"])
	assert.Equal(t, DecisionAllow, facts.Decisions["second"]["jamie"])
}

func TestAudienceChangesUseTheSameMomentDecisionEvaluator(t *testing.T) {
	t.Parallel()
	before := accessFacts{
		EntryMoments: map[string]string{"one": "first", "two": "first", "three": "second"},
		Decisions: map[string]map[string]Decision{
			"first":  {"alex": DecisionAllow},
			"second": {"sam": DecisionAllow},
		},
	}
	after := before.clone()
	after.EntryMoments["two"] = "second"

	assert.Equal(t, []AudienceChange{
		{PersonID: "alex", GainedEntryIDs: []string{}, LostEntryIDs: []string{"two"}},
		{PersonID: "sam", GainedEntryIDs: []string{"two"}, LostEntryIDs: []string{}},
	}, audienceChanges(before, after, []string{"alex", "sam", "taylor"}))
}
