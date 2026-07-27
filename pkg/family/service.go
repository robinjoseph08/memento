// Package family owns explicit Family relationships and derived Family branches.
package family

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/uptrace/bun"
)

var (
	ErrNotFound             = errors.New("family relationship not found")
	ErrPersonNotFound       = errors.New("person not found")
	ErrPersonUnavailable    = errors.New("family relationships require current People")
	ErrInvalid              = errors.New("family relationship is invalid")
	ErrDuplicate            = errors.New("family relationship already exists")
	ErrCycle                = errors.New("parent-child relationship would create a cycle")
	ErrMergeCycle           = errors.New("merging these People would collapse a parent-child path into a cycle")
	ErrMergePartnerConflict = errors.New("merging these People would collapse current and former partner relationships")
	ErrStale                = errors.New("family relationship was changed by another request")
)

// Person identifies one endpoint without implying access.
type Person struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	SortName    string `json:"sort_name"`
	Status      string `json:"status"`
}

// Relationship is one explicit connection maintained by the Curator.
type Relationship struct {
	ID            string     `json:"id"`
	Type          string     `json:"relationship_type"`
	PersonA       Person     `json:"person_a"`
	PersonB       Person     `json:"person_b"`
	PartnerStatus string     `json:"partner_status,omitempty"`
	Version       int64      `json:"version"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	ArchivedAt    *time.Time `json:"archived_at,omitempty"`
}

// ListResponse is generated to TypeScript by Tygo.
type ListResponse struct {
	Relationships []Relationship `json:"relationships"`
}

// MutationRequest creates or replaces the editable fields of a relationship.
type MutationRequest struct {
	RelationshipType string `json:"relationship_type" validate:"required,oneof=parent_child sibling partner"`
	PersonAID        string `json:"person_a_id" validate:"required,uuid"`
	PersonBID        string `json:"person_b_id" validate:"required,uuid"`
	PartnerStatus    string `json:"partner_status" validate:"omitempty,oneof=current former"`
	Version          int64  `json:"version,omitempty" validate:"omitempty,min=1"`
}

// VersionRequest archives one exact relationship version.
type VersionRequest struct {
	Version int64 `json:"version" validate:"required,min=1"`
}

// BranchMember describes why a Person appears in the derived Family branch.
type BranchMember struct {
	Person         Person `json:"person"`
	ConnectionType string `json:"connection_type"`
	Generation     int    `json:"generation"`
}

// BranchResponse is a deterministic, relationship-annotated Family branch.
type BranchResponse struct {
	Root    Person         `json:"root"`
	Members []BranchMember `json:"members"`
}

// PersonMergeEffects summarizes Family references affected by a Person merge.
type PersonMergeEffects struct {
	RelationshipsMoved    int
	RelationshipsArchived int
	ReferenceFingerprint  string
}

// Service maintains the graph transactionally.
type Service struct {
	db  *bun.DB
	now func() time.Time
}

func New(db *bun.DB) *Service { return &Service{db: db, now: time.Now} }

type relationshipRow struct {
	ID            uuid.UUID  `bun:"id"`
	Type          string     `bun:"relationship_type"`
	PersonAID     uuid.UUID  `bun:"person_a_id"`
	PersonBID     uuid.UUID  `bun:"person_b_id"`
	PartnerStatus *string    `bun:"partner_status"`
	Version       int64      `bun:"version"`
	CreatedAt     time.Time  `bun:"created_at"`
	UpdatedAt     time.Time  `bun:"updated_at"`
	ArchivedAt    *time.Time `bun:"archived_at"`
}

type personRow struct {
	ID                 uuid.UUID     `bun:"id"`
	DisplayName        string        `bun:"display_name"`
	SortName           string        `bun:"sort_name"`
	ArchivedAt         *time.Time    `bun:"archived_at"`
	MergedAt           *time.Time    `bun:"merged_at"`
	MergedIntoPersonID uuid.NullUUID `bun:"merged_into_person_id"`
}

func (row personRow) person() Person {
	status := "current"
	if row.MergedAt != nil {
		status = "merged"
	} else if row.ArchivedAt != nil {
		status = "archived"
	}
	return Person{ID: row.ID.String(), DisplayName: row.DisplayName, SortName: row.SortName, Status: status}
}

func normalize(request MutationRequest) (MutationRequest, uuid.UUID, uuid.UUID, error) {
	personA, err := uuid.Parse(request.PersonAID)
	if err != nil {
		return MutationRequest{}, uuid.Nil, uuid.Nil, ErrInvalid
	}
	personB, err := uuid.Parse(request.PersonBID)
	if err != nil || personA == personB {
		return MutationRequest{}, uuid.Nil, uuid.Nil, ErrInvalid
	}
	switch request.RelationshipType {
	case "parent_child", "sibling":
		if request.PartnerStatus != "" {
			return MutationRequest{}, uuid.Nil, uuid.Nil, ErrInvalid
		}
	case "partner":
		if request.PartnerStatus != "current" && request.PartnerStatus != "former" {
			return MutationRequest{}, uuid.Nil, uuid.Nil, ErrInvalid
		}
	default:
		return MutationRequest{}, uuid.Nil, uuid.Nil, ErrInvalid
	}
	if request.RelationshipType != "parent_child" && bytes.Compare(personA[:], personB[:]) > 0 {
		personA, personB = personB, personA
	}
	request.PersonAID = personA.String()
	request.PersonBID = personB.String()
	return request, personA, personB, nil
}

func (s *Service) Create(ctx context.Context, actor setup.CuratorSession, request MutationRequest) (Relationship, error) {
	request, personA, personB, err := normalize(request)
	if err != nil {
		return Relationship{}, err
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return Relationship{}, err
	}
	var result Relationship
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockFamilyGraph(ctx, tx); err != nil {
			return err
		}
		if err := validateMutation(ctx, tx, uuid.Nil, request, personA, personB); err != nil {
			return err
		}
		now := s.now().UTC()
		var partnerStatus any
		if request.RelationshipType == "partner" {
			partnerStatus = request.PartnerStatus
		}
		if _, err := tx.NewRaw(`INSERT INTO family_relationships (id, relationship_type, person_a_id, person_b_id, partner_status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, id, request.RelationshipType, personA, personB, partnerStatus, now, now).Exec(ctx); err != nil {
			return err
		}
		if err := appendAudit(ctx, tx, actor, personA, "family_relationship_created", id, map[string]any{"relationship_type": request.RelationshipType, "person_a_id": personA, "person_b_id": personB, "partner_status": request.PartnerStatus}); err != nil {
			return err
		}
		result, err = getRelationship(ctx, tx, id, false)
		return err
	})
	return result, err
}

