package recipients

import (
	"context"
	"testing"

	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/require"
)

func TestOnboardingRejectsInvalidOrIncompleteChoicesBeforePersistence(t *testing.T) {
	service := New(nil, nil, "")
	actor := setup.SessionActor{}

	_, err := service.SaveOnboarding(context.Background(), actor, OnboardingRequest{
		EmailPreference: "sometimes",
		SessionType:     "trusted",
	}, "csrf")
	require.ErrorIs(t, err, ErrOnboardingChoices)

	_, err = service.CompleteOnboarding(context.Background(), actor, OnboardingRequest{
		EmailPreference: "immediate",
		SessionType:     "trusted",
	})
	require.ErrorIs(t, err, ErrOnboardingChoices)
}
