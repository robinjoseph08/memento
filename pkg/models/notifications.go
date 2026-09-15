package models

import (
	"encoding/json"
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

// AnnouncedEntry is the lifetime association between a Person and an Album
// Entry. NotificationID names the Update Notification that announced it, and is
// nil for content added at Onboarding or dismissed without a notification.
type AnnouncedEntry struct {
	bun.BaseModel  `bun:"table:announced_entries,alias:announced_entry"`
	PersonID       UUID  `bun:"person_id,pk,type:uuid"`
	EntryID        UUID  `bun:"entry_id,pk,type:uuid"`
	NotificationID *UUID `bun:"notification_id,type:uuid"`
	AnnouncedAt    time.Time
}

// UpdateNotification is one approved, immutable in-app summary. Version names
// the payload shape so later readers can still render old rows; ReadAt is the
// only column that changes after creation. DeliveryID names the optional email
// for the same summary and is nil when the Person gets it in app only.
type UpdateNotification struct {
	bun.BaseModel `bun:"table:update_notifications,alias:notification"`
	ID            UUID `bun:"id,pk,type:uuid"`
	PersonID      UUID `bun:"person_id,type:uuid"`
	Version       int
	Payload       json.RawMessage `bun:"payload,type:jsonb"`
	CreatedAt     time.Time
	ReadAt        *time.Time
	DeliveryID    *UUID `bun:"delivery_id,type:uuid"`
}

// UnsubscribeToken is a Person's private link secret for stopping update
// email without signing in. The token itself is stored so every later email
// can carry the same link.
type UnsubscribeToken struct {
	bun.BaseModel `bun:"table:unsubscribe_tokens,alias:unsubscribe"`
	PersonID      UUID `bun:"person_id,pk,type:uuid"`
	Token         string
	CreatedAt     time.Time
}
