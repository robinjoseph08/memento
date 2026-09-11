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
		{PersonID: "alex", GainedEntryIDs: []string{}, LostEntryIDs: []string{"two"}},
		{PersonID: "sam", GainedEntryIDs: []string{"two"}, LostEntryIDs: []string{}},
	}, audienceChanges(before, after, []string{"alex", "sam", "taylor"}))
}
