package models

import (
	"time"

	"github.com/uptrace/bun"
)

type Person struct {
	bun.BaseModel `bun:"table:persons"`
	ID            UUID `bun:"id,pk,type:uuid"`
	DisplayName   string
	IsCurator     bool
	CreatedAt     time.Time
}

type Identity struct {
	bun.BaseModel `bun:"table:identities"`
	ID            UUID `bun:"id,pk,type:uuid"`
	PersonID      UUID `bun:"person_id,type:uuid"`
	Provider      string
	Subject       string
	Email         string
	CreatedAt     time.Time
}

type Session struct {
	bun.BaseModel `bun:"table:sessions"`
	TokenHash     []byte `bun:"token_hash,pk"`
	IdentityID    UUID   `bun:"identity_id,type:uuid"`
	CreatedAt     time.Time
	RenewedAt     time.Time
	ExpiresAt     time.Time
}
