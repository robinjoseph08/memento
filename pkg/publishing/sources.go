package publishing

import (
	"context"
	"net/url"
	"sort"
	"strings"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
)

const sourcePageSize = 24

// ListSources pages locally because Immich's album-list endpoint has no paging
// or partial-name search. Existing imports remain visible regardless of the gate.
func (m *Module) ListSources(ctx context.Context, search string, page int) (SourcePage, error) {
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
	search = strings.ToLower(strings.TrimSpace(search))
	matches := []SourceAlbum{}
	for _, source := range sources {
		if !strings.Contains(strings.ToLower(source.Name), search) {
			continue
		}
		cover := ""
		if source.ThumbnailID != nil && *source.ThumbnailID != "" {
			cover = "/api/media/sources/" + url.PathEscape(source.ID) + "/cover"
		}
		matches = append(matches, SourceAlbum{ID: source.ID, Title: source.Name, Description: source.Description, Count: source.Count,
			StartDate: source.StartDate, EndDate: source.EndDate, CoverURL: cover, AlbumID: bySource[source.ID]})
	}
	sort.Slice(matches, func(i, j int) bool {
		a, b := matches[i].StartDate, matches[j].StartDate
		if a == b {
			return matches[i].ID < matches[j].ID
		}
		return a > b
	})
	pages := max(1, (len(matches)+sourcePageSize-1)/sourcePageSize)
	page = min(max(page, 1), pages)
	start := min((page-1)*sourcePageSize, len(matches))
	end := min(start+sourcePageSize, len(matches))
	return SourcePage{Albums: matches[start:end], Page: page, Pages: pages, Total: len(matches)}, nil
}
