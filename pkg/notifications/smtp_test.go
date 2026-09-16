package notifications_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/robinjoseph08/memento/pkg/notifications/smtptest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSMTPMailerConfiguration(t *testing.T) {
	t.Parallel()
	for name, scenario := range map[string]struct {
		url, from string
		wantErr   string
	}{
		"plain":            {url: "smtp://mail.example.test:587", from: "Memento <memento@example.test>"},
		"implicit tls":     {url: "smtps://user:secret@mail.example.test", from: "memento@example.test"},
		"wrong scheme":     {url: "http://mail.example.test", from: "memento@example.test", wantErr: "smtp_url: must be smtp://"},
		"missing host":     {url: "smtp://", from: "memento@example.test", wantErr: "smtp_url"},
		"path":             {url: "smtp://mail.example.test/relay", from: "memento@example.test", wantErr: "smtp_url: must not contain a path"},
		"bad port":         {url: "smtp://mail.example.test:99999", from: "memento@example.test", wantErr: "smtp_url: port"},
		"invalid sender":   {url: "smtp://mail.example.test", from: "not an address", wantErr: "smtp_from"},
		"secret not shown": {url: "smtp://user:supersecret@mail.example.test/x", from: "memento@example.test", wantErr: "smtp_url"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := notifications.NewSMTPMailer(scenario.url, scenario.from)
			if scenario.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), scenario.wantErr)
			assert.NotContains(t, err.Error(), "supersecret")
		})
	}
}

func TestSMTPMailerCheckConnectsWithoutSending(t *testing.T) {
	t.Parallel()
	server, err := smtptest.Start(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = server.Close() })
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	unreachable := "smtp://" + closed.Addr().String()
	require.NoError(t, closed.Close())
	// Parallel subtests run after this function returns, so the check that
	// nothing was sent belongs in a cleanup.
	t.Cleanup(func() { assert.Empty(t, server.Messages(), "a check never sends") })
	for name, scenario := range map[string]struct {
		url, from, sender, message string
		usable                     bool
	}{
		"plain relay":         {url: server.URL(), from: "Memento <memento@example.test>", sender: "Memento <memento@example.test>", usable: true, message: "The mail server is connected."},
		"bare address":        {url: server.URL(), from: "memento@example.test", sender: "memento@example.test", usable: true, message: "The mail server is connected."},
		"sign-in without TLS": {url: "smtp://user:supersecret@" + server.Address(), from: "memento@example.test", sender: "memento@example.test", message: "The mail server replied 502."},
		"unreachable":         {url: unreachable, from: "memento@example.test", sender: "memento@example.test", message: "could not be reached"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			mailer, err := notifications.NewSMTPMailer(scenario.url, scenario.from)
			require.NoError(t, err)
			status := mailer.Check(t.Context())
			assert.True(t, status.Configured)
			assert.Equal(t, scenario.usable, status.Usable)
			assert.Equal(t, scenario.sender, status.Sender)
			assert.Contains(t, status.Message, scenario.message)
			assert.NotContains(t, status.Message, "supersecret")
			assert.NotContains(t, status.Message, "127.0.0.1")
		})
	}
}

func TestCheckMailReportsMissingSMTPQuietly(t *testing.T) {
	t.Parallel()
	status := notifications.New(nil, nil, nil, nil, nil).CheckMail(t.Context())
	assert.Equal(t, notifications.MailStatus{Message: "Email is not configured for this installation, so Invitations and update emails cannot be sent."}, status)
	recorded := notifications.New(nil, &notifications.Recorder{}, nil, nil, nil).CheckMail(t.Context())
	assert.True(t, recorded.Configured)
	assert.True(t, recorded.Usable)
}

func TestSMTPMailerContractAgainstLocalServer(t *testing.T) {
	t.Parallel()
	server, err := smtptest.Start(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = server.Close() })
	mailer, err := notifications.NewSMTPMailer(server.URL(), "Memento <memento@example.test>")
	require.NoError(t, err)
	message := notifications.Message{Kind: "invitation", To: "alex@example.test", Subject: "You're invited to memento ✉", Body: "Hello Alex,\n\n.Sign in at https://memento.example.test/sign-in\n"}

	require.NoError(t, mailer.Send(t.Context(), message))
	messages := server.Messages()
	require.Len(t, messages, 1)
	assert.Equal(t, "memento@example.test", messages[0].From)
	assert.Equal(t, "alex@example.test", messages[0].To)
	assert.Contains(t, messages[0].Data, "Subject: =?utf-8?q?You're_invited_to_memento_=E2=9C=89?=")
	assert.Contains(t, messages[0].Data, "\r\n.Sign in at https://memento.example.test/sign-in\r\n")
	assert.Contains(t, messages[0].Data, "Content-Type: text/plain; charset=utf-8")

	var failure *notifications.DeliveryError
	server.SetMode(smtptest.ModeTransient)
	err = mailer.Send(t.Context(), message)
	require.ErrorAs(t, err, &failure)
	assert.Equal(t, notifications.OutcomeTransient, failure.Outcome)
	assert.Equal(t, "The mail server replied 451.", failure.Summary)

	server.SetMode(smtptest.ModePermanent)
	err = mailer.Send(t.Context(), message)
	require.ErrorAs(t, err, &failure)
	assert.Equal(t, notifications.OutcomePermanent, failure.Outcome)
	assert.Equal(t, "The mail server replied 550.", failure.Summary)
	assert.Len(t, server.Messages(), 1, "rejected messages are not recorded")

	// The server records the message but never replies; cancelling the send is uncertain.
	server.SetMode(smtptest.ModeHold)
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		select {
		case <-server.Holds():
		case <-t.Context().Done():
		}
		cancel()
	}()
	err = mailer.Send(ctx, message)
	require.ErrorAs(t, err, &failure)
	assert.Equal(t, notifications.OutcomeUncertain, failure.Outcome)
	assert.Contains(t, failure.Summary, "may have accepted")
	server.SetMode(smtptest.ModeAccept)
	assert.Len(t, server.Messages(), 2)

	// An unavailable server is a safe transient failure.
	unavailable, err := notifications.NewSMTPMailer("smtp://127.0.0.1:1", "memento@example.test")
	require.NoError(t, err)
	err = unavailable.Send(t.Context(), message)
	require.ErrorAs(t, err, &failure)
	assert.Equal(t, notifications.OutcomeTransient, failure.Outcome)
	assert.Equal(t, "The mail server could not be reached.", failure.Summary)
	var netErr net.Error
	assert.True(t, errors.As(err, &netErr) || strings.Contains(err.Error(), "could not be reached"))
}

func TestSMTPMailerRefusesCredentialsOverPlaintextPermanently(t *testing.T) {
	t.Parallel()
	server, err := smtptest.Start(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = server.Close() })
	// A non-localhost host with credentials over a plain connection is a
	// configuration mistake, not a transient outage.
	mailer, err := notifications.NewSMTPMailer("smtp://user:secret@mail.example.test:25", "memento@example.test")
	require.NoError(t, err)
	notifications.SetSMTPDialForTest(mailer, server.Address())
	var failure *notifications.DeliveryError
	err = mailer.Send(t.Context(), notifications.Message{Kind: "invitation", To: "alex@example.test", Subject: "Hi", Body: "Hello"})
	require.ErrorAs(t, err, &failure)
	assert.Equal(t, notifications.OutcomePermanent, failure.Outcome)
	assert.Contains(t, failure.Summary, "did not accept sign-in")
	assert.NotContains(t, err.Error(), "secret")
	assert.Empty(t, server.Messages())
}
