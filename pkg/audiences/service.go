// Package audiences owns Curator-confirmed Attendance, explained proposals, and reviewed Audience snapshots.
package audiences

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/uptrace/bun"
)

var (
	ErrNotFound              = errors.New("audience target not found")
	ErrInvalid               = errors.New("audience request is invalid")
	ErrPersonUnavailable     = errors.New("attendance Person is unavailable")
	ErrRecipientIneligible   = errors.New("audience override Recipient is ineligible")
	ErrAttendanceUnconfirmed = errors.New("attendance must be confirmed before audience approval")
	ErrProposalStale         = errors.New("audience proposal is stale or incomplete")
	ErrStale                 = errors.New("audience review version is stale")
)

const (
	targetMoment = "moment"
	targetLoose  = "loose_item"
)

// Connector is the narrow boundary for Curator-only advisory face evidence.
type Connector interface {
	Faces(ctx context.Context, assetID uuid.UUID) ([]immich.FaceSummary, error)
}

// Person omits Recipient state and all Immich identity data.
type Person struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	SortName    string `json:"sort_name"`
}

// FaceEvidence is private advisory evidence for one detected face on portal Media.
type FaceEvidence struct {
	MediaItemID     string  `json:"media_item_id"`
	EvidenceID      string  `json:"evidence_id"`
	ImageWidth      int     `json:"image_width"`
	ImageHeight     int     `json:"image_height"`
	X1              int     `json:"x1"`
	Y1              int     `json:"y1"`
	X2              int     `json:"x2"`
	Y2              int     `json:"y2"`
	SuggestedPerson *Person `json:"suggested_person" tstype:"Person | null,required"`
}

// Reason preserves one applicable proposal basis and its matching Person.
type Reason struct {
	Kind           string  `json:"kind"`
	MatchingPerson *Person `json:"matching_person" tstype:"Person | null,required"`
}

// ProposalRecipient is an Eligible Recipient retained for Curator review.
type ProposalRecipient struct {
	Recipient Person   `json:"recipient"`
	Included  bool     `json:"included"`
	Reasons   []Reason `json:"reasons"`
}

// ApprovedAudience describes the current reviewed snapshot, including an empty one.
type ApprovedAudience struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	Recipients []Person  `json:"recipients"`
	ApprovedAt time.Time `json:"approved_at"`
}

// Review is Curator-only Attendance evidence and Audience state.
type Review struct {
	TargetKind            string              `json:"target_kind"`
	TargetID              string              `json:"target_id"`
	Version               int64               `json:"version"`
	AttendanceConfirmed   bool                `json:"attendance_confirmed"`
	AudienceComplete      bool                `json:"audience_complete"`
	People                []Person            `json:"people"`
	EligibleRecipients    []Person            `json:"eligible_recipients"`
	Attendance            []Person            `json:"attendance"`
	FaceEvidence          []FaceEvidence      `json:"face_evidence"`
	FaceEvidenceAvailable bool                `json:"face_evidence_available"`
	Proposal              []ProposalRecipient `json:"proposal"`
	ApprovedAudience      *ApprovedAudience   `json:"approved_audience" tstype:"ApprovedAudience | null,required"`
}

// AttendanceRequest explicitly replaces confirmed Attendance for one Moment.
type AttendanceRequest struct {
	PersonIDs *[]string `json:"person_ids" validate:"required,max=100000" tstype:"string[],required"`
}

// OverrideRequest sets or clears one manual proposal override.
type OverrideRequest struct {
	RecipientPersonID string `json:"recipient_person_id" validate:"required,uuid"`
	State             string `json:"state" validate:"required,oneof=automatic included excluded"`
}

// ApprovalResponse returns the immutable snapshot selected as current.
type ApprovalResponse struct {
	Audience ApprovedAudience `json:"audience"`
	Version  int64            `json:"version"`
}

type Service struct {
	db        *bun.DB
	connector Connector
	now       func() time.Time
}

// PersonMergeEffects summarizes active Audience references moved to a surviving Person.
type PersonMergeEffects struct {
	AttendanceEntriesMoved                 int
	CurrentPublishedAttendanceEntriesMoved int
	AudienceOverridesMoved                 int
	AudienceReasonsMoved                   int
	ReferenceFingerprint                   string
}

func New(db *bun.DB, connector Connector) *Service {
	return &Service{db: db, connector: connector, now: time.Now}
}

type target struct {
	kind string
	id   uuid.UUID
}

func (s *Service) ReviewMoment(ctx context.Context, id uuid.UUID) (Review, error) {
	if err := requireTarget(ctx, s.db, target{targetMoment, id}); err != nil {
		return Review{}, err
	}
	review, err := s.review(ctx, s.db, target{targetMoment, id})
	if err != nil {
		return Review{}, err
	}
	review.FaceEvidence, review.FaceEvidenceAvailable = s.faceEvidence(ctx, id)
	return review, nil
}

func (s *Service) ReviewLooseItem(ctx context.Context, id uuid.UUID) (Review, error) {
	if err := requireTarget(ctx, s.db, target{targetLoose, id}); err != nil {
		return Review{}, err
	}
	return s.review(ctx, s.db, target{targetLoose, id})
}

