package emaildelivery

import (
	"context"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/smtp"
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
