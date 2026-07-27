// Package repairs owns Curator-only review and explicit confirmation of Immich identity changes.
package repairs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/uptrace/bun"
)

var (
	ErrNotFound        = errors.New("repair candidate not found")
	ErrInvalid         = errors.New("repair confirmation is invalid")
	ErrConflict        = errors.New("repair candidate conflicts with current state")
	ErrDependency      = errors.New("immich identity evidence unavailable")
	ErrAlreadyResolved = errors.New("repair candidate is already resolved")
)

// Connector is the narrow private Immich identity evidence boundary.
type Connector interface {
	Check(ctx context.Context) error
	People(ctx context.Context) ([]immich.PersonSummary, error)
	Faces(ctx context.Context, assetID uuid.UUID) ([]immich.FaceSummary, error)
}

// Evidence describes normalized private media attributes. Path is intentionally Curator-only.
type Evidence struct {
	Checksum string `json:"checksum,omitempty"`
	Capture  string `json:"capture,omitempty"`
	Filename string `json:"filename"`
	Path     string `json:"path"`
}

// FaceAnchorEvidence explains a person or Media candidate without authorizing anyone.
type FaceAnchorEvidence struct {
	FaceID       string `json:"face_id"`
	AssetID      string `json:"asset_id"`
	Checksum     string `json:"checksum,omitempty"`
	ImageWidth   int    `json:"image_width"`
	ImageHeight  int    `json:"image_height"`
	X1           int    `json:"x1"`
	Y1           int    `json:"y1"`
	X2           int    `json:"x2"`
	Y2           int    `json:"y2"`
	LastPersonID string `json:"last_immich_person_id,omitempty"`
}

// PersonCandidate is a private proposed replacement for one confirmed portal Person link.
type PersonCandidate struct {
	ID                        string               `json:"id"`
	PersonID                  string               `json:"person_id"`
	PersonName                string               `json:"person_name"`
	PreviousImmichPersonID    string               `json:"previous_immich_person_id"`
	CandidateImmichPersonID   string               `json:"candidate_immich_person_id,omitempty"`
	CandidateImmichPersonName string               `json:"candidate_immich_person_name,omitempty"`
	State                     string               `json:"state"`
	Anchors                   []FaceAnchorEvidence `json:"face_anchors"`
	Conflicts                 []string             `json:"conflicts"`
	CreatedAt                 time.Time            `json:"created_at"`
	ResolvedAt                *time.Time           `json:"resolved_at,omitempty"`
}

// MediaCandidate is a private proposal to preserve one portal Media identity with a new backing.
type MediaCandidate struct {
	ID                     string               `json:"id"`
	MediaItemID            string               `json:"media_item_id"`
	PreviousImmichAssetID  string               `json:"previous_immich_asset_id"`
	CandidateImmichAssetID string               `json:"candidate_immich_asset_id"`
	State                  string               `json:"state"`
	Previous               Evidence             `json:"previous"`
	Candidate              Evidence             `json:"candidate"`
	FaceAnchors            []FaceAnchorEvidence `json:"face_anchors"`
	Conflicts              []string             `json:"conflicts"`
	CreatedAt              time.Time            `json:"created_at"`
	ResolvedAt             *time.Time           `json:"resolved_at,omitempty"`
}

// UnlinkedPerson is a newly observed Immich identity, still only an addition.
type UnlinkedPerson struct {
	ImmichPersonID string `json:"immich_person_id"`
	Name           string `json:"name"`
	Hidden         bool   `json:"hidden"`
}

// ListResponse is generated to TypeScript by Tygo.
type ListResponse struct {
	PersonCandidates     []PersonCandidate `json:"person_candidates"`
	MediaCandidates      []MediaCandidate  `json:"media_candidates"`
	UnlinkedImmichPeople []UnlinkedPerson  `json:"unlinked_immich_people"`
}

// LinkPersonRequest explicitly maps one addition to one portal Person.
type LinkPersonRequest struct {
	PersonID       string `json:"person_id" validate:"required,uuid"`
	ImmichPersonID string `json:"immich_person_id" validate:"required,uuid"`
}

// MutationResponse is generated to TypeScript by Tygo.
type MutationResponse struct {
	Status string `json:"status"`
}

type anchorRow struct {
	ID           uuid.UUID      `bun:"id"`
	PersonID     uuid.UUID      `bun:"person_id"`
	FaceID       uuid.UUID      `bun:"immich_face_id"`
	AssetID      uuid.UUID      `bun:"immich_asset_id"`
	Checksum     sql.NullString `bun:"asset_checksum"`
	ImageWidth   int            `bun:"image_width"`
	ImageHeight  int            `bun:"image_height"`
	X1           int            `bun:"x1"`
	Y1           int            `bun:"y1"`
	X2           int            `bun:"x2"`
	Y2           int            `bun:"y2"`
	LastPersonID uuid.NullUUID  `bun:"last_linked_immich_person_id"`
	LastSeenAt   time.Time      `bun:"last_seen_at"`
}

