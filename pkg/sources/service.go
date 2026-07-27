// Package sources owns private Source album discovery, triage, and reconciliation.
package sources

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/uptrace/bun"
)

var (
	ErrNotFound             = errors.New("source album not found")
	ErrInvalidTransition    = errors.New("source album disposition transition is invalid")
	ErrInvalidCursor        = errors.New("source album cursor is invalid")
	ErrStaleVersion         = errors.New("source album version is stale")
	ErrInvalidConfiguration = errors.New("immich configuration is invalid")
	ErrDependency           = errors.New("immich dependency unavailable")
	ErrUnstable             = errors.New("source album scan was unstable")
)

// Connector is the narrow, normalized Immich boundary used by Source workflows.
type Connector interface {
	Check(ctx context.Context) error
	OwnedAlbums(ctx context.Context) ([]immich.AlbumSummary, error)
	Album(ctx context.Context, id uuid.UUID) (immich.AlbumSummary, error)
	AlbumAssetsPage(ctx context.Context, albumID uuid.UUID, page int) (immich.AssetPage, error)
}

// Album is the allowlisted Source album representation exposed only to the Curator.
type Album struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	AssetCount      int        `json:"asset_count"`
	SourceCreatedAt time.Time  `json:"source_created_at"`
	SourceUpdatedAt time.Time  `json:"source_updated_at"`
	StartAt         *time.Time `json:"start_at"`
	EndAt           *time.Time `json:"end_at"`
	Disposition     string     `json:"disposition"`
	Version         int64      `json:"version"`
	FirstSeenAt     time.Time  `json:"first_seen_at"`
	LastSeenAt      time.Time  `json:"last_seen_at"`
	SourceMissing   bool       `json:"source_missing"`
}

// ListResponse is generated to TypeScript by Tygo.
type ListResponse struct {
	Albums     []Album `json:"albums"`
	NextCursor *string `json:"next_cursor"`
}

// DiscoveryResponse is generated to TypeScript by Tygo.
type DiscoveryResponse struct {
	Status          string `json:"status"`
	DiscoveredCount int    `json:"discovered_count"`
}

// Service validates Immich and persists private editable Source state.
type Service struct {
	db                     *bun.DB
	connector              Connector
	now                    func() time.Time
	reconciliationInterval time.Duration
}

func New(db *bun.DB, connector Connector, reconciliationInterval time.Duration) *Service {
	return &Service{
		db: db, connector: connector, now: time.Now,
		reconciliationInterval: reconciliationInterval,
	}
}

