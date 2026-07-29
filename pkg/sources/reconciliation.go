package sources

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/staging"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/uptrace/bun"
)

const ReconciliationJobKind = "reconcile_source_album"

var errSourceAlbumMissing = errors.New("source album missing")

// ReconciliationResponse acknowledges a bounded Curator request. Dependency
// work remains in the single-concurrency durable worker.
type ReconciliationResponse struct {
	Status string `json:"status"`
}

type reconciliationJobPayload struct {
	SourceAlbumID uuid.UUID `json:"source_album_id"`
}

type reconciliationSnapshot struct {
	before      immich.AlbumSummary
	after       immich.AlbumSummary
	assets      map[uuid.UUID]immich.AssetSummary
	fingerprint [32]byte
	diagnostic  string
}

// QueueReconciliation makes one durable per-album job immediately eligible.
// Repeated requests and requests during a live lease coalesce into that job.
func (s *Service) QueueReconciliation(ctx context.Context, sourceAlbumID uuid.UUID) (ReconciliationResponse, error) {
	now := s.now().UTC()
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var exists bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM source_albums WHERE id = ?)`, sourceAlbumID).Scan(ctx, &exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		return enqueueReconciliation(ctx, tx, sourceAlbumID, now)
	})
	if err != nil {
		return ReconciliationResponse{}, err
	}
	return ReconciliationResponse{Status: "queued"}, nil
}

func enqueueReconciliation(ctx context.Context, tx bun.Tx, sourceAlbumID uuid.UUID, availableAt time.Time) error {
	payload, err := json.Marshal(reconciliationJobPayload{SourceAlbumID: sourceAlbumID})
	if err != nil {
		return fmt.Errorf("encode Source reconciliation job: %w", err)
	}
	_, err = tx.NewRaw(`
		INSERT INTO jobs (kind, payload, idempotency_key, available_at)
		VALUES (?, ?::jsonb, ?, ?)
		ON CONFLICT (idempotency_key) WHERE idempotency_key IS NOT NULL DO UPDATE SET
			status = CASE WHEN jobs.status IN ('completed', 'failed') THEN 'pending' ELSE jobs.status END,
			available_at = CASE WHEN jobs.status = 'running' THEN jobs.available_at ELSE LEAST(jobs.available_at, EXCLUDED.available_at) END,
			attempts = CASE WHEN jobs.status IN ('completed', 'failed') THEN 0 ELSE jobs.attempts END,
			payload = EXCLUDED.payload,
			last_safe_error = CASE WHEN jobs.status IN ('completed', 'failed') THEN NULL ELSE jobs.last_safe_error END,
			rerun_requested = jobs.status = 'running', updated_at = now()
	`, ReconciliationJobKind, string(payload), "source-reconcile:"+sourceAlbumID.String(), availableAt).Exec(ctx)
	if err != nil {
		return fmt.Errorf("queue Source reconciliation: %w", err)
	}
	return nil
}

// HandleReconciliationJob validates worker payloads and keeps successful scans
// on the configured schedule. Retryable scan failures use worker backoff.
func (s *Service) HandleReconciliationJob(ctx context.Context, job worker.Job) error {
	var payload reconciliationJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.SourceAlbumID == uuid.Nil {
		return worker.Permanent("invalid_source_reconciliation_payload")
	}
	if err := s.Reconcile(ctx, payload.SourceAlbumID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return worker.Permanent("source_album_not_found")
		}
		return err
	}
	return worker.RescheduleAfter(s.reconciliationInterval)
}

// Reconcile validates a complete candidate membership before changing private
// editable source state. Failed and unstable scans commit diagnostics and
// scheduling metadata, but never membership or validated removal evidence.
func (s *Service) Reconcile(ctx context.Context, sourceAlbumID uuid.UUID) error {
	now := s.now().UTC()
	var outcome error
	var rolledBackFailure *reconciliationSnapshot
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := staging.LockAccessSummaryRefresh(ctx, tx); err != nil {
			return err
		}
		var immichAlbumID uuid.UUID
		if err := tx.NewRaw(`SELECT immich_album_id FROM source_albums WHERE id = ? FOR UPDATE`, sourceAlbumID).Scan(ctx, &immichAlbumID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("lock Source album reconciliation: %w", err)
		}

		snapshot, snapshotErr := s.readSnapshot(ctx, immichAlbumID)
		if errors.Is(snapshotErr, errSourceAlbumMissing) {
			return recordMissingSourceAlbum(ctx, tx, sourceAlbumID, now, s.reconciliationInterval)
		}
		if snapshotErr != nil {
			outcome = snapshotErr
			status := "unstable"
			if errors.Is(snapshotErr, ErrDependency) {
				status = "failed"
			}
			return recordFailedReconciliation(ctx, tx, sourceAlbumID, now, status, snapshot)
		}

		stablePasses, additions, removals, err := s.applyValidatedSnapshot(ctx, tx, sourceAlbumID, snapshot, now)
		if err != nil {
			if errors.Is(err, ErrDependency) {
				snapshot.diagnostic = "dependency_unavailable"
				rolledBackFailure = &snapshot
			}
			return err
		}
		snapshot.diagnostic = ""
		if err := recordValidatedRun(ctx, tx, sourceAlbumID, now, snapshot, stablePasses, additions, removals); err != nil {
			return err
		}
		return nil
	})
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		if rolledBackFailure != nil {
			recordErr := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				var lockedSourceAlbumID uuid.UUID
				if err := tx.NewRaw(`SELECT id FROM source_albums WHERE id = ? FOR UPDATE`, sourceAlbumID).Scan(ctx, &lockedSourceAlbumID); err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						return ErrNotFound
					}
					return err
				}
				return recordFailedReconciliation(ctx, tx, sourceAlbumID, now, "failed", *rolledBackFailure)
			})
			if recordErr != nil {
				return fmt.Errorf("record failed Source reconciliation: %w", recordErr)
			}
		}
		return fmt.Errorf("reconcile Source album: %w", err)
	}
	return outcome
}

func (s *Service) readSnapshot(ctx context.Context, albumID uuid.UUID) (reconciliationSnapshot, error) {
	var snapshot reconciliationSnapshot
	if err := s.connector.Check(ctx); err != nil {
		snapshot.diagnostic = "dependency_unavailable"
		return snapshot, ErrDependency
	}
	before, err := s.connector.Album(ctx, albumID)
	if errors.Is(err, immich.ErrNotFound) {
		return snapshot, errSourceAlbumMissing
	}
	if err != nil {
		snapshot.diagnostic = "dependency_unavailable"
		return snapshot, ErrDependency
	}
	snapshot.before = before
	beforeFingerprint, err := albumFingerprint(before)
	if err != nil {
		return snapshot, err
	}

	assets := make(map[uuid.UUID]immich.AssetSummary)
	pageNumber := 1
	pagesRead := 0
	for {
		pagesRead++
		if pagesRead > max(1, before.AssetCount+1) {
			snapshot.diagnostic = "pagination_incomplete"
			return snapshot, ErrUnstable
		}
		page, pageErr := s.connector.AlbumAssetsPage(ctx, albumID, pageNumber)
		if errors.Is(pageErr, immich.ErrNotFound) {
			return snapshot, errSourceAlbumMissing
		}
		if pageErr != nil {
			snapshot.diagnostic = "dependency_unavailable"
			return snapshot, ErrDependency
		}
		assetsBeforePage := len(assets)
		for _, asset := range page.Items {
			if existing, duplicate := assets[asset.SourceID]; duplicate {
				if !sameAsset(existing, asset) {
					snapshot.diagnostic = "pagination_incomplete"
					return snapshot, ErrUnstable
				}
				continue
			}
			assets[asset.SourceID] = asset
		}
		if page.NextPage == nil {
			break
		}
		if *page.NextPage <= pageNumber || len(assets) == assetsBeforePage {
			snapshot.diagnostic = "pagination_incomplete"
			return snapshot, ErrUnstable
		}
		pageNumber = *page.NextPage
	}

	after, err := s.connector.Album(ctx, albumID)
	if errors.Is(err, immich.ErrNotFound) {
		return snapshot, errSourceAlbumMissing
	}
	if err != nil {
		snapshot.diagnostic = "dependency_unavailable"
		return snapshot, ErrDependency
	}
	snapshot.after = after
	afterFingerprint, err := albumFingerprint(after)
	if err != nil {
		return snapshot, err
	}
	if beforeFingerprint != afterFingerprint {
		snapshot.diagnostic = "summary_changed"
		return snapshot, ErrUnstable
	}
	if len(assets) != after.AssetCount {
		snapshot.diagnostic = "pagination_incomplete"
		return snapshot, ErrUnstable
	}
	snapshot.assets = assets
	snapshot.fingerprint = membershipFingerprint(assets)
	return snapshot, nil
}

func (s *Service) applyValidatedSnapshot(
	ctx context.Context,
	tx bun.Tx,
	sourceAlbumID uuid.UUID,
	snapshot reconciliationSnapshot,
	now time.Time,
) (int, int, int, error) {
	var previous []byte
	var previousPasses int
	if err := tx.NewRaw(`
		SELECT candidate_membership_fingerprint, candidate_membership_passes
		FROM source_albums WHERE id = ?
	`, sourceAlbumID).Scan(ctx, &previous, &previousPasses); err != nil {
		return 0, 0, 0, err
	}
	stablePasses := 1
	if len(previous) == sha256.Size && string(previous) == string(snapshot.fingerprint[:]) {
		stablePasses = previousPasses + 1
	}

	assetIDs := make([]uuid.UUID, 0, len(snapshot.assets))
	for id := range snapshot.assets {
		assetIDs = append(assetIDs, id)
	}
	sort.Slice(assetIDs, func(i, j int) bool { return assetIDs[i].String() < assetIDs[j].String() })
	encodedIDs, err := json.Marshal(assetIDs)
	if err != nil {
		return 0, 0, 0, err
	}
	var addedAssetIDs []uuid.UUID
	if err := tx.NewRaw(`
		SELECT incoming.id::uuid
		FROM jsonb_array_elements_text(?::jsonb) AS incoming(id)
		WHERE NOT EXISTS (
			SELECT 1 FROM source_album_memberships
			WHERE source_album_id = ? AND immich_asset_id = incoming.id::uuid
		)
		ORDER BY incoming.id::uuid
	`, string(encodedIDs), sourceAlbumID).Scan(ctx, &addedAssetIDs); err != nil {
		return 0, 0, 0, err
	}
	additions := len(addedAssetIDs)
	metadataChangedMediaIDs := make([]uuid.UUID, 0)
	for start := 0; start < len(assetIDs); start += 1000 {
		end := min(start+1000, len(assetIDs))
		batch := make([]immich.AssetSummary, 0, end-start)
		for _, assetID := range assetIDs[start:end] {
			batch = append(batch, snapshot.assets[assetID])
		}
		changedMediaIDs, err := upsertMediaItemBatch(ctx, tx, sourceAlbumID, batch, now)
		if err != nil {
			return 0, 0, 0, err
		}
		metadataChangedMediaIDs = append(metadataChangedMediaIDs, changedMediaIDs...)
	}
	if err := supersedeInvalidMediaRepairs(ctx, tx, now); err != nil {
		return 0, 0, 0, err
	}

	var addedMediaIDs []uuid.UUID
	if len(addedAssetIDs) > 0 {
		if err := tx.NewRaw(`SELECT id FROM media_items WHERE immich_asset_id IN (?) ORDER BY id`, bun.List(addedAssetIDs)).Scan(ctx, &addedMediaIDs); err != nil {
			return 0, 0, 0, err
		}
	}
	removals := 0
	var removedMediaIDs []uuid.UUID
	if stablePasses >= 2 {
		type removedMembership struct {
			MediaItemID   uuid.UUID `bun:"media_item_id"`
			ImmichAssetID uuid.UUID `bun:"immich_asset_id"`
		}
		var removedMemberships []removedMembership
		if err := tx.NewRaw(`
			SELECT membership.media_item_id, membership.immich_asset_id
			FROM source_album_memberships AS membership
			WHERE membership.source_album_id = ?
			  AND NOT EXISTS (
				SELECT 1 FROM jsonb_array_elements_text(?::jsonb) AS candidate(id)
				WHERE candidate.id::uuid = membership.immich_asset_id
			  )
			ORDER BY membership.media_item_id
		`, sourceAlbumID, string(encodedIDs)).Scan(ctx, &removedMemberships); err != nil {
			return 0, 0, 0, err
		}
		confirmedMissingMediaIDs := make([]uuid.UUID, 0)
		for _, membership := range removedMemberships {
			removedMediaIDs = append(removedMediaIDs, membership.MediaItemID)
			var retainedByAnotherSource bool
			if err := tx.NewRaw(`
				SELECT EXISTS (
					SELECT 1 FROM source_album_memberships
					WHERE media_item_id = ? AND source_album_id <> ?
				)
			`, membership.MediaItemID, sourceAlbumID).Scan(ctx, &retainedByAnotherSource); err != nil {
				return 0, 0, 0, err
			}
			if retainedByAnotherSource {
				continue
			}
			exists, err := s.connector.AssetExists(ctx, membership.ImmichAssetID)
			if err != nil {
				return 0, 0, 0, fmt.Errorf("confirm removed Immich asset: %w", ErrDependency)
			}
			if !exists {
				confirmedMissingMediaIDs = append(confirmedMissingMediaIDs, membership.MediaItemID)
			}
		}
		result, err := tx.NewRaw(`
			DELETE FROM source_album_memberships AS membership
			WHERE source_album_id = ?
			  AND NOT EXISTS (
				SELECT 1 FROM jsonb_array_elements_text(?::jsonb) AS candidate(id)
				WHERE candidate.id::uuid = membership.immich_asset_id
			  )
		`, sourceAlbumID, string(encodedIDs)).Exec(ctx)
		if err != nil {
			return 0, 0, 0, err
		}
		removed, err := result.RowsAffected()
		if err != nil {
			return 0, 0, 0, err
		}
		removals = int(removed)
		if len(confirmedMissingMediaIDs) > 0 {
			if _, err := tx.NewRaw(`
				UPDATE media_items AS media SET availability = 'source_missing', missing_since = COALESCE(missing_since, ?), updated_at = ?
				WHERE availability = 'current' AND id IN (?)
				  AND NOT EXISTS (SELECT 1 FROM source_album_memberships WHERE media_item_id = media.id)
			`, now, now, bun.List(confirmedMissingMediaIDs)).Exec(ctx); err != nil {
				return 0, 0, 0, err
			}
		}
		if err := proposeMediaRepairs(ctx, tx, now); err != nil {
			return 0, 0, 0, err
		}
		// Evidence-free additions predate repair support and have no safe identity seam to preserve.
		if _, err := tx.NewRaw(`
			DELETE FROM media_backings AS backing
			WHERE backing.active AND backing.checksum IS NULL
			  AND EXISTS (
				SELECT 1 FROM media_items AS media WHERE media.id = backing.media_item_id
				  AND media.availability = 'source_missing'
				  AND NOT EXISTS (SELECT 1 FROM source_album_memberships WHERE media_item_id = media.id)
				  AND NOT EXISTS (SELECT 1 FROM media_repair_candidates WHERE media_item_id = media.id)
			  )
		`).Exec(ctx); err != nil {
			return 0, 0, 0, err
		}
		if _, err := tx.NewRaw(`
			DELETE FROM media_items AS media
			WHERE availability = 'source_missing'
			  AND NOT EXISTS (SELECT 1 FROM source_album_memberships WHERE media_item_id = media.id)
			  AND NOT EXISTS (SELECT 1 FROM media_backings WHERE media_item_id = media.id)
			  AND NOT EXISTS (SELECT 1 FROM media_repair_candidates WHERE media_item_id = media.id)
			  AND NOT EXISTS (SELECT 1 FROM draft_media_placements WHERE media_item_id = media.id)
			  AND NOT EXISTS (SELECT 1 FROM loose_items WHERE media_item_id = media.id)
		`).Exec(ctx); err != nil {
			return 0, 0, 0, err
		}
	}
	if err := syncEditableEvents(ctx, tx, sourceAlbumID, addedMediaIDs, removedMediaIDs, metadataChangedMediaIDs, now); err != nil {
		return 0, 0, 0, err
	}

	summaryFingerprint, err := albumFingerprint(snapshot.after)
	if err != nil {
		return 0, 0, 0, err
	}
	_, err = tx.NewRaw(`
		UPDATE source_albums SET
			name = ?, description = ?, asset_count = ?, source_created_at = ?, source_updated_at = ?,
			source_start_at = ?, source_end_at = ?, source_last_modified_asset_at = ?,
			source_fingerprint = ?, candidate_membership_fingerprint = ?, candidate_membership_passes = ?,
			source_missing = false, missing_since = NULL, last_seen_at = ?,
			version = version + CASE WHEN source_missing THEN 1 ELSE 0 END,
			last_reconciled_at = ?, next_reconciliation_at = ?, updated_at = ?
		WHERE id = ?
	`, snapshot.after.Name, snapshot.after.Description, snapshot.after.AssetCount,
		snapshot.after.CreatedAt, snapshot.after.UpdatedAt, snapshot.after.StartDate, snapshot.after.EndDate,
		snapshot.after.LastModifiedAssetTimestamp, summaryFingerprint[:], snapshot.fingerprint[:], stablePasses,
		now, now, now.Add(s.reconciliationInterval), now, sourceAlbumID).Exec(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	return stablePasses, additions, removals, nil
}

func syncEditableEvents(
	ctx context.Context,
	tx bun.Tx,
	sourceAlbumID uuid.UUID,
	addedMediaIDs, removedMediaIDs, metadataChangedMediaIDs []uuid.UUID,
	now time.Time,
) error {
	if len(addedMediaIDs) == 0 && len(removedMediaIDs) == 0 && len(metadataChangedMediaIDs) == 0 {
		return nil
	}
	encodedMetadataIDs, err := json.Marshal(metadataChangedMediaIDs)
	if err != nil {
		return err
	}
	type editableEvent struct {
		ID                 uuid.UUID `bun:"id"`
		LinkedSource       bool      `bun:"linked_source"`
		IncludeFutureMedia bool      `bun:"include_future_media"`
	}
	var events []editableEvent
	if err := tx.NewRaw(`
		SELECT event.id,
		       EXISTS (
			   SELECT 1 FROM event_sources AS source
			   WHERE source.event_id = event.id AND source.source_album_id = ?
		       ) AS linked_source,
		       EXISTS (
			   SELECT 1 FROM event_sources AS source
			   WHERE source.event_id = event.id AND source.source_album_id = ?
			     AND source.include_future_media
		       ) AS include_future_media
		FROM events AS event
		WHERE event.lifecycle IN ('draft', 'published')
		  AND (
			EXISTS (
				SELECT 1 FROM event_sources AS source
				WHERE source.event_id = event.id AND source.source_album_id = ?
			)
			OR EXISTS (
				SELECT 1 FROM draft_media_placements AS placement
				JOIN jsonb_array_elements_text(?::jsonb) AS changed(id)
				  ON changed.id::uuid = placement.media_item_id
				WHERE placement.event_id = event.id
			)
		  )
		ORDER BY event.id
	`, sourceAlbumID, sourceAlbumID, sourceAlbumID, string(encodedMetadataIDs)).Scan(ctx, &events); err != nil {
		return err
	}
	for _, event := range events {
		eventID := event.ID
		var lockedEventID uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM events WHERE id = ? FOR UPDATE`, eventID).Scan(ctx, &lockedEventID); err != nil {
			return err
		}
		changed := false
		changedMomentIDs := make(map[uuid.UUID]struct{})
		restoredMomentReviews := make(map[uuid.UUID]struct{})
		if len(metadataChangedMediaIDs) > 0 {
			if err := tx.NewRaw(`
				SELECT EXISTS (
					SELECT 1 FROM draft_media_placements
					WHERE event_id = ? AND media_item_id IN (?)
				)
			`, eventID, bun.List(metadataChangedMediaIDs)).Scan(ctx, &changed); err != nil {
				return err
			}
		}
		if !event.LinkedSource {
			if !changed {
				continue
			}
			if _, err := staging.InvalidateEvent(ctx, tx, lockedEventID, now); err != nil {
				return err
			}
			continue
		}
		for _, mediaID := range addedMediaIDs {
			var exists bool
			if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM draft_media_placements WHERE event_id = ? AND media_item_id = ?)`, eventID, mediaID).Scan(ctx, &exists); err != nil {
				return err
			}
			if exists {
				continue
			}
			var momentID *uuid.UUID
			var position int
			var wasCover bool
			err := tx.NewRaw(`
				SELECT draft_moment_id, position, was_cover
				FROM staged_source_removals WHERE event_id = ? AND media_item_id = ?
			`, eventID, mediaID).Scan(ctx, &momentID, &position, &wasCover)
			if errors.Is(err, sql.ErrNoRows) {
				if !event.IncludeFutureMedia {
					continue
				}
				momentID = nil
				if err := tx.NewRaw(`SELECT COALESCE(max(position), -1) + 1 FROM draft_media_placements WHERE event_id = ?`, eventID).Scan(ctx, &position); err != nil {
					return err
				}
			} else if err != nil {
				return err
			} else {
				var occupied bool
				if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM draft_media_placements WHERE event_id = ? AND position = ?)`, eventID, position).Scan(ctx, &occupied); err != nil {
					return err
				}
				if occupied {
					if err := tx.NewRaw(`SELECT COALESCE(max(position), -1) + 1 FROM draft_media_placements WHERE event_id = ?`, eventID).Scan(ctx, &position); err != nil {
						return err
					}
				}
				if momentID != nil {
					var retained bool
					if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM draft_moments WHERE event_id = ? AND id = ?)`, eventID, *momentID).Scan(ctx, &retained); err != nil {
						return err
					}
					if !retained {
						momentID = nil
					}
				}
			}
			if _, err := tx.NewRaw(`
				INSERT INTO draft_media_placements (event_id, media_item_id, draft_moment_id, position, created_at)
				VALUES (?, ?, ?, ?, ?)
			`, eventID, mediaID, momentID, position, now).Exec(ctx); err != nil {
				return err
			}
			if momentID != nil {
				changedMomentIDs[*momentID] = struct{}{}
			}
			if wasCover && momentID != nil {
				if _, err := tx.NewRaw(`UPDATE draft_moments SET cover_media_item_id = ? WHERE id = ? AND cover_media_item_id IS NULL`, mediaID, *momentID).Exec(ctx); err != nil {
					return err
				}
			}
			if _, err := tx.NewRaw(`DELETE FROM staged_source_removals WHERE event_id = ? AND media_item_id = ?`, eventID, mediaID).Exec(ctx); err != nil {
				return err
			}
			if momentID != nil {
				restored, err := staging.RestoreMomentReviewIfPublishedResult(ctx, tx, eventID, *momentID)
				if err != nil {
					return err
				}
				if restored {
					restoredMomentReviews[*momentID] = struct{}{}
				}
			}
			changed = true
		}
		for _, mediaID := range removedMediaIDs {
			var retainedByLinkedSource bool
			if err := tx.NewRaw(`
				SELECT EXISTS (
					SELECT 1 FROM event_sources AS source
					JOIN source_album_memberships AS membership ON membership.source_album_id = source.source_album_id
					WHERE source.event_id = ? AND membership.media_item_id = ?
				)
			`, eventID, mediaID).Scan(ctx, &retainedByLinkedSource); err != nil {
				return err
			}
			if retainedByLinkedSource {
				continue
			}
			var momentID *uuid.UUID
			var position int
			err := tx.NewRaw(`
				SELECT draft_moment_id, position FROM draft_media_placements
				WHERE event_id = ? AND media_item_id = ?
			`, eventID, mediaID).Scan(ctx, &momentID, &position)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			wasCover := false
			if momentID != nil {
				if err := tx.NewRaw(`SELECT cover_media_item_id = ? FROM draft_moments WHERE id = ?`, mediaID, *momentID).Scan(ctx, &wasCover); err != nil {
					return err
				}
			}
			var wasPublished, sourceMissing bool
			if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM current_published_placements WHERE event_id = ? AND media_item_id = ?)`, eventID, mediaID).Scan(ctx, &wasPublished); err != nil {
				return err
			}
			if err := tx.NewRaw(`SELECT availability = 'source_missing' FROM media_items WHERE id = ?`, mediaID).Scan(ctx, &sourceMissing); err != nil {
				return err
			}
			if wasPublished {
				if momentID != nil && !sourceMissing {
					if err := staging.PreserveMomentReview(ctx, tx, eventID, *momentID, now); err != nil {
						return err
					}
				}
				if _, err := tx.NewRaw(`
					INSERT INTO staged_source_removals (
						event_id, media_item_id, draft_moment_id, position, was_cover, created_at
					) VALUES (?, ?, ?, ?, ?, ?)
					ON CONFLICT (event_id, media_item_id) DO NOTHING
				`, eventID, mediaID, momentID, position, wasCover, now).Exec(ctx); err != nil {
					return err
				}
			}
			if wasCover {
				if _, err := tx.NewRaw(`UPDATE draft_moments SET cover_media_item_id = NULL WHERE id = ?`, *momentID).Exec(ctx); err != nil {
					return err
				}
			}
			if _, err := tx.NewRaw(`DELETE FROM draft_media_placements WHERE event_id = ? AND media_item_id = ?`, eventID, mediaID).Exec(ctx); err != nil {
				return err
			}
			if momentID != nil && !sourceMissing {
				changedMomentIDs[*momentID] = struct{}{}
			}
			changed = true
		}
		if !changed {
			continue
		}
		for momentID := range changedMomentIDs {
			if _, restored := restoredMomentReviews[momentID]; restored {
				continue
			}
			if err := invalidateSourceChangedMomentReview(ctx, tx, momentID); err != nil {
				return err
			}
		}
		if _, err := staging.InvalidateEvent(ctx, tx, lockedEventID, now); err != nil {
			return err
		}
	}
	return nil
}

