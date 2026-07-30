package push

import (
	"context"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fuzzResolver struct{ addresses []netip.Addr }

func (resolver fuzzResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return resolver.addresses, nil
}

func FuzzPushEndpointPolicy(f *testing.F) {
	f.Add("https://push.example/subscription", byte(8), byte(8), byte(8), byte(8), false)
	f.Add("https://user:secret@push.example/subscription", byte(8), byte(8), byte(8), byte(8), false)
	f.Add("https://push.example/subscription#fragment", byte(8), byte(8), byte(8), byte(8), false)
	f.Add("https://push.example/subscription", byte(127), byte(0), byte(0), byte(1), true)
	f.Fuzz(func(t *testing.T, endpoint string, a, b, c, d byte, mixed bool) {
		if len(endpoint) > 16<<10 {
			t.Skip()
		}
		addresses := []netip.Addr{netip.AddrFrom4([4]byte{a, b, c, d})}
		if mixed {
			addresses = append(addresses, netip.MustParseAddr("127.0.0.1"))
		}
		policy := NewEndpointPolicy(fuzzResolver{addresses: addresses})
		err := policy.Validate(context.Background(), endpoint)
		if err != nil {
			if strings.Contains(endpoint, "://") {
				assert.NotContains(t, err.Error(), endpoint, "endpoint tokens must not appear in errors")
			}
			return
		}
		parsed, parseErr := url.ParseRequestURI(endpoint)
		require.NoError(t, parseErr)
		assert.Equal(t, "https", parsed.Scheme)
		assert.NotEmpty(t, parsed.Host)
		assert.Nil(t, parsed.User)
		assert.Empty(t, parsed.Fragment)
		for _, address := range addresses {
			assert.True(t, publicAddress(address.Unmap()))
		}
	})
}
