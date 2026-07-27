// Package visibility owns Visibility circles, Recipient discovery, and audited Interest lists.
package visibility

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/family"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/uptrace/bun"
)

var (
	ErrCircleNotFound    = errors.New("visibility circle not found")
	ErrPersonNotFound    = errors.New("person not found")
	ErrPersonUnavailable = errors.New("person is unavailable")
	ErrRecipientRequired = errors.New("interest list owner must be a Recipient")
	ErrNotAuthorized     = errors.New("visibility resource is not available to this actor")
	ErrSelfSelection     = errors.New("recipient cannot select their own Person")
	ErrNotDiscoverable   = errors.New("person is not discoverable")
	ErrInvalid           = errors.New("visibility request is invalid")
	ErrInvalidCursor     = errors.New("visibility cursor is invalid")
	ErrDuplicateName     = errors.New("visibility circle name already exists")
	ErrStale             = errors.New("visibility circle was changed by another request")
)

// Person is the only Person shape exposed by Recipient discovery APIs.
type Person struct {
	ID           string                  `json:"id"`
	DisplayName  string                  `json:"display_name"`
	SortName     string                  `json:"sort_name"`
	Relationship *RelationshipAnnotation `json:"relationship,omitempty"`
}

// RelationshipAnnotation explains a visible Person's Family connection without exposing intermediates.
type RelationshipAnnotation struct {
	ConnectionType string `json:"connection_type"`
	Generation     int    `json:"generation,omitempty"`
}

// Circle is a Curator-only Visibility circle and its direct members.
type Circle struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Version    int64      `json:"version"`
	Members    []Person   `json:"members"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
}

// CircleListResponse is generated to TypeScript by Tygo.
type CircleListResponse struct {
	Circles []Circle `json:"circles"`
}

// CircleRequest creates or renames a circle.
type CircleRequest struct {
	Name    string `json:"name" validate:"required,max=120"`
	Version int64  `json:"version,omitempty" validate:"omitempty,min=1"`
}

// CircleVersionRequest archives one exact circle version.
type CircleVersionRequest struct {
	Version int64 `json:"version" validate:"required,min=1"`
}

// MembershipRequest changes one direct circle membership.
type MembershipRequest struct {
	Included *bool `json:"included" validate:"required"`
	Version  int64 `json:"version" validate:"required,min=1"`
}

// DiscoveryResponse contains only the union visible to one Recipient.
type DiscoveryResponse struct {
	People     []Person `json:"people"`
	NextCursor *string  `json:"next_cursor,omitempty"`
}

// DiscoveryPageRequest selects one deterministic page of the Recipient directory.
type DiscoveryPageRequest struct {
	Cursor string
	Limit  int
}

type discoveryCursor struct {
	SortKey string    `json:"sort_key"`
	ID      uuid.UUID `json:"id"`
}

// InterestEntry is one retained choice and its current eligibility.
type InterestEntry struct {
	Person    Person    `json:"person"`
	State     string    `json:"state"`
	ChosenAt  time.Time `json:"chosen_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// InterestHistory records who requested each mutation and its resulting state.
type InterestHistory struct {
	ID        int64     `json:"id"`
	Person    Person    `json:"person"`
	Actor     Person    `json:"actor"`
	Action    string    `json:"action"`
	Result    string    `json:"result"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

// InterestListResponse is visible only to its Recipient or the Curator.
type InterestListResponse struct {
	Recipient         Person            `json:"recipient"`
	Entries           []InterestEntry   `json:"entries"`
	History           []InterestHistory `json:"history"`
	HistoryNextCursor *string           `json:"history_next_cursor,omitempty"`
}

// HistoryPageRequest selects one retained Interest history page.
type HistoryPageRequest struct {
	Cursor string
	Limit  int
}

type historyCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        int64     `json:"id"`
}

// InterestMutationRequest explicitly selects or deselects one Person.
type InterestMutationRequest struct {
	Selected *bool `json:"selected" validate:"required"`
}

// ProposalRecipient explains why one Eligible Recipient is proposed for Attendance.
type ProposalRecipient struct {
	Recipient      Person   `json:"recipient"`
	Reasons        []string `json:"reasons"`
	MatchingPeople []Person `json:"matching_people"`
}

type Service struct {
	db  *bun.DB
	now func() time.Time
}

func New(db *bun.DB) *Service { return &Service{db: db, now: time.Now} }

type circleRow struct {
	ID         uuid.UUID  `bun:"id"`
	Name       string     `bun:"name"`
	Version    int64      `bun:"version"`
	CreatedAt  time.Time  `bun:"created_at"`
	UpdatedAt  time.Time  `bun:"updated_at"`
	ArchivedAt *time.Time `bun:"archived_at"`
}

type personRow struct {
	ID          uuid.UUID `bun:"id"`
	DisplayName string    `bun:"display_name"`
	SortName    string    `bun:"sort_name"`
}

func (row personRow) person() Person {
	return Person{ID: row.ID.String(), DisplayName: row.DisplayName, SortName: row.SortName}
}

func normalizeCircleName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') || utf8.RuneCountInString(value) > 120 {
		return "", ErrInvalid
	}
	return value, nil
}

func lockVisibility(ctx context.Context, tx bun.Tx) error {
	_, err := tx.NewRaw(`SELECT pg_advisory_xact_lock(hashtextextended('memento:visibility', 0))`).Exec(ctx)
	return err
}

// LockReferences serializes Person merges with Visibility and Interest mutations.
func LockReferences(ctx context.Context, tx bun.Tx) error {
	return lockVisibility(ctx, tx)
}

func (s *Service) CreateCircle(ctx context.Context, actor setup.SessionActor, request CircleRequest) (Circle, error) {
	if !actor.Curator {
		return Circle{}, ErrNotAuthorized
	}
	name, err := normalizeCircleName(request.Name)
	if err != nil {
		return Circle{}, err
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return Circle{}, err
	}
	var circle Circle
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockVisibility(ctx, tx); err != nil {
			return err
		}
		var duplicate bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM visibility_circles WHERE lower(name) = lower(?) AND archived_at IS NULL)`, name).Scan(ctx, &duplicate); err != nil {
			return err
		}
		if duplicate {
			return ErrDuplicateName
		}
		now := s.now().UTC()
		if _, err := tx.NewRaw(`INSERT INTO visibility_circles (id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`, id, name, now, now).Exec(ctx); err != nil {
			return err
		}
		if err := appendAudit(ctx, tx, actor, actor.PersonID, "visibility_circle_created", map[string]any{"circle_id": id, "name": name}); err != nil {
			return err
		}
		circle, err = getCircle(ctx, tx, id)
		return err
	})
	return circle, err
}

