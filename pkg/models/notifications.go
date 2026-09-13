package models

import (
	"time"

	"github.com/uptrace/bun"
)

// MailDelivery is Memento's authoritative delivery state; River rows are never exposed.
type MailDelivery struct {
	bun.BaseModel `bun:"table:mail_deliveries,alias:delivery"`
	ID            UUID `bun:"id,pk,type:uuid"`
	Kind          string
	Recipient     string
	Subject       string
	Body          string
	Status        string
	Attempts      int
	Message       string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DeliveredAt   *time.Time
}

type AnnouncedAlbum struct {
	bun.BaseModel `bun:"table:announced_albums,alias:announced_album"`
	PersonID      UUID `bun:"person_id,pk,type:uuid"`
	AlbumID       UUID `bun:"album_id,pk,type:uuid"`
	AnnouncedAt   time.Time
}

type AnnouncedEntry struct {
	bun.BaseModel `bun:"table:announced_entries,alias:announced_entry"`
	PersonID      UUID `bun:"person_id,pk,type:uuid"`
	EntryID       UUID `bun:"entry_id,pk,type:uuid"`
	AnnouncedAt   time.Time
}
