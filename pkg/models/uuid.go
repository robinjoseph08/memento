package models

import (
	"database/sql/driver"
	"fmt"
	"uuid"
)

// UUID adds database support to the standard library UUID type.
type UUID uuid.UUID

func NewUUIDv7() UUID {
	return UUID(uuid.NewV7())
}

func (u UUID) String() string {
	return uuid.UUID(u).String()
}

func (u UUID) MarshalText() ([]byte, error) {
	return uuid.UUID(u).MarshalText()
}

func (u *UUID) UnmarshalText(text []byte) error {
	parsed, err := uuid.Parse(string(text))
	if err != nil {
		return err
	}
	*u = UUID(parsed)
	return nil
}

func (u *UUID) Scan(src any) error {
	switch src := src.(type) {
	case nil:
		*u = UUID{}
		return nil
	case string:
		return u.UnmarshalText([]byte(src))
	case []byte:
		if len(src) == len(uuid.UUID{}) {
			copy(u[:], src)
			return nil
		}
		return u.UnmarshalText(src)
	default:
		return fmt.Errorf("scan UUID from %T", src)
	}
}

func (u UUID) Value() (driver.Value, error) {
	return u.String(), nil
}
