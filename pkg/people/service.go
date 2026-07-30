// Package people owns durable Person identity and safe Curator-controlled merges.
package people

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/audiences"
	"github.com/robinjoseph08/memento/pkg/family"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/visibility"
	"github.com/uptrace/bun"
)

const maxNameLength = 120

var (
	ErrNotFound                 = errors.New("Person not found")
	ErrInvalidPerson            = errors.New("Person details are invalid")
	ErrStale                    = errors.New("Person was changed by another request")
	ErrMergeStale               = fmt.Errorf("merge preview is stale: %w", ErrStale)
	ErrCuratorMustSurvive       = errors.New("the Curator Person must survive the merge")
	ErrSurvivorMustBeCurrent    = errors.New("the survivor Person must be current")
	ErrTwoCurrentGenerations    = errors.New("both People have current Recipient access generations")
	ErrGenerationTransferNeeded = errors.New("the source current Recipient access generation requires explicit transfer")
	ErrEmailResolutionNeeded    = errors.New("the login email conflict requires explicit resolution")
	ErrFamilyMergeCycle         = errors.New("the People are connected by a parent-child path")
	ErrFamilyPartnerConflict    = errors.New("the People have conflicting current and former partner relationships")
	ErrInvalidMerge             = errors.New("Person merge is invalid")
)

// Person is the durable Curator view of one identity.
type Person struct {
	ID                         string     `json:"id"`
	DisplayName                string     `json:"display_name"`
	SortName                   string     `json:"sort_name"`
	Version                    int64      `json:"version"`
	Status                     string     `json:"status"`
	CreatedAt                  time.Time  `json:"created_at"`
	UpdatedAt                  time.Time  `json:"updated_at"`
	ArchivedAt                 *time.Time `json:"archived_at,omitempty"`
	MergedAt                   *time.Time `json:"merged_at,omitempty"`
	MergedIntoPersonID         string     `json:"merged_into_person_id,omitempty"`
	Roles                      []string   `json:"roles"`
	CurrentAccess              *Access    `json:"current_recipient_access,omitempty"`
	CurrentLoginEmail          string     `json:"current_login_email,omitempty"`
	UnrevokedSessions          int        `json:"unrevoked_sessions"`
	HistoricalAuditCount       int        `json:"historical_audit_count"`
	LatestMeaningfulActivityAt *time.Time `json:"latest_meaningful_activity_at,omitempty"`
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
	CurrentRecipientGenerationID           string   `json:"current_recipient_generation_id,omitempty"`
	SessionsInvalidated                    int      `json:"sessions_invalidated"`
	HistoricalAuditRowsPreserved           int      `json:"historical_audit_rows_preserved"`
	SourceRoles                            []string `json:"source_roles"`
	SurvivorRoles                          []string `json:"survivor_roles"`
	RecipientRoleWillTransfer              bool     `json:"recipient_role_will_transfer"`
	ResultingRecipientGeneration           int      `json:"resulting_recipient_generation,omitempty"`
	FamilyRelationshipsMoved               int      `json:"family_relationships_moved"`
	FamilyRelationshipsArchived            int      `json:"family_relationships_archived"`
	FamilyReferenceFingerprint             string   `json:"family_reference_fingerprint"`
	VisibilityMembershipsMoved             int      `json:"visibility_memberships_moved"`
	InterestEntriesMoved                   int      `json:"interest_entries_moved"`
	InterestHistoryOwnersRetained          int      `json:"interest_history_owners_retained"`
	VisibilityReferenceFingerprint         string   `json:"visibility_reference_fingerprint"`
	AttendanceEntriesMoved                 int      `json:"attendance_entries_moved"`
	CurrentPublishedAttendanceEntriesMoved int      `json:"current_published_attendance_entries_moved"`
	AudienceOverridesMoved                 int      `json:"audience_overrides_moved"`
	AudienceReasonsMoved                   int      `json:"audience_reasons_moved"`
	AudienceReferenceFingerprint           string   `json:"audience_reference_fingerprint"`
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
	CurrentCuratorSessionKept  bool             `json:"current_curator_session_kept"`
	PreviewFingerprint         string           `json:"preview_fingerprint"`
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
	ExpectedRecipientGeneration     int    `json:"expected_recipient_generation" validate:"omitempty,min=1"`
	PreviewFingerprint              string `json:"preview_fingerprint" validate:"required,len=64,hexadecimal"`
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
	if displayName == "" || sortName == "" || strings.ContainsRune(displayName, '\x00') || strings.ContainsRune(sortName, '\x00') ||
		!utf8.ValidString(displayName) || !utf8.ValidString(sortName) ||
		utf8.RuneCountInString(displayName) > maxNameLength || utf8.RuneCountInString(sortName) > maxNameLength {
		return "", "", ErrInvalidPerson
	}
	return displayName, sortName, nil
}

