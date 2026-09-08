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
	UpdateIdentityID      *UUID  `bun:"update_identity_id,type:uuid"`
	UpdateEmail           string `bun:",scanonly"`
	EmailUpdates          bool
	CreatedAt             time.Time
}

type Identity struct {
	bun.BaseModel `bun:"table:identities"`
	ID            UUID `bun:"id,pk,type:uuid"`
	PersonID      UUID `bun:"person_id,type:uuid"`
	Provider      string
	Subject       string
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

type Session struct {
	bun.BaseModel `bun:"table:sessions"`
	ID            UUID `bun:"id,type:uuid"`
	Device        string
	TokenHash     []byte `bun:"token_hash,pk"`
	IdentityID    UUID   `bun:"identity_id,type:uuid"`
	CreatedAt     time.Time
	RenewedAt     time.Time
	ExpiresAt     time.Time
}
