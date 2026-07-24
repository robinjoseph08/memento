// Package smtp provides a certificate-verifying generic SMTP transport.
package smtp

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/mail"
	netsmtp "net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/robinjoseph08/memento/pkg/config"
)

var errNotEnabled = errors.New("SMTP is not enabled")

const (
	StatusDisabled            = "disabled"
	StatusOK                  = "ok"
	StatusUnavailable         = "unavailable"
	StatusInsecureDevelopment = "insecure_development"
)

// Message contains a complete required email. Callers must not log it.
type Message struct {
	ID      string
	To      string
	Subject string
	Body    string
}

// DeliveryError is the only dependency failure exposed outside this package.
// Diagnostic is allowlisted and never contains the raw SMTP response.
type DeliveryError struct {
	Diagnostic string
	Temporary  bool
}

func (e *DeliveryError) Error() string { return e.Diagnostic }

// Disabled reports that SMTP has not been configured.
type Disabled struct{}

func (Disabled) Status() string { return StatusDisabled }

// Sender sends one message through generic SMTP.
type Sender interface {
	Send(ctx context.Context, message Message) error
}

// Client is a secure generic SMTP sender.
type Client struct {
	cfg       config.SMTPConfig
	tlsConfig *tls.Config
	dialer    net.Dialer
	status    atomic.Int32
}

// New constructs a sender. Tests may provide tlsConfig for a local CA; nil
// uses the system roots. Certificate verification is never disabled.
func New(cfg config.SMTPConfig, tlsConfig *tls.Config) (*Client, error) {
	if !cfg.Enabled {
		return nil, errNotEnabled
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	serverName := cfg.ServerName
	if serverName == "" {
		serverName = cfg.Host
	}
	if tlsConfig == nil {
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName}
	} else {
		tlsConfig = tlsConfig.Clone()
		tlsConfig.MinVersion = tls.VersionTLS12
		tlsConfig.ServerName = serverName
		tlsConfig.InsecureSkipVerify = false
	}
	client := &Client{cfg: cfg, tlsConfig: tlsConfig, dialer: net.Dialer{Timeout: cfg.Timeout}}
	if cfg.Mode == "insecure" {
		client.status.Store(2)
	} else {
		client.status.Store(1)
	}
	return client, nil
}

// Status returns an allowlisted health detail.
func (c *Client) Status() string {
	switch c.status.Load() {
	case 1:
		return StatusOK
	case 2:
		return StatusInsecureDevelopment
	default:
		return StatusUnavailable
	}
}

// Send delivers one message and classifies failures without retaining raw responses.
func (c *Client) Send(ctx context.Context, message Message) error {
	conn, err := c.connect(ctx)
	if err != nil {
		failure := classify("connect", err)
		c.mark(failure)
		return failure
	}
	defer conn.Close()
	deadline := time.Now().Add(c.cfg.Timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		failure := &DeliveryError{Diagnostic: "smtp_unavailable", Temporary: true}
		c.mark(failure)
		return failure
	}

	client, err := netsmtp.NewClient(conn, c.cfg.Host)
	if err != nil {
		failure := classify("greeting", err)
		c.mark(failure)
		return failure
	}
	defer client.Close()
	if c.cfg.Mode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			failure := &DeliveryError{Diagnostic: "tls_required", Temporary: false}
			c.mark(failure)
			return failure
		}
		if err := client.StartTLS(c.tlsConfig); err != nil {
			failure := classify("tls", err)
			c.mark(failure)
			return failure
		}
	}
	if c.cfg.Username != "" {
		if ok, _ := client.Extension("AUTH"); !ok {
			failure := &DeliveryError{Diagnostic: "authentication_unavailable", Temporary: false}
			c.mark(failure)
			return failure
		}
		if err := client.Auth(plainAuth{username: c.cfg.Username, password: c.cfg.Password}); err != nil {
			failure := classify("auth", err)
			c.mark(failure)
			return failure
		}
	}
	from, _ := mail.ParseAddress(c.cfg.FromAddress)
	to, err := mail.ParseAddress(message.To)
	if err != nil || to.Address != message.To {
		return &DeliveryError{Diagnostic: "invalid_recipient", Temporary: false}
	}
	if err := client.Mail(from.Address); err != nil {
		failure := classify("mail", err)
		c.mark(failure)
		return failure
	}
	if err := client.Rcpt(to.Address); err != nil {
		failure := classify("recipient", err)
		c.mark(failure)
		return failure
	}
	writer, err := client.Data()
	if err != nil {
		failure := classify("data", err)
		c.mark(failure)
		return failure
	}
	if _, err := io.WriteString(writer, formatMessage(c.cfg.FromAddress, message)); err != nil {
		_ = writer.Close()
		failure := classify("body", err)
		c.mark(failure)
		return failure
	}
	if err := writer.Close(); err != nil {
		failure := classify("data", err)
		c.mark(failure)
		return failure
	}
	// writer.Close read the server's final acceptance response. A later QUIT
	// failure must not retry an already accepted message.
	_ = client.Quit()
	if c.cfg.Mode == "insecure" {
		c.status.Store(2)
	} else {
		c.status.Store(1)
	}
	return nil
}

