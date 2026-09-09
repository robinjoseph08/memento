package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/robinjoseph08/memento/cmd/immich-smoke/fixture"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/stretchr/testify/require"
)

func TestParseRelease(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "default", want: "v3.1.0"},
		{name: "previous minor", args: []string{"--version", "v3.0.3"}, want: "v3.0.3"},
		{name: "floating", args: []string{"--version", "release"}},
		{name: "missing value", args: []string{"--version"}},
		{name: "empty", args: []string{"--version", ""}},
		{name: "prerelease", args: []string{"--version", "v3.1.0-rc.1"}},
		{name: "build metadata", args: []string{"--version", "v3.1.0+build"}},
		{name: "leading zero", args: []string{"--version", "v3.01.0"}},
		{name: "positional", args: []string{"v3.0.3"}},
		{name: "unknown flag", args: []string{"--release", "v3.0.3"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseRelease(test.args)
			if test.want == "" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, test.want, got)
			}
		})
	}
}

func TestSnapshotProtectsSourceAlbumCover(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, cover string
		failure     bool
	}{
		{name: "unchanged cover", cover: "asset-z"},
		{name: "changed cover", cover: "asset-a", failure: true},
		{name: "removed cover", failure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			album := fixture.Album{ID: "album", Name: "Source name", Description: "Source description", AssetIDs: []string{"asset-a", "asset-z"}, CoverAssetID: "asset-z"}
			sourceAlbum := immich.Album{ID: album.ID, Name: album.Name, Description: album.Description, Count: 2, UpdatedAt: "2024-07-02T07:00:00Z"}
			if test.cover != "" {
				sourceAlbum.ThumbnailID = &test.cover
			}
			members := []immich.Asset{}
			for _, id := range []string{"asset-z", "asset-a"} {
				members = append(members, immich.Asset{ID: id, Filename: id + ".jpg", Checksum: "AAAAAAAAAAAAAAAAAAAAAAAAAAA=", Kind: "IMAGE", Visibility: "timeline", LocalDateTime: "2024-07-02T00:00:00Z", FileCreatedAt: "2024-07-02T07:00:00Z", UpdatedAt: "2024-07-02T07:00:00Z"})
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Api-Key") != "read-key" || r.Header.Get("Authorization") != "" {
					t.Error("snapshots must use only the read key")
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.Method + " " + r.URL.Path {
				case "GET /api/albums":
					_ = json.NewEncoder(w).Encode([]immich.Album{sourceAlbum})
				case "GET /api/albums/album":
					_ = json.NewEncoder(w).Encode(sourceAlbum)
				case "POST /api/search/metadata":
					_ = json.NewEncoder(w).Encode(map[string]any{"assets": map[string]any{"items": members, "count": 2, "nextPage": nil}})
				default:
					t.Errorf("unexpected snapshot request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(server.Close)
			got, err := snapshot(t.Context(), immich.New(server.URL, "read-key"), []fixture.Album{album})
			if test.failure {
				require.ErrorContains(t, err, "cover")
			} else {
				require.NoError(t, err)
				require.Equal(t, []albumSnapshot{{ID: "album", Name: "Source name", Description: "Source description", Members: []string{"asset-a", "asset-z"}, CoverAssetID: "asset-z"}}, got)
			}
		})
	}
}

func TestVerifyAlbum(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name             string
		firstMomentCover bool
		change           func(*publishing.AlbumDetail)
		failure          string
	}{
		{name: "later Moment seeded without changing summary cover"},
		{name: "first Moment seeded including summary cover", firstMomentCover: true},
		{name: "source cover ignored", change: func(a *publishing.AlbumDetail) {
			a.Moments[1].CoverEntryID = a.Moments[1].Entries[0].ID
		}, failure: "configured seed"},
		{name: "other Moment cover changed", change: func(a *publishing.AlbumDetail) {
			a.Moments[0].CoverEntryID = a.Moments[1].CoverEntryID
		}, failure: "configured seed"},
		{name: "summary incorrectly follows later source cover", change: func(a *publishing.AlbumDetail) {
			a.CoverURL = a.Moments[1].Entries[1].ThumbnailURL
		}, failure: "summary"},
		{name: "summary ignores first Moment source cover", firstMomentCover: true, change: func(a *publishing.AlbumDetail) {
			a.CoverURL = a.Moments[0].Entries[0].ThumbnailURL
		}, failure: "summary"},
		{name: "UTC date shift", change: func(a *publishing.AlbumDetail) { a.Moments[0].Date = "2024-07-02" }, failure: "capture-local"},
		{name: "tie order", change: func(a *publishing.AlbumDetail) {
			entries := a.Moments[1].Entries
			entries[0], entries[1] = entries[1], entries[0]
		}, failure: "deterministic entry order"},
		{name: "missing entry", change: func(a *publishing.AlbumDetail) { a.Moments = a.Moments[:2] }, failure: "lost entries"},
		{name: "published import", change: func(a *publishing.AlbumDetail) { a.Published = true }, failure: "metadata or completion"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			album := fixture.Album{ID: "source", Name: "Source name", Description: "Source description", AssetIDs: []string{"first", "tie-z", "tie-a", "last"}, CoverAssetID: "tie-z"}
			assets := []fixture.Asset{}
			for i, photo := range fixture.Photos() {
				assets = append(assets, fixture.Asset{ID: album.AssetIDs[i], Photo: photo})
			}
			detail := publishing.AlbumDetail{Title: album.Name, Description: album.Description, Status: "complete", Processed: 4, Total: 4,
				PhotoCount: 4, StartDate: assets[0].CapturedAt[:10], EndDate: assets[3].CapturedAt[:10], CoverURL: "/api/media/entries/entry-first/thumbnail?v=fixture"}
			for _, indices := range [][]int{{0}, {2, 1}, {3}} {
				moment := publishing.Moment{Date: assets[indices[0]].CapturedAt[:10], CoverEntryID: "entry-" + assets[indices[0]].ID}
				for _, i := range indices {
					asset := assets[i]
					moment.Entries = append(moment.Entries, publishing.Entry{ID: "entry-" + asset.ID, Filename: asset.Filename, CapturedAt: asset.CapturedAt[:19], Kind: "IMAGE", Available: true, ThumbnailURL: "/api/media/entries/entry-" + asset.ID + "/thumbnail?v=fixture"})
				}
				detail.Moments = append(detail.Moments, moment)
			}
			detail.Moments[1].CoverEntryID = "entry-tie-z"
			if test.firstMomentCover {
				album.AssetIDs = []string{"tie-z", "tie-a"}
				detail.Moments = detail.Moments[1:2]
				detail.Processed, detail.Total, detail.PhotoCount = 2, 2, 2
				detail.StartDate, detail.EndDate = "2024-07-02", "2024-07-02"
				detail.CoverURL = "/api/media/entries/entry-tie-z/thumbnail?v=fixture"
			}
			if test.change != nil {
				test.change(&detail)
			}
			err := verifyAlbum(detail, album, assets)
			if test.failure == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, test.failure)
			}
		})
	}
}
