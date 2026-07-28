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
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/uptrace/bun"
)

var (
	ErrWithdrawalInvalid = errors.New("Withdrawal request is invalid")
	ErrAlreadyWithdrawn  = errors.New("content is already withdrawn")
)

// WithdrawRequest identifies one published stable identity and records why access is removed.
type WithdrawRequest struct {
	TargetKind string `json:"target_kind" validate:"required,oneof=event moment media"`
	TargetID   string `json:"target_id" validate:"required"`
	Reason     string `json:"reason" validate:"required,max=1000" mod:"trim"`
}

// Withdrawal confirms the durable access removal while retaining the target identity.
type Withdrawal struct {
	ID                      string     `json:"id"`
	TargetKind              string     `json:"target_kind"`
	TargetID                string     `json:"target_id"`
	Reason                  string     `json:"reason"`
	WithdrawnByName         string     `json:"withdrawn_by_name"`
	WithdrawnAt             time.Time  `json:"withdrawn_at"`
	RestoredByPublicationID *string    `json:"restored_by_publication_id" tstype:"string | null,required"`
	RestoredAt              *time.Time `json:"restored_at" tstype:"string | null,required"`
	AffectedRecipientCount  int        `json:"affected_recipient_count"`
	AffectedMediaCount      int        `json:"affected_media_count"`
	AffectedEventCount      int        `json:"affected_event_count"`
}

// Withdraw immediately denies Recipient access and invalidates the Audience reviews required for restoration.
func (s *Service) Withdraw(ctx context.Context, actor setup.CuratorSession, request WithdrawRequest) (Withdrawal, error) {
	kind := strings.TrimSpace(request.TargetKind)
	targetID, err := uuid.Parse(request.TargetID)
	reason := strings.TrimSpace(request.Reason)
	if err != nil || targetID == uuid.Nil || (kind != "event" && kind != "moment" && kind != "media") || reason == "" || utf8.RuneCountInString(reason) > 1000 {
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
		var active bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM content_withdrawals WHERE target_kind = ? AND target_id = ? AND restored_at IS NULL)`, kind, targetID).Scan(ctx, &active); err != nil {
			return err
		}
		if active {
			return ErrAlreadyWithdrawn
		}

		targetFilter := `(? = 'event' AND placement.event_id = ?)
			OR (? = 'moment' AND moment.draft_moment_id = ?)
			OR (? = 'media' AND placement.media_item_id = ?)`
		var exists bool
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM current_published_placements AS placement
			JOIN published_moments AS moment ON moment.id = placement.published_moment_id
			WHERE `+targetFilter+`)`, kind, targetID, kind, targetID, kind, targetID).Scan(ctx, &exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}

		if err := tx.NewRaw(`WITH selected AS (
			SELECT placement.event_id, placement.media_item_id, moment.draft_moment_id
			FROM current_published_placements AS placement
			JOIN published_moments AS moment ON moment.id = placement.published_moment_id
			WHERE `+targetFilter+`
		), visible AS (
			SELECT selected.event_id, selected.media_item_id, entitlement.recipient_access_generation_id
			FROM selected
			JOIN current_audience_entitlements AS entitlement
			  ON entitlement.event_id = selected.event_id AND entitlement.media_item_id = selected.media_item_id
			JOIN recipient_access_generations AS access
			  ON access.id = entitlement.recipient_access_generation_id
			 AND access.is_current AND access.state = 'completed'
			WHERE NOT EXISTS (SELECT 1 FROM content_withdrawals WHERE restored_at IS NULL AND target_kind = 'event' AND target_id = selected.event_id)
			  AND NOT EXISTS (SELECT 1 FROM content_withdrawals WHERE restored_at IS NULL AND target_kind = 'moment' AND target_id = selected.draft_moment_id)
			  AND NOT EXISTS (SELECT 1 FROM content_withdrawals WHERE restored_at IS NULL AND target_kind = 'media' AND target_id = selected.media_item_id)
		)
		SELECT count(DISTINCT recipient_access_generation_id)::integer,
		       count(DISTINCT media_item_id)::integer, count(DISTINCT event_id)::integer
		FROM visible`, kind, targetID, kind, targetID, kind, targetID).Scan(ctx,
			&result.AffectedRecipientCount, &result.AffectedMediaCount, &result.AffectedEventCount); err != nil {
			return err
		}

		var eventIDs []uuid.UUID
		if err := tx.NewRaw(`SELECT DISTINCT placement.event_id
			FROM current_published_placements AS placement
			JOIN published_moments AS moment ON moment.id = placement.published_moment_id
			WHERE `+targetFilter+` ORDER BY placement.event_id`, kind, targetID, kind, targetID, kind, targetID).Scan(ctx, &eventIDs); err != nil {
			return err
		}
		var lockedEventIDs []uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM events WHERE id IN (?) ORDER BY id FOR UPDATE`, bun.List(eventIDs)).Scan(ctx, &lockedEventIDs); err != nil {
			return err
		}
		if len(lockedEventIDs) != len(eventIDs) {
			return ErrNotFound
		}

		var momentIDs []uuid.UUID
		switch kind {
		case "event":
			if err := tx.NewRaw(`SELECT id FROM draft_moments WHERE event_id = ? ORDER BY id FOR UPDATE`, targetID).Scan(ctx, &momentIDs); err != nil {
				return err
			}
		case "moment":
			if err := tx.NewRaw(`SELECT id FROM draft_moments WHERE id = ? FOR UPDATE`, targetID).Scan(ctx, &momentIDs); err != nil {
				return err
			}
		case "media":
			if err := tx.NewRaw(`SELECT moment.id FROM draft_media_placements AS placement
				JOIN draft_moments AS moment ON moment.id = placement.draft_moment_id
				WHERE placement.media_item_id = ? ORDER BY moment.id FOR UPDATE OF moment`, targetID).Scan(ctx, &momentIDs); err != nil {
				return err
			}
		}
		if len(momentIDs) == 0 || len(eventIDs) == 0 {
			return ErrNotFound
		}

		if _, err := tx.NewRaw(`INSERT INTO content_withdrawals (
			id, target_kind, target_id, reason, withdrawn_by_person_id, withdrawn_at
		) VALUES (?, ?, ?, ?, ?, ?)`, withdrawalID, kind, targetID, reason, actor.PersonID, now).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM current_audience_snapshots WHERE target_kind = 'moment' AND target_id IN (?)`, bun.List(momentIDs)).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE draft_moments SET audience_complete = false, review_version = review_version + 1 WHERE id IN (?)`, bun.List(momentIDs)).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE events SET final_review_complete = false, version = version + 1, updated_at = ? WHERE id IN (?)`, now, bun.List(eventIDs)).Exec(ctx); err != nil {
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
		return nil
	})
	if err != nil {
		return Withdrawal{}, err
	}
	return result, nil
}

type restoredWithdrawal struct {
	ID         uuid.UUID
	TargetKind string
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
					WHERE event_id = ? AND publication_id = ? AND media_item_id = withdrawal.target_id
				)
				AND NOT EXISTS (
					SELECT 1 FROM current_published_placements AS placement
					JOIN current_published_events AS current
					  ON current.event_id = placement.event_id AND current.publication_id = placement.publication_id
					WHERE placement.media_item_id = withdrawal.target_id
					  AND current.committed_at <= withdrawal.withdrawn_at
				)
			)
		)
		RETURNING withdrawal.id, withdrawal.target_kind, withdrawal.target_id`, publicationID, now,
		eventID, eventID, eventID, publicationID).Scan(ctx, &restored); err != nil {
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
