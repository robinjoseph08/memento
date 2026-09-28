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
		email, err := normalizeEmail(request.Email)
		if err != nil {
			return fieldError("email", "Enter a valid email address.")
		}
		exists, err := tx.NewSelect().Model((*models.Preauthorization)(nil)).Where("email = ? AND consumed_at IS NULL AND revoked_at IS NULL", email).Exists(ctx)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if exists {
			return fieldError("email", "This email already has an unused preauthorization.")
		}
		holder, held, err := linkedHolder(ctx, tx, email)
		if err != nil {
			return err
		}
		switch {
		case held && holder == person.ID:
			return fieldError("email", "This email is already linked to this Person.")
		case held:
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
			return &errcodes.Error{HTTPCode: 409, Code: "preauthorization_consumed", Message: "This email has already been used to sign in. Unlink the email to remove its access."}
		}
		if authorization.RevokedAt != nil {
			return nil
		}
		_, err = tx.NewUpdate().Model(&authorization).Set("revoked_at = ?", m.now().UTC()).WherePK().Exec(ctx)
		return errorstack.CaptureContext(ctx, err)
	})
}

// errNoPreauthorization separates "nobody approved this email" from other
// refusals so SignIn can record an Access Request for it.
var errNoPreauthorization = errors.New("no preauthorization")

// resolvePreauthorization is the admission decision for an address nobody
// holds. Only an unused, unrevoked approval for it admits it, and the approval
// is consumed on the way.
func (m *Module) resolvePreauthorization(ctx context.Context, tx bun.Tx, email string) (models.Person, error) {
	var person models.Person
	var approval models.Preauthorization
	err := tx.NewSelect().Model(&approval).Where("email = ? AND consumed_at IS NULL AND revoked_at IS NULL", email).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return person, errNoPreauthorization
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
	_, err = tx.NewUpdate().Model(&approval).Set("consumed_at = ?", m.now().UTC()).WherePK().Exec(ctx)
	return person, errorstack.CaptureContext(ctx, err)
}

// linkedHolder is the Person a Linked Email currently signs in, and whether
// anyone holds the address at all.
func linkedHolder(ctx context.Context, tx bun.Tx, email string) (models.UUID, bool, error) {
	var linked models.Identity
	err := tx.NewSelect().Model(&linked).Column("person_id").Where("email = ? AND unlinked_at IS NULL", email).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return models.UUID{}, false, nil
	}
	if err != nil {
		return models.UUID{}, false, errorstack.CaptureContext(ctx, err)
	}
	return linked.PersonID, true, nil
}

func linkedIdentities(ctx context.Context, tx bun.Tx, personID models.UUID) ([]LinkedIdentity, error) {
	var identities []models.Identity
	if err := tx.NewSelect().Model(&identities).Where("person_id = ? AND unlinked_at IS NULL", personID).Order("created_at", "id").Scan(ctx); err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	result := make([]LinkedIdentity, 0, len(identities))
	for _, value := range identities {
		result = append(result, LinkedIdentity{ID: value.ID.String(), Email: value.Email, CreatedAt: value.CreatedAt})
	}
	return result, nil
}
