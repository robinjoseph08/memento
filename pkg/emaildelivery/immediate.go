package emaildelivery

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"strings"
	"time"

	// Register derivative decoders used by image.Decode before metadata-free JPEG encoding.
	_ "image/gif"
	_ "image/png"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/internal/placementlock"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/smtp"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/uptrace/bun"
)

const (
	ImmediateJobKind     = "send_immediate_email"
	coalescingWindow     = 15 * time.Minute
	maxPreviewBytes      = 20 << 20
	maxPreviewPixels     = 480
	maxPreviewSourceArea = 25_000_000

	publicationBatchItem batchItemKind = "publication"
	commentBatchItem     batchItemKind = "comment"
)

var (
	errPreviewDimensions            = errors.New("preview dimensions are invalid")
	errPreviewDimensionsChanged     = errors.New("preview dimensions changed during decode")
	errUnsupportedImmediateItemKind = errors.New("unsupported immediate batch item kind")
	errMalformedImmediateItem       = errors.New("malformed immediate batch item")
)

type previewSource interface {
	Thumbnail(ctx context.Context, assetID uuid.UUID, request immich.MediaRequest) (immich.MediaResponse, error)
}

type immediateJobPayload struct {
	BatchID int64 `json:"batch_id"`
}

type assembledImmediate struct {
	Recipient      string
	Body           string
	PreviewMediaID uuid.UUID
	PreviewAssetID uuid.UUID
	Empty          bool
}

type batchItemKind string

type batchItem struct {
	ID                int64
	Kind              batchItemKind
	PublicationID     *uuid.UUID
	CommentID         *uuid.UUID
	ActivityCreatedAt time.Time
}

type batchItemSpec struct {
	sourceColumn string
	sourceID     func(batchItem) *uuid.UUID
	assemble     func(context.Context, bun.IDB, uuid.UUID, uuid.UUID, bool) (string, uuid.UUID, uuid.UUID, bool, error)
}

func (kind batchItemKind) spec() (batchItemSpec, error) {
	switch kind {
	case publicationBatchItem:
		return batchItemSpec{
			sourceColumn: "publication_id",
			sourceID:     func(item batchItem) *uuid.UUID { return item.PublicationID },
			assemble: func(ctx context.Context, db bun.IDB, accessID, sourceID uuid.UUID, _ bool) (string, uuid.UUID, uuid.UUID, bool, error) {
				return assemblePublication(ctx, db, accessID, sourceID)
			},
		}, nil
	case commentBatchItem:
		return batchItemSpec{
			sourceColumn: "comment_id",
			sourceID:     func(item batchItem) *uuid.UUID { return item.CommentID },
			assemble:     assembleComment,
		}, nil
	default:
		return batchItemSpec{}, fmt.Errorf("%w: %q", errUnsupportedImmediateItemKind, kind)
	}
}

type immediateComment struct {
	CreatedAt time.Time
	Author    string
	MediaID   uuid.UUID
	AssetID   uuid.UUID
}

// SetPreviewSource installs the private derivative source used for embedded previews.
func (s *Service) SetPreviewSource(source previewSource) { s.previewSource = source }