func (s *Service) UpdateCircle(ctx context.Context, actor setup.SessionActor, id uuid.UUID, request CircleRequest) (Circle, error) {
	if !actor.Curator {
		return Circle{}, ErrNotAuthorized
	}
	name, err := normalizeCircleName(request.Name)
	if err != nil || request.Version < 1 {
		return Circle{}, ErrInvalid
	}
	var circle Circle
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockVisibility(ctx, tx); err != nil {
			return err
		}
		row, err := getCircleRow(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if row.ArchivedAt != nil || row.Version != request.Version {
			return ErrStale
		}
		var duplicate bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM visibility_circles WHERE lower(name) = lower(?) AND archived_at IS NULL AND id <> ?)`, name, id).Scan(ctx, &duplicate); err != nil {
			return err
		}
		if duplicate {
			return ErrDuplicateName
		}
		now := s.now().UTC()
		result, err := tx.NewRaw(`UPDATE visibility_circles SET name = ?, version = version + 1, updated_at = ? WHERE id = ? AND version = ? AND archived_at IS NULL`, name, now, id, request.Version).Exec(ctx)
		if err != nil {
			return err
		}
		if err := requireOne(result); err != nil {
			return err
		}
		if err := appendAudit(ctx, tx, actor, actor.PersonID, "visibility_circle_updated", map[string]any{"circle_id": id, "name": name, "previous_version": request.Version}); err != nil {
			return err
		}
		circle, err = getCircle(ctx, tx, id)
		return err
	})
	return circle, err
}

func (s *Service) ArchiveCircle(ctx context.Context, actor setup.SessionActor, id uuid.UUID, version int64) (Circle, error) {
	if !actor.Curator {
		return Circle{}, ErrNotAuthorized
	}
	if version < 1 {
		return Circle{}, ErrInvalid
	}
	var circle Circle
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockVisibility(ctx, tx); err != nil {
			return err
		}
		row, err := getCircleRow(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if row.ArchivedAt != nil || row.Version != version {
			return ErrStale
		}
		now := s.now().UTC()
		result, err := tx.NewRaw(`UPDATE visibility_circles SET archived_at = ?, version = version + 1, updated_at = ? WHERE id = ? AND version = ? AND archived_at IS NULL`, now, now, id, version).Exec(ctx)
		if err != nil {
			return err
		}
		if err := requireOne(result); err != nil {
			return err
		}
		if err := reconcileEligibility(ctx, tx, actor, now); err != nil {
			return err
		}
		if err := appendAudit(ctx, tx, actor, actor.PersonID, "visibility_circle_archived", map[string]any{"circle_id": id, "previous_version": version}); err != nil {
			return err
		}
		circle, err = getCircle(ctx, tx, id)
		return err
	})
	return circle, err
}

func (s *Service) SetMembership(ctx context.Context, actor setup.SessionActor, circleID, personID uuid.UUID, request MembershipRequest) (Circle, error) {
	if !actor.Curator {
		return Circle{}, ErrNotAuthorized
	}
	if request.Included == nil || request.Version < 1 {
		return Circle{}, ErrInvalid
	}
	var circle Circle
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockVisibility(ctx, tx); err != nil {
			return err
		}
		row, err := getCircleRow(ctx, tx, circleID, true)
		if err != nil {
			return err
		}
		if row.ArchivedAt != nil || row.Version != request.Version {
			return ErrStale
		}
		if err := requireCurrentPerson(ctx, tx, personID); err != nil {
			return err
		}
		now := s.now().UTC()
		if *request.Included {
			_, err = tx.NewRaw(`INSERT INTO visibility_circle_members (circle_id, person_id, created_at) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, circleID, personID, now).Exec(ctx)
		} else {
			_, err = tx.NewRaw(`DELETE FROM visibility_circle_members WHERE circle_id = ? AND person_id = ?`, circleID, personID).Exec(ctx)
		}
		if err != nil {
			return err
		}
		result, err := tx.NewRaw(`UPDATE visibility_circles SET version = version + 1, updated_at = ? WHERE id = ? AND version = ? AND archived_at IS NULL`, now, circleID, request.Version).Exec(ctx)
		if err != nil {
			return err
		}
		if err := requireOne(result); err != nil {
			return err
		}
		if !*request.Included {
			if err := reconcileEligibility(ctx, tx, actor, now); err != nil {
				return err
			}
		}
		action := "visibility_circle_member_removed"
		if *request.Included {
			action = "visibility_circle_member_added"
		}
		if err := appendAudit(ctx, tx, actor, personID, action, map[string]any{"circle_id": circleID}); err != nil {
			return err
		}
		circle, err = getCircle(ctx, tx, circleID)
		return err
	})
	return circle, err
}

