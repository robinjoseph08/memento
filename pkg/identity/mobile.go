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

// HandoffCodeLifetime bounds the hand-off from the browser sheet to the app.
const HandoffCodeLifetime = time.Minute

var ErrHandoffCodeInvalid = &errcodes.Error{HTTPCode: 401, Code: "invalid_code", Message: "That sign-in has expired. Start again from the app."}

// IssueHandoffCode hands the browser's signed-in session over to the Mobile
// App as a Hand-off Code, and ends that session in the same transaction:
// the phone's session replaces it, so the next sign-in from the app starts
// fresh and can be someone else. Expired codes are removed on the way.
func (m *Module) IssueHandoffCode(ctx context.Context, token string) (string, error) {
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
		tokenHash := sha256.Sum256([]byte(token))
		if _, err := tx.NewDelete().Model((*models.Session)(nil)).Where("token_hash = ?", tokenHash[:]).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if _, err := tx.NewDelete().Model((*models.HandoffCode)(nil)).Where("expires_at <= ?", now).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		pending := models.HandoffCode{CodeHash: hash, IdentityID: identityID, ExpiresAt: now.Add(HandoffCodeLifetime)}
		_, err = tx.NewInsert().Model(&pending).Exec(ctx)
		return errorstack.CaptureContext(ctx, err)
	})
	return code, err
}

// ExchangeHandoffCode issues the app its own session, labeled by platform. The
// code is consumed as it is read, so concurrent exchanges admit one phone.
func (m *Module) ExchangeHandoffCode(ctx context.Context, code, platform string) (Session, error) {
	if len(code) != 43 {
		return Session{}, ErrHandoffCodeInvalid
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
		err := tx.NewDelete().Model((*models.HandoffCode)(nil)).
			Where("code_hash = ?", codeHash[:]).Where("expires_at > ?", now).
			Returning("identity_id").Scan(ctx, &identityID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrHandoffCodeInvalid
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
