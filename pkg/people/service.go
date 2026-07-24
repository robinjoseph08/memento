// Package people owns durable Person identity and safe Curator-controlled merges.
package people

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/uptrace/bun"
)

const maxNameLength = 120

var (
	ErrNotFound                 = errors.New("Person not found")
	ErrInvalidPerson            = errors.New("Person details are invalid")
	ErrStale                    = errors.New("Person was changed by another request")
	ErrCuratorMustSurvive       = errors.New("the Curator Person must survive the merge")
	ErrSurvivorMustBeCurrent    = errors.New("the survivor Person must be current")
	ErrTwoCurrentGenerations    = errors.New("both People have current Recipient access generations")
	ErrGenerationTransferNeeded = errors.New("the source current Recipient access generation requires explicit transfer")
	ErrEmailResolutionNeeded    = errors.New("the login email conflict requires explicit resolution")
	ErrInvalidMerge             = errors.New("Person merge is invalid")
)

// Person is the durable Curator view of one identity.
type Person struct {
	ID                   string     `json:"id"`
	DisplayName          string     `json:"display_name"`
	SortName             string     `json:"sort_name"`
	Version              int64      `json:"version"`
	Status               string     `json:"status"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	ArchivedAt           *time.Time `json:"archived_at,omitempty"`
	MergedAt             *time.Time `json:"merged_at,omitempty"`
	MergedIntoPersonID   string     `json:"merged_into_person_id,omitempty"`
	Roles                []string   `json:"roles"`
	CurrentAccess        *Access    `json:"current_recipient_access,omitempty"`
	CurrentLoginEmail    string     `json:"current_login_email,omitempty"`
	ActiveSessions       int        `json:"active_sessions"`
	HistoricalAuditCount int        `json:"historical_audit_count"`
}

// Access describes the current Recipient access generation without granting authority.
type Access struct {
	ID         string `json:"id"`
	Generation int    `json:"generation"`
	State      string `json:"state"`
}

// ListResponse is generated to TypeScript by Tygo.
type ListResponse struct {
	People []Person `json:"people"`
}

// CreateRequest is generated to TypeScript by Tygo.
type CreateRequest struct {
	DisplayName string `json:"display_name" mod:"trim" validate:"required,max=120"`
	SortName    string `json:"sort_name" mod:"trim" validate:"omitempty,max=120"`
}

// UpdateRequest is generated to TypeScript by Tygo.
type UpdateRequest struct {
	DisplayName string `json:"display_name" mod:"trim" validate:"required,max=120"`
	SortName    string `json:"sort_name" mod:"trim" validate:"required,max=120"`
	Version     int64  `json:"version" validate:"required,min=1"`
}

// VersionRequest is generated to TypeScript by Tygo.
type VersionRequest struct {
	Version int64 `json:"version" validate:"required,min=1"`
}

// MergePreviewRequest is generated to TypeScript by Tygo.
type MergePreviewRequest struct {
	SourcePersonID   string `json:"source_person_id" validate:"required,uuid"`
	SurvivorPersonID string `json:"survivor_person_id" validate:"required,uuid"`
}

// ReferenceEffects summarizes references affected or deliberately retained by a merge.
type ReferenceEffects struct {
	CurrentRecipientGenerationID string   `json:"current_recipient_generation_id,omitempty"`
	SessionsInvalidated          int      `json:"sessions_invalidated"`
	HistoricalAuditRowsPreserved int      `json:"historical_audit_rows_preserved"`
	SourceRoles                  []string `json:"source_roles"`
	SurvivorRoles                []string `json:"survivor_roles"`
	RecipientRoleWillTransfer    bool     `json:"recipient_role_will_transfer"`
}

// MergePreview is generated to TypeScript by Tygo.
type MergePreview struct {
	Source                     Person           `json:"source"`
	Survivor                   Person           `json:"survivor"`
	References                 ReferenceEffects `json:"affected_references"`
	RequiresGenerationTransfer bool             `json:"requires_generation_transfer"`
	RequiresEmailResolution    bool             `json:"requires_email_resolution"`
	SourceEmail                string           `json:"source_email,omitempty"`
	SurvivorEmail              string           `json:"survivor_email,omitempty"`
	SessionsWillBeInvalidated  bool             `json:"sessions_will_be_invalidated"`
	RolesWillNotBeUnioned      bool             `json:"roles_will_not_be_unioned"`
	AudienceAuthorityUnchanged bool             `json:"audience_authority_unchanged"`
	CanMerge                   bool             `json:"can_merge"`
	Blockers                   []string         `json:"blockers"`
}

// MergeRequest confirms a preview against exact entity versions.
type MergeRequest struct {
	SourcePersonID                  string `json:"source_person_id" validate:"required,uuid"`
	SurvivorPersonID                string `json:"survivor_person_id" validate:"required,uuid"`
	SourceVersion                   int64  `json:"source_version" validate:"required,min=1"`
	SurvivorVersion                 int64  `json:"survivor_version" validate:"required,min=1"`
	TransferCurrentAccessGeneration bool   `json:"transfer_current_access_generation"`
	EmailResolution                 string `json:"email_resolution" validate:"omitempty,oneof=keep_source keep_survivor"`
}

// Service persists People independently from role and access lifecycles.
type Service struct {
	db  *bun.DB
	now func() time.Time
}

func New(db *bun.DB) *Service { return &Service{db: db, now: time.Now} }

func normalizePersonNames(displayName, sortName string) (string, string, error) {
	displayName = strings.TrimSpace(displayName)
	sortName = strings.TrimSpace(sortName)
	if sortName == "" {
		sortName = displayName
	}
	if displayName == "" || sortName == "" || !utf8.ValidString(displayName) || !utf8.ValidString(sortName) ||
		utf8.RuneCountInString(displayName) > maxNameLength || utf8.RuneCountInString(sortName) > maxNameLength {
		return "", "", ErrInvalidPerson
	}
	return displayName, sortName, nil
}

func (s *Service) Create(ctx context.Context, actor setup.CuratorSession, request CreateRequest) (Person, error) {
	displayName, sortName, err := normalizePersonNames(request.DisplayName, request.SortName)
	if err != nil {
		return Person{}, err
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return Person{}, err
	}
	now := s.now().UTC()
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`INSERT INTO people (id, display_name, sort_name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, id, displayName, sortName, now, now).Exec(ctx); err != nil {
			return err
		}
		return appendAudit(ctx, tx, actor, &id, "person_created", map[string]any{"version": 1})
	})
	if err != nil {
		return Person{}, fmt.Errorf("create Person: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *Service) List(ctx context.Context, query string, includeArchived bool) (ListResponse, error) {
	query = strings.TrimSpace(query)
	ids := make([]uuid.UUID, 0)
	err := s.db.NewRaw(`
		SELECT id FROM people
		WHERE (? OR archived_at IS NULL)
		  AND (? = '' OR memento_normalize_person_name(display_name || ' ' || sort_name)
		      LIKE '%' || memento_normalize_person_name(?) || '%')
		ORDER BY (merged_at IS NOT NULL), (archived_at IS NOT NULL), memento_normalize_person_name(sort_name), id
		LIMIT 200`, includeArchived, query, query).Scan(ctx, &ids)
	if err != nil {
		return ListResponse{}, err
	}
	result := ListResponse{People: make([]Person, 0, len(ids))}
	for _, id := range ids {
		person, err := s.Get(ctx, id)
		if err != nil {
			return ListResponse{}, err
		}
		result.People = append(result.People, person)
	}
	return result, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (Person, error) {
	return getPerson(ctx, s.db, id, false)
}

func getPerson(ctx context.Context, db bun.IDB, id uuid.UUID, lock bool) (Person, error) {
	query := `SELECT id, display_name, sort_name, version, created_at, updated_at, archived_at, merged_at, merged_into_person_id FROM people WHERE id = ?`
	if lock {
		query += ` FOR UPDATE`
	}
	var person Person
	var personID uuid.UUID
	var mergedInto uuid.NullUUID
	err := db.NewRaw(query, id).Scan(ctx, &personID, &person.DisplayName, &person.SortName, &person.Version, &person.CreatedAt, &person.UpdatedAt, &person.ArchivedAt, &person.MergedAt, &mergedInto)
	if errors.Is(err, sql.ErrNoRows) {
		return Person{}, ErrNotFound
	}
	if err != nil {
		return Person{}, err
	}
	person.ID = personID.String()
	if mergedInto.Valid {
		person.MergedIntoPersonID = mergedInto.UUID.String()
	}
	switch {
	case person.MergedAt != nil:
		person.Status = "merged"
	case person.ArchivedAt != nil:
		person.Status = "archived"
	default:
		person.Status = "current"
	}
	person.Roles = []string{}
	if err := db.NewRaw(`SELECT role FROM person_roles WHERE person_id = ? ORDER BY role`, id).Scan(ctx, &person.Roles); err != nil {
		return Person{}, err
	}
	var accessID uuid.UUID
	var access Access
	err = db.NewRaw(`SELECT id, generation, state FROM recipient_access_generations WHERE person_id = ? AND is_current`, id).Scan(ctx, &accessID, &access.Generation, &access.State)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Person{}, err
	}
	if err == nil {
		access.ID = accessID.String()
		person.CurrentAccess = &access
		_ = db.NewRaw(`SELECT email FROM recipient_emails WHERE recipient_access_generation_id = ? AND is_current`, accessID).Scan(ctx, &person.CurrentLoginEmail)
	}
	if err := db.NewRaw(`SELECT count(*) FROM sessions WHERE person_id = ? AND revoked_at IS NULL`, id).Scan(ctx, &person.ActiveSessions); err != nil {
		return Person{}, err
	}
	if err := db.NewRaw(`SELECT count(*) FROM security_audit_events WHERE actor_person_id = ? OR subject_person_id = ?`, id, id).Scan(ctx, &person.HistoricalAuditCount); err != nil {
		return Person{}, err
	}
	return person, nil
}

func (s *Service) Update(ctx context.Context, actor setup.CuratorSession, id uuid.UUID, request UpdateRequest) (Person, error) {
	displayName, sortName, err := normalizePersonNames(request.DisplayName, request.SortName)
	if err != nil {
		return Person{}, err
	}
	now := s.now().UTC()
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		result, err := tx.NewRaw(`UPDATE people SET display_name = ?, sort_name = ?, version = version + 1, updated_at = ? WHERE id = ? AND version = ? AND merged_at IS NULL`, displayName, sortName, now, id, request.Version).Exec(ctx)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			return staleOrNotFound(ctx, tx, id)
		}
		return appendAudit(ctx, tx, actor, &id, "person_updated", map[string]any{"previous_version": request.Version})
	})
	if err != nil {
		return Person{}, err
	}
	return s.Get(ctx, id)
}

