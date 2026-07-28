package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/internal/placementlock"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/uptrace/bun"
)

var (
	ErrWithdrawalInvalid = errors.New("Withdrawal request is invalid")
	ErrAlreadyWithdrawn  = errors.New("content is already withdrawn")
)

// WithdrawalTargetKind identifies a stable published identity that can be withdrawn.
type WithdrawalTargetKind string

const (
	WithdrawalTargetEvent  WithdrawalTargetKind = "event"
	WithdrawalTargetMoment WithdrawalTargetKind = "moment"
	WithdrawalTargetMedia  WithdrawalTargetKind = "media"
)

// WithdrawalStep identifies a transactional write boundary used by rollback tests.
type WithdrawalStep string

const (
	WithdrawalStepTargeted    WithdrawalStep = "targeted"
	WithdrawalStepLocked      WithdrawalStep = "locked"
	WithdrawalStepRecorded    WithdrawalStep = "recorded"
	WithdrawalStepProjections WithdrawalStep = "projections"
	WithdrawalStepActivity    WithdrawalStep = "activity"
	WithdrawalStepDelivery    WithdrawalStep = "delivery"
	WithdrawalStepReviews     WithdrawalStep = "reviews"
	WithdrawalStepAudit       WithdrawalStep = "audit"
)

func (kind WithdrawalTargetKind) valid() bool {
	switch kind {
	case WithdrawalTargetEvent, WithdrawalTargetMoment, WithdrawalTargetMedia:
		return true
	default:
		return false
	}
}

func (kind WithdrawalTargetKind) placementPredicate() string {
	switch kind {
	case WithdrawalTargetEvent:
		return "placement.event_id = ?"
	case WithdrawalTargetMoment:
		return "moment.draft_moment_id = ?"
	case WithdrawalTargetMedia:
		return "placement.media_item_id = ?"
	default:
		return "FALSE"
	}
}

// WithdrawalTarget describes one currently published stable identity available to withdraw.
type WithdrawalTarget struct {
	TargetKind WithdrawalTargetKind `json:"target_kind"`
	TargetID   string               `json:"target_id"`
	Label      string               `json:"label"`
}

// WithdrawRequest identifies one published stable identity and records why access is removed.
type WithdrawRequest struct {
	TargetKind WithdrawalTargetKind `json:"target_kind" validate:"required"`
	TargetID   string               `json:"target_id" validate:"required"`
	Reason     string               `json:"reason" validate:"required,max=1000" mod:"trim"`
}

// Withdrawal confirms the durable access removal while retaining the target identity.
type Withdrawal struct {
	ID                      string               `json:"id"`
	TargetKind              WithdrawalTargetKind `json:"target_kind"`
	TargetID                string               `json:"target_id"`
	Reason                  string               `json:"reason"`
	WithdrawnByName         string               `json:"withdrawn_by_name"`
	WithdrawnAt             time.Time            `json:"withdrawn_at"`
	RestoredByPublicationID *string              `json:"restored_by_publication_id" tstype:"string | null,required"`
	RestoredAt              *time.Time           `json:"restored_at" tstype:"string | null,required"`
	AffectedRecipientCount  int                  `json:"affected_recipient_count"`
	AffectedMediaCount      int                  `json:"affected_media_count"`
	AffectedEventCount      int                  `json:"affected_event_count"`
}

func (s *Service) withdrawalBoundary(step WithdrawalStep) error {
	if s.failWithdrawalStep == nil {
		return nil
	}
	return s.failWithdrawalStep(step)
}

