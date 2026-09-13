package notifications

import (
	"context"
	"net"
)

// SetSMTPDialForTest routes a mailer configured for any host to a local test server.
func SetSMTPDialForTest(m *SMTPMailer, address string) {
	m.dial = func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", address)
	}
}
