// Package suggestions owns Recipient-submitted Invitation suggestions and explicit Curator resolution.
package suggestions

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/people"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/uptrace/bun"
)

const (
	maxNameLength         = 120
	maxRelationshipLength = 1000
)

var (
	ErrInvalidSuggestion = errors.New("invitation suggestion is invalid")
	ErrNotFound          = errors.New("invitation suggestion not found")
	ErrNotSubmitted      = errors.New("invitation suggestion is no longer submitted")
	ErrInvalidResolution = errors.New("invitation suggestion resolution is invalid")
	ErrPersonUnavailable = errors.New("matched Person is unavailable")
	errGenerateID        = errors.New("generate Invitation suggestion ID")
)

// SubmitRequest is the complete Recipient-supplied suggestion.
type SubmitRequest struct {
	Name                string `json:"name" mod:"trim" validate:"required,max=120"`
	Email               string `json:"email" mod:"trim" validate:"required,email,max=320"`
	RelationshipContext string `json:"relationship_context" mod:"trim" validate:"required,max=1000"`
	SpokeWithPerson     *bool  `json:"spoke_with_person" validate:"required"`
}

// RequesterSuggestion deliberately omits Person matching and every access lifecycle.
type RequesterSuggestion struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Email               string    `json:"email"`
	RelationshipContext string    `json:"relationship_context"`
	SpokeWithPerson     bool      `json:"spoke_with_person"`
	Status              string    `json:"status"`
	SubmittedAt         time.Time `json:"submitted_at"`
}

// RequesterListResponse is the isolated suggestion history for one Recipient.
type RequesterListResponse struct {
	Suggestions []RequesterSuggestion `json:"suggestions"`
}

// PersonMatch is Curator-only advisory matching information. It never resolves a suggestion automatically.
type PersonMatch struct {
	PersonID    string   `json:"person_id"`
	DisplayName string   `json:"display_name"`
	Reasons     []string `json:"reasons"`
}

// CuratorSuggestion includes decision data that is never returned to the requester.
type CuratorSuggestion struct {
	ID                       string        `json:"id"`
	RequesterPersonID        string        `json:"requester_person_id"`
	RequesterName            string        `json:"requester_name"`
	Name                     string        `json:"name"`
	Email                    string        `json:"email"`
	RelationshipContext      string        `json:"relationship_context"`
	SpokeWithPerson          bool          `json:"spoke_with_person"`
	Status                   string        `json:"status"`
	SubmittedAt              time.Time     `json:"submitted_at"`
	ResolvedAt               *time.Time    `json:"resolved_at,omitempty"`
	MatchedPersonID          string        `json:"matched_person_id,omitempty"`
	MatchedPersonName        string        `json:"matched_person_name,omitempty"`
	MatchingPeople           []PersonMatch `json:"matching_people"`
	DuplicateSuggestionCount int           `json:"duplicate_suggestion_count"`
}

// CuratorListResponse is the Curator's review queue and decision history.
type CuratorListResponse struct {
	Suggestions []CuratorSuggestion `json:"suggestions"`
}

// CreatePersonRequest contains the Person fields accepted during resolution.
type CreatePersonRequest struct {
	DisplayName string `json:"display_name" mod:"trim" validate:"required,max=120"`
	SortName    string `json:"sort_name" mod:"trim" validate:"omitempty,max=120"`
}

// AcceptRequest selects exactly one existing or newly created Person.
type AcceptRequest struct {
	PersonID     string               `json:"person_id" validate:"omitempty,uuid"`
	CreatePerson *CreatePersonRequest `json:"create_person,omitempty"`
}

// Service persists suggestions without invoking Recipient access or Invitation services.
type Service struct {
	db     *bun.DB
	people *people.Service
	now    func() time.Time
	random io.Reader
}

func New(db *bun.DB, peopleService *people.Service) *Service {
	return &Service{db: db, people: peopleService, now: time.Now, random: rand.Reader}
}