func (s *Service) Create(ctx context.Context, actor setup.CuratorSession, request CreateRequest) (Person, error) {
	var created Person
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var err error
		created, err = s.CreateIn(ctx, tx, actor, request)
		return err
	})
	if err != nil {
		return Person{}, fmt.Errorf("create Person: %w", err)
	}
	return created, nil
}

// CreateIn creates and audits a Person inside a caller-owned transaction.
// Cross-domain workflows use this seam when Person creation must commit atomically
// with their own explicit Curator decision.
func (s *Service) CreateIn(ctx context.Context, tx bun.Tx, actor setup.CuratorSession, request CreateRequest) (Person, error) {
	displayName, sortName, err := normalizePersonNames(request.DisplayName, request.SortName)
	if err != nil {
		return Person{}, err
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return Person{}, err
	}
	now := s.now().UTC()
	if _, err := tx.NewRaw(`INSERT INTO people (id, display_name, sort_name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, id, displayName, sortName, now, now).Exec(ctx); err != nil {
		return Person{}, err
	}
	if err := appendAudit(ctx, tx, actor, &id, "person_created", map[string]any{"version": 1}); err != nil {
		return Person{}, err
	}
	return getPerson(ctx, tx, id, false)
}

type personListRow struct {
	ID                 uuid.UUID     `bun:"id"`
	DisplayName        string        `bun:"display_name"`
	SortName           string        `bun:"sort_name"`
	Version            int64         `bun:"version"`
	CreatedAt          time.Time     `bun:"created_at"`
	UpdatedAt          time.Time     `bun:"updated_at"`
	ArchivedAt         *time.Time    `bun:"archived_at"`
	MergedAt           *time.Time    `bun:"merged_at"`
	MergedIntoPersonID uuid.NullUUID `bun:"merged_into_person_id"`
}

type personRoleRow struct {
	PersonID uuid.UUID `bun:"person_id"`
	Role     string    `bun:"role"`
}

type personAccessRow struct {
	PersonID   uuid.UUID `bun:"person_id"`
	ID         uuid.UUID `bun:"id"`
	Email      string    `bun:"email"`
	State      string    `bun:"state"`
	Generation int       `bun:"generation"`
}

type personCountRow struct {
	PersonID uuid.UUID `bun:"person_id"`
	Count    int       `bun:"count"`
}

type personActivityRow struct {
	PersonID uuid.UUID `bun:"recipient_person_id"`
	Latest   time.Time `bun:"latest"`
}

func (s *Service) List(ctx context.Context, query string, includeArchived bool) (ListResponse, error) {
	query = escapeLikePattern(strings.TrimSpace(query))
	result := ListResponse{People: []Person{}}
	err := s.db.RunInTx(ctx, readOnlySnapshot(), func(ctx context.Context, tx bun.Tx) error {
		rows := make([]personListRow, 0)
		if err := tx.NewRaw(`
			SELECT id, display_name, sort_name, version, created_at, updated_at, archived_at, merged_at, merged_into_person_id
			FROM people
			WHERE (? OR archived_at IS NULL)
			  AND (? = '' OR memento_normalize_person_name(display_name || ' ' || sort_name)
			      LIKE '%' || memento_normalize_person_name(?) || '%' ESCAPE E'\\')
			ORDER BY (merged_at IS NOT NULL), (archived_at IS NOT NULL), memento_normalize_person_name(sort_name), id
			LIMIT 200`, includeArchived, query, query).Scan(ctx, &rows); err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, len(rows))
		indexByID := make(map[uuid.UUID]int, len(rows))
		result.People = make([]Person, len(rows))
		for i, row := range rows {
			ids[i] = row.ID
			indexByID[row.ID] = i
			person := Person{
				ID: row.ID.String(), DisplayName: row.DisplayName, SortName: row.SortName, Version: row.Version,
				CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, ArchivedAt: row.ArchivedAt, MergedAt: row.MergedAt,
				Roles: []string{},
			}
			if row.MergedIntoPersonID.Valid {
				person.MergedIntoPersonID = row.MergedIntoPersonID.UUID.String()
			}
			switch {
			case row.MergedAt != nil:
				person.Status = "merged"
			case row.ArchivedAt != nil:
				person.Status = "archived"
			default:
				person.Status = "current"
			}
			result.People[i] = person
		}

		roles := make([]personRoleRow, 0)
		if err := tx.NewRaw(`SELECT person_id, role FROM person_roles WHERE person_id IN (?) ORDER BY person_id, role`, bun.List(ids)).Scan(ctx, &roles); err != nil {
			return err
		}
		for _, role := range roles {
			i := indexByID[role.PersonID]
			result.People[i].Roles = append(result.People[i].Roles, role.Role)
		}

		accesses := make([]personAccessRow, 0)
		if err := tx.NewRaw(`SELECT access.person_id, access.id, access.generation, access.state, COALESCE(email.email, '') AS email
			FROM recipient_access_generations AS access
			LEFT JOIN recipient_emails AS email ON email.recipient_access_generation_id = access.id AND email.is_current
			WHERE access.person_id IN (?) AND access.is_current`, bun.List(ids)).Scan(ctx, &accesses); err != nil {
			return err
		}
		for _, access := range accesses {
			i := indexByID[access.PersonID]
			result.People[i].CurrentAccess = &Access{ID: access.ID.String(), Generation: access.Generation, State: access.State}
			result.People[i].CurrentLoginEmail = access.Email
		}

		activities := make([]personActivityRow, 0)
		if err := tx.NewRaw(`SELECT recipient_person_id, max(last_occurred_at) AS latest
			FROM engagement_daily_aggregates WHERE recipient_person_id IN (?)
			GROUP BY recipient_person_id`, bun.List(ids)).Scan(ctx, &activities); err != nil {
			return err
		}
		for _, activity := range activities {
			latest := activity.Latest
			result.People[indexByID[activity.PersonID]].LatestMeaningfulActivityAt = &latest
		}

		sessionCounts := make([]personCountRow, 0)
		if err := tx.NewRaw(`SELECT person_id, count(*) AS count FROM sessions WHERE person_id IN (?) AND revoked_at IS NULL GROUP BY person_id`, bun.List(ids)).Scan(ctx, &sessionCounts); err != nil {
			return err
		}
		for _, count := range sessionCounts {
			result.People[indexByID[count.PersonID]].UnrevokedSessions = count.Count
		}

		auditCounts := make([]personCountRow, 0)
		if err := tx.NewRaw(`SELECT person.id AS person_id, count(audit.id) AS count
			FROM people AS person
			LEFT JOIN security_audit_events AS audit ON audit.actor_person_id = person.id OR audit.subject_person_id = person.id
			WHERE person.id IN (?) GROUP BY person.id`, bun.List(ids)).Scan(ctx, &auditCounts); err != nil {
			return err
		}
		for _, count := range auditCounts {
			result.People[indexByID[count.PersonID]].HistoricalAuditCount = count.Count
		}
		return nil
	})
	return result, err
}

func escapeLikePattern(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

func readOnlySnapshot() *sql.TxOptions {
	return &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (Person, error) {
	var person Person
	err := s.db.RunInTx(ctx, readOnlySnapshot(), func(ctx context.Context, tx bun.Tx) error {
		var err error
		person, err = getPerson(ctx, tx, id, false)
		return err
	})
	return person, err
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
		emailErr := db.NewRaw(`SELECT email FROM recipient_emails WHERE recipient_access_generation_id = ? AND is_current`, accessID).Scan(ctx, &person.CurrentLoginEmail)
		if emailErr != nil && !errors.Is(emailErr, sql.ErrNoRows) {
			return Person{}, emailErr
		}
	}
	if err := db.NewRaw(`SELECT count(*) FROM sessions WHERE person_id = ? AND revoked_at IS NULL`, id).Scan(ctx, &person.UnrevokedSessions); err != nil {
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
	var updated Person
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockActorAndSubject(ctx, tx, actor.PersonID, id); err != nil {
			return err
		}
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
		if err := appendAudit(ctx, tx, actor, &id, "person_updated", map[string]any{"previous_version": request.Version}); err != nil {
			return err
		}
		updated, err = getPerson(ctx, tx, id, false)
		return err
	})
	if err != nil {
		return Person{}, err
	}
	return updated, nil
}

func (s *Service) Archive(ctx context.Context, actor setup.CuratorSession, id uuid.UUID, version int64) (Person, error) {
	now := s.now().UTC()
	var archived Person
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockActorAndSubject(ctx, tx, actor.PersonID, id); err != nil {
			return err
		}
		var curator bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM person_roles WHERE person_id = ? AND role = 'curator')`, id).Scan(ctx, &curator); err != nil {
			return err
		}
		if curator {
			return ErrCuratorMustSurvive
		}
		for _, lock := range []string{
			`SELECT person_id FROM immich_person_links WHERE person_id = ? FOR UPDATE`,
			`SELECT id FROM immich_face_anchors WHERE person_id = ? ORDER BY id FOR UPDATE`,
			`SELECT id FROM person_repair_candidates WHERE person_id = ? AND state = 'pending' ORDER BY id FOR UPDATE`,
		} {
			if _, err := tx.NewRaw(lock, id).Exec(ctx); err != nil {
				return err
			}
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
		visibilityActor := setup.SessionActor{PersonID: actor.PersonID, SessionID: actor.SessionID, Curator: true}
		if err := visibility.ArchivePersonReferences(ctx, tx, id, visibilityActor, now); err != nil {
			return err
		}
		var accessID uuid.UUID
		accessErr := tx.NewRaw(`SELECT id FROM recipient_access_generations WHERE person_id = ? AND is_current FOR UPDATE`, id).Scan(ctx, &accessID)
		if accessErr != nil && !errors.Is(accessErr, sql.ErrNoRows) {
			return accessErr
		}
		if accessErr == nil {
			if _, err := tx.NewRaw(`
				UPDATE invitations SET revoked_at = ?
				WHERE recipient_access_generation_id = ?
				  AND accepted_at IS NULL AND revoked_at IS NULL AND superseded_at IS NULL
			`, now, accessID).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`
				UPDATE recipient_emails SET is_current = false, ended_at = ?
				WHERE recipient_access_generation_id = ? AND is_current
			`, now, accessID).Exec(ctx); err != nil {
				return err
			}
			if err := execExactlyOne(ctx, tx, `
				UPDATE recipient_access_generations SET state = 'revoked', is_current = false, ended_at = ?, updated_at = ?
				WHERE id = ? AND is_current
			`, now, now, accessID); err != nil {
				return err
			}
		}
		if _, err := tx.NewRaw(`
			UPDATE person_repair_candidates
			SET state = 'superseded', resolved_at = ?, resolved_by_person_id = ?
			WHERE person_id = ? AND state = 'pending'
		`, now, actor.PersonID, id).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM immich_face_anchors WHERE person_id = ?`, id).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM immich_person_links WHERE person_id = ?`, id).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE sessions SET revoked_at = ? WHERE person_id = ? AND revoked_at IS NULL`, now, id).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE push_subscriptions AS push SET disabled_at = ? WHERE push.disabled_at IS NULL AND EXISTS (SELECT 1 FROM sessions AS session WHERE session.id = push.session_id AND session.person_id = ?)`, now, id).Exec(ctx); err != nil {
			return err
		}
		if err := appendAudit(ctx, tx, actor, &id, "person_archived", map[string]any{"previous_version": version}); err != nil {
			return err
		}
		archived, err = getPerson(ctx, tx, id, false)
		return err
	})
	if err != nil {
		return Person{}, err
	}
	return archived, nil
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

