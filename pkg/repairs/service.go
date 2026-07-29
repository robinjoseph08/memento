// Package repairs owns Curator-only review and explicit confirmation of Immich identity changes.
package repairs

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/staging"
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
	Asset(ctx context.Context, assetID uuid.UUID) (immich.AssetSummary, error)
	AssetDeliveryAvailable(ctx context.Context, assetID uuid.UUID, mediaType string) (bool, error)
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
	ID                          string               `json:"id"`
	PersonID                    string               `json:"person_id"`
	PersonName                  string               `json:"person_name"`
	PreviousImmichPersonID      string               `json:"previous_immich_person_id"`
	PreviousImmichPersonPresent bool                 `json:"previous_immich_person_present"`
	CandidateImmichPersonID     string               `json:"candidate_immich_person_id,omitempty"`
	CandidateImmichPersonName   string               `json:"candidate_immich_person_name,omitempty"`
	State                       string               `json:"state"`
	Anchors                     []FaceAnchorEvidence `json:"face_anchors"`
	Conflicts                   []string             `json:"conflicts"`
	CreatedAt                   time.Time            `json:"created_at"`
	ResolvedAt                  *time.Time           `json:"resolved_at,omitempty"`
}

// MediaCandidate is a private proposal to preserve one portal Media identity with a new backing.
type MediaCandidate struct {
	ID                     string               `json:"id"`
	MediaItemID            string               `json:"media_item_id"`
	PreviousImmichAssetID  string               `json:"previous_immich_asset_id"`
	CandidateImmichAssetID string               `json:"candidate_immich_asset_id"`
	MediaType              string               `json:"media_type"`
	ReviewToken            string               `json:"review_token"`
	State                  string               `json:"state"`
	Previous               Evidence             `json:"previous"`
	Candidate              Evidence             `json:"candidate"`
	FaceAnchors            []FaceAnchorEvidence `json:"face_anchors"`
	Conflicts              []string             `json:"conflicts"`
	CreatedAt              time.Time            `json:"created_at"`
	ResolvedAt             *time.Time           `json:"resolved_at,omitempty"`
}

// SourceProblem is a prioritized Curator-only Source missing problem.
type SourceProblem struct {
	Kind           string    `json:"kind"`
	ID             string    `json:"id"`
	Label          string    `json:"label"`
	Priority       string    `json:"priority"`
	Published      bool      `json:"published"`
	MissingSince   time.Time `json:"missing_since"`
	CandidateCount int       `json:"candidate_count"`
}

// UnlinkedPerson is a newly observed Immich identity, still only an addition.
type UnlinkedPerson struct {
	ImmichPersonID string `json:"immich_person_id"`
	Name           string `json:"name"`
	Hidden         bool   `json:"hidden"`
}

// ListResponse is generated to TypeScript by Tygo.
type ListResponse struct {
	SourceProblems       []SourceProblem   `json:"source_problems"`
	PersonCandidates     []PersonCandidate `json:"person_candidates"`
	MediaCandidates      []MediaCandidate  `json:"media_candidates"`
	UnlinkedImmichPeople []UnlinkedPerson  `json:"unlinked_immich_people"`
}

