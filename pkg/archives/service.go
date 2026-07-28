// Package archives plans and streams expiring, Session-bound Archive downloads.
package archives

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/internal/placementlock"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/uptrace/bun"
)

const (
	planLifetime        = 15 * time.Minute
	maximumSelection    = 1000
	maximumArchiveParts = 1000
)

var (
	ErrInvalidSelection = errors.New("invalid archive selection")
	ErrNotFound         = errors.New("archive not found")
	ErrUnavailable      = errors.New("archive unavailable")
)

type PlanRequest struct {
	Scope    string   `json:"scope"`
	EventID  *string  `json:"event_id" tstype:"string | null,required"`
	MediaIDs []string `json:"media_ids"`
}

type PartSummary struct {
	PartNumber  int    `json:"part_number"`
	Size        int64  `json:"size"`
	Filename    string `json:"filename"`
	DownloadURL string `json:"download_url"`
}

type PlanResponse struct {
	Name      string        `json:"name"`
	ItemCount int           `json:"item_count"`
	TotalSize int64         `json:"total_size"`
	ExpiresAt time.Time     `json:"expires_at"`
	Parts     []PartSummary `json:"parts"`
}

type Stream struct {
	Body          io.ReadCloser
	ContentLength int64
	Filename      string
}

type archiveSource interface {
	ArchiveInfo(ctx context.Context, assetIDs []uuid.UUID) ([]immich.ArchivePart, error)
	Original(ctx context.Context, assetID uuid.UUID, request immich.MediaRequest) (immich.MediaResponse, error)
}

type Service struct {
	db     *bun.DB
	source archiveSource
	now    func() time.Time
}

func New(db *bun.DB, source archiveSource) *Service {
	return &Service{db: db, source: source, now: time.Now}
}

type candidate struct {
	MediaID    uuid.UUID `bun:"media_item_id"`
	BackingID  uuid.UUID `bun:"backing_id"`
	AssetID    uuid.UUID `bun:"asset_id"`
	EventID    uuid.UUID `bun:"event_id"`
	EventTitle string    `bun:"event_title"`
}

const authorizedCandidates = `
	SELECT DISTINCT ON (valid.media_item_id)
	       valid.media_item_id, backing.id AS backing_id, backing.immich_asset_id AS asset_id,
	       valid.event_id, current.title AS event_title
	FROM (` + `
		SELECT placement.event_id, placement.publication_id, placement.published_moment_id,
		       placement.media_item_id, placement.position
		FROM current_published_placements AS placement
		JOIN events AS event ON event.id = placement.event_id AND event.lifecycle = 'published'
		JOIN current_audience_entitlements AS entitlement
		  ON entitlement.event_id = placement.event_id
		 AND entitlement.publication_id = placement.publication_id
		 AND entitlement.media_item_id = placement.media_item_id
		 AND entitlement.recipient_access_generation_id = ?
		JOIN published_moments AS moment ON moment.id = placement.published_moment_id
		JOIN media_items AS media ON media.id = placement.media_item_id AND media.availability = 'current'
		WHERE NOT content_is_withdrawn(placement.event_id, moment.draft_moment_id, placement.media_item_id)
	` + `) AS valid
	JOIN current_published_events AS current
	  ON current.event_id = valid.event_id AND current.publication_id = valid.publication_id
	JOIN media_backings AS backing ON backing.media_item_id = valid.media_item_id AND backing.active
`

