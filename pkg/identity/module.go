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
	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/uptrace/bun"
)

const SessionLifetime = 180 * 24 * time.Hour

var (
	ErrAccessDenied    = &errcodes.Error{HTTPCode: 403, Code: "access_denied", Message: "This email does not have access to this installation."}
	ErrUnauthenticated = &errcodes.Error{HTTPCode: 401, Code: "unauthenticated", Message: "Sign in to continue."}
	ErrUnverifiedEmail = &errcodes.Error{HTTPCode: 403, Code: "unverified_email", Message: "Sign-in requires a verified email address."}
	// ErrAccessRequested means the verified address is unknown and a Curator now has one pending request for it.
	ErrAccessRequested = &errcodes.Error{HTTPCode: 403, Code: "access_requested", Message: "This email does not have access yet. Your Curator has been asked to review your request."}
)

// Mail is the consumer-owned view of Notifications for Invitations.
type Mail interface {
	Enqueue(context.Context, bun.Tx, notifications.Message) (notifications.Delivery, error)
	Retry(context.Context, bun.Tx, string) (notifications.Delivery, error)
	Deliveries(context.Context, bun.IDB, []string) (map[string]notifications.Delivery, error)
}

// Announcements is the consumer-owned view of Notifications for Onboarding
// baselines and for turning "Tell me about albums I can join" back on.
type Announcements interface {
	RecordBaseline(context.Context, bun.Tx, string) (notifications.Baseline, error)
	RecordOfferedAlbums(context.Context, bun.Tx, string) error
	Announced(context.Context, bun.IDB, string) (notifications.Baseline, error)
}

// Claims are one verified address and the name the verifier reported. They
// must come from a trusted adapter, never an unchecked browser body, and any
// way of verifying an address (Google, a Sign-in Code) produces the same shape.
type Claims struct {
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
	// ImmichURL is the browser-reachable Immich origin used for "Open in
	// Immich" links on linked faces. Empty hides those links.
	ImmichURL string
	// PublicURL is the origin Invitation emails point at.
	PublicURL string
	// Mail is nil until the server wires Notifications; Invitations then report SMTP as unconfigured.
	Mail Mail
	// Announcements records the Onboarding baseline and Albums offered while
	// a Person was not hearing about them. Both fail without it.
	Announcements Announcements
	// Sender is nil until mail is configured, which turns Sign-in Codes off.
	Sender Sender
}

func New(db *bun.DB, now func() time.Time) *Module {
	if now == nil {
		now = time.Now
	}
	return &Module{db: db, now: microsecondClock(now)}
}

// microsecondClock matches PostgreSQL's timestamptz precision so a value
// returned from memory equals the same value read back from the database.
func microsecondClock(now func() time.Time) func() time.Time {
	return func() time.Time { return now().UTC().Truncate(time.Microsecond) }
}

func (m *Module) Claimed(ctx context.Context) (bool, error) {
	var claimed bool
	if err := m.db.NewSelect().Table("installation").
		ColumnExpr("claimed_by IS NOT NULL").Where("singleton = true").Scan(ctx, &claimed); err != nil {
		return false, errorstack.CaptureContext(ctx, err)
	}
	return claimed, nil
}

// SignIn resolves a verified address with resolveAddress and issues a
// session, or records an Access Request when no step admits the address.
func (m *Module) SignIn(ctx context.Context, claims Claims) (Session, error) {
	claims, err := normalizeClaims(claims)
	if err != nil {
		return Session{}, err
	}
	token, hash, err := newSecret()
	if err != nil {
		return Session{}, err
	}
	var result Session
	requested := false
	err = m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		person, linked, err := m.resolveAddress(ctx, tx, claims)
		if errors.Is(err, errUnknownAddress) {
			// Commit the request while refusing the session.
			requested = true
			return m.recordAccessRequest(ctx, tx, claims)
		}
		if err != nil {
			return err
		}
		result, err = m.startSession(ctx, tx, person, linked, token, hash)
		return err
	})
	if err == nil && requested {
		return Session{}, ErrAccessRequested
	}
	return result, err
}

// errUnknownAddress means no step admitted a verified address, so the caller
// records an Access Request for it.
var errUnknownAddress = errors.New("unknown address")

