package identity_test

import (
	"encoding/json"
	"testing"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func circleNames(t *testing.T, module *identity.Module, token string) []string {
	t.Helper()
	circles, err := module.ListCircles(t.Context(), token)
	require.NoError(t, err)
	names := []string{}
	for _, circle := range circles {
		names = append(names, circle.Name)
	}
	return names
}

func memberNames(t *testing.T, module *identity.Module, token, circleID string) []string {
	t.Helper()
	circles, err := module.ListCircles(t.Context(), token)
	require.NoError(t, err)
	for _, circle := range circles {
		if circle.ID == circleID {
			names := []string{}
			for _, member := range circle.Members {
				names = append(names, member.DisplayName)
			}
			return names
		}
	}
	t.Fatalf("Circle %s is not listed", circleID)
	return nil
}

// personCircleIDs lists the Circles a Person belongs to, in name order.
func personCircleIDs(t *testing.T, module *identity.Module, token, personID string) []string {
	t.Helper()
	detail, err := module.GetPerson(t.Context(), token, personID)
	require.NoError(t, err)
	ids := []string{}
	for _, circle := range detail.Circles {
		if circle.Member {
			ids = append(ids, circle.ID)
		}
	}
	return ids
}

func requireCircleFieldError(t *testing.T, err error, field, message string) {
	t.Helper()
	var fields *errcodes.FieldError
	require.ErrorAs(t, err, &fields)
	assert.Equal(t, message, fields.Fields[field])
}

func requireNotFound(t *testing.T, err error, resource string) {
	t.Helper()
	var typed *errcodes.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, "not_found", typed.Code)
	assert.Equal(t, resource+" not found.", typed.Message)
}

func TestCuratorCreatesRenamesAndDeletesCircles(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	curator := claimCurator(t, module)
	assert.Equal(t, []string{}, circleNames(t, module, curator.Token))

	family, err := module.CreateCircle(t.Context(), curator.Token, identity.CircleRequest{Name: "  Extended family "})
	require.NoError(t, err)
	assert.Equal(t, "Extended family", family.Name)
	assert.NotNil(t, family.Members)
	assert.Empty(t, family.Members)
	college, err := module.CreateCircle(t.Context(), curator.Token, identity.CircleRequest{Name: "College friends"})
	require.NoError(t, err)
	assert.Equal(t, []string{"College friends", "Extended family"}, circleNames(t, module, curator.Token))

	// Names are unique regardless of letter case, on create and on rename.
	_, err = module.CreateCircle(t.Context(), curator.Token, identity.CircleRequest{Name: "extended FAMILY"})
	requireCircleFieldError(t, err, "name", "Another Circle already has this name. Choose a different one.")
	_, err = module.RenameCircle(t.Context(), curator.Token, family.ID, identity.CircleRequest{Name: "College Friends"})
	requireCircleFieldError(t, err, "name", "Another Circle already has this name. Choose a different one.")
	_, err = module.CreateCircle(t.Context(), curator.Token, identity.CircleRequest{Name: "   "})
	requireCircleFieldError(t, err, "name", "Enter a Circle name.")
	assert.Equal(t, []string{"College friends", "Extended family"}, circleNames(t, module, curator.Token))

	// A Circle may change the case of its own name.
	renamed, err := module.RenameCircle(t.Context(), curator.Token, college.ID, identity.CircleRequest{Name: "College Friends"})
	require.NoError(t, err)
	assert.Equal(t, college.ID, renamed.ID)
	assert.Equal(t, "College Friends", renamed.Name)
	renamed, err = module.RenameCircle(t.Context(), curator.Token, college.ID, identity.CircleRequest{Name: "Old roommates"})
	require.NoError(t, err)
	assert.Equal(t, "Old roommates", renamed.Name)
	assert.Equal(t, []string{"Extended family", "Old roommates"}, circleNames(t, module, curator.Token))

	require.NoError(t, module.DeleteCircle(t.Context(), curator.Token, college.ID))
	assert.Equal(t, []string{"Extended family"}, circleNames(t, module, curator.Token))
	// A deleted Circle's name is free again.
	_, err = module.CreateCircle(t.Context(), curator.Token, identity.CircleRequest{Name: "Old roommates"})
	require.NoError(t, err)

	for _, missing := range []string{college.ID, "not-a-uuid"} {
		_, err = module.RenameCircle(t.Context(), curator.Token, missing, identity.CircleRequest{Name: "Gone"})
		requireNotFound(t, err, "Circle")
		requireNotFound(t, module.DeleteCircle(t.Context(), curator.Token, missing), "Circle")
		_, err = module.SetCircleMembers(t.Context(), curator.Token, missing, identity.CircleMembersRequest{PersonIDs: []string{}})
		requireNotFound(t, err, "Circle")
	}
}

