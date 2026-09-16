package publishing

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
)

const sourcePageSize = 24

// ListSources pages locally because Immich's album-list endpoint has no paging
// or partial-name search. Existing imports remain visible regardless of the
// gate and are never treated as ignored. Ignored selects the albums a Curator
// keeps off the import list instead of the ones offered for import.
func (m *Module) ListSources(ctx context.Context, search string, page int, ignored bool) (SourcePage, error) {
	sources, err := m.source.ListAlbums(ctx)
	if err != nil {
		return SourcePage{}, err
	}
	var imported []models.Album
	if err := m.db.NewSelect().Model(&imported).Column("id", "source_id").Scan(ctx); err != nil {
		return SourcePage{}, errorstack.CaptureContext(ctx, err)
	}
	bySource := map[string]string{}
	for _, album := range imported {
		bySource[album.SourceID] = album.ID.String()
	}
	var ignoredRows []models.IgnoredSource
	if err := m.db.NewSelect().Model(&ignoredRows).Column("source_id").Scan(ctx); err != nil {
		return SourcePage{}, errorstack.CaptureContext(ctx, err)
	}
	isIgnored := map[string]bool{}
	for _, row := range ignoredRows {
		isIgnored[row.SourceID] = bySource[row.SourceID] == ""
	}
	search = strings.ToLower(strings.TrimSpace(search))
	matches := []SourceAlbum{}
	ignoredCount := 0
	for _, source := range sources {
		if isIgnored[source.ID] {
			ignoredCount++
		}
		if isIgnored[source.ID] != ignored || !strings.Contains(strings.ToLower(source.Name), search) {
			continue
		}
		cover := ""
		if source.ThumbnailID != nil && *source.ThumbnailID != "" {
			cover = "/api/media/sources/" + url.PathEscape(source.ID) + "/cover"
		}
		matches = append(matches, SourceAlbum{ID: source.ID, Title: source.Name, Description: source.Description, Count: source.Count,
			StartDate: source.StartDate, EndDate: source.EndDate, CoverURL: cover, AlbumID: bySource[source.ID]})
	}
	// Newest end date first, the order every Album list in Memento shares.
	sort.Slice(matches, func(i, j int) bool {
		a, b := matches[i].EndDate, matches[j].EndDate
		if a == b {
			return matches[i].ID < matches[j].ID
		}
		return a > b
	})
	pages := max(1, (len(matches)+sourcePageSize-1)/sourcePageSize)
	page = min(max(page, 1), pages)
	start := min((page-1)*sourcePageSize, len(matches))
	end := min(start+sourcePageSize, len(matches))
	return SourcePage{Albums: matches[start:end], Page: page, Pages: pages, Total: len(matches), Ignored: ignoredCount}, nil
}

// IgnoreSource keeps an Immich album off the import list until it is restored.
// Ignoring an album twice is the same decision, so it is not an error.
func (m *Module) IgnoreSource(ctx context.Context, sourceID string) error {
	_, err := m.db.NewInsert().Model(&models.IgnoredSource{SourceID: sourceID, CreatedAt: time.Now().UTC()}).
		On("CONFLICT (source_id) DO NOTHING").Exec(ctx)
	return errorstack.CaptureContext(ctx, err)
}

// RestoreSource offers an ignored Immich album for import again.
func (m *Module) RestoreSource(ctx context.Context, sourceID string) error {
	_, err := m.db.NewDelete().Model((*models.IgnoredSource)(nil)).Where("source_id = ?", sourceID).Exec(ctx)
	return errorstack.CaptureContext(ctx, err)
}