func (c *Client) connect(ctx context.Context) (net.Conn, error) {
	address := net.JoinHostPort(c.cfg.Host, strconv.Itoa(c.cfg.Port))
	if c.cfg.Mode == "implicit_tls" {
		return (&tls.Dialer{NetDialer: &c.dialer, Config: c.tlsConfig}).DialContext(ctx, "tcp", address)
	}
	return c.dialer.DialContext(ctx, "tcp", address)
}

func (c *Client) mark(failure *DeliveryError) {
	if failure.Temporary || strings.HasPrefix(failure.Diagnostic, "tls_") || strings.HasPrefix(failure.Diagnostic, "authentication_") {
		c.status.Store(0)
	}
}

func classify(stage string, err error) *DeliveryError {
	var protocolError *textproto.Error
	if errors.As(err, &protocolError) {
		if protocolError.Code >= 400 && protocolError.Code < 500 {
			return &DeliveryError{Diagnostic: "smtp_unavailable", Temporary: true}
		}
		if stage == "recipient" {
			return &DeliveryError{Diagnostic: "recipient_rejected", Temporary: false}
		}
		if stage == "auth" {
			return &DeliveryError{Diagnostic: "authentication_rejected", Temporary: false}
		}
		return &DeliveryError{Diagnostic: "smtp_rejected", Temporary: false}
	}
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameError x509.HostnameError
	var certificateInvalid x509.CertificateInvalidError
	if errors.As(err, &unknownAuthority) || errors.As(err, &hostnameError) || errors.As(err, &certificateInvalid) {
		return &DeliveryError{Diagnostic: "tls_verification_failed", Temporary: false}
	}
	if stage == "tls" {
		return &DeliveryError{Diagnostic: "tls_verification_failed", Temporary: false}
	}
	return &DeliveryError{Diagnostic: "smtp_unavailable", Temporary: true}
}

func formatMessage(from string, message Message) string {
	var body strings.Builder
	writer := bufio.NewWriter(&body)
	_, _ = fmt.Fprintf(writer, "From: %s\r\nTo: %s\r\nSubject: %s\r\n", from, message.To, message.Subject)
	_, _ = fmt.Fprintf(writer, "Message-ID: <%s@memento.local>\r\n", message.ID)
	_, _ = io.WriteString(writer, "MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	_, _ = io.WriteString(writer, strings.ReplaceAll(message.Body, "\n", "\r\n"))
	_, _ = io.WriteString(writer, "\r\n")
	_ = writer.Flush()
	return body.String()
}

type plainAuth struct{ username, password string }

func (a plainAuth) Start(*netsmtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.username + "\x00" + a.password), nil
}
func (plainAuth) Next([]byte, bool) ([]byte, error) { return nil, nil }