// ConfirmMediaRequest binds confirmation to the exact evidence the Curator reviewed.
type ConfirmMediaRequest struct {
	ReviewToken string `json:"review_token" validate:"required,len=64,hexadecimal"`
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
	type linkRow struct {
		PersonID, ImmichPersonID uuid.UUID
		State                    string
		Version                  int64
	}
	var observedLinks []linkRow
	if err := s.db.NewRaw(`SELECT person_id, immich_person_id, state, version FROM immich_person_links ORDER BY person_id`).Scan(ctx, &observedLinks); err != nil {
		return MutationResponse{}, err
	}
	observedVersions := make(map[uuid.UUID]int64, len(observedLinks))
	for _, link := range observedLinks {
		observedVersions[link.PersonID] = link.Version
	}
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

		if _, err := tx.NewRaw(`
			SELECT person.id FROM people AS person
			JOIN immich_person_links AS link ON link.person_id = person.id
			ORDER BY person.id FOR NO KEY UPDATE OF person
		`).Exec(ctx); err != nil {
			return err
		}
		var links []linkRow
		if err := tx.NewRaw(`SELECT person_id, immich_person_id, state, version FROM immich_person_links ORDER BY person_id FOR UPDATE`).Scan(ctx, &links); err != nil {
			return err
		}
		anchorsByPerson := map[uuid.UUID][]anchorRow{}
		for _, anchor := range anchors {
			anchorsByPerson[anchor.PersonID] = append(anchorsByPerson[anchor.PersonID], anchor)
		}
		for _, link := range links {
			if observedVersion, observed := observedVersions[link.PersonID]; !observed || observedVersion != link.Version {
				continue
			}
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
	response := ListResponse{SourceProblems: []SourceProblem{}, PersonCandidates: []PersonCandidate{}, MediaCandidates: []MediaCandidate{}, UnlinkedImmichPeople: []UnlinkedPerson{}}
	if err := s.listSourceProblems(ctx, &response); err != nil {
		return ListResponse{}, err
	}
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

func (s *Service) listSourceProblems(ctx context.Context, response *ListResponse) error {
	return s.db.NewRaw(`
		WITH problems AS (
			SELECT 'media_item'::text AS kind, media.id,
				COALESCE(NULLIF(backing.filename, ''), 'Media item') AS label,
				EXISTS (
					SELECT 1 FROM current_published_placements AS placement
					WHERE placement.media_item_id = media.id
				) AS published,
				media.missing_since,
				(
					SELECT count(*) FROM media_repair_candidates AS candidate
					WHERE candidate.media_item_id = media.id AND candidate.state = 'pending'
				)::integer AS candidate_count
			FROM media_items AS media
			LEFT JOIN media_backings AS backing ON backing.media_item_id = media.id AND backing.active
			WHERE media.availability = 'source_missing'
			UNION ALL
			SELECT 'source_album'::text, album.id, album.name,
				EXISTS (
					SELECT 1 FROM source_album_memberships AS membership
					JOIN current_published_placements AS placement ON placement.media_item_id = membership.media_item_id
					WHERE membership.source_album_id = album.id
				),
				album.missing_since, 0
			FROM source_albums AS album
			WHERE album.source_missing
		)
		SELECT kind, id, label,
			CASE WHEN published THEN 'critical' ELSE 'high' END AS priority,
			published, missing_since, candidate_count
		FROM problems
		ORDER BY published DESC, (kind = 'media_item') DESC, missing_since, id
	`).Scan(ctx, &response.SourceProblems)
}

func (s *Service) listPersonCandidates(ctx context.Context, response *ListResponse) error {
	type row struct {
		ID, PersonID, PreviousID         uuid.UUID
		CandidateID                      uuid.NullUUID
		PersonName, CandidateName, State string
		PreviousPresent                  bool
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
			EXISTS (
				SELECT 1 FROM immich_people_inventory AS previous
				WHERE previous.immich_person_id = candidate.previous_immich_person_id AND previous.present
			) AS previous_present,
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
			PreviousImmichPersonID: raw.PreviousID.String(), PreviousImmichPersonPresent: raw.PreviousPresent,
			CandidateImmichPersonName: raw.CandidateName, State: raw.State, Conflicts: []string{},
			Anchors: []FaceAnchorEvidence{}, CreatedAt: raw.CreatedAt, ResolvedAt: raw.ResolvedAt}
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
		ID, MediaItemID                         uuid.UUID
		CandidateMediaItemID                    uuid.NullUUID
		PreviousID, CandidateID                 uuid.UUID
		MediaType, State                        string
		Previous, Candidate, Anchors, Conflicts json.RawMessage
		CreatedAt                               time.Time
		ResolvedAt                              *time.Time
	}
	var rows []row
	if err := s.db.NewRaw(`
		SELECT repair.id, repair.media_item_id, repair.candidate_media_item_id,
			repair.previous_immich_asset_id AS previous_id,
			repair.candidate_immich_asset_id AS candidate_id,
			COALESCE(candidate_media.media_type, stable_media.media_type) AS media_type,
			repair.state, repair.previous_evidence AS previous,
			repair.candidate_evidence AS candidate,
			repair.face_anchor_evidence AS anchors, repair.conflict_evidence AS conflicts,
			repair.created_at, repair.resolved_at
		FROM media_repair_candidates AS repair
		JOIN media_items AS stable_media ON stable_media.id = repair.media_item_id
		LEFT JOIN media_items AS candidate_media ON candidate_media.id = repair.candidate_media_item_id
		ORDER BY (repair.state = 'pending') DESC, repair.created_at DESC, repair.id
	`).Scan(ctx, &rows); err != nil {
		return err
	}
	for _, raw := range rows {
		candidate := MediaCandidate{ID: raw.ID.String(), MediaItemID: raw.MediaItemID.String(), PreviousImmichAssetID: raw.PreviousID.String(),
			CandidateImmichAssetID: raw.CandidateID.String(), MediaType: raw.MediaType, State: raw.State, Conflicts: []string{}, FaceAnchors: []FaceAnchorEvidence{},
			CreatedAt: raw.CreatedAt, ResolvedAt: raw.ResolvedAt}
		if err := json.Unmarshal(raw.Previous, &candidate.Previous); err != nil {
			return err
		}
		if err := json.Unmarshal(raw.Candidate, &candidate.Candidate); err != nil {
			return err
		}
		if err := json.Unmarshal(raw.Anchors, &candidate.FaceAnchors); err != nil {
			return err
		}
		if err := json.Unmarshal(raw.Conflicts, &candidate.Conflicts); err != nil {
			return err
		}
		if raw.State == "pending" {
			candidate.ReviewToken = mediaEvidenceToken(mediaEvidenceSnapshot{
				CandidateID: raw.ID, MediaItemID: raw.MediaItemID,
				CandidateMediaItemID: raw.CandidateMediaItemID,
				PreviousAssetID:      raw.PreviousID, CandidateAssetID: raw.CandidateID,
				MediaType: raw.MediaType, Previous: candidate.Previous, Candidate: candidate.Candidate,
				Anchors: candidate.FaceAnchors, Conflicts: candidate.Conflicts,
			})
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
	if err := s.requireCurrentImmichPerson(ctx, immichPersonID); err != nil {
		return MutationResponse{}, err
	}
	return s.linkPerson(ctx, actor, personID, immichPersonID, nil)
}

func (s *Service) requireCurrentImmichPerson(ctx context.Context, immichPersonID uuid.UUID) error {
	if err := s.connector.Check(ctx); err != nil {
		return ErrDependency
	}
	people, err := s.connector.People(ctx)
	if err != nil {
		return ErrDependency
	}
	for _, person := range people {
		if person.SourceID == immichPersonID {
			return nil
		}
	}
	return ErrConflict
}

func (s *Service) linkPerson(ctx context.Context, actor setup.CuratorSession, personID, immichPersonID uuid.UUID, candidateID *uuid.UUID) (MutationResponse, error) {
	anchors, err := s.collectAnchors(ctx, immichPersonID)
	if err != nil {
		return MutationResponse{}, err
	}
	if err := s.requireCurrentImmichPerson(ctx, immichPersonID); err != nil {
		return MutationResponse{}, err
	}
	return s.linkPersonWithAnchors(ctx, actor, personID, immichPersonID, candidateID, anchors)
}

func (s *Service) linkPersonWithAnchors(ctx context.Context, actor setup.CuratorSession, personID, immichPersonID uuid.UUID, candidateID *uuid.UUID, anchors []collectedAnchor) (MutationResponse, error) {
	now := s.now().UTC()
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`SELECT id FROM people WHERE id IN (?, ?) ORDER BY id FOR NO KEY UPDATE`, personID, actor.PersonID).Exec(ctx); err != nil {
			return err
		}
		var current bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM people WHERE id = ? AND archived_at IS NULL AND merged_at IS NULL)`, personID).Scan(ctx, &current); err != nil {
			return err
		}
		if !current {
			return ErrInvalid
		}
		if _, err := tx.NewRaw(`
			SELECT person_id FROM immich_person_links
			WHERE person_id = ? ORDER BY person_id FOR UPDATE
		`, personID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`SELECT id FROM immich_face_anchors WHERE person_id = ? ORDER BY id FOR UPDATE`, personID).Exec(ctx); err != nil {
			return err
		}
		if candidateID != nil {
			var lockedID uuid.UUID
			err := tx.NewRaw(`
				SELECT id FROM person_repair_candidates
				WHERE id = ? AND person_id = ? AND state = 'pending'
				FOR UPDATE
			`, *candidateID, personID).Scan(ctx, &lockedID)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrConflict
			}
			if err != nil {
				return err
			}
		} else if _, err := tx.NewRaw(`
			SELECT id FROM person_repair_candidates
			WHERE person_id = ? AND state = 'pending' ORDER BY id FOR UPDATE
		`, personID).Exec(ctx); err != nil {
			return err
		}
		var present, claimed bool
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
	var expectedConflicts []string
	if err := json.Unmarshal(encodedConflicts, &expectedConflicts); err != nil {
		return MutationResponse{}, err
	}
	currentCandidate, currentConflicts, currentAnchors, previousPresent, err := s.currentPersonEvaluation(ctx, personID, previousID)
	if err != nil {
		return MutationResponse{}, err
	}
	if !slices.Equal(currentConflicts, expectedConflicts) {
		return MutationResponse{}, ErrConflict
	}
	targetID := previousID
	if replacement.Valid {
		targetID = replacement.UUID
		if currentCandidate == nil || *currentCandidate != targetID {
			return MutationResponse{}, ErrConflict
		}
	} else if currentCandidate != nil || !previousPresent {
		return MutationResponse{}, ErrConflict
	}
	if err := s.requireCurrentImmichPerson(ctx, targetID); err != nil {
		return MutationResponse{}, err
	}
	return s.linkPersonWithAnchors(ctx, actor, personID, targetID, &candidateID, currentAnchors)
}

func (s *Service) currentPersonEvaluation(ctx context.Context, personID, previousID uuid.UUID) (*uuid.UUID, []string, []collectedAnchor, bool, error) {
	if err := s.connector.Check(ctx); err != nil {
		return nil, nil, nil, false, ErrDependency
	}
	people, err := s.connector.People(ctx)
	if err != nil {
		return nil, nil, nil, false, ErrDependency
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
		return nil, nil, nil, false, err
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
			return nil, nil, nil, false, ErrDependency
		}
		for _, face := range faces {
			facesByID[face.SourceID] = face
		}
	}
	candidate, conflicts, matched := evaluatePersonLink(previousID, present, anchors, facesByID)
	if matched {
		candidate = &previousID
	}
	anchorPersonID := previousID
	if candidate != nil {
		anchorPersonID = *candidate
	}
	currentAnchors := make([]collectedAnchor, 0, len(anchors))
	for _, anchor := range anchors {
		face, found := facesByID[anchor.FaceID]
		if !found || face.PersonID == nil || *face.PersonID != anchorPersonID {
			continue
		}
		currentAnchors = append(currentAnchors, collectedAnchor{AssetID: anchor.AssetID, Checksum: anchor.Checksum.String, Face: face})
	}
	_, previousPresent := present[previousID]
	return candidate, conflicts, currentAnchors, previousPresent, nil
}

// RejectPerson preserves evidence while leaving the link in needs-review.
func (s *Service) RejectPerson(ctx context.Context, actor setup.CuratorSession, candidateID uuid.UUID) (MutationResponse, error) {
	return s.resolveCandidate(ctx, actor, "person", candidateID, "rejected")
}

type mediaEvidenceSnapshot struct {
	CandidateID          uuid.UUID
	MediaItemID          uuid.UUID
	CandidateMediaItemID uuid.NullUUID
	PreviousAssetID      uuid.UUID
	CandidateAssetID     uuid.UUID
	MediaType            string
	Previous             Evidence
	Candidate            Evidence
	Anchors              []FaceAnchorEvidence
	Conflicts            []string
}

func mediaEvidenceToken(snapshot mediaEvidenceSnapshot) string {
	encoded, _ := json.Marshal(snapshot)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func validEvidenceChecksum(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha1ChecksumBytes && hex.EncodeToString(decoded) == value
}

const sha1ChecksumBytes = 20

func decodeEvidence(raw json.RawMessage) (Evidence, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Evidence{}, ErrConflict
	}
	for _, field := range []string{"checksum", "capture", "filename", "path"} {
		if _, present := fields[field]; !present {
			return Evidence{}, ErrConflict
		}
	}
	var evidence Evidence
	if err := json.Unmarshal(raw, &evidence); err != nil || !validEvidenceChecksum(evidence.Checksum) {
		return Evidence{}, ErrConflict
	}
	return evidence, nil
}

func decodeMediaEvidence(previousRaw, candidateRaw, anchorsRaw, conflictsRaw json.RawMessage) (Evidence, Evidence, []FaceAnchorEvidence, []string, error) {
	previous, err := decodeEvidence(previousRaw)
	if err != nil {
		return Evidence{}, Evidence{}, nil, nil, err
	}
	candidate, err := decodeEvidence(candidateRaw)
	if err != nil {
		return Evidence{}, Evidence{}, nil, nil, err
	}
	var anchors []FaceAnchorEvidence
	if err := json.Unmarshal(anchorsRaw, &anchors); err != nil || anchors == nil {
		return Evidence{}, Evidence{}, nil, nil, ErrConflict
	}
	for _, anchor := range anchors {
		faceID, faceErr := uuid.Parse(anchor.FaceID)
		assetID, assetErr := uuid.Parse(anchor.AssetID)
		validLastPerson := true
		if anchor.LastPersonID != "" {
			lastPersonID, err := uuid.Parse(anchor.LastPersonID)
			validLastPerson = err == nil && lastPersonID != uuid.Nil
		}
		if faceErr != nil || faceID == uuid.Nil || assetErr != nil || assetID == uuid.Nil || !validLastPerson ||
			(anchor.Checksum != "" && !validEvidenceChecksum(anchor.Checksum)) || anchor.ImageWidth < 0 || anchor.ImageHeight < 0 ||
			anchor.X1 < 0 || anchor.Y1 < 0 || anchor.X2 < anchor.X1 || anchor.Y2 < anchor.Y1 {
			return Evidence{}, Evidence{}, nil, nil, ErrConflict
		}
	}
	var conflicts []string
	if err := json.Unmarshal(conflictsRaw, &conflicts); err != nil || conflicts == nil {
		return Evidence{}, Evidence{}, nil, nil, ErrConflict
	}
	return previous, candidate, anchors, conflicts, nil
}

func reviewTokenMatches(provided, expected string) bool {
	providedBytes, providedErr := hex.DecodeString(provided)
	expectedBytes, expectedErr := hex.DecodeString(expected)
	return providedErr == nil && expectedErr == nil && len(providedBytes) == sha256.Size &&
		len(expectedBytes) == sha256.Size && subtle.ConstantTimeCompare(providedBytes, expectedBytes) == 1
}

func (s *Service) validateFreshMediaEvidence(ctx context.Context, snapshot mediaEvidenceSnapshot) error {
	asset, err := s.connector.Asset(ctx, snapshot.CandidateAssetID)
	if errors.Is(err, immich.ErrNotFound) {
		return ErrConflict
	}
	if err != nil {
		return ErrDependency
	}
	if asset.SourceID != snapshot.CandidateAssetID || asset.MediaType != snapshot.MediaType ||
		asset.Checksum != snapshot.Candidate.Checksum || asset.OriginalPath != snapshot.Candidate.Path {
		return ErrConflict
	}
	candidateAnchors := make([]FaceAnchorEvidence, 0)
	for _, anchor := range snapshot.Anchors {
		if anchor.AssetID == snapshot.CandidateAssetID.String() {
			candidateAnchors = append(candidateAnchors, anchor)
		}
	}
	if len(candidateAnchors) == 0 {
		return nil
	}
	faces, err := s.connector.Faces(ctx, snapshot.CandidateAssetID)
	if err != nil {
		if errors.Is(err, immich.ErrNotFound) {
			return ErrConflict
		}
		return ErrDependency
	}
	facesByID := make(map[string]immich.FaceSummary, len(faces))
	for _, face := range faces {
		facesByID[face.SourceID.String()] = face
	}
	for _, anchor := range candidateAnchors {
		face, present := facesByID[anchor.FaceID]
		if !present || (anchor.Checksum != "" && anchor.Checksum != asset.Checksum) ||
			face.ImageWidth != anchor.ImageWidth || face.ImageHeight != anchor.ImageHeight ||
			face.X1 != anchor.X1 || face.Y1 != anchor.Y1 || face.X2 != anchor.X2 || face.Y2 != anchor.Y2 {
			return ErrConflict
		}
	}
	return nil
}

// ConfirmMedia atomically moves source membership to the stable portal Media identity.
func (s *Service) ConfirmMedia(ctx context.Context, actor setup.CuratorSession, candidateID uuid.UUID, reviewToken string) (MutationResponse, error) {
	if s.connector == nil {
		return MutationResponse{}, ErrDependency
	}
	// Obtain the exact reviewed snapshot and fresh Immich evidence before taking
	// the global staging boundary or Media row locks. The transaction below then
	// rejects any identity, evidence, or type change observed before commit.
	var expectedMediaItemID, expectedPreviousAssetID, expectedCandidateAssetID uuid.UUID
	var expectedCandidateMediaItemID uuid.NullUUID
	var expectedCandidateItemAssetID uuid.NullUUID
	var expectedPreviousMediaType string
	var expectedMediaType, expectedCandidateBackingState sql.NullString
	var expectedCandidateHasBackingHistory bool
	var previousRaw, candidateRaw, anchorsRaw, conflictsRaw json.RawMessage
	err := s.db.NewRaw(`
		SELECT repair.media_item_id, repair.candidate_media_item_id,
			repair.previous_immich_asset_id, repair.candidate_immich_asset_id,
			stable_media.media_type, candidate_media.media_type,
			candidate_media.immich_asset_id, candidate_backing.state,
			EXISTS (
				SELECT 1 FROM media_backings AS history
				WHERE history.media_item_id = candidate_media.id
				  AND (candidate_backing.id IS NULL OR history.id <> candidate_backing.id)
			),
			repair.previous_evidence, repair.candidate_evidence,
			repair.face_anchor_evidence, repair.conflict_evidence
		FROM media_repair_candidates AS repair
		JOIN media_items AS stable_media ON stable_media.id = repair.media_item_id
		LEFT JOIN media_items AS candidate_media ON candidate_media.id = repair.candidate_media_item_id
		LEFT JOIN media_backings AS candidate_backing
		  ON candidate_backing.media_item_id = candidate_media.id
		 AND candidate_backing.immich_asset_id = repair.candidate_immich_asset_id
		 AND candidate_backing.active
		WHERE repair.id = ? AND repair.state = 'pending'
	`, candidateID).Scan(ctx, &expectedMediaItemID, &expectedCandidateMediaItemID,
		&expectedPreviousAssetID, &expectedCandidateAssetID, &expectedPreviousMediaType,
		&expectedMediaType, &expectedCandidateItemAssetID, &expectedCandidateBackingState,
		&expectedCandidateHasBackingHistory, &previousRaw, &candidateRaw, &anchorsRaw, &conflictsRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return MutationResponse{}, mediaCandidateMissingError(ctx, s.db, candidateID)
	}
	if err != nil {
		return MutationResponse{}, err
	}
	if !expectedCandidateMediaItemID.Valid || !expectedMediaType.Valid ||
		!expectedCandidateItemAssetID.Valid || expectedCandidateItemAssetID.UUID != expectedCandidateAssetID ||
		!expectedCandidateBackingState.Valid || expectedCandidateBackingState.String != "addition" ||
		expectedCandidateHasBackingHistory || expectedCandidateMediaItemID.UUID == expectedMediaItemID {
		return MutationResponse{}, ErrConflict
	}
	expectedPreviousEvidence, expectedCandidateEvidence, expectedAnchors, expectedConflicts, err := decodeMediaEvidence(previousRaw, candidateRaw, anchorsRaw, conflictsRaw)
	if err != nil {
		return MutationResponse{}, err
	}
	expectedSnapshot := mediaEvidenceSnapshot{
		CandidateID: candidateID, MediaItemID: expectedMediaItemID,
		CandidateMediaItemID: expectedCandidateMediaItemID,
		PreviousAssetID:      expectedPreviousAssetID, CandidateAssetID: expectedCandidateAssetID,
		MediaType: expectedMediaType.String, Previous: expectedPreviousEvidence, Candidate: expectedCandidateEvidence,
		Anchors: expectedAnchors, Conflicts: expectedConflicts,
	}
	if !reviewTokenMatches(reviewToken, mediaEvidenceToken(expectedSnapshot)) {
		return MutationResponse{}, ErrConflict
	}
	if expectedPreviousMediaType != expectedMediaType.String {
		return MutationResponse{}, ErrConflict
	}
	if err := s.validateFreshMediaEvidence(ctx, expectedSnapshot); err != nil {
		return MutationResponse{}, err
	}
	deliverable, err := s.connector.AssetDeliveryAvailable(ctx, expectedCandidateAssetID, expectedMediaType.String)
	if err != nil {
		return MutationResponse{}, ErrDependency
	}
	if !deliverable {
		return MutationResponse{}, ErrConflict
	}

	now := s.now().UTC()
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := staging.LockAccessSummaryRefresh(ctx, tx); err != nil {
			return err
		}
		if err := staging.LockMediaRelink(ctx, tx); err != nil {
			return err
		}
		var mediaItemID, previousAssetID, candidateAssetID uuid.UUID
		var candidateMediaItemID uuid.NullUUID
		err := tx.NewRaw(`
			SELECT media_item_id, candidate_media_item_id, previous_immich_asset_id, candidate_immich_asset_id
			FROM media_repair_candidates WHERE id = ? AND state = 'pending'
		`, candidateID).Scan(ctx, &mediaItemID, &candidateMediaItemID, &previousAssetID, &candidateAssetID)
		if errors.Is(err, sql.ErrNoRows) {
			return mediaCandidateMissingError(ctx, tx, candidateID)
		}
		if err != nil {
			return err
		}
		if !candidateMediaItemID.Valid {
			return ErrConflict
		}
		var lockedMediaIDs []uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM media_items WHERE id IN (?, ?) ORDER BY id FOR UPDATE`, mediaItemID, candidateMediaItemID.UUID).Scan(ctx, &lockedMediaIDs); err != nil {
			return err
		}
		if len(lockedMediaIDs) != 2 {
			return ErrConflict
		}
		var lockedMediaItemID, lockedPreviousAssetID, lockedCandidateAssetID uuid.UUID
		var lockedCandidateMediaItemID uuid.NullUUID
		var lockedPreviousRaw, lockedCandidateRaw, lockedAnchorsRaw, lockedConflictsRaw json.RawMessage
		err = tx.NewRaw(`
			SELECT media_item_id, candidate_media_item_id, previous_immich_asset_id,
				candidate_immich_asset_id, previous_evidence, candidate_evidence,
				face_anchor_evidence, conflict_evidence
			FROM media_repair_candidates WHERE id = ? AND state = 'pending' FOR UPDATE
		`, candidateID).Scan(ctx, &lockedMediaItemID, &lockedCandidateMediaItemID,
			&lockedPreviousAssetID, &lockedCandidateAssetID, &lockedPreviousRaw,
			&lockedCandidateRaw, &lockedAnchorsRaw, &lockedConflictsRaw)
		if errors.Is(err, sql.ErrNoRows) {
			return mediaCandidateMissingError(ctx, tx, candidateID)
		}
		if err != nil {
			return err
		}
		if !lockedCandidateMediaItemID.Valid || lockedMediaItemID != mediaItemID ||
			lockedCandidateMediaItemID.UUID != candidateMediaItemID.UUID || lockedPreviousAssetID != previousAssetID ||
			lockedCandidateAssetID != candidateAssetID || mediaItemID != expectedMediaItemID ||
			candidateMediaItemID.UUID != expectedCandidateMediaItemID.UUID || previousAssetID != expectedPreviousAssetID ||
			candidateAssetID != expectedCandidateAssetID {
			return ErrConflict
		}
		lockedPreviousEvidence, lockedCandidateEvidence, lockedAnchors, lockedConflicts, evidenceErr := decodeMediaEvidence(lockedPreviousRaw, lockedCandidateRaw, lockedAnchorsRaw, lockedConflictsRaw)
		if evidenceErr != nil || !reviewTokenMatches(reviewToken, mediaEvidenceToken(mediaEvidenceSnapshot{
			CandidateID: candidateID, MediaItemID: lockedMediaItemID,
			CandidateMediaItemID: lockedCandidateMediaItemID,
			PreviousAssetID:      lockedPreviousAssetID, CandidateAssetID: lockedCandidateAssetID,
			MediaType: expectedMediaType.String, Previous: lockedPreviousEvidence, Candidate: lockedCandidateEvidence,
			Anchors: lockedAnchors, Conflicts: lockedConflicts,
		})) {
			return ErrConflict
		}
		candidateMediaID := candidateMediaItemID.UUID
		var previousMediaType, candidateMediaType string
		if err := tx.NewRaw(`
			SELECT previous.media_type, candidate.media_type
			FROM media_items AS previous, media_items AS candidate
			WHERE previous.id = ? AND candidate.id = ?
		`, mediaItemID, candidateMediaID).Scan(ctx, &previousMediaType, &candidateMediaType); errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		} else if err != nil {
			return err
		}
		if previousMediaType != expectedPreviousMediaType || candidateMediaType != expectedMediaType.String ||
			previousMediaType != candidateMediaType {
			return ErrConflict
		}
		var previousChecksum, candidateChecksum sql.NullString
		var previousPath, candidatePath, previousAvailability, candidateAvailability string
		var candidateBackingState string
		var candidateItemAssetID uuid.UUID
		var candidateHasMembership, candidateHasBackingHistory bool
		if err := tx.NewRaw(`
			SELECT previous.checksum, candidate.checksum, previous.original_path, candidate.original_path,
				previous_item.availability, candidate_item.availability, candidate.state,
				candidate_item.immich_asset_id,
				EXISTS (SELECT 1 FROM source_album_memberships WHERE media_item_id = candidate_item.id),
				EXISTS (
					SELECT 1 FROM media_backings AS history
					WHERE history.media_item_id = candidate_item.id AND history.id <> candidate.id
				)
			FROM media_backings AS previous
			JOIN media_items AS previous_item ON previous_item.id = previous.media_item_id
			JOIN media_backings AS candidate ON candidate.media_item_id = ? AND candidate.immich_asset_id = ? AND candidate.active
			JOIN media_items AS candidate_item ON candidate_item.id = candidate.media_item_id
			WHERE previous.media_item_id = ? AND previous.immich_asset_id = ? AND previous.active
		`, candidateMediaID, candidateAssetID, mediaItemID, previousAssetID).Scan(ctx,
			&previousChecksum, &candidateChecksum, &previousPath, &candidatePath,
			&previousAvailability, &candidateAvailability, &candidateBackingState,
			&candidateItemAssetID, &candidateHasMembership, &candidateHasBackingHistory); errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		} else if err != nil {
			return err
		}
		if !previousChecksum.Valid || !candidateChecksum.Valid || previousChecksum.String != candidateChecksum.String ||
			previousChecksum.String != expectedPreviousEvidence.Checksum || previousPath != expectedPreviousEvidence.Path ||
			candidateChecksum.String != expectedCandidateEvidence.Checksum || candidatePath != expectedCandidateEvidence.Path ||
			previousAvailability != "source_missing" || candidateAvailability != "current" ||
			candidateBackingState != "addition" || candidateItemAssetID != candidateAssetID ||
			!candidateHasMembership || candidateHasBackingHistory {
			return ErrConflict
		}
		var candidateHasIdentityOrAuthorization bool
		if err := tx.NewRaw(`
			SELECT EXISTS (SELECT 1 FROM published_media_placements WHERE media_item_id = ?)
				OR EXISTS (SELECT 1 FROM current_audience_entitlements WHERE media_item_id = ?)
				OR EXISTS (SELECT 1 FROM comments WHERE media_item_id = ?)
				OR EXISTS (SELECT 1 FROM favorites WHERE media_item_id = ?)
		`, candidateMediaID, candidateMediaID, candidateMediaID, candidateMediaID).Scan(ctx, &candidateHasIdentityOrAuthorization); err != nil {
			return err
		}
		if candidateHasIdentityOrAuthorization {
			return ErrConflict
		}
		if err := relinkDraftMediaReferences(ctx, tx, mediaItemID, candidateMediaID, now); err != nil {
			return err
		}
		if err := execRepairExactlyOne(ctx, tx, `UPDATE media_backings SET active = false, ended_at = ? WHERE media_item_id = ? AND active AND immich_asset_id = ?`, now, mediaItemID, previousAssetID); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM source_album_memberships WHERE media_item_id = ? AND immich_asset_id = ?`, mediaItemID, previousAssetID).Exec(ctx); err != nil {
			return err
		}
		membershipResult, err := tx.NewRaw(`UPDATE source_album_memberships SET media_item_id = ? WHERE media_item_id = ?`, mediaItemID, candidateMediaID).Exec(ctx)
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
		if err := execRepairExactlyOne(ctx, tx, `UPDATE media_backings SET media_item_id = ?, state = 'confirmed', confirmed_at = ? WHERE media_item_id = ? AND active AND immich_asset_id = ?`, mediaItemID, now, candidateMediaID, candidateAssetID); err != nil {
			return err
		}
		if err := execRepairExactlyOne(ctx, tx, `UPDATE media_repair_candidates SET state = 'confirmed', resolved_at = ?, resolved_by_person_id = ?, candidate_media_item_id = NULL WHERE id = ? AND state = 'pending'`, now, actor.PersonID, candidateID); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`
			UPDATE media_repair_candidates
			SET state = 'superseded', resolved_at = ?, resolved_by_person_id = ?, candidate_media_item_id = NULL
			WHERE id <> ? AND state = 'pending'
			  AND (media_item_id IN (?, ?) OR candidate_media_item_id IN (?, ?))
		`, now, actor.PersonID, candidateID, mediaItemID, candidateMediaID, mediaItemID, candidateMediaID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`
			UPDATE media_repair_candidates SET media_item_id = ? WHERE media_item_id = ?
		`, mediaItemID, candidateMediaID).Exec(ctx); err != nil {
			return err
		}
		if err := execRepairExactlyOne(ctx, tx, `DELETE FROM media_items WHERE id = ?`, candidateMediaID); err != nil {
			return err
		}
		if err := execRepairExactlyOne(ctx, tx, `UPDATE media_items SET immich_asset_id = ?, availability = 'current', missing_since = NULL, last_seen_at = ?, updated_at = ? WHERE id = ? AND immich_asset_id = ?`, candidateAssetID, now, now, mediaItemID, previousAssetID); err != nil {
			return err
		}
		auditMetadata, err := json.Marshal(map[string]any{
			"candidate_id": candidateID.String(), "previous_immich_asset_id": previousAssetID.String(),
			"candidate_immich_asset_id": candidateAssetID.String(),
		})
		if err != nil {
			return err
		}
		if _, err := tx.NewRaw(`
			INSERT INTO publication_audit_events (
				target_kind, target_id, actor_person_id, action, metadata, created_at
			) VALUES ('media', ?, ?, 'media_relinked', ?::jsonb, ?)
		`, mediaItemID, actor.PersonID, string(auditMetadata), now).Exec(ctx); err != nil {
			return err
		}
		return appendAudit(ctx, tx, actor, nil, "immich_media_backing_confirmed", map[string]any{"media_item_id": mediaItemID.String(), "previous_immich_asset_id": previousAssetID.String(), "candidate_immich_asset_id": candidateAssetID.String()})
	})
	if err != nil {
		return MutationResponse{}, err
	}
	return MutationResponse{Status: "confirmed"}, nil
}

func relinkDraftMediaReferences(ctx context.Context, tx bun.Tx, stableMediaID, candidateMediaID uuid.UUID, now time.Time) error {
	var affectedEventIDs []uuid.UUID
	if err := tx.NewRaw(`
		SELECT id FROM events
		WHERE id IN (
			SELECT event_id FROM draft_media_placements WHERE media_item_id = ?
			UNION
			SELECT event_id FROM draft_moments WHERE cover_media_item_id = ?
			UNION
			SELECT event_id FROM staged_source_removals WHERE media_item_id IN (?, ?)
		)
		ORDER BY id FOR UPDATE
	`, candidateMediaID, candidateMediaID, stableMediaID, candidateMediaID).Scan(ctx, &affectedEventIDs); err != nil {
		return err
	}
	var eventCollision, looseCollision bool
	if err := tx.NewRaw(`
		SELECT EXISTS (
			SELECT 1 FROM draft_media_placements AS candidate
			JOIN draft_media_placements AS stable ON stable.event_id = candidate.event_id
			WHERE candidate.media_item_id = ? AND stable.media_item_id = ?
		)
	`, candidateMediaID, stableMediaID).Scan(ctx, &eventCollision); err != nil {
		return err
	}
	if err := tx.NewRaw(`
		SELECT count(DISTINCT media_item_id) = 2
		FROM loose_items WHERE media_item_id IN (?, ?)
	`, stableMediaID, candidateMediaID).Scan(ctx, &looseCollision); err != nil {
		return err
	}
	if eventCollision || looseCollision {
		return ErrConflict
	}
	if _, err := tx.NewRaw(`UPDATE draft_media_placements SET media_item_id = ? WHERE media_item_id = ?`, stableMediaID, candidateMediaID).Exec(ctx); err != nil {
		return err
	}
	if _, err := tx.NewRaw(`UPDATE draft_moments SET cover_media_item_id = ? WHERE cover_media_item_id = ?`, stableMediaID, candidateMediaID).Exec(ctx); err != nil {
		return err
	}
	for _, eventID := range affectedEventIDs {
		var momentID *uuid.UUID
		var position int
		var wasCover bool
		err := tx.NewRaw(`
			SELECT draft_moment_id, position, was_cover
			FROM staged_source_removals
			WHERE event_id = ? AND media_item_id IN (?, ?)
			ORDER BY (media_item_id = ?) DESC
			LIMIT 1
		`, eventID, stableMediaID, candidateMediaID, stableMediaID).Scan(ctx, &momentID, &position, &wasCover)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			if momentID != nil {
				var retained bool
				if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM draft_moments WHERE event_id = ? AND id = ?)`, eventID, *momentID).Scan(ctx, &retained); err != nil {
					return err
				}
				if !retained {
					momentID = nil
				}
			}
			var placed, occupied bool
			if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM draft_media_placements WHERE event_id = ? AND media_item_id = ?)`, eventID, stableMediaID).Scan(ctx, &placed); err != nil {
				return err
			}
			if err := tx.NewRaw(`
				SELECT EXISTS (
					SELECT 1 FROM draft_media_placements
					WHERE event_id = ? AND position = ? AND media_item_id <> ?
				)
			`, eventID, position, stableMediaID).Scan(ctx, &occupied); err != nil {
				return err
			}
			if placed && !occupied {
				if _, err := tx.NewRaw(`
					UPDATE draft_media_placements SET draft_moment_id = ?, position = ?
					WHERE event_id = ? AND media_item_id = ?
				`, momentID, position, eventID, stableMediaID).Exec(ctx); err != nil {
					return err
				}
			} else if !placed {
				if occupied {
					if err := tx.NewRaw(`SELECT COALESCE(max(position), -1) + 1 FROM draft_media_placements WHERE event_id = ?`, eventID).Scan(ctx, &position); err != nil {
						return err
					}
				}
				if _, err := tx.NewRaw(`
					INSERT INTO draft_media_placements (event_id, media_item_id, draft_moment_id, position, created_at)
					VALUES (?, ?, ?, ?, ?)
				`, eventID, stableMediaID, momentID, position, now).Exec(ctx); err != nil {
					return err
				}
			}
			if wasCover && momentID != nil {
				if _, err := tx.NewRaw(`UPDATE draft_moments SET cover_media_item_id = ? WHERE id = ? AND cover_media_item_id IS NULL`, stableMediaID, *momentID).Exec(ctx); err != nil {
					return err
				}
			}
		}
		if _, err := tx.NewRaw(`
			DELETE FROM staged_source_removals
			WHERE event_id = ? AND media_item_id IN (?, ?)
		`, eventID, stableMediaID, candidateMediaID).Exec(ctx); err != nil {
			return err
		}
		if _, err := staging.InvalidateEvent(ctx, tx, eventID, now); err != nil {
			return err
		}
	}
	if _, err := tx.NewRaw(`
		UPDATE loose_items SET media_item_id = ?, version = version + 1, updated_at = ?
		WHERE media_item_id = ?
	`, stableMediaID, now, candidateMediaID).Exec(ctx); err != nil {
		return err
	}
	return nil
}

func mediaCandidateMissingError(ctx context.Context, db bun.IDB, candidateID uuid.UUID) error {
	var exists bool
	if err := db.NewRaw(`SELECT EXISTS (SELECT 1 FROM media_repair_candidates WHERE id = ?)`, candidateID).Scan(ctx, &exists); err != nil {
		return err
	}
	if exists {
		return ErrAlreadyResolved
	}
	return ErrNotFound
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
		if kind == "person" {
			var subjectPersonID uuid.UUID
			if err := tx.NewRaw(`SELECT person_id FROM person_repair_candidates WHERE id = ?`, candidateID).Scan(ctx, &subjectPersonID); errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			} else if err != nil {
				return err
			}
			if _, err := tx.NewRaw(`SELECT id FROM people WHERE id IN (?, ?) ORDER BY id FOR NO KEY UPDATE`, subjectPersonID, actor.PersonID).Exec(ctx); err != nil {
				return err
			}
		}
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
		SELECT link.person_id FROM immich_person_links AS link
		JOIN people AS person ON person.id = link.person_id
		WHERE link.state = 'linked' AND link.immich_person_id IN (?)
		  AND person.archived_at IS NULL AND person.merged_at IS NULL
		ORDER BY link.person_id
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