// Withdraw immediately denies Recipient access and invalidates the Audience reviews required for restoration.
func (s *Service) Withdraw(ctx context.Context, actor setup.CuratorSession, request WithdrawRequest) (Withdrawal, error) {
	kind := request.TargetKind
	targetID, err := uuid.Parse(request.TargetID)
	reason := strings.TrimSpace(request.Reason)
	if err != nil || targetID == uuid.Nil || !kind.valid() || reason == "" || utf8.RuneCountInString(reason) > 1000 {
		return Withdrawal{}, ErrWithdrawalInvalid
	}

	now := s.now().UTC()
	withdrawalID := uuid.New()
	result := Withdrawal{
		ID: withdrawalID.String(), TargetKind: kind, TargetID: targetID.String(),
		Reason: reason, WithdrawnAt: now,
	}
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var curator bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM person_roles WHERE person_id = ? AND role = 'curator')`, actor.PersonID).Scan(ctx, &curator); err != nil {
			return err
		}
		if !curator {
			return setup.ErrNotCurator
		}
		if _, err := tx.NewRaw(`SELECT pg_advisory_xact_lock(hashtextextended(? || ':' || ?, 0))`, kind, targetID.String()).Exec(ctx); err != nil {
			return err
		}
		// Exclude Publication placement changes from discovery through commit. A
		// Publication already in flight completes first; later Publications wait
		// and observe the committed Withdrawal.
		if err := placementlock.Acquire(ctx, tx, placementlock.Exclusive); err != nil {
			return err
		}
		var active bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM content_withdrawals WHERE target_kind = ? AND target_id = ? AND restored_at IS NULL)`, kind, targetID).Scan(ctx, &active); err != nil {
			return err
		}
		if active {
			return ErrAlreadyWithdrawn
		}

		targetFilter := kind.placementPredicate()
		selectedPlacements := `SELECT placement.event_id, placement.media_item_id, moment.draft_moment_id
			FROM current_published_placements AS placement
			JOIN published_moments AS moment ON moment.id = placement.published_moment_id
			WHERE ` + targetFilter

		var eventIDs []uuid.UUID
		if err := tx.NewRaw(`SELECT DISTINCT placement.event_id
			FROM current_published_placements AS placement
			JOIN published_moments AS moment ON moment.id = placement.published_moment_id
			WHERE `+targetFilter+` ORDER BY placement.event_id`, targetID).Scan(ctx, &eventIDs); err != nil {
			return err
		}
		if len(eventIDs) == 0 {
			return ErrNotFound
		}
		if err := s.withdrawalBoundary(WithdrawalStepTargeted); err != nil {
			return err
		}
		var lockedEventIDs []uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM events WHERE id IN (?) ORDER BY id FOR UPDATE`, bun.List(eventIDs)).Scan(ctx, &lockedEventIDs); err != nil {
			return err
		}
		if len(lockedEventIDs) != len(eventIDs) {
			return ErrNotFound
		}

		var currentEventIDs []uuid.UUID
		if err := tx.NewRaw(`SELECT DISTINCT placement.event_id
			FROM current_published_placements AS placement
			JOIN published_moments AS moment ON moment.id = placement.published_moment_id
			WHERE `+targetFilter+` ORDER BY placement.event_id`, targetID).Scan(ctx, &currentEventIDs); err != nil {
			return err
		}
		if len(currentEventIDs) == 0 {
			return ErrNotFound
		}
		lockedEvents := make(map[uuid.UUID]struct{}, len(lockedEventIDs))
		for _, eventID := range lockedEventIDs {
			lockedEvents[eventID] = struct{}{}
		}
		for _, eventID := range currentEventIDs {
			if _, locked := lockedEvents[eventID]; !locked {
				return ErrVersionConflict
			}
		}
		eventIDs = currentEventIDs

		var hasVisiblePlacement bool
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM (`+selectedPlacements+`) AS selected
			WHERE NOT content_is_withdrawn(
				selected.event_id, selected.draft_moment_id, selected.media_item_id
			)
		)`, targetID).Scan(ctx, &hasVisiblePlacement); err != nil {
			return err
		}
		if !hasVisiblePlacement {
			return ErrAlreadyWithdrawn
		}

		if err := tx.NewRaw(`WITH selected AS (`+selectedPlacements+`
		), visible AS (
			SELECT selected.event_id, selected.media_item_id, entitlement.recipient_access_generation_id
			FROM selected
			JOIN current_audience_entitlements AS entitlement
			  ON entitlement.event_id = selected.event_id AND entitlement.media_item_id = selected.media_item_id
			JOIN recipient_access_generations AS access
			  ON access.id = entitlement.recipient_access_generation_id
			 AND access.is_current AND access.state = 'completed'
			WHERE NOT content_is_withdrawn(selected.event_id, selected.draft_moment_id, selected.media_item_id)
		)
		SELECT count(DISTINCT recipient_access_generation_id)::integer,
		       count(DISTINCT media_item_id)::integer, count(DISTINCT event_id)::integer
		FROM visible`, targetID).Scan(ctx,
			&result.AffectedRecipientCount, &result.AffectedMediaCount, &result.AffectedEventCount); err != nil {
			return err
		}
		var affectedAccessIDs []uuid.UUID
		if err := tx.NewRaw(`WITH selected AS (`+selectedPlacements+`)
			SELECT DISTINCT entitlement.recipient_access_generation_id
			FROM selected
			JOIN current_audience_entitlements AS entitlement
			  ON entitlement.event_id = selected.event_id AND entitlement.media_item_id = selected.media_item_id
			ORDER BY entitlement.recipient_access_generation_id`, targetID).Scan(ctx, &affectedAccessIDs); err != nil {
			return err
		}

		var momentIDs []uuid.UUID
		switch kind {
		case WithdrawalTargetEvent:
			if err := tx.NewRaw(`SELECT id FROM draft_moments WHERE event_id = ? ORDER BY id FOR UPDATE`, targetID).Scan(ctx, &momentIDs); err != nil {
				return err
			}
		case WithdrawalTargetMoment:
			if err := tx.NewRaw(`SELECT id FROM draft_moments WHERE id = ? FOR UPDATE`, targetID).Scan(ctx, &momentIDs); err != nil {
				return err
			}
		case WithdrawalTargetMedia:
			if err := tx.NewRaw(`SELECT moment.id FROM draft_media_placements AS placement
				JOIN draft_moments AS moment ON moment.id = placement.draft_moment_id
				WHERE placement.media_item_id = ? ORDER BY moment.id FOR UPDATE OF moment`, targetID).Scan(ctx, &momentIDs); err != nil {
				return err
			}
		}
		if err := s.withdrawalBoundary(WithdrawalStepLocked); err != nil {
			return err
		}

		var contentRevision int64
		if err := tx.NewRaw(`UPDATE system_settings SET content_revision = content_revision + 1 WHERE id = 1 RETURNING content_revision`).Scan(ctx, &contentRevision); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO content_withdrawals (
			id, target_kind, target_id, reason, withdrawn_by_person_id, withdrawn_at, content_revision
		) VALUES (?, ?, ?, ?, ?, ?, ?)`, withdrawalID, kind, targetID, reason, actor.PersonID, now, contentRevision).Exec(ctx); err != nil {
			return err
		}
		if err := s.withdrawalBoundary(WithdrawalStepRecorded); err != nil {
			return err
		}

		for _, statement := range []string{
			`WITH selected AS (` + selectedPlacements + `)
			 DELETE FROM published_search_documents AS document USING selected
			 WHERE document.event_id = selected.event_id AND document.media_item_id = selected.media_item_id`,
			`WITH selected AS (` + selectedPlacements + `)
			 DELETE FROM current_audience_entitlements AS entitlement USING selected
			 WHERE entitlement.event_id = selected.event_id AND entitlement.media_item_id = selected.media_item_id`,
		} {
			if _, err := tx.NewRaw(statement, targetID).Exec(ctx); err != nil {
				return err
			}
		}
		if _, err := tx.NewRaw(`DELETE FROM current_recipient_event_covers WHERE event_id IN (?)`, bun.List(eventIDs)).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO current_recipient_event_covers (
			event_id, recipient_access_generation_id, media_item_id
		)
		SELECT DISTINCT ON (entitlement.event_id, entitlement.recipient_access_generation_id)
		       entitlement.event_id, entitlement.recipient_access_generation_id, entitlement.media_item_id
		FROM current_audience_entitlements AS entitlement
		JOIN current_published_placements AS placement
		  ON placement.event_id = entitlement.event_id AND placement.media_item_id = entitlement.media_item_id
		JOIN media_items AS media ON media.id = entitlement.media_item_id
		JOIN published_moments AS moment ON moment.id = placement.published_moment_id
		WHERE entitlement.event_id IN (?)
		ORDER BY entitlement.event_id, entitlement.recipient_access_generation_id,
		         (media.availability = 'current') DESC,
		         (moment.cover_media_item_id = entitlement.media_item_id) DESC,
		         placement.position`, bun.List(eventIDs)).Exec(ctx); err != nil {
			return err
		}
		if err := s.withdrawalBoundary(WithdrawalStepProjections); err != nil {
			return err
		}
		if len(affectedAccessIDs) > 0 {
			for _, statement := range []string{
				`DELETE FROM new_for_you_entries AS entry USING publications AS publication
				 WHERE entry.publication_id = publication.id AND publication.event_id IN (?)
				   AND entry.recipient_access_generation_id IN (?)
				   AND NOT EXISTS (
					SELECT 1 FROM current_audience_entitlements AS remaining
					WHERE remaining.event_id = publication.event_id
					  AND remaining.recipient_access_generation_id = entry.recipient_access_generation_id
				   )`,
				`DELETE FROM publication_activity_items AS activity USING publications AS publication
				 WHERE activity.publication_id = publication.id AND publication.event_id IN (?)
				   AND activity.recipient_access_generation_id IN (?)
				   AND NOT EXISTS (
					SELECT 1 FROM current_audience_entitlements AS remaining
					WHERE remaining.event_id = publication.event_id
					  AND remaining.recipient_access_generation_id = activity.recipient_access_generation_id
				   )`,
			} {
				if _, err := tx.NewRaw(statement, bun.List(eventIDs), bun.List(affectedAccessIDs)).Exec(ctx); err != nil {
					return err
				}
			}
		}
		if err := s.withdrawalBoundary(WithdrawalStepActivity); err != nil {
			return err
		}
		eventIDStrings := make([]string, len(eventIDs))
		for index, eventID := range eventIDs {
			eventIDStrings[index] = eventID.String()
		}
		if _, err := tx.NewRaw(`UPDATE outbox_events
			SET delivered_at = ?, lease_owner = NULL, lease_expires_at = NULL
			WHERE kind = 'publication_committed' AND aggregate_kind = 'event_publication'
			  AND aggregate_id IN (?) AND delivered_at IS NULL`, now, bun.List(eventIDStrings)).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE jobs
			SET status = 'failed', last_safe_error = 'publication_withdrawn', updated_at = ?
			WHERE kind = 'publication_committed' AND status = 'pending'
			  AND payload->>'event_id' IN (?)`, now, bun.List(eventIDStrings)).Exec(ctx); err != nil {
			return err
		}
		if err := s.withdrawalBoundary(WithdrawalStepDelivery); err != nil {
			return err
		}

		if len(momentIDs) > 0 {
			if _, err := tx.NewRaw(`DELETE FROM current_audience_snapshots WHERE target_kind = 'moment' AND target_id IN (?)`, bun.List(momentIDs)).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`UPDATE draft_moments SET audience_complete = false, review_version = review_version + 1 WHERE id IN (?)`, bun.List(momentIDs)).Exec(ctx); err != nil {
				return err
			}
		}
		if _, err := tx.NewRaw(`UPDATE events SET final_review_complete = false, version = version + 1, updated_at = ? WHERE id IN (?)`, now, bun.List(eventIDs)).Exec(ctx); err != nil {
			return err
		}
		if err := s.withdrawalBoundary(WithdrawalStepReviews); err != nil {
			return err
		}

		metadata, err := json.Marshal(map[string]any{
			"withdrawal_id": withdrawalID, "reason": reason,
			"affected_recipient_count": result.AffectedRecipientCount,
			"affected_media_count":     result.AffectedMediaCount,
			"affected_event_count":     result.AffectedEventCount,
		})
		if err != nil {
			return err
		}
		for _, eventID := range eventIDs {
			if _, err := tx.NewRaw(`INSERT INTO publication_audit_events (
				event_id, target_kind, target_id, actor_person_id, action, metadata, created_at
			) VALUES (?, ?, ?, ?, 'content_withdrawn', ?::jsonb, ?)`, eventID, kind, targetID, actor.PersonID, string(metadata), now).Exec(ctx); err != nil {
				return err
			}
		}
		if err := tx.NewRaw(`SELECT display_name FROM people WHERE id = ?`, actor.PersonID).Scan(ctx, &result.WithdrawnByName); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return setup.ErrNotCurator
			}
			return err
		}
		return s.withdrawalBoundary(WithdrawalStepAudit)
	})
	if err != nil {
		return Withdrawal{}, err
	}
	return result, nil
}

type restoredWithdrawal struct {
	ID         uuid.UUID
	TargetKind WithdrawalTargetKind
	TargetID   uuid.UUID
}

// restoreEligibleWithdrawals is called only inside a successful Publication transaction.
// A reused Media identity stays withdrawn until every current placement has a post-Withdrawal Publication.
func restoreEligibleWithdrawals(ctx context.Context, tx bun.Tx, eventID, publicationID uuid.UUID, now time.Time, actor setup.CuratorSession) error {
	var restored []restoredWithdrawal
	if err := tx.NewRaw(`UPDATE content_withdrawals AS withdrawal
		SET restored_by_publication_id = ?, restored_at = ?
		WHERE withdrawal.restored_at IS NULL AND (
			(withdrawal.target_kind = 'event' AND withdrawal.target_id = ?)
			OR (withdrawal.target_kind = 'moment' AND EXISTS (
				SELECT 1 FROM draft_moments WHERE event_id = ? AND id = withdrawal.target_id
			))
			OR (withdrawal.target_kind = 'media'
				AND EXISTS (
					SELECT 1 FROM current_published_placements
					WHERE media_item_id = withdrawal.target_id
				)
				AND NOT EXISTS (
					SELECT 1 FROM current_published_placements AS placement
					JOIN publications AS publication ON publication.id = placement.publication_id
					WHERE placement.media_item_id = withdrawal.target_id
					  AND publication.content_revision <= withdrawal.content_revision
				)
			)
		)
		RETURNING withdrawal.id, withdrawal.target_kind, withdrawal.target_id`, publicationID, now,
		eventID, eventID).Scan(ctx, &restored); err != nil {
		return err
	}
	for _, withdrawal := range restored {
		metadata, err := json.Marshal(map[string]any{
			"withdrawal_id": withdrawal.ID, "restored_by_publication_id": publicationID,
		})
		if err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO publication_audit_events (
			event_id, target_kind, target_id, actor_person_id, action, metadata, created_at
		) VALUES (?, ?, ?, ?, 'content_restored_by_publication', ?::jsonb, ?)`, eventID,
			withdrawal.TargetKind, withdrawal.TargetID, actor.PersonID, string(metadata), now).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}
