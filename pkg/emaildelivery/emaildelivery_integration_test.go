//go:build integration

package emaildelivery

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/outbox"
	"github.com/robinjoseph08/memento/pkg/smtp"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type scriptedSender struct {
	mu      sync.Mutex
	results []error
	calls   int
}

func (s *scriptedSender) Send(context.Context, smtp.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if len(s.results) == 0 {
		return nil
	}
	result := s.results[0]
	s.results = s.results[1:]
	return result
}

func (s *scriptedSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func deliveryConfig() config.SMTPConfig {
	return config.SMTPConfig{
		Enabled: true, Host: "smtp.example.com", Port: 465, Mode: "implicit_tls",
		FromAddress: "memento@example.com", TestRecipient: "operator@example.com",
		Timeout: time.Second, RetryBase: 10 * time.Millisecond, RetryMax: 20 * time.Millisecond, RetryWindow: time.Hour,
	}
}

func workerConfig() config.WorkerConfig {
	return config.WorkerConfig{
		PollInterval: 5 * time.Millisecond, HeartbeatInterval: 10 * time.Millisecond,
		HeartbeatMaxAge: time.Second, LeaseDuration: time.Second, DrainTimeout: time.Second,
		RetryBase: 5 * time.Millisecond, RetryMax: time.Second,
	}
}

func TestRequiredTestEmailAPICommitsOnlySafeResponseData(t *testing.T) {
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(context.Background(), db))
	service := New(db, deliveryConfig(), new(scriptedSender))
	e := echo.New()
	RegisterRoutes(e, NewHandler(service))
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/setup/email/test", nil)
	response := httptest.NewRecorder()

	e.ServeHTTP(response, request)

	assert.Equal(t, http.StatusAccepted, response.Code)
	assert.Contains(t, response.Body.String(), `"status":"queued"`)
	assert.NotContains(t, response.Body.String(), "operator@example.com")
	assert.NotContains(t, response.Body.String(), "durable PostgreSQL outbox")
}