func (s *Service) Update(ctx context.Context, actor setup.CuratorSession, id uuid.UUID, request MutationRequest) (Relationship, error) {
	request, personA, personB, err := normalize(request)
	if err != nil || request.Version < 1 {
		return Relationship{}, ErrInvalid
	}
	var result Relationship
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockFamilyGraph(ctx, tx); err != nil {
			return err
		}
		var currentVersion int64
		err := tx.NewRaw(`SELECT version FROM family_relationships WHERE id = ? AND archived_at IS NULL FOR UPDATE`, id).Scan(ctx, &currentVersion)
		if errors.Is(err, sql.ErrNoRows) {
			return relationshipStaleOrNotFound(ctx, tx, id)
		}
		if err != nil {
			return err
		}
		if currentVersion != request.Version {
			return ErrStale
		}
		if err := validateMutation(ctx, tx, id, request, personA, personB); err != nil {
			return err
		}
		now := s.now().UTC()
		var partnerStatus any
		if request.RelationshipType == "partner" {
			partnerStatus = request.PartnerStatus
		}
		updateResult, err := tx.NewRaw(`UPDATE family_relationships SET relationship_type = ?, person_a_id = ?, person_b_id = ?, partner_status = ?, version = version + 1, updated_at = ? WHERE id = ? AND version = ? AND archived_at IS NULL`, request.RelationshipType, personA, personB, partnerStatus, now, id, request.Version).Exec(ctx)
		if err != nil {
			return err
		}
		affected, err := updateResult.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return ErrStale
		}
		if err := appendAudit(ctx, tx, actor, personA, "family_relationship_updated", id, map[string]any{"previous_version": request.Version, "relationship_type": request.RelationshipType, "person_a_id": personA, "person_b_id": personB, "partner_status": request.PartnerStatus}); err != nil {
			return err
		}
		result, err = getRelationship(ctx, tx, id, false)
		return err
	})
	return result, err
}