func faceAnchorEvidence(rows []anchorRow) []FaceAnchorEvidence {
	result := make([]FaceAnchorEvidence, 0, len(rows))
	for _, row := range rows {
		evidence := FaceAnchorEvidence{FaceID: row.FaceID.String(), AssetID: row.AssetID.String(), ImageWidth: row.ImageWidth,
			ImageHeight: row.ImageHeight, X1: row.X1, Y1: row.Y1, X2: row.X2, Y2: row.Y2}
		if row.Checksum.Valid {
			evidence.Checksum = row.Checksum.String
		}
		if row.LastPersonID.Valid {
			evidence.LastPersonID = row.LastPersonID.UUID.String()
		}
		result = append(result, evidence)
	}
	return result
}

// Service keeps repair state separate from Recipient authorization and published identity.
type Service struct {
	db        *bun.DB
	connector Connector
	now       func() time.Time
}

func New(db *bun.DB, connector Connector) *Service {
	return &Service{db: db, connector: connector, now: time.Now}
}

// ReconcilePeople records additions and removals, then marks broken links for review.
// It never confirms a replacement or changes Attendance, Audience, or Recipient state.
func (s *Service) ReconcilePeople(ctx context.Context) (MutationResponse, error) {
	if err := s.connector.Check(ctx); err != nil {
		return MutationResponse{}, ErrDependency
	}
	people, err := s.connector.People(ctx)
	if err != nil {
		return MutationResponse{}, ErrDependency
	}
	var anchors []anchorRow
	if err := s.db.NewRaw(`
		SELECT id, person_id, immich_face_id, immich_asset_id, asset_checksum,
			image_width, image_height, x1, y1, x2, y2, last_linked_immich_person_id, last_seen_at
		FROM immich_face_anchors ORDER BY immich_asset_id, immich_face_id
	`).Scan(ctx, &anchors); err != nil {
		return MutationResponse{}, err
	}
	facesByID := map[uuid.UUID]immich.FaceSummary{}
	assetSeen := map[uuid.UUID]struct{}{}
	for _, anchor := range anchors {
		if _, seen := assetSeen[anchor.AssetID]; seen {
			continue
		}
		assetSeen[anchor.AssetID] = struct{}{}
		faces, faceErr := s.connector.Faces(ctx, anchor.AssetID)
		if errors.Is(faceErr, immich.ErrNotFound) {
			continue
		}
		if faceErr != nil {
			return MutationResponse{}, ErrDependency
		}
		for _, face := range faces {
			facesByID[face.SourceID] = face
		}
	}

	now := s.now().UTC()
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`SELECT pg_advisory_xact_lock(hashtextextended('memento:identity-repair', 0))`).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE immich_people_inventory SET present = false`).Exec(ctx); err != nil {
			return err
		}
		present := make(map[uuid.UUID]immich.PersonSummary, len(people))
		for _, person := range people {
			present[person.SourceID] = person
			if _, err := tx.NewRaw(`
				INSERT INTO immich_people_inventory (immich_person_id, name, hidden, first_seen_at, last_seen_at)
				VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (immich_person_id) DO UPDATE SET name = EXCLUDED.name, hidden = EXCLUDED.hidden,
					present = true, last_seen_at = EXCLUDED.last_seen_at
			`, person.SourceID, person.Name, person.Hidden, now, now).Exec(ctx); err != nil {
				return err
			}
		}

		type linkRow struct {
			PersonID, ImmichPersonID uuid.UUID
			State                    string
		}
		var links []linkRow
		if err := tx.NewRaw(`SELECT person_id, immich_person_id, state FROM immich_person_links ORDER BY person_id FOR UPDATE`).Scan(ctx, &links); err != nil {
			return err
		}
		anchorsByPerson := map[uuid.UUID][]anchorRow{}
		for _, anchor := range anchors {
			anchorsByPerson[anchor.PersonID] = append(anchorsByPerson[anchor.PersonID], anchor)
		}
		for _, link := range links {
			personAnchors := anchorsByPerson[link.PersonID]
			encodedAnchors, err := json.Marshal(faceAnchorEvidence(personAnchors))
			if err != nil {
				return err
			}
			candidate, conflicts, matched := evaluatePersonLink(link.ImmichPersonID, present, personAnchors, facesByID)
			if matched {
				if _, err := tx.NewRaw(`UPDATE immich_person_links SET last_seen_at = ?, version = version + 1 WHERE person_id = ?`, now, link.PersonID).Exec(ctx); err != nil {
					return err
				}
				if link.State == "linked" {
					if _, err := tx.NewRaw(`DELETE FROM person_repair_candidates WHERE person_id = ? AND state = 'pending'`, link.PersonID).Exec(ctx); err != nil {
						return err
					}
					continue
				}
				if _, err := tx.NewRaw(`DELETE FROM person_repair_candidates WHERE person_id = ? AND state = 'pending'`, link.PersonID).Exec(ctx); err != nil {
					return err
				}
				if _, err := tx.NewRaw(`
					INSERT INTO person_repair_candidates (
						id, person_id, previous_immich_person_id, candidate_immich_person_id,
						anchor_count, anchor_evidence, conflict_evidence, created_at
					)
					SELECT ?, ?, ?, ?, ?, ?::jsonb, '[]'::jsonb, ?
					WHERE NOT EXISTS (
						SELECT 1 FROM person_repair_candidates
						WHERE person_id = ? AND state = 'rejected'
						  AND previous_immich_person_id = ?
						  AND candidate_immich_person_id = ?
					)
				`, uuid.New(), link.PersonID, link.ImmichPersonID, link.ImmichPersonID, len(personAnchors), string(encodedAnchors), now,
					link.PersonID, link.ImmichPersonID, link.ImmichPersonID).Exec(ctx); err != nil {
					return err
				}
				continue
			}
			if _, err := tx.NewRaw(`UPDATE immich_person_links SET state = 'needs_review', version = version + 1 WHERE person_id = ?`, link.PersonID).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`DELETE FROM person_repair_candidates WHERE person_id = ? AND state = 'pending'`, link.PersonID).Exec(ctx); err != nil {
				return err
			}
			encodedConflicts, err := json.Marshal(conflicts)
			if err != nil {
				return err
			}
			candidateID := uuid.New()
			if _, err := tx.NewRaw(`
				INSERT INTO person_repair_candidates (
					id, person_id, previous_immich_person_id, candidate_immich_person_id,
					anchor_count, anchor_evidence, conflict_evidence, created_at
				)
				SELECT ?, ?, ?, ?, ?, ?::jsonb, ?::jsonb, ?
				WHERE NOT EXISTS (
					SELECT 1 FROM person_repair_candidates
					WHERE person_id = ? AND state = 'rejected'
					  AND previous_immich_person_id = ?
					  AND candidate_immich_person_id IS NOT DISTINCT FROM ?
				)
			`, candidateID, link.PersonID, link.ImmichPersonID, candidate, len(personAnchors), string(encodedAnchors), string(encodedConflicts), now,
				link.PersonID, link.ImmichPersonID, candidate).Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return MutationResponse{}, fmt.Errorf("reconcile Immich identities: %w", err)
	}
	return MutationResponse{Status: "reconciled"}, nil
}

func evaluatePersonLink(current uuid.UUID, present map[uuid.UUID]immich.PersonSummary, anchors []anchorRow, faces map[uuid.UUID]immich.FaceSummary) (*uuid.UUID, []string, bool) {
	_, currentPresent := present[current]
	if len(anchors) == 0 {
		if currentPresent {
			return nil, nil, true
		}
		return nil, []string{"immich_person_missing", "no_surviving_face_anchors"}, false
	}
	assignments := map[uuid.UUID]int{}
	conflicts := []string{}
	for _, anchor := range anchors {
		face, found := faces[anchor.FaceID]
		if !found {
			conflicts = appendUnique(conflicts, "face_anchor_missing")
			continue
		}
		if face.PersonID == nil {
			conflicts = appendUnique(conflicts, "face_anchor_unassigned")
			continue
		}
		assignments[*face.PersonID]++
	}
	if !currentPresent {
		conflicts = appendUnique(conflicts, "immich_person_missing")
	}
	if len(assignments) == 1 && len(conflicts) == boolCount(!currentPresent) {
		for assignment := range assignments {
			if assignment == current && currentPresent {
				return nil, nil, true
			}
			candidate := assignment
			return &candidate, conflicts, false
		}
	}
	if len(assignments) > 1 {
		conflicts = appendUnique(conflicts, "anchors_split_across_people")
	} else if len(assignments) == 0 {
		conflicts = appendUnique(conflicts, "no_consistent_candidate")
	}
	return nil, conflicts, false
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// List returns private evidence and confirmation state only.
func (s *Service) List(ctx context.Context) (ListResponse, error) {
	response := ListResponse{PersonCandidates: []PersonCandidate{}, MediaCandidates: []MediaCandidate{}, UnlinkedImmichPeople: []UnlinkedPerson{}}
	if err := s.listPersonCandidates(ctx, &response); err != nil {
		return ListResponse{}, err
	}
	if err := s.listMediaCandidates(ctx, &response); err != nil {
		return ListResponse{}, err
	}
	if err := s.db.NewRaw(`
		SELECT inventory.immich_person_id, inventory.name, inventory.hidden
		FROM immich_people_inventory AS inventory
		WHERE inventory.present AND NOT EXISTS (
			SELECT 1 FROM immich_person_links AS link
			WHERE link.immich_person_id = inventory.immich_person_id
		)
		ORDER BY lower(inventory.name), inventory.immich_person_id
	`).Scan(ctx, &response.UnlinkedImmichPeople); err != nil {
		return ListResponse{}, err
	}
	return response, nil
}

func (s *Service) listPersonCandidates(ctx context.Context, response *ListResponse) error {
	type row struct {
		ID, PersonID, PreviousID         uuid.UUID
		CandidateID                      uuid.NullUUID
		PersonName, CandidateName, State string
		Anchors, Conflicts               json.RawMessage
		CreatedAt                        time.Time
		ResolvedAt                       *time.Time
	}
	var rows []row
	if err := s.db.NewRaw(`
		SELECT candidate.id, candidate.person_id, person.display_name AS person_name,
			candidate.previous_immich_person_id AS previous_id,
			candidate.candidate_immich_person_id AS candidate_id,
			COALESCE(inventory.name, '') AS candidate_name, candidate.state,
			candidate.anchor_evidence AS anchors, candidate.conflict_evidence AS conflicts,
			candidate.created_at, candidate.resolved_at
		FROM person_repair_candidates AS candidate
		JOIN people AS person ON person.id = candidate.person_id
		LEFT JOIN immich_people_inventory AS inventory ON inventory.immich_person_id = candidate.candidate_immich_person_id
		ORDER BY (candidate.state = 'pending') DESC, candidate.created_at DESC, candidate.id
	`).Scan(ctx, &rows); err != nil {
		return err
	}
	for _, raw := range rows {
		candidate := PersonCandidate{ID: raw.ID.String(), PersonID: raw.PersonID.String(), PersonName: raw.PersonName,
			PreviousImmichPersonID: raw.PreviousID.String(), CandidateImmichPersonName: raw.CandidateName,
			State: raw.State, Conflicts: []string{}, Anchors: []FaceAnchorEvidence{}, CreatedAt: raw.CreatedAt, ResolvedAt: raw.ResolvedAt}
		if raw.CandidateID.Valid {
			candidate.CandidateImmichPersonID = raw.CandidateID.UUID.String()
		}
		if err := json.Unmarshal(raw.Conflicts, &candidate.Conflicts); err != nil {
			return err
		}
		if err := json.Unmarshal(raw.Anchors, &candidate.Anchors); err != nil {
			return err
		}
		response.PersonCandidates = append(response.PersonCandidates, candidate)
	}
	return nil
}

func (s *Service) listMediaCandidates(ctx context.Context, response *ListResponse) error {
	type row struct {
		ID, MediaItemID, PreviousID, CandidateID uuid.UUID
		State                                    string
		Conflicts                                json.RawMessage
		CreatedAt                                time.Time
		ResolvedAt                               *time.Time
		PreviousChecksum, PreviousCapture        sql.NullString
		PreviousFilename, PreviousPath           string
		CandidateChecksum, CandidateCapture      sql.NullString
		CandidateFilename, CandidatePath         string
	}
	var rows []row
	if err := s.db.NewRaw(`
		SELECT candidate.id, candidate.media_item_id, candidate.previous_immich_asset_id AS previous_id,
			candidate.candidate_immich_asset_id AS candidate_id, candidate.state,
			candidate.conflict_evidence AS conflicts, candidate.created_at, candidate.resolved_at,
			previous.checksum AS previous_checksum, previous.capture_at AS previous_capture,
			previous.filename AS previous_filename, previous.original_path AS previous_path,
			replacement.checksum AS candidate_checksum, replacement.capture_at AS candidate_capture,
			replacement.filename AS candidate_filename, replacement.original_path AS candidate_path
		FROM media_repair_candidates AS candidate
		JOIN media_backings AS previous ON previous.media_item_id = candidate.media_item_id
			AND previous.immich_asset_id = candidate.previous_immich_asset_id
		JOIN media_backings AS replacement ON replacement.immich_asset_id = candidate.candidate_immich_asset_id
		ORDER BY (candidate.state = 'pending') DESC, candidate.created_at DESC, candidate.id
	`).Scan(ctx, &rows); err != nil {
		return err
	}
	for _, raw := range rows {
		candidate := MediaCandidate{ID: raw.ID.String(), MediaItemID: raw.MediaItemID.String(), PreviousImmichAssetID: raw.PreviousID.String(),
			CandidateImmichAssetID: raw.CandidateID.String(), State: raw.State, Conflicts: []string{}, FaceAnchors: []FaceAnchorEvidence{},
			CreatedAt: raw.CreatedAt, ResolvedAt: raw.ResolvedAt,
			Previous: Evidence{Filename: raw.PreviousFilename, Path: raw.PreviousPath}, Candidate: Evidence{Filename: raw.CandidateFilename, Path: raw.CandidatePath}}
		if raw.PreviousChecksum.Valid {
			candidate.Previous.Checksum = raw.PreviousChecksum.String
		}
		if raw.PreviousCapture.Valid {
			candidate.Previous.Capture = raw.PreviousCapture.String
		}
		if raw.CandidateChecksum.Valid {
			candidate.Candidate.Checksum = raw.CandidateChecksum.String
		}
		if raw.CandidateCapture.Valid {
			candidate.Candidate.Capture = raw.CandidateCapture.String
		}
		if err := json.Unmarshal(raw.Conflicts, &candidate.Conflicts); err != nil {
			return err
		}
		if err := s.db.NewRaw(`
			SELECT immich_face_id::text AS face_id, immich_asset_id::text AS asset_id,
				COALESCE(asset_checksum, '') AS checksum, image_width, image_height, x1, y1, x2, y2,
				COALESCE(last_linked_immich_person_id::text, '') AS last_person_id
			FROM immich_face_anchors WHERE immich_asset_id IN (?, ?)
			ORDER BY immich_asset_id, immich_face_id LIMIT 24
		`, raw.PreviousID, raw.CandidateID).Scan(ctx, &candidate.FaceAnchors); err != nil {
			return err
		}
		response.MediaCandidates = append(response.MediaCandidates, candidate)
	}
	return nil
}

// LinkPerson explicitly confirms a new Person link and captures a small anchor sample.
func (s *Service) LinkPerson(ctx context.Context, actor setup.CuratorSession, request LinkPersonRequest) (MutationResponse, error) {
	personID, err := uuid.Parse(request.PersonID)
	if err != nil || personID == uuid.Nil {
		return MutationResponse{}, ErrInvalid
	}
	immichPersonID, err := uuid.Parse(request.ImmichPersonID)
	if err != nil || immichPersonID == uuid.Nil {
		return MutationResponse{}, ErrInvalid
	}
	return s.linkPerson(ctx, actor, personID, immichPersonID, nil)
}

func (s *Service) linkPerson(ctx context.Context, actor setup.CuratorSession, personID, immichPersonID uuid.UUID, candidateID *uuid.UUID) (MutationResponse, error) {
	anchors, err := s.collectAnchors(ctx, immichPersonID)
	if err != nil {
		return MutationResponse{}, err
	}
	now := s.now().UTC()
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if candidateID != nil {
			var lockedID uuid.UUID
			err := tx.NewRaw(`
				SELECT id FROM person_repair_candidates
				WHERE id = ? AND person_id = ? AND candidate_immich_person_id = ? AND state = 'pending'
				FOR UPDATE
			`, *candidateID, personID, immichPersonID).Scan(ctx, &lockedID)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrConflict
			}
			if err != nil {
				return err
			}
		}
		var current, present, claimed bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM people WHERE id = ? AND archived_at IS NULL AND merged_at IS NULL)`, personID).Scan(ctx, &current); err != nil {
			return err
		}
		if !current {
			return ErrInvalid
		}
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM immich_people_inventory WHERE immich_person_id = ? AND present)`, immichPersonID).Scan(ctx, &present); err != nil {
			return err
		}
		if !present {
			return ErrConflict
		}
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM immich_person_links WHERE immich_person_id = ? AND person_id <> ?)`, immichPersonID, personID).Scan(ctx, &claimed); err != nil {
			return err
		}
		if claimed {
			return ErrConflict
		}
		if _, err := tx.NewRaw(`
			INSERT INTO immich_person_links (person_id, immich_person_id, state, last_seen_at, confirmed_at, confirmed_by_person_id)
			VALUES (?, ?, 'linked', ?, ?, ?)
			ON CONFLICT (person_id) DO UPDATE SET immich_person_id = EXCLUDED.immich_person_id,
				state = 'linked', last_seen_at = EXCLUDED.last_seen_at, confirmed_at = EXCLUDED.confirmed_at,
				confirmed_by_person_id = EXCLUDED.confirmed_by_person_id, version = immich_person_links.version + 1
		`, personID, immichPersonID, now, now, actor.PersonID).Exec(ctx); err != nil {
			return err
		}
		if candidateID != nil {
			if err := execRepairExactlyOne(ctx, tx, `UPDATE person_repair_candidates SET state = 'confirmed', resolved_at = ?, resolved_by_person_id = ? WHERE id = ? AND state = 'pending'`, now, actor.PersonID, *candidateID); err != nil {
				return err
			}
		} else {
			if _, err := tx.NewRaw(`UPDATE person_repair_candidates SET state = 'confirmed', resolved_at = ?, resolved_by_person_id = ? WHERE person_id = ? AND candidate_immich_person_id = ? AND state = 'pending'`, now, actor.PersonID, personID, immichPersonID).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`UPDATE person_repair_candidates SET state = 'rejected', resolved_at = ?, resolved_by_person_id = ? WHERE person_id = ? AND state = 'pending'`, now, actor.PersonID, personID).Exec(ctx); err != nil {
				return err
			}
		}
		if _, err := tx.NewRaw(`DELETE FROM immich_face_anchors WHERE person_id = ?`, personID).Exec(ctx); err != nil {
			return err
		}
		if err := storeAnchors(ctx, tx, personID, immichPersonID, anchors, now); err != nil {
			return err
		}
		return appendAudit(ctx, tx, actor, &personID, "immich_person_link_confirmed", map[string]any{"immich_person_id": immichPersonID.String()})
	})
	if err != nil {
		return MutationResponse{}, err
	}
	return MutationResponse{Status: "confirmed"}, nil
}

