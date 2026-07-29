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
		  AND notification_batch_id = ? AND expires_at > clock_timestamp() AND consumed_at IS NULL
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

// PreferenceToken returns the current settings authorized by an unconsumed signed token.
func (s *Service) PreferenceToken(ctx context.Context, encoded string) (Preference, error) {
	hash, err := unsubscribeTokenHash(encoded)
	if err != nil {
		return Preference{}, err
	}
	var preference Preference
	err = s.db.NewRaw(`SELECT preference.email_preference, preference.weekly_day,
		       preference.weekly_local_time,
		       CASE WHEN preference.weekly_schedule_overridden THEN preference.weekly_timezone
		            ELSE settings.weekly_timezone END
		FROM notification_preference_tokens AS token
		JOIN system_settings AS settings ON settings.id = 1
		JOIN recipient_access_generations AS access
		  ON access.id = token.recipient_access_generation_id AND access.is_current
		JOIN notification_preferences AS preference
		  ON preference.recipient_access_generation_id = token.recipient_access_generation_id
		WHERE token.token_hash = ? AND token.expires_at > clock_timestamp() AND token.consumed_at IS NULL`, hash[:]).
		Scan(ctx, &preference.EmailPreference, &preference.WeeklyDay,
			&preference.WeeklyLocalTime, &preference.WeeklyTimezone)
	if errors.Is(err, sql.ErrNoRows) {
		return Preference{}, ErrUnsubscribeToken
	}
	return preference, err
}

// ValidateUnsubscribe confirms that a preference token is current without mutating durable state.
func (s *Service) ValidateUnsubscribe(ctx context.Context, encoded string) error {
	_, err := s.PreferenceToken(ctx, encoded)
	return err
}

// UpdatePreferenceToken consumes one signed token while changing optional email settings.
func (s *Service) UpdatePreferenceToken(ctx context.Context, encoded string, update PreferenceUpdate) error {
	hash, err := unsubscribeTokenHash(encoded)
	if err != nil {
		return err
	}
	if err := validatePreference(update); err != nil {
		return err
	}
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var accessID uuid.UUID
		err := tx.NewRaw(`SELECT token.recipient_access_generation_id
			FROM notification_preference_tokens AS token
			JOIN recipient_access_generations AS access
			  ON access.id = token.recipient_access_generation_id AND access.is_current
			WHERE token.token_hash = ? AND token.expires_at > clock_timestamp() AND token.consumed_at IS NULL
			FOR UPDATE OF token, access`, hash[:]).Scan(ctx, &accessID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUnsubscribeToken
		}
		if err != nil {
			return err
		}
		if err := s.updatePreferenceIn(ctx, tx, accessID, update, false); err != nil {
			return err
		}
		result, err := tx.NewRaw(`UPDATE notification_preference_tokens SET consumed_at = clock_timestamp()
			WHERE token_hash = ? AND consumed_at IS NULL`, hash[:]).Exec(ctx)
		if err != nil {
			return err
		}
		updated, err := result.RowsAffected()
		if err != nil || updated != 1 {
			if err != nil {
				return err
			}
			return ErrUnsubscribeToken
		}
		return nil
	})
}

// Unsubscribe consumes one signed token and disables optional email while retaining the weekly schedule.
func (s *Service) Unsubscribe(ctx context.Context, encoded string) error {
	preference, err := s.PreferenceToken(ctx, encoded)
	if err != nil {
		return err
	}
	return s.UpdatePreferenceToken(ctx, encoded, PreferenceUpdate{
		EmailPreference: "none",
		WeeklyDay:       preference.WeeklyDay,
		WeeklyLocalTime: preference.WeeklyLocalTime,
		WeeklyTimezone:  preference.WeeklyTimezone,
	})
}
