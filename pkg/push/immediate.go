package push

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/notificationactivity"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/uptrace/bun"
)

const JobKind = "send_immediate_push"

var errLeaseLost = errors.New("push delivery job lease lost")

const (
	maximumPushActivities = 20
	maximumPushPayload    = 3000
)

type pushActivity struct {
	Kind          notificationactivity.Kind `json:"kind"`
	Title         string                    `json:"title,omitempty"`
	AdditionCount int                       `json:"addition_count,omitempty"`
	CountCapped   bool                      `json:"count_capped,omitempty"`
	Author        string                    `json:"author,omitempty"`
}

type pushPayload struct {
	Version    int            `json:"version"`
	Activities []pushActivity `json:"activities"`
	Truncated  bool           `json:"truncated,omitempty"`
}

// QueuePublication creates independent device batches for currently enabled trusted Sessions.
func (s *Service) QueuePublication(ctx context.Context, _ uuid.UUID, publicationID uuid.UUID) error {
	if !s.Configured() {
		return nil
	}
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		type candidate struct {
			AccessID          uuid.UUID
			SubscriptionID    uuid.UUID
			EnrollmentVersion int64
			CreatedAt         time.Time
		}
		var candidates []candidate
		if err := tx.NewRaw(`SELECT activity.recipient_access_generation_id AS access_id,
			 subscription.id AS subscription_id, subscription.enrollment_version, activity.created_at
			FROM publication_activity_items AS activity
			JOIN publications AS publication ON publication.id = activity.publication_id
			JOIN recipient_access_generations AS access ON access.id = activity.recipient_access_generation_id
			 AND access.is_current AND access.state = 'completed'
			JOIN people AS person ON person.id = access.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
			JOIN push_subscriptions AS subscription ON subscription.person_id = person.id AND subscription.disabled_at IS NULL
			 AND (subscription.expiration_at IS NULL OR subscription.expiration_at > clock_timestamp())
			JOIN sessions AS session ON session.id = subscription.session_id AND session.person_id = person.id
			 AND session.recipient_access_generation_id = access.id AND session.session_type = 'trusted'
			 AND session.revoked_at IS NULL AND session.idle_expires_at > clock_timestamp()
			JOIN system_settings AS settings ON settings.id = 1 AND settings.setup_complete
			 AND settings.security_epoch = session.security_epoch
			WHERE activity.publication_id = ? AND publication.notify_recipients
			AND EXISTS (
			 SELECT 1 FROM LATERAL (
			  SELECT candidate.media_item_id FROM publication_notification_media AS candidate
			  WHERE candidate.publication_id = activity.publication_id
			   AND candidate.recipient_access_generation_id = activity.recipient_access_generation_id
			  ORDER BY candidate.media_item_id LIMIT ?
			 ) AS candidate_media
			 JOIN current_audience_entitlements AS entitlement ON entitlement.event_id = publication.event_id
			  AND entitlement.recipient_access_generation_id = activity.recipient_access_generation_id
			  AND entitlement.media_item_id = candidate_media.media_item_id
			 JOIN current_published_placements AS placement ON placement.event_id = entitlement.event_id
			  AND placement.media_item_id = entitlement.media_item_id
			 JOIN published_moments AS moment ON moment.id = placement.published_moment_id
			 JOIN media_items AS media ON media.id = candidate_media.media_item_id AND media.availability = 'current'
			 WHERE NOT content_is_withdrawn(placement.event_id, moment.draft_moment_id, placement.media_item_id)
			) ORDER BY activity.recipient_access_generation_id, subscription.id`, publicationID,
			notificationactivity.MaxPublicationMedia+1).Scan(ctx, &candidates); err != nil {
			return err
		}
		for _, candidate := range candidates {
			subscriptionID := candidate.SubscriptionID
			if err := notificationactivity.QueueImmediate(ctx, tx, candidate.AccessID, notificationactivity.Publication,
				publicationID, candidate.CreatedAt, notificationactivity.Target{Channel: notificationactivity.Push, JobKind: JobKind,
					PreferenceVersion: candidate.EnrollmentVersion, PushSubscriptionID: &subscriptionID}); err != nil {
				return err
			}
		}
		return nil
	})
}