func TestCircleMembershipFromBothSides(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	curator := claimCurator(t, module)
	people := map[string]identity.Person{}
	for _, name := range []string{"Alex", "Sam", "Jo"} {
		person, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: name})
		require.NoError(t, err)
		people[name] = person
	}
	family, err := module.CreateCircle(t.Context(), curator.Token, identity.CircleRequest{Name: "Extended family"})
	require.NoError(t, err)
	college, err := module.CreateCircle(t.Context(), curator.Token, identity.CircleRequest{Name: "College friends"})
	require.NoError(t, err)
	circlesOf := func(name string) []string {
		t.Helper()
		return personCircleIDs(t, module, curator.Token, people[name].ID)
	}

	// From the Circles page.
	updated, err := module.SetCircleMembers(t.Context(), curator.Token, family.ID, identity.CircleMembersRequest{PersonIDs: []string{people["Sam"].ID, people["Alex"].ID, people["Alex"].ID}})
	require.NoError(t, err)
	assert.Equal(t, family.ID, updated.ID)
	require.Len(t, updated.Members, 2)
	assert.Equal(t, people["Alex"], updated.Members[0])
	assert.Equal(t, "Sam", updated.Members[1].DisplayName)
	assert.Equal(t, []string{family.ID}, circlesOf("Alex"))
	assert.Equal(t, []string{}, circlesOf("Jo"))
	detail, err := module.GetPerson(t.Context(), curator.Token, people["Jo"].ID)
	require.NoError(t, err)
	assert.Equal(t, []identity.PersonCircle{{ID: college.ID, Name: "College friends"}, {ID: family.ID, Name: "Extended family"}}, detail.Circles,
		"the details page offers every Circle, ticked or not")

	// From a Person's details page, into several Circles at once.
	require.NoError(t, module.SetPersonCircles(t.Context(), curator.Token, people["Jo"].ID, identity.PersonCirclesRequest{CircleIDs: []string{family.ID, college.ID}}))
	assert.ElementsMatch(t, []string{family.ID, college.ID}, circlesOf("Jo"))
	assert.Equal(t, []string{"Alex", "Jo", "Sam"}, memberNames(t, module, curator.Token, family.ID))
	assert.Equal(t, []string{"Jo"}, memberNames(t, module, curator.Token, college.ID))

	// Removing works from either side and leaves the other memberships alone.
	_, err = module.SetCircleMembers(t.Context(), curator.Token, family.ID, identity.CircleMembersRequest{PersonIDs: []string{people["Jo"].ID, people["Sam"].ID}})
	require.NoError(t, err)
	assert.Equal(t, []string{}, circlesOf("Alex"))
	require.NoError(t, module.SetPersonCircles(t.Context(), curator.Token, people["Sam"].ID, identity.PersonCirclesRequest{CircleIDs: []string{}}))
	assert.Equal(t, []string{"Jo"}, memberNames(t, module, curator.Token, family.ID))
	assert.ElementsMatch(t, []string{family.ID, college.ID}, circlesOf("Jo"))

	// Unknown records are refused without changing anything.
	_, err = module.SetCircleMembers(t.Context(), curator.Token, family.ID, identity.CircleMembersRequest{PersonIDs: []string{people["Alex"].ID, college.ID}})
	requireCircleFieldError(t, err, "person_ids", "Refresh the page and choose people from the list.")
	err = module.SetPersonCircles(t.Context(), curator.Token, people["Alex"].ID, identity.PersonCirclesRequest{CircleIDs: []string{people["Sam"].ID}})
	requireCircleFieldError(t, err, "circle_ids", "Refresh the page and choose Circles from the list.")
	err = module.SetPersonCircles(t.Context(), curator.Token, family.ID, identity.PersonCirclesRequest{CircleIDs: []string{}})
	requireNotFound(t, err, "Person")
	assert.Equal(t, []string{"Jo"}, memberNames(t, module, curator.Token, family.ID))
	assert.Equal(t, []string{}, circlesOf("Alex"))

	// Deleting a Circle removes its memberships and leaves its People.
	require.NoError(t, module.DeleteCircle(t.Context(), curator.Token, college.ID))
	assert.Equal(t, []string{family.ID}, circlesOf("Jo"))
	listed, err := module.ListPeople(t.Context(), curator.Token, "Jo")
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, people["Jo"], listed[0].Person)
}

