package identity

import (
	"context"
	"database/sql"
	"errors"
	"net/mail"
	"strings"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

func projectPreauthorization(value models.Preauthorization) Preauthorization {
	return Preauthorization{ID: value.ID.String(), Email: value.Email, CreatedAt: value.CreatedAt, ConsumedAt: value.ConsumedAt, RevokedAt: value.RevokedAt}
}

func (m *Module) Preauthorize(ctx context.Context, token, personID string, request PreauthorizeRequest) (Preauthorization, error) {
	var result Preauthorization
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		if _, err := m.actor(ctx, tx, token, true); err != nil {
			return err
		}
		person, err := personByID(ctx, tx, personID)
		if err != nil {
			return err
		}
		if person.DeactivatedAt != nil {
			return ErrAccessDenied
		}
		email := strings.TrimSpace(request.Email)
		parsed, err := mail.ParseAddress(email)
		if err != nil || parsed.Address != email || len(email) > 254 || strings.ContainsRune(email, 0) {
			return fieldError("email", "Enter a valid email address.")
		}
		exists, err := tx.NewSelect().Model((*models.Preauthorization)(nil)).Where("email = ? AND consumed_at IS NULL AND revoked_at IS NULL", email).Exists(ctx)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if exists {
			return fieldError("email", "This email already has an unused preauthorization.")
		}
		exists, err = tx.NewSelect().Model((*models.Identity)(nil)).Where("email = ? AND person_id <> ? AND unlinked_at IS NULL", email, person.ID).Exists(ctx)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if exists {
			return fieldError("email", "This email is already linked to another Person.")
		}
		authorization := models.Preauthorization{ID: models.NewUUIDv7(), PersonID: person.ID, Email: email, CreatedAt: m.now().UTC()}
		if _, err := tx.NewInsert().Model(&authorization).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		result = projectPreauthorization(authorization)
		return nil
	})
	return result, err
}

func (m *Module) RevokePreauthorization(ctx context.Context, token, personID, id string) error {
	return m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		if _, err := m.actor(ctx, tx, token, true); err != nil {
			return err
		}
		if _, err := personByID(ctx, tx, personID); err != nil {
			return err
		}
		if _, err := uuid.Parse(id); err != nil {
			return errcodes.NotFound("Preauthorization")
		}
		var authorization models.Preauthorization
		err := tx.NewSelect().Model(&authorization).Where("id = ? AND person_id = ?", id, personID).Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return errcodes.NotFound("Preauthorization")
		}
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if authorization.ConsumedAt != nil {
			return &errcodes.Error{HTTPCode: 409, Code: "preauthorization_consumed", Message: "This email has already been used to sign in. Unlink the identity to remove its access."}
		}
		if authorization.RevokedAt != nil {
			return nil
		}
		_, err = tx.NewUpdate().Model(&authorization).Set("revoked_at = ?", m.now().UTC()).WherePK().Exec(ctx)
		return errorstack.CaptureContext(ctx, err)
	})
}

// resolvePreauthorization is the admission decision for an unknown subject.
// No matching approval means no access; a later Access Request use case can extend this decision.
func (m *Module) resolvePreauthorization(ctx context.Context, tx bun.Tx, claims Claims) (models.Person, error) {
	var person models.Person
	var approval models.Preauthorization
	err := tx.NewSelect().Model(&approval).Where("email = ? AND consumed_at IS NULL AND revoked_at IS NULL", claims.Email).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return person, ErrAccessDenied
	}
	if err != nil {
		return person, errorstack.CaptureContext(ctx, err)
	}
	person, err = personByID(ctx, tx, approval.PersonID.String())
	if err != nil {
		return person, err
	}
	if person.DeactivatedAt != nil {
		return person, ErrAccessDenied
	}
	competing, err := tx.NewSelect().Model((*models.Identity)(nil)).Where("email = ? AND person_id <> ? AND unlinked_at IS NULL", claims.Email, person.ID).Exists(ctx)
	if err != nil {
		return person, errorstack.CaptureContext(ctx, err)
	}
	if competing {
		return person, ErrAccessDenied
	}
	_, err = tx.NewUpdate().Model(&approval).Set("consumed_at = ?", m.now().UTC()).WherePK().Exec(ctx)
	return person, errorstack.CaptureContext(ctx, err)
}

func linkedIdentities(ctx context.Context, tx bun.Tx, personID models.UUID) ([]LinkedIdentity, error) {
	var identities []models.Identity
	if err := tx.NewSelect().Model(&identities).Where("person_id = ? AND unlinked_at IS NULL", personID).Order("created_at", "id").Scan(ctx); err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	result := make([]LinkedIdentity, 0, len(identities))
	for _, value := range identities {
		result = append(result, LinkedIdentity{ID: value.ID.String(), Provider: value.Provider, Email: value.Email, CreatedAt: value.CreatedAt})
	}
	return result, nil
}
