package immich

import (
	"context"
	"io"
)

// Album is source metadata, not Memento's reviewed Album.
type Album struct {
	ID          string  `json:"id"`
	Name        string  `json:"albumName"`
	Description string  `json:"description"`
	ThumbnailID *string `json:"albumThumbnailAssetId"`
	Count       int     `json:"assetCount"`
	StartDate   string  `json:"startDate"`
	EndDate     string  `json:"endDate"`
	UpdatedAt   string  `json:"updatedAt"`
}

// Asset preserves Immich facts. LocalDateTime is a wall clock, not an instant.
type Asset struct {
	ID               string         `json:"id"`
	Checksum         string         `json:"checksum"`
	Filename         string         `json:"originalFileName"`
	Kind             string         `json:"type"`
	Visibility       string         `json:"visibility"`
	LocalDateTime    string         `json:"localDateTime"`
	FileCreatedAt    string         `json:"fileCreatedAt"`
	UpdatedAt        string         `json:"updatedAt"`
	Offline          bool           `json:"isOffline"`
	Trashed          bool           `json:"isTrashed"`
	Width            *int           `json:"width"`
	Height           *int           `json:"height"`
	Duration         *int           `json:"duration"`
	Thumbhash        *string        `json:"thumbhash"`
	LivePhotoVideoID *string        `json:"livePhotoVideoId"`
	EXIF             map[string]any `json:"exifInfo"`
	Stack            *AssetStack    `json:"stack"`
}

// AssetStack is a summary, not a request to import assets outside the source album.
type AssetStack struct {
	ID             string `json:"id"`
	PrimaryAssetID string `json:"primaryAssetId"`
	AssetCount     int    `json:"assetCount"`
}

// Library is the read-only source boundary used by imports.
type Library interface {
	CheckImport(context.Context) error
	ListAlbums(context.Context) ([]Album, error)
	GetAlbum(context.Context, string) (Album, error)
	ListMembers(context.Context, string, int) ([]Asset, int, error)
	GetAsset(context.Context, string) (Asset, error)
}

// Thumbnail is an Immich-generated image. The caller closes Body.
type Thumbnail struct {
	Body        io.ReadCloser
	ContentType string
}
