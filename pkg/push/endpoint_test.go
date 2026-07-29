package push

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type resolverAnswer struct {
	addresses []netip.Addr
	err       error
}

type sequenceResolver struct {
	mu      sync.Mutex
	answers []resolverAnswer
	calls   int
}

func (r *sequenceResolver) LookupNetIP(_ context.Context, _, _ string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	answer := r.answers[min(r.calls-1, len(r.answers)-1)]
	return answer.addresses, answer.err
}

type recordingDialer struct {
	called bool
}

func (d *recordingDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	d.called = true
	return nil, errors.New("test dial")
}

func TestEndpointPolicyRejectsUnsafeDestinations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		endpoint  string
		addresses []netip.Addr
	}{
		{name: "http", endpoint: "http://push.example/subscription", addresses: []netip.Addr{netip.MustParseAddr("8.8.8.8")}},
		{name: "credentials", endpoint: "https://user:secret@push.example/subscription", addresses: []netip.Addr{netip.MustParseAddr("8.8.8.8")}},
		{name: "loopback", endpoint: "https://push.example/subscription", addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")}},
		{name: "private", endpoint: "https://push.example/subscription", addresses: []netip.Addr{netip.MustParseAddr("10.0.0.1")}},
		{name: "link local", endpoint: "https://push.example/subscription", addresses: []netip.Addr{netip.MustParseAddr("169.254.169.254")}},
		{name: "documentation", endpoint: "https://push.example/subscription", addresses: []netip.Addr{netip.MustParseAddr("2001:db8::1")}},
		{name: "mixed answer", endpoint: "https://push.example/subscription", addresses: []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("192.168.1.1")}},
		{name: "mapped private", endpoint: "https://push.example/subscription", addresses: []netip.Addr{netip.MustParseAddr("::ffff:10.0.0.1")}},
		{name: "NAT64", endpoint: "https://push.example/subscription", addresses: []netip.Addr{netip.MustParseAddr("64:ff9b::a00:1")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			policy := NewEndpointPolicy(&sequenceResolver{answers: []resolverAnswer{{addresses: test.addresses}}})
			assert.Error(t, policy.Validate(context.Background(), test.endpoint))
		})
	}
}

func TestEndpointPolicyAcceptsOnlyPublicHTTPS(t *testing.T) {
	t.Parallel()
	policy := NewEndpointPolicy(&sequenceResolver{answers: []resolverAnswer{{addresses: []netip.Addr{
		netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("2606:4700:4700::1111"),
	}}}})
	require.NoError(t, policy.Validate(context.Background(), "https://push.example:443/subscription?token=value"))
}

func TestHTTPClientRejectsDNSRebindingBeforeDial(t *testing.T) {
	t.Parallel()
	resolver := &sequenceResolver{answers: []resolverAnswer{
		{addresses: []netip.Addr{netip.MustParseAddr("8.8.8.8")}},
		{addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")}},
	}}
	policy := NewEndpointPolicy(resolver)
	require.NoError(t, policy.Validate(context.Background(), "https://push.example/subscription"))
	dialer := &recordingDialer{}
	client := NewHTTPClient(policy, dialer, &tls.Config{MinVersion: tls.VersionTLS12}, time.Second)
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://push.example/subscription", nil)
	require.NoError(t, err)
	response, err := client.Do(request)
	if response != nil {
		response.Body.Close()
	}
	require.ErrorIs(t, err, ErrEndpointPrivate)
	assert.False(t, dialer.called, "the private rebinding answer must never reach the socket dialer")
	assert.Equal(t, 2, resolver.calls)
}

func TestHTTPClientDisablesProxyAndRedirects(t *testing.T) {
	t.Parallel()
	client := NewHTTPClient(NewEndpointPolicy(&sequenceResolver{answers: []resolverAnswer{{addresses: []netip.Addr{netip.MustParseAddr("8.8.8.8")}}}}), &recordingDialer{}, nil, time.Second)
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	assert.Nil(t, transport.Proxy)
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://push.example/next", nil)
	require.NoError(t, err)
	assert.ErrorIs(t, client.CheckRedirect(request, nil), http.ErrUseLastResponse)
}
