// Package staging maintains the single private net update for each published Event.
package staging

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// RemovedMedia identifies a published Media item absent from the resulting Event.
type RemovedMedia struct {
	ID            string  `json:"id"`
	MediaType     string  `json:"media_type"`
	LocalDateTime *string `json:"local_date_time"`
}

// DeletedMoment identifies published structure absent from the resulting Event.
type DeletedMoment struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	ProposedDay string `json:"proposed_day"`
}

// ChangeKind identifies a supported category in a coalesced update.
type ChangeKind string

var errUnknownChangeKind = errors.New("unknown Staged change kind")

const (
	ChangeKindAddition        ChangeKind = "addition"
	ChangeKindRemoval         ChangeKind = "removal"
	ChangeKindMove            ChangeKind = "move"
	ChangeKindMetadata        ChangeKind = "metadata"
	ChangeKindMomentStructure ChangeKind = "moment_structure"
	ChangeKindAccess          ChangeKind = "access"
)

// UnmarshalJSON rejects persisted change categories that this version cannot render.
func (kind *ChangeKind) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	candidate := ChangeKind(value)
	switch candidate {
	case ChangeKindAddition, ChangeKindRemoval, ChangeKindMove, ChangeKindMetadata,
		ChangeKindMomentStructure, ChangeKindAccess:
		*kind = candidate
		return nil
	default:
		return fmt.Errorf("%w %q", errUnknownChangeKind, value)
	}
}

// Change is one category in the coalesced difference from the current Publication.
type Change struct {
	Kind           ChangeKind      `json:"kind"`
	Count          int             `json:"count"`
	MediaItemIDs   []string        `json:"media_item_ids"`
	MomentIDs      []string        `json:"moment_ids"`
	RemovedMedia   []RemovedMedia  `json:"removed_media,omitempty"`
	DeletedMoments []DeletedMoment `json:"deleted_moments,omitempty"`
	Detail         string          `json:"detail"`
}