func TestCirclesAreCuratorOnly(t *testing.T) {
	t.Parallel()
	module := newAdmission(t, false).identity
	curator := claimCurator(t, module)
	member := authorizePerson(t, module, curator, "Alex", "alex@example.test")
	_, err := module.CompleteOnboarding(t.Context(), member.Token, identity.UpdateProfileRequest{DisplayName: "Alex", UpdateEmail: "alex@example.test", EmailUpdates: true})
	require.NoError(t, err)
	circle, err := module.CreateCircle(t.Context(), curator.Token, identity.CircleRequest{Name: "Immediate family"})
	require.NoError(t, err)
	require.NoError(t, module.SetPersonCircles(t.Context(), curator.Token, member.Person.ID, identity.PersonCirclesRequest{CircleIDs: []string{circle.ID}}))

	_, err = module.ListCircles(t.Context(), member.Token)
	require.ErrorIs(t, err, identity.ErrAccessDenied)
	_, err = module.CreateCircle(t.Context(), member.Token, identity.CircleRequest{Name: "Mine"})
	require.ErrorIs(t, err, identity.ErrAccessDenied)
	_, err = module.RenameCircle(t.Context(), member.Token, circle.ID, identity.CircleRequest{Name: "Mine"})
	require.ErrorIs(t, err, identity.ErrAccessDenied)
	_, err = module.SetCircleMembers(t.Context(), member.Token, circle.ID, identity.CircleMembersRequest{PersonIDs: []string{}})
	require.ErrorIs(t, err, identity.ErrAccessDenied)
	require.ErrorIs(t, module.SetPersonCircles(t.Context(), member.Token, member.Person.ID, identity.PersonCirclesRequest{CircleIDs: []string{}}), identity.ErrAccessDenied)
	require.ErrorIs(t, module.DeleteCircle(t.Context(), member.Token, circle.ID), identity.ErrAccessDenied)
	_, err = module.ListCircles(t.Context(), "invalid")
	require.ErrorIs(t, err, identity.ErrUnauthenticated)
	assert.Equal(t, []string{"Immediate family"}, circleNames(t, module, curator.Token))

	// Nothing a viewer reads about themselves mentions their Circles.
	session, err := module.Authenticate(t.Context(), member.Token)
	require.NoError(t, err)
	profile, err := module.Profile(t.Context(), member.Token)
	require.NoError(t, err)
	for _, response := range []any{session.Person, profile} {
		body, err := json.Marshal(response)
		require.NoError(t, err)
		assert.NotContains(t, string(body), "Immediate family")
		assert.NotContains(t, string(body), circle.ID)
		assert.NotContains(t, string(body), "circle")
	}
}

