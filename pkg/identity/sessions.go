package identity

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

type browserKey struct{}

// WithBrowser labels newly issued sessions without retaining a full user agent.
func WithBrowser(ctx context.Context, userAgent string) context.Context {
	browser := "Unknown browser"
	switch {
	case strings.Contains(userAgent, "Edg/"):
		browser = "Edge"
	case strings.Contains(userAgent, "Firefox/"), strings.Contains(userAgent, "FxiOS/"):
		browser = "Firefox"
	case strings.Contains(userAgent, "Chrome/"), strings.Contains(userAgent, "CriOS/"):
		browser = "Chrome"
	case strings.Contains(userAgent, "Safari/"):
		browser = "Safari"
	}
	switch {
	case strings.Contains(userAgent, "iPhone"):
		browser += " on iPhone"
	case strings.Contains(userAgent, "iPad"):
		browser += " on iPad"
	case strings.Contains(userAgent, "Android"):
		browser += " on Android"
	case strings.Contains(userAgent, "Windows"):
		browser += " on Windows"
	case strings.Contains(userAgent, "Macintosh"):
		browser += " on Mac"
	case strings.Contains(userAgent, "Linux"):
		browser += " on Linux"
	}
	return context.WithValue(ctx, browserKey{}, browser)
}

func browserDevice(ctx context.Context) string {
	if device, ok := ctx.Value(browserKey{}).(string); ok {
		return device
	}
	return "Unknown browser"
}

func (m *Module) Sessions(ctx context.Context, token string) ([]BrowserSession, error) {
	result := []BrowserSession{}
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		person, err := m.actor(ctx, tx, token, false)
		if err != nil {
			return err
		}
		result, err = m.personSessions(ctx, tx, person.ID, token)
		return err
	})
	return result, err
}

func (m *Module) personSessions(ctx context.Context, tx bun.Tx, personID models.UUID, token string) ([]BrowserSession, error) {
	result := []BrowserSession{}
	hash := sha256.Sum256([]byte(token))
	var rows []struct {
		ID         models.UUID
		IdentityID models.UUID
		Email      string
		Device     string
		CreatedAt  time.Time
		RenewedAt  time.Time
		ExpiresAt  time.Time
		Current    bool
	}
	err := tx.NewSelect().TableExpr("sessions AS s").
		Column("s.id", "s.identity_id", "i.email", "s.device", "s.created_at", "s.renewed_at", "s.expires_at").
		ColumnExpr("s.token_hash = ? AS current", hash[:]).
		Join("JOIN identities AS i ON i.id = s.identity_id").
		Where("i.person_id = ?", personID).Where("i.unlinked_at IS NULL").
		Where("s.expires_at > ?", m.now().UTC()).Order("s.created_at", "s.id").Scan(ctx, &rows)
	if err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	for _, row := range rows {
		result = append(result, BrowserSession{ID: row.ID.String(), IdentityID: row.IdentityID.String(), Email: row.Email, Device: row.Device, CreatedAt: row.CreatedAt, LastUsedAt: row.RenewedAt, ExpiresAt: row.ExpiresAt, Current: row.Current})
	}
	return result, nil
}

func (m *Module) SignOutEverywhere(ctx context.Context, token string) error {
	return m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		person, err := m.actor(ctx, tx, token, false)
		if err != nil {
			return err
		}
		return revokePersonSessions(ctx, tx, person.ID)
	})
}

func revokePersonSessions(ctx context.Context, tx bun.Tx, personID models.UUID) error {
	identities := tx.NewSelect().Table("identities").Column("id").Where("person_id = ?", personID)
	_, err := tx.NewDelete().Model((*models.Session)(nil)).Where("identity_id IN (?)", identities).Exec(ctx)
	return errorstack.CaptureContext(ctx, err)
}

// sessionPerson checks credentials inside the same transaction as the use case.
func (m *Module) sessionPerson(ctx context.Context, tx bun.Tx, token string) (models.Person, error) {
	var person models.Person
	if len(token) != 43 {
		return person, ErrUnauthenticated
	}
	hash := sha256.Sum256([]byte(token))
	err := selectPeople(tx, &person).
		Join("JOIN identities AS i ON i.person_id = person.id").
		Join("JOIN sessions AS s ON s.identity_id = i.id").
		Where("s.token_hash = ?", hash[:]).Where("s.expires_at > ?", m.now().UTC()).
		Where("person.deactivated_at IS NULL").Where("i.unlinked_at IS NULL").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return person, ErrUnauthenticated
	}
	return person, errorstack.CaptureContext(ctx, err)
}