// Discover validates the connection, then serializes snapshot retrieval and
// persistence so an older dependency snapshot cannot commit after a newer one.
// It writes nothing unless both dependency calls complete successfully.
func (s *Service) Discover(ctx context.Context) (DiscoveryResponse, error) {
	if err := s.connector.Check(ctx); err != nil {
		if immich.IsConfigurationError(err) {
			return DiscoveryResponse{}, ErrInvalidConfiguration
		}
		return DiscoveryResponse{}, ErrDependency
	}
	var albums []immich.AlbumSummary
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('memento:sources:discovery', 0))`); err != nil {
			return fmt.Errorf("lock Source discovery: %w", err)
		}
		var err error
		albums, err = s.connector.OwnedAlbums(ctx)
		if err != nil {
			if immich.IsConfigurationError(err) {
				return ErrInvalidConfiguration
			}
			return ErrDependency
		}
		now := s.now().UTC()
		if _, err := tx.NewRaw(`
			UPDATE source_albums
			SET source_missing = true, missing_since = COALESCE(missing_since, ?),
			    version = version + 1, updated_at = ?
			WHERE source_missing = false
		`, now, now).Exec(ctx); err != nil {
			return fmt.Errorf("mark absent Source albums: %w", err)
		}
		for _, album := range albums {
			publicID, err := uuid.NewRandom()
			if err != nil {
				return fmt.Errorf("generate Source album identity: %w", err)
			}
			fingerprint, err := albumFingerprint(album)
			if err != nil {
				return err
			}
			if _, err := tx.NewRaw(`
				INSERT INTO source_albums (
					id, immich_album_id, name, description, asset_count,
					source_created_at, source_updated_at, source_start_at, source_end_at,
					source_last_modified_asset_at, first_seen_at, last_seen_at,
					source_fingerprint, next_reconciliation_at, updated_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (immich_album_id) DO UPDATE SET
					name = EXCLUDED.name,
					description = EXCLUDED.description,
					asset_count = EXCLUDED.asset_count,
					source_created_at = EXCLUDED.source_created_at,
					source_updated_at = EXCLUDED.source_updated_at,
					source_start_at = EXCLUDED.source_start_at,
					source_end_at = EXCLUDED.source_end_at,
					source_last_modified_asset_at = EXCLUDED.source_last_modified_asset_at,
					last_seen_at = EXCLUDED.last_seen_at,
					source_missing = false,
					missing_since = NULL,
					source_fingerprint = EXCLUDED.source_fingerprint,
					next_reconciliation_at = EXCLUDED.next_reconciliation_at,
					version = source_albums.version + 1,
					updated_at = EXCLUDED.updated_at
			`, publicID, album.SourceID, album.Name, album.Description, album.AssetCount,
				album.CreatedAt, album.UpdatedAt, album.StartDate, album.EndDate,
				album.LastModifiedAssetTimestamp, now, now, fingerprint[:], now, now).Exec(ctx); err != nil {
				return fmt.Errorf("upsert Source album: %w", err)
			}
			var sourceAlbumID uuid.UUID
			if err := tx.NewRaw(`SELECT id FROM source_albums WHERE immich_album_id = ?`, album.SourceID).Scan(ctx, &sourceAlbumID); err != nil {
				return fmt.Errorf("read Source album identity: %w", err)
			}
			if err := enqueueReconciliation(ctx, tx, sourceAlbumID, now); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, ErrInvalidConfiguration) || errors.Is(err, ErrDependency) {
		return DiscoveryResponse{}, err
	}
	if err != nil {
		return DiscoveryResponse{}, fmt.Errorf("persist Source album discovery: %w", err)
	}
	return DiscoveryResponse{Status: "connected", DiscoveredCount: len(albums)}, nil
}

// List returns a private, cursor-paginated disposition view without source identifiers.
func (s *Service) List(ctx context.Context, disposition, encodedCursor string, limit int) (ListResponse, error) {
	if disposition != "unreviewed" && disposition != "ignored" {
		return ListResponse{}, ErrInvalidTransition
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	cursor, err := decodeListCursor(encodedCursor)
	if err != nil {
		return ListResponse{}, ErrInvalidCursor
	}
	query := `
		SELECT id, name, description, asset_count, source_created_at, source_updated_at,
		       source_start_at, source_end_at, disposition, version, first_seen_at, last_seen_at, source_missing
		FROM source_albums
		WHERE disposition = ?`
	args := []any{disposition}
	if cursor != nil {
		query += ` AND (first_seen_at < ? OR (first_seen_at = ? AND id > ?))`
		args = append(args, cursor.FirstSeenAt, cursor.FirstSeenAt, cursor.ID)
	}
	query += ` ORDER BY first_seen_at DESC, id LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return ListResponse{}, err
	}
	defer rows.Close()
	albums := make([]Album, 0, limit)
	for rows.Next() {
		album, err := scanAlbum(rows)
		if err != nil {
			return ListResponse{}, err
		}
		albums = append(albums, album)
	}
	if err := rows.Err(); err != nil {
		return ListResponse{}, err
	}
	var nextCursor *string
	if len(albums) > limit {
		albums = albums[:limit]
		next, err := encodeListCursor(albums[len(albums)-1])
		if err != nil {
			return ListResponse{}, err
		}
		nextCursor = &next
	}
	return ListResponse{Albums: albums, NextCursor: nextCursor}, nil
}

