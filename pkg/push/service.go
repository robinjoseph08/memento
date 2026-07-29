// Package push owns trusted-device Web Push enrollment and immediate delivery.
package push

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/uptrace/bun"
)

var (
	ErrUnavailable      = errors.New("web push is unavailable")
	ErrTrustedRequired  = errors.New("a current trusted-device Session is required")
	ErrSubscription     = errors.New("push subscription is invalid")
	ErrEndpointConflict = errors.New("push endpoint is already active")
	errMaterial         = errors.New("stored push subscription is invalid")
)

// BrowserSubscriptionKeys is generated to TypeScript by Tygo.
type BrowserSubscriptionKeys struct {
	P256DH string `json:"p256dh" validate:"required,max=256"`
	Auth   string `json:"auth" validate:"required,max=128"`
}

// BrowserSubscription is generated to TypeScript by Tygo.
type BrowserSubscription struct {
	Endpoint       string                  `json:"endpoint" validate:"required,max=4096"`
	ExpirationTime *time.Time              `json:"expiration_time,omitempty"`
	Keys           BrowserSubscriptionKeys `json:"keys" validate:"required"`
}

// SubscriptionRequest is generated to TypeScript by Tygo.
type SubscriptionRequest = BrowserSubscription

// ReconcileRequest is generated to TypeScript by Tygo.
type ReconcileRequest struct {
	Subscription *BrowserSubscription `json:"subscription"`
}

// ConfigurationResponse is generated to TypeScript by Tygo.
type ConfigurationResponse struct {
	Available bool   `json:"available"`
	PublicKey string `json:"public_key"`
	Enrolled  bool   `json:"enrolled"`
}

// ReconcileResponse is generated to TypeScript by Tygo.
type ReconcileResponse struct {
	Enrolled    bool `json:"enrolled"`
	RemoveLocal bool `json:"remove_local"`
}

type Service struct {
	db     *bun.DB
	cfg    config.PushConfig
	policy *EndpointPolicy
	sender Sender
	aead   cipher.AEAD
	now    func() time.Time
}

func New(db *bun.DB, cfg config.PushConfig, securitySecret string, sender Sender, policy *EndpointPolicy) *Service {
	if policy == nil {
		policy = NewEndpointPolicy(nil)
	}
	service := &Service{db: db, cfg: cfg, policy: policy, sender: sender, now: time.Now}
	if len(securitySecret) >= 32 {
		key := sha256.Sum256(append([]byte("memento:web-push:"), []byte(securitySecret)...))
		if block, err := aes.NewCipher(key[:]); err == nil {
			service.aead, _ = cipher.NewGCM(block)
		}
	}
	return service
}

func (s *Service) Configured() bool {
	return s != nil && s.cfg.Enabled && s.sender != nil && s.aead != nil
}

func (s *Service) Configuration(ctx context.Context, actor setup.SessionActor) (ConfigurationResponse, error) {
	response := ConfigurationResponse{Available: s.Configured(), PublicKey: s.cfg.PublicKey}
	if !s.Configured() {
		return response, nil
	}
	var trusted bool
	if err := s.db.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM sessions AS session
		JOIN system_settings AS settings ON settings.id = 1 AND settings.security_epoch = session.security_epoch
		JOIN recipient_access_generations AS access ON access.id = session.recipient_access_generation_id
		 AND access.is_current AND access.state = 'completed'
		WHERE session.id = ? AND session.person_id = ? AND access.id = ? AND session.session_type = 'trusted'
		 AND session.revoked_at IS NULL AND session.idle_expires_at > clock_timestamp()
	)`, actor.SessionID, actor.PersonID, actor.AccessID).Scan(ctx, &trusted); err != nil {
		return ConfigurationResponse{}, err
	}
	if !trusted {
		response.Available = false
		response.PublicKey = ""
		return response, nil
	}
	if err := s.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM push_subscriptions
		WHERE session_id = ? AND person_id = ? AND disabled_at IS NULL
		 AND (expiration_at IS NULL OR expiration_at > clock_timestamp()))`, actor.SessionID, actor.PersonID).Scan(ctx, &response.Enrolled); err != nil {
		return ConfigurationResponse{}, err
	}
	return response, nil
}

