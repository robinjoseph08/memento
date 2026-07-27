package setup

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/stretchr/testify/require"
)

const unitSecret = "test-only-security-secret-32-bytes"

func testSecurityConfig() config.SecurityConfig {
	return config.SecurityConfig{
		Secret: unitSecret, SetupRateWindow: 15 * time.Minute,
		SetupEmailLimit: 3, SetupIPLimit: 20,
		TrustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
	}
}

var errTestRandomUnavailable = errors.New("test randomness unavailable")

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errTestRandomUnavailable }

func TestSetupRejectsInvalidInputBeforePersistence(t *testing.T) {
	service := New(nil, nil, testSecurityConfig())

	_, err := service.RequestCode(context.Background(), RequestCodeRequest{DisplayName: "", Email: "invalid"})
	require.ErrorIs(t, err, ErrInvalidIdentity)
	_, err = service.VerifyCode(context.Background(), VerifyCodeRequest{ChallengeID: "invalid", Code: "12345678"})
	require.ErrorIs(t, err, ErrInvalidCode)
	_, err = service.complete(context.Background(), CompleteRequest{VerificationToken: "invalid"})
	require.ErrorIs(t, err, ErrInvalidToken)

	validToken := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	_, err = service.complete(context.Background(), CompleteRequest{VerificationToken: validToken})
	require.ErrorIs(t, err, ErrInvalidChoices)

	acknowledgments := []struct {
		name  string
		clear func(*CompleteRequest)
	}{
		{"private access", func(request *CompleteRequest) { request.PrivacyAcknowledged = false }},
		{"engagement", func(request *CompleteRequest) { request.EngagementAcknowledged = false }},
		{"Interest list", func(request *CompleteRequest) { request.InterestListAcknowledged = false }},
		{"email previews", func(request *CompleteRequest) { request.EmailPreviewsAcknowledged = false }},
		{"push guidance", func(request *CompleteRequest) { request.PushGuidanceAcknowledged = false }},
	}
	for _, acknowledgment := range acknowledgments {
		t.Run(acknowledgment.name, func(t *testing.T) {
			request := CompleteRequest{
				VerificationToken:   validToken,
				PrivacyAcknowledged: true, EngagementAcknowledged: true,
				InterestListAcknowledged: true, EmailPreviewsAcknowledged: true,
				PushGuidanceAcknowledged: true, EmailPreference: "immediate", SessionType: "trusted",
			}
			acknowledgment.clear(&request)
			_, err := service.complete(context.Background(), request)
			require.ErrorIs(t, err, ErrInvalidChoices)
		})
	}
}

func TestDescribeUserAgentRecognizesIOSBrowserTokens(t *testing.T) {
	tests := []struct {
		userAgent string
		browser   string
	}{
		{userAgent: "Mozilla/5.0 (iPhone) AppleWebKit/605.1.15 CriOS/126.0 Mobile/15E148 Safari/604.1", browser: "Chrome"},
		{userAgent: "Mozilla/5.0 (iPhone) AppleWebKit/605.1.15 FxiOS/127.0 Mobile/15E148 Safari/605.1.15", browser: "Firefox"},
		{userAgent: "Mozilla/5.0 (iPhone) AppleWebKit/605.1.15 EdgiOS/126.0 Mobile/15E148 Safari/605.1.15", browser: "Edge"},
	}
	for _, test := range tests {
		browser, platform := describeUserAgent(test.userAgent)
		require.Equal(t, test.browser, browser)
		require.Equal(t, "iOS", platform)
	}
}

func TestSetupFailsBeforePersistenceWhenSecureRandomnessIsUnavailable(t *testing.T) {
	service := New(nil, nil, testSecurityConfig())
	service.random = failingReader{}

	_, err := service.RequestCode(context.Background(), RequestCodeRequest{DisplayName: "Robin", Email: "robin@example.com"})
	require.ErrorIs(t, err, errGenerateCredential)
	_, err = service.VerifyCode(context.Background(), VerifyCodeRequest{
		ChallengeID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Code:        "12345678",
	})
	require.ErrorIs(t, err, errGenerateCredential)
	_, err = service.complete(context.Background(), CompleteRequest{
		VerificationToken:         "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PrivacyAcknowledged:       true,
		EngagementAcknowledged:    true,
		InterestListAcknowledged:  true,
		EmailPreviewsAcknowledged: true,
		PushGuidanceAcknowledged:  true,
		EmailPreference:           "immediate",
		SessionType:               "trusted",
	})
	require.ErrorIs(t, err, errGenerateCredential)
}
