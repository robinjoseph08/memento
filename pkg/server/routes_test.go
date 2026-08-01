package server

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/activity"
	"github.com/robinjoseph08/memento/pkg/archives"
	"github.com/robinjoseph08/memento/pkg/audiences"
	"github.com/robinjoseph08/memento/pkg/comments"
	"github.com/robinjoseph08/memento/pkg/emaildelivery"
	"github.com/robinjoseph08/memento/pkg/engagement"
	"github.com/robinjoseph08/memento/pkg/events"
	"github.com/robinjoseph08/memento/pkg/family"
	"github.com/robinjoseph08/memento/pkg/favorites"
	"github.com/robinjoseph08/memento/pkg/health"
	"github.com/robinjoseph08/memento/pkg/library"
	"github.com/robinjoseph08/memento/pkg/people"
	"github.com/robinjoseph08/memento/pkg/push"
	"github.com/robinjoseph08/memento/pkg/recipients"
	"github.com/robinjoseph08/memento/pkg/recovery"
	"github.com/robinjoseph08/memento/pkg/repairs"
	"github.com/robinjoseph08/memento/pkg/search"
	"github.com/robinjoseph08/memento/pkg/sessions"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/sources"
	"github.com/robinjoseph08/memento/pkg/suggestions"
	"github.com/robinjoseph08/memento/pkg/visibility"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func completeRouteHandlers() RouteHandlers {
	return RouteHandlers{
		Health:      new(health.Service),
		Email:       emaildelivery.NewHandler(nil),
		Setup:       setup.NewHandler(nil),
		People:      people.NewHandler(nil, nil),
		Family:      family.NewHandler(nil, nil),
		Visibility:  visibility.NewHandler(nil, nil),
		Recipients:  recipients.NewHandler(nil, nil),
		Sources:     sources.NewHandler(nil, nil),
		Events:      events.NewHandler(nil, nil),
		Repairs:     repairs.NewHandler(nil, nil),
		Suggestions: suggestions.NewHandler(nil, nil),
		Audiences:   audiences.NewHandler(nil, nil),
		Sessions:    sessions.NewHandler(nil, nil),
		Library:     library.NewHandler(nil, nil),
		Archives:    archives.NewHandler(nil, nil),
		Search:      search.NewHandler(nil, nil),
		Comments:    comments.NewHandler(nil, nil),
		Favorites:   favorites.NewHandler(nil, nil),
		Activity:    activity.NewHandler(nil, nil),
		Engagement:  engagement.NewHandler(nil, nil),
		Push:        push.NewHandler(nil, nil),
		Recovery:    recovery.NewHandler(nil, nil),
	}
}

func TestProductionRoutePolicyCensus(t *testing.T) {
	e, err := New("https://photos.example", completeRouteHandlers())
	require.NoError(t, err)

	allowedPolicies := map[string]bool{
		"policy:public_safe": true, "policy:setup_only": true,
		"policy:token_inspect": true, "policy:token_exchange": true,
		"policy:session": true, "policy:session_csrf": true,
		"policy:onboarding_session": true, "policy:onboarding_session_csrf": true,
		"policy:recipient": true, "policy:recipient_self": true, "policy:recipient_self_csrf": true,
		"policy:recipient_discovery": true, "policy:recipient_self_interest": true,
		"policy:recipient_self_interest_csrf": true, "policy:recipient_self_suggestions": true,
		"policy:recipient_self_suggestions_csrf": true, "policy:recipient_content": true,
		"policy:recipient_content_csrf": true, "policy:curator": true, "policy:curator_csrf": true,
		"policy:curator_visibility": true, "policy:curator_visibility_csrf": true,
		"policy:curator_suggestions": true, "policy:curator_suggestions_csrf": true,
		"policy:recovery_curator": true,
	}
	seen := make(map[string]string)
	census := make([]string, 0, len(e.Routes()))
	for _, route := range e.Routes() {
		if route.Method == echo.RouteNotFound {
			continue
		}
		key := route.Method + " " + route.Path
		if prior, duplicate := seen[key]; duplicate {
			t.Errorf("duplicate production route %s has policies %q and %q", key, prior, route.Name)
			continue
		}
		seen[key] = route.Name
		census = append(census, key+"="+route.Name)
		assert.Truef(t, allowedPolicies[route.Name], "%s has unknown or missing primary policy %q", key, route.Name)
	}

	sort.Strings(census)
	digest := sha256.Sum256([]byte(strings.Join(census, "\n") + "\n"))
	assert.Len(t, seen, 151, "the census must contain every production API route exactly once")
	assert.Equalf(t, "4d58dc632b40a623737d007d55852ee28511d81b6e4136da948533f3ce77dbe5", hex.EncodeToString(digest[:]),
		"the production route-policy census changed:\n%s", strings.Join(census, "\n"))
	assert.Equal(t, "policy:recipient_content", seen["GET /api/me/events/:id"])
}
