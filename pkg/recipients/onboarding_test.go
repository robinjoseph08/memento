package recipients

import (
	"context"
	"testing"

	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/require"
)

func TestOnboardingRejectsInvalidOrIncompleteChoicesBeforePersistence(t *testing.T) {
	service := New(nil, nil, "", nil)
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

	_, err = service.CompleteOnboarding(context.Background(), actor, OnboardingRequest{
		PrivacyAcknowledged: true, EngagementAcknowledged: true,
		InterestListAcknowledged: true, EmailPreviewsAcknowledged: true,
		PushGuidanceAcknowledged: true, EmailPreference: "immediate",
	})
	require.ErrorIs(t, err, ErrOnboardingChoices, "completion requires an explicit browser choice")

	acknowledgments := []struct {
		name  string
		clear func(*OnboardingRequest)
	}{
		{"private access", func(request *OnboardingRequest) { request.PrivacyAcknowledged = false }},
		{"engagement", func(request *OnboardingRequest) { request.EngagementAcknowledged = false }},
		{"Interest list", func(request *OnboardingRequest) { request.InterestListAcknowledged = false }},
		{"email previews", func(request *OnboardingRequest) { request.EmailPreviewsAcknowledged = false }},
		{"push guidance", func(request *OnboardingRequest) { request.PushGuidanceAcknowledged = false }},
	}
	for _, acknowledgment := range acknowledgments {
		t.Run(acknowledgment.name, func(t *testing.T) {
			request := OnboardingRequest{
				PrivacyAcknowledged: true, EngagementAcknowledged: true,
				InterestListAcknowledged: true, EmailPreviewsAcknowledged: true,
				PushGuidanceAcknowledged: true, EmailPreference: "immediate", SessionType: "trusted",
			}
			acknowledgment.clear(&request)
			_, err := service.CompleteOnboarding(context.Background(), actor, request)
			require.ErrorIs(t, err, ErrOnboardingChoices)
		})
	}
}