func (s *Service) Archive(ctx context.Context, actor setup.CuratorSession, id uuid.UUID, version int64) (Relationship, error) {
	if version < 1 {
		return Relationship{}, ErrInvalid
	}
	var archived Relationship
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockFamilyGraph(ctx, tx); err != nil {
			return err
		}
		row, err := getRelationshipRow(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if row.ArchivedAt != nil || row.Version != version {
			return ErrStale
		}
		now := s.now().UTC()
		result, err := tx.NewRaw(`UPDATE family_relationships SET archived_at = ?, version = version + 1, updated_at = ? WHERE id = ? AND version = ? AND archived_at IS NULL`, now, now, id, version).Exec(ctx)
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
		if err := appendAudit(ctx, tx, actor, row.PersonAID, "family_relationship_archived", id, map[string]any{"previous_version": version}); err != nil {
			return err
		}
		archived, err = getRelationship(ctx, tx, id, false)
		return err
	})
	return archived, err
}

func lockFamilyGraph(ctx context.Context, tx bun.Tx) error {
	_, err := tx.NewRaw(`SELECT pg_advisory_xact_lock(hashtextextended('memento:family-graph', 0))`).Exec(ctx)
	return err
}

// LockGraph serializes Person merges with Family graph mutations.
func LockGraph(ctx context.Context, tx bun.Tx) error {
	return lockFamilyGraph(ctx, tx)
}

// PreviewPersonMerge reports how Family references would move and whether the
// selected identities are connected by an active parent-child path.
func PreviewPersonMerge(ctx context.Context, db bun.IDB, sourceID, survivorID uuid.UUID) (PersonMergeEffects, error) {
	if sourceID == survivorID {
		return PersonMergeEffects{}, ErrInvalid
	}
	connected, err := parentPathExists(ctx, db, sourceID, survivorID)
	if err != nil {
		return PersonMergeEffects{}, err
	}
	if !connected {
		connected, err = parentPathExists(ctx, db, survivorID, sourceID)
		if err != nil {
			return PersonMergeEffects{}, err
		}
	}
	rows := make([]relationshipRow, 0)
	if err := db.NewRaw(`SELECT id, relationship_type, person_a_id, person_b_id, partner_status, version, created_at, updated_at, archived_at FROM family_relationships WHERE person_a_id IN (?, ?) OR person_b_id IN (?, ?) ORDER BY id`, sourceID, survivorID, sourceID, survivorID).Scan(ctx, &rows); err != nil {
		return PersonMergeEffects{}, err
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		return PersonMergeEffects{}, err
	}
	digest := sha256.Sum256(encoded)
	effects := PersonMergeEffects{ReferenceFingerprint: hex.EncodeToString(digest[:])}
	partnerConflict := false
	for _, row := range rows {
		if row.PersonAID != sourceID && row.PersonBID != sourceID {
			continue
		}
		personA, personB := mergedEndpoints(row, sourceID, survivorID)
		if personA == personB {
			if row.ArchivedAt == nil {
				effects.RelationshipsArchived++
			}
			continue
		}
		duplicate, duplicateExists, err := activeDuplicate(ctx, db, row.ID, row.Type, personA, personB)
		if err != nil {
			return PersonMergeEffects{}, err
		}
		if row.ArchivedAt == nil && duplicateExists {
			effects.RelationshipsArchived++
			if row.Type == "partner" && !samePartnerStatus(row.PartnerStatus, duplicate.PartnerStatus) {
				partnerConflict = true
			}
		}
		effects.RelationshipsMoved++
	}
	var conflicts []error
	if connected {
		conflicts = append(conflicts, ErrMergeCycle)
	}
	if partnerConflict {
		conflicts = append(conflicts, ErrMergePartnerConflict)
	}
	return effects, errors.Join(conflicts...)
}