func (s *Service) ListCircles(ctx context.Context, actor setup.SessionActor, includeArchived bool) (CircleListResponse, error) {
	if !actor.Curator {
		return CircleListResponse{}, ErrNotAuthorized
	}
	response := CircleListResponse{Circles: []Circle{}}
	err := s.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		rows := make([]circleRow, 0)
		if err := tx.NewRaw(`SELECT id, name, version, created_at, updated_at, archived_at FROM visibility_circles WHERE (? OR archived_at IS NULL) ORDER BY (archived_at IS NOT NULL), lower(name), id`, includeArchived).Scan(ctx, &rows); err != nil {
			return err
		}
		for _, row := range rows {
			circle, err := circleFromRow(ctx, tx, row)
			if err != nil {
				return err
			}
			response.Circles = append(response.Circles, circle)
		}
		return nil
	})
	return response, err
}

func getCircleRow(ctx context.Context, db bun.IDB, id uuid.UUID, lock bool) (circleRow, error) {
	query := `SELECT id, name, version, created_at, updated_at, archived_at FROM visibility_circles WHERE id = ?`
	if lock {
		query += ` FOR UPDATE`
	}
	var row circleRow
	if err := db.NewRaw(query, id).Scan(ctx, &row); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return circleRow{}, ErrCircleNotFound
		}
		return circleRow{}, err
	}
	return row, nil
}

func getCircle(ctx context.Context, db bun.IDB, id uuid.UUID) (Circle, error) {
	row, err := getCircleRow(ctx, db, id, false)
	if err != nil {
		return Circle{}, err
	}
	return circleFromRow(ctx, db, row)
}

func circleFromRow(ctx context.Context, db bun.IDB, row circleRow) (Circle, error) {
	members := make([]personRow, 0)
	if err := db.NewRaw(`SELECT person.id, person.display_name, person.sort_name FROM visibility_circle_members AS member JOIN people AS person ON person.id = member.person_id WHERE member.circle_id = ? AND person.archived_at IS NULL AND person.merged_at IS NULL ORDER BY memento_normalize_person_name(person.sort_name), person.id`, row.ID).Scan(ctx, &members); err != nil {
		return Circle{}, err
	}
	circle := Circle{ID: row.ID.String(), Name: row.Name, Version: row.Version, Members: []Person{}, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, ArchivedAt: row.ArchivedAt}
	for _, member := range members {
		circle.Members = append(circle.Members, member.person())
	}
	return circle, nil
}

func (s *Service) Discover(ctx context.Context, actor setup.SessionActor, recipientID uuid.UUID, page DiscoveryPageRequest) (DiscoveryResponse, error) {
	if !actor.Curator && actor.PersonID != recipientID {
		return DiscoveryResponse{}, ErrNotAuthorized
	}
	if err := requireRecipient(ctx, s.db, recipientID); err != nil {
		return DiscoveryResponse{}, err
	}
	if page.Limit == 0 {
		page.Limit = 100
	}
	if page.Limit < 1 || page.Limit > 200 {
		return DiscoveryResponse{}, ErrInvalid
	}
	cursor, hasCursor, err := decodeDiscoveryCursor(page.Cursor)
	if err != nil {
		return DiscoveryResponse{}, ErrInvalidCursor
	}
	type discoveryRow struct {
		personRow
		SortKey string `bun:"sort_key"`
	}
	rows := make([]discoveryRow, 0)
	if err := s.db.NewRaw(`SELECT person.id, person.display_name, person.sort_name, memento_normalize_person_name(person.sort_name) AS sort_key
		FROM visibility_circle_members AS own_membership
		JOIN visibility_circles AS circle ON circle.id = own_membership.circle_id AND circle.archived_at IS NULL
		JOIN visibility_circle_members AS visible_membership ON visible_membership.circle_id = circle.id
		JOIN people AS person ON person.id = visible_membership.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
		WHERE own_membership.person_id = ? AND person.id <> ?
		GROUP BY person.id, person.display_name, person.sort_name
		HAVING NOT ? OR (memento_normalize_person_name(person.sort_name), person.id) > (?, ?)
		ORDER BY memento_normalize_person_name(person.sort_name), person.id
		LIMIT ?`, recipientID, recipientID, hasCursor, cursor.SortKey, cursor.ID, page.Limit+1).Scan(ctx, &rows); err != nil {
		return DiscoveryResponse{}, err
	}
	annotations, err := s.relationshipAnnotations(ctx, recipientID)
	if err != nil {
		return DiscoveryResponse{}, err
	}
	response := DiscoveryResponse{People: []Person{}}
	if len(rows) > page.Limit {
		last := rows[page.Limit-1]
		next, err := encodeDiscoveryCursor(discoveryCursor{SortKey: last.SortKey, ID: last.ID})
		if err != nil {
			return DiscoveryResponse{}, err
		}
		response.NextCursor = &next
		rows = rows[:page.Limit]
	}
	for _, row := range rows {
		person := row.person()
		person.Relationship = annotations[row.ID]
		response.People = append(response.People, person)
	}
	return response, nil
}

