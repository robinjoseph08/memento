package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

var ErrLastEmail = &errcodes.Error{HTTPCode: 409, Code: "last_email", Message: "Link another email before removing your last one, or ask a Curator to remove it for you."}

func (m *Module) Profile(ctx context.Context, token string) (Profile, error) {
	var result Profile
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		person, err := m.actor(ctx, tx, token, false)
		if err != nil {
			return err
		}
		result.Person = projectPerson(person)
		result.Emails, err = linkedEmails(ctx, tx, person.ID)
		return err
	})
	return result, err
}

// applyProfile validates and stores the fields a Person controls. Both profile
// editing and Onboarding completion share it so the two paths cannot drift.
func (m *Module) applyProfile(ctx context.Context, tx bun.Tx, person *models.Person, request UpdateProfileRequest) ([]LinkedEmail, error) {
	name, err := displayName(request.DisplayName)
	if err != nil {
		return nil, err
	}
	linked, err := linkedEmails(ctx, tx, person.ID)
	if err != nil {
		return nil, err
	}
	allowed := request.UpdateEmail == "" && !request.EmailUpdates
	person.UpdateEmailID = nil
	for _, value := range linked {
		if value.Email == request.UpdateEmail {
			allowed = true
			id, err := uuid.Parse(value.ID)
			if err != nil {
				return nil, errorstack.Capture(err)
			}
			selected := models.UUID(id)
			person.UpdateEmailID = &selected
			break
		}
	}
	if !allowed {
		return nil, fieldError("update_email", "Choose one of your linked emails.")
	}
	person.DisplayName = name
	person.UpdateEmail = request.UpdateEmail
	person.EmailUpdates = request.EmailUpdates
	if err := m.setOfferedAlbumUpdates(ctx, tx, person, request.OfferedAlbumUpdates); err != nil {
		return nil, err
	}
	if _, err := tx.NewUpdate().Model(person).Column("display_name", "update_email_id", "email_updates", "offered_album_updates").WherePK().Exec(ctx); err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	return linked, nil
}

// setOfferedAlbumUpdates changes "Tell me about albums I can join" on the
// Person for the caller to save; nil keeps it as it is. Turning it back on
// records everything in their "More albums" as announced, so it starts fresh
// instead of sending what was offered while it was off.
func (m *Module) setOfferedAlbumUpdates(ctx context.Context, tx bun.Tx, person *models.Person, request *bool) error {
	if request == nil {
		return nil
	}
	on := *request
	if on && !person.OfferedAlbumUpdates {
		if m.Announcements == nil {
			return errorstack.Capture(fmt.Errorf("turning on offered Album updates requires announcement storage"))
		}
		if err := m.Announcements.RecordOfferedAlbums(ctx, tx, person.ID.String()); err != nil {
			return err
		}
	}
	person.OfferedAlbumUpdates = on
	return nil
}

func (m *Module) UpdateProfile(ctx context.Context, token string, request UpdateProfileRequest) (Profile, error) {
	var result Profile
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		person, err := m.actor(ctx, tx, token, false)
		if err != nil {
			return err
		}
		linked, err := m.applyProfile(ctx, tx, &person, request)
		if err != nil {
			return err
		}
		result = Profile{Person: projectPerson(person), Emails: linked}
		return nil
	})
	return result, err
}

// UnlinkEmail keeps the address on record but removes its sign-in access,
// ending its sessions. An empty personID selects the signed-in Person. The
// address is admitted again only through a new Preauthorization.
func (m *Module) UnlinkEmail(ctx context.Context, token, personID, emailID string) error {
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
		if _, err := uuid.Parse(emailID); err != nil {
			return errcodes.NotFound("Linked Email")
		}
		var linked models.LinkedEmail
		err = tx.NewSelect().Model(&linked).Where("id = ? AND person_id = ?", emailID, person.ID).Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return errcodes.NotFound("Linked Email")
		}
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if linked.UnlinkedAt != nil {
			return nil
		}
		otherEmail, err := tx.NewSelect().Model((*models.LinkedEmail)(nil)).Where("person_id = ? AND id <> ? AND unlinked_at IS NULL", person.ID, linked.ID).Exists(ctx)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if !otherEmail {
			if person.ID == actor.ID {
				return ErrLastEmail
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
		if _, err := tx.NewDelete().Model((*models.Session)(nil)).Where("linked_email_id = ?", linked.ID).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if person.UpdateEmailID != nil && *person.UpdateEmailID == linked.ID {
			_, err := tx.NewUpdate().Model(&person).Set("update_email_id = NULL, email_updates = false").WherePK().Exec(ctx)
			return errorstack.CaptureContext(ctx, err)
		}
		return nil
	})
}
