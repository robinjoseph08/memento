package notifications

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

var (
	ErrMailUnconfigured = &errcodes.Error{HTTPCode: 409, Code: "mail_unconfigured", Message: "Email is not configured for this installation. Add SMTP settings to send email."}
	ErrAlreadyDelivered = &errcodes.Error{HTTPCode: 409, Code: "already_delivered", Message: "This email was already delivered."}
)

const (
	StatusQueued    = "queued"
	StatusSending   = "sending"
	StatusDelivered = "delivered"
	StatusFailed    = "failed"
	StatusUncertain = "uncertain"
	// StatusSkipped means send-time validation found no eligible recipient
	// or content, so nothing was sent and nothing needs a Curator.
	StatusSkipped = "skipped"
)

const recordOutcomeTimeout = 10 * time.Second

func projectDelivery(row models.MailDelivery) Delivery {
	return Delivery{ID: row.ID.String(), Status: row.Status, Attempts: row.Attempts, Message: row.Message, UpdatedAt: row.UpdatedAt, DeliveredAt: row.DeliveredAt}
}

// deliveryClaimColumns are rewritten when an attempt starts. Update email
// also refreshes its destination and content at that moment.
var deliveryClaimColumns = []string{"status", "attempts", "message", "updated_at", "recipient", "subject", "body"}

// Enqueue stores the complete message and commits its durable work with the
// caller's transaction. Preferences are the caller's concern: transactional
// mail such as Invitations never consults the update-email setting.
func (m *Module) Enqueue(ctx context.Context, tx bun.Tx, message Message) (Delivery, error) {
	if m.mailer == nil {
		return Delivery{}, ErrMailUnconfigured
	}
	recipient, err := mail.ParseAddress(strings.TrimSpace(message.To))
	if err != nil || strings.TrimSpace(message.Kind) == "" || strings.TrimSpace(message.Subject) == "" {
		return Delivery{}, errorstack.Capture(fmt.Errorf("mail message requires a kind, subject, and valid recipient"))
	}
	now := m.now().UTC()
	row := models.MailDelivery{ID: models.NewUUIDv7(), Kind: message.Kind, Recipient: recipient.Address, Subject: message.Subject, Body: message.Body,
		Status: StatusQueued, CreatedAt: now, UpdatedAt: now}
	if _, err := tx.NewInsert().Model(&row).Exec(ctx); err != nil {
		return Delivery{}, errorstack.CaptureContext(ctx, err)
	}
	if err := m.enqueue(ctx, tx, row.ID.String()); err != nil {
		return Delivery{}, err
	}
	return projectDelivery(row), nil
}

// Retry requeues a failed, uncertain, or skipped delivery. Repeated clicks and
// concurrent submissions are idempotent: an already queued or sending delivery
// is returned unchanged.
func (m *Module) Retry(ctx context.Context, tx bun.Tx, deliveryID string) (Delivery, error) {
	row, err := deliveryRow(ctx, tx, deliveryID, true)
	if err != nil {
		return Delivery{}, err
	}
	return m.retry(ctx, tx, row)
}

// retry requeues an already locked row.
func (m *Module) retry(ctx context.Context, tx bun.Tx, row models.MailDelivery) (Delivery, error) {
	switch row.Status {
	case StatusQueued, StatusSending:
		return projectDelivery(row), nil
	case StatusDelivered:
		return Delivery{}, ErrAlreadyDelivered
	}
	if m.mailer == nil {
		return Delivery{}, ErrMailUnconfigured
	}
	row.Status = StatusQueued
	row.Message = ""
	row.UpdatedAt = m.now().UTC()
	if _, err := tx.NewUpdate().Model(&row).Column("status", "message", "updated_at").WherePK().Exec(ctx); err != nil {
		return Delivery{}, errorstack.CaptureContext(ctx, err)
	}
	if err := m.enqueue(ctx, tx, row.ID.String()); err != nil {
		return Delivery{}, err
	}
	return projectDelivery(row), nil
}

// Deliveries loads Curator-facing state for the given IDs in one query.
func (m *Module) Deliveries(ctx context.Context, db bun.IDB, ids []string) (map[string]Delivery, error) {
	result := map[string]Delivery{}
	if len(ids) == 0 {
		return result, nil
	}
	var rows []models.MailDelivery
	if err := db.NewSelect().Model(&rows).Where("delivery.id IN (?)", bun.List(ids)).Scan(ctx); err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	for _, row := range rows {
		result[row.ID.String()] = projectDelivery(row)
	}
	return result, nil
}

func deliveryRow(ctx context.Context, db bun.IDB, id string, lock bool) (models.MailDelivery, error) {
	var row models.MailDelivery
	if _, err := uuid.Parse(id); err != nil {
		return row, errcodes.NotFound("Delivery")
	}
	q := db.NewSelect().Model(&row).Where("delivery.id = ?", id)
	if lock {
		q = q.For("UPDATE")
	}
	err := q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return row, errcodes.NotFound("Delivery")
	}
	return row, errorstack.CaptureContext(ctx, err)
}

