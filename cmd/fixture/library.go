package main

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // The Immich checksum contract requires SHA-1, not a security hash.
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"time"
)

type sourceAlbum struct {
	ID              string   `json:"id"`
	Name            string   `json:"albumName"`
	Description     string   `json:"description"`
	ThumbnailID     string   `json:"albumThumbnailAssetId"`
	Count           int      `json:"assetCount"`
	StartDate       string   `json:"startDate"`
	EndDate         string   `json:"endDate"`
	UpdatedAt       string   `json:"updatedAt"`
	CreatedAt       string   `json:"createdAt"`
	Users           []string `json:"albumUsers"`
	Shared          bool     `json:"shared"`
	HasSharedLink   bool     `json:"hasSharedLink"`
	ActivityEnabled bool     `json:"isActivityEnabled"`
	Members         []string `json:"-"`
}

type sourceAsset struct {
	ID             string         `json:"id"`
	Checksum       string         `json:"checksum"`
	Filename       string         `json:"originalFileName"`
	OriginalPath   string         `json:"originalPath"`
	OwnerID        string         `json:"ownerId"`
	Kind           string         `json:"type"`
	LocalDateTime  string         `json:"localDateTime"`
	FileCreatedAt  string         `json:"fileCreatedAt"`
	FileModifiedAt string         `json:"fileModifiedAt"`
	CreatedAt      string         `json:"createdAt"`
	UpdatedAt      string         `json:"updatedAt"`
	HasMetadata    bool           `json:"hasMetadata"`
	Offline        bool           `json:"isOffline"`
	Trashed        bool           `json:"isTrashed"`
	Archived       bool           `json:"isArchived"`
	Edited         bool           `json:"isEdited"`
	Favorite       bool           `json:"isFavorite"`
	Visibility     string         `json:"visibility"`
	Width          int            `json:"width"`
	Height         int            `json:"height"`
	Duration       *int           `json:"duration"`
	Thumbhash      *string        `json:"thumbhash"`
	EXIF           map[string]any `json:"exifInfo"`
	Thumbnail      []byte         `json:"-"`
	ContentType    string         `json:"-"`
}

// fixtureLibrary spans local midnight, timestamp ties, three days, and shared media.
// Every thumbnail is an encoded image, including the generated video previews.
func fixtureLibrary() ([]sourceAlbum, map[string]sourceAsset) {
	assets := make(map[string]sourceAsset)
	captures := []string{
		"2026-06-01T23:59:59-07:00",
		"2026-06-02T00:00:00-07:00",
		"2026-06-02T00:00:00-07:00",
		"2026-06-02T14:00:00-07:00",
		"2026-06-03T10:00:00-07:00",
		"2026-06-03T11:00:00-07:00",
		"2026-06-04T12:00:00-07:00",
	}
	for index, capture := range captures {
		n := index + 1
		id := fmt.Sprintf("fixture-asset-%02d", n)
		local, _ := time.Parse(time.RFC3339, capture)
		instant := local.UTC().Format(time.RFC3339)
		thumbnail, contentType := generatedThumbnail(n)
		checksum := sha1.Sum(thumbnail) //nolint:gosec // Match Immich's source checksum format.
		asset := sourceAsset{ID: id, Checksum: base64.StdEncoding.EncodeToString(checksum[:]),
			Filename: fmt.Sprintf("coast-%02d.jpg", n), OriginalPath: "/fixture/not-downloadable/" + id,
			OwnerID: "fixture-owner", Kind: "IMAGE", LocalDateTime: capture, FileCreatedAt: instant,
			FileModifiedAt: instant, CreatedAt: instant, UpdatedAt: "2026-06-05T12:00:00Z",
			HasMetadata: true, Visibility: "timeline", Width: 320, Height: 240,
			EXIF:      map[string]any{"timeZone": "America/Los_Angeles", "orientation": 1},
			Thumbnail: thumbnail, ContentType: contentType}
		if n == 4 || n == 6 {
			duration := 12500
			asset.Kind, asset.Filename, asset.Duration = "VIDEO", fmt.Sprintf("coast-%02d.mp4", n), &duration
		}
		assets[id] = asset
	}
	album := func(id, name string, members ...string) sourceAlbum {
		return sourceAlbum{ID: id, Name: name, Description: "A disposable family album. Source titles and media stay in Immich.",
			ThumbnailID: members[0], Count: len(members), StartDate: assets[members[0]].FileCreatedAt,
			EndDate: assets[members[len(members)-1]].FileCreatedAt, UpdatedAt: "2026-06-05T12:00:00Z",
			CreatedAt: "2026-06-05T12:00:00Z", Users: []string{}, ActivityEnabled: true, Members: members}
	}
	albums := []sourceAlbum{
		album("fixture-album-coast", "Fixture Album - Coast", "fixture-asset-01", "fixture-asset-02", "fixture-asset-03", "fixture-asset-04", "fixture-asset-05", "fixture-asset-06"),
		album("fixture-album-family", "Fixture Album - Family", "fixture-asset-02", "fixture-asset-04", "fixture-asset-07"),
	}
	albums[0].ThumbnailID = "fixture-asset-03"
	for n := 1; n <= 30; n++ {
		albums = append(albums, album(fmt.Sprintf("fixture-album-practice-%02d", n), fmt.Sprintf("Practice Album %02d", n), "fixture-asset-07"))
	}
	return albums, assets
}

func generatedThumbnail(seed int) ([]byte, string) {
	canvas := image.NewRGBA(image.Rect(0, 0, 320, 240))
	tint := uint8(seed & 7)
	for y := range 240 {
		for x := range 320 {
			shade := color.RGBA{70 + tint*12, uint8(135 + y/4), uint8(190 + x/8), 255}
			if y > 145+x/8 {
				shade = color.RGBA{30 + tint*10, uint8(95 + x/8), uint8(120 + y/4), 255}
			}
			if (x-250)*(x-250)+(y-55)*(y-55) < 625 {
				shade = color.RGBA{250, 215, 110 + tint*8, 255}
			}
			canvas.SetRGBA(x, y, shade)
		}
	}
	var output bytes.Buffer
	if seed%2 == 0 {
		_ = jpeg.Encode(&output, canvas, &jpeg.Options{Quality: 85})
		return output.Bytes(), "image/jpeg"
	}
	_ = png.Encode(&output, canvas)
	return output.Bytes(), "image/png"
}
