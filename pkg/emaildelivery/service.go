// Package emaildelivery owns durable required-email requests and delivery status.
package emaildelivery

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/smtp"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/uptrace/bun"
)

const (
	JobKind       = "send_required_email"
	KindSetupCode = "setup_code"
)

var (
	errGenerateIdentity  = errors.New("generate email delivery identity")
	errDeliveryLeaseLost = errors.New("email delivery job lease lost")
	errUnsupportedKind   = errors.New("unsupported required email kind")
	errSensitiveBodyKey  = errors.New("sensitive email encryption key is unavailable")
	errSensitiveBody     = errors.New("sensitive email body is invalid")
	ErrNotConfigured     = errors.New("SMTP is not configured")
	ErrSetupComplete     = errors.New("test email is available only during setup")
	ErrDeliveryAbsent    = errors.New("email delivery not found")
)

// TestEmailResponse is generated to TypeScript by Tygo.
type TestEmailResponse struct {
	DeliveryID string `json:"delivery_id"`
	Status     string `json:"status"`
}

// StatusResponse is generated to TypeScript by Tygo.
type StatusResponse struct {
	DeliveryID string  `json:"delivery_id"`
	Status     string  `json:"status"`
	Attempts   int     `json:"attempts"`
	Failure    *string `json:"failure,omitempty"`
}

// RequiredMessage is committed by a domain transaction before worker delivery.
type RequiredMessage struct {
	Kind          string
	Recipient     string
	Subject       string
	Body          string
	DeliverBefore *time.Time
}

type jobPayload struct {
	DeliveryID int64 `json:"delivery_id"`
}

type delivery struct {
	ID            int64
	PublicID      string
	Kind          string
	Recipient     string
	Subject       string
	Body          string
	Status        string
	Attempts      int
	LastSafeError *string
	NextRetryAt   *time.Time
	DeliverBefore *time.Time
	CreatedAt     time.Time
}

// Service creates required test requests and handles their leased jobs.
type Service struct {
	db       *bun.DB
	cfg      config.SMTPConfig
	sender   smtp.Sender
	bodyAEAD cipher.AEAD
}

func New(db *bun.DB, cfg config.SMTPConfig, sender smtp.Sender, securitySecret ...string) *Service {
	service := &Service{db: db, cfg: cfg, sender: sender}
	if len(securitySecret) != 0 && len(securitySecret[0]) >= 32 {
		key := sha256.Sum256(append([]byte("memento:required-email:"), []byte(securitySecret[0])...))
		block, err := aes.NewCipher(key[:])
		if err == nil {
			service.bodyAEAD, _ = cipher.NewGCM(block)
		}
	}
	return service
}

// RequestTest atomically commits the delivery and its outbox event.
func (s *Service) RequestTest(ctx context.Context) (TestEmailResponse, error) {
	if !s.cfg.Enabled || s.sender == nil {
		return TestEmailResponse{}, ErrNotConfigured
	}
	var publicID string
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var setupComplete bool
		if err := tx.NewRaw(`SELECT setup_complete FROM system_settings WHERE id = 1 FOR SHARE`).Scan(ctx, &setupComplete); err != nil {
			return err
		}
		if setupComplete {
			return ErrSetupComplete
		}
		_, queuedID, err := s.QueueRequired(ctx, tx, RequiredMessage{
			Kind:      "required_test",
			Recipient: s.cfg.TestRecipient,
			Subject:   "Memento email delivery test",
			Body:      "Memento delivered this required test email through the durable PostgreSQL outbox.",
		})
		publicID = queuedID
		return err
	})
	if err != nil {
		return TestEmailResponse{}, fmt.Errorf("request required test email: %w", err)
	}
	return TestEmailResponse{DeliveryID: publicID, Status: "queued"}, nil
}

