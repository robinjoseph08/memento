package main

import (
	"testing"

	"github.com/robinjoseph08/memento/cmd/immich-smoke/fixture"
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

func TestVerifyAlbum(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		change  func(*publishing.AlbumDetail)
		failure string
	}{
		{name: "valid local dates and source-ID tie order"},
		{name: "UTC date shift", change: func(a *publishing.AlbumDetail) { a.Moments[0].Date = "2024-07-02" }, failure: "capture-local"},
		{name: "tie order", change: func(a *publishing.AlbumDetail) {
			entries := a.Moments[1].Entries
			entries[0], entries[1] = entries[1], entries[0]
			a.Moments[1].CoverEntryID = entries[0].ID
		}, failure: "deterministic entry order"},
		{name: "missing entry", change: func(a *publishing.AlbumDetail) { a.Moments = a.Moments[:2] }, failure: "lost entries"},
		{name: "published import", change: func(a *publishing.AlbumDetail) { a.Published = true }, failure: "metadata or completion"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			album := fixture.Album{ID: "source", Name: "Source name", Description: "Source description", AssetIDs: []string{"first", "tie-z", "tie-a", "last"}}
			assets := []fixture.Asset{}
			for i, photo := range fixture.Photos() {
				assets = append(assets, fixture.Asset{ID: album.AssetIDs[i], Photo: photo})
			}
			detail := publishing.AlbumDetail{Title: album.Name, Description: album.Description, Status: "complete", Processed: 4, Total: 4}
			for _, indices := range [][]int{{0}, {2, 1}, {3}} {
				moment := publishing.Moment{Date: assets[indices[0]].CapturedAt[:10], CoverEntryID: assets[indices[0]].ID}
				for _, i := range indices {
					asset := assets[i]
					moment.Entries = append(moment.Entries, publishing.Entry{ID: asset.ID, Filename: asset.Filename, CapturedAt: asset.CapturedAt[:19], Kind: "IMAGE", Available: true, ThumbnailURL: "/api/media/entries/fixture/thumbnail?v=fixture"})
				}
				detail.Moments = append(detail.Moments, moment)
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
