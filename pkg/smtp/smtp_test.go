package smtp

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/textproto"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type smtpFixture struct {
	listener      net.Listener
	address       string
	roots         *x509.CertPool
	rcptCode      int
	dropAfterData bool
	startTLS      *tls.Config
	mu            sync.Mutex
	messages      [][]byte
}

func newSMTPFixture(t *testing.T, secure bool, rcptCode int) *smtpFixture {
	t.Helper()
	mode := "insecure"
	if secure {
		mode = "implicit_tls"
	}
	return newSMTPFixtureMode(t, mode, rcptCode)
}

func newSMTPFixtureMode(t *testing.T, mode string, rcptCode int) *smtpFixture {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	fixture := &smtpFixture{listener: listener, address: listener.Addr().String(), rcptCode: rcptCode}
	if mode == "implicit_tls" || mode == "starttls" {
		certificate, leaf := testCertificate(t)
		fixture.roots = x509.NewCertPool()
		fixture.roots.AddCert(leaf)
		serverTLS := &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
		if mode == "implicit_tls" {
			fixture.listener = tls.NewListener(listener, serverTLS)
		} else {
			fixture.startTLS = serverTLS
		}
	}
	go fixture.serve()
	t.Cleanup(func() { _ = fixture.listener.Close() })
	return fixture
}

func (s *smtpFixture) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *smtpFixture) handle(conn net.Conn) {
	defer conn.Close()
	reader := textproto.NewReader(bufio.NewReader(conn))
	writer := bufio.NewWriter(conn)
	write := func(value string) { _, _ = writer.WriteString(value + "\r\n"); _ = writer.Flush() }
	write("220 local test SMTP")
	upgraded := false
	for {
		line, err := reader.ReadLine()
		if err != nil {
			return
		}
		switch {
		case len(line) >= 4 && (line[:4] == "EHLO" || line[:4] == "HELO"):
			if s.startTLS != nil && !upgraded {
				_, _ = writer.WriteString("250-local\r\n250 STARTTLS\r\n")
				_ = writer.Flush()
			} else {
				write("250 local")
			}
		case line == "STARTTLS" && s.startTLS != nil && !upgraded:
			write("220 begin TLS")
			secure := tls.Server(conn, s.startTLS)
			if err := secure.HandshakeContext(context.Background()); err != nil {
				return
			}
			conn = secure
			reader = textproto.NewReader(bufio.NewReader(conn))
			writer = bufio.NewWriter(conn)
			upgraded = true
		case len(line) >= 4 && line[:4] == "MAIL":
			write("250 sender accepted")
		case len(line) >= 4 && line[:4] == "RCPT":
			switch s.rcptCode {
			case 451:
				write("451 raw temporary private response")
			case 550:
				write("550 raw permanent private response")
			default:
				write("250 recipient accepted")
			}
		case line == "DATA":
			write("354 continue")
			message, err := reader.ReadDotBytes()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.messages = append(s.messages, message)
			s.mu.Unlock()
			write("250 queued")
			if s.dropAfterData {
				return
			}
		case line == "QUIT":
			write("221 bye")
			return
		default:
			write("500 unsupported")
		}
	}
}

func (s *smtpFixture) config(mode string) config.SMTPConfig {
	host, port, _ := net.SplitHostPort(s.address)
	portNumber, _ := strconv.Atoi(port)
	return config.SMTPConfig{
		Enabled: true, Host: host, Port: portNumber, Mode: mode,
		FromAddress: "memento@example.com", TestRecipient: "operator@example.com",
		Timeout: time.Second, RetryBase: time.Millisecond, RetryMax: time.Second, RetryWindow: time.Hour,
		InsecureDevelopment: mode == "insecure",
	}
}

func (s *smtpFixture) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.messages)
}

func TestImplicitTLSSendsWithCertificateVerification(t *testing.T) {
	server := newSMTPFixture(t, true, 250)
	client, err := New(server.config("implicit_tls"), &tls.Config{RootCAs: server.roots})
	require.NoError(t, err)

	err = client.Send(context.Background(), Message{ID: "delivery-1", To: "operator@example.com", Subject: "Test", Body: "private body"})

	require.NoError(t, err)
	assert.Equal(t, 1, server.count())
	assert.Equal(t, StatusOK, client.Status())
}

func TestSTARTTLSSendsWithCertificateVerification(t *testing.T) {
	server := newSMTPFixtureMode(t, "starttls", 250)
	client, err := New(server.config("starttls"), &tls.Config{RootCAs: server.roots})
	require.NoError(t, err)

	err = client.Send(context.Background(), Message{ID: "delivery-starttls", To: "operator@example.com", Subject: "Test", Body: "private body"})

	require.NoError(t, err)
	assert.Equal(t, 1, server.count())
}

func TestAcceptedMessageIsNotRetriedWhenQUITIsInterrupted(t *testing.T) {
	server := newSMTPFixture(t, true, 250)
	server.dropAfterData = true
	client, err := New(server.config("implicit_tls"), &tls.Config{RootCAs: server.roots})
	require.NoError(t, err)

	err = client.Send(context.Background(), Message{ID: "delivery-accepted", To: "operator@example.com", Subject: "Test", Body: "private body"})

	require.NoError(t, err)
	assert.Equal(t, 1, server.count())
}

func TestImplicitTLSRejectsUntrustedCertificate(t *testing.T) {
	server := newSMTPFixture(t, true, 250)
	client, err := New(server.config("implicit_tls"), &tls.Config{RootCAs: x509.NewCertPool()})
	require.NoError(t, err)

	err = client.Send(context.Background(), Message{ID: "delivery-2", To: "operator@example.com", Subject: "Test", Body: "private body"})

	var failure *DeliveryError
	require.ErrorAs(t, err, &failure)
	assert.Equal(t, "tls_verification_failed", failure.Diagnostic)
	assert.False(t, failure.Temporary)
	assert.Equal(t, 0, server.count())
}

func TestSTARTTLSCannotDowngradeToPlaintext(t *testing.T) {
	server := newSMTPFixture(t, false, 250)
	client, err := New(server.config("starttls"), nil)
	require.NoError(t, err)

	err = client.Send(context.Background(), Message{ID: "delivery-3", To: "operator@example.com", Subject: "Test", Body: "private body"})

	var failure *DeliveryError
	require.ErrorAs(t, err, &failure)
	assert.Equal(t, "tls_required", failure.Diagnostic)
	assert.Equal(t, 0, server.count())
}

func TestSMTPResponsesAreReducedToSafeFailureCodes(t *testing.T) {
	for _, test := range []struct {
		name      string
		code      int
		want      string
		temporary bool
	}{
		{"temporary", 451, "smtp_unavailable", true},
		{"permanent recipient rejection", 550, "recipient_rejected", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newSMTPFixture(t, true, test.code)
			client, err := New(server.config("implicit_tls"), &tls.Config{RootCAs: server.roots})
			require.NoError(t, err)
			err = client.Send(context.Background(), Message{ID: "delivery-4", To: "operator@example.com", Subject: "Test", Body: "private body"})
			var failure *DeliveryError
			require.ErrorAs(t, err, &failure)
			assert.Equal(t, test.want, failure.Diagnostic)
			assert.Equal(t, test.temporary, failure.Temporary)
			assert.NotContains(t, err.Error(), "private response")
		})
	}
}

func testCertificate(t *testing.T) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, IsCA: true,
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
	)
	require.NoError(t, err)
	leaf, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return certificate, leaf
}
