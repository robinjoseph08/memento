package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/uptrace/bun"
)

const (
	// SignInCodeLifetime is how long an emailed Sign-in Code works.
	SignInCodeLifetime = 10 * time.Minute
	// signInCodeAttempts wrong guesses kill a code.
	signInCodeAttempts = 5
	// signInCodeCooldown is the least time between two emails to one address.
	signInCodeCooldown = time.Minute
	// unknownCodesPerHour caps mail to addresses Memento does not know, so
	// the sign-in page cannot be used to send mail to strangers. Known
	// addresses are never capped, so the family cannot be locked out.
	unknownCodesPerHour = 10
	// signInCodeSendTimeout bounds the SMTP session the Person waits on. The
	// send outlives a dropped request so the code still arrives.
	signInCodeSendTimeout = 30 * time.Second
)

var (
	ErrSignInCodesUnavailable = &errcodes.Error{HTTPCode: 404, Code: "sign_in_codes_unavailable", Message: "Signing in with an emailed code is not available here."}
	// ErrSignInCodeUnsent wraps the mail failure, which the server logs. The
	// browser sees server errors redacted and words this one itself.
	ErrSignInCodeUnsent = &errcodes.Error{HTTPCode: 503, Code: "sign_in_code_unsent", Message: "The sign-in code email could not be sent."}
	// ErrNameRequired means the code is right but the address is unknown: the
	// Person gives a name and verifies again to request access. The code is
	// not spent.
	ErrNameRequired    = errors.New("a name is needed to request access")
	errSignInCodeWrong = fieldError("code", "That code isn't right. Check the email and try again.")
	errSignInCodeDead  = fieldError("code", "This code no longer works. Send a new code.")
)

// Sender delivers one email now. Sign-in Codes skip the durable queue because
// a code is worthless after ten minutes and a retry is the wrong tool.
type Sender interface {
	Send(context.Context, notifications.Message) error
}

// SignInCodesAvailable reports whether this Installation can email Sign-in Codes.
func (m *Module) SignInCodesAvailable() bool { return m.Sender != nil }

// RequestSignInCode emails a new Sign-in Code that replaces any earlier one.
// It reports success whether or not Memento knows the address, and also when
// a send limit quietly sends nothing, so nobody can learn who belongs.
func (m *Module) RequestSignInCode(ctx context.Context, request RequestSignInCodeRequest) error {
	if m.Sender == nil {
		return ErrSignInCodesUnavailable
	}
	email, err := normalizeEmail(request.Email)
	if err != nil {
		return fieldError("email", "Enter an email address like name@example.com.")
	}
	code, err := newSignInCode()
	if err != nil {
		return err
	}
	var row models.SignInCode
	err = m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		now := m.now().UTC()
		if _, err := tx.NewDelete().Model((*models.SignInCode)(nil)).Where("created_at <= ?", now.Add(-time.Hour)).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		recent, err := tx.NewSelect().Model((*models.SignInCode)(nil)).
			Where("email = ? AND created_at > ?", email, now.Add(-signInCodeCooldown)).Exists(ctx)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if recent {
			return nil
		}
		known, err := knownAddress(ctx, tx, email)
		if err != nil {
			return err
		}
		if !known {
			sent, err := tx.NewSelect().Model((*models.SignInCode)(nil)).Where("NOT known").Count(ctx)
			if err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
			if sent >= unknownCodesPerHour {
				return nil
			}
		}
		hash := sha256.Sum256([]byte(code))
		row = models.SignInCode{ID: models.NewUUIDv7(), Email: email, CodeHash: hash[:], Known: known, CreatedAt: now, ExpiresAt: now.Add(SignInCodeLifetime)}
		_, err = tx.NewInsert().Model(&row).Exec(ctx)
		return errorstack.CaptureContext(ctx, err)
	})
	if err != nil || row.ID == (models.UUID{}) {
		return err
	}
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), signInCodeSendTimeout)
	defer cancel()
	if err := m.Sender.Send(sendCtx, m.signInCodeMessage(email, code)); err != nil {
		// A code that never went out neither replaces the previous one nor
		// holds back an immediate retry.
		if deleteErr := m.change(context.WithoutCancel(ctx), func(ctx context.Context, tx bun.Tx) error {
			_, err := tx.NewDelete().Model(&row).WherePK().Exec(ctx)
			return errorstack.CaptureContext(ctx, err)
		}); deleteErr != nil {
			return errors.Join(fmt.Errorf("%w: %w", ErrSignInCodeUnsent, err), deleteErr)
		}
		return fmt.Errorf("%w: %w", ErrSignInCodeUnsent, err)
	}
	return nil
}