// QueueRequired adds required email and outbox records to the caller's transaction.
func (s *Service) QueueRequired(ctx context.Context, tx bun.Tx, message RequiredMessage) (int64, string, error) {
	if !s.cfg.Enabled || s.sender == nil {
		return 0, "", ErrNotConfigured
	}
	if message.Kind != "required_test" && message.Kind != KindSetupCode {
		return 0, "", fmt.Errorf("%w: %s", errUnsupportedKind, message.Kind)
	}
	if (message.Kind == KindSetupCode) != (message.DeliverBefore != nil) {
		return 0, "", errSensitiveBody
	}
	publicID, err := randomID()
	if err != nil {
		return 0, "", errGenerateIdentity
	}
	body, err := s.persistedBody(message)
	if err != nil {
		return 0, "", err
	}
	var id int64
	if err := tx.NewRaw(`
		INSERT INTO email_deliveries (public_id, kind, recipient, subject, body, deliver_before)
		VALUES (?, ?, ?, ?, ?, ?)
		RETURNING id
	`, publicID, message.Kind, message.Recipient, message.Subject, body, message.DeliverBefore).Scan(ctx, &id); err != nil {
		return 0, "", err
	}
	payload, err := json.Marshal(jobPayload{DeliveryID: id})
	if err != nil {
		return 0, "", err
	}
	if _, err := tx.NewRaw(`
		INSERT INTO outbox_events (kind, aggregate_kind, aggregate_id, aggregate_version, payload)
		VALUES (?, 'email_delivery', ?, 1, ?::jsonb)
	`, JobKind, publicID, string(payload)).Exec(ctx); err != nil {
		return 0, "", err
	}
	return id, publicID, nil
}

// Status returns only allowlisted operator-visible delivery details.
func (s *Service) Status(ctx context.Context, deliveryID string) (StatusResponse, error) {
	decoded, err := hex.DecodeString(deliveryID)
	if err != nil || len(decoded) != 16 {
		return StatusResponse{}, ErrDeliveryAbsent
	}
	var result StatusResponse
	result.DeliveryID = deliveryID
	err = s.db.NewRaw(`
		SELECT delivery.status, delivery.attempts, delivery.last_safe_error
		FROM email_deliveries AS delivery
		JOIN system_settings AS settings ON settings.id = 1 AND settings.setup_complete = false
		WHERE delivery.public_id = ? AND delivery.kind = 'required_test'
	`, deliveryID).Scan(ctx, &result.Status, &result.Attempts, &result.Failure)
	if errors.Is(err, sql.ErrNoRows) {
		return StatusResponse{}, ErrDeliveryAbsent
	}
	if err != nil {
		return StatusResponse{}, fmt.Errorf("read test email status: %w", err)
	}
	return result, nil
}

