package push

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"net/netip"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type staticResolver []netip.Addr

func (r staticResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return r, nil
}

type captureTransport struct {
	request *http.Request
	body    []byte
}

func (t *captureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.request = request.Clone(request.Context())
	var err error
	t.body, err = io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusCreated, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(nil)), Request: request}, nil
}

func TestWebPushSenderEncryptsPayloadAndAddsVAPIDWithoutApplicationCredentials(t *testing.T) {
	t.Parallel()
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	require.NoError(t, err)
	userKey, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	auth := make([]byte, 16)
	_, err = rand.Read(auth)
	require.NoError(t, err)
	capture := new(captureTransport)
	client := &http.Client{Transport: capture}
	policy := NewEndpointPolicy(staticResolver{netip.MustParseAddr("8.8.8.8")})
	sender := NewWebPushSender(config.PushConfig{
		PublicKey: publicKey, PrivateKey: privateKey, Subject: "mailto:operator@example.com", TTL: 15 * time.Minute,
	}, policy, client)
	plaintext := []byte(`{"version":1,"publication_count":1,"private_marker":"must-not-leak"}`)
	result, err := sender.Send(context.Background(), BrowserSubscription{
		Endpoint: "https://push.example/subscription",
		Keys: BrowserSubscriptionKeys{
			P256DH: base64.RawURLEncoding.EncodeToString(userKey.PublicKey().Bytes()),
			Auth:   base64.RawURLEncoding.EncodeToString(auth),
		},
	}, plaintext)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, result.StatusCode)
	require.NotNil(t, capture.request)
	assert.Equal(t, "aes128gcm", capture.request.Header.Get("Content-Encoding"))
	assert.Contains(t, capture.request.Header.Get("Authorization"), "vapid")
	assert.Equal(t, "900", capture.request.Header.Get("TTL"))
	assert.Empty(t, capture.request.Header.Get("Cookie"))
	assert.Empty(t, capture.request.Header.Get("X-Memento-CSRF"))
	assert.NotContains(t, string(capture.body), "must-not-leak")
	assert.NotEqual(t, plaintext, capture.body)
}
