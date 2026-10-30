package identity_test

import (
	"testing"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetiredTipsFollowThePersonAcrossSessions(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	curator := claimCurator(t, module)
	member := authorizePerson(t, module, curator, "Alex", "alex@example.test")
	assert.Equal(t, []identity.Tip{}, member.Person.RetiredTips, "a new Person has every Tip ahead of them")

	retired, err := module.RetireTip(t.Context(), member.Token, identity.TipVideos)
	require.NoError(t, err)
	assert.Equal(t, []identity.Tip{identity.TipVideos}, retired.RetiredTips)
	again, err := module.RetireTip(t.Context(), member.Token, identity.TipVideos)
	require.NoError(t, err)
	assert.Equal(t, retired.RetiredTips, again.RetiredTips, "retiring twice changes nothing")
	_, err = module.RetireTip(t.Context(), member.Token, identity.TipChapters)
	require.NoError(t, err)

	// Another device signs in separately and finds the same Tips retired.
	phone, err := module.SignIn(t.Context(), identity.FakeClaims(identity.SignInRequest{Email: "alex@example.test", DisplayName: "Alex"}))
	require.NoError(t, err)
	assert.Equal(t, []identity.Tip{identity.TipVideos, identity.TipChapters}, phone.Person.RetiredTips)
	curatorView, err := module.Authenticate(t.Context(), curator.Token)
	require.NoError(t, err)
	assert.Empty(t, curatorView.Person.RetiredTips, "Tips belong to one Person")

	// The database admits every Tip the code knows.
	for _, tip := range identity.Tips {
		_, err := module.RetireTip(t.Context(), member.Token, tip)
		require.NoError(t, err, tip)
	}

	reset, err := module.ResetTips(t.Context(), member.Token)
	require.NoError(t, err)
	assert.Equal(t, []identity.Tip{}, reset.RetiredTips, "development can bring every Tip back")
	_, err = module.RetireTip(t.Context(), member.Token, identity.TipJoin)
	require.NoError(t, err)

	_, err = module.RetireTip(t.Context(), member.Token, identity.Tip("tour"))
	var expected *errcodes.Error
	require.ErrorAs(t, err, &expected)
	assert.Equal(t, 404, expected.HTTPCode)
	_, err = module.RetireTip(t.Context(), "signed-out", identity.TipJoin)
	assert.ErrorIs(t, err, identity.ErrUnauthenticated)
}