func (s *Service) Enroll(ctx context.Context, actor setup.SessionActor, request SubscriptionRequest) (ConfigurationResponse, error) {
	if !s.Configured() {
		return ConfigurationResponse{}, ErrUnavailable
	}
	if err := s.validateSubscription(ctx, request); err != nil {
		return ConfigurationResponse{}, err
	}
	hash := sha256.Sum256([]byte(request.Endpoint))
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := requireTrustedSession(ctx, tx, actor, true); err != nil {
			return err
		}
		type existingRow struct {
			ID                uuid.UUID
			SessionID         uuid.UUID
			DisabledAt        *time.Time
			EnrollmentVersion int64
		}
		var endpoint existingRow
		err := tx.NewRaw(`SELECT id, session_id, disabled_at, enrollment_version
			FROM push_subscriptions WHERE endpoint_hash = ? FOR UPDATE`, hash[:]).Scan(ctx, &endpoint)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && endpoint.DisabledAt == nil && endpoint.SessionID != actor.SessionID {
			return ErrEndpointConflict
		}
		if _, err := tx.NewRaw(`UPDATE push_subscriptions SET disabled_at = clock_timestamp(), updated_at = clock_timestamp()
			WHERE session_id = ? AND disabled_at IS NULL AND endpoint_hash <> ?`, actor.SessionID, hash[:]).Exec(ctx); err != nil {
			return err
		}
		if err == nil {
			version := endpoint.EnrollmentVersion + 1
			ciphertext, err := s.encrypt(endpoint.ID, version, request)
			if err != nil {
				return err
			}
			_, err = tx.NewRaw(`UPDATE push_subscriptions SET session_id = ?, person_id = ?, session_type = 'trusted',
				material_ciphertext = ?, expiration_at = ?, enrollment_version = ?, disabled_at = NULL,
				last_reconciled_at = clock_timestamp(), last_success_at = NULL, updated_at = clock_timestamp()
				WHERE id = ?`, actor.SessionID, actor.PersonID, ciphertext, request.ExpirationTime, version, endpoint.ID).Exec(ctx)
			return err
		}
		id := uuid.New()
		ciphertext, err := s.encrypt(id, 1, request)
		if err != nil {
			return err
		}
		_, err = tx.NewRaw(`INSERT INTO push_subscriptions
			(id, session_id, person_id, session_type, endpoint_hash, material_ciphertext, expiration_at,
			 enrollment_version, last_reconciled_at, created_at)
			VALUES (?, ?, ?, 'trusted', ?, ?, ?, 1, clock_timestamp(), clock_timestamp())`,
			id, actor.SessionID, actor.PersonID, hash[:], ciphertext, request.ExpirationTime).Exec(ctx)
		return err
	})
	if err != nil {
		return ConfigurationResponse{}, err
	}
	return ConfigurationResponse{Available: true, PublicKey: s.cfg.PublicKey, Enrolled: true}, nil
}

func (s *Service) Reconcile(ctx context.Context, actor setup.SessionActor, request ReconcileRequest) (ReconcileResponse, error) {
	if !s.Configured() {
		return ReconcileResponse{RemoveLocal: request.Subscription != nil}, nil
	}
	if request.Subscription != nil {
		if err := s.validateSubscription(ctx, *request.Subscription); err != nil {
			return ReconcileResponse{}, err
		}
	}
	response := ReconcileResponse{}
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := requireTrustedSession(ctx, tx, actor, true); err != nil {
			response.RemoveLocal = request.Subscription != nil
			return nil
		}
		type row struct {
			ID                uuid.UUID
			EndpointHash      []byte
			EnrollmentVersion int64
		}
		var current row
		err := tx.NewRaw(`SELECT id, endpoint_hash, enrollment_version FROM push_subscriptions
			WHERE session_id = ? AND disabled_at IS NULL FOR UPDATE`, actor.SessionID).Scan(ctx, &current)
		if errors.Is(err, sql.ErrNoRows) {
			response.RemoveLocal = request.Subscription != nil
			return nil
		}
		if err != nil {
			return err
		}
		if request.Subscription == nil {
			_, err = tx.NewRaw(`UPDATE push_subscriptions SET disabled_at = clock_timestamp(), updated_at = clock_timestamp()
				WHERE id = ? AND disabled_at IS NULL`, current.ID).Exec(ctx)
			return err
		}
		hash := sha256.Sum256([]byte(request.Subscription.Endpoint))
		if !equalHash(current.EndpointHash, hash[:]) {
			response.RemoveLocal = true
			_, err = tx.NewRaw(`UPDATE push_subscriptions SET disabled_at = clock_timestamp(), updated_at = clock_timestamp()
				WHERE id = ? AND disabled_at IS NULL`, current.ID).Exec(ctx)
			return err
		}
		ciphertext, err := s.encrypt(current.ID, current.EnrollmentVersion, *request.Subscription)
		if err != nil {
			return err
		}
		_, err = tx.NewRaw(`UPDATE push_subscriptions SET material_ciphertext = ?, expiration_at = ?,
			last_reconciled_at = clock_timestamp(), updated_at = clock_timestamp() WHERE id = ?`,
			ciphertext, request.Subscription.ExpirationTime, current.ID).Exec(ctx)
		if err == nil {
			response.Enrolled = true
		}
		return err
	})
	return response, err
}