func TestCompletedSetupCannotQueueTestEmail(t *testing.T) {
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(context.Background(), db))
	_, err := db.NewRaw(`UPDATE system_settings SET setup_complete = true WHERE id = 1`).Exec(context.Background())
	require.NoError(t, err)
	service := New(db, deliveryConfig(), new(scriptedSender))

	_, err = service.RequestTest(context.Background())

	require.ErrorIs(t, err, ErrSetupComplete)
	var deliveries int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM email_deliveries`).Scan(context.Background(), &deliveries))
	assert.Zero(t, deliveries)
}

func TestInvalidDeliveryIdentifiersAndPayloadsFailSafely(t *testing.T) {
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(context.Background(), db))
	service := New(db, deliveryConfig(), new(scriptedSender))

	_, err := service.Status(context.Background(), "token-or-email-code")
	require.ErrorIs(t, err, ErrDeliveryAbsent)
	err = service.Handle(context.Background(), worker.Job{Payload: []byte(`{"delivery_id":"private"}`)})
	assert.EqualError(t, err, "invalid_delivery_job")
}

func TestRequiredTestEmailIsCommittedBeforeWorkerDelivery(t *testing.T) {
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(context.Background(), db))
	server := newDeliverySMTPFixture(t, 250)
	smtpClient, err := smtp.New(server.config(), &tls.Config{RootCAs: server.roots})
	require.NoError(t, err)
	service := New(db, server.config(), smtpClient)

	response, err := service.RequestTest(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "queued", response.Status)
	assert.Zero(t, server.count(), "request path must not contact SMTP")
	var events, jobs int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM outbox_events WHERE delivered_at IS NULL`).Scan(context.Background(), &events))
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM jobs`).Scan(context.Background(), &jobs))
	assert.Equal(t, 1, events)
	assert.Zero(t, jobs)

	jobWorker, err := worker.New(db, workerConfig(), "restarted-process", map[string]worker.Handler{JobKind: service.Handle}, worker.WithDispatcher(outbox.New(db)))
	require.NoError(t, err)
	jobWorker.Start(context.Background())
	defer stopWorker(jobWorker)
	require.Eventually(t, func() bool {
		status, statusErr := service.Status(context.Background(), response.DeliveryID)
		return statusErr == nil && status.Status == "sent"
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, 1, server.count())
}

func TestTemporaryFailureRetriesWithBoundedBackoff(t *testing.T) {
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(context.Background(), db))
	sender := &scriptedSender{results: []error{errors.New("raw SMTP response with secret"), nil}}
	service := New(db, deliveryConfig(), sender)
	response, err := service.RequestTest(context.Background())
	require.NoError(t, err)
	jobWorker, err := worker.New(db, workerConfig(), "retry-worker", map[string]worker.Handler{JobKind: service.Handle}, worker.WithDispatcher(outbox.New(db)))
	require.NoError(t, err)
	jobWorker.Start(context.Background())
	defer stopWorker(jobWorker)

	require.Eventually(t, func() bool {
		status, statusErr := service.Status(context.Background(), response.DeliveryID)
		return statusErr == nil && status.Status == "sent"
	}, time.Second, 5*time.Millisecond)
	status, err := service.Status(context.Background(), response.DeliveryID)
	require.NoError(t, err)
	assert.Equal(t, 2, status.Attempts)
	assert.Equal(t, 2, sender.count())
	var jobAttempts int
	var safeError string
	require.NoError(t, db.NewRaw(`SELECT attempts, last_safe_error FROM jobs`).Scan(context.Background(), &jobAttempts, &safeError))
	assert.Equal(t, 1, jobAttempts)
	assert.Equal(t, "smtp_unavailable", safeError)
	assert.NotContains(t, safeError, "secret")
}

func TestExpiredLeaseRecoversWithoutRepeatingRecordedEffect(t *testing.T) {
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(context.Background(), db))
	server := newDeliverySMTPFixture(t, 250)
	smtpClient, err := smtp.New(server.config(), &tls.Config{RootCAs: server.roots})
	require.NoError(t, err)
	service := New(db, server.config(), smtpClient)
	_, err = service.RequestTest(context.Background())
	require.NoError(t, err)
	dispatcher := outbox.New(db)
	dispatched, err := dispatcher.Dispatch(context.Background(), "interrupted-dispatcher", time.Second)
	require.NoError(t, err)
	assert.True(t, dispatched)
	var job worker.Job
	require.NoError(t, db.NewRaw(`SELECT id, kind, payload, attempts FROM jobs`).Scan(context.Background(), &job.ID, &job.Kind, &job.Payload, &job.Attempts))
	require.NoError(t, service.Handle(context.Background(), job))
	assert.Equal(t, 1, server.count())
	_, err = db.NewRaw(`UPDATE jobs SET status = 'running', lease_owner = 'dead-process', lease_expires_at = now() - interval '1 second'`).Exec(context.Background())
	require.NoError(t, err)

	jobWorker, err := worker.New(db, workerConfig(), "recovery-worker", map[string]worker.Handler{JobKind: service.Handle})
	require.NoError(t, err)
	jobWorker.Start(context.Background())
	defer stopWorker(jobWorker)
	require.Eventually(t, func() bool {
		var status string
		err := db.NewRaw(`SELECT status FROM jobs`).Scan(context.Background(), &status)
		return err == nil && status == "completed"
	}, time.Second, 5*time.Millisecond)
	assert.Equal(t, 1, server.count(), "a recorded send must make lease recovery idempotent")
}

func TestRetryWindowExhaustionBecomesPermanentFailure(t *testing.T) {
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(context.Background(), db))
	service := New(db, deliveryConfig(), new(scriptedSender))
	response, err := service.RequestTest(context.Background())
	require.NoError(t, err)
	dispatched, err := outbox.New(db).Dispatch(context.Background(), "dispatcher", time.Second)
	require.NoError(t, err)
	assert.True(t, dispatched)
	_, err = db.NewRaw(`UPDATE email_deliveries SET created_at = now() - interval '2 hours' WHERE public_id = ?`, response.DeliveryID).Exec(context.Background())
	require.NoError(t, err)
	var job worker.Job
	require.NoError(t, db.NewRaw(`SELECT id, kind, payload, attempts FROM jobs`).Scan(context.Background(), &job.ID, &job.Kind, &job.Payload, &job.Attempts))

	err = service.Handle(context.Background(), job)

	assert.EqualError(t, err, "retry_window_exhausted")
	status, statusErr := service.Status(context.Background(), response.DeliveryID)
	require.NoError(t, statusErr)
	assert.Equal(t, "failed", status.Status)
	require.NotNil(t, status.Failure)
	assert.Equal(t, "retry_window_exhausted", *status.Failure)
}

func TestPermanentRecipientRejectionCreatesSafeOperatorVisibleFailure(t *testing.T) {
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(context.Background(), db))
	server := newDeliverySMTPFixture(t, 550)
	smtpClient, err := smtp.New(server.config(), &tls.Config{RootCAs: server.roots})
	require.NoError(t, err)
	service := New(db, server.config(), smtpClient)
	response, err := service.RequestTest(context.Background())
	require.NoError(t, err)
	jobWorker, err := worker.New(db, workerConfig(), "failure-worker", map[string]worker.Handler{JobKind: service.Handle}, worker.WithDispatcher(outbox.New(db)))
	require.NoError(t, err)
	jobWorker.Start(context.Background())
	defer stopWorker(jobWorker)

	require.Eventually(t, func() bool {
		status, statusErr := service.Status(context.Background(), response.DeliveryID)
		return statusErr == nil && status.Status == "failed"
	}, time.Second, 5*time.Millisecond)
	status, err := service.Status(context.Background(), response.DeliveryID)
	require.NoError(t, err)
	require.NotNil(t, status.Failure)
	assert.Equal(t, "recipient_rejected", *status.Failure)
	var diagnostic string
	require.NoError(t, db.NewRaw(`SELECT diagnostic FROM delivery_problems`).Scan(context.Background(), &diagnostic))
	assert.Equal(t, "recipient_rejected", diagnostic)
	var jobStatus string
	require.NoError(t, db.NewRaw(`SELECT status FROM jobs`).Scan(context.Background(), &jobStatus))
	assert.Equal(t, "failed", jobStatus)
}

func TestOutboxLeaseIsReclaimableAfterInterruptedDispatch(t *testing.T) {
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(context.Background(), db))
	service := New(db, deliveryConfig(), new(scriptedSender))
	_, err := service.RequestTest(context.Background())
	require.NoError(t, err)
	_, err = db.NewRaw(`UPDATE outbox_events SET lease_owner = 'dead-process', lease_expires_at = now() - interval '1 second'`).Exec(context.Background())
	require.NoError(t, err)

	dispatched, err := outbox.New(db).Dispatch(context.Background(), "new-process", time.Second)
	require.NoError(t, err)
	assert.True(t, dispatched)
	var jobs int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM jobs`).Scan(context.Background(), &jobs))
	assert.Equal(t, 1, jobs)
}

