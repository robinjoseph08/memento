package identity

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

var ErrFinalCurator = &errcodes.Error{HTTPCode: 409, Code: "final_curator", Message: "Keep at least one active Curator with a linked sign-in account before making this change."}

// protectCuratorAccess prevents both role changes and unlinking from leaving
// an installation that no Curator can sign in to administer.
func protectCuratorAccess(ctx context.Context, tx bun.Tx, excluding models.UUID) error {
	remaining, err := tx.NewSelect().Model((*models.Person)(nil)).
		Where("person.id <> ? AND person.is_curator = true AND person.deactivated_at IS NULL", excluding).
		Where("EXISTS (SELECT 1 FROM identities i WHERE i.person_id = person.id AND i.unlinked_at IS NULL)").Exists(ctx)
	if err != nil {
		return errorstack.CaptureContext(ctx, err)
	}
	if !remaining {
		return ErrFinalCurator
	}
	return nil
}

// change serializes identity changes against sign-in and session revocation.
// This installation-wide lock also protects the final active Curator.
func (m *Module) change(ctx context.Context, fn func(context.Context, bun.Tx) error) error {
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var singleton bool
		if err := tx.NewRaw("SELECT singleton FROM installation WHERE singleton = true FOR UPDATE").Scan(ctx, &singleton); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		return fn(ctx, tx)
	})
	if _, expected := errors.AsType[*errcodes.Error](err); expected {
		return err
	}
	return errorstack.CaptureContext(ctx, err)
}

func selectPeople(tx bun.Tx, model any) *bun.SelectQuery {
	return tx.NewSelect().Model(model).
		Column("person.id", "person.display_name", "person.is_curator", "person.onboarding_completed_at", "person.deactivated_at", "person.update_identity_id", "person.email_updates", "person.created_at").
		ColumnExpr("COALESCE(updates.email, '') AS update_email").
		Join("LEFT JOIN identities AS updates ON updates.id = person.update_identity_id")
}

func personByID(ctx context.Context, tx bun.Tx, id string) (models.Person, error) {
	var person models.Person
	if _, err := uuid.Parse(id); err != nil {
		return person, errcodes.NotFound("Person")
	}
	err := selectPeople(tx, &person).Where("person.id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return person, errcodes.NotFound("Person")
	}
	return person, errorstack.CaptureContext(ctx, err)
}

func (m *Module) actor(ctx context.Context, tx bun.Tx, token string, curator bool) (models.Person, error) {
	session, err := m.sessionPerson(ctx, tx, token)
	if err != nil {
		return models.Person{}, err
	}
	if curator && !session.IsCurator {
		return models.Person{}, ErrAccessDenied
	}
	return session, nil
}

func (m *Module) CreatePerson(ctx context.Context, token string, request CreatePersonRequest) (Person, error) {
	var result Person
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		if _, err := m.actor(ctx, tx, token, true); err != nil {
			return err
		}
		name, err := displayName(request.DisplayName)
		if err != nil {
			return err
		}
		person := models.Person{ID: models.NewUUIDv7(), DisplayName: name, CreatedAt: m.now().UTC()}
		if _, err := tx.NewInsert().Model(&person).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		result = projectPerson(person)
		return nil
	})
	return result, err
}

func (m *Module) ListPeople(ctx context.Context, token, search string) ([]Person, error) {
	result := []Person{}
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		if _, err := m.actor(ctx, tx, token, true); err != nil {
			return err
		}
		var people []models.Person
		query := selectPeople(tx, &people).OrderExpr("lower(person.display_name), person.id")
		if search = strings.TrimSpace(search); search != "" {
			query = query.Where("strpos(lower(person.display_name), lower(?)) > 0", search)
		}
		if err := query.Scan(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		for _, person := range people {
			result = append(result, projectPerson(person))
		}
		return nil
	})
	return result, err
}

func (m *Module) GetPerson(ctx context.Context, token, id string) (PersonDetail, error) {
	result := PersonDetail{Identities: []LinkedIdentity{}, Preauthorizations: []Preauthorization{}}
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		if _, err := m.actor(ctx, tx, token, true); err != nil {
			return err
		}
		person, err := personByID(ctx, tx, id)
		if err != nil {
			return err
		}
		result.Person = projectPerson(person)
		result.Identities, err = linkedIdentities(ctx, tx, person.ID)
		if err != nil {
			return err
		}
		var approvals []models.Preauthorization
		if err := tx.NewSelect().Model(&approvals).Where("person_id = ?", person.ID).Order("created_at", "id").Scan(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		for _, approval := range approvals {
			result.Preauthorizations = append(result.Preauthorizations, projectPreauthorization(approval))
		}
		return nil
	})
	return result, err
}

func (m *Module) UpdatePerson(ctx context.Context, token, id string, request UpdatePersonRequest) (Person, error) {
	var result Person
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		if _, err := m.actor(ctx, tx, token, true); err != nil {
			return err
		}
		person, err := personByID(ctx, tx, id)
		if err != nil {
			return err
		}
		name, err := displayName(request.DisplayName)
		if err != nil {
			return err
		}
		if person.IsCurator && person.DeactivatedAt == nil && (!request.IsCurator || request.Deactivated) {
			if err := protectCuratorAccess(ctx, tx, person.ID); err != nil {
				return err
			}
		}
		person.DisplayName = name
		person.IsCurator = request.IsCurator
		if request.Deactivated {
			if person.DeactivatedAt == nil {
				now := m.now().UTC()
				person.DeactivatedAt = &now
			}
			if err := revokePersonSessions(ctx, tx, person.ID); err != nil {
				return err
			}
		} else {
			person.DeactivatedAt = nil
		}
		if _, err := tx.NewUpdate().Model(&person).WherePK().Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		result = projectPerson(person)
		return nil
	})
	return result, err
}