func TestApprovingAJoinRequestPicksCircles(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	curator := claimCurator(t, module)
	family, err := module.CreateCircle(t.Context(), curator.Token, identity.CircleRequest{Name: "Extended family"})
	require.NoError(t, err)
	college, err := module.CreateCircle(t.Context(), curator.Token, identity.CircleRequest{Name: "College friends"})
	require.NoError(t, err)
	joinRequest := func(email string) identity.AccessRequest {
		t.Helper()
		_, err := module.SignIn(t.Context(), identity.Claims{Email: email, EmailVerified: true, DisplayName: "Stranger"})
		require.ErrorIs(t, err, identity.ErrAccessRequested)
		requests, err := module.ListAccessRequests(t.Context(), curator.Token)
		require.NoError(t, err)
		for _, request := range requests {
			if request.Email == email {
				return request
			}
		}
		t.Fatalf("no Access Request for %s", email)
		return identity.AccessRequest{}
	}

	// An unknown or deleted Circle is refused before anyone is created.
	sam := joinRequest("sam@example.test")
	_, err = module.ApproveAccessRequest(t.Context(), curator.Token, sam.ID, identity.ApproveAccessRequestRequest{DisplayName: "Sam", CircleIDs: []string{family.ID, "not-a-circle"}})
	requireCircleFieldError(t, err, "circle_ids", "Refresh the page and choose Circles from the list.")
	gone, err := module.CreateCircle(t.Context(), curator.Token, identity.CircleRequest{Name: "Old roommates"})
	require.NoError(t, err)
	require.NoError(t, module.DeleteCircle(t.Context(), curator.Token, gone.ID))
	_, err = module.ApproveAccessRequest(t.Context(), curator.Token, sam.ID, identity.ApproveAccessRequestRequest{DisplayName: "Sam", CircleIDs: []string{gone.ID}})
	requireCircleFieldError(t, err, "circle_ids", "Refresh the page and choose Circles from the list.")
	people, err := module.ListPeople(t.Context(), curator.Token, "Sam")
	require.NoError(t, err)
	assert.Empty(t, people)

	// A new Person arrives in the picked Circles.
	approved, err := module.ApproveAccessRequest(t.Context(), curator.Token, sam.ID, identity.ApproveAccessRequestRequest{DisplayName: "Sam", CircleIDs: []string{family.ID, family.ID}})
	require.NoError(t, err)
	assert.Equal(t, []string{family.ID}, personCircleIDs(t, module, curator.Token, approved.PersonID))
	_, err = module.ApproveAccessRequest(t.Context(), curator.Token, sam.ID, identity.ApproveAccessRequestRequest{DisplayName: "Sam", CircleIDs: []string{college.ID}})
	require.NoError(t, err)
	assert.Equal(t, []string{family.ID}, personCircleIDs(t, module, curator.Token, approved.PersonID), "approving again changes nothing")
	admitted, err := module.SignIn(t.Context(), identity.Claims{Email: "sam@example.test", EmailVerified: true, DisplayName: "Sam"})
	require.NoError(t, err)
	assert.Equal(t, approved.PersonID, admitted.Person.ID)
	assert.Equal(t, []string{"Sam"}, memberNames(t, module, curator.Token, family.ID))

	// Linking an existing Person adds the picked Circles to the ones they had.
	alex, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Alex"})
	require.NoError(t, err)
	require.NoError(t, module.SetPersonCircles(t.Context(), curator.Token, alex.ID, identity.PersonCirclesRequest{CircleIDs: []string{college.ID}}))
	linked, err := module.ApproveAccessRequest(t.Context(), curator.Token, joinRequest("alex@example.test").ID, identity.ApproveAccessRequestRequest{PersonID: alex.ID, CircleIDs: []string{family.ID, college.ID}})
	require.NoError(t, err)
	assert.Equal(t, alex.ID, linked.PersonID)
	assert.Equal(t, []string{college.ID, family.ID}, personCircleIDs(t, module, curator.Token, alex.ID))

	// Leaving the picker empty assigns nothing.
	jo, err := module.ApproveAccessRequest(t.Context(), curator.Token, joinRequest("jo@example.test").ID, identity.ApproveAccessRequestRequest{DisplayName: "Jo"})
	require.NoError(t, err)
	assert.Equal(t, []string{}, personCircleIDs(t, module, curator.Token, jo.PersonID))
	assert.Equal(t, []string{"Alex", "Sam"}, memberNames(t, module, curator.Token, family.ID))
}

func TestCirclesPickedBeforeSignInStayThroughIt(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	curator := claimCurator(t, module)
	college, err := module.CreateCircle(t.Context(), curator.Token, identity.CircleRequest{Name: "College friends"})
	require.NoError(t, err)
	person, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Sam"})
	require.NoError(t, err)
	require.NoError(t, module.SetPersonCircles(t.Context(), curator.Token, person.ID, identity.PersonCirclesRequest{CircleIDs: []string{college.ID}}))
	_, err = module.Preauthorize(t.Context(), curator.Token, person.ID, identity.PreauthorizeRequest{Email: "sam@example.test"})
	require.NoError(t, err)

	session, err := module.SignIn(t.Context(), identity.Claims{Email: "sam@example.test", EmailVerified: true, DisplayName: "Sam"})
	require.NoError(t, err)
	assert.Equal(t, person.ID, session.Person.ID)
	assert.Equal(t, []string{college.ID}, personCircleIDs(t, module, curator.Token, person.ID))
}
