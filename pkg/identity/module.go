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

// SignIn claims an empty installation or authenticates a known provider subject.
// Locking the singleton serializes only sign-in, including concurrent claims.
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
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var claimedBy sql.NullString
		if err := tx.NewRaw("SELECT claimed_by FROM installation WHERE singleton = true FOR UPDATE").Scan(ctx, &claimedBy); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		var linked models.Identity
		err := tx.NewSelect().Model(&linked).Where("provider = ? AND subject = ?", claims.Provider, claims.Subject).Scan(ctx)
		var person models.Person
		if errors.Is(err, sql.ErrNoRows) {
			if claimedBy.Valid {
				return ErrAccessDenied
			}
			personID := models.NewUUIDv7()
			identityID := models.NewUUIDv7()
			person = models.Person{ID: personID, DisplayName: strings.TrimSpace(claims.DisplayName), IsCurator: true, CreatedAt: now}
			linked = models.Identity{ID: identityID, PersonID: personID, Provider: claims.Provider, Subject: claims.Subject, Email: claims.Email, CreatedAt: now}
			if _, err := tx.NewInsert().Model(&person).Exec(ctx); err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
			if _, err := tx.NewInsert().Model(&linked).Exec(ctx); err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
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
		} else if err != nil {
			return errorstack.CaptureContext(ctx, err)
		} else if err := tx.NewSelect().Model(&person).Where("id = ?", linked.PersonID).Scan(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		session := models.Session{TokenHash: hash[:], IdentityID: linked.ID, CreatedAt: now, RenewedAt: now, ExpiresAt: now.Add(SessionLifetime)}
		if _, err := tx.NewInsert().Model(&session).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		result = Session{Person: projectPerson(person), Token: token, ExpiresAt: session.ExpiresAt}
		return nil
	})
	if err != nil && !errors.Is(err, ErrAccessDenied) {
		err = errorstack.CaptureContext(ctx, err)
	}
	return result, err
}

func projectPerson(person models.Person) Person {
	return Person{ID: person.ID.String(), DisplayName: person.DisplayName, IsCurator: person.IsCurator}
}

// Authenticate rejects expired sessions and extends active sessions at most daily.
func (m *Module) Authenticate(ctx context.Context, token string) (Session, error) {
	if len(token) != 43 {
		return Session{}, ErrUnauthenticated
	}
	now := m.now().UTC()
	hash := sha256.Sum256([]byte(token))
	if _, err := m.db.NewDelete().Model((*models.Session)(nil)).Where("expires_at <= ?", now).Exec(ctx); err != nil {
		return Session{}, errorstack.CaptureContext(ctx, err)
	}
	var row struct {
		ID          models.UUID
		DisplayName string
		IsCurator   bool
		ExpiresAt   time.Time
	}
	err := m.db.NewRaw(`SELECT p.id, p.display_name, p.is_curator, s.expires_at
 FROM sessions s JOIN identities i ON i.id = s.identity_id JOIN persons p ON p.id = i.person_id
 WHERE s.token_hash = ? AND s.expires_at > ?`, hash[:], now).Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrUnauthenticated
	}
	if err != nil {
		return Session{}, errorstack.CaptureContext(ctx, err)
	}
	// The condition prevents simultaneous requests from repeatedly extending activity.
	var expiry time.Time
	err = m.db.NewRaw(`UPDATE sessions SET renewed_at = ?, expires_at = ?
 WHERE token_hash = ? AND renewed_at <= ? AND expires_at > ? RETURNING expires_at`, now, now.Add(SessionLifetime), hash[:], now.Add(-24*time.Hour), now).Scan(ctx, &expiry)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Session{}, errorstack.CaptureContext(ctx, err)
	}
	if err == nil {
		row.ExpiresAt = expiry
	}
	return Session{Person: Person{ID: row.ID.String(), DisplayName: row.DisplayName, IsCurator: row.IsCurator}, ExpiresAt: row.ExpiresAt, Renewed: err == nil}, nil
}

// SignOut removes only the current browser's session and is safe to repeat.
func (m *Module) SignOut(ctx context.Context, token string) error {
	hash := sha256.Sum256([]byte(token))
	_, err := m.db.NewDelete().Model((*models.Session)(nil)).Where("token_hash = ?", hash[:]).Exec(ctx)
	return errorstack.CaptureContext(ctx, err)
}