// knownAddress reports whether an address would sign its Person in: it is
// linked or has an unused Preauthorization. Before the Installation is
// claimed nobody is known, so a public unclaimed Installation stays capped.
func knownAddress(ctx context.Context, tx bun.Tx, email string) (bool, error) {
	linked, err := tx.NewSelect().Model((*models.LinkedEmail)(nil)).Where("email = ? AND unlinked_at IS NULL", email).Exists(ctx)
	if err != nil || linked {
		return linked, errorstack.CaptureContext(ctx, err)
	}
	approved, err := tx.NewSelect().Model((*models.Preauthorization)(nil)).Where("email = ? AND consumed_at IS NULL AND revoked_at IS NULL", email).Exists(ctx)
	return approved, errorstack.CaptureContext(ctx, err)
}

// newSignInCode is six uniformly random digits.
func newSignInCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", errorstack.Capture(err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// signInCodeMessage puts the code in the subject, so it shows in a
// notification, and ends with the origin-bound line Apple Mail and Safari
// read to offer the code for autofill on this Installation only.
func (m *Module) signInCodeMessage(email, code string) notifications.Message {
	host := "localhost"
	if u, err := url.Parse(m.PublicURL); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	return notifications.Message{Kind: "sign_in_code", To: email, Subject: code + " is your Memento sign-in code", Body: fmt.Sprintf(`Your Memento sign-in code is:

%s

It expires in 10 minutes. If you didn't try to sign in to Memento, you can ignore this email.

@%s #%s
`, code, host, code)}
}

// VerifySignInCode checks the newest code sent to the address and resolves it
// the way Google sign-in does. A code works once, dies after five wrong
// guesses, and is not spent when an unknown address must first give a name
// (ErrNameRequired). With a name, an unknown address spends the code on an
// Access Request and gets ErrAccessRequested.
func (m *Module) VerifySignInCode(ctx context.Context, request VerifySignInCodeRequest) (Session, error) {
	email, err := normalizeEmail(request.Email)
	if err != nil {
		return Session{}, errSignInCodeWrong
	}
	name := strings.TrimSpace(request.DisplayName)
	if name != "" {
		if name, err = displayName(name); err != nil {
			return Session{}, err
		}
	}
	code := strings.Join(strings.Fields(request.Code), "")
	token, hash, err := newSecret()
	if err != nil {
		return Session{}, err
	}
	claims := Claims{Email: email, EmailVerified: true, DisplayName: name}
	var result Session
	var outcome error
	err = m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		now := m.now().UTC()
		var row models.SignInCode
		err := tx.NewSelect().Model(&row).Where("email = ?", email).
			Order("created_at DESC", "id DESC").Limit(1).Scan(ctx)
		// No code at all reads as a wrong guess, so a quietly unsent code
		// looks no different from one in someone's inbox.
		if errors.Is(err, sql.ErrNoRows) {
			outcome = errSignInCodeWrong
			return nil
		}
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if row.UsedAt != nil || !row.ExpiresAt.After(now) || row.Attempts >= signInCodeAttempts {
			outcome = errSignInCodeDead
			return nil
		}
		given := sha256.Sum256([]byte(code))
		if subtle.ConstantTimeCompare(given[:], row.CodeHash) != 1 {
			// Commit the wrong guess while refusing it. The last allowed guess
			// says the code is spent rather than inviting another try.
			outcome = errSignInCodeWrong
			if row.Attempts+1 >= signInCodeAttempts {
				outcome = errSignInCodeDead
			}
			_, err := tx.NewUpdate().Model(&row).Set("attempts = attempts + 1").WherePK().Exec(ctx)
			return errorstack.CaptureContext(ctx, err)
		}
		person, linked, err := m.resolveAddress(ctx, tx, claims)
		unknown := errors.Is(err, errUnknownAddress)
		if unknown && name == "" {
			outcome = ErrNameRequired
			return nil
		}
		if err != nil && !unknown {
			return err
		}
		if _, err := tx.NewUpdate().Model(&row).Set("used_at = ?", now).WherePK().Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if unknown {
			outcome = ErrAccessRequested
			return m.recordAccessRequest(ctx, tx, claims)
		}
		result, err = m.startSession(ctx, tx, person, linked, token, hash)
		return err
	})
	if err == nil && outcome != nil {
		return Session{}, outcome
	}
	return result, err
}