// MergePersonReferences moves Family relationship endpoints to the survivor,
// archiving active rows that would become self-connections or duplicates.
func MergePersonReferences(ctx context.Context, tx bun.Tx, sourceID, survivorID uuid.UUID, now time.Time) (PersonMergeEffects, error) {
	effects, err := PreviewPersonMerge(ctx, tx, sourceID, survivorID)
	if err != nil {
		return PersonMergeEffects{}, err
	}
	rows := make([]relationshipRow, 0)
	if err := tx.NewRaw(`SELECT id, relationship_type, person_a_id, person_b_id, partner_status, version, created_at, updated_at, archived_at FROM family_relationships WHERE person_a_id = ? OR person_b_id = ? ORDER BY id FOR UPDATE`, sourceID, sourceID).Scan(ctx, &rows); err != nil {
		return PersonMergeEffects{}, err
	}
	for _, row := range rows {
		personA, personB := mergedEndpoints(row, sourceID, survivorID)
		if personA == personB {
			if row.ArchivedAt == nil {
				if _, err := tx.NewRaw(`UPDATE family_relationships SET archived_at = ?, version = version + 1, updated_at = ? WHERE id = ?`, now, now, row.ID).Exec(ctx); err != nil {
					return PersonMergeEffects{}, err
				}
			}
			continue
		}
		_, duplicateExists, err := activeDuplicate(ctx, tx, row.ID, row.Type, personA, personB)
		if err != nil {
			return PersonMergeEffects{}, err
		}
		archivedAt := row.ArchivedAt
		if row.ArchivedAt == nil && duplicateExists {
			archivedAt = &now
		}
		if _, err := tx.NewRaw(`UPDATE family_relationships SET person_a_id = ?, person_b_id = ?, archived_at = ?, version = version + 1, updated_at = ? WHERE id = ?`, personA, personB, archivedAt, now, row.ID).Exec(ctx); err != nil {
			return PersonMergeEffects{}, err
		}
	}
	return effects, nil
}

func mergedEndpoints(row relationshipRow, sourceID, survivorID uuid.UUID) (uuid.UUID, uuid.UUID) {
	personA, personB := row.PersonAID, row.PersonBID
	if personA == sourceID {
		personA = survivorID
	}
	if personB == sourceID {
		personB = survivorID
	}
	if row.Type != "parent_child" && bytes.Compare(personA[:], personB[:]) > 0 {
		personA, personB = personB, personA
	}
	return personA, personB
}

func activeDuplicate(ctx context.Context, db bun.IDB, excludedID uuid.UUID, relationshipType string, personA, personB uuid.UUID) (relationshipRow, bool, error) {
	var duplicate relationshipRow
	err := db.NewRaw(`SELECT id, relationship_type, person_a_id, person_b_id, partner_status, version, created_at, updated_at, archived_at FROM family_relationships WHERE relationship_type = ? AND person_a_id = ? AND person_b_id = ? AND archived_at IS NULL AND id <> ? ORDER BY id LIMIT 1`, relationshipType, personA, personB, excludedID).Scan(ctx, &duplicate)
	if errors.Is(err, sql.ErrNoRows) {
		return relationshipRow{}, false, nil
	}
	return duplicate, err == nil, err
}

