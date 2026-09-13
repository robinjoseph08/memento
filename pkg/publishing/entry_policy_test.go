package publishing

import "testing"

func TestEntryAllowedResolvesMostSpecificDecision(t *testing.T) {
	t.Parallel()
	albums := []Decision{"", DecisionAllow}
	rules := []Decision{"", DecisionInherit, DecisionAllow, DecisionDeny}
	// Rows are Moment choices, columns are Entry choices in rules order.
	want := [2][4][4]bool{
		{{false, false, true, false}, {false, false, true, false}, {true, true, true, false}, {false, false, true, false}},
		{{true, true, true, false}, {true, true, true, false}, {true, true, true, false}, {false, false, true, false}},
	}
	for a, album := range albums {
		for m, moment := range rules {
			for e, entry := range rules {
				t.Run(string(album)+"/"+string(moment)+"/"+string(entry), func(t *testing.T) {
					t.Parallel()
					if got := entryAllowed(album, moment, entry); got != want[a][m][e] {
						t.Fatalf("entryAllowed(%q, %q, %q) = %v, want %v", album, moment, entry, got, want[a][m][e])
					}
				})
			}
		}
	}
}
