package emaildelivery

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

var errUnsubscribeURLUnavailable = errors.New("unsubscribe URL is unavailable")

type durableUnsubscribe struct {
	URL  string
	Hash [sha256.Size]byte
}

// SetPublicURL installs the validated public origin used by optional email links.
func (s *Service) SetPublicURL(publicURL string) { s.publicURL = strings.TrimRight(publicURL, "/") }

func (s *Service) newDurableUnsubscribeURL(ctx context.Context, batchID int64) (durableUnsubscribe, error) {
	if s.publicURL == "" {
		return durableUnsubscribe{}, errUnsubscribeURLUnavailable
	}
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return durableUnsubscribe{}, fmt.Errorf("generate unsubscribe token: %w", err)
	}
	hash := sha256.Sum256(token[:])
	result, err := s.db.NewRaw(`INSERT INTO notification_preference_tokens
		(token_hash, recipient_access_generation_id, notification_batch_id, created_at, expires_at)
		SELECT ?, batch.recipient_access_generation_id, batch.id,
		       clock_timestamp(), clock_timestamp() + interval '1 year'
		FROM notification_batches AS batch
		WHERE batch.id = ? AND batch.status = 'pending'`, hash[:], batchID).Exec(ctx)
	if err != nil {
		return durableUnsubscribe{}, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return durableUnsubscribe{}, err
	}
	if inserted == 0 {
		return durableUnsubscribe{}, nil
	}
	encoded := base64.RawURLEncoding.EncodeToString(token[:])
	return durableUnsubscribe{
		URL:  s.publicURL + "/api/email/preferences/unsubscribe?token=" + url.QueryEscape(encoded),
		Hash: hash,
	}, nil
}

func lockDurableUnsubscribe(ctx context.Context, tx bun.Tx, token durableUnsubscribe, accessID uuid.UUID, batchID int64) error {
	var hash []byte
	err := tx.NewRaw(`SELECT token_hash FROM notification_preference_tokens
		WHERE token_hash = ? AND recipient_access_generation_id = ?
		  AND notification_batch_id = ? AND expires_at > clock_timestamp()
		FOR SHARE`, token.Hash[:], accessID, batchID).Scan(ctx, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnsubscribeToken
	}
	return err
}

func unsubscribeTokenHash(encoded string) ([sha256.Size]byte, error) {
	token, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(token) != 32 {
		return [sha256.Size]byte{}, ErrUnsubscribeToken
	}
	return sha256.Sum256(token), nil
}

// ValidateUnsubscribe confirms that a preference token is current without mutating durable state.
func (s *Service) ValidateUnsubscribe(ctx context.Context, encoded string) error {
	hash, err := unsubscribeTokenHash(encoded)
	if err != nil {
		return err
	}
	var valid bool
	if err := s.db.NewRaw(`SELECT EXISTS (
		SELECT 1
		FROM notification_preference_tokens AS token
		JOIN recipient_access_generations AS access
		  ON access.id = token.recipient_access_generation_id AND access.is_current
		JOIN notification_preferences AS preference
		  ON preference.recipient_access_generation_id = token.recipient_access_generation_id
		WHERE token.token_hash = ? AND token.expires_at > clock_timestamp()
	)`, hash[:]).Scan(ctx, &valid); err != nil {
		return err
	}
	if !valid {
		return ErrUnsubscribeToken
	}
	return nil
}

// Unsubscribe disables optional email for the current Recipient generation named by an opaque token.
func (s *Service) Unsubscribe(ctx context.Context, encoded string) error {
	hash, err := unsubscribeTokenHash(encoded)
	if err != nil {
		return err
	}
	result, err := s.db.NewRaw(`UPDATE notification_preferences AS preference
		SET email_preference = 'none', updated_at = clock_timestamp()
		FROM notification_preference_tokens AS token
		JOIN recipient_access_generations AS access
		  ON access.id = token.recipient_access_generation_id AND access.is_current
		WHERE token.token_hash = ? AND token.expires_at > clock_timestamp()
		  AND preference.recipient_access_generation_id = token.recipient_access_generation_id`, hash[:]).Exec(ctx)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return ErrUnsubscribeToken
	}
	return nil
}
