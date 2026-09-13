package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/uptrace/bun"
)

var (
	ErrPreauthorizationConsumed = &errcodes.Error{HTTPCode: 409, Code: "preauthorization_consumed", Message: "This email has already been used to sign in, so there is nobody left to invite."}
	ErrPreauthorizationRevoked  = &errcodes.Error{HTTPCode: 409, Code: "preauthorization_revoked", Message: "This email approval was revoked. Approve the email again before inviting."}
	ErrPersonDeactivated        = &errcodes.Error{HTTPCode: 409, Code: "person_deactivated", Message: "This person is deactivated. Restore their access before inviting them."}
)

// invitationEligibility is shared by sending and retrying: only an active
// Person with an unused approval should receive outreach.
func invitationEligibility(person models.Person, approval models.Preauthorization) error {
	switch {
	case person.DeactivatedAt != nil:
		return ErrPersonDeactivated
	case approval.ConsumedAt != nil:
		return ErrPreauthorizationConsumed
	case approval.RevokedAt != nil:
		return ErrPreauthorizationRevoked
	}
	return nil
}

// invitationMessage is plain outreach: it names the exact email to sign in
// with and links to ordinary sign-in. It carries no token or private link.
func invitationMessage(publicURL, personName, curatorName, email string) notifications.Message {
	body := fmt.Sprintf(`Hi %s,

%s invited you to see photos and videos shared with you on Memento.

Sign in with Google using %s to get started:
%s/sign-in

Access is tied to that exact Google account, so signing in with a different email will not work. If you were not expecting this, you can ignore this message.
`, personName, curatorName, email, strings.TrimRight(publicURL, "/"))
	return notifications.Message{Kind: "invitation", To: email, Subject: fmt.Sprintf("%s invited you to Memento", curatorName), Body: body}
}

func projectInvitation(row models.Invitation, email, sentBy string, delivery notifications.Delivery) Invitation {
	return Invitation{ID: row.ID.String(), PreauthorizationID: row.PreauthorizationID.String(), Email: email, SentBy: sentBy, CreatedAt: row.CreatedAt,
		Delivery: InvitationDelivery{Status: delivery.Status, Attempts: delivery.Attempts, Message: delivery.Message, UpdatedAt: delivery.UpdatedAt, DeliveredAt: delivery.DeliveredAt}}
}

type invitationRow struct {
	models.Invitation `bun:"embed:"`
	Email             string
	SentByName        string
}

func selectInvitations(tx bun.Tx) *bun.SelectQuery {
	return tx.NewSelect().Model((*models.Invitation)(nil)).Column("invitation.*").
		ColumnExpr("approval.email AS email, sender.display_name AS sent_by_name").
		Join("JOIN preauthorizations AS approval ON approval.id = invitation.preauthorization_id").
		Join("JOIN persons AS sender ON sender.id = invitation.sent_by")
}

// personInvitations attaches Memento's delivery state to each Invitation.
func (m *Module) personInvitations(ctx context.Context, tx bun.Tx, personID models.UUID) ([]Invitation, error) {
	rows := []invitationRow{}
	if err := selectInvitations(tx).Where("invitation.person_id = ?", personID).Order("invitation.created_at", "invitation.id").Scan(ctx, &rows); err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	result := make([]Invitation, 0, len(rows))
	if len(rows) == 0 {
		return result, nil
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.DeliveryID.String())
	}
	deliveries := map[string]notifications.Delivery{}
	if m.Mail != nil {
		var err error
		deliveries, err = m.Mail.Deliveries(ctx, tx, ids)
		if err != nil {
			return nil, err
		}
	}
	for _, row := range rows {
		result = append(result, projectInvitation(row.Invitation, row.Email, row.SentByName, deliveries[row.DeliveryID.String()]))
	}
	return result, nil
}

