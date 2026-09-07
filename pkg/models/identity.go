package models

import (
	"github.com/google/uuid"
	"github.com/uptrace/bun"
	"time"
)

type Person struct {
	bun.BaseModel `bun:"table:persons"`
	ID            uuid.UUID `bun:"id,pk,type:uuid"`
	DisplayName   string
	IsCurator     bool
	CreatedAt     time.Time
}

type Identity struct {
	bun.BaseModel `bun:"table:identities"`
	ID            uuid.UUID `bun:"id,pk,type:uuid"`
	PersonID      uuid.UUID `bun:"person_id,type:uuid"`
	Provider      string
	Subject       string
	Email         string
	CreatedAt     time.Time
}

type Session struct {
	bun.BaseModel `bun:"table:sessions"`
	TokenHash     []byte    `bun:"token_hash,pk"`
	IdentityID    uuid.UUID `bun:"identity_id,type:uuid"`
	CreatedAt     time.Time
	RenewedAt     time.Time
	ExpiresAt     time.Time
}
