// Package emaildelivery owns durable required-email requests and delivery status.
package emaildelivery

import (
	"context"
	"crypto/rand"
	"database/sql"
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

const JobKind = "send_required_email"

var (
	errGenerateIdentity = errors.New("generate email delivery identity")
	ErrNotConfigured    = errors.New("SMTP is not configured")
	ErrSetupComplete    = errors.New("test email is available only during setup")
	ErrDeliveryAbsent   = errors.New("email delivery not found")
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

type jobPayload struct {
	DeliveryID int64 `json:"delivery_id"`
}

type delivery struct {
	ID            int64
	PublicID      string
	Recipient     string
	Subject       string
	Body          string
	Status        string
	Attempts      int
	LastSafeError *string
	CreatedAt     time.Time
}

// Service creates required test requests and handles their leased jobs.
type Service struct {
	db     *bun.DB
	cfg    config.SMTPConfig
	sender smtp.Sender
}

func New(db *bun.DB, cfg config.SMTPConfig, sender smtp.Sender) *Service {
	return &Service{db: db, cfg: cfg, sender: sender}
}

// RequestTest atomically commits the delivery and its outbox event.
func (s *Service) RequestTest(ctx context.Context) (TestEmailResponse, error) {
	if !s.cfg.Enabled || s.sender == nil {
		return TestEmailResponse{}, ErrNotConfigured
	}
	publicID, err := randomID()
	if err != nil {
		return TestEmailResponse{}, errGenerateIdentity
	}
	var id int64
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var setupComplete bool
		if err := tx.NewRaw(`SELECT setup_complete FROM system_settings WHERE id = 1 FOR SHARE`).Scan(ctx, &setupComplete); err != nil {
			return err
		}
		if setupComplete {
			return ErrSetupComplete
		}
		if err := tx.NewRaw(`
			INSERT INTO email_deliveries (public_id, kind, recipient, subject, body)
			VALUES (?, 'required_test', ?, 'Memento email delivery test', ?)
			RETURNING id
		`, publicID, s.cfg.TestRecipient, "Memento delivered this required test email through the durable PostgreSQL outbox.").Scan(ctx, &id); err != nil {
			return err
		}
		payload, err := json.Marshal(jobPayload{DeliveryID: id})
		if err != nil {
			return err
		}
		_, err = tx.NewRaw(`
			INSERT INTO outbox_events (kind, aggregate_kind, aggregate_id, aggregate_version, payload)
			VALUES (?, 'email_delivery', ?, 1, ?::jsonb)
		`, JobKind, publicID, string(payload)).Exec(ctx)
		return err
	})
	if err != nil {
		return TestEmailResponse{}, fmt.Errorf("request required test email: %w", err)
	}
	return TestEmailResponse{DeliveryID: publicID, Status: "queued"}, nil
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
		SELECT id, public_id, recipient, subject, body, status, attempts, last_safe_error, created_at
		FROM email_deliveries WHERE id = ?
	`, payload.DeliveryID).Scan(ctx, &message.ID, &message.PublicID, &message.Recipient, &message.Subject, &message.Body, &message.Status, &message.Attempts, &message.LastSafeError, &message.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return worker.Permanent("delivery_missing")
	}
	if err != nil {
		return err
	}
	if message.Status == "sent" || message.Status == "failed" {
		return nil
	}
	if time.Since(message.CreatedAt) >= s.cfg.RetryWindow {
		if err := s.recordFailure(ctx, message.ID, "retry_window_exhausted"); err != nil {
			return err
		}
		return worker.Permanent("retry_window_exhausted")
	}
	_, err = s.db.NewRaw(`UPDATE email_deliveries SET attempts = attempts + 1, updated_at = now() WHERE id = ? AND status = 'queued'`, message.ID).Exec(ctx)
	if err != nil {
		return err
	}
	err = s.sender.Send(ctx, smtp.Message{
		ID: message.PublicID, To: message.Recipient, Subject: message.Subject, Body: message.Body,
	})
	if err == nil {
		_, updateErr := s.db.NewRaw(`
			UPDATE email_deliveries SET status = 'sent', sent_at = now(), next_retry_at = NULL,
				last_safe_error = NULL, updated_at = now()
			WHERE id = ? AND status = 'queued'
		`, message.ID).Exec(ctx)
		return updateErr
	}
	failure := &smtp.DeliveryError{Diagnostic: "smtp_unavailable", Temporary: true}
	if !errors.As(err, &failure) {
		failure = &smtp.DeliveryError{Diagnostic: "smtp_unavailable", Temporary: true}
	}
	if !failure.Temporary {
		if err := s.recordFailure(ctx, message.ID, failure.Diagnostic); err != nil {
			return err
		}
		return worker.Permanent(failure.Diagnostic)
	}
	delay := s.retryDelay(message.Attempts)
	remaining := s.cfg.RetryWindow - time.Since(message.CreatedAt)
	if remaining <= 0 {
		if err := s.recordFailure(ctx, message.ID, "retry_window_exhausted"); err != nil {
			return err
		}
		return worker.Permanent("retry_window_exhausted")
	}
	if delay > remaining {
		delay = remaining
	}
	_, updateErr := s.db.NewRaw(`
		UPDATE email_deliveries SET last_safe_error = ?, next_retry_at = now() + (? * interval '1 microsecond'), updated_at = now()
		WHERE id = ? AND status = 'queued'
	`, failure.Diagnostic, delay.Microseconds(), message.ID).Exec(ctx)
	if updateErr != nil {
		return updateErr
	}
	return worker.RetryAfter(delay, failure.Diagnostic)
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

func (s *Service) recordFailure(ctx context.Context, id int64, diagnostic string) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`
			UPDATE email_deliveries SET status = 'failed', failed_at = now(), last_safe_error = ?,
				next_retry_at = NULL, updated_at = now()
			WHERE id = ? AND status = 'queued'
		`, diagnostic, id).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewRaw(`
			INSERT INTO delivery_problems (email_delivery_id, diagnostic)
			VALUES (?, ?) ON CONFLICT (email_delivery_id) DO NOTHING
		`, id, diagnostic).Exec(ctx)
		return err
	})
}
