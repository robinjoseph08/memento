package identity_test

import (
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

func claimCurator(t *testing.T, module *identity.Module) identity.Session {
	t.Helper()
	session, err := module.SignIn(t.Context(), identity.FakeClaims(identity.SignInRequest{Email: "curator@example.test", DisplayName: "Curator"}))
	require.NoError(t, err)
	return session
}

func cacheFace(t *testing.T, db *bun.DB, sourceID string) {
	t.Helper()
	now := time.Now().UTC()
	item := models.MediaItem{ID: models.NewUUIDv7(), SourceID: "asset-" + sourceID, Checksum: sourceID, Filename: sourceID + ".jpg", Kind: "IMAGE",
		CapturedAt: now, SourceCreatedAt: now, SourceUpdatedAt: now, ContentVersion: sourceID}
	require.NoError(t, func() error {
		_, err := db.NewInsert().Model(&item).Exec(t.Context())
		return err
	}())
	face := models.MediaFaceAssociation{MediaItemID: item.ID, SourceFaceID: sourceID, SourceName: "Immich " + sourceID}
	_, err := db.NewInsert().Model(&face).Exec(t.Context())
	require.NoError(t, err)
}

func TestCuratorLinksMultipleFacesAndSelectsAnAvatar(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	module := identity.New(db, nil)
	curator := claimCurator(t, module)
	alex, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Alex"})
	require.NoError(t, err)
	other, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Other"})
	require.NoError(t, err)
	for _, faceID := range []string{"face-one", "face-two", "face-three", "face-four"} {
		cacheFace(t, db, faceID)
	}

	_, err = module.LinkFace(t.Context(), curator.Token, alex.ID, identity.LinkFaceRequest{SourceFaceID: "face-one"})
	require.NoError(t, err)
	_, err = module.LinkFace(t.Context(), curator.Token, alex.ID, identity.LinkFaceRequest{SourceFaceID: "face-two"})
	require.NoError(t, err)
	updated, err := module.SetPersonAvatar(t.Context(), curator.Token, alex.ID, identity.SetPersonAvatarRequest{SourceFaceID: "face-two"})
	require.NoError(t, err)
	assert.Contains(t, updated.Person.AvatarURL, "face-two")
	require.Len(t, updated.Faces, 2)
	assert.True(t, updated.Faces[1].Avatar)

	_, err = module.LinkFace(t.Context(), curator.Token, other.ID, identity.LinkFaceRequest{SourceFaceID: "face-one"})
	require.Error(t, err)
	_, err = module.SetPersonAvatar(t.Context(), curator.Token, other.ID, identity.SetPersonAvatarRequest{SourceFaceID: "face-one"})
	require.Error(t, err)

	created, err := module.CreatePersonFromFace(t.Context(), curator.Token, identity.CreatePersonFromFaceRequest{DisplayName: "Sam", SourceFaceID: "face-three"})
	require.NoError(t, err)
	assert.Equal(t, "Sam", created.Person.DisplayName)
	require.Len(t, created.Faces, 1)

	require.NoError(t, module.IgnoreFace(t.Context(), curator.Token, "face-four"))
	_, err = module.LinkFace(t.Context(), curator.Token, alex.ID, identity.LinkFaceRequest{SourceFaceID: "face-four"})
	require.NoError(t, err)
}

func TestPreauthorizationLinksExactVerifiedEmail(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	curator := claimCurator(t, module)
	person, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Alex"})
	require.NoError(t, err)
	authorization, err := module.Preauthorize(t.Context(), curator.Token, person.ID, identity.PreauthorizeRequest{Email: "alex@example.test"})
	require.NoError(t, err)
	other, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Also Alex"})
	require.NoError(t, err)
	_, err = module.Preauthorize(t.Context(), curator.Token, other.ID, identity.PreauthorizeRequest{Email: "alex@example.test"})
	require.Error(t, err)
	claims := identity.Claims{Provider: "google", Subject: "alex-subject", Email: "alex@example.test", EmailVerified: true, DisplayName: "Different name"}
	for _, rejected := range []identity.Claims{
		{Provider: "google", Subject: "alex-subject", Email: "Alex@example.test", EmailVerified: true, DisplayName: "Alex"},
		{Provider: "google", Subject: "alex-subject", Email: "alex@example.test", DisplayName: "Alex"},
		{Provider: "google", Subject: "alex-subject", Email: "other@example.test", EmailVerified: true, DisplayName: "Alex"},
	} {
		_, err := module.SignIn(t.Context(), rejected)
		require.Error(t, err)
	}
	session, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	assert.Equal(t, person.ID, session.Person.ID)
	assert.Equal(t, "Alex", session.Person.DisplayName)
	assert.Nil(t, session.Person.OnboardingCompletedAt)
	detail, err := module.GetPerson(t.Context(), curator.Token, person.ID)
	require.NoError(t, err)
	require.Len(t, detail.Preauthorizations, 1)
	assert.Equal(t, authorization.ID, detail.Preauthorizations[0].ID)
	assert.NotNil(t, detail.Preauthorizations[0].ConsumedAt)
	require.Len(t, detail.Identities, 1)
	claims.Subject = "another-subject"
	_, err = module.SignIn(t.Context(), claims)
	require.ErrorIs(t, err, identity.ErrAccessDenied)
	claims.Subject = "alex-subject"
	claims.Email = "changed@example.test"
	returning, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	assert.Equal(t, person.ID, returning.Person.ID)
	_, err = module.Preauthorize(t.Context(), session.Token, other.ID, identity.PreauthorizeRequest{Email: "not-allowed@example.test"})
	require.ErrorIs(t, err, identity.ErrAccessDenied)
	revoked, err := module.Preauthorize(t.Context(), curator.Token, person.ID, identity.PreauthorizeRequest{Email: "revoked@example.test"})
	require.NoError(t, err)
	require.NoError(t, module.RevokePreauthorization(t.Context(), curator.Token, person.ID, revoked.ID))
	claims.Subject = "revoked-subject"
	claims.Email = "revoked@example.test"
	_, err = module.SignIn(t.Context(), claims)
	require.ErrorIs(t, err, identity.ErrAccessDenied)
}

func TestPreauthorizationRechecksEmailOwnershipAtConsumption(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	curator := claimCurator(t, module)
	first := authorizePerson(t, module, curator, "First", "old@example.test")
	second, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Second"})
	require.NoError(t, err)
	approval, err := module.Preauthorize(t.Context(), curator.Token, second.ID, identity.PreauthorizeRequest{Email: "shared@example.test"})
	require.NoError(t, err)
	changed := identity.FakeClaims(identity.SignInRequest{Email: "old@example.test", DisplayName: "First"})
	changed.Email = "shared@example.test"
	returning, err := module.SignIn(t.Context(), changed)
	require.NoError(t, err)
	assert.Equal(t, first.Person.ID, returning.Person.ID)
	_, err = module.SignIn(t.Context(), identity.FakeClaims(identity.SignInRequest{Email: "shared@example.test", DisplayName: "Second"}))
	require.ErrorIs(t, err, identity.ErrAccessDenied)
	detail, err := module.GetPerson(t.Context(), curator.Token, second.ID)
	require.NoError(t, err)
	require.Len(t, detail.Preauthorizations, 1)
	assert.Equal(t, approval.ID, detail.Preauthorizations[0].ID)
	assert.Nil(t, detail.Preauthorizations[0].ConsumedAt)
	assert.Empty(t, detail.Identities)
}

func requirePersonFieldError(t *testing.T, err error, field string) {
	t.Helper()
	var fields *errcodes.FieldError
	require.ErrorAs(t, err, &fields)
	assert.NotEmpty(t, fields.Fields[field])
}

func TestCuratorsCannotDemoteOrDeactivateThemselves(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	first := claimCurator(t, module)
	second := authorizePerson(t, module, first, "Second", "second@example.test")
	for _, secondIsCurator := range []bool{false, true} {
		_, err := module.UpdatePerson(t.Context(), first.Token, second.Person.ID, identity.UpdatePersonRequest{DisplayName: "Second", IsCurator: secondIsCurator})
		require.NoError(t, err)
		for _, edit := range []struct {
			request identity.UpdatePersonRequest
			field   string
		}{
			{identity.UpdatePersonRequest{DisplayName: "Changed"}, "is_curator"},
			{identity.UpdatePersonRequest{DisplayName: "Changed", IsCurator: true, Deactivated: true}, "deactivated"},
		} {
			_, err := module.UpdatePerson(t.Context(), first.Token, first.Person.ID, edit.request)
			requirePersonFieldError(t, err, edit.field)
			current, err := module.Authenticate(t.Context(), first.Token)
			require.NoError(t, err)
			assert.Equal(t, first.Person, current.Person)
		}
	}
	_, err := module.UpdatePerson(t.Context(), first.Token, first.Person.ID, identity.UpdatePersonRequest{DisplayName: "Changed", Deactivated: true})
	var fields *errcodes.FieldError
	require.ErrorAs(t, err, &fields)
	assert.Equal(t, map[string]string{
		"is_curator":  "Ask another Curator to remove your Curator role.",
		"deactivated": "Ask another Curator to deactivate your access.",
	}, fields.Fields)
	renamed, err := module.UpdatePerson(t.Context(), first.Token, first.Person.ID, identity.UpdatePersonRequest{DisplayName: "New name", IsCurator: true})
	require.NoError(t, err)
	assert.Equal(t, "New name", renamed.DisplayName)
	assert.True(t, renamed.IsCurator)
	assert.Nil(t, renamed.DeactivatedAt)

	// Another signed-in Curator may change either status.
	demoted, err := module.UpdatePerson(t.Context(), second.Token, first.Person.ID, identity.UpdatePersonRequest{DisplayName: "New name"})
	require.NoError(t, err)
	assert.False(t, demoted.IsCurator)
	_, err = module.UpdatePerson(t.Context(), first.Token, second.Person.ID, identity.UpdatePersonRequest{DisplayName: "Second"})
	require.ErrorIs(t, err, identity.ErrAccessDenied)
	_, err = module.UpdatePerson(t.Context(), second.Token, first.Person.ID, identity.UpdatePersonRequest{DisplayName: "New name", IsCurator: true})
	require.NoError(t, err)
	deactivated, err := module.UpdatePerson(t.Context(), second.Token, first.Person.ID, identity.UpdatePersonRequest{DisplayName: "New name", IsCurator: true, Deactivated: true})
	require.NoError(t, err)
	assert.NotNil(t, deactivated.DeactivatedAt)
	_, err = module.Authenticate(t.Context(), first.Token)
	require.ErrorIs(t, err, identity.ErrUnauthenticated)
}

func TestCuratorPersonDetailIncludesActiveTargetSessions(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	module := identity.New(testdb.New(t), func() time.Time { return now })
	curator := claimCurator(t, module)
	member := authorizePerson(t, module, curator, "Alex", "alex@example.test")
	now = now.Add(time.Hour)
	second, err := module.SignIn(identity.WithBrowser(t.Context(), "Firefox/ on Macintosh"), identity.FakeClaims(identity.SignInRequest{Email: "alex@example.test", DisplayName: "Alex"}))
	require.NoError(t, err)
	detail, err := module.GetPerson(t.Context(), curator.Token, member.Person.ID)
	require.NoError(t, err)
	require.Len(t, detail.Sessions, 2)
	assert.False(t, detail.Sessions[0].Current)
	assert.False(t, detail.Sessions[1].Current)
	assert.Equal(t, "Firefox on Mac", detail.Sessions[1].Device)
	assert.Equal(t, "alex@example.test", detail.Sessions[1].Email)
	assert.Equal(t, second.ExpiresAt, detail.Sessions[1].ExpiresAt)
	own, err := module.GetPerson(t.Context(), curator.Token, curator.Person.ID)
	require.NoError(t, err)
	sessions, err := module.Sessions(t.Context(), curator.Token)
	require.NoError(t, err)
	assert.Equal(t, sessions, own.Sessions)
	require.Len(t, own.Sessions, 1)
	assert.True(t, own.Sessions[0].Current)
	_, err = module.GetPerson(t.Context(), member.Token, member.Person.ID)
	require.ErrorIs(t, err, identity.ErrAccessDenied)
	_, err = module.GetPerson(t.Context(), member.Token, curator.Person.ID)
	require.ErrorIs(t, err, identity.ErrAccessDenied)

	// Expire the first session without authenticating it, which would delete it.
	now = member.ExpiresAt
	curator = claimCurator(t, module)
	detail, err = module.GetPerson(t.Context(), curator.Token, member.Person.ID)
	require.NoError(t, err)
	require.Len(t, detail.Sessions, 1)
	assert.Equal(t, second.ExpiresAt, detail.Sessions[0].ExpiresAt)
	require.NoError(t, module.SignOut(t.Context(), second.Token))
	detail, err = module.GetPerson(t.Context(), curator.Token, member.Person.ID)
	require.NoError(t, err)
	assert.NotNil(t, detail.Sessions)
	assert.Empty(t, detail.Sessions)
}

func TestPeopleWithoutLogin(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	curator := claimCurator(t, module)
	person, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Alex"})
	require.NoError(t, err)
	assert.Equal(t, "Alex", person.DisplayName)
	assert.False(t, person.IsCurator)
	assert.Nil(t, person.OnboardingCompletedAt)
	assert.Nil(t, person.DeactivatedAt)
	people, err := module.ListPeople(t.Context(), curator.Token, "alex")
	require.NoError(t, err)
	require.Len(t, people, 1)
	assert.Equal(t, person, people[0])
	detail, err := module.GetPerson(t.Context(), curator.Token, person.ID)
	require.NoError(t, err)
	assert.Empty(t, detail.Identities)
	assert.Empty(t, detail.Preauthorizations)
	renamed, err := module.UpdatePerson(t.Context(), curator.Token, person.ID, identity.UpdatePersonRequest{DisplayName: "Alex Smith"})
	require.NoError(t, err)
	assert.Equal(t, "Alex Smith", renamed.DisplayName)
	_, err = module.CreatePerson(t.Context(), "invalid", identity.CreatePersonRequest{DisplayName: "Not allowed"})
	require.ErrorIs(t, err, identity.ErrUnauthenticated)
}