// Get returns one normalized Source album by its Memento identity.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Album, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, description, asset_count, source_created_at, source_updated_at,
		       source_start_at, source_end_at, disposition, version, first_seen_at, last_seen_at, source_missing
		FROM source_albums WHERE id = ?
	`, id)
	album, err := scanAlbum(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Album{}, ErrNotFound
	}
	return album, err
}

// Ignore preserves source identity and last-seen state while removing the album from the inbox.
func (s *Service) Ignore(ctx context.Context, id uuid.UUID, version int64) (Album, error) {
	return s.transition(ctx, id, version, "unreviewed", "ignored")
}

// Restore returns an ignored Source album to the unreviewed inbox.
func (s *Service) Restore(ctx context.Context, id uuid.UUID, version int64) (Album, error) {
	return s.transition(ctx, id, version, "ignored", "unreviewed")
}

func (s *Service) transition(ctx context.Context, id uuid.UUID, version int64, from, to string) (Album, error) {
	now := s.now().UTC()
	var ignoredAt any
	if to == "ignored" {
		ignoredAt = now
	}
	result, err := s.db.NewRaw(`
		UPDATE source_albums
		SET disposition = ?, ignored_at = ?, version = version + 1, updated_at = ?
		WHERE id = ? AND disposition = ? AND version = ?
	`, to, ignoredAt, now, id, from, version).Exec(ctx)
	if err != nil {
		return Album{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Album{}, err
	}
	if affected == 0 {
		var currentDisposition string
		var currentVersion int64
		err := s.db.NewRaw(`SELECT disposition, version FROM source_albums WHERE id = ?`, id).Scan(ctx, &currentDisposition, &currentVersion)
		if errors.Is(err, sql.ErrNoRows) {
			return Album{}, ErrNotFound
		}
		if err != nil {
			return Album{}, err
		}
		if currentDisposition != from {
			return Album{}, ErrInvalidTransition
		}
		return Album{}, ErrStaleVersion
	}
	return s.Get(ctx, id)
}

type listCursor struct {
	FirstSeenAt time.Time `json:"first_seen_at"`
	ID          uuid.UUID `json:"id"`
}

func encodeListCursor(album Album) (string, error) {
	id, err := uuid.Parse(album.ID)
	if err != nil || id == uuid.Nil || album.FirstSeenAt.IsZero() {
		return "", ErrInvalidCursor
	}
	encoded, err := json.Marshal(listCursor{FirstSeenAt: album.FirstSeenAt, ID: id})
	if err != nil {
		return "", fmt.Errorf("encode Source album cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeListCursor(encoded string) (*listCursor, error) {
	if encoded == "" {
		return nil, nil
	}
	contents, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	var cursor listCursor
	if err := json.Unmarshal(contents, &cursor); err != nil || cursor.ID == uuid.Nil || cursor.FirstSeenAt.IsZero() {
		return nil, ErrInvalidCursor
	}
	cursor.FirstSeenAt = cursor.FirstSeenAt.UTC()
	return &cursor, nil
}

func albumFingerprint(album immich.AlbumSummary) ([32]byte, error) {
	encoded, err := json.Marshal(struct {
		Name         string
		Description  string
		AssetCount   int
		CreatedAt    time.Time
		UpdatedAt    time.Time
		StartDate    *time.Time
		EndDate      *time.Time
		LastModified *time.Time
	}{album.Name, album.Description, album.AssetCount, album.CreatedAt, album.UpdatedAt, album.StartDate, album.EndDate, album.LastModifiedAssetTimestamp})
	if err != nil {
		return [32]byte{}, fmt.Errorf("fingerprint Source album summary: %w", err)
	}
	return sha256.Sum256(encoded), nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanAlbum(row scanner) (Album, error) {
	var album Album
	err := row.Scan(
		&album.ID, &album.Name, &album.Description, &album.AssetCount,
		&album.SourceCreatedAt, &album.SourceUpdatedAt, &album.StartAt, &album.EndAt,
		&album.Disposition, &album.Version, &album.FirstSeenAt, &album.LastSeenAt, &album.SourceMissing,
	)
	return album, err
}