// Handle sends one required email. Persisted terminal state makes retries idempotent.
func (s *Service) Handle(ctx context.Context, job worker.Job) error {
	var payload jobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.DeliveryID <= 0 {
		return worker.Permanent("invalid_delivery_job")
	}
	var message delivery
	err := s.db.NewRaw(`
		SELECT id, public_id, kind, recipient, subject, body, status, attempts, last_safe_error,
		       next_retry_at, deliver_before, created_at
		FROM email_deliveries WHERE id = ?
	`, payload.DeliveryID).Scan(
		ctx, &message.ID, &message.PublicID, &message.Kind, &message.Recipient, &message.Subject,
		&message.Body, &message.Status, &message.Attempts, &message.LastSafeError,
		&message.NextRetryAt, &message.DeliverBefore, &message.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return worker.Permanent("delivery_missing")
	}
	if err != nil {
		return err
	}
	if message.Status == "sent" || message.Status == "failed" {
		return nil
	}
	if err := s.requireLease(ctx, job); err != nil {
		return err
	}
	if message.Kind == KindSetupCode {
		var obsolete bool
		err := s.db.NewRaw(`
			SELECT setup_complete OR now() >= ?
			FROM system_settings WHERE id = 1
		`, message.DeliverBefore).Scan(ctx, &obsolete)
		if err != nil {
			return err
		}
		if obsolete {
			if err := s.recordFailure(ctx, job, message.ID, "delivery_expired"); err != nil {
				return err
			}
			return worker.Permanent("delivery_expired")
		}
	}
	message.Body, err = s.deliveryBody(message.Kind, message.Body)
	if err != nil {
		if recordErr := s.recordFailure(ctx, job, message.ID, "delivery_payload_invalid"); recordErr != nil {
			return recordErr
		}
		return worker.Permanent("delivery_payload_invalid")
	}
	if time.Since(message.CreatedAt) >= s.cfg.RetryWindow {
		if err := s.recordFailure(ctx, job, message.ID, "retry_window_exhausted"); err != nil {
			return err
		}
		return worker.Permanent("retry_window_exhausted")
	}
	if message.NextRetryAt != nil && time.Until(*message.NextRetryAt) > 0 {
		diagnostic := "smtp_unavailable"
		if message.LastSafeError != nil {
			diagnostic = *message.LastSafeError
		}
		return worker.RetryAfter(time.Until(*message.NextRetryAt), diagnostic)
	}
	updated, err := s.db.NewRaw(`
		UPDATE email_deliveries SET attempts = attempts + 1, updated_at = now()
		WHERE id = ? AND status = 'queued' AND EXISTS (
			SELECT 1 FROM jobs WHERE id = ? AND status = 'running' AND lease_owner = ? AND lease_expires_at > now()
		)
	`, message.ID, job.ID, job.LeaseOwner).Exec(ctx)
	if err != nil {
		return err
	}
	if affected, _ := updated.RowsAffected(); affected != 1 {
		return errDeliveryLeaseLost
	}
	err = s.sender.Send(ctx, smtp.Message{
		ID: message.PublicID, To: message.Recipient, Subject: message.Subject, Body: message.Body,
	})
	if err == nil {
		updated, updateErr := s.db.NewRaw(`
			UPDATE email_deliveries SET status = 'sent', sent_at = now(), next_retry_at = NULL,
				last_safe_error = NULL, body = CASE WHEN kind = 'setup_code' THEN '' ELSE body END, updated_at = now()
			WHERE id = ? AND status = 'queued' AND EXISTS (
				SELECT 1 FROM jobs WHERE id = ? AND status = 'running' AND lease_owner = ? AND lease_expires_at > now()
			)
		`, message.ID, job.ID, job.LeaseOwner).Exec(ctx)
		if updateErr != nil {
			return updateErr
		}
		if affected, _ := updated.RowsAffected(); affected != 1 {
			return errDeliveryLeaseLost
		}
		return nil
	}
	failure := &smtp.DeliveryError{Diagnostic: "smtp_unavailable", Temporary: true}
	if !errors.As(err, &failure) {
		failure = &smtp.DeliveryError{Diagnostic: "smtp_unavailable", Temporary: true}
	}
	if !failure.Temporary {
		if err := s.recordFailure(ctx, job, message.ID, failure.Diagnostic); err != nil {
			return err
		}
		return worker.Permanent(failure.Diagnostic)
	}
	delay := s.retryDelay(message.Attempts)
	remaining := s.cfg.RetryWindow - time.Since(message.CreatedAt)
	if remaining <= 0 {
		if err := s.recordFailure(ctx, job, message.ID, "retry_window_exhausted"); err != nil {
			return err
		}
		return worker.Permanent("retry_window_exhausted")
	}
	if delay > remaining {
		delay = remaining
	}
	updated, updateErr := s.db.NewRaw(`
		UPDATE email_deliveries SET last_safe_error = ?, next_retry_at = now() + (? * interval '1 microsecond'), updated_at = now()
		WHERE id = ? AND status = 'queued' AND EXISTS (
			SELECT 1 FROM jobs WHERE id = ? AND status = 'running' AND lease_owner = ? AND lease_expires_at > now()
		)
	`, failure.Diagnostic, delay.Microseconds(), message.ID, job.ID, job.LeaseOwner).Exec(ctx)
	if updateErr != nil {
		return updateErr
	}
	if affected, _ := updated.RowsAffected(); affected != 1 {
		return errDeliveryLeaseLost
	}
	return worker.RetryAfter(delay, failure.Diagnostic)
}

