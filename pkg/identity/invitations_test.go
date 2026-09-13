package identity_test

import (
	"context"
	"testing"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvitationsRequireEligiblePreauthorizationAndAreIdempotent(t *testing.T) {
	t.Parallel()
	a := newAdmission(t, true)
	module := a.identity
	curator := claimCurator(t, module)
	person, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Alex"})
	require.NoError(t, err)
	approval, err := module.Preauthorize(t.Context(), curator.Token, person.ID, identity.PreauthorizeRequest{Email: "alex@example.test"})
	require.NoError(t, err)

	const clicks = 5
	results := make([]identity.Invitation, clicks)
	errs := make([]error, clicks)
	operations := make([]func(), clicks)
	for i := range clicks {
		operations[i] = func() {
			results[i], errs[i] = module.SendInvitation(t.Context(), curator.Token, person.ID, identity.SendInvitationRequest{PreauthorizationID: approval.ID})
		}
	}
	raceIdentityOperations(operations...)
	for i := range clicks {
		require.NoError(t, errs[i])
		assert.Equal(t, results[0].ID, results[i].ID, "duplicate submissions share one Invitation")
	}
	invitation := results[0]
	assert.Equal(t, "alex@example.test", invitation.Email)
	assert.Equal(t, "Curator", invitation.SentBy)
	assert.Equal(t, "queued", invitation.Delivery.Status)
	assert.Equal(t, 1, a.queue.count(), "one delivery is enqueued for one Invitation")
	assert.Empty(t, a.recorder.Sent(), "nothing is sent inside the request")

	a.deliverAll(t)
	sent := a.recorder.Sent()
	require.Len(t, sent, 1)
	assert.Equal(t, "invitation", sent[0].Kind)
	assert.Equal(t, "alex@example.test", sent[0].To)
	assert.Equal(t, "Curator invited you to Memento", sent[0].Subject)
	assert.Contains(t, sent[0].Body, "Hi Alex,")
	assert.Contains(t, sent[0].Body, "https://memento.example.test/sign-in\n")
	assert.Contains(t, sent[0].Body, "alex@example.test")
	assert.NotContains(t, sent[0].Body, "token=", "the link carries no credential")
	detail, err := module.GetPerson(t.Context(), curator.Token, person.ID)
	require.NoError(t, err)
	require.Len(t, detail.Invitations, 1)
	assert.Equal(t, "delivered", detail.Invitations[0].Delivery.Status)
	assert.NotNil(t, detail.Invitations[0].Delivery.DeliveredAt)
	assert.Equal(t, approval.ID, detail.Invitations[0].PreauthorizationID)

	// Ineligible approvals and callers are refused before anything is written.
	other, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Other"})
	require.NoError(t, err)
	_, err = module.SendInvitation(t.Context(), curator.Token, other.ID, identity.SendInvitationRequest{PreauthorizationID: approval.ID})
	require.ErrorIs(t, err, errcodes.NotFound("Preauthorization"))
	revoked, err := module.Preauthorize(t.Context(), curator.Token, other.ID, identity.PreauthorizeRequest{Email: "revoked@example.test"})
	require.NoError(t, err)
	require.NoError(t, module.RevokePreauthorization(t.Context(), curator.Token, other.ID, revoked.ID))
	_, err = module.SendInvitation(t.Context(), curator.Token, other.ID, identity.SendInvitationRequest{PreauthorizationID: revoked.ID})
	require.ErrorIs(t, err, identity.ErrPreauthorizationRevoked)
	member, err := module.SignIn(t.Context(), identity.FakeClaims(identity.SignInRequest{Email: "alex@example.test", DisplayName: "Alex"}))
	require.NoError(t, err)
	_, err = module.SendInvitation(t.Context(), curator.Token, person.ID, identity.SendInvitationRequest{PreauthorizationID: approval.ID})
	require.NoError(t, err, "an existing Invitation is still readable after its approval is consumed")
	consumedSecond, err := module.Preauthorize(t.Context(), curator.Token, person.ID, identity.PreauthorizeRequest{Email: "alex-second@example.test"})
	require.NoError(t, err)
	_, err = module.SignIn(t.Context(), identity.FakeClaims(identity.SignInRequest{Email: "alex-second@example.test", DisplayName: "Alex"}))
	require.NoError(t, err)
	_, err = module.SendInvitation(t.Context(), curator.Token, person.ID, identity.SendInvitationRequest{PreauthorizationID: consumedSecond.ID})
	require.ErrorIs(t, err, identity.ErrPreauthorizationConsumed)
	_, err = module.SendInvitation(t.Context(), member.Token, other.ID, identity.SendInvitationRequest{PreauthorizationID: revoked.ID})
	require.ErrorIs(t, err, identity.ErrAccessDenied)
	_, err = module.UpdatePerson(t.Context(), curator.Token, other.ID, identity.UpdatePersonRequest{DisplayName: "Other", Deactivated: true})
	require.NoError(t, err)
	_, err = module.Preauthorize(t.Context(), curator.Token, other.ID, identity.PreauthorizeRequest{Email: "fresh@example.test"})
	require.Error(t, err, "deactivated People cannot be approved")
	_, err = module.SendInvitation(t.Context(), curator.Token, other.ID, identity.SendInvitationRequest{PreauthorizationID: revoked.ID})
	require.ErrorIs(t, err, identity.ErrPersonDeactivated)
	assert.Equal(t, 0, a.queue.count())

	// Invitations ignore the update-email preference: they are transactional.
	_, err = module.UpdateProfile(t.Context(), member.Token, identity.UpdateProfileRequest{DisplayName: "Alex", UpdateEmail: "", EmailUpdates: false})
	require.NoError(t, err)
	third, err := module.Preauthorize(t.Context(), curator.Token, person.ID, identity.PreauthorizeRequest{Email: "alex-third@example.test"})
	require.NoError(t, err)
	_, err = module.SendInvitation(t.Context(), curator.Token, person.ID, identity.SendInvitationRequest{PreauthorizationID: third.ID})
	require.NoError(t, err)
	a.deliverAll(t)
	require.Len(t, a.recorder.Sent(), 2)
	assert.Equal(t, "alex-third@example.test", a.recorder.Sent()[1].To)
}

func TestInvitationRetryFollowsDeliveryState(t *testing.T) {
	t.Parallel()
	a := newAdmission(t, true)
	module := a.identity
	curator := claimCurator(t, module)
	person, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Alex"})
	require.NoError(t, err)
	approval, err := module.Preauthorize(t.Context(), curator.Token, person.ID, identity.PreauthorizeRequest{Email: "alex@example.test"})
	require.NoError(t, err)
	a.recorder.AfterSend = func(_ context.Context, _ notifications.Message) error {
		return &notifications.DeliveryError{Outcome: notifications.OutcomePermanent, Summary: "The mail server replied 550."}
	}
	invitation, err := module.SendInvitation(t.Context(), curator.Token, person.ID, identity.SendInvitationRequest{PreauthorizationID: approval.ID})
	require.NoError(t, err)
	a.deliverAll(t)
	detail, err := module.GetPerson(t.Context(), curator.Token, person.ID)
	require.NoError(t, err)
	require.Len(t, detail.Invitations, 1)
	assert.Equal(t, "failed", detail.Invitations[0].Delivery.Status)
	assert.Equal(t, "The mail server replied 550.", detail.Invitations[0].Delivery.Message)
	_, err = module.SendInvitation(t.Context(), curator.Token, person.ID, identity.SendInvitationRequest{PreauthorizationID: approval.ID})
	require.NoError(t, err)
	assert.Equal(t, 0, a.queue.count(), "resubmitting does not retry by accident")

	a.recorder.AfterSend = nil
	retried, err := module.RetryInvitation(t.Context(), curator.Token, person.ID, invitation.ID)
	require.NoError(t, err)
	assert.Equal(t, "queued", retried.Delivery.Status)
	again, err := module.RetryInvitation(t.Context(), curator.Token, person.ID, invitation.ID)
	require.NoError(t, err)
	assert.Equal(t, "queued", again.Delivery.Status)
	assert.Equal(t, 1, a.queue.count(), "a second click while queued adds no work")
	a.deliverAll(t)
	detail, err = module.GetPerson(t.Context(), curator.Token, person.ID)
	require.NoError(t, err)
	assert.Equal(t, "delivered", detail.Invitations[0].Delivery.Status)
	assert.Equal(t, 2, detail.Invitations[0].Delivery.Attempts)
	_, err = module.RetryInvitation(t.Context(), curator.Token, person.ID, invitation.ID)
	require.ErrorIs(t, err, notifications.ErrAlreadyDelivered)
	_, err = module.RetryInvitation(t.Context(), curator.Token, person.ID, "not-an-invitation")
	require.ErrorIs(t, err, errcodes.NotFound("Invitation"))
	require.NoError(t, module.RevokePreauthorization(t.Context(), curator.Token, person.ID, approval.ID))
	_, err = module.RetryInvitation(t.Context(), curator.Token, person.ID, invitation.ID)
	require.ErrorIs(t, err, identity.ErrPreauthorizationRevoked, "retry keeps the same eligibility as sending")
	assert.Len(t, a.recorder.Sent(), 2, "the rejected attempt and the deliberate retry both reached the server")
}

func TestMissingSMTPLeavesClearStateWithoutBlockingPersonSetup(t *testing.T) {
	t.Parallel()
	a := newAdmission(t, false)
	module := a.identity
	curator := claimCurator(t, module)
	person, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Alex"})
	require.NoError(t, err)
	approval, err := module.Preauthorize(t.Context(), curator.Token, person.ID, identity.PreauthorizeRequest{Email: "alex@example.test"})
	require.NoError(t, err)
	_, err = module.SendInvitation(t.Context(), curator.Token, person.ID, identity.SendInvitationRequest{PreauthorizationID: approval.ID})
	require.ErrorIs(t, err, notifications.ErrMailUnconfigured)
	detail, err := module.GetPerson(t.Context(), curator.Token, person.ID)
	require.NoError(t, err)
	assert.Empty(t, detail.Invitations)
	assert.Equal(t, 0, a.queue.count())
	// Sign-in through the approval still works without email, and an unknown
	// identity still records its request without a Curator alert.
	_, err = module.SignIn(t.Context(), identity.FakeClaims(identity.SignInRequest{Email: "alex@example.test", DisplayName: "Alex"}))
	require.NoError(t, err)
	_, err = module.SignIn(t.Context(), identity.FakeClaims(identity.SignInRequest{Email: "stranger@example.test", DisplayName: "Stranger"}))
	require.ErrorIs(t, err, identity.ErrAccessRequested)
	assert.Equal(t, 0, a.queue.count())
	unwired := identity.New(a.db, nil)
	_, err = unwired.SendInvitation(t.Context(), curator.Token, person.ID, identity.SendInvitationRequest{PreauthorizationID: approval.ID})
	require.Error(t, err)
}