func (s *Service) Disable(ctx context.Context, actor setup.SessionActor) error {
	if !s.Configured() {
		return nil
	}
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := requireTrustedSession(ctx, tx, actor, true); err != nil {
			return err
		}
		_, err := tx.NewRaw(`UPDATE push_subscriptions SET disabled_at = clock_timestamp(), updated_at = clock_timestamp()
			WHERE session_id = ? AND person_id = ? AND disabled_at IS NULL`, actor.SessionID, actor.PersonID).Exec(ctx)
		return err
	})
}

func requireTrustedSession(ctx context.Context, db bun.IDB, actor setup.SessionActor, lock bool) error {
	lockClause := ""
	if lock {
		lockClause = " FOR UPDATE OF session"
	}
	var id uuid.UUID
	err := db.NewRaw(`SELECT session.id FROM sessions AS session
		JOIN people AS person ON person.id = session.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
		JOIN recipient_access_generations AS access ON access.id = session.recipient_access_generation_id
		 AND access.person_id = session.person_id AND access.is_current AND access.state = 'completed'
		JOIN system_settings AS settings ON settings.id = 1 AND settings.setup_complete
		 AND settings.security_epoch = session.security_epoch
		WHERE session.id = ? AND session.person_id = ? AND access.id = ? AND session.session_type = 'trusted'
		 AND session.revoked_at IS NULL AND session.idle_expires_at > clock_timestamp()`+lockClause,
		actor.SessionID, actor.PersonID, actor.AccessID).Scan(ctx, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrTrustedRequired
	}
	return err
}

func (s *Service) validateSubscription(ctx context.Context, subscription BrowserSubscription) error {
	if err := s.policy.Validate(ctx, subscription.Endpoint); err != nil {
		return fmt.Errorf("%w: %v", ErrSubscription, err)
	}
	if subscription.ExpirationTime != nil && !subscription.ExpirationTime.After(s.now()) {
		return ErrSubscription
	}
	p256dh, err := decodeBrowserKey(subscription.Keys.P256DH)
	if err != nil || len(p256dh) != 65 {
		return ErrSubscription
	}
	if _, err := ecdh.P256().NewPublicKey(p256dh); err != nil {
		return ErrSubscription
	}
	auth, err := decodeBrowserKey(subscription.Keys.Auth)
	if err != nil || len(auth) < 16 || len(auth) > 64 {
		return ErrSubscription
	}
	return nil
}

func decodeBrowserKey(value string) ([]byte, error) {
	if decoded, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return base64.RawStdEncoding.DecodeString(value)
}

func (s *Service) encrypt(id uuid.UUID, version int64, material BrowserSubscription) ([]byte, error) {
	if s.aead == nil {
		return nil, errMaterial
	}
	plaintext, err := json.Marshal(material)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, plaintext, []byte(fmt.Sprintf("%s:%d", id, version))), nil
}

func (s *Service) decrypt(id uuid.UUID, version int64, ciphertext []byte) (BrowserSubscription, error) {
	if s.aead == nil || len(ciphertext) < s.aead.NonceSize() {
		return BrowserSubscription{}, errMaterial
	}
	nonce := ciphertext[:s.aead.NonceSize()]
	plaintext, err := s.aead.Open(nil, nonce, ciphertext[s.aead.NonceSize():], []byte(fmt.Sprintf("%s:%d", id, version)))
	if err != nil {
		return BrowserSubscription{}, errMaterial
	}
	var material BrowserSubscription
	if err := json.Unmarshal(plaintext, &material); err != nil {
		return BrowserSubscription{}, errMaterial
	}
	return material, nil
}

func equalHash(left, right []byte) bool {
	return subtle.ConstantTimeCompare(left, right) == 1
}