func (s *Service) PreviewMerge(ctx context.Context, actor setup.CuratorSession, sourceID, survivorID uuid.UUID) (MergePreview, error) {
	if sourceID == survivorID {
		return MergePreview{}, ErrInvalidMerge
	}
	var preview MergePreview
	err := s.db.RunInTx(ctx, readOnlySnapshot(), func(ctx context.Context, tx bun.Tx) error {
		var err error
		preview, err = previewMerge(ctx, tx, actor, sourceID, survivorID, false)
		return err
	})
	return preview, err
}

func invitationSuggestionReferenceFingerprint(ctx context.Context, db bun.IDB, sourceID, survivorID uuid.UUID) (string, error) {
	references := make([]struct {
		Kind        string
		ReferenceID string
	}, 0)
	if err := db.NewRaw(`
		SELECT 'requester' AS kind, id::text AS reference_id
		FROM invitation_suggestions WHERE requester_person_id IN (?, ?)
		UNION ALL
		SELECT 'match' AS kind, id::text AS reference_id
		FROM invitation_suggestions WHERE matched_person_id IN (?, ?)
		UNION ALL
		SELECT 'recipient_activity' AS kind, id::text AS reference_id
		FROM recipient_activity_items WHERE recipient_person_id IN (?, ?)
		ORDER BY kind, reference_id
	`, sourceID, survivorID, sourceID, survivorID, sourceID, survivorID).Scan(ctx, &references); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(references)
	if err != nil {
		return "", err
	}
	fingerprint := sha256.Sum256(encoded)
	return hex.EncodeToString(fingerprint[:]), nil
}