// Update is the one private net update for a published Event.
type Update struct {
	ID                string    `json:"id"`
	BasePublicationID string    `json:"base_publication_id"`
	Changes           []Change  `json:"changes"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type stagedPlacement struct {
	MediaItemID uuid.UUID `bun:"media_item_id"`
	MomentID    uuid.UUID `bun:"moment_id"`
	Position    int       `bun:"position"`
}

// Refresh coalesces the editable Event against its current immutable
// Publication. Callers must make their editable writes and this refresh in the
// same transaction so an empty update can never enter the work queue.
func Refresh(ctx context.Context, tx bun.Tx, eventID uuid.UUID, now time.Time) (*Update, error) {
	var lifecycle string
	var publicationID *uuid.UUID
	if err := tx.NewRaw(`SELECT lifecycle, current_publication_id FROM events WHERE id = ? FOR UPDATE`, eventID).Scan(ctx, &lifecycle, &publicationID); err != nil {
		return nil, err
	}
	if lifecycle != "published" || publicationID == nil {
		if err := Clear(ctx, tx, eventID); err != nil {
			return nil, err
		}
		return nil, nil
	}

	changes, err := summarizeStagedUpdate(ctx, tx, eventID, *publicationID)
	if err != nil {
		return nil, err
	}
	if len(changes) == 0 {
		if err := Clear(ctx, tx, eventID); err != nil {
			return nil, err
		}
		return nil, nil
	}
	encoded, err := json.Marshal(changes)
	if err != nil {
		return nil, err
	}
	candidateID := uuid.New()
	var stagedID uuid.UUID
	var createdAt time.Time
	if err := tx.NewRaw(`
		INSERT INTO staged_updates (id, event_id, base_publication_id, net_changes, created_at, updated_at)
		VALUES (?, ?, ?, ?::jsonb, ?, ?)
		ON CONFLICT (event_id) DO UPDATE SET
			base_publication_id = EXCLUDED.base_publication_id,
			net_changes = EXCLUDED.net_changes,
			updated_at = EXCLUDED.updated_at
		RETURNING id, created_at
	`, candidateID, eventID, *publicationID, string(encoded), now, now).Scan(ctx, &stagedID, &createdAt); err != nil {
		return nil, fmt.Errorf("coalesce Staged update: %w", err)
	}
	if _, err := tx.NewRaw(`UPDATE events SET current_staged_update_id = ? WHERE id = ?`, stagedID, eventID).Exec(ctx); err != nil {
		return nil, err
	}
	return &Update{ID: stagedID.String(), BasePublicationID: publicationID.String(), Changes: changes, UpdatedAt: now}, nil
}

// InvalidateEvent records an externally-applied editable change and refreshes
// its coalesced update in the same transaction.
func InvalidateEvent(ctx context.Context, tx bun.Tx, eventID uuid.UUID, now time.Time) (*Update, error) {
	if _, err := tx.NewRaw(`
		UPDATE events SET version = version + 1, final_review_complete = false, updated_at = ?
		WHERE id = ?
	`, now, eventID).Exec(ctx); err != nil {
		return nil, err
	}
	return Refresh(ctx, tx, eventID, now)
}

// Clear removes an Event's coalesced update after successful Publication.
func Clear(ctx context.Context, tx bun.Tx, eventID uuid.UUID) error {
	if _, err := tx.NewRaw(`UPDATE events SET current_staged_update_id = NULL WHERE id = ?`, eventID).Exec(ctx); err != nil {
		return err
	}
	_, err := tx.NewRaw(`DELETE FROM staged_updates WHERE event_id = ?`, eventID).Exec(ctx)
	return err
}

func summarizeStagedUpdate(ctx context.Context, db bun.IDB, eventID, publicationID uuid.UUID) ([]Change, error) {
	var draft, published []stagedPlacement
	if err := db.NewRaw(`
		SELECT placement.media_item_id, COALESCE(placement.draft_moment_id, ?::uuid) AS moment_id, placement.position
		FROM draft_media_placements AS placement
		WHERE placement.event_id = ? ORDER BY placement.position, placement.media_item_id
	`, uuid.Nil, eventID).Scan(ctx, &draft); err != nil {
		return nil, err
	}
	if err := db.NewRaw(`
		SELECT placement.media_item_id, moment.draft_moment_id AS moment_id, placement.position
		FROM published_media_placements AS placement
		JOIN published_moments AS moment ON moment.id = placement.published_moment_id
		WHERE moment.publication_id = ? ORDER BY moment.position, placement.position, placement.media_item_id
	`, publicationID).Scan(ctx, &published); err != nil {
		return nil, err
	}
	draftByMedia := make(map[uuid.UUID]stagedPlacement, len(draft))
	publishedByMedia := make(map[uuid.UUID]stagedPlacement, len(published))
	for _, placement := range draft {
		draftByMedia[placement.MediaItemID] = placement
	}
	for _, placement := range published {
		publishedByMedia[placement.MediaItemID] = placement
	}
	additions, removals, moves := make([]string, 0), make([]string, 0), make([]string, 0)
	for _, placement := range draft {
		prior, exists := publishedByMedia[placement.MediaItemID]
		if !exists {
			additions = append(additions, placement.MediaItemID.String())
		} else if prior.MomentID != placement.MomentID || prior.Position != placement.Position {
			moves = append(moves, placement.MediaItemID.String())
		}
	}
	for _, placement := range published {
		if _, exists := draftByMedia[placement.MediaItemID]; !exists {
			removals = append(removals, placement.MediaItemID.String())
		}
	}
	var removedMedia []RemovedMedia
	if err := db.NewRaw(`
		SELECT placement.media_item_id AS id, placement.media_type, placement.local_date_time
		FROM published_media_placements AS placement
		JOIN published_moments AS moment ON moment.id = placement.published_moment_id
		WHERE moment.publication_id = ?
		  AND NOT EXISTS (
			SELECT 1 FROM draft_media_placements AS editable
			WHERE editable.event_id = ? AND editable.media_item_id = placement.media_item_id
		  )
		ORDER BY moment.position, placement.position, placement.media_item_id
	`, publicationID, eventID).Scan(ctx, &removedMedia); err != nil {
		return nil, err
	}

	var eventMetadataChanged bool
	if err := db.NewRaw(`
		SELECT EXISTS (
			SELECT 1 FROM events AS editable
			JOIN published_event_revisions AS published ON published.publication_id = ?
			WHERE editable.id = ? AND (editable.title, editable.description, editable.grouping_timezone)
				IS DISTINCT FROM (published.title, published.description, published.grouping_timezone)
		)
	`, publicationID, eventID).Scan(ctx, &eventMetadataChanged); err != nil {
		return nil, err
	}
	var metadataMedia []uuid.UUID
	if err := db.NewRaw(`
		SELECT media.id
		FROM draft_media_placements AS editable
		JOIN media_items AS media ON media.id = editable.media_item_id
		JOIN current_published_placements AS current
		  ON current.event_id = editable.event_id AND current.media_item_id = editable.media_item_id
		JOIN published_media_placements AS published
		  ON published.published_moment_id = current.published_moment_id
		 AND published.media_item_id = current.media_item_id
		WHERE editable.event_id = ?
		  AND (media.media_type, media.width, media.height, media.local_date_time)
		      IS DISTINCT FROM (published.media_type, published.width, published.height, published.local_date_time)
		ORDER BY editable.position, media.id
	`, eventID).Scan(ctx, &metadataMedia); err != nil {
		return nil, err
	}
	var metadataMoments []uuid.UUID
	if err := db.NewRaw(`
		SELECT editable.id
		FROM draft_moments AS editable
		JOIN published_moments AS published
		  ON published.publication_id = ? AND published.draft_moment_id = editable.id
		WHERE editable.event_id = ?
		  AND (editable.title, editable.proposed_day, editable.cover_media_item_id)
		      IS DISTINCT FROM (published.title, published.proposed_day, published.cover_media_item_id)
		ORDER BY editable.position, editable.id
	`, publicationID, eventID).Scan(ctx, &metadataMoments); err != nil {
		return nil, err
	}
	var structureMoments []uuid.UUID
	if err := db.NewRaw(`
		SELECT COALESCE(editable.id, published.draft_moment_id)
		FROM (SELECT id, position FROM draft_moments WHERE event_id = ?) AS editable
		FULL JOIN (SELECT draft_moment_id, position FROM published_moments WHERE publication_id = ?) AS published
		  ON published.draft_moment_id = editable.id
		WHERE editable.id IS NULL OR published.draft_moment_id IS NULL OR editable.position <> published.position
		ORDER BY COALESCE(editable.position, published.position), COALESCE(editable.id, published.draft_moment_id)
	`, eventID, publicationID).Scan(ctx, &structureMoments); err != nil {
		return nil, err
	}
	var deletedMoments []DeletedMoment
	if err := db.NewRaw(`
		SELECT published.draft_moment_id AS id, published.title, published.proposed_day::text AS proposed_day
		FROM published_moments AS published
		WHERE published.publication_id = ?
		  AND NOT EXISTS (
			SELECT 1 FROM draft_moments AS editable
			WHERE editable.event_id = ? AND editable.id = published.draft_moment_id
		  )
		ORDER BY published.position, published.draft_moment_id
	`, publicationID, eventID).Scan(ctx, &deletedMoments); err != nil {
		return nil, err
	}
	var accessMoments []uuid.UUID
	if err := db.NewRaw(`
		SELECT moment.id
		FROM draft_moments AS moment
		WHERE moment.event_id = ? AND EXISTS (
			(SELECT entry.recipient_access_generation_id
			 FROM current_audience_snapshots AS current
			 JOIN audience_snapshot_entries AS entry ON entry.snapshot_id = current.snapshot_id
			 WHERE current.target_kind = 'moment' AND current.target_id = moment.id
			 EXCEPT
			 SELECT audience.recipient_access_generation_id
			 FROM audience_entries AS audience
			 JOIN published_moments AS published ON published.id = audience.published_moment_id
			 WHERE published.publication_id = ? AND published.draft_moment_id = moment.id)
			UNION ALL
			(SELECT audience.recipient_access_generation_id
			 FROM audience_entries AS audience
			 JOIN published_moments AS published ON published.id = audience.published_moment_id
			 WHERE published.publication_id = ? AND published.draft_moment_id = moment.id
			 EXCEPT
			 SELECT entry.recipient_access_generation_id
			 FROM current_audience_snapshots AS current
			 JOIN audience_snapshot_entries AS entry ON entry.snapshot_id = current.snapshot_id
			 WHERE current.target_kind = 'moment' AND current.target_id = moment.id)
		) ORDER BY moment.position, moment.id
	`, eventID, publicationID, publicationID).Scan(ctx, &accessMoments); err != nil {
		return nil, err
	}

	changes := make([]Change, 0, 6)
	appendMediaChange := func(kind ChangeKind, detail string, ids []string) {
		if len(ids) == 0 {
			return
		}
		change := Change{Kind: kind, Count: len(ids), MediaItemIDs: ids, MomentIDs: []string{}, Detail: detail}
		if kind == ChangeKindRemoval {
			change.RemovedMedia = removedMedia
		}
		changes = append(changes, change)
	}
	appendMomentChange := func(kind ChangeKind, detail string, ids []uuid.UUID, extra int) {
		if len(ids)+extra == 0 {
			return
		}
		momentIDs := make([]string, 0, len(ids))
		for _, id := range ids {
			momentIDs = append(momentIDs, id.String())
		}
		change := Change{Kind: kind, Count: len(ids) + extra, MediaItemIDs: []string{}, MomentIDs: momentIDs, Detail: detail}
		if kind == ChangeKindMomentStructure {
			change.DeletedMoments = deletedMoments
		}
		changes = append(changes, change)
	}
	appendMediaChange(ChangeKindAddition, "Media added", additions)
	appendMediaChange(ChangeKindRemoval, "Media removed", removals)
	appendMediaChange(ChangeKindMove, "Media moved or reordered", moves)
	metadataExtra := 0
	if eventMetadataChanged {
		metadataExtra = 1
	}
	if len(metadataMedia)+len(metadataMoments)+metadataExtra > 0 {
		momentIDs := make([]string, 0, len(metadataMoments))
		for _, id := range metadataMoments {
			momentIDs = append(momentIDs, id.String())
		}
		mediaIDs := make([]string, 0, len(metadataMedia))
		for _, id := range metadataMedia {
			mediaIDs = append(mediaIDs, id.String())
		}
		changes = append(changes, Change{
			Kind: ChangeKindMetadata, Count: len(metadataMedia) + len(metadataMoments) + metadataExtra,
			MediaItemIDs: mediaIDs, MomentIDs: momentIDs, Detail: "Event, Moment, or Media metadata edited",
		})
	}
	appendMomentChange(ChangeKindMomentStructure, "Moment structure or ordering changed", structureMoments, 0)
	appendMomentChange(ChangeKindAccess, "Audience access changed", accessMoments, 0)
	return changes, nil
}

// Load returns the stored coalesced update without changing editable state.
func Load(ctx context.Context, db bun.IDB, eventID uuid.UUID) (*Update, error) {
	var id, publicationID uuid.UUID
	var encoded []byte
	var updatedAt time.Time
	if err := db.NewRaw(`
		SELECT id, base_publication_id, net_changes, updated_at
		FROM staged_updates WHERE event_id = ?
	`, eventID).Scan(ctx, &id, &publicationID, &encoded, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	changes := make([]Change, 0)
	if err := json.Unmarshal(encoded, &changes); err != nil {
		return nil, err
	}
	return &Update{ID: id.String(), BasePublicationID: publicationID.String(), Changes: changes, UpdatedAt: updatedAt}, nil
}