func (s *Service) resolve(ctx context.Context, db bun.IDB, actor setup.SessionActor, request PlanRequest) ([]candidate, uuid.UUID, string, error) {
	if err := ensureActor(ctx, db, actor); err != nil {
		return nil, uuid.Nil, "", err
	}
	switch request.Scope {
	case "event":
		if request.EventID == nil || len(request.MediaIDs) != 0 {
			return nil, uuid.Nil, "", ErrInvalidSelection
		}
		eventID, err := uuid.Parse(*request.EventID)
		if err != nil || eventID == uuid.Nil {
			return nil, uuid.Nil, "", ErrInvalidSelection
		}
		var rows []candidate
		query := authorizedCandidates + ` WHERE valid.event_id = ? ORDER BY valid.media_item_id, valid.position`
		if err := db.NewRaw(query, actor.AccessID, eventID).Scan(ctx, &rows); err != nil {
			return nil, uuid.Nil, "", err
		}
		if len(rows) == 0 {
			return nil, uuid.Nil, "", ErrNotFound
		}
		return rows, eventID, rows[0].EventTitle, nil
	case "subset":
		if request.EventID != nil || len(request.MediaIDs) == 0 || len(request.MediaIDs) > maximumSelection {
			return nil, uuid.Nil, "", ErrInvalidSelection
		}
		ids := make([]uuid.UUID, len(request.MediaIDs))
		seen := make(map[uuid.UUID]struct{}, len(ids))
		for index, rawID := range request.MediaIDs {
			id, err := uuid.Parse(rawID)
			if err != nil || id == uuid.Nil {
				return nil, uuid.Nil, "", ErrInvalidSelection
			}
			if _, duplicate := seen[id]; duplicate {
				return nil, uuid.Nil, "", ErrInvalidSelection
			}
			seen[id] = struct{}{}
			ids[index] = id
		}
		var rows []candidate
		query := authorizedCandidates + ` WHERE valid.media_item_id IN (?) ORDER BY valid.media_item_id, current.committed_at DESC, valid.event_id`
		if err := db.NewRaw(query, actor.AccessID, bun.List(ids)).Scan(ctx, &rows); err != nil {
			return nil, uuid.Nil, "", err
		}
		if len(rows) != len(ids) {
			return nil, uuid.Nil, "", ErrInvalidSelection
		}
		return rows, uuid.Nil, "Memento selection", nil
	default:
		return nil, uuid.Nil, "", ErrInvalidSelection
	}
}

func sameCandidates(left, right []candidate) bool {
	if len(left) != len(right) {
		return false
	}
	slices.SortFunc(left, func(a, b candidate) int { return strings.Compare(a.MediaID.String(), b.MediaID.String()) })
	slices.SortFunc(right, func(a, b candidate) int { return strings.Compare(a.MediaID.String(), b.MediaID.String()) })
	return slices.Equal(left, right)
}

