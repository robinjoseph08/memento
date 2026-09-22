package identity

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"time"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

// MobileCodeLifetime bounds the hand-off from the browser sheet to the app.
const MobileCodeLifetime = time.Minute

var ErrMobileCodeInvalid = &errcodes.Error{HTTPCode: 401, Code: "invalid_code", Message: "That sign-in has expired. Start again from the app."}

// IssueMobileCode turns the browser's signed-in session into a single-use code
// for the Mobile App. Expired codes are removed while a new one is issued.
func (m *Module) IssueMobileCode(ctx context.Context, token string) (string, error) {
	code, hash, err := newSecret()
	if err != nil {
		return "", err
	}
	err = m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		now := m.now().UTC()
		linked, err := m.sessionIdentity(ctx, tx, token)
		if err != nil {
			return err
		}
		identityID := linked.ID
		if _, err := tx.NewDelete().Model((*models.MobileSignInCode)(nil)).Where("expires_at <= ?", now).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		pending := models.MobileSignInCode{CodeHash: hash, IdentityID: identityID, ExpiresAt: now.Add(MobileCodeLifetime)}
		_, err = tx.NewInsert().Model(&pending).Exec(ctx)
		return errorstack.CaptureContext(ctx, err)
	})
	return code, err
}

// ExchangeMobileCode issues the app its own session, labeled by platform. The
// code is consumed as it is read, so concurrent exchanges admit one phone.
func (m *Module) ExchangeMobileCode(ctx context.Context, code, platform string) (Session, error) {
	if len(code) != 43 {
		return Session{}, ErrMobileCodeInvalid
	}
	token, hash, err := newSecret()
	if err != nil {
		return Session{}, err
	}
	codeHash := sha256.Sum256([]byte(code))
	var result Session
	err = m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		now := m.now().UTC()
		var identityID models.UUID
		err := tx.NewDelete().Model((*models.MobileSignInCode)(nil)).
			Where("code_hash = ?", codeHash[:]).Where("expires_at > ?", now).
			Returning("identity_id").Scan(ctx, &identityID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrMobileCodeInvalid
		}
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		var person models.Person
		err = selectPeople(tx, &person).
			Join("JOIN identities AS i ON i.person_id = person.id").
			Where("i.id = ?", identityID).Where("i.unlinked_at IS NULL").Where("person.deactivated_at IS NULL").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAccessDenied
		}
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		session, err := insertSession(ctx, tx, identityID, "Memento on "+platform, hash, now)
		if err != nil {
			return err
		}
		result = Session{Person: projectPerson(person), Token: token, ExpiresAt: session.ExpiresAt}
		return nil
	})
	return result, err
}
