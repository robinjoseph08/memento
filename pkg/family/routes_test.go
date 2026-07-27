package family

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoutesDeclareExplicitCuratorPolicies(t *testing.T) {
	e := echo.New()
	RegisterRoutes(e, NewHandler(nil, nil))
	policies := make(map[string]string)
	for _, route := range e.Routes() {
		policies[route.Method+" "+route.Path] = route.Name
	}
	for _, route := range []string{
		"GET /api/relationships",
		"GET /api/relationships/branches/:person_id",
	} {
		assert.Equal(t, curatorReadPolicy, policies[route], route)
	}
	for _, route := range []string{
		"POST /api/relationships",
		"PATCH /api/relationships/:id",
		"POST /api/relationships/:id/archive",
	} {
		assert.Equal(t, curatorMutationPolicy, policies[route], route)
	}
}

func TestNormalizeRejectsInvalidConnections(t *testing.T) {
	validA := "00000000-0000-0000-0000-000000000001"
	validB := "00000000-0000-0000-0000-000000000002"
	for _, request := range []MutationRequest{
		{RelationshipType: "sibling", PersonAID: "not-a-uuid", PersonBID: validB},
		{RelationshipType: "sibling", PersonAID: validA, PersonBID: validA},
		{RelationshipType: "parent_child", PersonAID: validA, PersonBID: validB, PartnerStatus: "current"},
		{RelationshipType: "partner", PersonAID: validA, PersonBID: validB},
		{RelationshipType: "cousin", PersonAID: validA, PersonBID: validB},
	} {
		_, _, _, err := normalize(request)
		assert.ErrorIs(t, err, ErrInvalid)
	}
}

func TestNormalizeCanonicalizesSymmetricConnectionsButPreservesParentDirection(t *testing.T) {
	higher := "ffffffff-ffff-ffff-ffff-ffffffffffff"
	lower := "00000000-0000-0000-0000-000000000001"

	sibling, _, _, err := normalize(MutationRequest{RelationshipType: "sibling", PersonAID: higher, PersonBID: lower})
	require.NoError(t, err)
	assert.Equal(t, lower, sibling.PersonAID)
	assert.Equal(t, higher, sibling.PersonBID)

	parentChild, _, _, err := normalize(MutationRequest{RelationshipType: "parent_child", PersonAID: higher, PersonBID: lower})
	require.NoError(t, err)
	assert.Equal(t, higher, parentChild.PersonAID)
	assert.Equal(t, lower, parentChild.PersonBID)
}
