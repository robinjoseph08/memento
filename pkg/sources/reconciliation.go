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
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/uptrace/bun"
)

const ReconciliationJobKind = "reconcile_source_album"

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
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var immichAlbumID uuid.UUID
		if err := tx.NewRaw(`SELECT immich_album_id FROM source_albums WHERE id = ? FOR UPDATE`, sourceAlbumID).Scan(ctx, &immichAlbumID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("lock Source album reconciliation: %w", err)
		}

		snapshot, snapshotErr := s.readSnapshot(ctx, immichAlbumID)
		if snapshotErr != nil {
			outcome = snapshotErr
			status := "unstable"
			if errors.Is(snapshotErr, ErrDependency) {
				status = "failed"
			}
			if err := recordReconciliationRun(ctx, tx, sourceAlbumID, now, status, snapshot); err != nil {
				return err
			}
			_, err := tx.NewRaw(`UPDATE source_albums SET next_reconciliation_at = ?, updated_at = ? WHERE id = ?`, now, now, sourceAlbumID).Exec(ctx)
			return err
		}

		stablePasses, additions, removals, err := s.applyValidatedSnapshot(ctx, tx, sourceAlbumID, snapshot, now)
		if err != nil {
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
	var additions int
	if err := tx.NewRaw(`
		SELECT count(*)
		FROM jsonb_array_elements_text(?::jsonb) AS incoming(id)
		WHERE NOT EXISTS (
			SELECT 1 FROM source_album_memberships
			WHERE source_album_id = ? AND immich_asset_id = incoming.id::uuid
		)
	`, string(encodedIDs), sourceAlbumID).Scan(ctx, &additions); err != nil {
		return 0, 0, 0, err
	}
	for start := 0; start < len(assetIDs); start += 1000 {
		end := min(start+1000, len(assetIDs))
		batch := make([]immich.AssetSummary, 0, end-start)
		for _, assetID := range assetIDs[start:end] {
			batch = append(batch, snapshot.assets[assetID])
		}
		if err := upsertMediaItemBatch(ctx, tx, sourceAlbumID, batch, now); err != nil {
			return 0, 0, 0, err
		}
	}

	removals := 0
	if stablePasses >= 2 {
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
		if _, err := tx.NewRaw(`
			UPDATE media_items AS media SET availability = 'source_missing', updated_at = ?
			WHERE availability = 'current'
			  AND NOT EXISTS (SELECT 1 FROM source_album_memberships WHERE media_item_id = media.id)
		`, now).Exec(ctx); err != nil {
			return 0, 0, 0, err
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

	summaryFingerprint, err := albumFingerprint(snapshot.after)
	if err != nil {
		return 0, 0, 0, err
	}
	_, err = tx.NewRaw(`
		UPDATE source_albums SET
			name = ?, description = ?, asset_count = ?, source_created_at = ?, source_updated_at = ?,
			source_start_at = ?, source_end_at = ?, source_last_modified_asset_at = ?,
			source_fingerprint = ?, candidate_membership_fingerprint = ?, candidate_membership_passes = ?,
			last_reconciled_at = ?, next_reconciliation_at = ?, updated_at = ?
		WHERE id = ?
	`, snapshot.after.Name, snapshot.after.Description, snapshot.after.AssetCount,
		snapshot.after.CreatedAt, snapshot.after.UpdatedAt, snapshot.after.StartDate, snapshot.after.EndDate,
		snapshot.after.LastModifiedAssetTimestamp, summaryFingerprint[:], snapshot.fingerprint[:], stablePasses,
		now, now.Add(s.reconciliationInterval), now, sourceAlbumID).Exec(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	return stablePasses, additions, removals, nil
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

func upsertMediaItemBatch(ctx context.Context, tx bun.Tx, sourceAlbumID uuid.UUID, assets []immich.AssetSummary, now time.Time) error {
	if len(assets) == 0 {
		return nil
	}
	rows := make([]databaseMediaItem, 0, len(assets))
	for _, asset := range assets {
		portalID, err := uuid.NewRandom()
		if err != nil {
			return err
		}
		backingID, err := uuid.NewRandom()
		if err != nil {
			return err
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
		return err
	}
	incoming := `jsonb_to_recordset(?::jsonb) AS incoming(
		portal_id uuid, backing_id uuid, immich_asset_id uuid, media_type text, width integer,
		height integer, local_date_time text, capture_at text, checksum text,
		filename text, original_path text, source_fingerprint text
	)`
	if _, err := tx.NewRaw(`
		INSERT INTO media_items (
			id, immich_asset_id, media_type, width, height, local_date_time, first_seen_at, last_seen_at, updated_at
		)
		SELECT portal_id, immich_asset_id, media_type, width, height, local_date_time, ?, ?, ?
		FROM `+incoming+`
		ON CONFLICT (immich_asset_id) DO UPDATE SET
			media_type = EXCLUDED.media_type, width = EXCLUDED.width, height = EXCLUDED.height,
			local_date_time = EXCLUDED.local_date_time, availability = 'current', last_seen_at = EXCLUDED.last_seen_at,
			updated_at = EXCLUDED.updated_at
	`, now, now, now, string(payload)).Exec(ctx); err != nil {
		return err
	}
	if _, err := tx.NewRaw(`
		INSERT INTO media_backings (
			id, media_item_id, immich_asset_id, checksum, capture_at, filename, original_path, linked_at
		)
		SELECT incoming.backing_id, media.id, incoming.immich_asset_id,
			NULLIF(incoming.checksum, ''), NULLIF(incoming.capture_at, ''), incoming.filename, incoming.original_path, ?
		FROM `+incoming+`
		JOIN media_items AS media ON media.immich_asset_id = incoming.immich_asset_id
		ON CONFLICT (immich_asset_id) WHERE active DO UPDATE SET
			checksum = EXCLUDED.checksum, capture_at = EXCLUDED.capture_at,
			filename = EXCLUDED.filename, original_path = EXCLUDED.original_path
	`, now, string(payload)).Exec(ctx); err != nil {
		return err
	}
	_, err = tx.NewRaw(`
		INSERT INTO source_album_memberships (
			source_album_id, immich_asset_id, media_item_id, first_seen_at, last_seen_at, source_fingerprint
		)
		SELECT ?, incoming.immich_asset_id, media.id, ?, ?, decode(incoming.source_fingerprint, 'hex')
		FROM `+incoming+`
		JOIN media_items AS media ON media.immich_asset_id = incoming.immich_asset_id
		ON CONFLICT (source_album_id, immich_asset_id) DO UPDATE SET
			media_item_id = EXCLUDED.media_item_id,
			last_seen_at = EXCLUDED.last_seen_at,
			source_fingerprint = EXCLUDED.source_fingerprint
	`, sourceAlbumID, now, now, string(payload)).Exec(ctx)
	return err
}

func proposeMediaRepairs(ctx context.Context, tx bun.Tx, now time.Time) error {
	_, err := tx.NewRaw(`
		INSERT INTO media_repair_candidates (
			id, media_item_id, candidate_media_item_id, previous_immich_asset_id,
			candidate_immich_asset_id, conflict_evidence, created_at
		)
		SELECT gen_random_uuid(), missing.media_item_id, addition.media_item_id,
			missing.immich_asset_id, addition.immich_asset_id,
			CASE WHEN count(*) OVER (PARTITION BY missing.checksum) > 1
				THEN jsonb_build_array('checksum_matches_multiple_media') ELSE '[]'::jsonb END,
			?
		FROM media_backings AS missing
		JOIN media_items AS missing_item ON missing_item.id = missing.media_item_id
		JOIN media_backings AS addition ON addition.checksum = missing.checksum AND addition.media_item_id <> missing.media_item_id
		JOIN media_items AS addition_item ON addition_item.id = addition.media_item_id
		WHERE missing.active AND addition.active AND missing.checksum IS NOT NULL
		  AND missing_item.availability = 'source_missing' AND addition_item.availability = 'current'
		  AND NOT EXISTS (
			SELECT 1 FROM media_repair_candidates AS rejected
			WHERE rejected.media_item_id = missing.media_item_id
			  AND rejected.candidate_immich_asset_id = addition.immich_asset_id
			  AND rejected.state = 'rejected'
		  )
		ON CONFLICT (media_item_id, candidate_immich_asset_id) WHERE state = 'pending' DO UPDATE SET
			candidate_media_item_id = EXCLUDED.candidate_media_item_id,
			conflict_evidence = EXCLUDED.conflict_evidence
	`, now).Exec(ctx)
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
	if left.SourceID != right.SourceID || left.MediaType != right.MediaType || !sameOptionalString(left.LocalDateTime, right.LocalDateTime) {
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
