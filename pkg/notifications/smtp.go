package notifications

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
)

const (
	smtpSessionTimeout = 60 * time.Second
	// smtpCheckTimeout bounds the Settings diagnostic, which a person waits on.
	smtpCheckTimeout = 10 * time.Second
)

// SMTPMailer delivers through one configured server. smtp:// connects in plain
// text and upgrades with STARTTLS when the server offers it; smtps:// uses TLS
// from the first byte.
type SMTPMailer struct {
	address     string
	host        string
	implicitTLS bool
	username    string
	password    string
	from        *mail.Address
	// dial is replaceable so tests can hand the adapter a paused connection.
	dial func(context.Context, string) (net.Conn, error)
}

// NewSMTPMailer validates the configured URL without connecting. Errors name
// the setting, never the URL, because it may contain credentials.
func NewSMTPMailer(rawURL, from string) (*SMTPMailer, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" || u.Opaque != "" || (u.Scheme != "smtp" && u.Scheme != "smtps") {
		return nil, fmt.Errorf("smtp_url: must be smtp://host:port or smtps://host:port")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("smtp_url: must not contain a path, query, or fragment")
	}
	port := u.Port()
	if port == "" {
		port = "587"
		if u.Scheme == "smtps" {
			port = "465"
		}
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return nil, fmt.Errorf("smtp_url: port must be between 1 and 65535")
	}
	sender, err := mail.ParseAddress(strings.TrimSpace(from))
	if err != nil {
		return nil, fmt.Errorf("smtp_from: must be a valid email address")
	}
	m := &SMTPMailer{address: net.JoinHostPort(u.Hostname(), port), host: u.Hostname(), implicitTLS: u.Scheme == "smtps", from: sender}
	if u.User != nil {
		m.username = u.User.Username()
		m.password, _ = u.User.Password()
	}
	m.dial = func(ctx context.Context, address string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", address)
	}
	return m, nil
}

// connect opens one session: dial, TLS, and sign-in when credentials are
// configured. The returned function releases the connection; call it once.
func (m *SMTPMailer) connect(ctx context.Context) (*smtp.Client, func(), error) {
	conn, err := m.dial(ctx, m.address)
	if err != nil {
		return nil, nil, classifySMTP(errorstack.CaptureContext(ctx, err), false)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	release := func() {
		stop()
		_ = conn.Close()
	}
	_ = conn.SetDeadline(time.Now().Add(smtpSessionTimeout))
	if m.implicitTLS {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: m.host, MinVersion: tls.VersionTLS12})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			release()
			return nil, nil, classifySMTP(errorstack.CaptureContext(ctx, err), false)
		}
		conn = tlsConn
	}
	client, err := smtp.NewClient(conn, m.host)
	if err != nil {
		release()
		return nil, nil, classifySMTP(errorstack.CaptureContext(ctx, err), false)
	}
	if !m.implicitTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: m.host, MinVersion: tls.VersionTLS12}); err != nil {
				release()
				return nil, nil, classifySMTP(errorstack.CaptureContext(ctx, err), false)
			}
		}
	}
	if m.username != "" {
		if err := client.Auth(smtp.PlainAuth("", m.username, m.password, m.host)); err != nil {
			release()
			// Go refuses PLAIN over an unencrypted connection and servers without
			// AUTH; both are configuration problems that retrying cannot fix.
			if _, reply := errors.AsType[*textproto.Error](err); !reply {
				return nil, nil, &DeliveryError{Outcome: OutcomePermanent, Summary: "The mail server did not accept sign-in. Credentials need smtps:// or a server that offers STARTTLS and AUTH.", Cause: errorstack.CaptureContext(ctx, err)}
			}
			return nil, nil, classifySMTP(errorstack.CaptureContext(ctx, err), false)
		}
	}
	return client, release, nil
}

// Send performs one complete SMTP session. Every failure before the final
// end-of-data reply is safe to retry; a lost connection afterwards is not.
func (m *SMTPMailer) Send(ctx context.Context, message Message) error {
	client, release, err := m.connect(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := client.Mail(m.from.Address); err != nil {
		return classifySMTP(errorstack.CaptureContext(ctx, err), false)
	}
	if err := client.Rcpt(message.To); err != nil {
		return classifySMTP(errorstack.CaptureContext(ctx, err), false)
	}
	writer, err := client.Data()
	if err != nil {
		return classifySMTP(errorstack.CaptureContext(ctx, err), false)
	}
	if _, err := io.WriteString(writer, formatMessage(m.from, message, time.Now())); err != nil {
		_ = writer.Close()
		return classifySMTP(errorstack.CaptureContext(ctx, err), false)
	}
	// Close sends the terminating dot and waits for the acceptance reply.
	if err := writer.Close(); err != nil {
		return classifySMTP(errorstack.CaptureContext(ctx, err), true)
	}
	_ = client.Quit()
	return nil
}

// Check connects and signs in without sending anything, so Settings can show
// whether email would work. The result names the sender, never the server or
// its credentials.
func (m *SMTPMailer) Check(ctx context.Context) MailStatus {
	ctx, cancel := context.WithTimeout(ctx, smtpCheckTimeout)
	defer cancel()
	status := MailStatus{Configured: true, Sender: m.sender()}
	client, release, err := m.connect(ctx)
	if err != nil {
		status.Message = "The mail server could not be reached."
		if failure, ok := errors.AsType[*DeliveryError](err); ok {
			status.Message = failure.Summary
		}
		return status
	}
	defer release()
	_ = client.Quit()
	status.Usable = true
	status.Message = "The mail server is connected."
	if m.username != "" {
		status.Message += " Sign-in succeeded."
	}
	return status
}

// sender is the From address as Settings shows it, without the quoting that
// mail headers need.
func (m *SMTPMailer) sender() string {
	if m.from.Name == "" {
		return m.from.Address
	}
	return m.from.Name + " <" + m.from.Address + ">"
}

// formatMessage renders a plain-text UTF-8 email with CRLF line endings. The
// Message-ID uses the sender's domain so receivers see a fully qualified one.
func formatMessage(sender *mail.Address, message Message, now time.Time) string {
	domain := "memento"
	if at := strings.LastIndex(sender.Address, "@"); at >= 0 {
		domain = sender.Address[at+1:]
	}
	var b strings.Builder
	b.WriteString("From: " + sender.String() + "\r\n")
	b.WriteString("To: " + message.To + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", message.Subject) + "\r\n")
	b.WriteString("Date: " + now.UTC().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Message-ID: <" + models.NewUUIDv7().String() + "@" + domain + ">\r\n")
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	body := strings.ReplaceAll(strings.ReplaceAll(message.Body, "\r\n", "\n"), "\n", "\r\n")
	b.WriteString(body)
	if !strings.HasSuffix(body, "\r\n") {
		b.WriteString("\r\n")
	}
	return b.String()
}
