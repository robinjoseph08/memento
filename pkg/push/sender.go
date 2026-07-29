package push

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/robinjoseph08/memento/pkg/config"
)

// DeliveryResult contains only the provider status needed for safe classification.
type DeliveryResult struct {
	StatusCode int
	RetryAfter time.Duration
}

// Sender delivers an encrypted Web Push payload.
type Sender interface {
	Send(ctx context.Context, subscription BrowserSubscription, payload []byte) (DeliveryResult, error)
}

// WebPushSender applies endpoint policy and standards-based payload encryption and VAPID.
type WebPushSender struct {
	cfg    config.PushConfig
	policy *EndpointPolicy
	client *http.Client
}

func NewWebPushSender(cfg config.PushConfig, policy *EndpointPolicy, client *http.Client) *WebPushSender {
	if policy == nil {
		policy = NewEndpointPolicy(nil)
	}
	if client == nil {
		client = NewHTTPClient(policy, nil, nil, cfg.Timeout)
	}
	return &WebPushSender{cfg: cfg, policy: policy, client: client}
}

func (s *WebPushSender) Send(ctx context.Context, subscription BrowserSubscription, payload []byte) (DeliveryResult, error) {
	if err := s.policy.Validate(ctx, subscription.Endpoint); err != nil {
		return DeliveryResult{}, err
	}
	response, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{
		Endpoint: subscription.Endpoint,
		Keys:     webpush.Keys{P256dh: subscription.Keys.P256DH, Auth: subscription.Keys.Auth},
	}, &webpush.Options{
		HTTPClient: s.client, Subscriber: s.cfg.Subject, TTL: int(s.cfg.TTL.Seconds()),
		VAPIDPublicKey: s.cfg.PublicKey, VAPIDPrivateKey: s.cfg.PrivateKey,
	})
	if err != nil {
		return DeliveryResult{}, fmt.Errorf("send encrypted Web Push: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	return DeliveryResult{StatusCode: response.StatusCode,
		RetryAfter: parseRetryAfter(response.Header.Get("Retry-After"), time.Now())}, nil
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil && retryAt.After(now) {
		return retryAt.Sub(now)
	}
	return 0
}
