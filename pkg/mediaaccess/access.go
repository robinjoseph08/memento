// Package mediaaccess centralizes current item-level interaction authorization.
package mediaaccess

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/internal/placementlock"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/uptrace/bun"
)

var ErrNotFound = errors.New("authorized Media item not found")

// Require verifies the persisted Session actor and current Media entitlement.
// The Curator has authority over every portal Media identity independently of Audiences.
func Require(ctx context.Context, db bun.IDB, actor setup.SessionActor, mediaID uuid.UUID) error {
	var authorized bool
	if err := db.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM sessions AS session
		JOIN people AS person
		  ON person.id = session.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
		JOIN recipient_access_generations AS access
		  ON access.id = session.recipient_access_generation_id
		 AND access.person_id = session.person_id AND access.is_current AND access.state = 'completed'
		JOIN system_settings AS settings
		  ON settings.id = 1 AND settings.setup_complete AND NOT settings.recovery_hold
		 AND settings.security_epoch = session.security_epoch
		JOIN media_items AS media ON media.id = ?
		WHERE session.id = ? AND session.person_id = ? AND session.recipient_access_generation_id = ?
		  AND session.revoked_at IS NULL
		  AND ((session.session_type = 'trusted' AND session.idle_expires_at > now())
		    OR (session.session_type = 'public' AND session.absolute_expires_at > now()))
		  AND (
		    EXISTS (SELECT 1 FROM person_roles WHERE person_id = session.person_id AND role = 'curator')
		    OR EXISTS (
		      SELECT 1 FROM current_media_entitlements AS entitlement
		      WHERE entitlement.recipient_access_generation_id = session.recipient_access_generation_id
		        AND entitlement.media_item_id = media.id
		    )
		  )
	)`, mediaID, actor.SessionID, actor.PersonID, actor.AccessID).Scan(ctx, &authorized); err != nil {
		return err
	}
	if !authorized {
		return ErrNotFound
	}
	return nil
}

// RequireForMutation protects a successful authorization through transaction commit.
// Its lock order matches Media delivery and lifecycle writers: current placements,
// singleton settings, Person, access generation, then Session.
func RequireForMutation(ctx context.Context, tx bun.Tx, actor setup.SessionActor, mediaID uuid.UUID) error {
	if err := placementlock.Acquire(ctx, tx, placementlock.Shared); err != nil {
		return err
	}
	locks := []struct {
		query string
		args  []any
	}{
		{`SELECT id FROM system_settings WHERE id = 1 FOR SHARE`, nil},
		{`SELECT id FROM people WHERE id = ? FOR SHARE`, []any{actor.PersonID}},
		{`SELECT id FROM recipient_access_generations WHERE id = ? FOR SHARE`, []any{actor.AccessID}},
		{`SELECT id FROM sessions WHERE id = ? FOR SHARE`, []any{actor.SessionID}},
	}
	for _, lock := range locks {
		var id any
		if err := tx.NewRaw(lock.query, lock.args...).Scan(ctx, &id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
	}
	return Require(ctx, tx, actor, mediaID)
}

// LockGenerationForCommit orders a generation-scoped write against Person and
// Recipient lifecycle transitions. The caller must first hold the placement lock.
func LockGenerationForCommit(ctx context.Context, tx bun.Tx, accessID uuid.UUID) error {
	var personID uuid.UUID
	if err := tx.NewRaw(`SELECT person.id FROM people AS person
		JOIN recipient_access_generations AS access ON access.person_id = person.id
		WHERE access.id = ? FOR SHARE OF person`, accessID).Scan(ctx, &personID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	var lockedAccessID uuid.UUID
	if err := tx.NewRaw(`SELECT id FROM recipient_access_generations WHERE id = ? FOR SHARE`, accessID).Scan(ctx, &lockedAccessID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// GenerationCanAccess reports whether a completed current generation can see an item now.
// It is used at notification creation and handoff so access loss has immediate effect.
func GenerationCanAccess(ctx context.Context, db bun.IDB, accessID, mediaID uuid.UUID) (bool, error) {
	var authorized bool
	err := db.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM recipient_access_generations AS access
		JOIN people AS person ON person.id = access.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
		JOIN system_settings AS settings ON settings.id = 1 AND settings.setup_complete AND NOT settings.recovery_hold
		JOIN media_items AS media ON media.id = ?
		WHERE access.id = ? AND access.is_current AND access.state = 'completed'
		AND (
		  EXISTS (SELECT 1 FROM person_roles WHERE person_id = access.person_id AND role = 'curator')
		  OR EXISTS (
		    SELECT 1 FROM current_media_entitlements AS entitlement
		    WHERE entitlement.recipient_access_generation_id = access.id
		      AND entitlement.media_item_id = media.id
		  )
		)
	)`, mediaID, accessID).Scan(ctx, &authorized)
	return authorized, err
}