func (s *Service) Archive(ctx context.Context, actor setup.CuratorSession, id uuid.UUID, version int64) (Person, error) {
	now := s.now().UTC()
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var curator bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM person_roles WHERE person_id = ? AND role = 'curator')`, id).Scan(ctx, &curator); err != nil {
			return err
		}
		if curator {
			return ErrCuratorMustSurvive
		}
		result, err := tx.NewRaw(`UPDATE people SET archived_at = ?, version = version + 1, updated_at = ? WHERE id = ? AND version = ? AND archived_at IS NULL AND merged_at IS NULL`, now, now, id, version).Exec(ctx)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			return staleOrNotFound(ctx, tx, id)
		}
		if _, err := tx.NewRaw(`UPDATE sessions SET revoked_at = ? WHERE person_id = ? AND revoked_at IS NULL`, now, id).Exec(ctx); err != nil {
			return err
		}
		return appendAudit(ctx, tx, actor, &id, "person_archived", map[string]any{"previous_version": version})
	})
	if err != nil {
		return Person{}, err
	}
	return s.Get(ctx, id)
}

func staleOrNotFound(ctx context.Context, db bun.IDB, id uuid.UUID) error {
	var exists bool
	if err := db.NewRaw(`SELECT EXISTS (SELECT 1 FROM people WHERE id = ?)`, id).Scan(ctx, &exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return ErrStale
}

func (s *Service) PreviewMerge(ctx context.Context, sourceID, survivorID uuid.UUID) (MergePreview, error) {
	if sourceID == survivorID {
		return MergePreview{}, ErrInvalidMerge
	}
	source, err := s.Get(ctx, sourceID)
	if err != nil {
		return MergePreview{}, err
	}
	survivor, err := s.Get(ctx, survivorID)
	if err != nil {
		return MergePreview{}, err
	}
	if source.Status == "merged" || survivor.Status == "merged" {
		return MergePreview{}, ErrInvalidMerge
	}
	var historicalAuditRows int
	if err := s.db.NewRaw(`SELECT count(*) FROM security_audit_events
		WHERE actor_person_id IN (?, ?) OR subject_person_id IN (?, ?)`, sourceID, survivorID, sourceID, survivorID).Scan(ctx, &historicalAuditRows); err != nil {
		return MergePreview{}, err
	}
	preview := MergePreview{Source: source, Survivor: survivor, CanMerge: true, Blockers: []string{}, RolesWillNotBeUnioned: true, AudienceAuthorityUnchanged: true}
	preview.References = ReferenceEffects{
		SessionsInvalidated:          source.ActiveSessions + survivor.ActiveSessions,
		HistoricalAuditRowsPreserved: historicalAuditRows,
		SourceRoles:                  source.Roles,
		SurvivorRoles:                survivor.Roles,
		RecipientRoleWillTransfer:    source.CurrentAccess != nil && contains(source.Roles, "recipient"),
	}
	preview.SessionsWillBeInvalidated = preview.References.SessionsInvalidated > 0
	if survivor.Status != "current" {
		preview.CanMerge = false
		preview.Blockers = append(preview.Blockers, "The survivor Person must be current.")
	}
	if contains(source.Roles, "curator") {
		preview.CanMerge = false
		preview.Blockers = append(preview.Blockers, "The Curator Person must be the survivor.")
	}
	if source.CurrentAccess != nil && survivor.CurrentAccess != nil {
		preview.CanMerge = false
		preview.Blockers = append(preview.Blockers, "Resolve one current Recipient access generation before merging.")
	}
	if source.CurrentAccess != nil && survivor.CurrentAccess == nil {
		preview.RequiresGenerationTransfer = true
		preview.References.CurrentRecipientGenerationID = source.CurrentAccess.ID
		preview.SourceEmail = source.CurrentLoginEmail
		var survivorEmail string
		_ = s.db.NewRaw(`SELECT email.email FROM recipient_emails email JOIN recipient_access_generations access ON access.id = email.recipient_access_generation_id WHERE access.person_id = ? AND email.is_current ORDER BY email.created_at DESC LIMIT 1`, survivorID).Scan(ctx, &survivorEmail)
		preview.SurvivorEmail = survivorEmail
		preview.RequiresEmailResolution = survivorEmail != "" && source.CurrentLoginEmail != "" && !strings.EqualFold(survivorEmail, source.CurrentLoginEmail)
	}
	return preview, nil
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (s *Service) Merge(ctx context.Context, actor setup.CuratorSession, request MergeRequest) (Person, error) {
	sourceID, err := uuid.Parse(request.SourcePersonID)
	if err != nil {
		return Person{}, ErrInvalidMerge
	}
	survivorID, err := uuid.Parse(request.SurvivorPersonID)
	if err != nil || sourceID == survivorID {
		return Person{}, ErrInvalidMerge
	}
	newEmailID, err := uuid.NewRandom()
	if err != nil {
		return Person{}, err
	}
	now := s.now().UTC()
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// Lock in stable UUID order before evaluating either identity.
		if _, err := tx.NewRaw(`SELECT id FROM people WHERE id IN (?, ?) ORDER BY id FOR UPDATE`, sourceID, survivorID).Exec(ctx); err != nil {
			return err
		}
		source, err := getPerson(ctx, tx, sourceID, false)
		if err != nil {
			return err
		}
		survivor, err := getPerson(ctx, tx, survivorID, false)
		if err != nil {
			return err
		}
		if source.Version != request.SourceVersion || survivor.Version != request.SurvivorVersion {
			return ErrStale
		}
		if source.Status == "merged" || survivor.Status == "merged" {
			return ErrInvalidMerge
		}
		if survivor.Status != "current" {
			return ErrSurvivorMustBeCurrent
		}
		if contains(source.Roles, "curator") {
			return ErrCuratorMustSurvive
		}
		if source.CurrentAccess != nil && survivor.CurrentAccess != nil {
			return ErrTwoCurrentGenerations
		}
		if source.CurrentAccess != nil {
			if !request.TransferCurrentAccessGeneration {
				return ErrGenerationTransferNeeded
			}
			var survivorEmailID uuid.UUID
			var survivorEmail string
			emailErr := tx.NewRaw(`SELECT email.id, email.email FROM recipient_emails email JOIN recipient_access_generations access ON access.id = email.recipient_access_generation_id WHERE access.person_id = ? AND email.is_current ORDER BY email.created_at DESC LIMIT 1`, survivorID).Scan(ctx, &survivorEmailID, &survivorEmail)
			if emailErr != nil && !errors.Is(emailErr, sql.ErrNoRows) {
				return emailErr
			}
			conflict := emailErr == nil && source.CurrentLoginEmail != "" && !strings.EqualFold(survivorEmail, source.CurrentLoginEmail)
			if conflict && request.EmailResolution != "keep_source" && request.EmailResolution != "keep_survivor" {
				return ErrEmailResolutionNeeded
			}
			if conflict {
				if _, err := tx.NewRaw(`UPDATE recipient_emails SET is_current = false, ended_at = ? WHERE id = ?`, now, survivorEmailID).Exec(ctx); err != nil {
					return err
				}
				if request.EmailResolution == "keep_survivor" {
					if _, err := tx.NewRaw(`UPDATE recipient_emails SET is_current = false, ended_at = ? WHERE recipient_access_generation_id = ? AND is_current`, now, source.CurrentAccess.ID).Exec(ctx); err != nil {
						return err
					}
					if _, err := tx.NewRaw(`INSERT INTO recipient_emails (id, recipient_access_generation_id, email, normalized_email, is_current, created_at) VALUES (?, ?, ?, lower(?), true, ?)`, newEmailID, source.CurrentAccess.ID, survivorEmail, survivorEmail, now).Exec(ctx); err != nil {
						return err
					}
				}
			}
			if _, err := tx.NewRaw(`UPDATE recipient_access_generations
				SET person_id = ?, generation = (
					SELECT COALESCE(max(existing.generation), 0) + 1
					FROM recipient_access_generations AS existing WHERE existing.person_id = ?
				), updated_at = ?
				WHERE id = ? AND person_id = ? AND is_current`, survivorID, survivorID, now, source.CurrentAccess.ID, sourceID).Exec(ctx); err != nil {
				return err
			}
			if contains(source.Roles, "recipient") {
				if _, err := tx.NewRaw(`INSERT INTO person_roles (person_id, role, created_at) VALUES (?, 'recipient', ?) ON CONFLICT DO NOTHING`, survivorID, now).Exec(ctx); err != nil {
					return err
				}
				if _, err := tx.NewRaw(`DELETE FROM person_roles WHERE person_id = ? AND role = 'recipient'`, sourceID).Exec(ctx); err != nil {
					return err
				}
			}
		}
		if _, err := tx.NewRaw(`UPDATE sessions SET revoked_at = ? WHERE person_id IN (?, ?) AND revoked_at IS NULL`, now, sourceID, survivorID).Exec(ctx); err != nil {
			return err
		}
		result, err := tx.NewRaw(`UPDATE people SET archived_at = COALESCE(archived_at, ?), merged_at = ?, merged_into_person_id = ?, version = version + 1, updated_at = ? WHERE id = ? AND version = ?`, now, now, survivorID, now, sourceID, request.SourceVersion).Exec(ctx)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return ErrStale
		}
		result, err = tx.NewRaw(`UPDATE people SET version = version + 1, updated_at = ? WHERE id = ? AND version = ?`, now, survivorID, request.SurvivorVersion).Exec(ctx)
		if err != nil {
			return err
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return ErrStale
		}
		metadata := map[string]any{"survivor_person_id": survivorID.String(), "source_version": request.SourceVersion, "survivor_version": request.SurvivorVersion, "generation_transferred": source.CurrentAccess != nil, "email_resolution": request.EmailResolution}
		return appendAudit(ctx, tx, actor, &sourceID, "people_merged", metadata)
	})
	if err != nil {
		return Person{}, err
	}
	return s.Get(ctx, survivorID)
}

func appendAudit(ctx context.Context, tx bun.Tx, actor setup.CuratorSession, subject *uuid.UUID, action string, metadata map[string]any) error {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = tx.NewRaw(`INSERT INTO security_audit_events (actor_person_id, subject_person_id, action, outcome, session_id, metadata) VALUES (?, ?, ?, 'success', ?, ?::jsonb)`, actor.PersonID, subject, action, actor.SessionID, string(encoded)).Exec(ctx)
	return err
}