// QueueComment creates independent device batches inside the authorized Comment transaction.
func (s *Service) QueueComment(ctx context.Context, tx bun.Tx, accessID, commentID uuid.UUID) error {
	if !s.Configured() {
		return nil
	}
	activity, eligible, err := notificationactivity.AuthorizeComment(ctx, tx, accessID, commentID, false)
	if err != nil || !eligible {
		return err
	}
	type candidate struct {
		SubscriptionID    uuid.UUID
		EnrollmentVersion int64
	}
	var candidates []candidate
	if err := tx.NewRaw(`SELECT subscription.id AS subscription_id, subscription.enrollment_version
		FROM push_subscriptions AS subscription
		JOIN sessions AS session ON session.id = subscription.session_id AND session.person_id = subscription.person_id
		 AND session.recipient_access_generation_id = ? AND session.session_type = 'trusted'
		 AND session.revoked_at IS NULL AND session.idle_expires_at > clock_timestamp()
		JOIN system_settings AS settings ON settings.id = 1 AND settings.setup_complete
		 AND settings.security_epoch = session.security_epoch
		WHERE subscription.disabled_at IS NULL
		 AND (subscription.expiration_at IS NULL OR subscription.expiration_at > clock_timestamp())
		ORDER BY subscription.id`, accessID).Scan(ctx, &candidates); err != nil {
		return err
	}
	for _, candidate := range candidates {
		subscriptionID := candidate.SubscriptionID
		if err := notificationactivity.QueueImmediate(ctx, tx, accessID, notificationactivity.Comment,
			commentID, activity.ActivityCreatedAt, notificationactivity.Target{Channel: notificationactivity.Push, JobKind: JobKind,
				PreferenceVersion: candidate.EnrollmentVersion, PushSubscriptionID: &subscriptionID}); err != nil {
			return err
		}
	}
	return nil
}

