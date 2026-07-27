package setup

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/uptrace/bun"
)

// RequestMetadata is the trusted request attribution stored with security audits.
type RequestMetadata struct {
	ClientIP  string
	UserAgent string
}

type requestMetadataKey struct{}

func withRequestMetadata(ctx context.Context, request *http.Request, trustedProxies []netip.Prefix) context.Context {
	metadata := RequestMetadata{ClientIP: clientIP(request, trustedProxies), UserAgent: request.UserAgent()}
	userAgentRunes := []rune(metadata.UserAgent)
	if len(userAgentRunes) > 512 {
		metadata.UserAgent = string(userAgentRunes[:512])
	}
	return context.WithValue(ctx, requestMetadataKey{}, metadata)
}

func metadataFromContext(ctx context.Context) RequestMetadata {
	metadata, _ := ctx.Value(requestMetadataKey{}).(RequestMetadata)
	return metadata
}

// ContextWithRequestMetadata records the trusted client address and bounded user agent.
func (s *Service) ContextWithRequestMetadata(ctx context.Context, request *http.Request) context.Context {
	return withRequestMetadata(ctx, request, s.security.TrustedProxyCIDRs)
}

// RequestMetadataFromContext returns request attribution prepared by ContextWithRequestMetadata.
func RequestMetadataFromContext(ctx context.Context) RequestMetadata {
	return metadataFromContext(ctx)
}

func clientIP(request *http.Request, trustedProxies []netip.Prefix) string {
	peer := request.RemoteAddr
	if host, _, err := net.SplitHostPort(peer); err == nil {
		peer = host
	}
	peerIP := net.ParseIP(peer)
	if peerIP == nil {
		return ""
	}
	peerAddress, valid := netip.AddrFromSlice(peerIP)
	if valid && addressInPrefixes(peerAddress.Unmap(), trustedProxies) {
		if forwardedByCaddy := net.ParseIP(strings.TrimSpace(request.Header.Get("X-Memento-Client-IP"))); forwardedByCaddy != nil {
			return forwardedByCaddy.String()
		}
		forwarded := strings.Split(request.Header.Get("X-Forwarded-For"), ",")
		for index := len(forwarded) - 1; index >= 0; index-- {
			candidate := net.ParseIP(strings.TrimSpace(forwarded[index]))
			if candidate != nil {
				return candidate.String()
			}
		}
	}
	return peerIP.String()
}

func secureSetupCompletion(request *http.Request, trustedProxies []netip.Prefix) bool {
	if request.TLS != nil {
		return true
	}
	host := request.Host
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	if host == "localhost" {
		return true
	}
	if hostAddress, err := netip.ParseAddr(host); err == nil && hostAddress.IsLoopback() {
		return true
	}
	peer := request.RemoteAddr
	if peerHost, _, err := net.SplitHostPort(peer); err == nil {
		peer = peerHost
	}
	peerAddress, err := netip.ParseAddr(peer)
	if err != nil || !addressInPrefixes(peerAddress.Unmap(), trustedProxies) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(request.Header.Get("X-Forwarded-Proto")), "https")
}

func addressInPrefixes(address netip.Addr, prefixes []netip.Prefix) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

type rateWindow struct {
	startedAt time.Time
	count     int
}

type setupLimiter struct {
	mu          sync.Mutex
	window      time.Duration
	emailLimit  int
	ipLimit     int
	secret      []byte
	entries     map[string]rateWindow
	now         func() time.Time
	lastCleanup time.Time
}

func newSetupLimiter(cfg config.SecurityConfig) *setupLimiter {
	return &setupLimiter{
		window: cfg.SetupRateWindow, emailLimit: cfg.SetupEmailLimit, ipLimit: cfg.SetupIPLimit,
		secret: []byte(cfg.Secret), entries: make(map[string]rateWindow), now: time.Now,
	}
}

func (l *setupLimiter) allowRequestCode(ip, email string) bool {
	keys := make([]rateKey, 0, 2)
	if ip != "" {
		keys = append(keys, rateKey{value: "ip:" + ip, limit: l.ipLimit})
	}
	if email != "" {
		mac := hmac.New(sha256.New, l.secret)
		_, _ = mac.Write([]byte("setup-rate:"))
		_, _ = mac.Write([]byte(strings.ToLower(strings.TrimSpace(email))))
		keys = append(keys, rateKey{value: "email:" + hex.EncodeToString(mac.Sum(nil)), limit: l.emailLimit})
	}
	return l.allow(keys)
}

func (l *setupLimiter) allowIP(ip string) bool {
	if ip == "" {
		return true
	}
	return l.allow([]rateKey{{value: "ip:" + ip, limit: l.ipLimit}})
}

type rateKey struct {
	value string
	limit int
}

func (l *setupLimiter) allow(keys []rateKey) bool {
	if len(keys) == 0 {
		return true
	}
	now := l.now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lastCleanup.IsZero() || now.Sub(l.lastCleanup) >= l.window {
		for key, entry := range l.entries {
			if now.Sub(entry.startedAt) >= l.window {
				delete(l.entries, key)
			}
		}
		l.lastCleanup = now
	}
	for _, key := range keys {
		entry := l.entries[key.value]
		if now.Sub(entry.startedAt) >= l.window {
			entry = rateWindow{startedAt: now}
		}
		if entry.count >= key.limit {
			return false
		}
	}
	for _, key := range keys {
		entry := l.entries[key.value]
		if now.Sub(entry.startedAt) >= l.window {
			entry = rateWindow{startedAt: now}
		}
		entry.count++
		l.entries[key.value] = entry
	}
	return true
}

func (s *Service) appendAudit(
	ctx context.Context,
	tx bun.Tx,
	action, outcome string,
	actorPersonID, subjectPersonID, sessionID *uuid.UUID,
) error {
	metadata := metadataFromContext(ctx)
	_, err := tx.NewRaw(`
		INSERT INTO security_audit_events (
			actor_person_id, subject_person_id, action, outcome, client_ip, user_agent, session_id
		) VALUES (?, ?, ?, ?, NULLIF(?, '')::inet, ?, ?)
	`, actorPersonID, subjectPersonID, action, outcome, metadata.ClientIP, metadata.UserAgent, sessionID).Exec(ctx)
	return err
}