func normalizeSubmission(request SubmitRequest) (SubmitRequest, string, error) {
	request.Name = strings.TrimSpace(request.Name)
	request.Email = strings.TrimSpace(request.Email)
	request.RelationshipContext = strings.TrimSpace(request.RelationshipContext)
	if request.SpokeWithPerson == nil || request.Name == "" || request.RelationshipContext == "" ||
		!utf8.ValidString(request.Name) || !utf8.ValidString(request.RelationshipContext) ||
		strings.ContainsRune(request.Name, '\x00') || strings.ContainsRune(request.RelationshipContext, '\x00') ||
		utf8.RuneCountInString(request.Name) > maxNameLength || utf8.RuneCountInString(request.RelationshipContext) > maxRelationshipLength {
		return SubmitRequest{}, "", ErrInvalidSuggestion
	}
	parsed, err := mail.ParseAddress(request.Email)
	if err != nil || parsed.Address != request.Email || len(request.Email) > 320 || strings.ContainsRune(request.Email, '\x00') {
		return SubmitRequest{}, "", ErrInvalidSuggestion
	}
	return request, strings.ToLower(parsed.Address), nil
}

// Submit records only free-form suggestion, activity, and audit data.
func (s *Service) Submit(ctx context.Context, actor setup.SessionActor, request SubmitRequest) (RequesterSuggestion, error) {
	request, normalizedEmail, err := normalizeSubmission(request)
	if err != nil {
		return RequesterSuggestion{}, err
	}
	id, err := uuid.NewRandomFromReader(s.random)
	if err != nil {
		return RequesterSuggestion{}, errGenerateID
	}
	now := s.now().UTC()
	var result RequesterSuggestion
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockPeople(ctx, tx, actor.PersonID); err != nil {
			return err
		}
		var current bool
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM recipient_access_generations
			WHERE id = ? AND person_id = ? AND is_current AND state = 'completed'
		)`, actor.AccessID, actor.PersonID).Scan(ctx, &current); err != nil {
			return err
		}
		if !current {
			return setup.ErrUnauthenticated
		}
		if _, err := tx.NewRaw(`
			INSERT INTO invitation_suggestions (
				id, requester_person_id, requester_access_generation_id, requester_session_id,
				name, email, normalized_email, relationship_context, spoke_with_person, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, id, actor.PersonID, actor.AccessID, actor.SessionID, request.Name, request.Email, normalizedEmail,
			request.RelationshipContext, *request.SpokeWithPerson, now, now).Exec(ctx); err != nil {
			return err
		}
		if err := appendActivity(ctx, tx, id, actor.PersonID, actor.PersonID, "invitation_suggestion_submitted", now); err != nil {
			return err
		}
		if err := appendAudit(ctx, tx, actor.PersonID, actor.PersonID, actor.SessionID, "invitation_suggestion_submitted", id, nil); err != nil {
			return err
		}
		result = RequesterSuggestion{
			ID: id.String(), Name: request.Name, Email: request.Email,
			RelationshipContext: request.RelationshipContext, SpokeWithPerson: *request.SpokeWithPerson,
			Status: "submitted", SubmittedAt: now,
		}
		return nil
	})
	return result, err
}

// ListRequester returns only suggestions owned by the authenticated Person.
func (s *Service) ListRequester(ctx context.Context, actor setup.SessionActor) (RequesterListResponse, error) {
	rows := make([]suggestionRow, 0)
	if err := s.db.NewRaw(`
		SELECT id, name, email, relationship_context, spoke_with_person, status, withdrawn_at, created_at
		FROM invitation_suggestions WHERE requester_person_id = ?
		ORDER BY created_at DESC, id DESC
	`, actor.PersonID).Scan(ctx, &rows); err != nil {
		return RequesterListResponse{}, err
	}
	result := RequesterListResponse{Suggestions: make([]RequesterSuggestion, len(rows))}
	for index, row := range rows {
		result.Suggestions[index] = row.requester()
	}
	return result, nil
}

// Withdraw serializes against Curator resolution and succeeds only while Submitted.
func (s *Service) Withdraw(ctx context.Context, actor setup.SessionActor, id uuid.UUID) (RequesterSuggestion, error) {
	var result RequesterSuggestion
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockPeople(ctx, tx, actor.PersonID); err != nil {
			return err
		}
		row, err := lockSuggestion(ctx, tx, id)
		if err != nil || row.RequesterPersonID != actor.PersonID {
			if errors.Is(err, sql.ErrNoRows) || err == nil {
				return ErrNotFound
			}
			return err
		}
		if row.Status != "submitted" || row.WithdrawnAt != nil {
			return ErrNotSubmitted
		}
		now := s.now().UTC()
		if _, err := tx.NewRaw(`UPDATE invitation_suggestions SET withdrawn_at = ?, updated_at = ? WHERE id = ?`, now, now, id).Exec(ctx); err != nil {
			return err
		}
		row.WithdrawnAt = &now
		if err := appendActivity(ctx, tx, id, actor.PersonID, actor.PersonID, "invitation_suggestion_withdrawn", now); err != nil {
			return err
		}
		if err := appendAudit(ctx, tx, actor.PersonID, actor.PersonID, actor.SessionID, "invitation_suggestion_withdrawn", id, nil); err != nil {
			return err
		}
		result = row.requester()
		return nil
	})
	return result, err
}

