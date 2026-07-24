// Package sources owns the private Source album inventory and triage lifecycle.
package sources

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/uptrace/bun"
)

var (
	ErrNotFound          = errors.New("source album not found")
	ErrInvalidTransition = errors.New("source album disposition transition is invalid")
	ErrDependency        = errors.New("immich dependency unavailable")
)

// Connector is the narrow, normalized Immich boundary used by discovery.
type Connector interface {
	Check(ctx context.Context) error
	OwnedAlbums(ctx context.Context) ([]immich.AlbumSummary, error)
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
	FirstSeenAt     time.Time  `json:"first_seen_at"`
	LastSeenAt      time.Time  `json:"last_seen_at"`
	SourceMissing   bool       `json:"source_missing"`
}

// ListResponse is generated to TypeScript by Tygo.
type ListResponse struct {
	Albums   []Album `json:"albums"`
	NextPage *int    `json:"next_page"`
}

// DiscoveryResponse is generated to TypeScript by Tygo.
type DiscoveryResponse struct {
	Status          string `json:"status"`
	DiscoveredCount int    `json:"discovered_count"`
}

// Service validates Immich and persists a private source-only inventory.
type Service struct {
	db        *bun.DB
	connector Connector
	now       func() time.Time
}

func New(db *bun.DB, connector Connector) *Service {
	return &Service{db: db, connector: connector, now: time.Now}
}

// Discover validates the pinned connection before fetching owned albums. It
// writes nothing unless both dependency calls complete successfully.
func (s *Service) Discover(ctx context.Context) (DiscoveryResponse, error) {
	if err := s.connector.Check(ctx); err != nil {
		return DiscoveryResponse{}, ErrDependency
	}
	albums, err := s.connector.OwnedAlbums(ctx)
	if err != nil {
		return DiscoveryResponse{}, ErrDependency
	}
	now := s.now().UTC()
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('memento:sources:discovery', 0))`); err != nil {
			return fmt.Errorf("lock Source discovery: %w", err)
		}
		if _, err := tx.NewRaw(`
			UPDATE source_albums
			SET source_missing = true, missing_since = COALESCE(missing_since, ?), updated_at = ?
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
					updated_at = EXCLUDED.updated_at
			`, publicID, album.SourceID, album.Name, album.Description, album.AssetCount,
				album.CreatedAt, album.UpdatedAt, album.StartDate, album.EndDate,
				album.LastModifiedAssetTimestamp, now, now, fingerprint[:], now, now).Exec(ctx); err != nil {
				return fmt.Errorf("upsert Source album: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return DiscoveryResponse{}, fmt.Errorf("persist Source album discovery: %w", err)
	}
	return DiscoveryResponse{Status: "connected", DiscoveredCount: len(albums)}, nil
}

// List returns a private, paginated disposition view without source identifiers.
func (s *Service) List(ctx context.Context, disposition string, page, limit int) (ListResponse, error) {
	if disposition != "unreviewed" && disposition != "ignored" {
		return ListResponse{}, ErrInvalidTransition
	}
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, description, asset_count, source_created_at, source_updated_at,
		       source_start_at, source_end_at, disposition, first_seen_at, last_seen_at, source_missing
		FROM source_albums
		WHERE disposition = ?
		ORDER BY first_seen_at DESC, id
		LIMIT ? OFFSET ?
	`, disposition, limit+1, (page-1)*limit)
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
	var nextPage *int
	if len(albums) > limit {
		albums = albums[:limit]
		next := page + 1
		nextPage = &next
	}
	return ListResponse{Albums: albums, NextPage: nextPage}, nil
}

// Get returns one normalized Source album by its Memento identity.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Album, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, description, asset_count, source_created_at, source_updated_at,
		       source_start_at, source_end_at, disposition, first_seen_at, last_seen_at, source_missing
		FROM source_albums WHERE id = ?
	`, id)
	album, err := scanAlbum(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Album{}, ErrNotFound
	}
	return album, err
}

// Ignore preserves source identity and last-seen state while removing the album from the inbox.
func (s *Service) Ignore(ctx context.Context, id uuid.UUID) (Album, error) {
	return s.transition(ctx, id, "unreviewed", "ignored")
}

// Restore returns an ignored Source album to the unreviewed inbox.
func (s *Service) Restore(ctx context.Context, id uuid.UUID) (Album, error) {
	return s.transition(ctx, id, "ignored", "unreviewed")
}

func (s *Service) transition(ctx context.Context, id uuid.UUID, from, to string) (Album, error) {
	now := s.now().UTC()
	var ignoredAt any
	if to == "ignored" {
		ignoredAt = now
	}
	result, err := s.db.NewRaw(`
		UPDATE source_albums SET disposition = ?, ignored_at = ?, updated_at = ?
		WHERE id = ? AND disposition = ?
	`, to, ignoredAt, now, id, from).Exec(ctx)
	if err != nil {
		return Album{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Album{}, err
	}
	if affected == 0 {
		var exists bool
		if err := s.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM source_albums WHERE id = ?)`, id).Scan(ctx, &exists); err != nil {
			return Album{}, err
		}
		if !exists {
			return Album{}, ErrNotFound
		}
		return Album{}, ErrInvalidTransition
	}
	return s.Get(ctx, id)
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
		&album.Disposition, &album.FirstSeenAt, &album.LastSeenAt, &album.SourceMissing,
	)
	return album, err
}