func (s *Service) review(ctx context.Context, db bun.IDB, target target) (Review, error) {
	response := Review{TargetKind: target.kind, TargetID: target.id.String(), People: []Person{}, EligibleRecipients: []Person{}, Attendance: []Person{}, FaceEvidence: []FaceEvidence{}, Proposal: []ProposalRecipient{}}
	stateQuery := `SELECT review_version, attendance_complete, audience_complete FROM draft_moments WHERE id = ?`
	if target.kind == targetLoose {
		stateQuery = `SELECT review_version, true, audience_complete FROM loose_items WHERE id = ?`
	}
	if err := db.NewRaw(stateQuery, target.id).Scan(ctx, &response.Version, &response.AttendanceConfirmed, &response.AudienceComplete); err != nil {
		return Review{}, err
	}
	if err := db.NewRaw(`SELECT id, display_name, sort_name FROM people WHERE archived_at IS NULL AND merged_at IS NULL ORDER BY memento_normalize_person_name(sort_name), id`).Scan(ctx, &response.People); err != nil {
		return Review{}, err
	}
	if err := db.NewRaw(`SELECT person.id, person.display_name, person.sort_name FROM recipient_access_generations AS access JOIN person_roles AS role ON role.person_id = access.person_id AND role.role = 'recipient' JOIN people AS person ON person.id = access.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL WHERE access.is_current AND access.state IN ('pending', 'onboarding', 'completed') AND NOT EXISTS (SELECT 1 FROM person_roles AS curator WHERE curator.person_id = access.person_id AND curator.role = 'curator') ORDER BY memento_normalize_person_name(person.sort_name), person.id`).Scan(ctx, &response.EligibleRecipients); err != nil {
		return Review{}, err
	}
	if target.kind == targetMoment {
		if err := db.NewRaw(`SELECT person.id, person.display_name, person.sort_name FROM attendance AS attended JOIN people AS person ON person.id = attended.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL WHERE attended.moment_id = ? ORDER BY memento_normalize_person_name(person.sort_name), person.id`, target.id).Scan(ctx, &response.Attendance); err != nil {
			return Review{}, err
		}
	}
	proposals, err := loadProposals(ctx, db, target)
	if err != nil {
		return Review{}, err
	}
	response.Proposal = proposals
	approved, err := loadApproved(ctx, db, target)
	if err != nil {
		return Review{}, err
	}
	response.ApprovedAudience = approved
	return response, nil
}

func (s *Service) ConfirmAttendance(ctx context.Context, actor setup.CuratorSession, momentID uuid.UUID, version int64, request AttendanceRequest) (Review, error) {
	if request.PersonIDs == nil {
		return Review{}, ErrInvalid
	}
	ids, err := parseIDs(*request.PersonIDs)
	if err != nil {
		return Review{}, err
	}
	now := s.now().UTC()
	var response Review
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		t := target{targetMoment, momentID}
		if err := lockTarget(ctx, tx, t, version); err != nil {
			return err
		}
		if len(ids) > 0 {
			var available []uuid.UUID
			if err := tx.NewRaw(`SELECT id FROM people WHERE id IN (?) AND archived_at IS NULL AND merged_at IS NULL FOR SHARE`, bun.List(ids)).Scan(ctx, &available); err != nil {
				return err
			}
			if len(available) != len(ids) {
				return ErrPersonUnavailable
			}
		}
		if _, err := tx.NewRaw(`DELETE FROM attendance WHERE moment_id = ?`, momentID).Exec(ctx); err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := tx.NewRaw(`INSERT INTO attendance (moment_id, person_id, source, confirmed_by_person_id, confirmed_at) VALUES (?, ?, 'manual', ?, ?)`, momentID, id, actor.PersonID, now).Exec(ctx); err != nil {
				return err
			}
		}
		if _, err := tx.NewRaw(`UPDATE draft_moments SET attendance_complete = true, audience_complete = false WHERE id = ?`, momentID).Exec(ctx); err != nil {
			return err
		}
		if err := recalculate(ctx, tx, t, now); err != nil {
			return err
		}
		if err := advanceTargetVersion(ctx, tx, t); err != nil {
			return err
		}
		if err := invalidateEventReview(ctx, tx, t, now); err != nil {
			return err
		}
		if err := appendPublicationAudit(ctx, tx, actor, t, "attendance_confirmed", map[string]any{"moment_id": momentID, "person_count": len(ids)}); err != nil {
			return err
		}
		response, err = s.review(ctx, tx, t)
		return err
	})
	if err != nil {
		return Review{}, err
	}
	response.FaceEvidence, response.FaceEvidenceAvailable = s.faceEvidence(ctx, momentID)
	return response, nil
}

func (s *Service) Recalculate(ctx context.Context, actor setup.CuratorSession, kind string, id uuid.UUID, version int64) (Review, error) {
	t, err := validTarget(kind, id)
	if err != nil {
		return Review{}, err
	}
	now := s.now().UTC()
	var response Review
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockTarget(ctx, tx, t, version); err != nil {
			return err
		}
		if err := recalculate(ctx, tx, t, now); err != nil {
			return err
		}
		if err := markAudienceIncomplete(ctx, tx, t); err != nil {
			return err
		}
		if err := advanceTargetVersion(ctx, tx, t); err != nil {
			return err
		}
		if err := invalidateEventReview(ctx, tx, t, now); err != nil {
			return err
		}
		if err := appendPublicationAudit(ctx, tx, actor, t, "audience_proposal_recalculated", map[string]any{"target_kind": t.kind, "target_id": t.id}); err != nil {
			return err
		}
		response, err = s.review(ctx, tx, t)
		return err
	})
	if err != nil {
		return Review{}, err
	}
	if t.kind == targetMoment {
		response.FaceEvidence, response.FaceEvidenceAvailable = s.faceEvidence(ctx, id)
	}
	return response, nil
}