// Handle reauthorizes and holds Session, content, and preference locks through provider acceptance.
func (s *Service) Handle(ctx context.Context, job worker.Job) error {
	var payload notificationactivity.JobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.BatchID <= 0 {
		return worker.Permanent("invalid_push_job")
	}
	if !s.Configured() {
		return worker.Permanent("push_disabled")
	}
	var sendErr error
	var diagnostic string
	var retryAfter time.Duration
	var terminal bool
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := notificationactivity.LockBatchResources(ctx, tx, payload.BatchID); err != nil {
			return err
		}
		var subscriptionID uuid.UUID
		var publicID uuid.UUID
		var status string
		var attempts int
		var closesAt time.Time
		if err := tx.NewRaw(`SELECT push_subscription_id, public_id, status, attempts, closes_at
			FROM notification_batches WHERE id = ? AND channel = 'push' FOR UPDATE`, payload.BatchID).
			Scan(ctx, &subscriptionID, &publicID, &status, &attempts, &closesAt); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return worker.Permanent("push_batch_missing")
			}
			return err
		}
		if status != "pending" {
			return nil
		}
		if err := requireLease(ctx, tx, job); err != nil {
			return err
		}
		if s.cfg.RetryWindow > 0 && time.Since(closesAt) >= s.cfg.RetryWindow {
			diagnostic, terminal = "retry_window_exhausted", true
			return failPushBatch(ctx, tx, payload.BatchID, diagnostic)
		}

		var materialCiphertext []byte
		var enrollmentVersion int64
		var storedID uuid.UUID
		err := tx.NewRaw(`SELECT subscription.id, subscription.enrollment_version, subscription.material_ciphertext
			FROM push_subscriptions AS subscription
			JOIN notification_batches AS batch ON batch.push_subscription_id = subscription.id
			 AND batch.preference_version = subscription.enrollment_version
			JOIN sessions AS session ON session.id = subscription.session_id AND session.person_id = subscription.person_id
			 AND session.session_type = 'trusted' AND session.revoked_at IS NULL
			 AND session.idle_expires_at > clock_timestamp()
			JOIN recipient_access_generations AS access ON access.id = session.recipient_access_generation_id
			 AND access.id = batch.recipient_access_generation_id AND access.is_current AND access.state = 'completed'
			JOIN people AS person ON person.id = session.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
			JOIN system_settings AS settings ON settings.id = 1 AND settings.setup_complete
			 AND settings.security_epoch = session.security_epoch
			WHERE batch.id = ? AND subscription.id = ? AND subscription.disabled_at IS NULL
			 AND (subscription.expiration_at IS NULL OR subscription.expiration_at > clock_timestamp())
			FOR UPDATE OF subscription, session, access, person, settings`, payload.BatchID, subscriptionID).
			Scan(ctx, &storedID, &enrollmentVersion, &materialCiphertext)
		if errors.Is(err, sql.ErrNoRows) {
			if _, updateErr := tx.NewRaw(`UPDATE push_subscriptions AS subscription
				SET disabled_at = clock_timestamp(), updated_at = clock_timestamp()
				WHERE subscription.id = ? AND subscription.disabled_at IS NULL AND (
				 subscription.expiration_at <= clock_timestamp() OR NOT EXISTS (
				  SELECT 1 FROM sessions AS session
				  JOIN recipient_access_generations AS access ON access.id = session.recipient_access_generation_id
				   AND access.is_current AND access.state = 'completed'
				  JOIN people AS person ON person.id = session.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
				  JOIN system_settings AS settings ON settings.id = 1 AND settings.setup_complete
				   AND settings.security_epoch = session.security_epoch
				  WHERE session.id = subscription.session_id AND session.person_id = subscription.person_id
				   AND session.session_type = 'trusted' AND session.revoked_at IS NULL
				   AND session.idle_expires_at > clock_timestamp()
				 )
				)`, subscriptionID).Exec(ctx); updateErr != nil {
				return updateErr
			}
			_, updateErr := tx.NewRaw(`UPDATE notification_batches SET status = 'suppressed', last_safe_error = NULL,
				updated_at = clock_timestamp() WHERE id = ? AND status = 'pending'`, payload.BatchID).Exec(ctx)
			return updateErr
		}
		if err != nil {
			return err
		}
		authorized, err := notificationactivity.AuthorizeBatch(ctx, tx, payload.BatchID, true)
		if err != nil {
			return err
		}
		if authorized.Empty() {
			_, err := tx.NewRaw(`UPDATE notification_batches SET status = 'suppressed', last_safe_error = NULL,
				updated_at = clock_timestamp() WHERE id = ? AND status = 'pending'`, payload.BatchID).Exec(ctx)
			return err
		}
		material, err := s.decrypt(storedID, enrollmentVersion, materialCiphertext)
		if err != nil {
			if _, updateErr := tx.NewRaw(`UPDATE push_subscriptions SET disabled_at = clock_timestamp(), updated_at = clock_timestamp()
				WHERE id = ? AND disabled_at IS NULL`, storedID).Exec(ctx); updateErr != nil {
				return updateErr
			}
			_, updateErr := tx.NewRaw(`UPDATE notification_batches SET status = 'suppressed', last_safe_error = 'subscription_invalid',
				updated_at = clock_timestamp() WHERE id = ? AND status = 'pending'`, payload.BatchID).Exec(ctx)
			return updateErr
		}
		contents, err := authorizedPushPayload(authorized)
		if err != nil {
			return err
		}
		result, attemptErr := s.sender.Send(ctx, material, contents)
		if attemptErr == nil && result.StatusCode >= 200 && result.StatusCode < 300 {
			if _, err := tx.NewRaw(`UPDATE push_subscriptions SET last_success_at = clock_timestamp(), updated_at = clock_timestamp()
				WHERE id = ?`, storedID).Exec(ctx); err != nil {
				return err
			}
			_, err = tx.NewRaw(`UPDATE notification_batches SET status = 'sent', attempts = attempts + 1,
				sent_at = clock_timestamp(), last_safe_error = NULL, updated_at = clock_timestamp()
				WHERE id = ? AND status = 'pending'`, payload.BatchID).Exec(ctx)
			return err
		}
		if attemptErr == nil && (result.StatusCode == http.StatusNotFound || result.StatusCode == http.StatusGone) {
			if _, err := tx.NewRaw(`UPDATE push_subscriptions SET disabled_at = clock_timestamp(), updated_at = clock_timestamp()
				WHERE id = ? AND disabled_at IS NULL`, storedID).Exec(ctx); err != nil {
				return err
			}
			_, err = tx.NewRaw(`UPDATE notification_batches SET status = 'suppressed', attempts = attempts + 1,
				last_safe_error = 'subscription_expired', updated_at = clock_timestamp()
				WHERE id = ? AND status = 'pending'`, payload.BatchID).Exec(ctx)
			return err
		}
		sendErr = attemptErr
		temporary := attemptErr != nil || result.StatusCode == http.StatusTooManyRequests || result.StatusCode >= 500
		if temporary {
			diagnostic = "push_unavailable"
			retryAfter = s.retryDelay(attempts)
			remaining := s.cfg.RetryWindow - time.Since(closesAt)
			if remaining <= 0 {
				diagnostic, terminal = "retry_window_exhausted", true
				return failPushBatch(ctx, tx, payload.BatchID, diagnostic)
			}
			if retryAfter > remaining {
				retryAfter = remaining
			}
			_, err = tx.NewRaw(`UPDATE notification_batches SET attempts = attempts + 1, last_safe_error = ?,
				updated_at = clock_timestamp() WHERE id = ? AND status = 'pending'`, diagnostic, payload.BatchID).Exec(ctx)
			return err
		}
		diagnostic, terminal = "push_rejected", true
		return failPushBatch(ctx, tx, payload.BatchID, diagnostic)
	})
	if err != nil {
		return err
	}
	if terminal {
		return worker.Permanent(diagnostic)
	}
	if sendErr != nil || diagnostic != "" {
		return worker.RetryAfter(retryAfter, diagnostic)
	}
	return nil
}