// ListCurator returns the review queue with advisory duplicate and Person matches.
func (s *Service) ListCurator(ctx context.Context, status string) (CuratorListResponse, error) {
	if status != "" && status != "submitted" && status != "accepted" && status != "rejected" && status != "withdrawn" {
		return CuratorListResponse{}, ErrInvalidSuggestion
	}
	rows := make([]suggestionRow, 0)
	query := `
		SELECT suggestion.id, suggestion.requester_person_id, requester.display_name AS requester_name,
		       suggestion.name, suggestion.email, suggestion.normalized_email, suggestion.relationship_context,
		       suggestion.spoke_with_person, suggestion.status, suggestion.withdrawn_at, suggestion.resolved_at,
		       COALESCE(suggestion.matched_person_id, '00000000-0000-0000-0000-000000000000'::uuid) AS matched_person_id,
		       COALESCE(matched.display_name, '') AS matched_person_name,
		       suggestion.created_at
		FROM invitation_suggestions AS suggestion
		JOIN people AS requester ON requester.id = suggestion.requester_person_id
		LEFT JOIN people AS matched ON matched.id = suggestion.matched_person_id
		WHERE (? = '' OR (? = 'withdrawn' AND suggestion.withdrawn_at IS NOT NULL) OR
		       (? <> 'withdrawn' AND suggestion.status = ? AND suggestion.withdrawn_at IS NULL))
		ORDER BY (suggestion.status = 'submitted' AND suggestion.withdrawn_at IS NULL) DESC,
		         suggestion.created_at DESC, suggestion.id DESC
		LIMIT 200
	`
	if err := s.db.NewRaw(query, status, status, status, status).Scan(ctx, &rows); err != nil {
		return CuratorListResponse{}, err
	}
	response := CuratorListResponse{Suggestions: make([]CuratorSuggestion, len(rows))}
	for index, row := range rows {
		item := row.curator()
		if err := s.db.NewRaw(`SELECT count(*) FROM invitation_suggestions WHERE normalized_email = ? AND id <> ?`, row.NormalizedEmail, row.ID).Scan(ctx, &item.DuplicateSuggestionCount); err != nil {
			return CuratorListResponse{}, err
		}
		matches := make([]struct {
			ID          uuid.UUID
			DisplayName string
			NameMatch   bool
			EmailMatch  bool
		}, 0)
		if err := s.db.NewRaw(`
			SELECT person.id, person.display_name,
			       memento_normalize_person_name(person.display_name) = memento_normalize_person_name(?) AS name_match,
			       EXISTS (
				   SELECT 1 FROM recipient_access_generations access
				   JOIN recipient_emails email ON email.recipient_access_generation_id = access.id AND email.is_current
				   WHERE access.person_id = person.id AND email.normalized_email = ?
			       ) AS email_match
			FROM people person
			WHERE person.archived_at IS NULL AND person.merged_at IS NULL AND (
				memento_normalize_person_name(person.display_name) = memento_normalize_person_name(?) OR EXISTS (
					SELECT 1 FROM recipient_access_generations access
					JOIN recipient_emails email ON email.recipient_access_generation_id = access.id AND email.is_current
					WHERE access.person_id = person.id AND email.normalized_email = ?
				)
			) ORDER BY person.sort_name, person.id
		`, row.Name, row.NormalizedEmail, row.Name, row.NormalizedEmail).Scan(ctx, &matches); err != nil {
			return CuratorListResponse{}, err
		}
		item.MatchingPeople = make([]PersonMatch, len(matches))
		for matchIndex, match := range matches {
			reasons := make([]string, 0, 2)
			if match.NameMatch {
				reasons = append(reasons, "same_name")
			}
			if match.EmailMatch {
				reasons = append(reasons, "same_recipient_email")
			}
			item.MatchingPeople[matchIndex] = PersonMatch{PersonID: match.ID.String(), DisplayName: match.DisplayName, Reasons: reasons}
		}
		response.Suggestions[index] = item
	}
	return response, nil
}