// resolveAddress finds who a verified address signs in as, in order: an
// unclaimed Installation makes it the first Curator; a Linked Email signs its
// Person in; an unused Preauthorization links it; anything else is
// errUnknownAddress. An unlinked address is refused until a Curator
// preauthorizes it again, and a deactivated Person is refused outright.
// Display names never grant access. The Linked Email lookup runs first
// because an unclaimed Installation has no Person who could hold one.
func (m *Module) resolveAddress(ctx context.Context, tx bun.Tx, claims Claims) (models.Person, models.LinkedEmail, error) {
	now := m.now().UTC()
	var claimedBy sql.NullString
	if err := tx.NewSelect().Table("installation").
		Column("claimed_by").Where("singleton = true").Scan(ctx, &claimedBy); err != nil {
		return models.Person{}, models.LinkedEmail{}, errorstack.CaptureContext(ctx, err)
	}
	var person models.Person
	var linked models.LinkedEmail
	switch err := tx.NewSelect().Model(&linked).Where("email = ? AND unlinked_at IS NULL", claims.Email).Scan(ctx); {
	case err == nil:
		person, err = personByID(ctx, tx, linked.PersonID.String())
		if err != nil {
			return person, linked, err
		}
		if person.DeactivatedAt != nil {
			return person, linked, ErrAccessDenied
		}
		return person, linked, nil
	case !errors.Is(err, sql.ErrNoRows):
		return person, linked, errorstack.CaptureContext(ctx, err)
	case !claimedBy.Valid:
		name := claims.DisplayName
		if name == "" {
			// A Sign-in Code carries no name; Onboarding asks for one.
			name = claims.Email[:strings.LastIndex(claims.Email, "@")]
		}
		person = models.Person{ID: models.NewUUIDv7(), DisplayName: name, IsCurator: true, CreatedAt: now}
		if _, err := tx.NewInsert().Model(&person).Exec(ctx); err != nil {
			return person, linked, errorstack.CaptureContext(ctx, err)
		}
		if err := claimInstallation(ctx, tx, person.ID, now); err != nil {
			return person, linked, err
		}
		linked, err = link(ctx, tx, &person, claims.Email, now)
		return person, linked, err
	}
	person, err := m.resolvePreauthorization(ctx, tx, claims.Email)
	if errors.Is(err, errNoPreauthorization) {
		unlinked, err := tx.NewSelect().Model((*models.LinkedEmail)(nil)).Where("email = ?", claims.Email).Exists(ctx)
		if err != nil {
			return person, linked, errorstack.CaptureContext(ctx, err)
		}
		if unlinked {
			return person, linked, ErrAccessDenied
		}
		return person, linked, errUnknownAddress
	}
	if err != nil {
		return person, linked, err
	}
	linked, err = link(ctx, tx, &person, claims.Email, now)
	return person, linked, err
}

func (m *Module) startSession(ctx context.Context, tx bun.Tx, person models.Person, linked models.LinkedEmail, token string, hash []byte) (Session, error) {
	session, err := insertSession(ctx, tx, linked.ID, browserDevice(ctx), hash, m.now().UTC())
	if err != nil {
		return Session{}, err
	}
	return Session{Person: projectPerson(person), Token: token, ExpiresAt: session.ExpiresAt}, nil
}

// link adds a Linked Email to the Person. The first one a Person ever links
// also becomes where their update emails go.
func link(ctx context.Context, tx bun.Tx, person *models.Person, email string, now time.Time) (models.LinkedEmail, error) {
	linkedBefore, err := tx.NewSelect().Model((*models.LinkedEmail)(nil)).Where("person_id = ?", person.ID).Exists(ctx)
	if err != nil {
		return models.LinkedEmail{}, errorstack.CaptureContext(ctx, err)
	}
	linked := models.LinkedEmail{ID: models.NewUUIDv7(), PersonID: person.ID, Email: email, CreatedAt: now}
	if _, err := tx.NewInsert().Model(&linked).Exec(ctx); err != nil {
		return models.LinkedEmail{}, errorstack.CaptureContext(ctx, err)
	}
	if !linkedBefore {
		person.UpdateEmailID = &linked.ID
		person.UpdateEmail = linked.Email
		person.EmailUpdates = true
		if _, err := tx.NewUpdate().Model(person).Column("update_email_id", "email_updates").WherePK().Exec(ctx); err != nil {
			return models.LinkedEmail{}, errorstack.CaptureContext(ctx, err)
		}
	}
	return linked, nil
}

// claimInstallation makes the Person the first Curator, once.
func claimInstallation(ctx context.Context, tx bun.Tx, personID models.UUID, now time.Time) error {
	updated, err := tx.NewUpdate().Table("installation").
		Set("claimed_by = ?", personID).Set("claimed_at = ?", now).
		Where("singleton = true").Where("claimed_by IS NULL").Exec(ctx)
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
	return nil
}

// newSecret is an opaque credential and the hash that is stored in its place.
func newSecret() (string, []byte, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", nil, errorstack.Capture(err)
	}
	secret := base64.RawURLEncoding.EncodeToString(bytes)
	hash := sha256.Sum256([]byte(secret))
	return secret, hash[:], nil
}

func insertSession(ctx context.Context, tx bun.Tx, emailID models.UUID, device string, hash []byte, now time.Time) (models.Session, error) {
	session := models.Session{ID: models.NewUUIDv7(), Device: device, TokenHash: hash, LinkedEmailID: emailID, CreatedAt: now, RenewedAt: now, ExpiresAt: now.Add(SessionLifetime)}
	if _, err := tx.NewInsert().Model(&session).Exec(ctx); err != nil {
		return models.Session{}, errorstack.CaptureContext(ctx, err)
	}
	return session, nil
}

func projectPerson(person models.Person) Person {
	return Person{ID: person.ID.String(), DisplayName: person.DisplayName, IsCurator: person.IsCurator,
		OnboardingCompletedAt: person.OnboardingCompletedAt, DeactivatedAt: person.DeactivatedAt,
		UpdateEmail: person.UpdateEmail, EmailUpdates: person.EmailUpdates, OfferedAlbumUpdates: person.OfferedAlbumUpdates,
		AvatarURL: personAvatarURL(person)}
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