// SendInvitation creates at most one Invitation per Preauthorization and commits
// its delivery record with the same transaction. Repeating the request returns
// the existing Invitation instead of sending another email.
func (m *Module) SendInvitation(ctx context.Context, token, personID string, request SendInvitationRequest) (Invitation, error) {
	var result Invitation
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		curator, err := m.actor(ctx, tx, token, true)
		if err != nil {
			return err
		}
		person, err := personByID(ctx, tx, personID)
		if err != nil {
			return err
		}
		if _, err := uuid.Parse(request.PreauthorizationID); err != nil {
			return errcodes.NotFound("Preauthorization")
		}
		var approval models.Preauthorization
		err = tx.NewSelect().Model(&approval).Where("id = ? AND person_id = ?", request.PreauthorizationID, person.ID).Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return errcodes.NotFound("Preauthorization")
		}
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		// An existing Invitation is the idempotent answer even after its approval was used.
		var existing invitationRow
		err = selectInvitations(tx).Where("invitation.preauthorization_id = ?", approval.ID).Scan(ctx, &existing)
		if err == nil {
			result, err = m.invitationWithDelivery(ctx, tx, existing)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return errorstack.CaptureContext(ctx, err)
		}
		if err := invitationEligibility(person, approval); err != nil {
			return err
		}
		if m.Mail == nil {
			return notifications.ErrMailUnconfigured
		}
		delivery, err := m.Mail.Enqueue(ctx, tx, invitationMessage(m.PublicURL, person.DisplayName, curator.DisplayName, approval.Email))
		if err != nil {
			return err
		}
		deliveryID, err := uuid.Parse(delivery.ID)
		if err != nil {
			return errorstack.Capture(err)
		}
		row := models.Invitation{ID: models.NewUUIDv7(), PersonID: person.ID, PreauthorizationID: approval.ID, DeliveryID: models.UUID(deliveryID), SentBy: curator.ID, CreatedAt: m.now().UTC()}
		if _, err := tx.NewInsert().Model(&row).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		result = projectInvitation(row, approval.Email, curator.DisplayName, delivery)
		return nil
	})
	return result, err
}

func (m *Module) invitationWithDelivery(ctx context.Context, tx bun.Tx, row invitationRow) (Invitation, error) {
	var delivery notifications.Delivery
	if m.Mail != nil {
		deliveries, err := m.Mail.Deliveries(ctx, tx, []string{row.DeliveryID.String()})
		if err != nil {
			return Invitation{}, err
		}
		delivery = deliveries[row.DeliveryID.String()]
	}
	return projectInvitation(row.Invitation, row.Email, row.SentByName, delivery), nil
}

// RetryInvitation is the deliberate resend for failed or uncertain delivery.
// The delivery record decides whether any work is added, so repeated clicks are safe.
func (m *Module) RetryInvitation(ctx context.Context, token, personID, invitationID string) (Invitation, error) {
	var result Invitation
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		if _, err := m.actor(ctx, tx, token, true); err != nil {
			return err
		}
		person, err := personByID(ctx, tx, personID)
		if err != nil {
			return err
		}
		if _, err := uuid.Parse(invitationID); err != nil {
			return errcodes.NotFound("Invitation")
		}
		var row invitationRow
		err = selectInvitations(tx).Where("invitation.id = ? AND invitation.person_id = ?", invitationID, person.ID).Scan(ctx, &row)
		if errors.Is(err, sql.ErrNoRows) {
			return errcodes.NotFound("Invitation")
		}
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		var approval models.Preauthorization
		if err := tx.NewSelect().Model(&approval).Where("id = ?", row.PreauthorizationID).Scan(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if err := invitationEligibility(person, approval); err != nil {
			return err
		}
		if m.Mail == nil {
			return notifications.ErrMailUnconfigured
		}
		delivery, err := m.Mail.Retry(ctx, tx, row.DeliveryID.String())
		if err != nil {
			return err
		}
		result = projectInvitation(row.Invitation, row.Email, row.SentByName, delivery)
		return nil
	})
	return result, err
}
