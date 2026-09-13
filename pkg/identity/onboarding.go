package identity

import (
	"context"
	"fmt"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

// applyProfile validates and stores the fields a Person controls. Both profile
// editing and Onboarding completion share it so the two paths cannot drift.
func applyProfile(ctx context.Context, tx bun.Tx, person *models.Person, request UpdateProfileRequest) ([]LinkedIdentity, error) {
	name, err := displayName(request.DisplayName)
	if err != nil {
		return nil, err
	}
	linked, err := linkedIdentities(ctx, tx, person.ID)
	if err != nil {
		return nil, err
	}
	allowed := request.UpdateEmail == "" && !request.EmailUpdates
	person.UpdateIdentityID = nil
	for _, value := range linked {
		if value.Email == request.UpdateEmail {
			allowed = true
			id, err := uuid.Parse(value.ID)
			if err != nil {
				return nil, errorstack.Capture(err)
			}
			selected := models.UUID(id)
			person.UpdateIdentityID = &selected
			break
		}
	}
	if !allowed {
		return nil, fieldError("update_email", "Choose an email from your linked identities.")
	}
	person.DisplayName = name
	person.UpdateEmail = request.UpdateEmail
	person.EmailUpdates = request.EmailUpdates
	if _, err := tx.NewUpdate().Model(person).Column("display_name", "update_identity_id", "email_updates").WherePK().Exec(ctx); err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	return linked, nil
}

// CompleteOnboarding is the one-time Person transition. It saves the confirmed
// profile and records the notification baseline in the same transaction, so a
// retry, another identity, or a later sign-in finds it already complete and
// changes nothing.
func (m *Module) CompleteOnboarding(ctx context.Context, token string, request UpdateProfileRequest) (Person, error) {
	var result Person
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		person, err := m.actor(ctx, tx, token, false)
		if err != nil {
			return err
		}
		if person.OnboardingCompletedAt != nil {
			result = projectPerson(person)
			return nil
		}
		if m.Announcements == nil {
			return errorstack.Capture(fmt.Errorf("onboarding requires announcement storage"))
		}
		if _, err := applyProfile(ctx, tx, &person, request); err != nil {
			return err
		}
		now := m.now().UTC()
		updated, err := tx.NewUpdate().Model((*models.Person)(nil)).Set("onboarding_completed_at = ?", now).
			Where("id = ? AND onboarding_completed_at IS NULL", person.ID).Exec(ctx)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		count, err := updated.RowsAffected()
		if err != nil {
			return errorstack.Capture(err)
		}
		if count == 1 {
			person.OnboardingCompletedAt = &now
			if _, err := m.Announcements.RecordBaseline(ctx, tx, person.ID.String()); err != nil {
				return err
			}
		}
		result = projectPerson(person)
		return nil
	})
	return result, err
}
