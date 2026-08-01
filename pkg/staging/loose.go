package staging

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// RefreshLoose coalesces editable Loose state against its current immutable
// Publication. Callers hold the Loose row and access-summary lock in the same
// transaction as the editable mutation.
func RefreshLoose(ctx context.Context, tx bun.Tx, looseID, publicationID uuid.UUID, now time.Time) error {
	var changed bool
	if err := tx.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM loose_items AS loose JOIN published_loose_item_revisions AS published ON published.publication_id = ?
		WHERE loose.id = ? AND (
		 (loose.title, loose.description, loose.grouping_timezone, loose.proposed_day,
		  loose.place_labels, loose.audience_complete)
		 IS DISTINCT FROM (published.title, published.description, published.grouping_timezone,
		  published.proposed_day, published.place_labels, true)
		 OR EXISTS (
		  (SELECT entry.recipient_person_id, entry.recipient_access_generation_id
		   FROM current_audience_snapshots AS current
		   JOIN audience_snapshot_entries AS entry ON entry.snapshot_id = current.snapshot_id
		   WHERE current.target_kind = 'loose_item' AND current.target_id = loose.id
		   EXCEPT
		   SELECT recipient_person_id, recipient_access_generation_id
		   FROM published_loose_audience_entries WHERE publication_id = published.publication_id)
		  UNION ALL
		  (SELECT recipient_person_id, recipient_access_generation_id
		   FROM published_loose_audience_entries WHERE publication_id = published.publication_id
		   EXCEPT
		   SELECT entry.recipient_person_id, entry.recipient_access_generation_id
		   FROM current_audience_snapshots AS current
		   JOIN audience_snapshot_entries AS entry ON entry.snapshot_id = current.snapshot_id
		   WHERE current.target_kind = 'loose_item' AND current.target_id = loose.id)
		 )
		)
	)`, publicationID, looseID).Scan(ctx, &changed); err != nil {
		return err
	}
	if !changed {
		if _, err := tx.NewRaw(`UPDATE loose_items SET current_staged_update_id = NULL WHERE id = ?`, looseID).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewRaw(`DELETE FROM loose_staged_updates WHERE loose_item_id = ?`, looseID).Exec(ctx)
		return err
	}
	var stagedID uuid.UUID
	if err := tx.NewRaw(`INSERT INTO loose_staged_updates
		(id, loose_item_id, base_publication_id, net_changes, created_at, updated_at)
		VALUES (gen_random_uuid(), ?, ?, '{"kind":"loose_item_correction"}'::jsonb, ?, ?)
		ON CONFLICT (loose_item_id) DO UPDATE SET base_publication_id = EXCLUDED.base_publication_id,
			net_changes = EXCLUDED.net_changes, updated_at = EXCLUDED.updated_at RETURNING id`,
		looseID, publicationID, now, now).Scan(ctx, &stagedID); err != nil {
		return err
	}
	_, err := tx.NewRaw(`UPDATE loose_items SET current_staged_update_id = ? WHERE id = ?`, stagedID, looseID).Exec(ctx)
	return err
}