// Reject records the Curator decision without creating or matching a Person.
func (s *Service) Reject(ctx context.Context, actor setup.CuratorSession, id uuid.UUID) (CuratorSuggestion, error) {
	return s.resolve(ctx, actor, id, "rejected", uuid.Nil, nil)
}

// AcceptExisting explicitly matches a current Person without changing Recipient access.
func (s *Service) AcceptExisting(ctx context.Context, actor setup.CuratorSession, id, personID uuid.UUID) (CuratorSuggestion, error) {
	if personID == uuid.Nil {
		return CuratorSuggestion{}, ErrInvalidResolution
	}
	return s.resolve(ctx, actor, id, "accepted", personID, nil)
}

// AcceptNew explicitly creates a Person in the same transaction as acceptance.
func (s *Service) AcceptNew(ctx context.Context, actor setup.CuratorSession, id uuid.UUID, request people.CreateRequest) (CuratorSuggestion, error) {
	if s.people == nil {
		return CuratorSuggestion{}, ErrInvalidResolution
	}
	return s.resolve(ctx, actor, id, "accepted", uuid.Nil, &request)
}

func (s *Service) resolve(ctx context.Context, actor setup.CuratorSession, id uuid.UUID, status string, personID uuid.UUID, create *people.CreateRequest) (CuratorSuggestion, error) {
	var result CuratorSuggestion
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var requesterID uuid.UUID
		if err := tx.NewRaw(`SELECT requester_person_id FROM invitation_suggestions WHERE id = ?`, id).Scan(ctx, &requesterID); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		ids := []uuid.UUID{actor.PersonID, requesterID}
		if personID != uuid.Nil {
			ids = append(ids, personID)
		}
		if err := lockPeople(ctx, tx, ids...); err != nil {
			return err
		}
		row, err := lockSuggestion(ctx, tx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if row.Status != "submitted" || row.WithdrawnAt != nil {
			return ErrNotSubmitted
		}
		matchedID := personID
		matchedName := ""
		if create != nil {
			created, err := s.people.CreateIn(ctx, tx, actor, *create)
			if err != nil {
				return err
			}
			matchedID, err = uuid.Parse(created.ID)
			if err != nil {
				return err
			}
			matchedName = created.DisplayName
		} else if status == "accepted" {
			var archivedAt, mergedAt *time.Time
			if err := tx.NewRaw(`SELECT display_name, archived_at, merged_at FROM people WHERE id = ?`, matchedID).Scan(ctx, &matchedName, &archivedAt, &mergedAt); errors.Is(err, sql.ErrNoRows) {
				return ErrPersonUnavailable
			} else if err != nil {
				return err
			}
			if archivedAt != nil || mergedAt != nil {
				return ErrPersonUnavailable
			}
		}
		now := s.now().UTC()
		var matched any
		if status == "accepted" {
			matched = matchedID
		}
		if _, err := tx.NewRaw(`
			UPDATE invitation_suggestions SET status = ?, resolved_at = ?, resolved_by_person_id = ?, matched_person_id = ?, updated_at = ?
			WHERE id = ?
		`, status, now, actor.PersonID, matched, now, id).Exec(ctx); err != nil {
			return err
		}
		action := "invitation_suggestion_" + status
		if err := appendActivity(ctx, tx, id, row.RequesterPersonID, actor.PersonID, action, now); err != nil {
			return err
		}
		if err := appendAudit(ctx, tx, actor.PersonID, row.RequesterPersonID, actor.SessionID, action, id, matched); err != nil {
			return err
		}
		row.Status, row.ResolvedAt, row.MatchedPersonID, row.MatchedPersonName = status, &now, matchedID, matchedName
		result = row.curator()
		return nil
	})
	return result, err
}

type suggestionRow struct {
	ID                  uuid.UUID
	RequesterPersonID   uuid.UUID
	RequesterName       string
	Name                string
	Email               string
	NormalizedEmail     string
	RelationshipContext string
	SpokeWithPerson     bool
	Status              string
	WithdrawnAt         *time.Time
	ResolvedAt          *time.Time
	MatchedPersonID     uuid.UUID
	MatchedPersonName   string
	CreatedAt           time.Time
}