type collectedAnchor struct {
	AssetID  uuid.UUID
	Checksum string
	Face     immich.FaceSummary
}

func (s *Service) collectAnchors(ctx context.Context, immichPersonID uuid.UUID) ([]collectedAnchor, error) {
	type backing struct {
		AssetID  uuid.UUID
		Checksum sql.NullString
	}
	var backings []backing
	if err := s.db.NewRaw(`SELECT immich_asset_id AS asset_id, checksum FROM media_backings WHERE active ORDER BY linked_at DESC LIMIT 50`).Scan(ctx, &backings); err != nil {
		return nil, err
	}
	result := make([]collectedAnchor, 0, 5)
	for _, backing := range backings {
		faces, err := s.connector.Faces(ctx, backing.AssetID)
		if errors.Is(err, immich.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, ErrDependency
		}
		for _, face := range faces {
			if face.PersonID != nil && *face.PersonID == immichPersonID {
				result = append(result, collectedAnchor{AssetID: backing.AssetID, Checksum: backing.Checksum.String, Face: face})
				if len(result) == 5 {
					return result, nil
				}
			}
		}
	}
	return result, nil
}

func storeAnchors(ctx context.Context, tx bun.Tx, personID, immichPersonID uuid.UUID, anchors []collectedAnchor, now time.Time) error {
	for _, anchor := range anchors {
		if _, err := tx.NewRaw(`
			INSERT INTO immich_face_anchors (
				id, person_id, immich_face_id, immich_asset_id, asset_checksum,
				image_width, image_height, x1, y1, x2, y2, last_linked_immich_person_id, last_seen_at
			) VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (person_id, immich_face_id) DO UPDATE SET immich_asset_id = EXCLUDED.immich_asset_id,
				asset_checksum = EXCLUDED.asset_checksum, image_width = EXCLUDED.image_width,
				image_height = EXCLUDED.image_height, x1 = EXCLUDED.x1, y1 = EXCLUDED.y1,
				x2 = EXCLUDED.x2, y2 = EXCLUDED.y2, last_linked_immich_person_id = EXCLUDED.last_linked_immich_person_id,
				last_seen_at = EXCLUDED.last_seen_at
		`, uuid.New(), personID, anchor.Face.SourceID, anchor.AssetID, anchor.Checksum,
			anchor.Face.ImageWidth, anchor.Face.ImageHeight, anchor.Face.X1, anchor.Face.Y1, anchor.Face.X2, anchor.Face.Y2,
			immichPersonID, now).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

// ConfirmPerson confirms a generated Person repair candidate.
func (s *Service) ConfirmPerson(ctx context.Context, actor setup.CuratorSession, candidateID uuid.UUID) (MutationResponse, error) {
	var personID, previousID uuid.UUID
	var replacement uuid.NullUUID
	var encodedConflicts json.RawMessage
	if err := s.db.NewRaw(`
		SELECT person_id, previous_immich_person_id, candidate_immich_person_id, conflict_evidence
		FROM person_repair_candidates WHERE id = ? AND state = 'pending'
	`, candidateID).Scan(ctx, &personID, &previousID, &replacement, &encodedConflicts); errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if scanErr := s.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM person_repair_candidates WHERE id = ?)`, candidateID).Scan(ctx, &exists); scanErr != nil {
			return MutationResponse{}, scanErr
		}
		if exists {
			return MutationResponse{}, ErrAlreadyResolved
		}
		return MutationResponse{}, ErrNotFound
	} else if err != nil {
		return MutationResponse{}, err
	}
	if !replacement.Valid {
		return MutationResponse{}, ErrConflict
	}
	var expectedConflicts []string
	if err := json.Unmarshal(encodedConflicts, &expectedConflicts); err != nil {
		return MutationResponse{}, err
	}
	currentCandidate, currentConflicts, err := s.currentPersonEvaluation(ctx, personID, previousID)
	if err != nil {
		return MutationResponse{}, err
	}
	if currentCandidate == nil || *currentCandidate != replacement.UUID || !slices.Equal(currentConflicts, expectedConflicts) {
		return MutationResponse{}, ErrConflict
	}
	return s.linkPerson(ctx, actor, personID, replacement.UUID, &candidateID)
}

func (s *Service) currentPersonEvaluation(ctx context.Context, personID, previousID uuid.UUID) (*uuid.UUID, []string, error) {
	if err := s.connector.Check(ctx); err != nil {
		return nil, nil, ErrDependency
	}
	people, err := s.connector.People(ctx)
	if err != nil {
		return nil, nil, ErrDependency
	}
	present := make(map[uuid.UUID]immich.PersonSummary, len(people))
	for _, person := range people {
		present[person.SourceID] = person
	}
	var anchors []anchorRow
	if err := s.db.NewRaw(`
		SELECT id, person_id, immich_face_id, immich_asset_id, asset_checksum,
			image_width, image_height, x1, y1, x2, y2, last_linked_immich_person_id, last_seen_at
		FROM immich_face_anchors WHERE person_id = ? ORDER BY immich_asset_id, immich_face_id
	`, personID).Scan(ctx, &anchors); err != nil {
		return nil, nil, err
	}
	facesByID := map[uuid.UUID]immich.FaceSummary{}
	seenAssets := map[uuid.UUID]struct{}{}
	for _, anchor := range anchors {
		if _, seen := seenAssets[anchor.AssetID]; seen {
			continue
		}
		seenAssets[anchor.AssetID] = struct{}{}
		faces, err := s.connector.Faces(ctx, anchor.AssetID)
		if errors.Is(err, immich.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, nil, ErrDependency
		}
		for _, face := range faces {
			facesByID[face.SourceID] = face
		}
	}
	candidate, conflicts, matched := evaluatePersonLink(previousID, present, anchors, facesByID)
	if matched {
		candidate = &previousID
	}
	return candidate, conflicts, nil
}

// RejectPerson preserves evidence while leaving the link in needs-review.
func (s *Service) RejectPerson(ctx context.Context, actor setup.CuratorSession, candidateID uuid.UUID) (MutationResponse, error) {
	return s.resolveCandidate(ctx, actor, "person", candidateID, "rejected")
}

// ConfirmMedia atomically moves source membership to the stable portal Media identity.
func (s *Service) ConfirmMedia(ctx context.Context, actor setup.CuratorSession, candidateID uuid.UUID) (MutationResponse, error) {
	now := s.now().UTC()
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var mediaItemID, candidateMediaItemID, previousAssetID, candidateAssetID uuid.UUID
		err := tx.NewRaw(`
			SELECT media_item_id, candidate_media_item_id, previous_immich_asset_id, candidate_immich_asset_id
			FROM media_repair_candidates WHERE id = ? AND state = 'pending' FOR UPDATE
		`, candidateID).Scan(ctx, &mediaItemID, &candidateMediaItemID, &previousAssetID, &candidateAssetID)
		if errors.Is(err, sql.ErrNoRows) {
			var exists bool
			if scanErr := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM media_repair_candidates WHERE id = ?)`, candidateID).Scan(ctx, &exists); scanErr != nil {
				return scanErr
			}
			if exists {
				return ErrAlreadyResolved
			}
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if candidateMediaItemID == uuid.Nil {
			return ErrConflict
		}
		if _, err := tx.NewRaw(`SELECT id FROM media_items WHERE id IN (?, ?) ORDER BY id FOR UPDATE`, mediaItemID, candidateMediaItemID).Exec(ctx); err != nil {
			return err
		}
		var previousChecksum, candidateChecksum sql.NullString
		var previousAvailability, candidateAvailability string
		var previousHasMembership, candidateHasMembership bool
		if err := tx.NewRaw(`
			SELECT previous.checksum, candidate.checksum, previous_item.availability, candidate_item.availability,
				EXISTS (SELECT 1 FROM source_album_memberships WHERE media_item_id = previous_item.id),
				EXISTS (SELECT 1 FROM source_album_memberships WHERE media_item_id = candidate_item.id)
			FROM media_backings AS previous
			JOIN media_items AS previous_item ON previous_item.id = previous.media_item_id
			JOIN media_backings AS candidate ON candidate.media_item_id = ? AND candidate.immich_asset_id = ? AND candidate.active
			JOIN media_items AS candidate_item ON candidate_item.id = candidate.media_item_id
			WHERE previous.media_item_id = ? AND previous.immich_asset_id = ? AND previous.active
		`, candidateMediaItemID, candidateAssetID, mediaItemID, previousAssetID).Scan(ctx,
			&previousChecksum, &candidateChecksum, &previousAvailability, &candidateAvailability,
			&previousHasMembership, &candidateHasMembership); errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		} else if err != nil {
			return err
		}
		if !previousChecksum.Valid || !candidateChecksum.Valid || previousChecksum.String != candidateChecksum.String ||
			previousAvailability != "source_missing" || candidateAvailability != "current" || previousHasMembership || !candidateHasMembership {
			return ErrConflict
		}
		if err := execRepairExactlyOne(ctx, tx, `UPDATE media_backings SET active = false, ended_at = ? WHERE media_item_id = ? AND active AND immich_asset_id = ?`, now, mediaItemID, previousAssetID); err != nil {
			return err
		}
		membershipResult, err := tx.NewRaw(`UPDATE source_album_memberships SET media_item_id = ? WHERE media_item_id = ?`, mediaItemID, candidateMediaItemID).Exec(ctx)
		if err != nil {
			return err
		}
		memberships, err := membershipResult.RowsAffected()
		if err != nil {
			return err
		}
		if memberships == 0 {
			return ErrConflict
		}
		if err := execRepairExactlyOne(ctx, tx, `UPDATE media_backings SET media_item_id = ?, state = 'confirmed', confirmed_at = ? WHERE media_item_id = ? AND active AND immich_asset_id = ?`, mediaItemID, now, candidateMediaItemID, candidateAssetID); err != nil {
			return err
		}
		if err := execRepairExactlyOne(ctx, tx, `UPDATE media_repair_candidates SET state = 'confirmed', resolved_at = ?, resolved_by_person_id = ?, candidate_media_item_id = NULL WHERE id = ? AND state = 'pending'`, now, actor.PersonID, candidateID); err != nil {
			return err
		}
		if err := execRepairExactlyOne(ctx, tx, `DELETE FROM media_items WHERE id = ?`, candidateMediaItemID); err != nil {
			return err
		}
		if err := execRepairExactlyOne(ctx, tx, `UPDATE media_items SET immich_asset_id = ?, availability = 'current', last_seen_at = ?, updated_at = ? WHERE id = ? AND immich_asset_id = ?`, candidateAssetID, now, now, mediaItemID, previousAssetID); err != nil {
			return err
		}
		return appendAudit(ctx, tx, actor, nil, "immich_media_backing_confirmed", map[string]any{"media_item_id": mediaItemID.String(), "previous_immich_asset_id": previousAssetID.String(), "candidate_immich_asset_id": candidateAssetID.String()})
	})
	if err != nil {
		return MutationResponse{}, err
	}
	return MutationResponse{Status: "confirmed"}, nil
}

