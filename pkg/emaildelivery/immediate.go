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
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/notificationactivity"
	"github.com/robinjoseph08/memento/pkg/smtp"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/uptrace/bun"
)

const (
	ImmediateJobKind         = "send_immediate_email"
	WeeklyJobKind            = "send_weekly_email"
	coalescingWindow         = 15 * time.Minute
	maxPreviewBytes          = 20 << 20
	maxPreviewPixels         = 480
	maxPreviewSourceArea     = 25_000_000
	maxEmailBatchItems       = 100
	maxEmailPublicationMedia = 100
	emailBodyLineBudget      = 30 << 10

	publicationBatchItem batchItemKind = "publication"
	commentBatchItem     batchItemKind = "comment"
)

var (
	errPreviewDimensions        = errors.New("preview dimensions are invalid")
	errPreviewDimensionsChanged = errors.New("preview dimensions changed during decode")
	errUnsupportedEmailItemKind = errors.New("unsupported immediate batch item kind")
	errMalformedEmailItem       = errors.New("malformed immediate batch item")
)

type previewSource interface {
	EmailThumbnail(ctx context.Context, assetID uuid.UUID, request immich.MediaRequest) (immich.MediaResponse, error)
}

type emailBatchJobPayload struct {
	BatchID int64 `json:"batch_id"`
}

type assembledImmediate struct {
	Recipient      string
	Body           string
	PreviewMediaID uuid.UUID
	PreviewAssetID uuid.UUID
	PreviewAllowed bool
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
		return batchItemSpec{}, fmt.Errorf("%w: %q", errUnsupportedEmailItemKind, kind)
	}
}