// QueuePublication creates or extends immediate email batches for currently eligible Publication activity.
// It is safe to call after the Publication transaction commits and is a no-op when optional SMTP is disabled.
func (s *Service) QueuePublication(ctx context.Context, _ uuid.UUID, publicationID uuid.UUID) error {
	if !s.Configured() {
		return nil
	}
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		type candidate struct {
			AccessID  uuid.UUID `bun:"access_id"`
			CreatedAt time.Time `bun:"created_at"`
		}
		var candidates []candidate
		if err := tx.NewRaw(`
			SELECT activity.recipient_access_generation_id AS access_id, activity.created_at
			FROM publication_activity_items AS activity
			JOIN publications AS publication ON publication.id = activity.publication_id
			JOIN recipient_access_generations AS access
			  ON access.id = activity.recipient_access_generation_id
			 AND access.is_current AND access.state = 'completed'
			JOIN people AS person
			  ON person.id = access.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
			JOIN notification_preferences AS preference
			  ON preference.recipient_access_generation_id = access.id
			 AND preference.email_preference = 'immediate'
			JOIN recipient_emails AS email
			  ON email.recipient_access_generation_id = access.id AND email.is_current
			WHERE activity.publication_id = ? AND publication.notify_recipients
			  AND EXISTS (
				SELECT 1
				FROM publication_notification_media AS candidate_media
				JOIN current_audience_entitlements AS entitlement
				  ON entitlement.event_id = publication.event_id
				 AND entitlement.recipient_access_generation_id = activity.recipient_access_generation_id
				 AND entitlement.media_item_id = candidate_media.media_item_id
				JOIN current_published_placements AS placement
				  ON placement.event_id = entitlement.event_id
				 AND placement.media_item_id = entitlement.media_item_id
				JOIN published_moments AS moment ON moment.id = placement.published_moment_id
				JOIN media_items AS media ON media.id = candidate_media.media_item_id AND media.availability = 'current'
				WHERE candidate_media.publication_id = activity.publication_id
				  AND candidate_media.recipient_access_generation_id = activity.recipient_access_generation_id
				  AND NOT content_is_withdrawn(placement.event_id, moment.draft_moment_id, placement.media_item_id)
			  )
			ORDER BY activity.recipient_access_generation_id
		`, publicationID).Scan(ctx, &candidates); err != nil {
			return err
		}
		for _, candidate := range candidates {
			if err := s.queueImmediateItem(ctx, tx, candidate.AccessID, publicationBatchItem, publicationID, candidate.CreatedAt); err != nil {
				return err
			}
		}
		return nil
	})
}

// QueueComment creates or extends one immediate email batch in the caller's authorized Comment transaction.
func (s *Service) QueueComment(ctx context.Context, tx bun.Tx, accessID, commentID uuid.UUID) error {
	if !s.Configured() {
		return nil
	}
	comment, eligible, err := loadImmediateComment(ctx, tx, accessID, commentID, false)
	if err != nil || !eligible {
		return err
	}
	return s.queueImmediateItem(ctx, tx, accessID, commentBatchItem, commentID, comment.CreatedAt)
}

