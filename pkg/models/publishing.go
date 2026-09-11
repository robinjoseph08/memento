package models

import (
	"time"

	"github.com/uptrace/bun"
)

// Album includes its durable initial import intent and progress.
type Album struct {
	bun.BaseModel   `bun:"table:albums,alias:album"`
	ID              UUID `bun:"id,pk,type:uuid"`
	SourceID        string
	Title           string
	Description     string
	PublishedAt     *time.Time
	ImportStatus    string
	ImportMessage   string
	ImportProcessed int
	ImportTotal     int
	ImportUpdatedAt time.Time
	CreatedAt       time.Time
}

// MediaItem holds shared source facts, never media bytes.
type MediaItem struct {
	bun.BaseModel        `bun:"table:media_items,alias:media_item"`
	ID                   UUID `bun:"id,pk,type:uuid"`
	SourceID             string
	Checksum             string
	Filename             string
	Kind                 string
	CapturedAt           time.Time `bun:"type:timestamp without time zone"`
	SourceCreatedAt      time.Time
	SourceUpdatedAt      time.Time
	Offline              bool
	Trashed              bool
	Width                *int
	Height               *int
	Duration             *int
	Thumbhash            *string
	LivePhotoVideoID     *string
	SourceStackID        *string
	SourceStackPrimaryID *string
	SourceStackCount     *int
	EXIF                 map[string]any `bun:"exif,type:jsonb"`
	ContentVersion       string
}

// AlbumEntry retains identity when membership is removed, without keeping an empty Moment.
type AlbumEntry struct {
	bun.BaseModel `bun:"table:album_entries,alias:album_entry"`
	ID            UUID  `bun:"id,pk,type:uuid"`
	AlbumID       UUID  `bun:"type:uuid"`
	MediaItemID   UUID  `bun:"type:uuid"`
	MomentID      *UUID `bun:"type:uuid"`
	RemovedAt     *time.Time
}

type Moment struct {
	bun.BaseModel `bun:"table:moments,alias:moment"`
	ID            UUID `bun:"id,pk,type:uuid"`
	AlbumID       UUID `bun:"type:uuid"`
	CaptureDate   string
	Title         *string
	SortOrder     int64
	CoverEntryID  UUID `bun:"type:uuid"`
}

type MomentAccessDecision struct {
	bun.BaseModel `bun:"table:moment_access_decisions,alias:decision"`
	MomentID      UUID `bun:"moment_id,pk,type:uuid"`
	AlbumID       UUID `bun:"type:uuid"`
	PersonID      UUID `bun:"person_id,pk,type:uuid"`
	Decision      string
	UpdatedAt     time.Time
}

type MediaFaceAssociation struct {
	bun.BaseModel `bun:"table:media_face_associations,alias:face"`
	MediaItemID   UUID   `bun:"media_item_id,pk,type:uuid"`
	SourceFaceID  string `bun:"source_face_id,pk"`
	SourceName    string
	SourceVersion string
}

type MediaFaceRefresh struct {
	bun.BaseModel `bun:"table:media_face_refreshes,alias:refresh"`
	MediaItemID   UUID `bun:"media_item_id,pk,type:uuid"`
	RefreshedAt   time.Time
}