func (row suggestionRow) visibleStatus() string {
	if row.WithdrawnAt != nil {
		return "withdrawn"
	}
	return row.Status
}

func (row suggestionRow) requester() RequesterSuggestion {
	return RequesterSuggestion{
		ID: row.ID.String(), Name: row.Name, Email: row.Email, RelationshipContext: row.RelationshipContext,
		SpokeWithPerson: row.SpokeWithPerson, Status: row.visibleStatus(), SubmittedAt: row.CreatedAt,
	}
}

func (row suggestionRow) curator() CuratorSuggestion {
	result := CuratorSuggestion{
		ID: row.ID.String(), RequesterPersonID: row.RequesterPersonID.String(), RequesterName: row.RequesterName,
		Name: row.Name, Email: row.Email, RelationshipContext: row.RelationshipContext,
		SpokeWithPerson: row.SpokeWithPerson, Status: row.visibleStatus(), SubmittedAt: row.CreatedAt,
		ResolvedAt: row.ResolvedAt, MatchingPeople: []PersonMatch{},
	}
	if row.MatchedPersonID != uuid.Nil {
		result.MatchedPersonID = row.MatchedPersonID.String()
		result.MatchedPersonName = row.MatchedPersonName
	}
	return result
}

func lockSuggestion(ctx context.Context, tx bun.Tx, id uuid.UUID) (suggestionRow, error) {
	var row suggestionRow
	err := tx.NewRaw(`
		SELECT suggestion.id, suggestion.requester_person_id, requester.display_name, suggestion.name,
		       suggestion.email, suggestion.normalized_email, suggestion.relationship_context,
		       suggestion.spoke_with_person, suggestion.status, suggestion.withdrawn_at,
		       suggestion.resolved_at, suggestion.created_at
		FROM invitation_suggestions suggestion
		JOIN people requester ON requester.id = suggestion.requester_person_id
		WHERE suggestion.id = ? FOR UPDATE OF suggestion
	`, id).Scan(ctx, &row.ID, &row.RequesterPersonID, &row.RequesterName, &row.Name, &row.Email, &row.NormalizedEmail,
		&row.RelationshipContext, &row.SpokeWithPerson, &row.Status, &row.WithdrawnAt, &row.ResolvedAt, &row.CreatedAt)
	return row, err
}

func lockPeople(ctx context.Context, tx bun.Tx, ids ...uuid.UUID) error {
	_, err := tx.NewRaw(`SELECT id FROM people WHERE id IN (?) ORDER BY id FOR NO KEY UPDATE`, bun.List(ids)).Exec(ctx)
	return err
}

func appendActivity(ctx context.Context, tx bun.Tx, suggestionID, recipientID, actorID uuid.UUID, action string, now time.Time) error {
	if _, err := tx.NewRaw(`INSERT INTO recipient_activity_items (recipient_person_id, actor_person_id, invitation_suggestion_id, action, created_at) VALUES (?, ?, ?, ?, ?)`, recipientID, actorID, suggestionID, action, now).Exec(ctx); err != nil {
		return err
	}
	_, err := tx.NewRaw(`INSERT INTO curator_activity_items (actor_person_id, invitation_suggestion_id, action, created_at) VALUES (?, ?, ?, ?)`, actorID, suggestionID, action, now).Exec(ctx)
	return err
}

func appendAudit(ctx context.Context, tx bun.Tx, actorID, subjectID, sessionID uuid.UUID, action string, suggestionID uuid.UUID, matchedPerson any) error {
	metadata := map[string]any{"invitation_suggestion_id": suggestionID.String()}
	if matchedPerson != nil {
		metadata["matched_person_id"] = matchedPerson
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	request := setup.RequestMetadataFromContext(ctx)
	_, err = tx.NewRaw(`
		INSERT INTO security_audit_events (actor_person_id, subject_person_id, action, outcome, client_ip, user_agent, session_id, metadata)
		VALUES (?, ?, ?, 'success', NULLIF(?, '')::inet, ?, ?, ?::jsonb)
	`, actorID, subjectID, action, request.ClientIP, request.UserAgent, sessionID, string(encoded)).Exec(ctx)
	return err
}
