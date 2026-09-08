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
	err := tx.NewRaw(`SELECT s.id, s.identity_id, i.email, s.device, s.created_at, s.renewed_at, s.expires_at, s.token_hash = ? AS current
 FROM sessions s JOIN identities i ON i.id = s.identity_id
 WHERE i.person_id = ? AND i.unlinked_at IS NULL AND s.expires_at > ? ORDER BY s.created_at, s.id`, hash[:], personID, m.now().UTC()).Scan(ctx, &rows)
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
	_, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE identity_id IN (SELECT id FROM identities WHERE person_id = ?)", personID)
	return errorstack.CaptureContext(ctx, err)
}

// sessionPerson checks credentials inside the same transaction as the use case.
func (m *Module) sessionPerson(ctx context.Context, tx bun.Tx, token string) (models.Person, error) {
	var person models.Person
	if len(token) != 43 {
		return person, ErrUnauthenticated
	}
	hash := sha256.Sum256([]byte(token))
	err := tx.NewRaw(`SELECT p.id, p.display_name, p.is_curator, p.onboarding_completed_at, p.deactivated_at, p.update_identity_id, COALESCE(updates.email, '') AS update_email, p.email_updates, p.created_at FROM sessions s JOIN identities i ON i.id = s.identity_id
 JOIN persons p ON p.id = i.person_id LEFT JOIN identities updates ON updates.id = p.update_identity_id WHERE s.token_hash = ? AND s.expires_at > ? AND p.deactivated_at IS NULL AND i.unlinked_at IS NULL`, hash[:], m.now().UTC()).Scan(ctx, &person)
	if errors.Is(err, sql.ErrNoRows) {
		return person, ErrUnauthenticated
	}
	return person, errorstack.CaptureContext(ctx, err)
}
