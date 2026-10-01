package models

import (
	"time"

	"github.com/uptrace/bun"
)

type Person struct {
	bun.BaseModel         `bun:"table:persons"`
	ID                    UUID `bun:"id,pk,type:uuid"`
	DisplayName           string
	IsCurator             bool
	OnboardingCompletedAt *time.Time
	DeactivatedAt         *time.Time
	UpdateEmailID         *UUID  `bun:"update_email_id,type:uuid"`
	UpdateEmail           string `bun:",scanonly"`
	EmailUpdates          bool
	AvatarFaceID          *string
	AvatarVersion         string `bun:",scanonly"`
	CreatedAt             time.Time
}

// LinkedEmail is a verified, lowercased address that signs its Person in.
// Unlinking keeps the row so the address stays known.
type LinkedEmail struct {
	bun.BaseModel `bun:"table:linked_emails,alias:linked_email"`
	ID            UUID `bun:"id,pk,type:uuid"`
	PersonID      UUID `bun:"person_id,type:uuid"`
	Email         string
	UnlinkedAt    *time.Time
	CreatedAt     time.Time
}

type Preauthorization struct {
	bun.BaseModel `bun:"table:preauthorizations"`
	ID            UUID `bun:"id,pk,type:uuid"`
	PersonID      UUID `bun:"person_id,type:uuid"`
	Email         string
	CreatedAt     time.Time
	ConsumedAt    *time.Time
	RevokedAt     *time.Time
}

type ImmichFaceLink struct {
	bun.BaseModel `bun:"table:immich_face_links,alias:face_link"`
	SourceID      string `bun:"source_id,pk"`
	PersonID      *UUID  `bun:"type:uuid"`
	Ignored       bool
	UpdatedAt     time.Time
}

type Session struct {
	bun.BaseModel `bun:"table:sessions"`
	ID            UUID `bun:"id,type:uuid"`
	Device        string
	TokenHash     []byte `bun:"token_hash,pk"`
	LinkedEmailID UUID   `bun:"linked_email_id,type:uuid"`
	CreatedAt     time.Time
	RenewedAt     time.Time
	ExpiresAt     time.Time
}

// HandoffCode is exchanged once by the Mobile App for a session of its own.
type HandoffCode struct {
	bun.BaseModel `bun:"table:handoff_codes"`
	CodeHash      []byte `bun:"code_hash,pk"`
	LinkedEmailID UUID   `bun:"linked_email_id,type:uuid"`
	ExpiresAt     time.Time
}

// SignInCode is one emailed Sign-in Code. Only the newest code for an address
// can be verified; older rows stay for an hour so the send limits can count
// them. Known records whether the address would sign in when the code was
// sent. A decoy stands in for a code the unknown-address cap kept from being
// sent, so it was never emailed and nothing can match it.
type SignInCode struct {
	bun.BaseModel `bun:"table:sign_in_codes,alias:code"`
	ID            UUID `bun:"id,pk,type:uuid"`
	Email         string
	CodeHash      []byte
	Known         bool
	Decoy         bool
	Attempts      int
	CreatedAt     time.Time
	ExpiresAt     time.Time
	UsedAt        *time.Time
}

type Invitation struct {
	bun.BaseModel      `bun:"table:invitations,alias:invitation"`
	ID                 UUID `bun:"id,pk,type:uuid"`
	PersonID           UUID `bun:"person_id,type:uuid"`
	PreauthorizationID UUID `bun:"preauthorization_id,type:uuid"`
	DeliveryID         UUID `bun:"delivery_id,type:uuid"`
	SentBy             UUID `bun:"sent_by,type:uuid"`
	CreatedAt          time.Time
}

// AccessRequest records who asked, never whether two addresses share a human.
type AccessRequest struct {
	bun.BaseModel `bun:"table:access_requests,alias:request"`
	ID            UUID `bun:"id,pk,type:uuid"`
	Kind          string
	Email         string
	DisplayName   string
	PersonID      *UUID `bun:"person_id,type:uuid"`
	AlbumID       *UUID `bun:"album_id,type:uuid"`
	Status        string
	SignInCount   int
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ResolvedAt    *time.Time
	ResolvedBy    *UUID `bun:"resolved_by,type:uuid"`
}