func (s *Service) SetOverride(ctx context.Context, actor setup.CuratorSession, kind string, id uuid.UUID, version int64, request OverrideRequest) (Review, error) {
	t, err := validTarget(kind, id)
	if err != nil {
		return Review{}, err
	}
	recipientID, err := uuid.Parse(request.RecipientPersonID)
	if err != nil || recipientID == uuid.Nil || (request.State != "automatic" && request.State != "included" && request.State != "excluded") {
		return Review{}, ErrInvalid
	}
	now := s.now().UTC()
	var response Review
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockTarget(ctx, tx, t, version); err != nil {
			return err
		}
		var lockedRecipient uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM people WHERE id = ? FOR SHARE`, recipientID).Scan(ctx, &lockedRecipient); errors.Is(err, sql.ErrNoRows) {
			return ErrRecipientIneligible
		} else if err != nil {
			return err
		}
		if request.State == "automatic" {
			if _, err := tx.NewRaw(`DELETE FROM audience_overrides WHERE target_kind = ? AND target_id = ? AND recipient_person_id = ?`, t.kind, t.id, recipientID).Exec(ctx); err != nil {
				return err
			}
		} else {
			var eligible bool
			if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM recipient_access_generations AS access JOIN person_roles AS role ON role.person_id = access.person_id AND role.role = 'recipient' JOIN people AS person ON person.id = access.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL WHERE access.person_id = ? AND access.is_current AND access.state IN ('pending', 'onboarding', 'completed') AND NOT EXISTS (SELECT 1 FROM person_roles AS curator WHERE curator.person_id = access.person_id AND curator.role = 'curator'))`, recipientID).Scan(ctx, &eligible); err != nil {
				return err
			}
			if !eligible {
				return ErrRecipientIneligible
			}
			if _, err := tx.NewRaw(`INSERT INTO audience_overrides (target_kind, target_id, recipient_person_id, state, updated_by_person_id, updated_at) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (target_kind, target_id, recipient_person_id) DO UPDATE SET state = EXCLUDED.state, updated_by_person_id = EXCLUDED.updated_by_person_id, updated_at = EXCLUDED.updated_at`, t.kind, t.id, recipientID, request.State, actor.PersonID, now).Exec(ctx); err != nil {
				return err
			}
		}
		if err := recalculate(ctx, tx, t, now); err != nil {
			return err
		}
		if err := markAudienceIncomplete(ctx, tx, t); err != nil {
			return err
		}
		if err := advanceTargetVersion(ctx, tx, t); err != nil {
			return err
		}
		if err := invalidateEventReview(ctx, tx, t, now); err != nil {
			return err
		}
		if err := appendPublicationAudit(ctx, tx, actor, t, "audience_override_changed", map[string]any{"target_kind": t.kind, "target_id": t.id, "recipient_person_id": recipientID, "state": request.State}); err != nil {
			return err
		}
		response, err = s.review(ctx, tx, t)
		return err
	})
	if err != nil {
		return Review{}, err
	}
	if t.kind == targetMoment {
		response.FaceEvidence, response.FaceEvidenceAvailable = s.faceEvidence(ctx, id)
	}
	return response, nil
}

func (s *Service) Approve(ctx context.Context, actor setup.CuratorSession, kind string, id uuid.UUID, version int64) (ApprovalResponse, error) {
	t, err := validTarget(kind, id)
	if err != nil {
		return ApprovalResponse{}, err
	}
	now, snapshotID := s.now().UTC(), uuid.New()
	var response ApprovalResponse
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockTarget(ctx, tx, t, version); err != nil {
			return err
		}
		if t.kind == targetMoment {
			var attendanceConfirmed bool
			if err := tx.NewRaw(`SELECT attendance_complete FROM draft_moments WHERE id = ?`, t.id).Scan(ctx, &attendanceConfirmed); err != nil {
				return err
			}
			if !attendanceConfirmed {
				return ErrAttendanceUnconfirmed
			}
		}
		count, err := lockEligibleProposalRecipients(ctx, tx, t)
		if err != nil {
			return err
		}
		label := "Shared"
		if count == 0 {
			label = "Curator only"
		}
		if _, err := tx.NewRaw(`INSERT INTO audience_snapshots (id, target_kind, target_id, approved_by_person_id, approved_at, label) VALUES (?, ?, ?, ?, ?, ?)`, snapshotID, t.kind, t.id, actor.PersonID, now, label).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO audience_snapshot_entries (snapshot_id, recipient_person_id, recipient_access_generation_id) SELECT ?, recipient_person_id, recipient_access_generation_id FROM audience_proposals WHERE target_kind = ? AND target_id = ? AND included`, snapshotID, t.kind, t.id).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO current_audience_snapshots (target_kind, target_id, snapshot_id) VALUES (?, ?, ?) ON CONFLICT (target_kind, target_id) DO UPDATE SET snapshot_id = EXCLUDED.snapshot_id`, t.kind, t.id, snapshotID).Exec(ctx); err != nil {
			return err
		}
		if t.kind == targetMoment {
			_, err = tx.NewRaw(`UPDATE draft_moments SET audience_complete = true WHERE id = ?`, t.id).Exec(ctx)
		} else {
			_, err = tx.NewRaw(`UPDATE loose_items SET audience_complete = true WHERE id = ?`, t.id).Exec(ctx)
		}
		if err != nil {
			return err
		}
		if err := advanceTargetVersion(ctx, tx, t); err != nil {
			return err
		}
		if err := invalidateEventReview(ctx, tx, t, now); err != nil {
			return err
		}
		if err := appendPublicationAudit(ctx, tx, actor, t, "audience_approved", map[string]any{"target_kind": t.kind, "target_id": t.id, "snapshot_id": snapshotID, "recipient_count": count, "label": label}); err != nil {
			return err
		}
		approved, err := loadApproved(ctx, tx, t)
		if err != nil {
			return err
		}
		response = ApprovalResponse{Audience: *approved, Version: version + 1}
		return nil
	})
	return response, err
}