func authorizedPushPayload(authorized notificationactivity.AuthorizedSet) ([]byte, error) {
	payload := pushPayload{Version: 1, Truncated: authorized.Truncated}
	for _, activity := range authorized.Activities {
		item := pushActivity{Kind: activity.Kind}
		switch activity.Kind {
		case notificationactivity.Publication:
			item.Title = activity.Title
			item.AdditionCount = min(activity.AdditionCount, notificationactivity.MaxPublicationMedia)
			item.CountCapped = activity.AdditionCount > notificationactivity.MaxPublicationMedia
		case notificationactivity.Comment:
			item.Author = activity.Author
		default:
			continue
		}
		candidate := payload
		candidate.Activities = append(append([]pushActivity(nil), payload.Activities...), item)
		encoded, err := json.Marshal(candidate)
		if err != nil {
			return nil, err
		}
		if len(candidate.Activities) > maximumPushActivities || len(encoded) > maximumPushPayload {
			payload.Truncated = true
			break
		}
		payload = candidate
	}
	if len(payload.Activities) < len(authorized.Activities) {
		payload.Truncated = true
	}
	return json.Marshal(payload)
}

func failPushBatch(ctx context.Context, tx bun.Tx, batchID int64, diagnostic string) error {
	if _, err := tx.NewRaw(`UPDATE notification_batches SET status = 'failed', attempts = attempts + 1,
		last_safe_error = ?, updated_at = clock_timestamp() WHERE id = ? AND status = 'pending'`, diagnostic, batchID).Exec(ctx); err != nil {
		return err
	}
	_, err := tx.NewRaw(`INSERT INTO delivery_problems (notification_batch_id, diagnostic)
		VALUES (?, ?) ON CONFLICT (notification_batch_id) DO NOTHING`, batchID, diagnostic).Exec(ctx)
	return err
}

func requireLease(ctx context.Context, db bun.IDB, job worker.Job) error {
	var owned bool
	if err := db.NewRaw(`SELECT EXISTS (SELECT 1 FROM jobs WHERE id = ? AND status = 'running'
		AND lease_owner = ? AND lease_expires_at > clock_timestamp())`, job.ID, job.LeaseOwner).Scan(ctx, &owned); err != nil {
		return err
	}
	if !owned {
		return errLeaseLost
	}
	return nil
}

func (s *Service) retryDelay(attempts int) time.Duration {
	delay := s.cfg.RetryBase
	for range attempts {
		if delay >= s.cfg.RetryMax/2 {
			return s.cfg.RetryMax
		}
		delay *= 2
	}
	if delay > s.cfg.RetryMax {
		return s.cfg.RetryMax
	}
	return delay
}