func invalidateSourceChangedMomentReview(ctx context.Context, tx bun.Tx, momentID uuid.UUID) error {
	if _, err := tx.NewRaw(`
		UPDATE draft_moments
		SET attendance_complete = false, audience_complete = false,
			review_version = review_version + 1
		WHERE id = ?
	`, momentID).Exec(ctx); err != nil {
		return err
	}
	for _, statement := range []string{
		`DELETE FROM attendance WHERE moment_id = ?`,
		`DELETE FROM audience_proposals WHERE target_kind = 'moment' AND target_id = ?`,
		`DELETE FROM audience_overrides WHERE target_kind = 'moment' AND target_id = ?`,
		`DELETE FROM current_audience_snapshots WHERE target_kind = 'moment' AND target_id = ?`,
	} {
		if _, err := tx.NewRaw(statement, momentID).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

type databaseMediaItem struct {
	PortalID          uuid.UUID `json:"portal_id"`
	BackingID         uuid.UUID `json:"backing_id"`
	ImmichAssetID     uuid.UUID `json:"immich_asset_id"`
	MediaType         string    `json:"media_type"`
	Width             *int      `json:"width"`
	Height            *int      `json:"height"`
	LocalDateTime     *string   `json:"local_date_time"`
	CaptureAt         string    `json:"capture_at"`
	Checksum          string    `json:"checksum"`
	Filename          string    `json:"filename"`
	OriginalPath      string    `json:"original_path"`
	SourceFingerprint string    `json:"source_fingerprint"`
}

func upsertMediaItemBatch(ctx context.Context, tx bun.Tx, sourceAlbumID uuid.UUID, assets []immich.AssetSummary, now time.Time) ([]uuid.UUID, error) {
	if len(assets) == 0 {
		return nil, nil
	}
	rows := make([]databaseMediaItem, 0, len(assets))
	for _, asset := range assets {
		portalID, err := uuid.NewRandom()
		if err != nil {
			return nil, err
		}
		backingID, err := uuid.NewRandom()
		if err != nil {
			return nil, err
		}
		fingerprint := fingerprintAsset(asset)
		rows = append(rows, databaseMediaItem{
			PortalID: portalID, BackingID: backingID, ImmichAssetID: asset.SourceID, MediaType: asset.MediaType,
			Width: asset.Width, Height: asset.Height, LocalDateTime: asset.LocalDateTime,
			CaptureAt: asset.CaptureAt, Checksum: asset.Checksum, Filename: asset.Filename, OriginalPath: asset.OriginalPath,
			SourceFingerprint: hex.EncodeToString(fingerprint[:]),
		})
	}
	payload, err := json.Marshal(rows)
	if err != nil {
		return nil, err
	}
	incoming := `jsonb_to_recordset(?::jsonb) AS incoming(
		portal_id uuid, backing_id uuid, immich_asset_id uuid, media_type text, width integer,
		height integer, local_date_time text, capture_at text, checksum text,
		filename text, original_path text, source_fingerprint text
	)`
	var metadataChangedMediaIDs []uuid.UUID
	if err := tx.NewRaw(`
		SELECT media.id
		FROM `+incoming+`
		JOIN media_items AS media ON media.immich_asset_id = incoming.immich_asset_id
		WHERE (media.media_type, media.width, media.height, media.local_date_time)
		      IS DISTINCT FROM (incoming.media_type, incoming.width, incoming.height, incoming.local_date_time)
		ORDER BY media.id
	`, string(payload)).Scan(ctx, &metadataChangedMediaIDs); err != nil {
		return nil, err
	}
	// Album membership proves metadata presence, not that derivatives or original
	// bytes are serveable. Only an explicit confirmed relink may clear source_missing.
	// Inactive backing history is also a tombstone for a source identity retired by
	// that relink, so stale album metadata cannot import it as a new portal identity.
	if _, err := tx.NewRaw(`
		INSERT INTO media_items (
			id, immich_asset_id, media_type, width, height, local_date_time, first_seen_at, last_seen_at, updated_at
		)
		SELECT portal_id, immich_asset_id, media_type, width, height, local_date_time, ?, ?, ?
		FROM `+incoming+`
		WHERE NOT EXISTS (
			SELECT 1 FROM media_backings AS retired
			WHERE retired.immich_asset_id = incoming.immich_asset_id AND NOT retired.active
		)
		ON CONFLICT (immich_asset_id) DO UPDATE SET
			media_type = EXCLUDED.media_type, width = EXCLUDED.width, height = EXCLUDED.height,
			local_date_time = EXCLUDED.local_date_time, last_seen_at = EXCLUDED.last_seen_at,
			updated_at = EXCLUDED.updated_at
	`, now, now, now, string(payload)).Exec(ctx); err != nil {
		return nil, err
	}
	if _, err := tx.NewRaw(`
		INSERT INTO media_backings (
			id, media_item_id, immich_asset_id, checksum, capture_at, filename, original_path, linked_at
		)
		SELECT incoming.backing_id, media.id, incoming.immich_asset_id,
			NULLIF(incoming.checksum, ''), NULLIF(incoming.capture_at, ''), incoming.filename, incoming.original_path, ?
		FROM `+incoming+`
		JOIN media_items AS media ON media.immich_asset_id = incoming.immich_asset_id
		WHERE NOT EXISTS (
			SELECT 1 FROM media_backings AS retired
			WHERE retired.immich_asset_id = incoming.immich_asset_id AND NOT retired.active
		)
		ON CONFLICT (immich_asset_id) WHERE active DO UPDATE SET
			checksum = EXCLUDED.checksum, capture_at = EXCLUDED.capture_at,
			filename = EXCLUDED.filename, original_path = EXCLUDED.original_path
	`, now, string(payload)).Exec(ctx); err != nil {
		return nil, err
	}
	_, err = tx.NewRaw(`
		INSERT INTO source_album_memberships (
			source_album_id, immich_asset_id, media_item_id, first_seen_at, last_seen_at, source_fingerprint
		)
		SELECT ?, incoming.immich_asset_id, media.id, ?, ?, decode(incoming.source_fingerprint, 'hex')
		FROM `+incoming+`
		JOIN media_items AS media ON media.immich_asset_id = incoming.immich_asset_id
		WHERE NOT EXISTS (
			SELECT 1 FROM media_backings AS retired
			WHERE retired.immich_asset_id = incoming.immich_asset_id AND NOT retired.active
		)
		ON CONFLICT (source_album_id, immich_asset_id) DO UPDATE SET
			media_item_id = EXCLUDED.media_item_id,
			last_seen_at = EXCLUDED.last_seen_at,
			source_fingerprint = EXCLUDED.source_fingerprint
	`, sourceAlbumID, now, now, string(payload)).Exec(ctx)
	return metadataChangedMediaIDs, err
}

func supersedeInvalidMediaRepairs(ctx context.Context, tx bun.Tx, now time.Time) error {
	_, err := tx.NewRaw(`
		UPDATE media_repair_candidates AS repair
		SET state = 'superseded', resolved_at = ?, candidate_media_item_id = NULL
		WHERE repair.state = 'pending'
		  AND NOT EXISTS (
			SELECT 1
			FROM media_backings AS previous
			JOIN media_items AS previous_item ON previous_item.id = previous.media_item_id
			JOIN media_backings AS candidate ON candidate.media_item_id = repair.candidate_media_item_id
				AND candidate.immich_asset_id = repair.candidate_immich_asset_id AND candidate.active
			JOIN media_items AS candidate_item ON candidate_item.id = candidate.media_item_id
			WHERE previous.media_item_id = repair.media_item_id
			  AND previous.immich_asset_id = repair.previous_immich_asset_id AND previous.active
			  AND previous.checksum IS NOT NULL AND previous.checksum = candidate.checksum
			  AND previous_item.media_type = candidate_item.media_type
			  AND previous_item.availability = 'source_missing' AND candidate_item.availability = 'current'
			  AND EXISTS (SELECT 1 FROM source_album_memberships WHERE media_item_id = candidate_item.id)
		  )
	`, now).Exec(ctx)
	return err
}

func proposeMediaRepairs(ctx context.Context, tx bun.Tx, now time.Time) error {
	_, err := tx.NewRaw(`
		INSERT INTO media_repair_candidates (
			id, media_item_id, candidate_media_item_id, previous_immich_asset_id,
			candidate_immich_asset_id, previous_evidence, candidate_evidence,
			face_anchor_evidence, conflict_evidence, created_at
		)
		SELECT gen_random_uuid(), missing.media_item_id, addition.media_item_id,
			missing.immich_asset_id, addition.immich_asset_id,
			jsonb_build_object(
				'checksum', missing.checksum, 'capture', missing.capture_at,
				'filename', missing.filename, 'path', missing.original_path
			),
			jsonb_build_object(
				'checksum', addition.checksum, 'capture', addition.capture_at,
				'filename', addition.filename, 'path', addition.original_path
			),
			COALESCE((
				SELECT jsonb_agg(jsonb_build_object(
					'face_id', anchor.immich_face_id, 'asset_id', anchor.immich_asset_id,
					'checksum', anchor.asset_checksum, 'image_width', anchor.image_width,
					'image_height', anchor.image_height, 'x1', anchor.x1, 'y1', anchor.y1,
					'x2', anchor.x2, 'y2', anchor.y2,
					'last_immich_person_id', anchor.last_linked_immich_person_id
				) ORDER BY anchor.immich_asset_id, anchor.immich_face_id)
				FROM immich_face_anchors AS anchor
				WHERE anchor.immich_asset_id IN (missing.immich_asset_id, addition.immich_asset_id)
			), '[]'::jsonb),
			CASE WHEN count(*) OVER (PARTITION BY missing.checksum) > 1
				THEN jsonb_build_array('checksum_matches_multiple_media') ELSE '[]'::jsonb END,
			?
		FROM media_backings AS missing
		JOIN media_items AS missing_item ON missing_item.id = missing.media_item_id
		JOIN media_backings AS addition ON addition.checksum = missing.checksum AND addition.media_item_id <> missing.media_item_id
		JOIN media_items AS addition_item ON addition_item.id = addition.media_item_id
		WHERE missing.active AND addition.active AND missing.checksum IS NOT NULL
		  AND missing_item.media_type = addition_item.media_type
		  AND missing_item.availability = 'source_missing' AND addition_item.availability = 'current'
		  AND NOT EXISTS (
			SELECT 1 FROM media_repair_candidates AS rejected
			WHERE rejected.media_item_id = missing.media_item_id
			  AND rejected.candidate_immich_asset_id = addition.immich_asset_id
			  AND rejected.state = 'rejected'
		  )
		ON CONFLICT (media_item_id, candidate_immich_asset_id) WHERE state = 'pending' DO UPDATE SET
			candidate_media_item_id = EXCLUDED.candidate_media_item_id,
			previous_evidence = EXCLUDED.previous_evidence,
			candidate_evidence = EXCLUDED.candidate_evidence,
			face_anchor_evidence = EXCLUDED.face_anchor_evidence,
			conflict_evidence = EXCLUDED.conflict_evidence
	`, now).Exec(ctx)
	return err
}

func recordMissingSourceAlbum(ctx context.Context, tx bun.Tx, sourceAlbumID uuid.UUID, now time.Time, interval time.Duration) error {
	_, err := tx.NewRaw(`
		UPDATE source_albums SET
			source_missing = true, missing_since = COALESCE(missing_since, ?),
			version = version + CASE WHEN source_missing THEN 0 ELSE 1 END,
			next_reconciliation_at = ?, updated_at = ?
		WHERE id = ?
	`, now, now.Add(interval), now, sourceAlbumID).Exec(ctx)
	return err
}

func recordFailedReconciliation(
	ctx context.Context,
	tx bun.Tx,
	sourceAlbumID uuid.UUID,
	now time.Time,
	status string,
	snapshot reconciliationSnapshot,
) error {
	if err := recordReconciliationRun(ctx, tx, sourceAlbumID, now, status, snapshot); err != nil {
		return err
	}
	_, err := tx.NewRaw(`UPDATE source_albums SET next_reconciliation_at = ?, updated_at = ? WHERE id = ?`, now, now, sourceAlbumID).Exec(ctx)
	return err
}

func recordReconciliationRun(
	ctx context.Context,
	tx bun.Tx,
	sourceAlbumID uuid.UUID,
	now time.Time,
	status string,
	snapshot reconciliationSnapshot,
) error {
	runID, err := uuid.NewRandom()
	if err != nil {
		return err
	}
	var before, after any
	if snapshot.before.SourceID != uuid.Nil {
		fingerprint, err := albumFingerprint(snapshot.before)
		if err != nil {
			return err
		}
		before = fingerprint[:]
	}
	if snapshot.after.SourceID != uuid.Nil {
		fingerprint, err := albumFingerprint(snapshot.after)
		if err != nil {
			return err
		}
		after = fingerprint[:]
	}
	_, err = tx.NewRaw(`
		INSERT INTO reconciliation_runs (
			id, source_album_id, status, diagnostic, before_summary_fingerprint,
			after_summary_fingerprint, started_at, completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, runID, sourceAlbumID, status, snapshot.diagnostic, before, after, now, sNow(now)).Exec(ctx)
	return err
}

func recordValidatedRun(
	ctx context.Context,
	tx bun.Tx,
	sourceAlbumID uuid.UUID,
	now time.Time,
	snapshot reconciliationSnapshot,
	stablePasses, additions, removals int,
) error {
	runID, err := uuid.NewRandom()
	if err != nil {
		return err
	}
	before, err := albumFingerprint(snapshot.before)
	if err != nil {
		return err
	}
	after, err := albumFingerprint(snapshot.after)
	if err != nil {
		return err
	}
	_, err = tx.NewRaw(`
		INSERT INTO reconciliation_runs (
			id, source_album_id, status, before_summary_fingerprint, after_summary_fingerprint,
			membership_fingerprint, stable_passes, addition_count, removal_count, started_at, completed_at
		) VALUES (?, ?, 'validated', ?, ?, ?, ?, ?, ?, ?, ?)
	`, runID, sourceAlbumID, before[:], after[:], snapshot.fingerprint[:], stablePasses, additions, removals, now, sNow(now)).Exec(ctx)
	return err
}

func sNow(start time.Time) time.Time {
	completed := time.Now().UTC()
	if completed.Before(start) {
		return start
	}
	return completed
}

func membershipFingerprint(assets map[uuid.UUID]immich.AssetSummary) [32]byte {
	ids := make([]string, 0, len(assets))
	for id := range assets {
		ids = append(ids, id.String())
	}
	sort.Strings(ids)
	hash := sha256.New()
	for _, id := range ids {
		_, _ = hash.Write([]byte(id))
		_, _ = hash.Write([]byte{0})
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func fingerprintAsset(asset immich.AssetSummary) [32]byte {
	encoded, _ := json.Marshal(asset)
	return sha256.Sum256(encoded)
}

func sameAsset(left, right immich.AssetSummary) bool {
	if left.SourceID != right.SourceID || left.MediaType != right.MediaType ||
		!sameOptionalString(left.LocalDateTime, right.LocalDateTime) || left.CaptureAt != right.CaptureAt ||
		left.Checksum != right.Checksum || left.Filename != right.Filename || left.OriginalPath != right.OriginalPath {
		return false
	}
	return sameOptionalInt(left.Width, right.Width) && sameOptionalInt(left.Height, right.Height)
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func sameOptionalInt(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
