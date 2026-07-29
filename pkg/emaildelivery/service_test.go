package emaildelivery

import (
	"context"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/smtp"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type discardSender struct{}

func (discardSender) Send(context.Context, smtp.Message) error { return nil }

func TestQueueRequiredRejectsUnknownEmailKindBeforePersistence(t *testing.T) {
	service := New(nil, config.SMTPConfig{Enabled: true}, discardSender{})
	_, _, err := service.QueueRequired(context.Background(), bun.Tx{}, RequiredMessage{Kind: "optional"})
	require.ErrorIs(t, err, errUnsupportedKind)
}

func TestSetupCodeBodyIsEncryptedForPersistenceAndStableRestarts(t *testing.T) {
	const secret = "test-only-security-secret-32-bytes"
	service := New(nil, config.SMTPConfig{}, nil, secret)
	plaintext := "Your setup code is 12345678."
	message := RequiredMessage{Kind: KindSetupCode, Body: plaintext}

	persisted, err := service.persistedBody(message)
	require.NoError(t, err)
	second, err := service.persistedBody(message)
	require.NoError(t, err)
	assert.NotContains(t, persisted, "12345678")
	assert.NotEqual(t, persisted, second, "each body needs a fresh authenticated-encryption nonce")

	restarted := New(nil, config.SMTPConfig{}, nil, secret)
	decrypted, err := restarted.deliveryBody(KindSetupCode, persisted)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
	_, err = New(nil, config.SMTPConfig{}, nil, "different-security-secret-32-bytes").deliveryBody(KindSetupCode, persisted)
	require.ErrorIs(t, err, errSensitiveBody)
}

func TestImmediateHandlerRejectsInvalidPayloadBeforeDependencies(t *testing.T) {
	err := new(Service).HandleImmediate(context.Background(), worker.Job{Payload: []byte(`{"batch_id":"private"}`)})
	assert.EqualError(t, err, "invalid_immediate_email_job")
}

func TestImmediateBatchItemKindRejectsUnknownValues(t *testing.T) {
	_, err := batchItemKind("future_kind").spec()
	require.ErrorIs(t, err, errUnsupportedImmediateItemKind)
}

func TestSafePreviewRejectsUndecodableBytes(t *testing.T) {
	_, err := safePreview([]byte("private source metadata without an image"))
	require.Error(t, err)
}

func TestRetryDelayStaysWithinConfiguredExponentialBounds(t *testing.T) {
	service := New(nil, config.SMTPConfig{RetryBase: 100 * time.Millisecond, RetryMax: 400 * time.Millisecond}, nil)

	tests := []struct {
		attempts int
		minimum  time.Duration
		maximum  time.Duration
	}{
		{attempts: 0, minimum: 80 * time.Millisecond, maximum: 120 * time.Millisecond},
		{attempts: 1, minimum: 160 * time.Millisecond, maximum: 240 * time.Millisecond},
		{attempts: 2, minimum: 320 * time.Millisecond, maximum: 400 * time.Millisecond},
		{attempts: 20, minimum: 320 * time.Millisecond, maximum: 400 * time.Millisecond},
	}
	for _, test := range tests {
		delay := service.retryDelay(test.attempts)
		assert.GreaterOrEqual(t, delay, test.minimum)
		assert.LessOrEqual(t, delay, test.maximum)
	}
}