func lockEligibleProposalRecipients(ctx context.Context, tx bun.Tx, t target) (int, error) {
	type includedProposal struct {
		RecipientID uuid.UUID `bun:"recipient_id"`
		AccessID    uuid.UUID `bun:"access_id"`
	}
	var included []includedProposal
	if err := tx.NewRaw(`
		SELECT recipient_person_id AS recipient_id, recipient_access_generation_id AS access_id
		FROM audience_proposals
		WHERE target_kind = ? AND target_id = ? AND included
		ORDER BY recipient_person_id
	`, t.kind, t.id).Scan(ctx, &included); err != nil {
		return 0, err
	}
	var referencedRecipientIDs []uuid.UUID
	if err := tx.NewRaw(`
		SELECT recipient_person_id
		FROM audience_proposals
		WHERE target_kind = ? AND target_id = ?
		UNION
		SELECT recipient_person_id
		FROM audience_overrides
		WHERE target_kind = ? AND target_id = ?
		ORDER BY recipient_person_id
	`, t.kind, t.id, t.kind, t.id).Scan(ctx, &referencedRecipientIDs); err != nil {
		return 0, err
	}
	includedRecipientIDs := make([]uuid.UUID, 0, len(included))
	accessIDs := make([]uuid.UUID, 0, len(included))
	for _, proposal := range included {
		includedRecipientIDs = append(includedRecipientIDs, proposal.RecipientID)
		accessIDs = append(accessIDs, proposal.AccessID)
	}
	type recipientLock struct {
		query string
		ids   []uuid.UUID
	}
	locks := []recipientLock{{`SELECT id FROM people WHERE id IN (?) ORDER BY id FOR SHARE`, referencedRecipientIDs}}
	if len(included) > 0 {
		locks = append(locks,
			recipientLock{`SELECT person_id FROM person_roles WHERE person_id IN (?) ORDER BY person_id, role FOR SHARE`, includedRecipientIDs},
			recipientLock{`SELECT id FROM recipient_access_generations WHERE id IN (?) ORDER BY id FOR SHARE`, accessIDs},
		)
	}
	for _, lock := range locks {
		if len(lock.ids) > 0 {
			if _, err := tx.NewRaw(lock.query, bun.List(lock.ids)).Exec(ctx); err != nil {
				return 0, err
			}
		}
	}
	if err := lockAudienceReferences(ctx, tx); err != nil {
		return 0, err
	}
	var overridesConsistent bool
	if err := tx.NewRaw(`
		SELECT NOT EXISTS (
			SELECT 1
			FROM audience_overrides AS override_row
			LEFT JOIN audience_proposals AS proposal
				ON proposal.target_kind = override_row.target_kind
				AND proposal.target_id = override_row.target_id
				AND proposal.recipient_person_id = override_row.recipient_person_id
			WHERE override_row.target_kind = ? AND override_row.target_id = ?
				AND EXISTS (
					SELECT 1
					FROM recipient_access_generations AS access
					JOIN person_roles AS recipient_role ON recipient_role.person_id = access.person_id AND recipient_role.role = 'recipient'
					JOIN people AS person ON person.id = access.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
					WHERE access.person_id = override_row.recipient_person_id
						AND access.is_current
						AND access.state IN ('pending', 'onboarding', 'completed')
						AND NOT EXISTS (
							SELECT 1 FROM person_roles AS curator_role
							WHERE curator_role.person_id = access.person_id AND curator_role.role = 'curator'
						)
				)
				AND (
					proposal.recipient_person_id IS NULL
					OR proposal.included <> (override_row.state = 'included')
					OR NOT EXISTS (
						SELECT 1 FROM audience_reasons AS reason
						WHERE reason.target_kind = override_row.target_kind
							AND reason.target_id = override_row.target_id
							AND reason.recipient_person_id = override_row.recipient_person_id
							AND reason.kind = CASE override_row.state WHEN 'included' THEN 'manually_included' ELSE 'manually_excluded' END
					)
				)
		)
	`, t.kind, t.id).Scan(ctx, &overridesConsistent); err != nil {
		return 0, err
	}
	if !overridesConsistent {
		return 0, ErrProposalStale
	}
	if len(included) == 0 {
		return 0, nil
	}
	var eligible []uuid.UUID
	if err := tx.NewRaw(`
		SELECT proposal.recipient_person_id
		FROM audience_proposals AS proposal
		JOIN recipient_access_generations AS access
			ON access.id = proposal.recipient_access_generation_id
			AND access.person_id = proposal.recipient_person_id
			AND access.is_current
			AND access.state IN ('pending', 'onboarding', 'completed')
		JOIN people AS person
			ON person.id = proposal.recipient_person_id
			AND person.archived_at IS NULL
			AND person.merged_at IS NULL
		JOIN person_roles AS recipient_role
			ON recipient_role.person_id = proposal.recipient_person_id
			AND recipient_role.role = 'recipient'
		WHERE proposal.target_kind = ? AND proposal.target_id = ? AND proposal.included
			AND NOT EXISTS (
				SELECT 1 FROM person_roles AS curator_role
				WHERE curator_role.person_id = proposal.recipient_person_id
					AND curator_role.role = 'curator'
			)
			AND EXISTS (
				SELECT 1 FROM audience_reasons AS reason
				WHERE reason.target_kind = proposal.target_kind
					AND reason.target_id = proposal.target_id
					AND reason.recipient_person_id = proposal.recipient_person_id
			)
	`, t.kind, t.id).Scan(ctx, &eligible); err != nil {
		return 0, err
	}
	if len(eligible) != len(included) {
		return 0, ErrProposalStale
	}
	return len(included), nil
}

func validTarget(kind string, id uuid.UUID) (target, error) {
	if id == uuid.Nil || (kind != targetMoment && kind != targetLoose) {
		return target{}, ErrInvalid
	}
	return target{kind, id}, nil
}