func execRepairExactlyOne(ctx context.Context, tx bun.Tx, query string, args ...any) error {
	result, err := tx.NewRaw(query, args...).Exec(ctx)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

// RejectMedia preserves the add/remove state and repair evidence.
func (s *Service) RejectMedia(ctx context.Context, actor setup.CuratorSession, candidateID uuid.UUID) (MutationResponse, error) {
	return s.resolveCandidate(ctx, actor, "media", candidateID, "rejected")
}

func (s *Service) resolveCandidate(ctx context.Context, actor setup.CuratorSession, kind string, candidateID uuid.UUID, state string) (MutationResponse, error) {
	table := "person_repair_candidates"
	action := "immich_person_repair_rejected"
	if kind == "media" {
		table, action = "media_repair_candidates", "immich_media_repair_rejected"
	}
	now := s.now().UTC()
	returnResult := MutationResponse{}
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		result, err := tx.NewRaw(`UPDATE `+table+` SET state = ?, resolved_at = ?, resolved_by_person_id = ? WHERE id = ? AND state = 'pending'`, state, now, actor.PersonID, candidateID).Exec(ctx)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			var exists bool
			if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM `+table+` WHERE id = ?)`, candidateID).Scan(ctx, &exists); err != nil {
				return err
			}
			if exists {
				return ErrAlreadyResolved
			}
			return ErrNotFound
		}
		return appendAudit(ctx, tx, actor, nil, action, map[string]any{"candidate_id": candidateID.String()})
	})
	if err != nil {
		return MutationResponse{}, err
	}
	returnResult.Status = state
	return returnResult, nil
}

// SuggestionPersonIDs maps advisory Immich detections only through confirmed, healthy links.
func (s *Service) SuggestionPersonIDs(ctx context.Context, immichPersonIDs []uuid.UUID) ([]uuid.UUID, error) {
	if len(immichPersonIDs) == 0 {
		return []uuid.UUID{}, nil
	}
	var result []uuid.UUID
	if err := s.db.NewRaw(`
		SELECT person_id FROM immich_person_links
		WHERE state = 'linked' AND immich_person_id IN (?) ORDER BY person_id
	`, bun.List(immichPersonIDs)).Scan(ctx, &result); err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result, nil
}

func appendAudit(ctx context.Context, tx bun.Tx, actor setup.CuratorSession, subject *uuid.UUID, action string, metadata map[string]any) error {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = tx.NewRaw(`INSERT INTO security_audit_events (actor_person_id, subject_person_id, action, outcome, session_id, metadata) VALUES (?, ?, ?, 'success', ?, ?::jsonb)`, actor.PersonID, subject, action, actor.SessionID, string(encoded)).Exec(ctx)
	return err
}
