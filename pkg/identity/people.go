package identity

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
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
	linkedIdentities := tx.NewSelect().TableExpr("identities AS i").Column("i.id").
		Where("i.person_id = person.id").Where("i.unlinked_at IS NULL")
	remaining, err := tx.NewSelect().Model((*models.Person)(nil)).
		Where("person.id <> ? AND person.is_curator = true AND person.deactivated_at IS NULL", excluding).
		Where("EXISTS (?)", linkedIdentities).Exists(ctx)
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
		if err := tx.NewSelect().Table("installation").Column("singleton").
			Where("singleton = true").For("UPDATE").Scan(ctx, &singleton); err != nil {
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
		Column("person.id", "person.display_name", "person.is_curator", "person.onboarding_completed_at", "person.deactivated_at", "person.update_identity_id", "person.email_updates", "person.avatar_face_id", "person.created_at").
		ColumnExpr("COALESCE(updates.email, '') AS update_email").
		ColumnExpr("(SELECT coalesce(max(face.source_version), '') FROM media_face_associations AS face WHERE face.source_face_id = person.avatar_face_id) AS avatar_version").
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

func (m *Module) ListPeople(ctx context.Context, token, search string) ([]PersonSummary, error) {
	result := []PersonSummary{}
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
		standing, err := signInStanding(ctx, tx, people)
		if err != nil {
			return err
		}
		for _, person := range people {
			row := standing[person.ID]
			row.Person = projectPerson(person)
			result = append(result, row)
		}
		return nil
	})
	return result, err
}

// signInStanding fills the sign-in fields of a PersonSummary for each Person:
// the newest linked email or open Preauthorization, how far they have come,
// and when a browser last used one of their sessions.
func signInStanding(ctx context.Context, tx bun.Tx, people []models.Person) (map[models.UUID]PersonSummary, error) {
	result := make(map[models.UUID]PersonSummary, len(people))
	if len(people) == 0 {
		return result, nil
	}
	ids := make([]models.UUID, 0, len(people))
	for _, person := range people {
		ids = append(ids, person.ID)
		result[person.ID] = PersonSummary{Access: "none"}
	}
	var seen []struct {
		PersonID  models.UUID
		RenewedAt time.Time
	}
	err := tx.NewSelect().TableExpr("sessions AS s").
		ColumnExpr("i.person_id").ColumnExpr("max(s.renewed_at) AS renewed_at").
		Join("JOIN identities AS i ON i.id = s.identity_id").
		Where("i.person_id IN (?)", bun.List(ids)).Where("i.unlinked_at IS NULL").
		GroupExpr("i.person_id").Scan(ctx, &seen)
	if err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	for _, row := range seen {
		summary := result[row.PersonID]
		last := row.RenewedAt
		summary.LastSeenAt = &last
		result[row.PersonID] = summary
	}
	// Oldest first, so the newest of each kind is the one left standing.
	var approvals []models.Preauthorization
	err = tx.NewSelect().Model(&approvals).Where("person_id IN (?)", bun.List(ids)).
		Where("consumed_at IS NULL AND revoked_at IS NULL").Order("created_at", "id").Scan(ctx)
	if err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	for _, approval := range approvals {
		summary := result[approval.PersonID]
		summary.Email = approval.Email
		summary.Access = "approved"
		result[approval.PersonID] = summary
	}
	var identities []models.Identity
	err = tx.NewSelect().Model(&identities).Where("person_id IN (?)", bun.List(ids)).
		Where("unlinked_at IS NULL").Order("created_at", "id").Scan(ctx)
	if err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	onboarded := make(map[models.UUID]bool, len(people))
	for _, person := range people {
		onboarded[person.ID] = person.OnboardingCompletedAt != nil
	}
	for _, identity := range identities {
		summary := result[identity.PersonID]
		summary.Email = identity.Email
		summary.Access = "linked"
		if onboarded[identity.PersonID] {
			summary.Access = "onboarded"
		}
		result[identity.PersonID] = summary
	}
	return result, nil
}

func (m *Module) GetPerson(ctx context.Context, token, id string) (PersonDetail, error) {
	result := PersonDetail{Faces: []LinkedFace{}, Identities: []LinkedIdentity{}, Preauthorizations: []Preauthorization{}, Invitations: []Invitation{}, Sessions: []BrowserSession{}}
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		if _, err := m.actor(ctx, tx, token, true); err != nil {
			return err
		}
		person, err := personByID(ctx, tx, id)
		if err != nil {
			return err
		}
		result.Person = projectPerson(person)
		result.Faces, err = linkedFaces(ctx, tx, person, m.ImmichURL)
		if err != nil {
			return err
		}
		result.Identities, err = linkedIdentities(ctx, tx, person.ID)
		if err != nil {
			return err
		}
		result.Sessions, err = m.personSessions(ctx, tx, person.ID, token)
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
		result.Invitations, err = m.personInvitations(ctx, tx, person.ID)
		if err != nil {
			return err
		}
		if m.Announcements != nil {
			announced, err := m.Announcements.Announced(ctx, tx, person.ID.String())
			if err != nil {
				return err
			}
			result.Announced = AnnouncedContent{Albums: announced.Albums, Entries: announced.Entries}
		}
		return nil
	})
	return result, err
}

func (m *Module) UpdatePerson(ctx context.Context, token, id string, request UpdatePersonRequest) (Person, error) {
	var result Person
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		actor, err := m.actor(ctx, tx, token, true)
		if err != nil {
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
		if person.ID == actor.ID {
			fields := map[string]string{}
			if !request.IsCurator {
				fields["is_curator"] = "Ask another Curator to remove your Curator role."
			}
			if request.Deactivated {
				fields["deactivated"] = "Ask another Curator to deactivate your access."
			}
			if len(fields) > 0 {
				return errcodes.ValidationFields("Check the highlighted fields.", fields)
			}
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
