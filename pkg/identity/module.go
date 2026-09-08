package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

const SessionLifetime = 180 * 24 * time.Hour

var (
	ErrAccessDenied       = &errcodes.Error{HTTPCode: 403, Code: "access_denied", Message: "This identity does not have access to this installation."}
	ErrUnauthenticated    = &errcodes.Error{HTTPCode: 401, Code: "unauthenticated", Message: "Sign in to continue."}
	ErrUnverifiedIdentity = &errcodes.Error{HTTPCode: 403, Code: "unverified_identity", Message: "Sign-in requires a verified email address."}
)

// Claims must come from a trusted provider adapter, never an unchecked browser body.
type Claims struct {
	Provider      string
	Subject       string
	Email         string
	EmailVerified bool
	DisplayName   string
}

// Session carries the opaque token only when issuing a new session.
type Session struct {
	Person    Person
	Token     string
	ExpiresAt time.Time
	Renewed   bool
}

// Module owns Identity transactions. The clock is replaceable for expiry tests.
type Module struct {
	db  *bun.DB
	now func() time.Time
}

func New(db *bun.DB, now func() time.Time) *Module {
	if now == nil {
		now = time.Now
	}
	return &Module{db: db, now: now}
}

func (m *Module) Claimed(ctx context.Context) (bool, error) {
	var claimed bool
	if err := m.db.NewRaw("SELECT claimed_by IS NOT NULL FROM installation WHERE singleton = true").Scan(ctx, &claimed); err != nil {
		return false, errorstack.CaptureContext(ctx, err)
	}
	return claimed, nil
}

// SignIn claims an empty installation, authenticates a known subject, or consumes
// an exact verified-email preauthorization. Display names never grant access.
func (m *Module) SignIn(ctx context.Context, claims Claims) (Session, error) {
	if err := validateClaims(claims); err != nil {
		return Session{}, err
	}
	now := m.now().UTC()
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return Session{}, errorstack.Capture(err)
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	hash := sha256.Sum256([]byte(token))
	var result Session
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		var claimedBy sql.NullString
		if err := tx.NewRaw("SELECT claimed_by FROM installation WHERE singleton = true").Scan(ctx, &claimedBy); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		var linked models.Identity
		err := tx.NewSelect().Model(&linked).Where("provider = ? AND subject = ?", claims.Provider, claims.Subject).Scan(ctx)
		var person models.Person
		if errors.Is(err, sql.ErrNoRows) {
			if claimedBy.Valid {
				person, err = m.resolvePreauthorization(ctx, tx, claims)
				if err != nil {
					return err
				}
			} else {
				person = models.Person{ID: models.NewUUIDv7(), DisplayName: strings.TrimSpace(claims.DisplayName), IsCurator: true, CreatedAt: now}
				if _, err := tx.NewInsert().Model(&person).Exec(ctx); err != nil {
					return errorstack.CaptureContext(ctx, err)
				}
			}
			previousIdentity, err := tx.NewSelect().Model((*models.Identity)(nil)).Where("person_id = ?", person.ID).Exists(ctx)
			if err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
			linked = models.Identity{ID: models.NewUUIDv7(), PersonID: person.ID, Provider: claims.Provider, Subject: claims.Subject, Email: claims.Email, CreatedAt: now}
			if _, err := tx.NewInsert().Model(&linked).Exec(ctx); err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
			if !previousIdentity {
				person.UpdateIdentityID = &linked.ID
				person.UpdateEmail = linked.Email
				person.EmailUpdates = true
				if _, err := tx.NewUpdate().Model(&person).Column("update_identity_id", "email_updates").WherePK().Exec(ctx); err != nil {
					return errorstack.CaptureContext(ctx, err)
				}
			}
			if !claimedBy.Valid {
				updated, err := tx.ExecContext(ctx, "UPDATE installation SET claimed_by = ?, claimed_at = ? WHERE singleton = true AND claimed_by IS NULL", person.ID, now)
				if err != nil {
					return errorstack.CaptureContext(ctx, err)
				}
				count, err := updated.RowsAffected()
				if err != nil {
					return errorstack.Capture(err)
				}
				if count != 1 {
					return ErrAccessDenied
				}
			}
		} else if err != nil {
			return errorstack.CaptureContext(ctx, err)
		} else {
			person, err = personByID(ctx, tx, linked.PersonID.String())
			if err != nil {
				return err
			}
		}
		if person.DeactivatedAt != nil {
			return ErrAccessDenied
		}
		if linked.UnlinkedAt != nil {
			approved, err := m.resolvePreauthorization(ctx, tx, claims)
			if err != nil {
				return err
			}
			if approved.ID != person.ID {
				return ErrAccessDenied
			}
		}
		if _, err := tx.NewUpdate().Model(&linked).Set("email = ?, unlinked_at = NULL", claims.Email).WherePK().Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if linked.Email != claims.Email && person.UpdateIdentityID != nil && *person.UpdateIdentityID == linked.ID {
			person.UpdateEmail = ""
			person.UpdateIdentityID = nil
			person.EmailUpdates = false
			if _, err := tx.NewUpdate().Model(&person).Column("update_identity_id", "email_updates").WherePK().Exec(ctx); err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
		}
		session := models.Session{ID: models.NewUUIDv7(), Device: browserDevice(ctx), TokenHash: hash[:], IdentityID: linked.ID, CreatedAt: now, RenewedAt: now, ExpiresAt: now.Add(SessionLifetime)}
		if _, err := tx.NewInsert().Model(&session).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		result = Session{Person: projectPerson(person), Token: token, ExpiresAt: session.ExpiresAt}
		return nil
	})
	return result, err
}