func samePartnerStatus(left, right *string) bool {
	return left != nil && right != nil && *left == *right
}

func parentPathExists(ctx context.Context, db bun.IDB, ancestorID, descendantID uuid.UUID) (bool, error) {
	var exists bool
	err := db.NewRaw(`WITH RECURSIVE descendants(person_id) AS (
		SELECT person_b_id FROM family_relationships WHERE relationship_type = 'parent_child' AND archived_at IS NULL AND person_a_id = ?
		UNION
		SELECT relationship.person_b_id FROM family_relationships AS relationship
		JOIN descendants ON relationship.person_a_id = descendants.person_id
		WHERE relationship.relationship_type = 'parent_child' AND relationship.archived_at IS NULL
	) SELECT EXISTS (SELECT 1 FROM descendants WHERE person_id = ?)`, ancestorID, descendantID).Scan(ctx, &exists)
	return exists, err
}

func validateMutation(ctx context.Context, tx bun.Tx, excludedID uuid.UUID, request MutationRequest, personA, personB uuid.UUID) error {
	var currentPeople int
	if err := tx.NewRaw(`SELECT count(*) FROM people WHERE id IN (?, ?) AND archived_at IS NULL AND merged_at IS NULL`, personA, personB).Scan(ctx, &currentPeople); err != nil {
		return err
	}
	if currentPeople != 2 {
		var allPeople int
		if err := tx.NewRaw(`SELECT count(*) FROM people WHERE id IN (?, ?)`, personA, personB).Scan(ctx, &allPeople); err != nil {
			return err
		}
		if allPeople != 2 {
			return ErrPersonNotFound
		}
		return ErrPersonUnavailable
	}
	var duplicate bool
	if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM family_relationships WHERE relationship_type = ? AND person_a_id = ? AND person_b_id = ? AND archived_at IS NULL AND id <> ?)`, request.RelationshipType, personA, personB, excludedID).Scan(ctx, &duplicate); err != nil {
		return err
	}
	if duplicate {
		return ErrDuplicate
	}
	if request.RelationshipType != "parent_child" {
		return nil
	}
	var cycle bool
	if err := tx.NewRaw(`WITH RECURSIVE descendants(person_id) AS (
		SELECT person_b_id FROM family_relationships
		WHERE relationship_type = 'parent_child' AND archived_at IS NULL AND person_a_id = ? AND id <> ?
		UNION
		SELECT relationship.person_b_id FROM family_relationships AS relationship
		JOIN descendants ON relationship.person_a_id = descendants.person_id
		WHERE relationship.relationship_type = 'parent_child' AND relationship.archived_at IS NULL AND relationship.id <> ?
	) SELECT EXISTS (SELECT 1 FROM descendants WHERE person_id = ?)`, personB, excludedID, excludedID, personA).Scan(ctx, &cycle); err != nil {
		return err
	}
	if cycle {
		return ErrCycle
	}
	return nil
}

func relationshipStaleOrNotFound(ctx context.Context, db bun.IDB, id uuid.UUID) error {
	var exists bool
	if err := db.NewRaw(`SELECT EXISTS (SELECT 1 FROM family_relationships WHERE id = ?)`, id).Scan(ctx, &exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return ErrStale
}

func getRelationshipRow(ctx context.Context, db bun.IDB, id uuid.UUID, lock bool) (relationshipRow, error) {
	query := `SELECT id, relationship_type, person_a_id, person_b_id, partner_status, version, created_at, updated_at, archived_at FROM family_relationships WHERE id = ?`
	if lock {
		query += ` FOR UPDATE`
	}
	var row relationshipRow
	if err := db.NewRaw(query, id).Scan(ctx, &row); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return relationshipRow{}, ErrNotFound
		}
		return relationshipRow{}, err
	}
	return row, nil
}

func getRelationship(ctx context.Context, db bun.IDB, id uuid.UUID, lock bool) (Relationship, error) {
	row, err := getRelationshipRow(ctx, db, id, lock)
	if err != nil {
		return Relationship{}, err
	}
	people, err := loadPeople(ctx, db, []uuid.UUID{row.PersonAID, row.PersonBID})
	if err != nil {
		return Relationship{}, err
	}
	return relationshipFromRow(row, people), nil
}

func relationshipFromRow(row relationshipRow, people map[uuid.UUID]Person) Relationship {
	relationship := Relationship{ID: row.ID.String(), Type: row.Type, PersonA: people[row.PersonAID], PersonB: people[row.PersonBID], Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, ArchivedAt: row.ArchivedAt}
	if row.PartnerStatus != nil {
		relationship.PartnerStatus = *row.PartnerStatus
	}
	return relationship
}

func loadPeople(ctx context.Context, db bun.IDB, ids []uuid.UUID) (map[uuid.UUID]Person, error) {
	rows := make([]personRow, 0)
	query := `SELECT id, display_name, sort_name, archived_at, merged_at, merged_into_person_id FROM people`
	if ids != nil {
		if len(ids) == 0 {
			return map[uuid.UUID]Person{}, nil
		}
		query += ` WHERE id IN (?)`
		if err := db.NewRaw(query, bun.List(ids)).Scan(ctx, &rows); err != nil {
			return nil, err
		}
	} else if err := db.NewRaw(query).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	people := make(map[uuid.UUID]Person, len(rows))
	for _, row := range rows {
		people[row.ID] = row.person()
	}
	return people, nil
}

func (s *Service) List(ctx context.Context, includeArchived bool) (ListResponse, error) {
	response := ListResponse{Relationships: []Relationship{}}
	err := s.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		rows := make([]relationshipRow, 0)
		if err := tx.NewRaw(`SELECT relationship.id, relationship.relationship_type, relationship.person_a_id, relationship.person_b_id, relationship.partner_status, relationship.version, relationship.created_at, relationship.updated_at, relationship.archived_at
			FROM family_relationships AS relationship
			JOIN people AS person_a ON person_a.id = relationship.person_a_id
			JOIN people AS person_b ON person_b.id = relationship.person_b_id
			WHERE (? OR relationship.archived_at IS NULL)
			ORDER BY (relationship.archived_at IS NOT NULL),
				CASE relationship.relationship_type WHEN 'parent_child' THEN 0 WHEN 'partner' THEN 1 ELSE 2 END,
				memento_normalize_person_name(person_a.sort_name), memento_normalize_person_name(person_b.sort_name), relationship.id`, includeArchived).Scan(ctx, &rows); err != nil {
			return err
		}
		ids := make([]uuid.UUID, 0, len(rows)*2)
		for _, row := range rows {
			ids = append(ids, row.PersonAID, row.PersonBID)
		}
		people, err := loadPeople(ctx, tx, ids)
		if err != nil {
			return err
		}
		for _, row := range rows {
			response.Relationships = append(response.Relationships, relationshipFromRow(row, people))
		}
		return nil
	})
	return response, err
}

func (s *Service) Branch(ctx context.Context, rootID uuid.UUID) (BranchResponse, error) {
	response := BranchResponse{Members: []BranchMember{}}
	err := s.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		people, err := loadPeople(ctx, tx, nil)
		if err != nil {
			return err
		}
		root, exists := people[rootID]
		if !exists {
			return ErrPersonNotFound
		}
		if root.Status != "current" {
			return ErrPersonUnavailable
		}
		response.Root = root
		rows := make([]relationshipRow, 0)
		if err := tx.NewRaw(`SELECT id, relationship_type, person_a_id, person_b_id, partner_status, version, created_at, updated_at, archived_at FROM family_relationships WHERE archived_at IS NULL ORDER BY id`).Scan(ctx, &rows); err != nil {
			return err
		}
		children := make(map[uuid.UUID][]uuid.UUID)
		partners := make(map[uuid.UUID][]uuid.UUID)
		for _, row := range rows {
			switch row.Type {
			case "parent_child":
				children[row.PersonAID] = append(children[row.PersonAID], row.PersonBID)
			case "partner":
				if row.PartnerStatus != nil && *row.PartnerStatus == "current" {
					partners[row.PersonAID] = append(partners[row.PersonAID], row.PersonBID)
					partners[row.PersonBID] = append(partners[row.PersonBID], row.PersonAID)
				}
			}
		}
		generation := map[uuid.UUID]int{rootID: 0}
		queue := []uuid.UUID{rootID}
		for len(queue) > 0 {
			parent := queue[0]
			queue = queue[1:]
			for _, child := range children[parent] {
				if _, seen := generation[child]; seen {
					continue
				}
				generation[child] = generation[parent] + 1
				queue = append(queue, child)
			}
		}
		members := make(map[uuid.UUID]BranchMember)
		for descendantID, depth := range generation {
			if descendantID == rootID {
				continue
			}
			if person, ok := people[descendantID]; ok && person.Status == "current" {
				members[descendantID] = BranchMember{Person: person, ConnectionType: "descendant", Generation: depth}
			}
		}
		for lineageID, depth := range generation {
			for _, partnerID := range partners[lineageID] {
				if partnerID == rootID {
					continue
				}
				if _, isDescendant := generation[partnerID]; isDescendant {
					continue
				}
				person, ok := people[partnerID]
				if !ok || person.Status != "current" {
					continue
				}
				connection := "descendant_current_partner"
				if lineageID == rootID {
					connection = "current_partner"
				}
				candidate := BranchMember{Person: person, ConnectionType: connection, Generation: depth}
				current, exists := members[partnerID]
				if !exists || candidate.Generation < current.Generation || (candidate.Generation == current.Generation && candidate.ConnectionType < current.ConnectionType) {
					members[partnerID] = candidate
				}
			}
		}
		for _, member := range members {
			response.Members = append(response.Members, member)
		}
		sortBranch(response.Members)
		return nil
	})
	return response, err
}

func sortBranch(members []BranchMember) {
	connectionRank := func(connection string) int {
		switch connection {
		case "current_partner":
			return 0
		case "descendant":
			return 1
		default:
			return 2
		}
	}
	for i := 1; i < len(members); i++ {
		for j := i; j > 0; j-- {
			left, right := members[j-1], members[j]
			ordered := left.Generation < right.Generation ||
				(left.Generation == right.Generation && connectionRank(left.ConnectionType) < connectionRank(right.ConnectionType)) ||
				(left.Generation == right.Generation && connectionRank(left.ConnectionType) == connectionRank(right.ConnectionType) && (left.Person.SortName < right.Person.SortName || (left.Person.SortName == right.Person.SortName && left.Person.ID < right.Person.ID)))
			if ordered {
				break
			}
			members[j-1], members[j] = members[j], members[j-1]
		}
	}
}

func appendAudit(ctx context.Context, tx bun.Tx, actor setup.CuratorSession, subject uuid.UUID, action string, relationshipID uuid.UUID, metadata map[string]any) error {
	metadata["relationship_id"] = relationshipID.String()
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	request := setup.RequestMetadataFromContext(ctx)
	_, err = tx.NewRaw(`INSERT INTO security_audit_events (actor_person_id, subject_person_id, action, outcome, client_ip, user_agent, session_id, metadata) VALUES (?, ?, ?, 'success', NULLIF(?, '')::inet, ?, ?, ?::jsonb)`, actor.PersonID, subject, action, request.ClientIP, request.UserAgent, actor.SessionID, string(encoded)).Exec(ctx)
	return err
}