func previewMerge(ctx context.Context, db bun.IDB, actor setup.CuratorSession, sourceID, survivorID uuid.UUID, lockPeople bool) (MergePreview, error) {
	source, err := getPerson(ctx, db, sourceID, lockPeople)
	if err != nil {
		return MergePreview{}, err
	}
	survivor, err := getPerson(ctx, db, survivorID, lockPeople)
	if err != nil {
		return MergePreview{}, err
	}
	if source.Status == "merged" || survivor.Status == "merged" {
		return MergePreview{}, ErrInvalidMerge
	}
	var historicalAuditRows int
	if err := db.NewRaw(`SELECT count(*) FROM security_audit_events
		WHERE actor_person_id IN (?, ?) OR subject_person_id IN (?, ?)`, sourceID, survivorID, sourceID, survivorID).Scan(ctx, &historicalAuditRows); err != nil {
		return MergePreview{}, err
	}
	currentCuratorSessionKept := actor.PersonID == survivorID
	var sessionsInvalidated int
	sessionQuery := `SELECT count(*) FROM sessions WHERE person_id IN (?, ?) AND revoked_at IS NULL`
	sessionArgs := []any{sourceID, survivorID}
	if currentCuratorSessionKept {
		sessionQuery += ` AND id <> ?`
		sessionArgs = append(sessionArgs, actor.SessionID)
	}
	if err := db.NewRaw(sessionQuery, sessionArgs...).Scan(ctx, &sessionsInvalidated); err != nil {
		return MergePreview{}, err
	}
	preview := MergePreview{
		Source: source, Survivor: survivor, CanMerge: true, Blockers: []string{},
		RolesWillNotBeUnioned: true, AudienceAuthorityUnchanged: true,
		CurrentCuratorSessionKept: currentCuratorSessionKept,
	}
	preview.References = ReferenceEffects{
		SessionsInvalidated:          sessionsInvalidated,
		HistoricalAuditRowsPreserved: historicalAuditRows,
		SourceRoles:                  source.Roles,
		SurvivorRoles:                survivor.Roles,
		RecipientRoleWillTransfer:    source.CurrentAccess != nil && contains(source.Roles, "recipient"),
	}
	familyEffects, familyErr := family.PreviewPersonMerge(ctx, db, sourceID, survivorID)
	knownFamilyConflict := errors.Is(familyErr, family.ErrMergeCycle) || errors.Is(familyErr, family.ErrMergePartnerConflict)
	if familyErr != nil && !knownFamilyConflict {
		return MergePreview{}, familyErr
	}
	preview.References.FamilyRelationshipsMoved = familyEffects.RelationshipsMoved
	preview.References.FamilyRelationshipsArchived = familyEffects.RelationshipsArchived
	preview.References.FamilyReferenceFingerprint = familyEffects.ReferenceFingerprint
	visibilityEffects, err := visibility.PreviewPersonMerge(ctx, db, sourceID, survivorID)
	if err != nil {
		return MergePreview{}, err
	}
	preview.References.VisibilityMembershipsMoved = visibilityEffects.CircleMembershipsMoved
	preview.References.InterestEntriesMoved = visibilityEffects.InterestEntriesMoved
	preview.References.InterestHistoryOwnersRetained = visibilityEffects.InterestHistoryOwnersRetained
	preview.References.VisibilityReferenceFingerprint = visibilityEffects.ReferenceFingerprint
	audienceEffects, err := audiences.PreviewPersonMerge(ctx, db, sourceID, survivorID)
	if err != nil {
		return MergePreview{}, err
	}
	preview.References.AttendanceEntriesMoved = audienceEffects.AttendanceEntriesMoved
	preview.References.CurrentPublishedAttendanceEntriesMoved = audienceEffects.CurrentPublishedAttendanceEntriesMoved
	preview.References.AudienceOverridesMoved = audienceEffects.AudienceOverridesMoved
	preview.References.AudienceReasonsMoved = audienceEffects.AudienceReasonsMoved
	preview.References.AudienceReferenceFingerprint = audienceEffects.ReferenceFingerprint
	if errors.Is(familyErr, family.ErrMergeCycle) {
		preview.CanMerge = false
		preview.Blockers = append(preview.Blockers, "Resolve the parent-child path between these People before merging them.")
	}
	if errors.Is(familyErr, family.ErrMergePartnerConflict) {
		preview.CanMerge = false
		preview.Blockers = append(preview.Blockers, "Resolve conflicting current and former partner relationships before merging these People.")
	}
	preview.SessionsWillBeInvalidated = sessionsInvalidated > 0
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
		if err := db.NewRaw(`SELECT COALESCE(max(generation), 0) + 1 FROM recipient_access_generations WHERE person_id = ?`, survivorID).Scan(ctx, &preview.References.ResultingRecipientGeneration); err != nil {
			return MergePreview{}, err
		}
		preview.SourceEmail = source.CurrentLoginEmail
		var survivorEmail string
		emailErr := db.NewRaw(`SELECT email.email FROM recipient_emails email JOIN recipient_access_generations access ON access.id = email.recipient_access_generation_id WHERE access.person_id = ? AND email.is_current ORDER BY email.created_at DESC LIMIT 1`, survivorID).Scan(ctx, &survivorEmail)
		if emailErr != nil && !errors.Is(emailErr, sql.ErrNoRows) {
			return MergePreview{}, emailErr
		}
		preview.SurvivorEmail = survivorEmail
		preview.RequiresEmailResolution = survivorEmail != "" && source.CurrentLoginEmail != "" && !strings.EqualFold(survivorEmail, source.CurrentLoginEmail)
	}
	suggestionReferenceFingerprint, err := invitationSuggestionReferenceFingerprint(ctx, db, sourceID, survivorID)
	if err != nil {
		return MergePreview{}, err
	}
	encoded, err := json.Marshal(preview)
	if err != nil {
		return MergePreview{}, err
	}
	encoded = append(encoded, suggestionReferenceFingerprint...)
	fingerprint := sha256.Sum256(encoded)
	preview.PreviewFingerprint = hex.EncodeToString(fingerprint[:])
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
	var merged Person
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := family.LockGraph(ctx, tx); err != nil {
			return err
		}
		// The graph lock precedes stable parent and child-row locks across Family mutations and Person merges.
		if _, err := tx.NewRaw(`SELECT id FROM people WHERE id IN (?, ?, ?) ORDER BY id FOR NO KEY UPDATE`, actor.PersonID, sourceID, survivorID).Exec(ctx); err != nil {
			return err
		}
		for _, lock := range []string{
			`SELECT person_id FROM person_roles WHERE person_id IN (?, ?) ORDER BY person_id, role FOR UPDATE`,
			`SELECT id FROM recipient_access_generations WHERE person_id IN (?, ?) ORDER BY id FOR UPDATE`,
			`SELECT email.id FROM recipient_emails AS email JOIN recipient_access_generations AS access ON access.id = email.recipient_access_generation_id WHERE access.person_id IN (?, ?) ORDER BY email.id FOR UPDATE OF email`,
			`SELECT id FROM sessions WHERE person_id IN (?, ?) ORDER BY id FOR UPDATE`,
			`SELECT person_id FROM immich_person_links WHERE person_id IN (?, ?) ORDER BY person_id FOR UPDATE`,
			`SELECT id FROM immich_face_anchors WHERE person_id IN (?, ?) ORDER BY id FOR UPDATE`,
			`SELECT id FROM person_repair_candidates WHERE person_id IN (?, ?) AND state = 'pending' ORDER BY id FOR UPDATE`,
		} {
			if _, err := tx.NewRaw(lock, sourceID, survivorID).Exec(ctx); err != nil {
				return err
			}
		}
		if _, err := tx.NewRaw(`
			SELECT id FROM invitation_suggestions
			WHERE requester_person_id IN (?, ?) OR matched_person_id IN (?, ?)
			ORDER BY id FOR UPDATE
		`, sourceID, survivorID, sourceID, survivorID).Exec(ctx); err != nil {
			return err
		}
		if err := visibility.LockReferences(ctx, tx); err != nil {
			return err
		}
		if err := audiences.LockReferences(ctx, tx, sourceID, survivorID); err != nil {
			return err
		}
		currentPreview, err := previewMerge(ctx, tx, actor, sourceID, survivorID, false)
		if err != nil {
			return err
		}
		if request.PreviewFingerprint != currentPreview.PreviewFingerprint {
			return ErrMergeStale
		}
		source := currentPreview.Source
		survivor := currentPreview.Survivor
		if source.Version != request.SourceVersion || survivor.Version != request.SurvivorVersion {
			return ErrMergeStale
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
		familyEffects, err := family.MergePersonReferences(ctx, tx, sourceID, survivorID, now)
		if errors.Is(err, family.ErrMergeCycle) {
			return ErrFamilyMergeCycle
		}
		if errors.Is(err, family.ErrMergePartnerConflict) {
			return ErrFamilyPartnerConflict
		}
		if err != nil {
			return err
		}
		visibilityActor := setup.SessionActor{PersonID: actor.PersonID, SessionID: actor.SessionID, Curator: true}
		if err := visibility.MergePersonReferences(ctx, tx, sourceID, survivorID, visibilityActor, now); err != nil {
			return err
		}
		audienceEffects, err := audiences.MergePersonReferences(ctx, tx, sourceID, survivorID)
		if err != nil {
			return err
		}
		if _, err := tx.NewRaw(`
			UPDATE invitation_suggestions
			SET requester_person_id = CASE WHEN requester_person_id = ? THEN ? ELSE requester_person_id END,
			    matched_person_id = CASE WHEN matched_person_id = ? THEN ? ELSE matched_person_id END,
			    updated_at = ?
			WHERE requester_person_id = ? OR matched_person_id = ?
		`, sourceID, survivorID, sourceID, survivorID, now, sourceID, sourceID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE recipient_activity_items SET recipient_person_id = ? WHERE recipient_person_id = ?`, survivorID, sourceID).Exec(ctx); err != nil {
			return err
		}
		resultingGeneration := 0
		if _, err := tx.NewRaw(`
			UPDATE person_repair_candidates
			SET state = 'superseded', resolved_at = ?, resolved_by_person_id = ?
			WHERE person_id = ? AND state = 'pending'
		`, now, actor.PersonID, sourceID).Exec(ctx); err != nil {
			return err
		}
		if source.CurrentAccess != nil {
			if !request.TransferCurrentAccessGeneration {
				return ErrGenerationTransferNeeded
			}
			if err := tx.NewRaw(`SELECT COALESCE(max(generation), 0) + 1 FROM recipient_access_generations WHERE person_id = ?`, survivorID).Scan(ctx, &resultingGeneration); err != nil {
				return err
			}
			if request.ExpectedRecipientGeneration != resultingGeneration {
				return ErrMergeStale
			}
			if _, err := tx.NewRaw(`
				UPDATE invitations SET superseded_at = ?
				WHERE recipient_access_generation_id = ? AND accepted_at IS NULL AND revoked_at IS NULL AND superseded_at IS NULL
			`, now, source.CurrentAccess.ID).Exec(ctx); err != nil {
				return err
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
				if err := execExactlyOne(ctx, tx, `UPDATE recipient_emails SET is_current = false, ended_at = ? WHERE id = ? AND is_current`, now, survivorEmailID); err != nil {
					return err
				}
				if request.EmailResolution == "keep_survivor" {
					if err := execExactlyOne(ctx, tx, `UPDATE recipient_emails SET is_current = false, ended_at = ? WHERE recipient_access_generation_id = ? AND is_current`, now, source.CurrentAccess.ID); err != nil {
						return err
					}
					if _, err := tx.NewRaw(`INSERT INTO recipient_emails (id, recipient_access_generation_id, email, normalized_email, is_current, created_at) VALUES (?, ?, ?, lower(?), true, ?)`, newEmailID, source.CurrentAccess.ID, survivorEmail, survivorEmail, now).Exec(ctx); err != nil {
						return err
					}
				}
			}
			if err := execExactlyOne(ctx, tx, `UPDATE recipient_access_generations SET person_id = ?, generation = ?, updated_at = ? WHERE id = ? AND person_id = ? AND is_current`, survivorID, resultingGeneration, now, source.CurrentAccess.ID, sourceID); err != nil {
				return err
			}
			if contains(source.Roles, "recipient") {
				if _, err := tx.NewRaw(`INSERT INTO person_roles (person_id, role, created_at) VALUES (?, 'recipient', ?) ON CONFLICT DO NOTHING`, survivorID, now).Exec(ctx); err != nil {
					return err
				}
				if err := execExactlyOne(ctx, tx, `DELETE FROM person_roles WHERE person_id = ? AND role = 'recipient'`, sourceID); err != nil {
					return err
				}
			}
		}
		var survivorHasImmichLink bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM immich_person_links WHERE person_id = ?)`, survivorID).Scan(ctx, &survivorHasImmichLink); err != nil {
			return err
		}
		if survivorHasImmichLink {
			if _, err := tx.NewRaw(`DELETE FROM immich_face_anchors WHERE person_id = ?`, sourceID).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`DELETE FROM immich_person_links WHERE person_id = ?`, sourceID).Exec(ctx); err != nil {
				return err
			}
		} else {
			if _, err := tx.NewRaw(`UPDATE immich_person_links SET person_id = ?, version = version + 1 WHERE person_id = ?`, survivorID, sourceID).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`UPDATE immich_face_anchors SET person_id = ? WHERE person_id = ?`, survivorID, sourceID).Exec(ctx); err != nil {
				return err
			}
		}
		sessionQuery := `UPDATE sessions SET revoked_at = ? WHERE person_id IN (?, ?) AND revoked_at IS NULL`
		sessionArgs := []any{now, sourceID, survivorID}
		if actor.PersonID == survivorID {
			sessionQuery += ` AND id <> ?`
			sessionArgs = append(sessionArgs, actor.SessionID)
		}
		if _, err := tx.NewRaw(sessionQuery, sessionArgs...).Exec(ctx); err != nil {
			return err
		}
		pushQuery := `UPDATE push_subscriptions AS push SET disabled_at = ? WHERE push.disabled_at IS NULL AND EXISTS (SELECT 1 FROM sessions AS session WHERE session.id = push.session_id AND session.person_id IN (?, ?)`
		pushArgs := []any{now, sourceID, survivorID}
		if actor.PersonID == survivorID {
			pushQuery += ` AND session.id <> ?`
			pushArgs = append(pushArgs, actor.SessionID)
		}
		pushQuery += `)`
		if _, err := tx.NewRaw(pushQuery, pushArgs...).Exec(ctx); err != nil {
			return err
		}
		if err := execExactlyOne(ctx, tx, `UPDATE people SET archived_at = COALESCE(archived_at, ?), merged_at = ?, merged_into_person_id = ?, version = version + 1, updated_at = ? WHERE id = ? AND version = ?`, now, now, survivorID, now, sourceID, request.SourceVersion); err != nil {
			return err
		}
		if err := execExactlyOne(ctx, tx, `UPDATE people SET version = version + 1, updated_at = ? WHERE id = ? AND version = ?`, now, survivorID, request.SurvivorVersion); err != nil {
			return err
		}
		metadata := map[string]any{
			"survivor_person_id": survivorID.String(), "source_version": request.SourceVersion,
			"survivor_version": request.SurvivorVersion, "generation_transferred": source.CurrentAccess != nil,
			"resulting_recipient_generation": resultingGeneration, "email_resolution": request.EmailResolution,
			"family_relationships_moved":                 familyEffects.RelationshipsMoved,
			"family_relationships_archived":              familyEffects.RelationshipsArchived,
			"attendance_entries_moved":                   audienceEffects.AttendanceEntriesMoved,
			"current_published_attendance_entries_moved": audienceEffects.CurrentPublishedAttendanceEntriesMoved,
			"audience_overrides_moved":                   audienceEffects.AudienceOverridesMoved,
			"audience_reasons_moved":                     audienceEffects.AudienceReasonsMoved,
		}
		if err := appendAudit(ctx, tx, actor, &sourceID, "people_merged", metadata); err != nil {
			return err
		}
		merged, err = getPerson(ctx, tx, survivorID, false)
		return err
	})
	if err != nil {
		return Person{}, err
	}
	return merged, nil
}

func lockActorAndSubject(ctx context.Context, tx bun.Tx, actorID, subjectID uuid.UUID) error {
	_, err := tx.NewRaw(`SELECT id FROM people WHERE id IN (?, ?) ORDER BY id FOR NO KEY UPDATE`, actorID, subjectID).Exec(ctx)
	return err
}

func execExactlyOne(ctx context.Context, tx bun.Tx, query string, args ...any) error {
	result, err := tx.NewRaw(query, args...).Exec(ctx)
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
	return nil
}

func appendAudit(ctx context.Context, tx bun.Tx, actor setup.CuratorSession, subject *uuid.UUID, action string, metadata map[string]any) error {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	request := setup.RequestMetadataFromContext(ctx)
	_, err = tx.NewRaw(`INSERT INTO security_audit_events (actor_person_id, subject_person_id, action, outcome, client_ip, user_agent, session_id, metadata) VALUES (?, ?, ?, 'success', NULLIF(?, '')::inet, ?, ?, ?::jsonb)`, actor.PersonID, subject, action, request.ClientIP, request.UserAgent, actor.SessionID, string(encoded)).Exec(ctx)
	return err
}