type emailComment struct {
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
			AccessID          uuid.UUID `bun:"access_id"`
			CreatedAt         time.Time `bun:"created_at"`
			Preference        string    `bun:"email_preference"`
			WeeklyDay         string    `bun:"weekly_day"`
			WeeklyLocalTime   string    `bun:"weekly_local_time"`
			WeeklyTimezone    string    `bun:"weekly_timezone"`
			PreferenceVersion int64     `bun:"preference_version"`
		}
		var candidates []candidate
		if err := tx.NewRaw(`
			SELECT activity.recipient_access_generation_id AS access_id, activity.created_at,
			       preference.email_preference, preference.weekly_day, preference.weekly_local_time,
			       CASE WHEN preference.weekly_schedule_overridden THEN preference.weekly_timezone
			            ELSE settings.weekly_timezone END AS weekly_timezone,
			       preference.preference_version
			FROM publication_activity_items AS activity
			JOIN system_settings AS settings ON settings.id = 1
			JOIN publications AS publication ON publication.id = activity.publication_id
			JOIN recipient_access_generations AS access
			  ON access.id = activity.recipient_access_generation_id
			 AND access.is_current AND access.state = 'completed'
			JOIN people AS person
			  ON person.id = access.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
			JOIN notification_preferences AS preference
			  ON preference.recipient_access_generation_id = access.id
			 AND preference.email_preference IN ('immediate', 'weekly')
			JOIN recipient_emails AS email
			  ON email.recipient_access_generation_id = access.id AND email.is_current
			WHERE activity.publication_id = ? AND publication.notify_recipients
			  AND EXISTS (
				SELECT 1
				FROM LATERAL (
					SELECT candidate.media_item_id
					FROM publication_notification_media AS candidate
					WHERE candidate.publication_id = activity.publication_id
					  AND candidate.recipient_access_generation_id = activity.recipient_access_generation_id
					ORDER BY candidate.media_item_id
					LIMIT ?
				) AS candidate_media
				JOIN current_audience_entitlements AS entitlement
				  ON entitlement.event_id = publication.event_id
				 AND entitlement.recipient_access_generation_id = activity.recipient_access_generation_id
				 AND entitlement.media_item_id = candidate_media.media_item_id
				JOIN current_published_placements AS placement
				  ON placement.event_id = entitlement.event_id
				 AND placement.media_item_id = entitlement.media_item_id
				JOIN published_moments AS moment ON moment.id = placement.published_moment_id
				JOIN media_items AS media ON media.id = candidate_media.media_item_id AND media.availability = 'current'
				WHERE NOT content_is_withdrawn(placement.event_id, moment.draft_moment_id, placement.media_item_id)
			  )
			ORDER BY activity.recipient_access_generation_id
		`, publicationID, maxEmailPublicationMedia+1).Scan(ctx, &candidates); err != nil {
			return err
		}
		for _, candidate := range candidates {
			if candidate.Preference == "immediate" {
				if err := s.queueImmediateItem(ctx, tx, candidate.AccessID, publicationBatchItem, publicationID, candidate.CreatedAt, candidate.PreferenceVersion); err != nil {
					return err
				}
				continue
			}
			schedule, err := parseWeeklySchedule(candidate.WeeklyDay, candidate.WeeklyLocalTime, candidate.WeeklyTimezone)
			if err != nil {
				return err
			}
			if err := s.queueWeeklyItem(ctx, tx, candidate.AccessID, publicationBatchItem, publicationID, candidate.CreatedAt, candidate.PreferenceVersion, schedule); err != nil {
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
	var preference Preference
	var preferenceVersion int64
	if err := tx.NewRaw(`SELECT preference.email_preference, preference.weekly_day, preference.weekly_local_time,
		       CASE WHEN preference.weekly_schedule_overridden THEN preference.weekly_timezone
		            ELSE settings.weekly_timezone END, preference.preference_version
		FROM notification_preferences AS preference
		JOIN system_settings AS settings ON settings.id = 1
		WHERE preference.recipient_access_generation_id = ?`, accessID).
		Scan(ctx, &preference.EmailPreference, &preference.WeeklyDay, &preference.WeeklyLocalTime, &preference.WeeklyTimezone, &preferenceVersion); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if preference.EmailPreference != "immediate" && preference.EmailPreference != "weekly" {
		return nil
	}
	comment, eligible, err := loadEmailComment(ctx, tx, accessID, commentID, preference.EmailPreference, false)
	if err != nil || !eligible {
		return err
	}
	if preference.EmailPreference == "immediate" {
		return s.queueImmediateItem(ctx, tx, accessID, commentBatchItem, commentID, comment.CreatedAt, preferenceVersion)
	}
	schedule, err := parseWeeklySchedule(preference.WeeklyDay, preference.WeeklyLocalTime, preference.WeeklyTimezone)
	if err != nil {
		return err
	}
	return s.queueWeeklyItem(ctx, tx, accessID, commentBatchItem, commentID, comment.CreatedAt, preferenceVersion, schedule)
}

type pendingEmailBatch struct {
	ID              int64
	PublicID        uuid.UUID
	WindowStartedAt time.Time
	Truncated       bool
}

func (s *Service) queueImmediateItem(ctx context.Context, tx bun.Tx, accessID uuid.UUID, kind batchItemKind, sourceID uuid.UUID, activityAt time.Time, preferenceVersion int64) error {
	return notificationactivity.QueueImmediate(ctx, tx, accessID, notificationactivity.Kind(kind), sourceID, activityAt, notificationactivity.Target{
		Channel: notificationactivity.Email, JobKind: ImmediateJobKind, PreferenceVersion: preferenceVersion,
	})
}

// HandleImmediate reauthorizes at assembly and again while holding authorization locks through SMTP acceptance.
func (s *Service) HandleImmediate(ctx context.Context, job worker.Job) error {
	var payload emailBatchJobPayload
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
	if !assembled.Empty && assembled.PreviewAllowed && assembled.PreviewAssetID != uuid.Nil && s.previewSource != nil {
		preview = s.loadSafePreview(ctx, assembled.PreviewAssetID)
	}
	if s.beforeOptionalSend != nil {
		s.beforeOptionalSend()
	}

	var unsubscribe durableUnsubscribe
	if !assembled.Empty {
		unsubscribe, err = s.newDurableUnsubscribeURL(ctx, payload.BatchID)
		if err != nil {
			return err
		}
	}

	var sendErr error
	var terminalDiagnostic string
	var deliveryAttempts int
	var retryAfter time.Duration
	var terminalFailure bool
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := notificationactivity.LockBatchResources(ctx, tx, payload.BatchID); err != nil {
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
		if preview != nil && (!current.PreviewAllowed || current.PreviewMediaID != assembled.PreviewMediaID || current.PreviewAssetID != assembled.PreviewAssetID) {
			preview = nil
		}
		deliveryAttempts = attempts
		if s.cfg.RetryWindow > 0 && time.Since(closesAt) >= s.cfg.RetryWindow {
			terminalDiagnostic = "retry_window_exhausted"
			terminalFailure = true
			if _, err := tx.NewRaw(`UPDATE notification_batches
				SET status = 'failed', last_safe_error = ?, updated_at = clock_timestamp()
				WHERE id = ? AND status = 'pending'`, terminalDiagnostic, payload.BatchID).Exec(ctx); err != nil {
				return err
			}
			return recordEmailBatchProblemIn(ctx, tx, payload.BatchID, terminalDiagnostic)
		}
		if unsubscribe.URL == "" {
			return errUnsubscribeURLUnavailable
		}
		if err := lockDurableUnsubscribe(ctx, tx, unsubscribe, accessID, payload.BatchID); err != nil {
			return err
		}
		sendErr = s.sender.Send(ctx, smtp.Message{
			ID: publicID.String(), To: current.Recipient, Subject: "New activity in Memento",
			Body:           current.Body + "\n\nManage optional email or unsubscribe: " + unsubscribe.URL,
			UnsubscribeURL: unsubscribe.URL, Embedded: preview,
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
			if failure.Diagnostic == "recipient_rejected" {
				if _, err := tx.NewRaw(`UPDATE notification_preferences
					SET email_preference = 'none', preference_version = preference_version + 1,
					    updated_at = clock_timestamp()
					WHERE recipient_access_generation_id = ?`, accessID).Exec(ctx); err != nil {
					return err
				}
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
		if _, err = tx.NewRaw(`UPDATE notification_batches
			SET status = ?, attempts = attempts + 1, last_safe_error = ?, updated_at = clock_timestamp()
			WHERE id = ? AND status = 'pending'`, status, terminalDiagnostic, payload.BatchID).Exec(ctx); err != nil {
			return err
		}
		if terminalFailure {
			return recordEmailBatchProblemIn(ctx, tx, payload.BatchID, terminalDiagnostic)
		}
		return nil
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

func recordEmailBatchProblemIn(ctx context.Context, tx bun.Tx, batchID int64, diagnostic string) error {
	_, err := tx.NewRaw(`INSERT INTO delivery_problems (notification_batch_id, diagnostic)
		VALUES (?, ?) ON CONFLICT (notification_batch_id) DO NOTHING`, batchID, diagnostic).Exec(ctx)
	return err
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
	var truncated bool
	lockClause := ""
	if lock {
		lockClause = " FOR SHARE OF access, person, preference, email"
	}
	err := db.NewRaw(`
		SELECT access.id, email.email, batch.truncated
		FROM notification_batches AS batch
		JOIN recipient_access_generations AS access
		  ON access.id = batch.recipient_access_generation_id
		 AND access.is_current AND access.state = 'completed'
		JOIN people AS person
		  ON person.id = access.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
		JOIN notification_preferences AS preference
		  ON preference.recipient_access_generation_id = access.id
		 AND preference.email_preference = 'immediate'
		 AND preference.preference_version = batch.preference_version
		JOIN recipient_emails AS email
		  ON email.recipient_access_generation_id = access.id AND email.is_current
		WHERE batch.id = ? AND batch.channel = 'email' AND batch.cadence = 'immediate'
		  AND batch.status = 'pending'`+lockClause, batchID).Scan(ctx, &accessID, &recipient, &truncated)
	if errors.Is(err, sql.ErrNoRows) {
		return assembledImmediate{Empty: true}, nil
	}
	if err != nil {
		return assembledImmediate{}, err
	}
	previewAllowed, err := loadPreviewAcknowledgment(ctx, db, accessID, lock)
	if err != nil {
		return assembledImmediate{}, err
	}
	authorized, err := notificationactivity.AuthorizeBatch(ctx, db, batchID, lock)
	if err != nil {
		return assembledImmediate{}, err
	}
	truncated = truncated || authorized.Truncated
	lines := make([]string, 0, len(authorized.Activities)+1)
	lineBytes := 0
	result := assembledImmediate{Recipient: recipient, PreviewAllowed: previewAllowed}
	for _, activity := range authorized.Activities {
		if lineBytes+len(activity.Text)+1 > emailBodyLineBudget {
			truncated = true
			break
		}
		lines = append(lines, activity.Text)
		lineBytes += len(activity.Text) + 1
		if result.PreviewMediaID == uuid.Nil {
			result.PreviewMediaID, result.PreviewAssetID = activity.MediaID, activity.AssetID
		}
	}
	if len(lines) == 0 {
		return assembledImmediate{Empty: true}, nil
	}
	if truncated {
		lines = append(lines, "Additional activity is available in Memento.")
	}
	result.Body = "New activity is ready in Memento:\n\n" + strings.Join(lines, "\n") +
		"\n\nThis email excludes hidden item counts and Moment details. Sign in to Memento to view the activity you can currently access."
	return result, nil
}

func loadPreviewAcknowledgment(ctx context.Context, db bun.IDB, accessID uuid.UUID, lock bool) (bool, error) {
	lockClause := ""
	if lock {
		lockClause = " FOR SHARE"
	}
	var acknowledged bool
	err := db.NewRaw(`SELECT email_previews_acknowledged FROM onboarding_choices
		WHERE recipient_access_generation_id = ?`+lockClause, accessID).Scan(ctx, &acknowledged)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return acknowledged, err
}

func assemblePublication(ctx context.Context, db bun.IDB, accessID, publicationID uuid.UUID) (string, uuid.UUID, uuid.UUID, bool, error) {
	activity, survives, err := notificationactivity.AuthorizePublication(ctx, db, accessID, publicationID)
	return activity.Text, activity.MediaID, activity.AssetID, survives, err
}

func assembleComment(ctx context.Context, db bun.IDB, accessID, commentID uuid.UUID, lock bool) (string, uuid.UUID, uuid.UUID, bool, error) {
	comment, eligible, err := loadEmailComment(ctx, db, accessID, commentID, "immediate", lock)
	if err != nil || !eligible {
		return "", uuid.Nil, uuid.Nil, false, err
	}
	return comment.Author + " commented on an item you can access.", comment.MediaID, comment.AssetID, true, nil
}

// loadEmailComment is the shared queue-time and send-time Comment authorization boundary.
func loadEmailComment(ctx context.Context, db bun.IDB, accessID, commentID uuid.UUID, preferenceValue string, lock bool) (emailComment, bool, error) {
	var enabled bool
	if err := db.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM notification_preferences AS preference
		JOIN recipient_emails AS email ON email.recipient_access_generation_id = preference.recipient_access_generation_id
		 AND email.is_current
		WHERE preference.recipient_access_generation_id = ? AND preference.email_preference = ?
	)`, accessID, preferenceValue).Scan(ctx, &enabled); err != nil || !enabled {
		return emailComment{}, false, err
	}
	activity, survives, err := notificationactivity.AuthorizeComment(ctx, db, accessID, commentID, lock)
	if err != nil || !survives {
		return emailComment{}, false, err
	}
	return emailComment{CreatedAt: activity.ActivityCreatedAt, Author: activity.Author,
		MediaID: activity.MediaID, AssetID: activity.AssetID}, true, nil
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
	response, err := s.previewSource.EmailThumbnail(ctx, assetID, immich.MediaRequest{})
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