type deliverySMTPFixture struct {
	listener net.Listener
	address  string
	roots    *x509.CertPool
	rcptCode int
	mu       sync.Mutex
	messages int
}

func newDeliverySMTPFixture(t *testing.T, rcptCode int) *deliverySMTPFixture {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	certificate, leaf := deliveryTestCertificate(t)
	fixture := &deliverySMTPFixture{
		listener: tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}),
		address:  listener.Addr().String(), roots: x509.NewCertPool(), rcptCode: rcptCode,
	}
	fixture.roots.AddCert(leaf)
	go fixture.serve()
	t.Cleanup(func() { _ = fixture.listener.Close() })
	return fixture
}

func (s *deliverySMTPFixture) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *deliverySMTPFixture) handle(conn net.Conn) {
	defer conn.Close()
	reader := textproto.NewReader(bufio.NewReader(conn))
	writer := bufio.NewWriter(conn)
	write := func(value string) { _, _ = writer.WriteString(value + "\r\n"); _ = writer.Flush() }
	write("220 local test SMTP")
	for {
		line, err := reader.ReadLine()
		if err != nil {
			return
		}
		switch {
		case len(line) >= 4 && (line[:4] == "EHLO" || line[:4] == "HELO"):
			write("250 local")
		case len(line) >= 4 && line[:4] == "MAIL":
			write("250 sender accepted")
		case len(line) >= 4 && line[:4] == "RCPT":
			if s.rcptCode == 550 {
				write("550 raw permanent private response")
			} else {
				write("250 recipient accepted")
			}
		case line == "DATA":
			write("354 continue")
			if _, err := reader.ReadDotBytes(); err != nil {
				return
			}
			s.mu.Lock()
			s.messages++
			s.mu.Unlock()
			write("250 queued")
		case line == "QUIT":
			write("221 bye")
			return
		default:
			write("500 unsupported")
		}
	}
}

func (s *deliverySMTPFixture) config() config.SMTPConfig {
	host, port, _ := net.SplitHostPort(s.address)
	portNumber, _ := strconv.Atoi(port)
	cfg := deliveryConfig()
	cfg.Host = host
	cfg.Port = portNumber
	return cfg
}

func (s *deliverySMTPFixture) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.messages
}

func deliveryTestCertificate(t *testing.T) (tls.Certificate, *x509.Certificate) {
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

func stopWorker(jobWorker *worker.Worker) {
	jobWorker.StopClaims()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = jobWorker.Drain(ctx)
}
