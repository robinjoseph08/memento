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
	"github.com/robinjoseph08/memento/pkg/staging"
	"github.com/uptrace/bun"
)

var (
	ErrWithdrawalInvalid = errors.New("Withdrawal request is invalid")
	ErrAlreadyWithdrawn  = errors.New("content is already withdrawn")
)

// WithdrawalTargetKind identifies a stable published identity that can be withdrawn.
type WithdrawalTargetKind string

const (
	WithdrawalTargetEvent     WithdrawalTargetKind = "event"
	WithdrawalTargetMoment    WithdrawalTargetKind = "moment"
	WithdrawalTargetMedia     WithdrawalTargetKind = "media"
	WithdrawalTargetLooseItem WithdrawalTargetKind = "loose_item"
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
	case WithdrawalTargetEvent, WithdrawalTargetMoment, WithdrawalTargetMedia, WithdrawalTargetLooseItem:
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
	case WithdrawalTargetLooseItem:
		return "FALSE"
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
	if kind == WithdrawalTargetLooseItem {
		return s.withdrawLooseItem(ctx, actor, targetID, reason, now)
	}
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
		// Withdrawal changes the global effective-entitlement union. Take the
		// replacement lock before target, placement, or Event locks so ordinary
		// Staged refreshes cannot be missed or deadlock dependent scanning.
		if err := staging.LockAccessSummaryReplacement(ctx, tx); err != nil {
			return err
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
		var looseItemIDs []uuid.UUID
		if kind == WithdrawalTargetMedia {
			if err := tx.NewRaw(`SELECT loose_item_id FROM current_published_loose_items
				WHERE media_item_id = ? ORDER BY loose_item_id`, targetID).Scan(ctx, &looseItemIDs); err != nil {
				return err
			}
		}
		if len(eventIDs) == 0 && len(looseItemIDs) == 0 {
			return ErrNotFound
		}
		if err := s.withdrawalBoundary(WithdrawalStepTargeted); err != nil {
			return err
		}
		var lockedEventIDs []uuid.UUID
		if len(eventIDs) > 0 {
			if err := tx.NewRaw(`SELECT id FROM events WHERE id IN (?) ORDER BY id FOR UPDATE`, bun.List(eventIDs)).Scan(ctx, &lockedEventIDs); err != nil {
				return err
			}
			if len(lockedEventIDs) != len(eventIDs) {
				return ErrNotFound
			}
		}

		var currentEventIDs []uuid.UUID
		if err := tx.NewRaw(`SELECT DISTINCT placement.event_id
			FROM current_published_placements AS placement
			JOIN published_moments AS moment ON moment.id = placement.published_moment_id
			WHERE `+targetFilter+` ORDER BY placement.event_id`, targetID).Scan(ctx, &currentEventIDs); err != nil {
			return err
		}
		if len(currentEventIDs) == 0 && len(looseItemIDs) == 0 {
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
		if len(eventIDs) > 0 {
			if err := tx.NewRaw(`SELECT EXISTS (
				SELECT 1 FROM (`+selectedPlacements+`) AS selected
				WHERE NOT content_is_withdrawn(
					selected.event_id, selected.draft_moment_id, selected.media_item_id
				)
			)`, targetID).Scan(ctx, &hasVisiblePlacement); err != nil {
				return err
			}
		}
		if !hasVisiblePlacement && kind == WithdrawalTargetMedia && len(looseItemIDs) > 0 {
			if err := tx.NewRaw(`SELECT EXISTS (
				SELECT 1 FROM current_published_loose_items AS current
				WHERE current.loose_item_id IN (?)
				  AND NOT EXISTS (
					SELECT 1 FROM content_withdrawals AS withdrawal
					WHERE withdrawal.restored_at IS NULL
					  AND withdrawal.target_kind = 'loose_item'
					  AND withdrawal.target_id = current.loose_item_id
				  )
			)`, bun.List(looseItemIDs)).Scan(ctx, &hasVisiblePlacement); err != nil {
				return err
			}
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
			if err := tx.NewRaw(`SELECT id FROM draft_moments WHERE event_id = ?
				UNION
				SELECT published.draft_moment_id
				FROM current_published_placements AS placement
				JOIN published_moments AS published ON published.id = placement.published_moment_id
				WHERE placement.event_id = ?
				ORDER BY 1`, targetID, targetID).Scan(ctx, &momentIDs); err != nil {
				return err
			}
		case WithdrawalTargetMoment:
			momentIDs = []uuid.UUID{targetID}
		case WithdrawalTargetMedia:
			if err := tx.NewRaw(`SELECT moment.id
				FROM draft_media_placements AS placement
				JOIN draft_moments AS moment ON moment.id = placement.draft_moment_id
				WHERE placement.media_item_id = ?
				UNION
				SELECT published.draft_moment_id
				FROM current_published_placements AS placement
				JOIN published_moments AS published ON published.id = placement.published_moment_id
				WHERE placement.media_item_id = ?
				ORDER BY 1`, targetID, targetID).Scan(ctx, &momentIDs); err != nil {
				return err
			}
			if len(looseItemIDs) > 0 {
				var lockedLooseItemIDs []uuid.UUID
				if err := tx.NewRaw(`SELECT id FROM loose_items WHERE id IN (?) ORDER BY id FOR UPDATE`,
					bun.List(looseItemIDs)).Scan(ctx, &lockedLooseItemIDs); err != nil {
					return err
				}
				if len(lockedLooseItemIDs) != len(looseItemIDs) {
					return ErrVersionConflict
				}
			}
		case WithdrawalTargetLooseItem:
			return ErrWithdrawalInvalid
		}
		if len(momentIDs) > 0 {
			if _, err := tx.NewRaw(`SELECT id FROM draft_moments WHERE id IN (?) ORDER BY id FOR UPDATE`, bun.List(momentIDs)).Exec(ctx); err != nil {
				return err
			}
		}
		if kind == WithdrawalTargetMedia {
			result.AffectedMediaCount = 1
			if err := tx.NewRaw(`SELECT count(DISTINCT entitlement.recipient_access_generation_id)::integer
				FROM current_media_entitlements AS entitlement
				JOIN recipient_access_generations AS access
				  ON access.id = entitlement.recipient_access_generation_id
				 AND access.is_current AND access.state = 'completed'
				WHERE entitlement.media_item_id = ?`, targetID).Scan(ctx, &result.AffectedRecipientCount); err != nil {
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

		if len(eventIDs) > 0 {
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
		if len(looseItemIDs) > 0 {
			if _, err := tx.NewRaw(`DELETE FROM new_for_you_entries AS entry USING publications AS publication
				WHERE entry.publication_id = publication.id AND publication.loose_item_id IN (?)`, bun.List(looseItemIDs)).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`DELETE FROM publication_activity_items AS activity USING publications AS publication
				WHERE activity.publication_id = publication.id AND publication.loose_item_id IN (?)`, bun.List(looseItemIDs)).Exec(ctx); err != nil {
				return err
			}
		}
		if err := s.withdrawalBoundary(WithdrawalStepActivity); err != nil {
			return err
		}
		if len(eventIDs) > 0 {
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
		}
		if len(looseItemIDs) > 0 {
			looseIDStrings := make([]string, len(looseItemIDs))
			for index, looseID := range looseItemIDs {
				looseIDStrings[index] = looseID.String()
			}
			if _, err := tx.NewRaw(`UPDATE outbox_events
				SET delivered_at = ?, lease_owner = NULL, lease_expires_at = NULL
				WHERE kind = 'publication_committed' AND aggregate_kind = 'loose_item_publication'
				  AND aggregate_id IN (?) AND delivered_at IS NULL`, now, bun.List(looseIDStrings)).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`UPDATE jobs
				SET status = 'failed', last_safe_error = 'publication_withdrawn', updated_at = ?
				WHERE kind = 'publication_committed' AND status = 'pending'
				  AND payload->>'loose_item_id' IN (?)`, now, bun.List(looseIDStrings)).Exec(ctx); err != nil {
				return err
			}
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
		if len(eventIDs) > 0 {
			if _, err := tx.NewRaw(`UPDATE events SET final_review_complete = false, version = version + 1, updated_at = ? WHERE id IN (?)`, now, bun.List(eventIDs)).Exec(ctx); err != nil {
				return err
			}
		}
		if len(looseItemIDs) > 0 {
			if _, err := tx.NewRaw(`DELETE FROM current_audience_snapshots
				WHERE target_kind = 'loose_item' AND target_id IN (?)`, bun.List(looseItemIDs)).Exec(ctx); err != nil {
				return err
			}
			for _, looseID := range looseItemIDs {
				var currentPublicationID uuid.UUID
				if err := tx.NewRaw(`UPDATE loose_items SET audience_complete = false,
					review_version = review_version + 1, version = version + 1, updated_at = ?
					WHERE id = ? RETURNING current_publication_id`, now, looseID).Scan(ctx, &currentPublicationID); err != nil {
					return err
				}
				if err := refreshLooseStagedUpdate(ctx, tx, looseID, currentPublicationID, now); err != nil {
					return err
				}
			}
		}
		// The affected Events already received a new version for Withdrawal review.
		// Refresh them first, then invalidate any other Staged Event whose effective
		// access impact changed when the entitlement rows were removed.
		for _, eventID := range eventIDs {
			if _, err := staging.Refresh(ctx, tx, eventID, now); err != nil {
				return err
			}
		}
		if err := staging.RefreshDependentAccessUpdates(ctx, tx, uuid.Nil, now); err != nil {
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
		if len(eventIDs) == 0 {
			if _, err := tx.NewRaw(`INSERT INTO publication_audit_events (
				event_id, target_kind, target_id, actor_person_id, action, metadata, created_at
			) VALUES (NULL, ?, ?, ?, 'content_withdrawn', ?::jsonb, ?)`, kind, targetID,
				actor.PersonID, string(metadata), now).Exec(ctx); err != nil {
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

func (s *Service) withdrawLooseItem(ctx context.Context, actor setup.CuratorSession, looseID uuid.UUID, reason string, now time.Time) (Withdrawal, error) {
	withdrawalID := uuid.New()
	result := Withdrawal{ID: withdrawalID.String(), TargetKind: WithdrawalTargetLooseItem,
		TargetID: looseID.String(), Reason: reason, WithdrawnAt: now}
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := requireCurator(ctx, tx, actor.PersonID); err != nil {
			return err
		}
		if err := staging.LockAccessSummaryReplacement(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`SELECT pg_advisory_xact_lock(hashtextextended('loose_item:' || ?, 0))`, looseID.String()).Exec(ctx); err != nil {
			return err
		}
		if err := placementlock.Acquire(ctx, tx, placementlock.Exclusive); err != nil {
			return err
		}
		var publicationID, mediaID uuid.UUID
		if err := tx.NewRaw(`SELECT current_publication_id, media_item_id FROM loose_items
			WHERE id = ? AND lifecycle = 'published' FOR UPDATE`, looseID).Scan(ctx, &publicationID, &mediaID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		var active bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM content_withdrawals
			WHERE restored_at IS NULL AND (
			 (target_kind = 'loose_item' AND target_id = ?)
			 OR (target_kind = 'media' AND target_id = ?)))`, looseID, mediaID).Scan(ctx, &active); err != nil {
			return err
		}
		if active {
			return ErrAlreadyWithdrawn
		}
		if err := s.withdrawalBoundary(WithdrawalStepTargeted); err != nil {
			return err
		}
		if err := s.withdrawalBoundary(WithdrawalStepLocked); err != nil {
			return err
		}
		if err := tx.NewRaw(`SELECT count(DISTINCT entitlement.recipient_access_generation_id)::integer
			FROM current_loose_item_entitlements AS entitlement JOIN recipient_access_generations AS access
			ON access.id=entitlement.recipient_access_generation_id AND access.is_current AND access.state='completed'
			WHERE entitlement.loose_item_id=?`, looseID).Scan(ctx, &result.AffectedRecipientCount); err != nil {
			return err
		}
		result.AffectedMediaCount = 1
		var contentRevision int64
		if err := tx.NewRaw(`UPDATE system_settings SET content_revision=content_revision+1 WHERE id=1 RETURNING content_revision`).Scan(ctx, &contentRevision); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO content_withdrawals (id,target_kind,target_id,reason,withdrawn_by_person_id,withdrawn_at,content_revision)
			VALUES (?, 'loose_item', ?, ?, ?, ?, ?)`, withdrawalID, looseID, reason, actor.PersonID, now, contentRevision).Exec(ctx); err != nil {
			return err
		}
		if err := s.withdrawalBoundary(WithdrawalStepRecorded); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM published_loose_search_documents WHERE loose_item_id=?`, looseID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM current_loose_item_entitlements WHERE loose_item_id=?`, looseID).Exec(ctx); err != nil {
			return err
		}
		if err := s.withdrawalBoundary(WithdrawalStepProjections); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM new_for_you_entries WHERE publication_id=?`, publicationID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM publication_activity_items WHERE publication_id=?`, publicationID).Exec(ctx); err != nil {
			return err
		}
		if err := s.withdrawalBoundary(WithdrawalStepActivity); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE outbox_events SET delivered_at=?, lease_owner=NULL, lease_expires_at=NULL
			WHERE kind='publication_committed' AND aggregate_kind='loose_item_publication' AND aggregate_id=? AND delivered_at IS NULL`, now, looseID.String()).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE jobs SET status='failed', last_safe_error='publication_withdrawn', updated_at=?
			WHERE kind='publication_committed' AND status='pending' AND payload->>'loose_item_id'=?`, now, looseID.String()).Exec(ctx); err != nil {
			return err
		}
		if err := s.withdrawalBoundary(WithdrawalStepDelivery); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM current_audience_snapshots WHERE target_kind='loose_item' AND target_id=?`, looseID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE loose_items SET audience_complete=false, review_version=review_version+1,
			version=version+1, updated_at=? WHERE id=?`, now, looseID).Exec(ctx); err != nil {
			return err
		}
		if err := refreshLooseStagedUpdate(ctx, tx, looseID, publicationID, now); err != nil {
			return err
		}
		if err := staging.RefreshDependentAccessUpdates(ctx, tx, uuid.Nil, now); err != nil {
			return err
		}
		if err := s.withdrawalBoundary(WithdrawalStepReviews); err != nil {
			return err
		}
		metadata, _ := json.Marshal(map[string]any{"withdrawal_id": withdrawalID, "reason": reason,
			"affected_recipient_count": result.AffectedRecipientCount, "affected_media_count": 1, "affected_event_count": 0})
		if _, err := tx.NewRaw(`INSERT INTO publication_audit_events (event_id,target_kind,target_id,actor_person_id,action,metadata,created_at)
			VALUES (NULL,'loose_item',?,?, 'content_withdrawn',?::jsonb,?)`, looseID, actor.PersonID, string(metadata), now).Exec(ctx); err != nil {
			return err
		}
		if err := tx.NewRaw(`SELECT display_name FROM people WHERE id=?`, actor.PersonID).Scan(ctx, &result.WithdrawnByName); err != nil {
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

const pendingWithdrawalPublicationSQL = `EXISTS (
	SELECT 1
	FROM content_withdrawals AS withdrawal
	WHERE withdrawal.restored_at IS NULL AND (
		(withdrawal.target_kind = 'event' AND withdrawal.target_id = ?)
		OR (withdrawal.target_kind = 'moment' AND EXISTS (
			SELECT 1 FROM draft_moments
			WHERE event_id = ? AND id = withdrawal.target_id
		))
		OR (withdrawal.target_kind = 'media' AND EXISTS (
			SELECT 1
			FROM current_published_placements AS placement
			JOIN publications AS publication ON publication.id = placement.publication_id
			WHERE placement.event_id = ?
			  AND placement.media_item_id = withdrawal.target_id
			  AND publication.content_revision <= withdrawal.content_revision
		))
	)
)`

// hasPendingWithdrawalPublication reports whether an otherwise unchanged Event
// still needs a Publication to restore content or advance a stale Media placement.
func hasPendingWithdrawalPublication(ctx context.Context, db bun.IDB, eventID uuid.UUID) (bool, error) {
	var pending bool
	err := db.NewRaw(`SELECT `+pendingWithdrawalPublicationSQL, eventID, eventID, eventID).Scan(ctx, &pending)
	return pending, err
}

// projectDeferredRestorationActivity reactivates durable handoffs for current
// origins that advanced before the final shared-Media restoration Publication.
func projectDeferredRestorationActivity(ctx context.Context, tx bun.Tx, restoringPublicationID uuid.UUID, now time.Time) error {
	if _, err := tx.NewRaw(`CREATE TEMPORARY TABLE memento_deferred_restoration_candidates
		ON COMMIT DROP AS
		WITH restored_media AS MATERIALIZED (
		 SELECT target_id AS media_item_id, content_revision FROM content_withdrawals
		 WHERE target_kind = 'media' AND restored_by_publication_id = ?
		)
		SELECT DISTINCT entitlement.publication_id, entitlement.recipient_access_generation_id,
		 entitlement.media_item_id
		FROM current_audience_entitlements AS entitlement
		JOIN publications AS publication ON publication.id = entitlement.publication_id
		JOIN recipient_access_generations AS access ON access.id = entitlement.recipient_access_generation_id
		JOIN restored_media AS restored ON restored.media_item_id = entitlement.media_item_id
		 AND publication.content_revision > restored.content_revision
		WHERE entitlement.publication_id <> ? AND access.is_current AND access.state = 'completed'
		 AND NOT EXISTS (SELECT 1 FROM memento_prior_effective_entitlements AS prior
		  WHERE prior.access_id = entitlement.recipient_access_generation_id
		   AND prior.media_item_id = entitlement.media_item_id)
		UNION
		SELECT DISTINCT entitlement.publication_id, entitlement.recipient_access_generation_id,
		 entitlement.media_item_id
		FROM current_loose_item_entitlements AS entitlement
		JOIN publications AS publication ON publication.id = entitlement.publication_id
		JOIN recipient_access_generations AS access ON access.id = entitlement.recipient_access_generation_id
		JOIN restored_media AS restored ON restored.media_item_id = entitlement.media_item_id
		 AND publication.content_revision > restored.content_revision
		WHERE entitlement.publication_id <> ? AND access.is_current AND access.state = 'completed'
		 AND NOT EXISTS (SELECT 1 FROM memento_prior_effective_entitlements AS prior
		  WHERE prior.access_id = entitlement.recipient_access_generation_id
		   AND prior.media_item_id = entitlement.media_item_id)`, restoringPublicationID,
		restoringPublicationID, restoringPublicationID).Exec(ctx); err != nil {
		return err
	}
	if _, err := tx.NewRaw(`INSERT INTO publication_activity_items
		(publication_id, recipient_access_generation_id, created_at)
		SELECT DISTINCT publication_id, recipient_access_generation_id, ?::timestamptz
		FROM memento_deferred_restoration_candidates ON CONFLICT DO NOTHING`, now).Exec(ctx); err != nil {
		return err
	}
	if _, err := tx.NewRaw(`INSERT INTO new_for_you_entries
		(recipient_access_generation_id, publication_id)
		SELECT DISTINCT recipient_access_generation_id, publication_id
		FROM memento_deferred_restoration_candidates
		ON CONFLICT (recipient_access_generation_id, publication_id)
		DO UPDATE SET seen_at = NULL`).Exec(ctx); err != nil {
		return err
	}
	if _, err := tx.NewRaw(`INSERT INTO publication_notification_media
		(publication_id, recipient_access_generation_id, media_item_id)
		SELECT publication_id, recipient_access_generation_id, media_item_id
		FROM memento_deferred_restoration_candidates ON CONFLICT DO NOTHING`).Exec(ctx); err != nil {
		return err
	}
	_, err := tx.NewRaw(`WITH deferred AS MATERIALIZED (
	 SELECT DISTINCT publication.id, publication.event_id, publication.loose_item_id,
	  publication.notify_recipients
	 FROM publications AS publication
	 JOIN memento_deferred_restoration_candidates AS candidate
	  ON candidate.publication_id = publication.id
	 WHERE publication.notify_recipients
	) INSERT INTO outbox_events
	 (kind, aggregate_kind, aggregate_id, aggregate_version, payload, available_at, created_at)
	 SELECT 'publication_committed', identity.aggregate_kind, identity.aggregate_id,
	  version.next_version,
	  jsonb_build_object('publication_id', deferred.id, 'notify_recipients', true) ||
	   CASE WHEN deferred.event_id IS NOT NULL THEN jsonb_build_object('event_id', deferred.event_id)
	    ELSE jsonb_build_object('loose_item_id', deferred.loose_item_id) END,
	  ?::timestamptz, ?::timestamptz
	 FROM deferred
	 CROSS JOIN LATERAL (SELECT
	  CASE WHEN deferred.event_id IS NOT NULL THEN 'event_publication' ELSE 'loose_item_publication' END,
	  COALESCE(deferred.event_id, deferred.loose_item_id)::text
	 ) AS identity(aggregate_kind, aggregate_id)
	 CROSS JOIN LATERAL (SELECT COALESCE(max(aggregate_version), 0) + 1 AS next_version
	  FROM outbox_events WHERE aggregate_kind = identity.aggregate_kind
	   AND aggregate_id = identity.aggregate_id AND kind = 'publication_committed') AS version`, now, now).Exec(ctx)
	return err
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
				AND (
					EXISTS (
						SELECT 1 FROM current_published_placements
						WHERE media_item_id = withdrawal.target_id
					)
					OR EXISTS (
						SELECT 1 FROM current_published_loose_items
						WHERE media_item_id = withdrawal.target_id
					)
				)
				AND NOT EXISTS (
					SELECT 1 FROM current_published_placements AS placement
					JOIN publications AS publication ON publication.id = placement.publication_id
					WHERE placement.media_item_id = withdrawal.target_id
					  AND publication.content_revision <= withdrawal.content_revision
				)
				AND NOT EXISTS (
					SELECT 1 FROM current_published_loose_items AS loose
					JOIN publications AS publication ON publication.id = loose.publication_id
					WHERE loose.media_item_id = withdrawal.target_id
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

// restoreEligibleLooseWithdrawals restores a Loose target, and restores a shared
// Media target only after every current Event and Loose origin has advanced.
func restoreEligibleLooseWithdrawals(ctx context.Context, tx bun.Tx, looseID, mediaID, publicationID uuid.UUID, now time.Time, actor setup.CuratorSession) error {
	var restored []restoredWithdrawal
	if err := tx.NewRaw(`UPDATE content_withdrawals AS withdrawal SET restored_by_publication_id=?, restored_at=?
		WHERE withdrawal.restored_at IS NULL AND (
		 (withdrawal.target_kind='loose_item' AND withdrawal.target_id=?)
		 OR (withdrawal.target_kind='media' AND withdrawal.target_id=?
		  AND NOT EXISTS (SELECT 1 FROM current_published_placements AS placement
		   JOIN publications AS publication ON publication.id=placement.publication_id
		   WHERE placement.media_item_id=withdrawal.target_id
		    AND publication.content_revision <= withdrawal.content_revision)
		  AND NOT EXISTS (SELECT 1 FROM current_published_loose_items AS loose
		   JOIN publications AS publication ON publication.id=loose.publication_id
		   WHERE loose.media_item_id=withdrawal.target_id
		    AND publication.content_revision <= withdrawal.content_revision)
		 )) RETURNING id,target_kind,target_id`, publicationID, now, looseID, mediaID).Scan(ctx, &restored); err != nil {
		return err
	}
	for _, withdrawal := range restored {
		metadata, err := json.Marshal(map[string]any{"withdrawal_id": withdrawal.ID, "restored_by_publication_id": publicationID})
		if err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO publication_audit_events
			(event_id,target_kind,target_id,actor_person_id,action,metadata,created_at)
			VALUES (NULL,?,?,?,'content_restored_by_publication',?::jsonb,?)`, withdrawal.TargetKind,
			withdrawal.TargetID, actor.PersonID, string(metadata), now).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}