func projectPerson(person models.Person) Person {
	return Person{ID: person.ID.String(), DisplayName: person.DisplayName, IsCurator: person.IsCurator,
		OnboardingCompletedAt: person.OnboardingCompletedAt, DeactivatedAt: person.DeactivatedAt,
		UpdateEmail: person.UpdateEmail, EmailUpdates: person.EmailUpdates}
}

// Authenticate rejects expired sessions and extends active sessions at most daily.
func (m *Module) Authenticate(ctx context.Context, token string) (Session, error) {
	if len(token) != 43 {
		return Session{}, ErrUnauthenticated
	}
	var result Session
	valid := false
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		now := m.now().UTC()
		if _, err := tx.NewDelete().Model((*models.Session)(nil)).Where("expires_at <= ?", now).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		person, err := m.sessionPerson(ctx, tx, token)
		if errors.Is(err, ErrUnauthenticated) {
			return nil
		}
		if err != nil {
			return err
		}
		hash := sha256.Sum256([]byte(token))
		var session models.Session
		if err := tx.NewSelect().Model(&session).Where("token_hash = ?", hash[:]).Scan(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		renewed := !session.RenewedAt.After(now.Add(-24 * time.Hour))
		if renewed {
			session.RenewedAt = now
			session.ExpiresAt = now.Add(SessionLifetime)
			if _, err := tx.NewUpdate().Model(&session).Column("renewed_at", "expires_at").WherePK().Exec(ctx); err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
		}
		result = Session{Person: projectPerson(person), ExpiresAt: session.ExpiresAt, Renewed: renewed}
		valid = true
		return nil
	})
	if err == nil && !valid {
		return Session{}, ErrUnauthenticated
	}
	return result, err
}

// SignOut removes only the current browser's session and is safe to repeat.
func (m *Module) SignOut(ctx context.Context, token string) error {
	return m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		hash := sha256.Sum256([]byte(token))
		_, err := tx.NewDelete().Model((*models.Session)(nil)).Where("token_hash = ?", hash[:]).Exec(ctx)
		return errorstack.CaptureContext(ctx, err)
	})
}