func (s *Service) persistedBody(message RequiredMessage) (string, error) {
	if message.Kind != KindSetupCode {
		return message.Body, nil
	}
	if s.bodyAEAD == nil {
		return "", errSensitiveBodyKey
	}
	nonce := make([]byte, s.bodyAEAD.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", errGenerateIdentity
	}
	sealed := s.bodyAEAD.Seal(nonce, nonce, []byte(message.Body), []byte(message.Kind))
	return "v1:" + base64.RawStdEncoding.EncodeToString(sealed), nil
}

func (s *Service) deliveryBody(kind, body string) (string, error) {
	if kind != KindSetupCode {
		return body, nil
	}
	if s.bodyAEAD == nil || len(body) < 4 || body[:3] != "v1:" {
		return "", errSensitiveBody
	}
	sealed, err := base64.RawStdEncoding.DecodeString(body[3:])
	if err != nil || len(sealed) < s.bodyAEAD.NonceSize() {
		return "", errSensitiveBody
	}
	nonce := sealed[:s.bodyAEAD.NonceSize()]
	plaintext, err := s.bodyAEAD.Open(nil, nonce, sealed[s.bodyAEAD.NonceSize():], []byte(kind))
	if err != nil {
		return "", errSensitiveBody
	}
	return string(plaintext), nil
}

func randomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func (s *Service) retryDelay(attempts int) time.Duration {
	delay := s.cfg.RetryBase
	for range attempts {
		if delay >= s.cfg.RetryMax/2 {
			delay = s.cfg.RetryMax
			break
		}
		delay *= 2
	}
	if delay > s.cfg.RetryMax {
		delay = s.cfg.RetryMax
	}
	spread := delay / 5
	if spread == 0 {
		return delay
	}
	value, err := rand.Int(rand.Reader, big.NewInt(int64(2*spread)+1))
	if err != nil {
		return delay
	}
	result := delay - spread + time.Duration(value.Int64())
	if result > s.cfg.RetryMax {
		return s.cfg.RetryMax
	}
	return result
}

func (s *Service) requireLease(ctx context.Context, job worker.Job) error {
	var owned bool
	err := s.db.NewRaw(`
		SELECT EXISTS (
			SELECT 1 FROM jobs WHERE id = ? AND status = 'running' AND lease_owner = ? AND lease_expires_at > now()
		)
	`, job.ID, job.LeaseOwner).Scan(ctx, &owned)
	if err != nil {
		return err
	}
	if !owned {
		return errDeliveryLeaseLost
	}
	return nil
}

func (s *Service) recordFailure(ctx context.Context, job worker.Job, id int64, diagnostic string) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		updated, err := tx.NewRaw(`
			UPDATE email_deliveries SET status = 'failed', failed_at = now(), last_safe_error = ?,
				next_retry_at = NULL, body = CASE WHEN kind = 'setup_code' THEN '' ELSE body END, updated_at = now()
			WHERE id = ? AND status = 'queued' AND EXISTS (
				SELECT 1 FROM jobs WHERE id = ? AND status = 'running' AND lease_owner = ? AND lease_expires_at > now()
			)
		`, diagnostic, id, job.ID, job.LeaseOwner).Exec(ctx)
		if err != nil {
			return err
		}
		if affected, _ := updated.RowsAffected(); affected != 1 {
			return errDeliveryLeaseLost
		}
		_, err = tx.NewRaw(`
			INSERT INTO delivery_problems (email_delivery_id, diagnostic)
			VALUES (?, ?) ON CONFLICT (email_delivery_id) DO NOTHING
		`, id, diagnostic).Exec(ctx)
		return err
	})
}
