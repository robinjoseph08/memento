package setup

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

const unitSecret = "test-only-security-secret-32-bytes"

var errTestRandomUnavailable = errors.New("test randomness unavailable")

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errTestRandomUnavailable }

func TestSetupRejectsInvalidInputBeforePersistence(t *testing.T) {
	service := New(nil, nil, unitSecret)

	_, err := service.RequestCode(context.Background(), RequestCodeRequest{DisplayName: "", Email: "invalid"})
	require.ErrorIs(t, err, ErrInvalidIdentity)
	_, err = service.VerifyCode(context.Background(), VerifyCodeRequest{ChallengeID: "invalid", Code: "12345678"})
	require.ErrorIs(t, err, ErrInvalidCode)
	_, err = service.complete(context.Background(), CompleteRequest{VerificationToken: "invalid"})
	require.ErrorIs(t, err, ErrInvalidToken)

	validToken := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	_, err = service.complete(context.Background(), CompleteRequest{VerificationToken: validToken})
	require.ErrorIs(t, err, ErrInvalidChoices)
}

func TestSetupFailsBeforePersistenceWhenSecureRandomnessIsUnavailable(t *testing.T) {
	service := New(nil, nil, unitSecret)
	service.random = failingReader{}

	_, err := service.RequestCode(context.Background(), RequestCodeRequest{DisplayName: "Robin", Email: "robin@example.com"})
	require.ErrorIs(t, err, errGenerateCredential)
	_, err = service.VerifyCode(context.Background(), VerifyCodeRequest{
		ChallengeID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Code:        "12345678",
	})
	require.ErrorIs(t, err, errGenerateCredential)
	_, err = service.complete(context.Background(), CompleteRequest{
		VerificationToken:        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PrivacyAcknowledged:      true,
		EngagementAcknowledged:   true,
		InterestListAcknowledged: true,
		EmailPreference:          "immediate",
		SessionType:              "trusted",
	})
	require.ErrorIs(t, err, errGenerateCredential)
}