func decodeDiscoveryCursor(value string) (discoveryCursor, bool, error) {
	if value == "" {
		return discoveryCursor{}, false, nil
	}
	encoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return discoveryCursor{}, false, err
	}
	var cursor discoveryCursor
	if err := json.Unmarshal(encoded, &cursor); err != nil || cursor.SortKey == "" || cursor.ID == uuid.Nil {
		return discoveryCursor{}, false, ErrInvalidCursor
	}
	return cursor, true, nil
}

func encodeDiscoveryCursor(cursor discoveryCursor) (string, error) {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func (s *Service) relationshipAnnotations(ctx context.Context, recipientID uuid.UUID) (map[uuid.UUID]*RelationshipAnnotation, error) {
	annotations := map[uuid.UUID]*RelationshipAnnotation{}
	familyService := family.New(s.db)
	branch, err := familyService.Branch(ctx, recipientID)
	if errors.Is(err, family.ErrPersonNotFound) {
		return nil, ErrPersonNotFound
	}
	if errors.Is(err, family.ErrPersonUnavailable) {
		return nil, ErrPersonUnavailable
	}
	if err != nil {
		return nil, err
	}
	for _, member := range branch.Members {
		personID, err := uuid.Parse(member.Person.ID)
		if err != nil {
			return nil, err
		}
		annotations[personID] = &RelationshipAnnotation{ConnectionType: member.ConnectionType, Generation: member.Generation}
	}
	relationships, err := familyService.List(ctx, false)
	if err != nil {
		return nil, err
	}
	for _, relationship := range relationships.Relationships {
		personAID := uuid.MustParse(relationship.PersonA.ID)
		personBID := uuid.MustParse(relationship.PersonB.ID)
		var otherID uuid.UUID
		var connection string
		switch {
		case personAID == recipientID:
			otherID = personBID
			switch relationship.Type {
			case "parent_child":
				connection = "child"
			case "sibling":
				connection = "sibling"
			case "partner":
				connection = relationship.PartnerStatus + "_partner"
			}
		case personBID == recipientID:
			otherID = personAID
			switch relationship.Type {
			case "parent_child":
				connection = "parent"
			case "sibling":
				connection = "sibling"
			case "partner":
				connection = relationship.PartnerStatus + "_partner"
			}
		}
		if connection != "" {
			annotations[otherID] = &RelationshipAnnotation{ConnectionType: connection}
		}
	}
	return annotations, nil
}

func (s *Service) InterestList(ctx context.Context, actor setup.SessionActor, recipientID uuid.UUID, page HistoryPageRequest) (InterestListResponse, error) {
	if !actor.Curator && actor.PersonID != recipientID {
		return InterestListResponse{}, ErrNotAuthorized
	}
	if err := requireRecipient(ctx, s.db, recipientID); err != nil {
		return InterestListResponse{}, err
	}
	if page.Limit == 0 {
		page.Limit = 50
	}
	if page.Limit < 1 || page.Limit > 200 {
		return InterestListResponse{}, ErrInvalid
	}
	cursor, hasCursor, err := decodeHistoryCursor(page.Cursor)
	if err != nil {
		return InterestListResponse{}, ErrInvalidCursor
	}
	recipient, err := loadPerson(ctx, s.db, recipientID)
	if err != nil {
		return InterestListResponse{}, err
	}
	response := InterestListResponse{Recipient: recipient, Entries: []InterestEntry{}, History: []InterestHistory{}}
	type entryRow struct {
		personRow
		State     string    `bun:"state"`
		ChosenAt  time.Time `bun:"chosen_at"`
		UpdatedAt time.Time `bun:"updated_at"`
	}
	entries := make([]entryRow, 0)
	if err := s.db.NewRaw(`SELECT person.id, person.display_name, person.sort_name, entry.state, entry.chosen_at, entry.updated_at FROM interest_list_entries AS entry JOIN people AS person ON person.id = entry.selected_person_id WHERE entry.recipient_person_id = ? ORDER BY (entry.state <> 'active'), memento_normalize_person_name(person.sort_name), person.id`, recipientID).Scan(ctx, &entries); err != nil {
		return InterestListResponse{}, err
	}
	for _, entry := range entries {
		response.Entries = append(response.Entries, InterestEntry{Person: entry.person(), State: entry.State, ChosenAt: entry.ChosenAt, UpdatedAt: entry.UpdatedAt})
	}
	type historyRow struct {
		ID               int64     `bun:"id"`
		SelectedID       uuid.UUID `bun:"selected_id"`
		SelectedName     string    `bun:"selected_name"`
		SelectedSortName string    `bun:"selected_sort_name"`
		ActorID          uuid.UUID `bun:"actor_id"`
		ActorName        string    `bun:"actor_name"`
		ActorSortName    string    `bun:"actor_sort_name"`
		Action           string    `bun:"action"`
		Result           string    `bun:"result"`
		Reason           string    `bun:"reason"`
		CreatedAt        time.Time `bun:"created_at"`
	}
	history := make([]historyRow, 0)
	if err := s.db.NewRaw(`SELECT history.id, selected.id AS selected_id, selected.display_name AS selected_name, selected.sort_name AS selected_sort_name, actor.id AS actor_id, actor.display_name AS actor_name, actor.sort_name AS actor_sort_name, history.action, history.result, history.reason, history.created_at FROM interest_list_history AS history JOIN people AS selected ON selected.id = history.selected_person_id JOIN people AS actor ON actor.id = history.actor_person_id WHERE history.recipient_person_id = ? AND (NOT ? OR (history.created_at, history.id) < (?, ?)) ORDER BY history.created_at DESC, history.id DESC LIMIT ?`, recipientID, hasCursor, cursor.CreatedAt, cursor.ID, page.Limit+1).Scan(ctx, &history); err != nil {
		return InterestListResponse{}, err
	}
	if len(history) > page.Limit {
		last := history[page.Limit-1]
		next, err := encodeHistoryCursor(historyCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		if err != nil {
			return InterestListResponse{}, err
		}
		response.HistoryNextCursor = &next
		history = history[:page.Limit]
	}
	for _, item := range history {
		response.History = append(response.History, InterestHistory{ID: item.ID, Person: Person{ID: item.SelectedID.String(), DisplayName: item.SelectedName, SortName: item.SelectedSortName}, Actor: Person{ID: item.ActorID.String(), DisplayName: item.ActorName, SortName: item.ActorSortName}, Action: item.Action, Result: item.Result, Reason: item.Reason, CreatedAt: item.CreatedAt})
	}
	return response, nil
}

func decodeHistoryCursor(value string) (historyCursor, bool, error) {
	if value == "" {
		return historyCursor{}, false, nil
	}
	encoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return historyCursor{}, false, err
	}
	var cursor historyCursor
	if err := json.Unmarshal(encoded, &cursor); err != nil || cursor.CreatedAt.IsZero() || cursor.ID < 1 {
		return historyCursor{}, false, ErrInvalidCursor
	}
	return cursor, true, nil
}

func encodeHistoryCursor(cursor historyCursor) (string, error) {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func (s *Service) MutateInterest(ctx context.Context, actor setup.SessionActor, recipientID, selectedID uuid.UUID, selected bool) (InterestListResponse, error) {
	if !actor.Curator && actor.PersonID != recipientID {
		return InterestListResponse{}, ErrNotAuthorized
	}
	if recipientID == selectedID {
		return InterestListResponse{}, ErrSelfSelection
	}
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockVisibility(ctx, tx); err != nil {
			return err
		}
		if err := requireRecipient(ctx, tx, recipientID); err != nil {
			return err
		}
		if selected {
			if !actor.Curator {
				eligible, err := sharedCircle(ctx, tx, recipientID, selectedID)
				if err != nil {
					return err
				}
				if !eligible {
					return ErrPersonNotFound
				}
			} else if err := requireCurrentPerson(ctx, tx, selectedID); err != nil {
				return err
			}
		} else {
			if !actor.Curator {
				var exists bool
				if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM interest_list_entries WHERE recipient_person_id = ? AND selected_person_id = ?)`, recipientID, selectedID).Scan(ctx, &exists); err != nil {
					return err
				}
				if !exists {
					return ErrPersonNotFound
				}
			}
			if err := requirePersonExists(ctx, tx, selectedID); err != nil {
				return err
			}
		}
		now := s.now().UTC()
		action, result := "deselected", "deselected"
		if selected {
			eligible, err := sharedCircle(ctx, tx, recipientID, selectedID)
			if err != nil {
				return err
			}
			if !eligible {
				return ErrNotDiscoverable
			}
			_, err = tx.NewRaw(`INSERT INTO interest_list_entries (recipient_person_id, selected_person_id, state, chosen_at, updated_at) VALUES (?, ?, 'active', ?, ?) ON CONFLICT (recipient_person_id, selected_person_id) DO UPDATE SET state = 'active', chosen_at = EXCLUDED.chosen_at, updated_at = EXCLUDED.updated_at`, recipientID, selectedID, now, now).Exec(ctx)
			action, result = "selected", "active"
			if err != nil {
				return err
			}
		} else if _, err := tx.NewRaw(`DELETE FROM interest_list_entries WHERE recipient_person_id = ? AND selected_person_id = ?`, recipientID, selectedID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO interest_list_history (recipient_person_id, selected_person_id, actor_person_id, action, result, reason, created_at) VALUES (?, ?, ?, ?, ?, 'explicit', ?)`, recipientID, selectedID, actor.PersonID, action, result, now).Exec(ctx); err != nil {
			return err
		}
		return appendAudit(ctx, tx, actor, recipientID, "interest_list_mutated", map[string]any{"selected_person_id": selectedID, "action": action, "result": result})
	})
	if err != nil {
		return InterestListResponse{}, err
	}
	return s.InterestList(ctx, actor, recipientID, HistoryPageRequest{})
}

