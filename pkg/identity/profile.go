package identity

import (
	"context"
	"database/sql"
	"errors"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

var ErrLastAccount = &errcodes.Error{HTTPCode: 409, Code: "last_account", Message: "Link another sign-in account before removing your last account, or ask a Curator to remove it for you."}

func (m *Module) Profile(ctx context.Context, token string) (Profile, error) {
	var result Profile
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		person, err := m.actor(ctx, tx, token, false)
		if err != nil {
			return err
		}
		result.Person = projectPerson(person)
		result.Identities, err = linkedIdentities(ctx, tx, person.ID)
		return err
	})
	return result, err
}

func (m *Module) UpdateProfile(ctx context.Context, token string, request UpdateProfileRequest) (Profile, error) {
	var result Profile
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		person, err := m.actor(ctx, tx, token, false)
		if err != nil {
			return err
		}
		name, err := displayName(request.DisplayName)
		if err != nil {
			return err
		}
		linked, err := linkedIdentities(ctx, tx, person.ID)
		if err != nil {
			return err
		}
		allowed := request.UpdateEmail == "" && !request.EmailUpdates
		person.UpdateIdentityID = nil
		for _, value := range linked {
			if value.Email == request.UpdateEmail {
				allowed = true
				id, err := uuid.Parse(value.ID)
				if err != nil {
					return errorstack.Capture(err)
				}
				selected := models.UUID(id)
				person.UpdateIdentityID = &selected
				break
			}
		}
		if !allowed {
			return fieldError("update_email", "Choose an email from your linked identities.")
		}
		person.DisplayName = name
		person.UpdateEmail = request.UpdateEmail
		person.EmailUpdates = request.EmailUpdates
		if _, err := tx.NewUpdate().Model(&person).Column("display_name", "update_identity_id", "email_updates").WherePK().Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		result = Profile{Person: projectPerson(person), Identities: linked}
		return nil
	})
	return result, err
}

// UnlinkIdentity retains the subject's ownership and history but removes login access.
// An empty personID selects the signed-in Person for self-service.
func (m *Module) UnlinkIdentity(ctx context.Context, token, personID, identityID string) error {
	return m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		actor, err := m.actor(ctx, tx, token, personID != "")
		if err != nil {
			return err
		}
		if personID == "" {
			personID = actor.ID.String()
		}
		person, err := personByID(ctx, tx, personID)
		if err != nil {
			return err
		}
		if _, err := uuid.Parse(identityID); err != nil {
			return errcodes.NotFound("Identity")
		}
		var linked models.Identity
		err = tx.NewSelect().Model(&linked).Where("id = ? AND person_id = ?", identityID, person.ID).Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return errcodes.NotFound("Identity")
		}
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if linked.UnlinkedAt != nil {
			return nil
		}
		otherAccount, err := tx.NewSelect().Model((*models.Identity)(nil)).Where("person_id = ? AND id <> ? AND unlinked_at IS NULL", person.ID, linked.ID).Exists(ctx)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if !otherAccount {
			if person.ID == actor.ID {
				return ErrLastAccount
			}
			if person.IsCurator && person.DeactivatedAt == nil {
				if err := protectCuratorAccess(ctx, tx, person.ID); err != nil {
					return err
				}
			}
		}
		if _, err := tx.NewUpdate().Model(&linked).Set("unlinked_at = ?", m.now().UTC()).WherePK().Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if _, err := tx.NewDelete().Model((*models.Session)(nil)).Where("identity_id = ?", linked.ID).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if person.UpdateIdentityID != nil && *person.UpdateIdentityID == linked.ID {
			_, err := tx.NewUpdate().Model(&person).Set("update_identity_id = NULL, email_updates = false").WherePK().Exec(ctx)
			return errorstack.CaptureContext(ctx, err)
		}
		return nil
	})
}