func requireTarget(ctx context.Context, db bun.IDB, t target) error {
	query := `SELECT EXISTS (SELECT 1 FROM draft_moments WHERE id = ?)`
	if t.kind == targetLoose {
		query = `SELECT EXISTS (SELECT 1 FROM loose_items WHERE id = ?)`
	}
	var exists bool
	if err := db.NewRaw(query, t.id).Scan(ctx, &exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func lockTarget(ctx context.Context, tx bun.Tx, t target, expectedVersion int64) error {
	if expectedVersion < 1 {
		return ErrInvalid
	}
	query := `SELECT review_version FROM draft_moments WHERE id = ? FOR UPDATE`
	if t.kind == targetMoment {
		var eventID uuid.UUID
		if err := tx.NewRaw(`SELECT event_id FROM draft_moments WHERE id = ?`, t.id).Scan(ctx, &eventID); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		var lockedEventID uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM events WHERE id = ? FOR UPDATE`, eventID).Scan(ctx, &lockedEventID); err != nil {
			return err
		}
	} else {
		query = `SELECT review_version FROM loose_items WHERE id = ? FOR UPDATE`
	}
	var currentVersion int64
	if err := tx.NewRaw(query, t.id).Scan(ctx, &currentVersion); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if currentVersion != expectedVersion {
		return ErrStale
	}
	return nil
}

func advanceTargetVersion(ctx context.Context, tx bun.Tx, t target) error {
	query := `UPDATE draft_moments SET review_version = review_version + 1 WHERE id = ?`
	if t.kind == targetLoose {
		query = `UPDATE loose_items SET review_version = review_version + 1 WHERE id = ?`
	}
	_, err := tx.NewRaw(query, t.id).Exec(ctx)
	return err
}

func invalidateEventReview(ctx context.Context, tx bun.Tx, t target, now time.Time) error {
	if t.kind != targetMoment {
		return nil
	}
	_, err := tx.NewRaw(`
		UPDATE events SET version = version + 1, final_review_complete = false, updated_at = ?
		WHERE id = (SELECT event_id FROM draft_moments WHERE id = ?)
	`, now, t.id).Exec(ctx)
	return err
}

func markAudienceIncomplete(ctx context.Context, tx bun.Tx, t target) error {
	query := `UPDATE draft_moments SET audience_complete = false WHERE id = ?`
	if t.kind == targetLoose {
		query = `UPDATE loose_items SET audience_complete = false WHERE id = ?`
	}
	_, err := tx.NewRaw(query, t.id).Exec(ctx)
	return err
}

func parseIDs(raw []string) ([]uuid.UUID, error) {
	result := make([]uuid.UUID, 0, len(raw))
	seen := map[uuid.UUID]bool{}
	for _, value := range raw {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || seen[id] {
			return nil, ErrInvalid
		}
		seen[id] = true
		result = append(result, id)
	}
	return result, nil
}

func recalculate(ctx context.Context, tx bun.Tx, t target, now time.Time) error {
	if err := lockAudienceReferences(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.NewRaw(`DELETE FROM audience_proposals WHERE target_kind = ? AND target_id = ?`, t.kind, t.id).Exec(ctx); err != nil {
		return err
	}
	attendanceFilter := `false`
	args := []any{}
	if t.kind == targetMoment {
		attendanceFilter = `attended.moment_id = ?`
		args = append(args, t.id)
	}
	query := `WITH eligible AS MATERIALIZED (
		SELECT access.person_id, access.id AS generation_id
		FROM recipient_access_generations AS access
		JOIN person_roles AS recipient_role ON recipient_role.person_id = access.person_id AND recipient_role.role = 'recipient'
		JOIN people AS person ON person.id = access.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
		WHERE access.is_current AND access.state IN ('pending', 'onboarding', 'completed')
		AND NOT EXISTS (SELECT 1 FROM person_roles AS curator WHERE curator.person_id = access.person_id AND curator.role = 'curator')
	), automatic_reasons AS MATERIALIZED (
		SELECT eligible.person_id, 'present'::text AS kind, eligible.person_id AS matching_person_id
		FROM eligible JOIN attendance AS attended ON attended.person_id = eligible.person_id
		WHERE ` + attendanceFilter + `
		UNION ALL
		SELECT eligible.person_id, 'interested'::text AS kind, interest.selected_person_id AS matching_person_id
		FROM eligible
		JOIN interest_list_entries AS interest ON interest.recipient_person_id = eligible.person_id AND interest.state = 'active'
		JOIN attendance AS attended ON attended.person_id = interest.selected_person_id
		WHERE ` + attendanceFilter + `
	), automatic AS (
		SELECT DISTINCT person_id FROM automatic_reasons
	), candidate AS (
		SELECT person_id FROM automatic
		UNION
		SELECT override_row.recipient_person_id
		FROM audience_overrides AS override_row
		JOIN eligible ON eligible.person_id = override_row.recipient_person_id
		WHERE override_row.target_kind = ? AND override_row.target_id = ?
	), inserted_proposals AS (
		INSERT INTO audience_proposals (target_kind, target_id, recipient_person_id, recipient_access_generation_id, included, recalculated_at)
		SELECT ?::text, ?::uuid, candidate.person_id, eligible.generation_id,
			CASE override_row.state WHEN 'included' THEN true WHEN 'excluded' THEN false ELSE true END, ?
		FROM candidate
		JOIN eligible ON eligible.person_id = candidate.person_id
		LEFT JOIN audience_overrides AS override_row ON override_row.target_kind = ? AND override_row.target_id = ? AND override_row.recipient_person_id = candidate.person_id
		RETURNING target_kind, target_id, recipient_person_id
	)
	INSERT INTO audience_reasons (target_kind, target_id, recipient_person_id, kind, matching_person_id)
	SELECT proposal.target_kind, proposal.target_id, proposal.recipient_person_id, reason.kind, reason.matching_person_id
	FROM inserted_proposals AS proposal
	JOIN automatic_reasons AS reason ON reason.person_id = proposal.recipient_person_id
	UNION ALL
	SELECT proposal.target_kind, proposal.target_id, proposal.recipient_person_id,
		CASE override_row.state WHEN 'included' THEN 'manually_included' ELSE 'manually_excluded' END, NULL
	FROM inserted_proposals AS proposal
	JOIN audience_overrides AS override_row ON override_row.target_kind = proposal.target_kind AND override_row.target_id = proposal.target_id AND override_row.recipient_person_id = proposal.recipient_person_id`
	if t.kind == targetMoment {
		args = append(args, t.id)
	}
	args = append(args, t.kind, t.id, t.kind, t.id, now, t.kind, t.id)
	_, err := tx.NewRaw(query, args...).Exec(ctx)
	return err
}

func loadProposals(ctx context.Context, db bun.IDB, t target) ([]ProposalRecipient, error) {
	type row struct {
		RecipientID                  uuid.UUID
		RecipientName, RecipientSort string
		Included                     bool
		Kind                         string
		MatchingID                   uuid.NullUUID
		MatchingName, MatchingSort   sql.NullString
	}
	rows := []row{}
	if err := db.NewRaw(`SELECT proposal.recipient_person_id AS recipient_id, recipient.display_name AS recipient_name, recipient.sort_name AS recipient_sort, proposal.included, reason.kind, matching.id AS matching_id, matching.display_name AS matching_name, matching.sort_name AS matching_sort FROM audience_proposals AS proposal JOIN people AS recipient ON recipient.id = proposal.recipient_person_id JOIN audience_reasons AS reason ON reason.target_kind = proposal.target_kind AND reason.target_id = proposal.target_id AND reason.recipient_person_id = proposal.recipient_person_id LEFT JOIN people AS matching ON matching.id = reason.matching_person_id WHERE proposal.target_kind = ? AND proposal.target_id = ? ORDER BY memento_normalize_person_name(recipient.sort_name), recipient.id, CASE reason.kind WHEN 'present' THEN 0 WHEN 'interested' THEN 1 WHEN 'manually_included' THEN 2 ELSE 3 END, memento_normalize_person_name(matching.sort_name), matching.id`, t.kind, t.id).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	result := []ProposalRecipient{}
	indexes := map[uuid.UUID]int{}
	for _, row := range rows {
		i, ok := indexes[row.RecipientID]
		if !ok {
			i = len(result)
			indexes[row.RecipientID] = i
			result = append(result, ProposalRecipient{Recipient: Person{row.RecipientID.String(), row.RecipientName, row.RecipientSort}, Included: row.Included, Reasons: []Reason{}})
		}
		reason := Reason{Kind: row.Kind}
		if row.MatchingID.Valid {
			person := Person{row.MatchingID.UUID.String(), row.MatchingName.String, row.MatchingSort.String}
			reason.MatchingPerson = &person
		}
		result[i].Reasons = append(result[i].Reasons, reason)
	}
	return result, nil
}

func loadApproved(ctx context.Context, db bun.IDB, t target) (*ApprovedAudience, error) {
	var result ApprovedAudience
	err := db.NewRaw(`SELECT snapshot.id, snapshot.label, snapshot.approved_at FROM current_audience_snapshots AS current JOIN audience_snapshots AS snapshot ON snapshot.id = current.snapshot_id WHERE current.target_kind = ? AND current.target_id = ?`, t.kind, t.id).Scan(ctx, &result.ID, &result.Label, &result.ApprovedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result.Recipients = []Person{}
	if err := db.NewRaw(`SELECT person.id, person.display_name, person.sort_name FROM audience_snapshot_entries AS entry JOIN people AS person ON person.id = entry.recipient_person_id WHERE entry.snapshot_id = ? ORDER BY memento_normalize_person_name(person.sort_name), person.id`, result.ID).Scan(ctx, &result.Recipients); err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *Service) faceEvidence(ctx context.Context, momentID uuid.UUID) ([]FaceEvidence, bool) {
	if s.connector == nil {
		return []FaceEvidence{}, false
	}
	type backing struct{ MediaID, AssetID uuid.UUID }
	backings := []backing{}
	if err := s.db.NewRaw(`SELECT placement.media_item_id AS media_id, backing.immich_asset_id AS asset_id FROM draft_media_placements AS placement JOIN media_backings AS backing ON backing.media_item_id = placement.media_item_id AND backing.active WHERE placement.draft_moment_id = ? ORDER BY placement.position`, momentID).Scan(ctx, &backings); err != nil {
		return []FaceEvidence{}, false
	}
	result := []FaceEvidence{}
	for _, backing := range backings {
		faces, err := s.connector.Faces(ctx, backing.AssetID)
		if err != nil {
			return []FaceEvidence{}, false
		}
		for _, face := range faces {
			digest := sha256.Sum256([]byte(backing.MediaID.String() + ":" + face.SourceID.String()))
			evidence := FaceEvidence{MediaItemID: backing.MediaID.String(), EvidenceID: hex.EncodeToString(digest[:]), ImageWidth: face.ImageWidth, ImageHeight: face.ImageHeight, X1: face.X1, Y1: face.Y1, X2: face.X2, Y2: face.Y2}
			if face.PersonID != nil {
				var person Person
				err := s.db.NewRaw(`SELECT person.id, person.display_name, person.sort_name FROM immich_person_links AS link JOIN people AS person ON person.id = link.person_id WHERE link.immich_person_id = ? AND link.state = 'linked' AND person.archived_at IS NULL AND person.merged_at IS NULL`, *face.PersonID).Scan(ctx, &person.ID, &person.DisplayName, &person.SortName)
				switch {
				case err == nil:
					evidence.SuggestedPerson = &person
				case errors.Is(err, sql.ErrNoRows):
				default:
					return []FaceEvidence{}, false
				}
			}
			result = append(result, evidence)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].MediaItemID != result[j].MediaItemID {
			return result[i].MediaItemID < result[j].MediaItemID
		}
		return result[i].EvidenceID < result[j].EvidenceID
	})
	return result, true
}

func lockAudienceReferences(ctx context.Context, tx bun.Tx) error {
	_, err := tx.NewRaw(`SELECT pg_advisory_xact_lock(hashtextextended('memento:audiences', 0))`).Exec(ctx)
	return err
}

// LockPublishedAttendanceProjection serializes Publication with Person merges
// without serializing Publications for independent Events.
func LockPublishedAttendanceProjection(ctx context.Context, tx bun.Tx) error {
	_, err := tx.NewRaw(`SELECT pg_advisory_xact_lock_shared(hashtextextended('memento:audiences', 0))`).Exec(ctx)
	return err
}

// LockReferences serializes a Person merge with proposal recalculation and locks affected active references.
func LockReferences(ctx context.Context, tx bun.Tx, sourceID, survivorID uuid.UUID) error {
	if err := lockAudienceReferences(ctx, tx); err != nil {
		return err
	}
	for _, query := range []string{
		`SELECT moment_id, person_id FROM attendance WHERE person_id IN (?, ?) ORDER BY moment_id, person_id FOR UPDATE`,
		`SELECT attendance.published_moment_id, attendance.person_id
		 FROM published_attendance AS attendance
		 JOIN published_moments AS moment ON moment.id = attendance.published_moment_id
		 JOIN current_published_events AS current ON current.publication_id = moment.publication_id
		 WHERE attendance.person_id IN (?, ?)
		 ORDER BY attendance.published_moment_id, attendance.person_id FOR UPDATE OF attendance`,
		`SELECT target_kind, target_id, recipient_person_id FROM audience_overrides WHERE recipient_person_id IN (?, ?) ORDER BY target_kind, target_id, recipient_person_id FOR UPDATE`,
		`SELECT id FROM audience_reasons WHERE matching_person_id IN (?, ?) ORDER BY id FOR UPDATE`,
	} {
		if _, err := tx.NewRaw(query, sourceID, survivorID).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

// PreviewPersonMerge reports active Attendance and Audience references affected by a merge.
func PreviewPersonMerge(ctx context.Context, db bun.IDB, sourceID, survivorID uuid.UUID) (PersonMergeEffects, error) {
	type attendanceReference struct {
		MomentID, PersonID, ConfirmedByID uuid.UUID
		Source                            string
		ConfirmedAt                       time.Time
	}
	type publishedAttendanceReference struct {
		PublishedMomentID, PersonID uuid.UUID
	}
	type overrideReference struct {
		TargetKind            string
		TargetID, RecipientID uuid.UUID
		State                 string
		UpdatedByID           uuid.UUID
		UpdatedAt             time.Time
	}
	type reasonReference struct {
		ID                                int64
		TargetKind, Kind                  string
		TargetID, RecipientID, MatchingID uuid.UUID
	}
	attendance := make([]attendanceReference, 0)
	if err := db.NewRaw(`SELECT moment_id, person_id, source, confirmed_by_person_id AS confirmed_by_id, confirmed_at FROM attendance WHERE person_id IN (?, ?) ORDER BY moment_id, person_id`, sourceID, survivorID).Scan(ctx, &attendance); err != nil {
		return PersonMergeEffects{}, err
	}
	publishedAttendance := make([]publishedAttendanceReference, 0)
	if err := db.NewRaw(`
		SELECT attendance.published_moment_id, attendance.person_id
		FROM published_attendance AS attendance
		JOIN published_moments AS moment ON moment.id = attendance.published_moment_id
		JOIN current_published_events AS current ON current.publication_id = moment.publication_id
		WHERE attendance.person_id IN (?, ?)
		ORDER BY attendance.published_moment_id, attendance.person_id
	`, sourceID, survivorID).Scan(ctx, &publishedAttendance); err != nil {
		return PersonMergeEffects{}, err
	}
	overrides := make([]overrideReference, 0)
	if err := db.NewRaw(`SELECT target_kind, target_id, recipient_person_id AS recipient_id, state, updated_by_person_id AS updated_by_id, updated_at FROM audience_overrides WHERE recipient_person_id IN (?, ?) ORDER BY target_kind, target_id, recipient_person_id`, sourceID, survivorID).Scan(ctx, &overrides); err != nil {
		return PersonMergeEffects{}, err
	}
	reasons := make([]reasonReference, 0)
	if err := db.NewRaw(`SELECT id, target_kind, target_id, recipient_person_id AS recipient_id, kind, matching_person_id AS matching_id FROM audience_reasons WHERE matching_person_id IN (?, ?) ORDER BY id`, sourceID, survivorID).Scan(ctx, &reasons); err != nil {
		return PersonMergeEffects{}, err
	}
	effects := PersonMergeEffects{}
	for _, reference := range attendance {
		if reference.PersonID == sourceID {
			effects.AttendanceEntriesMoved++
		}
	}
	for _, reference := range publishedAttendance {
		if reference.PersonID == sourceID {
			effects.CurrentPublishedAttendanceEntriesMoved++
		}
	}
	for _, reference := range overrides {
		if reference.RecipientID == sourceID {
			effects.AudienceOverridesMoved++
		}
	}
	for _, reference := range reasons {
		if reference.MatchingID == sourceID {
			effects.AudienceReasonsMoved++
		}
	}
	encoded, err := json.Marshal(struct {
		Attendance          []attendanceReference          `json:"attendance"`
		PublishedAttendance []publishedAttendanceReference `json:"published_attendance"`
		Overrides           []overrideReference            `json:"overrides"`
		Reasons             []reasonReference              `json:"reasons"`
	}{Attendance: attendance, PublishedAttendance: publishedAttendance, Overrides: overrides, Reasons: reasons})
	if err != nil {
		return PersonMergeEffects{}, err
	}
	digest := sha256.Sum256(encoded)
	effects.ReferenceFingerprint = hex.EncodeToString(digest[:])
	return effects, nil
}

// MergePersonReferences moves editable and current-projection Attendance plus active Audience references.
// Historical Publication Attendance, approved snapshots, and publication audit attribution remain attached to the original Person.
func MergePersonReferences(ctx context.Context, tx bun.Tx, sourceID, survivorID uuid.UUID) (PersonMergeEffects, error) {
	if sourceID == survivorID {
		return PersonMergeEffects{}, ErrInvalid
	}
	if err := LockReferences(ctx, tx, sourceID, survivorID); err != nil {
		return PersonMergeEffects{}, err
	}
	effects, err := PreviewPersonMerge(ctx, tx, sourceID, survivorID)
	if err != nil {
		return PersonMergeEffects{}, err
	}
	if _, err := tx.NewRaw(`INSERT INTO attendance (moment_id, person_id, source, confirmed_by_person_id, confirmed_at) SELECT moment_id, ?, source, confirmed_by_person_id, confirmed_at FROM attendance WHERE person_id = ? ON CONFLICT (moment_id, person_id) DO NOTHING`, survivorID, sourceID).Exec(ctx); err != nil {
		return PersonMergeEffects{}, err
	}
	if _, err := tx.NewRaw(`DELETE FROM attendance WHERE person_id = ?`, sourceID).Exec(ctx); err != nil {
		return PersonMergeEffects{}, err
	}
	if _, err := tx.NewRaw(`
		DELETE FROM published_attendance AS source_attendance
		USING published_moments AS moment, current_published_events AS current
		WHERE source_attendance.published_moment_id = moment.id
		  AND current.publication_id = moment.publication_id
		  AND source_attendance.person_id = ?
		  AND EXISTS (
			SELECT 1 FROM published_attendance AS survivor_attendance
			WHERE survivor_attendance.published_moment_id = source_attendance.published_moment_id
			  AND survivor_attendance.person_id = ?
		  )
	`, sourceID, survivorID).Exec(ctx); err != nil {
		return PersonMergeEffects{}, err
	}
	if _, err := tx.NewRaw(`
		UPDATE published_attendance AS attendance
		SET person_id = ?
		FROM published_moments AS moment, current_published_events AS current
		WHERE attendance.published_moment_id = moment.id
		  AND current.publication_id = moment.publication_id
		  AND attendance.person_id = ?
	`, survivorID, sourceID).Exec(ctx); err != nil {
		return PersonMergeEffects{}, err
	}
	if _, err := tx.NewRaw(`
		INSERT INTO audience_overrides (target_kind, target_id, recipient_person_id, state, updated_by_person_id, updated_at)
		SELECT target_kind, target_id, ?, state, updated_by_person_id, updated_at
		FROM audience_overrides WHERE recipient_person_id = ?
		ON CONFLICT (target_kind, target_id, recipient_person_id) DO UPDATE SET
			state = CASE WHEN EXCLUDED.updated_at >= audience_overrides.updated_at THEN EXCLUDED.state ELSE audience_overrides.state END,
			updated_by_person_id = CASE WHEN EXCLUDED.updated_at >= audience_overrides.updated_at THEN EXCLUDED.updated_by_person_id ELSE audience_overrides.updated_by_person_id END,
			updated_at = GREATEST(EXCLUDED.updated_at, audience_overrides.updated_at)
	`, survivorID, sourceID).Exec(ctx); err != nil {
		return PersonMergeEffects{}, err
	}
	if _, err := tx.NewRaw(`DELETE FROM audience_overrides WHERE recipient_person_id = ?`, sourceID).Exec(ctx); err != nil {
		return PersonMergeEffects{}, err
	}
	if _, err := tx.NewRaw(`
		DELETE FROM audience_reasons AS source_reason
		WHERE source_reason.matching_person_id = ?
			AND EXISTS (
				SELECT 1 FROM audience_reasons AS survivor_reason
				WHERE survivor_reason.target_kind = source_reason.target_kind
					AND survivor_reason.target_id = source_reason.target_id
					AND survivor_reason.recipient_person_id = source_reason.recipient_person_id
					AND survivor_reason.kind = source_reason.kind
					AND survivor_reason.matching_person_id = ?
			)
	`, sourceID, survivorID).Exec(ctx); err != nil {
		return PersonMergeEffects{}, err
	}
	if _, err := tx.NewRaw(`UPDATE audience_reasons SET matching_person_id = ? WHERE matching_person_id = ?`, survivorID, sourceID).Exec(ctx); err != nil {
		return PersonMergeEffects{}, err
	}
	return effects, nil
}

func appendPublicationAudit(ctx context.Context, tx bun.Tx, actor setup.CuratorSession, t target, action string, metadata map[string]any) error {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	var eventID uuid.NullUUID
	if t.kind == targetMoment {
		if err := tx.NewRaw(`SELECT event_id FROM draft_moments WHERE id = ?`, t.id).Scan(ctx, &eventID); err != nil {
			return err
		}
	}
	_, err = tx.NewRaw(`INSERT INTO publication_audit_events (event_id, target_kind, target_id, actor_person_id, action, metadata) VALUES (?, ?, ?, ?, ?, ?::jsonb)`, eventID, t.kind, t.id, actor.PersonID, action, string(encoded)).Exec(ctx)
	return err
}