func (s *Service) queueImmediateItem(ctx context.Context, tx bun.Tx, accessID uuid.UUID, kind batchItemKind, sourceID uuid.UUID, activityAt time.Time) error {
	spec, err := kind.spec()
	if err != nil {
		return err
	}
	if _, err := tx.NewRaw(`SELECT pg_advisory_xact_lock(hashtextextended(?::text, 4610))`, accessID.String()).Exec(ctx); err != nil {
		return err
	}
	column := spec.sourceColumn
	var alreadyQueued bool
	if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM notification_batch_items
		WHERE recipient_access_generation_id = ? AND kind = ? AND `+column+` = ?)`,
		accessID, kind, sourceID).Scan(ctx, &alreadyQueued); err != nil {
		return err
	}
	if alreadyQueued {
		return nil
	}
	var batchID int64
	err = tx.NewRaw(`
		SELECT id FROM notification_batches
		WHERE recipient_access_generation_id = ? AND channel = 'email' AND status = 'pending'
		  AND window_started_at <= ? AND closes_at > ?
		ORDER BY window_started_at LIMIT 1 FOR UPDATE
	`, accessID, activityAt, activityAt).Scan(ctx, &batchID)
	if errors.Is(err, sql.ErrNoRows) {
		publicID := uuid.New()
		closesAt := activityAt.UTC().Add(coalescingWindow)
		if err := tx.NewRaw(`
			INSERT INTO notification_batches (
				public_id, recipient_access_generation_id, channel, window_started_at, closes_at
			) VALUES (?, ?, 'email', ?, ?) RETURNING id
		`, publicID, accessID, activityAt.UTC(), closesAt).Scan(ctx, &batchID); err != nil {
			return err
		}
		payload, err := json.Marshal(immediateJobPayload{BatchID: batchID})
		if err != nil {
			return err
		}
		if _, err := tx.NewRaw(`
			INSERT INTO outbox_events (
				kind, aggregate_kind, aggregate_id, aggregate_version, payload, available_at, created_at
			) VALUES (?, 'notification_batch', ?, 1, ?::jsonb, ?, clock_timestamp())
		`, ImmediateJobKind, publicID.String(), string(payload), closesAt).Exec(ctx); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	_, err = tx.NewRaw(`INSERT INTO notification_batch_items
		(batch_id, recipient_access_generation_id, kind, `+column+`, activity_created_at)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`, batchID, accessID, kind, sourceID, activityAt.UTC()).Exec(ctx)
	return err
}

// HandleImmediate reauthorizes at assembly and again while holding authorization locks through SMTP acceptance.
func (s *Service) HandleImmediate(ctx context.Context, job worker.Job) error {
	var payload immediateJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.BatchID <= 0 {
		return worker.Permanent("invalid_immediate_email_job")
	}
	if !s.Configured() {
		return worker.Permanent("optional_email_disabled")
	}

	assembled, err := s.assembleImmediate(ctx, payload.BatchID, false)
	if err != nil {
		return err
	}
	var preview *smtp.EmbeddedImage
	if !assembled.Empty && assembled.PreviewAssetID != uuid.Nil && s.previewSource != nil {
		preview = s.loadSafePreview(ctx, assembled.PreviewAssetID)
	}
	if s.beforeImmediateSend != nil {
		s.beforeImmediateSend()
	}

	var sendErr error
	var terminalDiagnostic string
	var deliveryAttempts int
	var retryAfter time.Duration
	var terminalFailure bool
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := placementlock.Acquire(ctx, tx, placementlock.Shared); err != nil {
			return err
		}
		var accessID, publicID uuid.UUID
		var status string
		var attempts int
		var closesAt time.Time
		if err := tx.NewRaw(`SELECT recipient_access_generation_id, public_id, status, attempts, closes_at
			FROM notification_batches WHERE id = ? FOR UPDATE`, payload.BatchID).Scan(ctx, &accessID, &publicID, &status, &attempts, &closesAt); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return worker.Permanent("immediate_email_batch_missing")
			}
			return err
		}
		if status != "pending" {
			return nil
		}
		if err := s.requireLease(ctx, job); err != nil {
			return err
		}
		if err := lockImmediateMedia(ctx, tx, payload.BatchID); err != nil {
			return err
		}
		if err := lockImmediateEvents(ctx, tx, payload.BatchID); err != nil {
			return err
		}
		if err := lockRecipientGeneration(ctx, tx, accessID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		current, err := s.assembleImmediateIn(ctx, tx, payload.BatchID, true)
		if err != nil {
			return err
		}
		if current.Empty {
			_, err := tx.NewRaw(`UPDATE notification_batches
				SET status = 'suppressed', last_safe_error = NULL, updated_at = clock_timestamp()
				WHERE id = ? AND status = 'pending'`, payload.BatchID).Exec(ctx)
			return err
		}
		if preview != nil && (current.PreviewMediaID != assembled.PreviewMediaID || current.PreviewAssetID != assembled.PreviewAssetID) {
			preview = nil
		}
		deliveryAttempts = attempts
		if s.cfg.RetryWindow > 0 && time.Since(closesAt) >= s.cfg.RetryWindow {
			terminalDiagnostic = "retry_window_exhausted"
			terminalFailure = true
			_, err := tx.NewRaw(`UPDATE notification_batches
				SET status = 'failed', last_safe_error = ?, updated_at = clock_timestamp()
				WHERE id = ? AND status = 'pending'`, terminalDiagnostic, payload.BatchID).Exec(ctx)
			return err
		}
		sendErr = s.sender.Send(ctx, smtp.Message{
			ID: publicID.String(), To: current.Recipient, Subject: "New activity in Memento",
			Body: current.Body, Embedded: preview,
		})
		if sendErr == nil {
			_, err := tx.NewRaw(`UPDATE notification_batches
				SET status = 'sent', attempts = attempts + 1, sent_at = clock_timestamp(),
				    last_safe_error = NULL, updated_at = clock_timestamp()
				WHERE id = ? AND status = 'pending'`, payload.BatchID).Exec(ctx)
			return err
		}
		failure := &smtp.DeliveryError{Diagnostic: "smtp_unavailable", Temporary: true}
		if !errors.As(sendErr, &failure) {
			failure = &smtp.DeliveryError{Diagnostic: "smtp_unavailable", Temporary: true}
		}
		terminalDiagnostic = failure.Diagnostic
		status = "pending"
		if !failure.Temporary {
			status = "failed"
			terminalFailure = true
			if _, err := tx.NewRaw(`UPDATE notification_preferences
				SET email_preference = 'none', updated_at = clock_timestamp()
				WHERE recipient_access_generation_id = ?`, accessID).Exec(ctx); err != nil {
				return err
			}
		} else {
			retryAfter = s.retryDelay(deliveryAttempts)
			if s.cfg.RetryWindow > 0 {
				remaining := s.cfg.RetryWindow - time.Since(closesAt)
				if remaining <= 0 {
					status = "failed"
					terminalDiagnostic = "retry_window_exhausted"
					terminalFailure = true
				} else if retryAfter > remaining {
					retryAfter = remaining
				}
			}
		}
		_, err = tx.NewRaw(`UPDATE notification_batches
			SET status = ?, attempts = attempts + 1, last_safe_error = ?, updated_at = clock_timestamp()
			WHERE id = ? AND status = 'pending'`, status, terminalDiagnostic, payload.BatchID).Exec(ctx)
		return err
	})
	if err != nil {
		return err
	}
	if terminalFailure {
		return worker.Permanent(terminalDiagnostic)
	}
	if sendErr == nil {
		return nil
	}
	return worker.RetryAfter(retryAfter, terminalDiagnostic)
}

func (s *Service) assembleImmediate(ctx context.Context, batchID int64, lock bool) (assembledImmediate, error) {
	var result assembledImmediate
	err := s.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var err error
		result, err = s.assembleImmediateIn(ctx, tx, batchID, lock)
		return err
	})
	return result, err
}

func (s *Service) assembleImmediateIn(ctx context.Context, db bun.IDB, batchID int64, lock bool) (assembledImmediate, error) {
	var accessID uuid.UUID
	var recipient string
	lockClause := ""
	if lock {
		lockClause = " FOR SHARE OF access, person, preference, email"
	}
	err := db.NewRaw(`
		SELECT access.id, email.email
		FROM notification_batches AS batch
		JOIN recipient_access_generations AS access
		  ON access.id = batch.recipient_access_generation_id
		 AND access.is_current AND access.state = 'completed'
		JOIN people AS person
		  ON person.id = access.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
		JOIN notification_preferences AS preference
		  ON preference.recipient_access_generation_id = access.id
		 AND preference.email_preference = 'immediate'
		JOIN recipient_emails AS email
		  ON email.recipient_access_generation_id = access.id AND email.is_current
		WHERE batch.id = ? AND batch.status = 'pending'`+lockClause, batchID).Scan(ctx, &accessID, &recipient)
	if errors.Is(err, sql.ErrNoRows) {
		return assembledImmediate{Empty: true}, nil
	}
	if err != nil {
		return assembledImmediate{}, err
	}
	var items []batchItem
	if err := db.NewRaw(`SELECT id, kind, publication_id, comment_id, activity_created_at
		FROM notification_batch_items WHERE batch_id = ? ORDER BY activity_created_at, id`, batchID).Scan(ctx, &items); err != nil {
		return assembledImmediate{}, err
	}
	lines := make([]string, 0, len(items))
	result := assembledImmediate{Recipient: recipient}
	for _, item := range items {
		spec, err := item.Kind.spec()
		if err != nil {
			return assembledImmediate{}, err
		}
		sourceID := spec.sourceID(item)
		if sourceID == nil {
			return assembledImmediate{}, fmt.Errorf("%w: item %d has no %s", errMalformedImmediateItem, item.ID, spec.sourceColumn)
		}
		line, mediaID, assetID, survives, err := spec.assemble(ctx, db, accessID, *sourceID, lock)
		if err != nil {
			return assembledImmediate{}, err
		}
		if survives {
			lines = append(lines, line)
			if result.PreviewMediaID == uuid.Nil {
				result.PreviewMediaID, result.PreviewAssetID = mediaID, assetID
			}
		}
	}
	if len(lines) == 0 {
		return assembledImmediate{Empty: true}, nil
	}
	result.Body = "New activity is ready in Memento:\n\n" + strings.Join(lines, "\n") +
		"\n\nCounts include only items you can currently access. Sign in to Memento to view them."
	return result, nil
}

func lockImmediateEvents(ctx context.Context, db bun.IDB, batchID int64) error {
	var ids []uuid.UUID
	if err := db.NewRaw(`
		SELECT event.id
		FROM events AS event
		WHERE event.id IN (
			SELECT publication.event_id
			FROM notification_batch_items AS item
			JOIN publications AS publication ON publication.id = item.publication_id
			WHERE item.batch_id = ? AND item.kind = 'publication'
			UNION
			SELECT placement.event_id
			FROM notification_batch_items AS item
			JOIN comments AS comment ON comment.id = item.comment_id
			JOIN current_published_placements AS placement ON placement.media_item_id = comment.media_item_id
			WHERE item.batch_id = ? AND item.kind = 'comment'
		) ORDER BY event.id FOR SHARE
	`, batchID, batchID).Scan(ctx, &ids); err != nil {
		return err
	}
	return nil
}

func lockImmediateMedia(ctx context.Context, db bun.IDB, batchID int64) error {
	var ids []uuid.UUID
	return db.NewRaw(`
		SELECT media.id FROM media_items AS media
		WHERE media.id IN (
			SELECT candidate.media_item_id
			FROM notification_batch_items AS item
			JOIN notification_batches AS batch ON batch.id = item.batch_id
			JOIN publication_notification_media AS candidate
			  ON candidate.publication_id = item.publication_id
			 AND candidate.recipient_access_generation_id = batch.recipient_access_generation_id
			WHERE item.batch_id = ? AND item.kind = 'publication'
			UNION
			SELECT comment.media_item_id
			FROM notification_batch_items AS item
			JOIN comments AS comment ON comment.id = item.comment_id
			WHERE item.batch_id = ? AND item.kind = 'comment'
		) ORDER BY media.id FOR SHARE
	`, batchID, batchID).Scan(ctx, &ids)
}

func assemblePublication(ctx context.Context, db bun.IDB, accessID, publicationID uuid.UUID) (string, uuid.UUID, uuid.UUID, bool, error) {
	var title string
	var count int
	var mediaID, assetID uuid.UUID
	err := db.NewRaw(`
		SELECT current.title, count(DISTINCT candidate.media_item_id),
		       (array_agg(candidate.media_item_id ORDER BY placement.position))[1],
		       (array_agg(media.immich_asset_id ORDER BY placement.position))[1]
		FROM publications AS source
		JOIN current_published_events AS current ON current.event_id = source.event_id
		JOIN publication_notification_media AS candidate
		  ON candidate.publication_id = source.id
		 AND candidate.recipient_access_generation_id = ?
		JOIN current_audience_entitlements AS entitlement
		  ON entitlement.event_id = source.event_id
		 AND entitlement.recipient_access_generation_id = candidate.recipient_access_generation_id
		 AND entitlement.media_item_id = candidate.media_item_id
		JOIN current_published_placements AS placement
		  ON placement.event_id = entitlement.event_id AND placement.media_item_id = entitlement.media_item_id
		JOIN published_moments AS moment ON moment.id = placement.published_moment_id
		JOIN media_items AS media ON media.id = candidate.media_item_id AND media.availability = 'current'
		WHERE source.id = ? AND source.notify_recipients
		  AND NOT content_is_withdrawn(placement.event_id, moment.draft_moment_id, placement.media_item_id)
		GROUP BY current.title, source.id`, accessID, publicationID).Scan(ctx, &title, &count, &mediaID, &assetID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", uuid.Nil, uuid.Nil, false, nil
	}
	if err != nil {
		return "", uuid.Nil, uuid.Nil, false, err
	}
	if strings.TrimSpace(title) == "" {
		title = "A family event"
	}
	label := "new item"
	if count != 1 {
		label = "new items"
	}
	return fmt.Sprintf("%s: %d %s", title, count, label), mediaID, assetID, count > 0, nil
}

func assembleComment(ctx context.Context, db bun.IDB, accessID, commentID uuid.UUID, lock bool) (string, uuid.UUID, uuid.UUID, bool, error) {
	comment, eligible, err := loadImmediateComment(ctx, db, accessID, commentID, lock)
	if err != nil || !eligible {
		return "", uuid.Nil, uuid.Nil, false, err
	}
	return comment.Author + " commented on an item you can access.", comment.MediaID, comment.AssetID, true, nil
}

// loadImmediateComment is the shared queue-time and send-time Comment authorization boundary.
func loadImmediateComment(ctx context.Context, db bun.IDB, accessID, commentID uuid.UUID, lock bool) (immediateComment, bool, error) {
	lockClause := ""
	if lock {
		lockClause = " FOR SHARE OF comment"
		var subscriptionMediaID uuid.UUID
		err := db.NewRaw(`SELECT media_item_id FROM comment_subscriptions
			WHERE recipient_access_generation_id = ?
			  AND media_item_id = (SELECT media_item_id FROM comments WHERE id = ?)
			FOR SHARE`, accessID, commentID).Scan(ctx, &subscriptionMediaID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return immediateComment{}, false, err
		}
	}
	var comment immediateComment
	err := db.NewRaw(`
		SELECT comment.created_at, author.display_name AS author,
		       comment.media_item_id AS media_id, media.immich_asset_id AS asset_id
		FROM comments AS comment
		JOIN recipient_access_generations AS access
		  ON access.id = ? AND access.is_current AND access.state = 'completed'
		JOIN people AS recipient
		  ON recipient.id = access.person_id AND recipient.archived_at IS NULL AND recipient.merged_at IS NULL
		JOIN notification_preferences AS preference
		  ON preference.recipient_access_generation_id = access.id
		 AND preference.email_preference = 'immediate'
		JOIN recipient_emails AS email
		  ON email.recipient_access_generation_id = access.id AND email.is_current
		JOIN people AS author ON author.id = comment.author_person_id
		JOIN media_items AS media ON media.id = comment.media_item_id AND media.availability = 'current'
		WHERE comment.id = ? AND comment.state = 'active' AND comment.author_person_id <> access.person_id
		  AND (
			EXISTS (SELECT 1 FROM person_roles WHERE person_id = access.person_id AND role = 'curator')
			OR EXISTS (
				SELECT 1 FROM comment_subscriptions AS subscription
				WHERE subscription.media_item_id = comment.media_item_id
				  AND subscription.recipient_access_generation_id = access.id AND NOT subscription.muted
			)
		  )
		  AND EXISTS (
			SELECT 1 FROM current_published_placements AS placement
			JOIN published_moments AS moment ON moment.id = placement.published_moment_id
			WHERE placement.media_item_id = comment.media_item_id
			  AND NOT content_is_withdrawn(placement.event_id, moment.draft_moment_id, placement.media_item_id)
		  )
		  AND (
			EXISTS (SELECT 1 FROM person_roles WHERE person_id = access.person_id AND role = 'curator')
			OR EXISTS (
				SELECT 1 FROM current_audience_entitlements AS entitlement
				JOIN current_published_placements AS placement
				  ON placement.event_id = entitlement.event_id
				 AND placement.media_item_id = entitlement.media_item_id
				JOIN published_moments AS moment ON moment.id = placement.published_moment_id
				WHERE entitlement.recipient_access_generation_id = access.id
				  AND entitlement.media_item_id = comment.media_item_id
				  AND NOT content_is_withdrawn(placement.event_id, moment.draft_moment_id, placement.media_item_id)
			)
		  )`+lockClause, accessID, commentID).Scan(ctx, &comment)
	if errors.Is(err, sql.ErrNoRows) {
		return immediateComment{}, false, nil
	}
	if err != nil {
		return immediateComment{}, false, err
	}
	return comment, true, nil
}

func lockRecipientGeneration(ctx context.Context, tx bun.Tx, accessID uuid.UUID) error {
	var personID uuid.UUID
	if err := tx.NewRaw(`SELECT person.id FROM people AS person
		JOIN recipient_access_generations AS access ON access.person_id = person.id
		WHERE access.id = ? FOR SHARE OF person`, accessID).Scan(ctx, &personID); err != nil {
		return err
	}
	var locked uuid.UUID
	return tx.NewRaw(`SELECT id FROM recipient_access_generations WHERE id = ? FOR SHARE`, accessID).Scan(ctx, &locked)
}

func (s *Service) loadSafePreview(ctx context.Context, assetID uuid.UUID) *smtp.EmbeddedImage {
	response, err := s.previewSource.Thumbnail(ctx, assetID, immich.MediaRequest{})
	if err != nil || response.StatusCode != http.StatusOK {
		if response.Body != nil {
			_ = response.Body.Close()
		}
		return nil
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(response.Body, maxPreviewBytes+1))
	if err != nil || len(contents) > maxPreviewBytes {
		return nil
	}
	safe, err := safePreview(contents)
	if err != nil {
		return nil
	}
	return &smtp.EmbeddedImage{ContentID: "memento-preview", ContentType: "image/jpeg", Data: safe}
}

func safePreview(contents []byte) ([]byte, error) {
	configuration, _, err := image.DecodeConfig(bytes.NewReader(contents))
	if err != nil {
		return nil, err
	}
	if configuration.Width <= 0 || configuration.Height <= 0 ||
		configuration.Width > maxPreviewSourceArea/configuration.Height {
		return nil, errPreviewDimensions
	}
	decoded, _, err := image.Decode(bytes.NewReader(contents))
	if err != nil {
		return nil, err
	}
	bounds := decoded.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width != configuration.Width || height != configuration.Height {
		return nil, errPreviewDimensionsChanged
	}
	targetWidth, targetHeight := width, height
	if width > maxPreviewPixels || height > maxPreviewPixels {
		if width >= height {
			targetWidth = maxPreviewPixels
			targetHeight = max(1, height*maxPreviewPixels/width)
		} else {
			targetHeight = maxPreviewPixels
			targetWidth = max(1, width*maxPreviewPixels/height)
		}
	}
	resized := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	for y := 0; y < targetHeight; y++ {
		sourceY := bounds.Min.Y + y*height/targetHeight
		for x := 0; x < targetWidth; x++ {
			sourceX := bounds.Min.X + x*width/targetWidth
			value := color.RGBAModel.Convert(decoded.At(sourceX, sourceY)).(color.RGBA)
			resized.SetRGBA(x, y, value)
		}
	}
	var output bytes.Buffer
	if err := jpeg.Encode(&output, resized, &jpeg.Options{Quality: 82}); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