// Execute is the worker body for one delivery. It claims the record, sends
// outside any transaction, then records the outcome. A record still marked
// sending when a new attempt starts means an earlier process died mid-session,
// so the delivery becomes uncertain instead of being sent again. Update email
// is re-validated while claiming: an ineligible recipient or fully revoked
// content skips the send without touching the in-app notification.
func (m *Module) Execute(ctx context.Context, deliveryID string, final bool) error {
	var row models.MailDelivery
	send := false
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var err error
		row, err = deliveryRow(ctx, tx, deliveryID, true)
		if err != nil {
			return err
		}
		now := m.now().UTC()
		switch row.Status {
		case StatusQueued:
			row.UpdatedAt = now
			if row.Kind == KindUpdate {
				skip, err := m.prepareUpdate(ctx, tx, &row, now)
				if err != nil {
					return err
				}
				if skip != "" {
					row.Status = StatusSkipped
					row.Message = skip
					_, err = tx.NewUpdate().Model(&row).Column("status", "message", "updated_at").WherePK().Exec(ctx)
					return errorstack.CaptureContext(ctx, err)
				}
			}
			row.Status = StatusSending
			row.Attempts++
			send = true
			_, err = tx.NewUpdate().Model(&row).Column(deliveryClaimColumns...).WherePK().Exec(ctx)
			return errorstack.CaptureContext(ctx, err)
		case StatusSending:
			row.Status = StatusUncertain
			row.Message = "Memento restarted while this email was being sent, so it may already have been delivered."
			row.UpdatedAt = now
			_, err = tx.NewUpdate().Model(&row).Column("status", "message", "updated_at").WherePK().Exec(ctx)
			return errorstack.CaptureContext(ctx, err)
		default:
			return nil
		}
	})
	if err != nil {
		if errors.Is(err, errcodes.NotFound("Delivery")) {
			return nil
		}
		// A claim that keeps failing, such as a broken content query behind
		// an update email, must not leave the record queued forever with no
		// Curator action: the final attempt records the failure instead.
		if final {
			return errors.Join(transactionError(ctx, err), m.failUnclaimed(ctx, deliveryID))
		}
		return transactionError(ctx, err)
	}
	if !send {
		return nil
	}
	var sendErr error
	if m.mailer == nil {
		sendErr = &DeliveryError{Outcome: OutcomePermanent, Summary: "Email is not configured for this installation."}
	} else {
		sendErr = m.mailer.Send(ctx, Message{Kind: row.Kind, To: row.Recipient, Subject: row.Subject, Body: row.Body})
	}
	return m.recordOutcome(ctx, row, sendErr, final)
}

// failUnclaimed marks a still-queued delivery failed after its last automatic
// attempt could not even claim it, so it surfaces for a deliberate retry.
func (m *Module) failUnclaimed(ctx context.Context, deliveryID string) error {
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordOutcomeTimeout)
	defer cancel()
	_, err := m.db.NewUpdate().Model((*models.MailDelivery)(nil)).
		Set("status = ?", StatusFailed).
		Set("message = ?", "Memento could not prepare this email. Check the application log, then send it again.").
		Set("updated_at = ?", m.now().UTC()).
		Where("id = ? AND status = ?", deliveryID, StatusQueued).Exec(recordCtx)
	return errorstack.CaptureContext(recordCtx, err)
}

// RecoverInterrupted runs once at startup. A delivery still marked sending
// belonged to a process that died mid-session, so its outcome is unknown and
// only a Curator may send it again. River's rescued job later finds nothing to
// do. This assumes Memento's single-process deployment: a second process
// starting while the first is mid-send would mark that send uncertain too.
func (m *Module) RecoverInterrupted(ctx context.Context) (int, error) {
	result, err := m.db.NewUpdate().Model((*models.MailDelivery)(nil)).
		Set("status = ?", StatusUncertain).
		Set("message = ?", "Memento restarted while this email was being sent, so it may already have been delivered.").
		Set("updated_at = ?", m.now().UTC()).
		Where("status = ?", StatusSending).Exec(ctx)
	if err != nil {
		return 0, errorstack.CaptureContext(ctx, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, errorstack.Capture(err)
	}
	return int(count), nil
}

// recordOutcome persists a known result even when the worker context is
// already cancelled, so a safe retry never depends on the dying process.
func (m *Module) recordOutcome(ctx context.Context, row models.MailDelivery, sendErr error, final bool) error {
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordOutcomeTimeout)
	defer cancel()
	now := m.now().UTC()
	var failure *DeliveryError
	retry := false
	switch {
	case sendErr == nil:
		row.Status = StatusDelivered
		row.Message = ""
		row.DeliveredAt = &now
	case errors.As(sendErr, &failure) && failure.Outcome == OutcomeUncertain:
		row.Status = StatusUncertain
		row.Message = failure.Summary
	case errors.As(sendErr, &failure) && failure.Outcome == OutcomePermanent:
		row.Status = StatusFailed
		row.Message = failure.Summary
	default:
		summary := "The mail server could not be reached."
		if failure != nil {
			summary = failure.Summary
		}
		if final {
			row.Status = StatusFailed
			row.Message = fmt.Sprintf("%s Memento stopped retrying after %d attempts.", summary, row.Attempts)
		} else {
			row.Status = StatusQueued
			row.Message = summary
			retry = true
		}
	}
	row.UpdatedAt = now
	_, err := m.db.NewUpdate().Model(&row).Column("status", "message", "updated_at", "delivered_at").
		Where("delivery.id = ? AND delivery.status = ?", row.ID, StatusSending).Exec(recordCtx)
	if err != nil {
		return errorstack.CaptureContext(recordCtx, err)
	}
	if retry {
		return fmt.Errorf("deliver %s: %w", row.Kind, sendErr)
	}
	return nil
}
