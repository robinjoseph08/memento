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
	Restorable    bool    `json:"restorable"`
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

// RecipientAccessChange describes a Recipient's net Media authorization change.
type RecipientAccessChange struct {
	RecipientPersonID string `json:"recipient_person_id"`
	RecipientName     string `json:"recipient_name"`
	GrantedMediaCount int    `json:"granted_media_count"`
	RevokedMediaCount int    `json:"revoked_media_count"`
}

// Change is one category in the coalesced difference from the current Publication.
type Change struct {
	Kind                ChangeKind              `json:"kind"`
	Count               int                     `json:"count"`
	MediaItemIDs        []string                `json:"media_item_ids"`
	MomentIDs           []string                `json:"moment_ids"`
	EventMetadataFields []string                `json:"event_metadata_fields,omitempty"`
	RemovedMedia        []RemovedMedia          `json:"removed_media,omitempty"`
	DeletedMoments      []DeletedMoment         `json:"deleted_moments,omitempty"`
	RecipientAccess     []RecipientAccessChange `json:"recipient_access,omitempty"`
	Detail              string                  `json:"detail"`
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

// AccessSummaryLockKey identifies the transaction lock protecting global
// Recipient-Media inputs and every Staged access summary derived from them.
const AccessSummaryLockKey = "events:staged-access-summary-inputs"

// LockAccessSummaryRefresh holds the global entitlement inputs stable while a
// Staged update is refreshed. Callers that write editable Event state must take
// this shared lock before locking or writing an Event.
func LockAccessSummaryRefresh(ctx context.Context, tx bun.Tx) error {
	_, err := tx.NewRaw(`SELECT pg_advisory_xact_lock_shared(hashtextextended(?, 0))`, AccessSummaryLockKey).Exec(ctx)
	return err
}

// LockAccessSummaryReplacement serializes Publication and Withdrawal changes
// to global entitlement inputs with every Staged refresh. It must be acquired
// before locking an individual Event.
func LockAccessSummaryReplacement(ctx context.Context, tx bun.Tx) error {
	_, err := tx.NewRaw(`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`, AccessSummaryLockKey).Exec(ctx)
	return err
}

// Refresh coalesces the editable Event against its current immutable
// Publication. Callers must make their editable writes and this refresh in the
// same transaction so an empty update can never enter the work queue. Callers
// that write Event state must acquire LockAccessSummaryRefresh before those
// writes; this acquisition also protects read-only/retry callers.
func Refresh(ctx context.Context, tx bun.Tx, eventID uuid.UUID, now time.Time) (*Update, error) {
	if err := LockAccessSummaryRefresh(ctx, tx); err != nil {
		return nil, err
	}
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

// RefreshDependentAccessUpdates refreshes other Staged Events after a
// Publication or Withdrawal replaces effective current entitlements. A changed
// access impact is new review information, so the dependent Event receives a
// new version and loses final-review approval in the same transaction.
// excludedEventID may be uuid.Nil when no Event is being replaced.
func RefreshDependentAccessUpdates(ctx context.Context, tx bun.Tx, excludedEventID uuid.UUID, now time.Time) error {
	type candidate struct {
		EventID    uuid.UUID `bun:"event_id"`
		NetChanges []byte    `bun:"net_changes"`
	}
	var candidates []candidate
	if err := tx.NewRaw(`
		SELECT staged.event_id, staged.net_changes
		FROM staged_updates AS staged
		JOIN events AS event ON event.id = staged.event_id
		WHERE ?::uuid = ?::uuid OR staged.event_id <> ?
		ORDER BY staged.event_id
		FOR UPDATE OF event, staged
	`, excludedEventID, uuid.Nil, excludedEventID).Scan(ctx, &candidates); err != nil {
		return err
	}
	for _, candidate := range candidates {
		var priorChanges []Change
		if err := json.Unmarshal(candidate.NetChanges, &priorChanges); err != nil {
			return err
		}
		priorAccess, err := accessChangeFingerprint(priorChanges)
		if err != nil {
			return err
		}
		update, err := Refresh(ctx, tx, candidate.EventID, now)
		if err != nil {
			return err
		}
		currentAccess := ""
		if update != nil {
			currentAccess, err = accessChangeFingerprint(update.Changes)
			if err != nil {
				return err
			}
		}
		if priorAccess == currentAccess {
			continue
		}
		if _, err := tx.NewRaw(`
			UPDATE events SET version = version + 1, final_review_complete = false, updated_at = ?
			WHERE id = ?
		`, now, candidate.EventID).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

func accessChangeFingerprint(changes []Change) (string, error) {
	for _, change := range changes {
		if change.Kind != ChangeKindAccess {
			continue
		}
		encoded, err := json.Marshal(change)
		return string(encoded), err
	}
	return "", nil
}

// InvalidateEvent records an externally-applied editable change and refreshes
// its coalesced update in the same transaction. Its caller must acquire
// LockAccessSummaryRefresh before locking or writing the Event.
func InvalidateEvent(ctx context.Context, tx bun.Tx, eventID uuid.UUID, now time.Time) (*Update, error) {
	if _, err := tx.NewRaw(`
		UPDATE events SET version = version + 1, final_review_complete = false, updated_at = ?
		WHERE id = ?
	`, now, eventID).Exec(ctx); err != nil {
		return nil, err
	}
	return Refresh(ctx, tx, eventID, now)
}

// Clear removes an Event's coalesced update after complete cancellation or
// successful Publication, including restoration context owned by that update.
func Clear(ctx context.Context, tx bun.Tx, eventID uuid.UUID) error {
	if _, err := tx.NewRaw(`UPDATE events SET current_staged_update_id = NULL WHERE id = ?`, eventID).Exec(ctx); err != nil {
		return err
	}
	if _, err := tx.NewRaw(`DELETE FROM staged_updates WHERE event_id = ?`, eventID).Exec(ctx); err != nil {
		return err
	}
	return ClearMomentReviewRestorations(ctx, tx, eventID)
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
	additions, removals := make([]string, 0), make([]string, 0)
	for _, placement := range draft {
		if _, exists := publishedByMedia[placement.MediaItemID]; !exists {
			additions = append(additions, placement.MediaItemID.String())
		}
	}
	for _, placement := range published {
		if _, exists := draftByMedia[placement.MediaItemID]; !exists {
			removals = append(removals, placement.MediaItemID.String())
		}
	}
	moves := meaningfulMoves(draft, published)
	var removedMedia []RemovedMedia
	if err := db.NewRaw(`
		SELECT placement.media_item_id AS id, placement.media_type, placement.local_date_time,
			EXISTS (
				SELECT 1
				FROM event_sources AS source
				JOIN source_album_memberships AS membership
				  ON membership.source_album_id = source.source_album_id
				JOIN media_items AS media ON media.id = membership.media_item_id
				WHERE source.event_id = ? AND membership.media_item_id = placement.media_item_id
				  AND media.availability = 'current'
			) AS restorable
		FROM published_media_placements AS placement
		JOIN published_moments AS moment ON moment.id = placement.published_moment_id
		WHERE moment.publication_id = ?
		  AND NOT EXISTS (
			SELECT 1 FROM draft_media_placements AS editable
			WHERE editable.event_id = ? AND editable.media_item_id = placement.media_item_id
		  )
		ORDER BY moment.position, placement.position, placement.media_item_id
	`, eventID, publicationID, eventID).Scan(ctx, &removedMedia); err != nil {
		return nil, err
	}

	var titleChanged, descriptionChanged, timezoneChanged bool
	if err := db.NewRaw(`
		SELECT editable.title IS DISTINCT FROM published.title,
		       editable.description IS DISTINCT FROM published.description,
		       editable.grouping_timezone IS DISTINCT FROM published.grouping_timezone
		FROM events AS editable
		JOIN published_event_revisions AS published ON published.publication_id = ?
		WHERE editable.id = ?
	`, publicationID, eventID).Scan(ctx, &titleChanged, &descriptionChanged, &timezoneChanged); err != nil {
		return nil, err
	}
	eventMetadataFields := make([]string, 0, 3)
	if titleChanged {
		eventMetadataFields = append(eventMetadataFields, "title")
	}
	if descriptionChanged {
		eventMetadataFields = append(eventMetadataFields, "description")
	}
	if timezoneChanged {
		eventMetadataFields = append(eventMetadataFields, "grouping_timezone")
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
		WITH editable AS (
			SELECT id, position FROM draft_moments WHERE event_id = ?
		), published AS (
			SELECT draft_moment_id AS id, position FROM published_moments WHERE publication_id = ?
		), editable_retained AS (
			SELECT editable.id, row_number() OVER (ORDER BY editable.position, editable.id) AS retained_rank
			FROM editable JOIN published USING (id)
		), published_retained AS (
			SELECT published.id, row_number() OVER (ORDER BY published.position, published.id) AS retained_rank
			FROM published JOIN editable USING (id)
		)
		SELECT COALESCE(editable.id, published.id)
		FROM editable
		FULL JOIN published USING (id)
		LEFT JOIN editable_retained ON editable_retained.id = COALESCE(editable.id, published.id)
		LEFT JOIN published_retained ON published_retained.id = COALESCE(editable.id, published.id)
		WHERE editable.id IS NULL OR published.id IS NULL
		   OR editable_retained.retained_rank <> published_retained.retained_rank
		ORDER BY COALESCE(editable.position, published.position), COALESCE(editable.id, published.id)
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
	type accessPairChange struct {
		RecipientPersonID uuid.UUID `bun:"recipient_person_id"`
		RecipientName     string    `bun:"recipient_name"`
		MediaItemID       uuid.UUID `bun:"media_item_id"`
		Granted           int       `bun:"granted"`
		Revoked           int       `bun:"revoked"`
	}
	var accessPairs []accessPairChange
	if err := db.NewRaw(`
		WITH editable_event AS (
			SELECT DISTINCT entry.recipient_access_generation_id AS access_id,
				placement.media_item_id
			FROM draft_moments AS moment
			JOIN current_audience_snapshots AS snapshot
			  ON snapshot.target_kind = 'moment' AND snapshot.target_id = moment.id
			JOIN audience_snapshot_entries AS entry ON entry.snapshot_id = snapshot.snapshot_id
			JOIN draft_media_placements AS placement ON placement.draft_moment_id = moment.id
			WHERE moment.event_id = ?
		), effective_entitlements AS (
			SELECT DISTINCT entitlement.event_id,
				entitlement.recipient_access_generation_id AS access_id,
				entitlement.media_item_id
			FROM current_audience_entitlements AS entitlement
			JOIN current_published_placements AS placement
			  ON placement.event_id = entitlement.event_id
			 AND placement.media_item_id = entitlement.media_item_id
			JOIN published_moments AS moment ON moment.id = placement.published_moment_id
			WHERE NOT content_is_withdrawn(
				placement.event_id, moment.draft_moment_id, placement.media_item_id
			)
		), before_global AS (
			SELECT DISTINCT access_id, media_item_id FROM effective_entitlements
		), after_global AS (
			SELECT DISTINCT access_id, media_item_id
			FROM effective_entitlements WHERE event_id <> ?
			UNION
			SELECT access_id, media_item_id FROM editable_event
		), changed AS (
			SELECT COALESCE(after_global.access_id, before_global.access_id) AS access_id,
				COALESCE(after_global.media_item_id, before_global.media_item_id) AS media_item_id,
				(after_global.media_item_id IS NOT NULL AND before_global.media_item_id IS NULL)::integer AS granted,
				(before_global.media_item_id IS NOT NULL AND after_global.media_item_id IS NULL)::integer AS revoked
			FROM after_global
			FULL JOIN before_global USING (access_id, media_item_id)
			WHERE after_global.media_item_id IS NULL OR before_global.media_item_id IS NULL
		)
		SELECT person.id AS recipient_person_id, person.display_name AS recipient_name,
			changed.media_item_id, changed.granted, changed.revoked
		FROM changed
		JOIN recipient_access_generations AS access ON access.id = changed.access_id
		JOIN people AS person ON person.id = access.person_id
		ORDER BY person.sort_name, person.id, changed.media_item_id
	`, eventID, eventID).Scan(ctx, &accessPairs); err != nil {
		return nil, err
	}
	accessMediaSet := make(map[uuid.UUID]bool)
	recipientAccess := make([]RecipientAccessChange, 0)
	recipientIndex := make(map[uuid.UUID]int)
	for _, pair := range accessPairs {
		accessMediaSet[pair.MediaItemID] = true
		index, exists := recipientIndex[pair.RecipientPersonID]
		if !exists {
			index = len(recipientAccess)
			recipientIndex[pair.RecipientPersonID] = index
			recipientAccess = append(recipientAccess, RecipientAccessChange{
				RecipientPersonID: pair.RecipientPersonID.String(),
				RecipientName:     pair.RecipientName,
			})
		}
		recipientAccess[index].GrantedMediaCount += pair.Granted
		recipientAccess[index].RevokedMediaCount += pair.Revoked
	}
	accessMedia := make([]string, 0, len(accessMediaSet))
	seenAccessMedia := make(map[uuid.UUID]bool, len(accessMediaSet))
	for _, placements := range [][]stagedPlacement{published, draft} {
		for _, placement := range placements {
			if accessMediaSet[placement.MediaItemID] && !seenAccessMedia[placement.MediaItemID] {
				accessMedia = append(accessMedia, placement.MediaItemID.String())
				seenAccessMedia[placement.MediaItemID] = true
			}
		}
	}
	seenAccessMoments := make(map[uuid.UUID]bool, len(accessMoments))
	for _, momentID := range accessMoments {
		seenAccessMoments[momentID] = true
	}
	for _, placements := range [][]stagedPlacement{published, draft} {
		for _, placement := range placements {
			if accessMediaSet[placement.MediaItemID] && placement.MomentID != uuid.Nil && !seenAccessMoments[placement.MomentID] {
				accessMoments = append(accessMoments, placement.MomentID)
				seenAccessMoments[placement.MomentID] = true
			}
		}
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
	if len(eventMetadataFields) > 0 {
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
			MediaItemIDs: mediaIDs, MomentIDs: momentIDs, EventMetadataFields: eventMetadataFields,
			Detail: "Event, Moment, or Media metadata edited",
		})
	}
	appendMomentChange(ChangeKindMomentStructure, "Moment structure or ordering changed", structureMoments, 0)
	if len(accessMoments) > 0 || len(recipientAccess) > 0 {
		momentIDs := make([]string, 0, len(accessMoments))
		for _, id := range accessMoments {
			momentIDs = append(momentIDs, id.String())
		}
		detail := "Moment Audience changed without changing global Recipient Media access"
		if len(recipientAccess) > 0 {
			detail = "Global Recipient Media access granted or revoked"
		}
		changes = append(changes, Change{
			Kind: ChangeKindAccess, Count: len(recipientAccess), MediaItemIDs: accessMedia,
			MomentIDs: momentIDs, RecipientAccess: recipientAccess, Detail: detail,
		})
	}
	return changes, nil
}

func meaningfulMoves(draft, published []stagedPlacement) []string {
	draftByMedia := make(map[uuid.UUID]stagedPlacement, len(draft))
	publishedByMedia := make(map[uuid.UUID]stagedPlacement, len(published))
	for _, placement := range draft {
		draftByMedia[placement.MediaItemID] = placement
	}
	for _, placement := range published {
		publishedByMedia[placement.MediaItemID] = placement
	}

	moved := make(map[uuid.UUID]bool)
	draftRanks := make(map[uuid.UUID]int)
	publishedRanks := make(map[uuid.UUID]int)
	nextRank := make(map[uuid.UUID]int)
	for _, placement := range draft {
		prior, retained := publishedByMedia[placement.MediaItemID]
		if !retained {
			continue
		}
		if prior.MomentID != placement.MomentID {
			moved[placement.MediaItemID] = true
			continue
		}
		draftRanks[placement.MediaItemID] = nextRank[placement.MomentID]
		nextRank[placement.MomentID]++
	}
	clear(nextRank)
	for _, placement := range published {
		next, retained := draftByMedia[placement.MediaItemID]
		if !retained || next.MomentID != placement.MomentID {
			continue
		}
		publishedRanks[placement.MediaItemID] = nextRank[placement.MomentID]
		nextRank[placement.MomentID]++
	}

	moves := make([]string, 0, len(moved))
	for _, placement := range draft {
		if moved[placement.MediaItemID] || draftRanks[placement.MediaItemID] != publishedRanks[placement.MediaItemID] {
			if _, retained := publishedByMedia[placement.MediaItemID]; retained {
				moves = append(moves, placement.MediaItemID.String())
			}
		}
	}
	return moves
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
