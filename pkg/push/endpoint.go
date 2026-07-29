package push

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var (
	ErrEndpointInvalid = errors.New("push endpoint is invalid")
	ErrEndpointPrivate = errors.New("push endpoint does not resolve only to public addresses")
	errEmptyDNSAnswer  = errors.New("push endpoint DNS answer is empty")
)

const maximumEndpointLength = 4096

// Resolver is the controlled DNS seam used at validation and connection time.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Dialer is the controlled socket seam used only after address validation.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// EndpointPolicy validates untrusted Web Push destinations.
type EndpointPolicy struct {
	resolver Resolver
}

func NewEndpointPolicy(resolver Resolver) *EndpointPolicy {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return &EndpointPolicy{resolver: resolver}
}

// Validate requires credential-free HTTPS and an all-public DNS answer set.
func (p *EndpointPolicy) Validate(ctx context.Context, endpoint string) error {
	if len(endpoint) == 0 || len(endpoint) > maximumEndpointLength {
		return ErrEndpointInvalid
	}
	parsed, err := url.ParseRequestURI(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return ErrEndpointInvalid
	}
	if strings.HasSuffix(parsed.Host, ":") || parsed.Hostname() == "" {
		return ErrEndpointInvalid
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return ErrEndpointInvalid
		}
	}
	_, err = p.resolvePublic(ctx, parsed.Hostname())
	return err
}

func (p *EndpointPolicy) resolvePublic(ctx context.Context, hostname string) ([]netip.Addr, error) {
	if literal, err := netip.ParseAddr(strings.TrimSuffix(hostname, ".")); err == nil {
		literal = literal.Unmap()
		if !publicAddress(literal) {
			return nil, ErrEndpointPrivate
		}
		return []netip.Addr{literal}, nil
	}
	addresses, err := p.resolver.LookupNetIP(ctx, "ip", hostname)
	if err != nil || len(addresses) == 0 {
		if err == nil {
			err = errEmptyDNSAnswer
		}
		return nil, fmt.Errorf("resolve push endpoint: %w", err)
	}
	validated := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if !publicAddress(address) {
			return nil, ErrEndpointPrivate
		}
		validated = append(validated, address)
	}
	return validated, nil
}

var nonPublicPrefixes = mustPrefixes(
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
	"172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16",
	"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
	"::/128", "::1/128", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/23",
	"2001:db8::/32", "2002::/16", "3fff::/20", "5f00::/16", "fc00::/7", "fe80::/10", "ff00::/8",
)

func mustPrefixes(values ...string) []netip.Prefix {
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		result = append(result, netip.MustParsePrefix(value))
	}
	return result
}

func publicAddress(address netip.Addr) bool {
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() ||
		address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

// NewHTTPClient creates a proxy-free, redirect-free client that re-resolves and pins every connection.
func NewHTTPClient(policy *EndpointPolicy, dialer Dialer, tlsConfig *tls.Config, timeout time.Duration) *http.Client {
	if policy == nil {
		policy = NewEndpointPolicy(nil)
	}
	if dialer == nil {
		dialer = &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	}
	transport := &http.Transport{
		Proxy:                 nil,
		TLSClientConfig:       tlsConfig,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          20,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, ErrEndpointInvalid
			}
			addresses, err := policy.resolvePublic(ctx, host)
			if err != nil {
				return nil, err
			}
			var lastErr error
			for _, resolved := range addresses {
				connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.String(), port))
				if err == nil {
					return connection, nil
				}
				lastErr = err
			}
			if lastErr == nil {
				lastErr = ErrEndpointPrivate
			}
			return nil, lastErr
		},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