func (s *Service) Plan(ctx context.Context, actor setup.SessionActor, request PlanRequest) (PlanResponse, error) {
	if s.db == nil || s.source == nil {
		return PlanResponse{}, ErrUnavailable
	}
	initial, eventID, title, err := s.resolve(ctx, s.db, actor, request)
	if err != nil {
		return PlanResponse{}, err
	}
	assetToCandidate := make(map[uuid.UUID]candidate, len(initial))
	assets := make([]uuid.UUID, len(initial))
	for index, item := range initial {
		assets[index] = item.AssetID
		assetToCandidate[item.AssetID] = item
	}
	parts, err := s.source.ArchiveInfo(ctx, assets)
	if err != nil || len(parts) == 0 || len(parts) > maximumArchiveParts {
		return PlanResponse{}, ErrUnavailable
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return PlanResponse{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	tokenHash := sha256.Sum256(tokenBytes)
	planID := uuid.New()
	now := s.now().UTC()
	expiresAt := now.Add(planLifetime)
	name := safeArchiveName(title)
	response := PlanResponse{Name: name, ItemCount: len(initial), ExpiresAt: expiresAt, Parts: make([]PartSummary, 0, len(parts))}

	err = s.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead}, func(ctx context.Context, tx bun.Tx) error {
		current, currentEventID, currentTitle, err := s.resolve(ctx, tx, actor, request)
		if err != nil {
			return err
		}
		if currentEventID != eventID || currentTitle != title || !sameCandidates(append([]candidate(nil), initial...), current) {
			return ErrInvalidSelection
		}
		var totalSize int64
		for _, part := range parts {
			if part.Size < 0 || len(part.AssetIDs) == 0 || part.Size > (1<<63-1)-totalSize {
				return ErrUnavailable
			}
			totalSize += part.Size
		}
		var storedEventID any
		if eventID != uuid.Nil {
			storedEventID = eventID
		}
		if _, err := tx.NewRaw(`INSERT INTO archive_plans
			(id, token_hash, recipient_person_id, recipient_access_generation_id, session_id,
			 scope, event_id, name, item_count, total_size, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, planID, tokenHash[:], actor.PersonID,
			actor.AccessID, actor.SessionID, request.Scope, storedEventID, name, len(initial), totalSize, now, expiresAt).Exec(ctx); err != nil {
			return err
		}
		for partIndex, part := range parts {
			partID := uuid.New()
			if _, err := tx.NewRaw(`INSERT INTO archive_parts (id, archive_plan_id, part_number, size)
				VALUES (?, ?, ?, ?)`, partID, planID, partIndex+1, part.Size).Exec(ctx); err != nil {
				return err
			}
			for position, assetID := range part.AssetIDs {
				primaryID := assetID
				companion := false
				if expandedFrom, expanded := part.CompanionOf[assetID]; expanded {
					primaryID = expandedFrom
					companion = true
				}
				item, found := assetToCandidate[primaryID]
				if !found {
					return ErrUnavailable
				}
				entryKind := "media"
				if companion {
					entryKind = "live-photo"
				}
				entryName := fmt.Sprintf("%s/%04d-%s", safeArchiveName(item.EventTitle), position+1, entryKind)
				if _, err := tx.NewRaw(`INSERT INTO archive_part_items
					(archive_part_id, position, media_item_id, media_backing_id, immich_asset_id, entry_name, is_live_photo_companion)
					VALUES (?, ?, ?, ?, ?, ?, ?)`, partID, position, item.MediaID, item.BackingID, assetID, entryName, companion).Exec(ctx); err != nil {
					return err
				}
			}
		}
		response.TotalSize = totalSize
		return nil
	})
	if err != nil {
		return PlanResponse{}, err
	}
	for index, part := range parts {
		partNumber := index + 1
		filename := partFilename(name, partNumber, len(parts))
		response.Parts = append(response.Parts, PartSummary{
			PartNumber: partNumber, Size: part.Size, Filename: filename,
			DownloadURL: fmt.Sprintf("/api/me/archives/parts/%d?token=%s", partNumber, token),
		})
	}
	return response, nil
}

var repeatedSeparator = regexp.MustCompile(`[-_ ]+`)

func safeArchiveName(title string) string {
	var name strings.Builder
	for _, value := range strings.TrimSpace(title) {
		switch {
		case unicode.IsLetter(value), unicode.IsDigit(value):
			name.WriteRune(value)
		case value == ' ', value == '-', value == '_':
			name.WriteRune(value)
		default:
			name.WriteByte('-')
		}
	}
	result := strings.Trim(repeatedSeparator.ReplaceAllString(name.String(), "-"), "-_. ")
	if result == "" || result == "." || result == ".." {
		result = "Memento-Event"
	}
	runes := []rune(result)
	if len(runes) > 140 {
		result = strings.TrimRight(string(runes[:140]), "-_. ")
	}
	return result
}

func partFilename(name string, part, total int) string {
	if total == 1 {
		return name + ".zip"
	}
	return fmt.Sprintf("%s-part-%d-of-%d.zip", name, part, total)
}

type plannedItem struct {
	Position            int           `bun:"position"`
	MediaID             uuid.UUID     `bun:"media_item_id"`
	BackingID           uuid.UUID     `bun:"media_backing_id"`
	AssetID             uuid.UUID     `bun:"immich_asset_id"`
	PrimaryAssetID      uuid.UUID     `bun:"primary_asset_id"`
	ExpectedCompanionID uuid.NullUUID `bun:"expected_companion_id"`
	EntryName           string        `bun:"entry_name"`
	Companion           bool          `bun:"is_live_photo_companion"`
}

type plannedPart struct {
	PlanID     uuid.UUID
	PartID     uuid.UUID
	Name       string
	PartNumber int
	PartCount  int
	ConsumedAt *time.Time
	ExpiresAt  time.Time
	Items      []plannedItem
}

func decodeToken(raw string) ([32]byte, error) {
	var result [32]byte
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) != len(result) || base64.RawURLEncoding.EncodeToString(decoded) != raw {
		return result, ErrNotFound
	}
	copy(result[:], decoded)
	return result, nil
}

func (s *Service) loadPart(ctx context.Context, db bun.IDB, actor setup.SessionActor, rawToken string, number int, lock bool) (plannedPart, error) {
	if number < 1 {
		return plannedPart{}, ErrNotFound
	}
	token, err := decodeToken(rawToken)
	if err != nil {
		return plannedPart{}, err
	}
	hash := sha256.Sum256(token[:])
	if err := ensureActor(ctx, db, actor); err != nil {
		return plannedPart{}, err
	}
	lockClause := ""
	if lock {
		lockClause = " FOR UPDATE OF part"
	}
	var result plannedPart
	query := `SELECT plan.id, part.id, plan.name, part.part_number,
		(SELECT count(*) FROM archive_parts AS count_part WHERE count_part.archive_plan_id = plan.id),
		part.consumed_at, plan.expires_at
		FROM archive_plans AS plan JOIN archive_parts AS part ON part.archive_plan_id = plan.id
		WHERE plan.token_hash = ? AND plan.recipient_person_id = ?
		  AND plan.recipient_access_generation_id = ? AND plan.session_id = ?
		  AND part.part_number = ?` + lockClause
	if err := db.NewRaw(query, hash[:], actor.PersonID, actor.AccessID, actor.SessionID, number).Scan(ctx,
		&result.PlanID, &result.PartID, &result.Name, &result.PartNumber, &result.PartCount,
		&result.ConsumedAt, &result.ExpiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return plannedPart{}, ErrNotFound
		}
		return plannedPart{}, err
	}
	if result.ConsumedAt != nil || !s.now().UTC().Before(result.ExpiresAt) {
		return plannedPart{}, ErrNotFound
	}
	if err := db.NewRaw(`SELECT item.position, item.media_item_id, item.media_backing_id,
		item.immich_asset_id, backing.immich_asset_id AS primary_asset_id,
		(SELECT companion.immich_asset_id
		 FROM archive_part_items AS companion
		 JOIN archive_parts AS companion_part ON companion_part.id = companion.archive_part_id
		 WHERE companion_part.archive_plan_id = ? AND companion.media_item_id = item.media_item_id
		   AND companion.is_live_photo_companion LIMIT 1) AS expected_companion_id,
		item.entry_name, item.is_live_photo_companion
		FROM archive_part_items AS item
		JOIN media_backings AS backing ON backing.id = item.media_backing_id
		WHERE item.archive_part_id = ? ORDER BY item.position`, result.PlanID, result.PartID).Scan(ctx, &result.Items); err != nil {
		return plannedPart{}, err
	}
	if len(result.Items) == 0 {
		return plannedPart{}, ErrNotFound
	}
	return result, nil
}

func expectedAssets(part plannedPart) ([]uuid.UUID, []uuid.UUID) {
	all := make([]uuid.UUID, 0, len(part.Items))
	primaries := make([]uuid.UUID, 0, len(part.Items))
	seenPrimary := make(map[uuid.UUID]struct{})
	for _, item := range part.Items {
		all = append(all, item.AssetID)
		if _, seen := seenPrimary[item.PrimaryAssetID]; !seen {
			seenPrimary[item.PrimaryAssetID] = struct{}{}
			primaries = append(primaries, item.PrimaryAssetID)
		}
	}
	return all, primaries
}

func archiveInfoMatches(parts []immich.ArchivePart, items []plannedItem) bool {
	actual := make(map[uuid.UUID]struct{})
	actualCompanions := make(map[uuid.UUID]uuid.UUID)
	currentCompanion := make(map[uuid.UUID]uuid.UUID)
	for _, part := range parts {
		for _, id := range part.AssetIDs {
			actual[id] = struct{}{}
		}
		for companion, primary := range part.CompanionOf {
			actualCompanions[companion] = primary
			currentCompanion[primary] = companion
		}
	}
	for _, item := range items {
		if _, current := actual[item.PrimaryAssetID]; !current {
			return false
		}
		if item.ExpectedCompanionID.Valid {
			if currentCompanion[item.PrimaryAssetID] != item.ExpectedCompanionID.UUID {
				return false
			}
		} else if currentCompanion[item.PrimaryAssetID] != uuid.Nil {
			return false
		}
		if item.Companion && actualCompanions[item.AssetID] != item.PrimaryAssetID {
			return false
		}
	}
	return true
}

func (s *Service) StreamPart(ctx context.Context, actor setup.SessionActor, rawToken string, number int) (Stream, error) {
	if s.db == nil || s.source == nil {
		return Stream{}, ErrUnavailable
	}
	part, err := s.loadPart(ctx, s.db, actor, rawToken, number, false)
	if err != nil {
		return Stream{}, err
	}
	if err := authorizeItems(ctx, s.db, actor, part.Items, false); err != nil {
		return Stream{}, err
	}
	_, primaries := expectedAssets(part)
	info, err := s.source.ArchiveInfo(ctx, primaries)
	if err != nil || !archiveInfoMatches(info, part.Items) {
		return Stream{}, ErrNotFound
	}
	opened, err := openArchive(ctx, s.source, part.Items)
	if err != nil {
		return Stream{}, ErrUnavailable
	}
	err = s.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := placementlock.Acquire(ctx, tx, placementlock.Shared); err != nil {
			return err
		}
		if err := lockActor(ctx, tx, actor); err != nil {
			return err
		}
		locked, err := s.loadPart(ctx, tx, actor, rawToken, number, true)
		if err != nil {
			return err
		}
		if len(locked.Items) != len(part.Items) {
			return ErrNotFound
		}
		for index, item := range locked.Items {
			if item != part.Items[index] {
				return ErrNotFound
			}
		}
		if err := authorizeItems(ctx, tx, actor, locked.Items, true); err != nil {
			return err
		}
		result, err := tx.NewRaw(`UPDATE archive_parts SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL`,
			s.now().UTC(), locked.PartID).Exec(ctx)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil || count != 1 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		_ = opened.Close()
		return Stream{}, err
	}
	return Stream{Body: opened, ContentLength: -1,
		Filename: partFilename(part.Name, part.PartNumber, part.PartCount)}, nil
}

func authorizeItems(ctx context.Context, db bun.IDB, actor setup.SessionActor, items []plannedItem, lock bool) error {
	if lock {
		byMedia := make(map[uuid.UUID]plannedItem)
		for _, item := range items {
			byMedia[item.MediaID] = item
		}
		mediaIDs := make([]uuid.UUID, 0, len(byMedia))
		for mediaID := range byMedia {
			mediaIDs = append(mediaIDs, mediaID)
		}
		slices.SortFunc(mediaIDs, func(left, right uuid.UUID) int {
			return strings.Compare(left.String(), right.String())
		})
		for _, mediaID := range mediaIDs {
			item := byMedia[mediaID]
			var lockedMediaID uuid.UUID
			if err := db.NewRaw(`SELECT id FROM media_items WHERE id = ? FOR SHARE`, mediaID).Scan(ctx, &lockedMediaID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ErrNotFound
				}
				return err
			}
			var backingID uuid.UUID
			if err := db.NewRaw(`SELECT id FROM media_backings
				WHERE id = ? AND media_item_id = ? AND immich_asset_id = ? AND active FOR SHARE`,
				item.BackingID, mediaID, item.PrimaryAssetID).Scan(ctx, &backingID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ErrNotFound
				}
				return err
			}
		}
	}
	for _, item := range items {
		var valid bool
		query := `SELECT EXISTS (` + authorizedCandidates + `
			WHERE valid.media_item_id = ? AND backing.id = ? AND backing.immich_asset_id = ?
			ORDER BY valid.media_item_id LIMIT 1)`
		if err := db.NewRaw(query, actor.AccessID, item.MediaID, item.BackingID, item.PrimaryAssetID).Scan(ctx, &valid); err != nil {
			return err
		}
		if !valid {
			return ErrNotFound
		}
	}
	return nil
}

func openArchive(ctx context.Context, source archiveSource, items []plannedItem) (io.ReadCloser, error) {
	if len(items) == 0 {
		return nil, ErrUnavailable
	}
	first, err := source.Original(ctx, items[0].AssetID, immich.MediaRequest{})
	if err != nil || first.Body == nil {
		return nil, ErrUnavailable
	}
	reader, writer := io.Pipe()
	go func() {
		archive := zip.NewWriter(writer)
		for index, item := range items {
			response := first
			if index > 0 {
				response, err = source.Original(ctx, item.AssetID, immich.MediaRequest{})
				if err != nil || response.Body == nil {
					_ = archive.Close()
					_ = writer.CloseWithError(ErrUnavailable)
					return
				}
			}
			entry, err := archive.CreateHeader(&zip.FileHeader{
				Name: item.EntryName + archiveExtension(response.ContentType), Method: zip.Store,
			})
			if err == nil {
				_, err = io.CopyBuffer(entry, response.Body, make([]byte, 32<<10))
			}
			closeErr := response.Body.Close()
			if err != nil || closeErr != nil {
				_ = archive.Close()
				_ = writer.CloseWithError(ErrUnavailable)
				return
			}
		}
		if err := archive.Close(); err != nil {
			_ = writer.CloseWithError(ErrUnavailable)
			return
		}
		_ = writer.Close()
	}()
	return reader, nil
}

func archiveExtension(contentType string) string {
	extension := map[string]string{
		"application/octet-stream": ".bin", "image/avif": ".avif", "image/bmp": ".bmp",
		"image/gif": ".gif", "image/heic": ".heic", "image/heif": ".heif", "image/jpeg": ".jpg",
		"image/png": ".png", "image/tiff": ".tiff", "image/webp": ".webp", "video/mp4": ".mp4",
		"video/mpeg": ".mpeg", "video/quicktime": ".mov", "video/webm": ".webm",
		"video/x-matroska": ".mkv", "video/x-msvideo": ".avi",
	}[contentType]
	if extension == "" {
		return ".bin"
	}
	return extension
}

func ensureActor(ctx context.Context, db bun.IDB, actor setup.SessionActor) error {
	var valid bool
	if err := db.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM sessions AS session
		JOIN people AS person ON person.id = session.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
		JOIN recipient_access_generations AS access
		  ON access.id = session.recipient_access_generation_id AND access.person_id = session.person_id
		 AND access.is_current AND access.state = 'completed'
		JOIN system_settings AS settings
		  ON settings.id = 1 AND settings.setup_complete AND settings.security_epoch = session.security_epoch
		WHERE session.id = ? AND session.person_id = ? AND session.recipient_access_generation_id = ?
		  AND session.revoked_at IS NULL
		  AND ((session.session_type = 'trusted' AND session.idle_expires_at > now())
		    OR (session.session_type = 'public' AND session.absolute_expires_at > now()))
	)`, actor.SessionID, actor.PersonID, actor.AccessID).Scan(ctx, &valid); err != nil {
		return err
	}
	if !valid {
		return ErrNotFound
	}
	return nil
}

func lockActor(ctx context.Context, tx bun.Tx, actor setup.SessionActor) error {
	for _, lock := range []struct {
		query string
		id    uuid.UUID
	}{
		{`SELECT id FROM system_settings WHERE id = 1 FOR SHARE`, uuid.Nil},
		{`SELECT id FROM people WHERE id = ? FOR SHARE`, actor.PersonID},
		{`SELECT id FROM recipient_access_generations WHERE id = ? FOR SHARE`, actor.AccessID},
		{`SELECT id FROM sessions WHERE id = ? FOR SHARE`, actor.SessionID},
	} {
		var id any
		var err error
		if lock.id == uuid.Nil {
			err = tx.NewRaw(lock.query).Scan(ctx, &id)
		} else {
			err = tx.NewRaw(lock.query, lock.id).Scan(ctx, &id)
		}
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
	}
	return ensureActor(ctx, tx, actor)
}