func sharedCircle(ctx context.Context, db bun.IDB, recipientID, selectedID uuid.UUID) (bool, error) {
	var eligible bool
	err := db.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM visibility_circle_members AS own
		JOIN visibility_circles AS circle ON circle.id = own.circle_id AND circle.archived_at IS NULL
		JOIN visibility_circle_members AS selected ON selected.circle_id = circle.id
		JOIN people AS recipient_person ON recipient_person.id = own.person_id AND recipient_person.archived_at IS NULL AND recipient_person.merged_at IS NULL
		JOIN people AS selected_person ON selected_person.id = selected.person_id AND selected_person.archived_at IS NULL AND selected_person.merged_at IS NULL
		WHERE own.person_id = ? AND selected.person_id = ?
	)`, recipientID, selectedID).Scan(ctx, &eligible)
	return eligible, err
}

func reconcileEligibility(ctx context.Context, tx bun.Tx, actor setup.SessionActor, now time.Time) error {
	type deactivatedRow struct {
		RecipientID uuid.UUID `bun:"recipient_person_id"`
		SelectedID  uuid.UUID `bun:"selected_person_id"`
	}
	rows := make([]deactivatedRow, 0)
	if err := tx.NewRaw(`UPDATE interest_list_entries AS entry SET state = 'ineligible', updated_at = ? WHERE entry.state = 'active' AND NOT EXISTS (
		SELECT 1 FROM visibility_circle_members AS own
		JOIN visibility_circles AS circle ON circle.id = own.circle_id AND circle.archived_at IS NULL
		JOIN visibility_circle_members AS selected ON selected.circle_id = circle.id
		JOIN people AS recipient_person ON recipient_person.id = own.person_id AND recipient_person.archived_at IS NULL AND recipient_person.merged_at IS NULL
		JOIN people AS selected_person ON selected_person.id = selected.person_id AND selected_person.archived_at IS NULL AND selected_person.merged_at IS NULL
		WHERE own.person_id = entry.recipient_person_id AND selected.person_id = entry.selected_person_id
	) RETURNING recipient_person_id, selected_person_id`, now).Scan(ctx, &rows); err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := tx.NewRaw(`INSERT INTO interest_list_history (recipient_person_id, selected_person_id, actor_person_id, action, result, reason, created_at) VALUES (?, ?, ?, 'deactivated', 'ineligible', 'visibility_lost', ?)`, row.RecipientID, row.SelectedID, actor.PersonID, now).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

// ArchivePersonReferences removes an unavailable Person from circles and deactivates newly ineligible choices.
func ArchivePersonReferences(ctx context.Context, tx bun.Tx, personID uuid.UUID, actor setup.SessionActor, now time.Time) error {
	if err := lockVisibility(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.NewRaw(`UPDATE visibility_circles SET version = version + 1, updated_at = ? WHERE id IN (SELECT circle_id FROM visibility_circle_members WHERE person_id = ?)`, now, personID).Exec(ctx); err != nil {
		return err
	}
	if _, err := tx.NewRaw(`DELETE FROM visibility_circle_members WHERE person_id = ?`, personID).Exec(ctx); err != nil {
		return err
	}
	return reconcileEligibility(ctx, tx, actor, now)
}

// PersonMergeEffects summarizes Visibility and Interest references affected by a Person merge.
type PersonMergeEffects struct {
	CircleMembershipsMoved     int
	InterestEntriesMoved       int
	InterestHistoryOwnersMoved int
	ReferenceFingerprint       string
}

// PreviewPersonMerge reports every current Visibility and Interest reference affected by a merge.
func PreviewPersonMerge(ctx context.Context, db bun.IDB, sourceID, survivorID uuid.UUID) (PersonMergeEffects, error) {
	type circleReference struct {
		CircleID  uuid.UUID `bun:"circle_id"`
		PersonID  uuid.UUID `bun:"person_id"`
		CreatedAt time.Time `bun:"created_at"`
	}
	type entryReference struct {
		RecipientID uuid.UUID `bun:"recipient_person_id"`
		SelectedID  uuid.UUID `bun:"selected_person_id"`
		State       string    `bun:"state"`
		ChosenAt    time.Time `bun:"chosen_at"`
		UpdatedAt   time.Time `bun:"updated_at"`
	}
	type historyReference struct {
		ID          int64     `bun:"id"`
		RecipientID uuid.UUID `bun:"recipient_person_id"`
		SelectedID  uuid.UUID `bun:"selected_person_id"`
		ActorID     uuid.UUID `bun:"actor_person_id"`
		Action      string    `bun:"action"`
		Result      string    `bun:"result"`
		Reason      string    `bun:"reason"`
		CreatedAt   time.Time `bun:"created_at"`
	}
	circles := make([]circleReference, 0)
	if err := db.NewRaw(`SELECT circle_id, person_id, created_at FROM visibility_circle_members WHERE person_id IN (?, ?) ORDER BY circle_id, person_id`, sourceID, survivorID).Scan(ctx, &circles); err != nil {
		return PersonMergeEffects{}, err
	}
	entries := make([]entryReference, 0)
	if err := db.NewRaw(`SELECT recipient_person_id, selected_person_id, state, chosen_at, updated_at FROM interest_list_entries WHERE recipient_person_id IN (?, ?) OR selected_person_id IN (?, ?) ORDER BY recipient_person_id, selected_person_id`, sourceID, survivorID, sourceID, survivorID).Scan(ctx, &entries); err != nil {
		return PersonMergeEffects{}, err
	}
	history := make([]historyReference, 0)
	if err := db.NewRaw(`SELECT id, recipient_person_id, selected_person_id, actor_person_id, action, result, reason, created_at FROM interest_list_history WHERE recipient_person_id IN (?, ?) ORDER BY id`, sourceID, survivorID).Scan(ctx, &history); err != nil {
		return PersonMergeEffects{}, err
	}
	effects := PersonMergeEffects{}
	for _, reference := range circles {
		if reference.PersonID == sourceID {
			effects.CircleMembershipsMoved++
		}
	}
	for _, reference := range entries {
		if reference.RecipientID == sourceID || reference.SelectedID == sourceID {
			effects.InterestEntriesMoved++
		}
	}
	for _, reference := range history {
		if reference.RecipientID == sourceID {
			effects.InterestHistoryOwnersMoved++
		}
	}
	encoded, err := json.Marshal(struct {
		Circles []circleReference  `json:"circles"`
		Entries []entryReference   `json:"entries"`
		History []historyReference `json:"history"`
	}{Circles: circles, Entries: entries, History: history})
	if err != nil {
		return PersonMergeEffects{}, err
	}
	digest := sha256.Sum256(encoded)
	effects.ReferenceFingerprint = hex.EncodeToString(digest[:])
	return effects, nil
}

// MergePersonReferences moves current Visibility and Interest references to a surviving Person.
// Historical selected People and actors keep their original identities for attribution.
func MergePersonReferences(ctx context.Context, tx bun.Tx, sourceID, survivorID uuid.UUID, actor setup.SessionActor, now time.Time) error {
	if sourceID == survivorID {
		return ErrInvalid
	}
	if err := lockVisibility(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.NewRaw(`UPDATE visibility_circles SET version = version + 1, updated_at = ? WHERE id IN (SELECT circle_id FROM visibility_circle_members WHERE person_id = ?)`, now, sourceID).Exec(ctx); err != nil {
		return err
	}
	if _, err := tx.NewRaw(`INSERT INTO visibility_circle_members (circle_id, person_id, created_at) SELECT circle_id, ?, created_at FROM visibility_circle_members WHERE person_id = ? ON CONFLICT DO NOTHING`, survivorID, sourceID).Exec(ctx); err != nil {
		return err
	}
	if _, err := tx.NewRaw(`DELETE FROM visibility_circle_members WHERE person_id = ?`, sourceID).Exec(ctx); err != nil {
		return err
	}
	if _, err := tx.NewRaw(`UPDATE interest_list_history SET recipient_person_id = ? WHERE recipient_person_id = ?`, survivorID, sourceID).Exec(ctx); err != nil {
		return err
	}
	type mergeEntryRow struct {
		RecipientID uuid.UUID `bun:"recipient_person_id"`
		SelectedID  uuid.UUID `bun:"selected_person_id"`
		State       string    `bun:"state"`
		ChosenAt    time.Time `bun:"chosen_at"`
	}
	entries := make([]mergeEntryRow, 0)
	if err := tx.NewRaw(`SELECT recipient_person_id, selected_person_id, state, chosen_at FROM interest_list_entries WHERE recipient_person_id = ? OR selected_person_id = ? ORDER BY recipient_person_id, selected_person_id FOR UPDATE`, sourceID, sourceID).Scan(ctx, &entries); err != nil {
		return err
	}
	if _, err := tx.NewRaw(`DELETE FROM interest_list_entries WHERE recipient_person_id = ? OR selected_person_id = ?`, sourceID, sourceID).Exec(ctx); err != nil {
		return err
	}
	for _, entry := range entries {
		recipientID, selectedID := entry.RecipientID, entry.SelectedID
		if recipientID == sourceID {
			recipientID = survivorID
		}
		if selectedID == sourceID {
			selectedID = survivorID
		}
		result := "deselected"
		if recipientID != selectedID {
			state := entry.State
			if state == "active" {
				eligible, err := sharedCircle(ctx, tx, recipientID, selectedID)
				if err != nil {
					return err
				}
				if !eligible {
					state = "ineligible"
				}
			}
			if _, err := tx.NewRaw(`INSERT INTO interest_list_entries (recipient_person_id, selected_person_id, state, chosen_at, updated_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT (recipient_person_id, selected_person_id) DO UPDATE SET state = CASE WHEN interest_list_entries.state = 'active' OR EXCLUDED.state = 'active' THEN 'active' ELSE 'ineligible' END, chosen_at = GREATEST(interest_list_entries.chosen_at, EXCLUDED.chosen_at), updated_at = EXCLUDED.updated_at`, recipientID, selectedID, state, entry.ChosenAt, now).Exec(ctx); err != nil {
				return err
			}
			if err := tx.NewRaw(`SELECT state FROM interest_list_entries WHERE recipient_person_id = ? AND selected_person_id = ?`, recipientID, selectedID).Scan(ctx, &result); err != nil {
				return err
			}
		}
		if _, err := tx.NewRaw(`INSERT INTO interest_list_history (recipient_person_id, selected_person_id, actor_person_id, action, result, reason, created_at) VALUES (?, ?, ?, 'moved', ?, 'person_merged', ?)`, recipientID, selectedID, actor.PersonID, result, now).Exec(ctx); err != nil {
			return err
		}
	}
	return reconcileEligibility(ctx, tx, actor, now)
}

// ProposeRecipients returns proposal inputs only; it does not create Audience authority.
func (s *Service) ProposeRecipients(ctx context.Context, actor setup.SessionActor, attendanceIDs []uuid.UUID) ([]ProposalRecipient, error) {
	if !actor.Curator {
		return nil, ErrNotAuthorized
	}
	if len(attendanceIDs) == 0 {
		return []ProposalRecipient{}, nil
	}
	type proposalRow struct {
		RecipientID   uuid.UUID `bun:"recipient_id"`
		RecipientName string    `bun:"recipient_name"`
		RecipientSort string    `bun:"recipient_sort"`
		MatchingID    uuid.UUID `bun:"matching_id"`
		MatchingName  string    `bun:"matching_name"`
		MatchingSort  string    `bun:"matching_sort"`
		Reason        string    `bun:"reason"`
	}
	rows := make([]proposalRow, 0)
	if err := s.db.NewRaw(`WITH attendance(person_id) AS (SELECT id FROM people WHERE id IN (?)), candidates AS (
		SELECT person.id AS recipient_id, person.display_name AS recipient_name, person.sort_name AS recipient_sort, person.id AS matching_id, person.display_name AS matching_name, person.sort_name AS matching_sort, 'present'::text AS reason
		FROM people AS person JOIN attendance ON attendance.person_id = person.id
		UNION ALL
		SELECT recipient.id, recipient.display_name, recipient.sort_name, selected.id, selected.display_name, selected.sort_name, 'interested'::text
		FROM interest_list_entries AS entry JOIN attendance ON attendance.person_id = entry.selected_person_id JOIN people AS recipient ON recipient.id = entry.recipient_person_id JOIN people AS selected ON selected.id = entry.selected_person_id WHERE entry.state = 'active'
	) SELECT candidate.* FROM candidates AS candidate
	JOIN recipient_access_generations AS access ON access.person_id = candidate.recipient_id AND access.is_current AND access.state IN ('pending', 'onboarding', 'completed')
	JOIN person_roles AS role ON role.person_id = candidate.recipient_id AND role.role = 'recipient'
	WHERE NOT EXISTS (SELECT 1 FROM person_roles AS curator WHERE curator.person_id = candidate.recipient_id AND curator.role = 'curator')
	ORDER BY memento_normalize_person_name(candidate.recipient_sort), candidate.recipient_id, CASE candidate.reason WHEN 'present' THEN 0 ELSE 1 END, memento_normalize_person_name(candidate.matching_sort), candidate.matching_id`, bun.List(attendanceIDs)).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	proposals := make([]ProposalRecipient, 0)
	index := map[uuid.UUID]int{}
	for _, row := range rows {
		i, exists := index[row.RecipientID]
		if !exists {
			i = len(proposals)
			index[row.RecipientID] = i
			proposals = append(proposals, ProposalRecipient{Recipient: Person{ID: row.RecipientID.String(), DisplayName: row.RecipientName, SortName: row.RecipientSort}, Reasons: []string{}, MatchingPeople: []Person{}})
		}
		if len(proposals[i].Reasons) == 0 || proposals[i].Reasons[len(proposals[i].Reasons)-1] != row.Reason {
			proposals[i].Reasons = append(proposals[i].Reasons, row.Reason)
		}
		proposals[i].MatchingPeople = append(proposals[i].MatchingPeople, Person{ID: row.MatchingID.String(), DisplayName: row.MatchingName, SortName: row.MatchingSort})
	}
	return proposals, nil
}

func requirePersonExists(ctx context.Context, db bun.IDB, id uuid.UUID) error {
	var exists bool
	if err := db.NewRaw(`SELECT EXISTS (SELECT 1 FROM people WHERE id = ?)`, id).Scan(ctx, &exists); err != nil {
		return err
	}
	if !exists {
		return ErrPersonNotFound
	}
	return nil
}

func requireCurrentPerson(ctx context.Context, db bun.IDB, id uuid.UUID) error {
	var current, exists bool
	if err := db.NewRaw(`SELECT EXISTS (SELECT 1 FROM people WHERE id = ?), EXISTS (SELECT 1 FROM people WHERE id = ? AND archived_at IS NULL AND merged_at IS NULL)`, id, id).Scan(ctx, &exists, &current); err != nil {
		return err
	}
	if !exists {
		return ErrPersonNotFound
	}
	if !current {
		return ErrPersonUnavailable
	}
	return nil
}

func requireRecipient(ctx context.Context, db bun.IDB, id uuid.UUID) error {
	if err := requireCurrentPerson(ctx, db, id); err != nil {
		return err
	}
	var recipient bool
	if err := db.NewRaw(`SELECT EXISTS (SELECT 1 FROM person_roles WHERE person_id = ? AND role = 'recipient')`, id).Scan(ctx, &recipient); err != nil {
		return err
	}
	if !recipient {
		return ErrRecipientRequired
	}
	return nil
}

func loadPerson(ctx context.Context, db bun.IDB, id uuid.UUID) (Person, error) {
	var row personRow
	if err := db.NewRaw(`SELECT id, display_name, sort_name FROM people WHERE id = ?`, id).Scan(ctx, &row); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Person{}, ErrPersonNotFound
		}
		return Person{}, err
	}
	return row.person(), nil
}

func requireOne(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrStale
	}
	return nil
}

func appendAudit(ctx context.Context, tx bun.Tx, actor setup.SessionActor, subject uuid.UUID, action string, metadata map[string]any) error {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	request := setup.RequestMetadataFromContext(ctx)
	_, err = tx.NewRaw(`INSERT INTO security_audit_events (actor_person_id, subject_person_id, action, outcome, client_ip, user_agent, session_id, metadata) VALUES (?, ?, ?, 'success', NULLIF(?, '')::inet, ?, ?, ?::jsonb)`, actor.PersonID, subject, action, request.ClientIP, request.UserAgent, actor.SessionID, string(encoded)).Exec(ctx)
	return err
}
