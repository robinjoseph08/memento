// Package mediaaccess centralizes current item-level interaction authorization.
package mediaaccess

import (
	"context"
	"errors"

	"github.com/google/uuid"
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
		  ON settings.id = 1 AND settings.setup_complete AND settings.security_epoch = session.security_epoch
		JOIN media_items AS media ON media.id = ?
		WHERE session.id = ? AND session.person_id = ? AND session.recipient_access_generation_id = ?
		  AND session.revoked_at IS NULL
		  AND ((session.session_type = 'trusted' AND session.idle_expires_at > now())
		    OR (session.session_type = 'public' AND session.absolute_expires_at > now()))
		  AND (
		    EXISTS (SELECT 1 FROM person_roles WHERE person_id = session.person_id AND role = 'curator')
		    OR EXISTS (
		      SELECT 1 FROM current_audience_entitlements AS entitlement
		      JOIN current_published_placements AS placement
		        ON placement.event_id = entitlement.event_id
		       AND placement.publication_id = entitlement.publication_id
		       AND placement.media_item_id = entitlement.media_item_id
		      JOIN events AS event ON event.id = placement.event_id AND event.lifecycle = 'published'
		      JOIN published_moments AS moment ON moment.id = placement.published_moment_id
		      WHERE entitlement.recipient_access_generation_id = session.recipient_access_generation_id
		        AND entitlement.media_item_id = media.id
		        AND NOT content_is_withdrawn(placement.event_id, moment.draft_moment_id, placement.media_item_id)
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

// GenerationCanAccess reports whether a completed current generation can see an item now.
// It is used at notification creation and handoff so access loss has immediate effect.
func GenerationCanAccess(ctx context.Context, db bun.IDB, accessID, mediaID uuid.UUID) (bool, error) {
	var authorized bool
	err := db.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM recipient_access_generations AS access
		JOIN people AS person ON person.id = access.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
		JOIN media_items AS media ON media.id = ?
		WHERE access.id = ? AND access.is_current AND access.state = 'completed'
		AND (
		  EXISTS (SELECT 1 FROM person_roles WHERE person_id = access.person_id AND role = 'curator')
		  OR EXISTS (
		    SELECT 1 FROM current_audience_entitlements AS entitlement
		    JOIN current_published_placements AS placement
		      ON placement.event_id = entitlement.event_id
		     AND placement.publication_id = entitlement.publication_id
		     AND placement.media_item_id = entitlement.media_item_id
		    JOIN events AS event ON event.id = placement.event_id AND event.lifecycle = 'published'
		    JOIN published_moments AS moment ON moment.id = placement.published_moment_id
		    WHERE entitlement.recipient_access_generation_id = access.id
		      AND entitlement.media_item_id = media.id
		      AND NOT content_is_withdrawn(placement.event_id, moment.draft_moment_id, placement.media_item_id)
		  )
		)
	)`, mediaID, accessID).Scan(ctx, &authorized)
	return authorized, err
}
