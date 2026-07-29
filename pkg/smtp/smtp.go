// Package smtp provides a certificate-verifying generic SMTP transport.
package smtp

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
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

// EmbeddedImage is a private inline image whose bytes travel only in the email.
type EmbeddedImage struct {
	ContentID   string
	ContentType string
	Data        []byte
}

// Message contains a complete email. Callers must not log it.
type Message struct {
	ID             string
	To             string
	Subject        string
	Body           string
	UnsubscribeURL string
	Embedded       *EmbeddedImage
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
	}
	return client, nil
}

// Status returns an allowlisted health detail.
func (c *Client) Status() string {
	if c.cfg.Mode == "insecure" {
		return StatusInsecureDevelopment
	}
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
		return c.fail(ctx, "connect", err)
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
	stopCancellation := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stopCancellation()

	client, err := netsmtp.NewClient(conn, c.cfg.Host)
	if err != nil {
		return c.fail(ctx, "greeting", err)
	}
	defer client.Close()
	if c.cfg.Mode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			failure := &DeliveryError{Diagnostic: "tls_required", Temporary: false}
			c.mark(failure)
			return failure
		}
		if err := client.StartTLS(c.tlsConfig); err != nil {
			return c.fail(ctx, "tls", err)
		}
	}
	if c.cfg.Username != "" {
		if ok, _ := client.Extension("AUTH"); !ok {
			failure := &DeliveryError{Diagnostic: "authentication_unavailable", Temporary: false}
			c.mark(failure)
			return failure
		}
		if err := client.Auth(plainAuth{username: c.cfg.Username, password: c.cfg.Password}); err != nil {
			return c.fail(ctx, "auth", err)
		}
	}
	from, _ := mail.ParseAddress(c.cfg.FromAddress)
	to, err := mail.ParseAddress(message.To)
	if err != nil || to.Address != message.To {
		return &DeliveryError{Diagnostic: "invalid_recipient", Temporary: false}
	}
	if err := client.Mail(from.Address); err != nil {
		return c.fail(ctx, "mail", err)
	}
	if err := client.Rcpt(to.Address); err != nil {
		return c.fail(ctx, "recipient", err)
	}
	writer, err := client.Data()
	if err != nil {
		return c.fail(ctx, "data", err)
	}
	if _, err := io.WriteString(writer, formatMessage(c.cfg.FromAddress, message)); err != nil {
		_ = writer.Close()
		return c.fail(ctx, "body", err)
	}
	if err := writer.Close(); err != nil {
		return c.fail(ctx, "data", err)
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

func (c *Client) fail(ctx context.Context, stage string, err error) error {
	failure := classify(stage, err)
	c.mark(failure)
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	return failure
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
		var networkError net.Error
		if errors.As(err, &networkError) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return &DeliveryError{Diagnostic: "smtp_unavailable", Temporary: true}
		}
		return &DeliveryError{Diagnostic: "tls_verification_failed", Temporary: false}
	}
	return &DeliveryError{Diagnostic: "smtp_unavailable", Temporary: true}
}

func formatMessage(from string, message Message) string {
	var body strings.Builder
	writer := bufio.NewWriter(&body)
	_, _ = fmt.Fprintf(writer, "From: %s\r\nTo: %s\r\nSubject: %s\r\n", from, message.To, safeHeader(message.Subject))
	_, _ = fmt.Fprintf(writer, "Message-ID: <%s@memento.local>\r\n", safeHeader(message.ID))
	if message.UnsubscribeURL != "" {
		_, _ = fmt.Fprintf(writer, "List-Unsubscribe: <%s>\r\n", safeHeader(message.UnsubscribeURL))
		_, _ = io.WriteString(writer, "List-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n")
	}
	if message.Embedded == nil {
		_, _ = io.WriteString(writer, "MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
		_, _ = io.WriteString(writer, strings.ReplaceAll(message.Body, "\n", "\r\n"))
		_, _ = io.WriteString(writer, "\r\n")
		_ = writer.Flush()
		return body.String()
	}

	boundary := "memento-related-" + safeHeader(message.ID)
	_, _ = fmt.Fprintf(writer, "MIME-Version: 1.0\r\nContent-Type: multipart/related; boundary=%q\r\n\r\n", boundary)
	_, _ = fmt.Fprintf(writer, "--%s\r\nContent-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary, boundary+"-alternative")
	_, _ = fmt.Fprintf(writer, "--%s-alternative\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n", boundary)
	_, _ = io.WriteString(writer, strings.ReplaceAll(message.Body, "\n", "\r\n"))
	_, _ = fmt.Fprintf(writer, "\r\n--%s-alternative\r\nContent-Type: text/html; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n", boundary)
	htmlBody := strings.ReplaceAll(html.EscapeString(message.Body), "\n", "<br>\r\n")
	_, _ = fmt.Fprintf(writer, "<p>%s</p><p><img src=\"cid:%s\" alt=\"Authorized Memento preview\" style=\"max-width:480px;max-height:480px\"></p>\r\n", htmlBody, safeHeader(message.Embedded.ContentID))
	_, _ = fmt.Fprintf(writer, "--%s-alternative--\r\n\r\n", boundary)
	_, _ = fmt.Fprintf(writer, "--%s\r\nContent-Type: %s\r\nContent-Transfer-Encoding: base64\r\nContent-ID: <%s>\r\nContent-Disposition: inline; filename=\"memento-preview.jpg\"\r\n\r\n", boundary, safeHeader(message.Embedded.ContentType), safeHeader(message.Embedded.ContentID))
	encoded := base64.StdEncoding.EncodeToString(message.Embedded.Data)
	for len(encoded) > 76 {
		_, _ = io.WriteString(writer, encoded[:76]+"\r\n")
		encoded = encoded[76:]
	}
	_, _ = io.WriteString(writer, encoded+"\r\n")
	_, _ = fmt.Fprintf(writer, "--%s--\r\n", boundary)
	_ = writer.Flush()
	return body.String()
}

func safeHeader(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r", ""), "\n", "")
}

type plainAuth struct{ username, password string }

func (a plainAuth) Start(*netsmtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.username + "\x00" + a.password), nil
}
func (plainAuth) Next([]byte, bool) ([]byte, error) { return nil, nil }
