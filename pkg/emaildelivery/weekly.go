package emaildelivery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/notificationactivity"
	"github.com/robinjoseph08/memento/pkg/smtp"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/uptrace/bun"
)

var (
	ErrNotificationPreference = errors.New("notification preference is invalid")
	weeklyTimePattern         = regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`)
	weeklyDays                = map[string]time.Weekday{
		"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday,
		"wednesday": time.Wednesday, "thursday": time.Thursday, "friday": time.Friday,
		"saturday": time.Saturday,
	}
)

type weeklySchedule struct {
	day      time.Weekday
	hour     int
	minute   int
	location *time.Location
}

func parseWeeklySchedule(day, localTime, timezone string) (weeklySchedule, error) {
	weekday, valid := weeklyDays[day]
	if !valid || !weeklyTimePattern.MatchString(localTime) || timezone == "" ||
		timezone != strings.TrimSpace(timezone) || len(timezone) > 255 || timezone == "Local" ||
		(timezone != "UTC" && !strings.Contains(timezone, "/")) {
		return weeklySchedule{}, ErrNotificationPreference
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return weeklySchedule{}, ErrNotificationPreference
	}
	parsed, err := time.Parse("15:04", localTime)
	if err != nil {
		return weeklySchedule{}, ErrNotificationPreference
	}
	return weeklySchedule{day: weekday, hour: parsed.Hour(), minute: parsed.Minute(), location: location}, nil
}

// Next returns the first scheduled instant strictly after the supplied instant.
func (schedule weeklySchedule) Next(after time.Time) time.Time {
	local := after.In(schedule.location)
	date := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	for offset := range 15 {
		candidateDate := date.AddDate(0, 0, offset)
		if candidateDate.Weekday() != schedule.day {
			continue
		}
		candidate := schedule.resolve(candidateDate)
		if candidate.After(after) {
			return candidate.UTC()
		}
	}
	return time.Time{}
}

// resolve chooses the first occurrence of a repeated wall time. If daylight
// saving skips the configured minute, it chooses the first valid minute after it.
func (schedule weeklySchedule) resolve(date time.Time) time.Time {
	start := date.Add(-16 * time.Hour)
	end := date.Add(40 * time.Hour)
	var firstAfter time.Time
	for instant := start; !instant.After(end); instant = instant.Add(time.Minute) {
		local := instant.In(schedule.location)
		if local.Year() != date.Year() || local.Month() != date.Month() || local.Day() != date.Day() {
			continue
		}
		minute := local.Hour()*60 + local.Minute()
		target := schedule.hour*60 + schedule.minute
		if minute == target {
			return instant
		}
		if minute > target && firstAfter.IsZero() {
			firstAfter = instant
		}
	}
	return firstAfter
}

const maxWeeklyPreviews = 3

type weeklyPreview struct {
	MediaID uuid.UUID
	AssetID uuid.UUID
}

type assembledWeekly struct {
	Recipient string
	Body      string
	Previews  []weeklyPreview
	Empty     bool
}

func (s *Service) queueWeeklyItem(ctx context.Context, tx bun.Tx, accessID uuid.UUID, kind batchItemKind, sourceID uuid.UUID, activityAt time.Time, preferenceVersion int64, schedule weeklySchedule) error {
	spec, err := kind.spec()
	if err != nil {
		return err
	}
	if _, err := tx.NewRaw(`SELECT pg_advisory_xact_lock(hashtextextended(?::text, 4610))`, accessID.String()).Exec(ctx); err != nil {
		return err
	}
	var alreadyQueued bool
	if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM notification_batch_items
		WHERE recipient_access_generation_id = ? AND kind = ? AND `+spec.sourceColumn+` = ?)`,
		accessID, kind, sourceID).Scan(ctx, &alreadyQueued); err != nil || alreadyQueued {
		return err
	}
	activityAt = activityAt.UTC()
	queuedAt := s.now().UTC()
	due := schedule.Next(queuedAt)
	windowStartedAt := queuedAt
	if due.IsZero() {
		return ErrNotificationPreference
	}
	var batch pendingEmailBatch
	for {
		var attempts int
		err = tx.NewRaw(`SELECT id, public_id, window_started_at, attempts FROM notification_batches
			WHERE recipient_access_generation_id = ? AND channel = 'email' AND cadence = 'weekly'
			  AND preference_version = ? AND status = 'pending' AND closes_at = ? FOR UPDATE`, accessID, preferenceVersion, due).
			Scan(ctx, &batch.ID, &batch.PublicID, &batch.WindowStartedAt, &attempts)
		if err == nil && attempts == 0 {
			break
		}
		if err == nil {
			windowStartedAt = due
			due = schedule.Next(due)
			if due.IsZero() {
				return ErrNotificationPreference
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		batch = pendingEmailBatch{PublicID: uuid.New(), WindowStartedAt: windowStartedAt}
		if err := tx.NewRaw(`INSERT INTO notification_batches
			(public_id, recipient_access_generation_id, channel, cadence, preference_version, window_started_at, closes_at)
			VALUES (?, ?, 'email', 'weekly', ?, ?, ?) RETURNING id`, batch.PublicID, accessID, preferenceVersion, windowStartedAt, due).
			Scan(ctx, &batch.ID); err != nil {
			return err
		}
		payload, err := json.Marshal(emailBatchJobPayload{BatchID: batch.ID})
		if err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO outbox_events
			(kind, aggregate_kind, aggregate_id, aggregate_version, payload, available_at, created_at)
			VALUES (?, 'notification_batch', ?, 1, ?::jsonb, ?, clock_timestamp())`,
			WeeklyJobKind, batch.PublicID.String(), string(payload), due).Exec(ctx); err != nil {
			return err
		}
		break
	}
	_, err = tx.NewRaw(`INSERT INTO notification_batch_items
		(batch_id, recipient_access_generation_id, kind, `+spec.sourceColumn+`, activity_created_at)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`, batch.ID, accessID, kind, sourceID, activityAt).Exec(ctx)
	return err
}

// HandleWeekly reauthorizes a weekly digest before preview loading and through SMTP acceptance.
func (s *Service) HandleWeekly(ctx context.Context, job worker.Job) error {
	var payload emailBatchJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.BatchID <= 0 {
		return worker.Permanent("invalid_weekly_email_job")
	}
	if !s.Configured() {
		return worker.Permanent("optional_email_disabled")
	}
	assembled, err := s.assembleWeekly(ctx, payload.BatchID, false)
	if err != nil {
		return err
	}
	loaded := make(map[weeklyPreview]smtp.EmbeddedImage, len(assembled.Previews))
	for index, candidate := range assembled.Previews {
		if image := s.loadSafePreview(ctx, candidate.AssetID); image != nil {
			image.ContentID = fmt.Sprintf("memento-preview-%d", index+1)
			loaded[candidate] = *image
		}
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
		var status, cadence string
		var attempts int
		var closesAt time.Time
		if err := tx.NewRaw(`SELECT recipient_access_generation_id, public_id, status, cadence, attempts, closes_at
			FROM notification_batches WHERE id = ? FOR UPDATE`, payload.BatchID).
			Scan(ctx, &accessID, &publicID, &status, &cadence, &attempts, &closesAt); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return worker.Permanent("weekly_email_batch_missing")
			}
			return err
		}
		if status != "pending" {
			return nil
		}
		if cadence != "weekly" {
			return worker.Permanent("weekly_email_batch_invalid")
		}
		if err := s.requireLease(ctx, job); err != nil {
			return err
		}
		if err := lockRecipientGeneration(ctx, tx, accessID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		current, err := s.assembleWeeklyIn(ctx, tx, payload.BatchID, true)
		if err != nil {
			return err
		}
		if current.Empty {
			_, err := tx.NewRaw(`UPDATE notification_batches
				SET status = 'suppressed', last_safe_error = NULL, updated_at = clock_timestamp()
				WHERE id = ? AND status = 'pending'`, payload.BatchID).Exec(ctx)
			return err
		}
		embedded := make([]smtp.EmbeddedImage, 0, maxWeeklyPreviews)
		for _, candidate := range current.Previews {
			if image, ok := loaded[candidate]; ok {
				embedded = append(embedded, image)
			}
		}
		deliveryAttempts = attempts
		if s.cfg.RetryWindow > 0 && time.Since(closesAt) >= s.cfg.RetryWindow {
			terminalDiagnostic, terminalFailure = "retry_window_exhausted", true
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
			ID: publicID.String(), To: current.Recipient, Subject: "Your weekly Memento digest",
			Body:           current.Body + "\n\nManage optional email or unsubscribe: " + unsubscribe.URL,
			UnsubscribeURL: unsubscribe.URL, EmbeddedImages: embedded,
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
			status, terminalFailure = "failed", true
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
					status, terminalDiagnostic, terminalFailure = "failed", "retry_window_exhausted", true
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

func (s *Service) assembleWeekly(ctx context.Context, batchID int64, lock bool) (assembledWeekly, error) {
	var result assembledWeekly
	err := s.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var err error
		result, err = s.assembleWeeklyIn(ctx, tx, batchID, lock)
		return err
	})
	return result, err
}

func (s *Service) assembleWeeklyIn(ctx context.Context, db bun.IDB, batchID int64, lock bool) (assembledWeekly, error) {
	lockClause := ""
	if lock {
		lockClause = " FOR SHARE OF access, person, preference, email"
	}
	var accessID uuid.UUID
	var recipient string
	var truncated bool
	err := db.NewRaw(`SELECT access.id, email.email, batch.truncated
		FROM notification_batches AS batch
		JOIN system_settings AS settings ON settings.id = 1 AND NOT settings.recovery_hold
		JOIN recipient_access_generations AS access
		  ON access.id = batch.recipient_access_generation_id
		 AND access.is_current AND access.state = 'completed'
		JOIN people AS person
		  ON person.id = access.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
		JOIN notification_preferences AS preference
		  ON preference.recipient_access_generation_id = access.id AND preference.email_preference = 'weekly'
		 AND preference.preference_version = batch.preference_version
		JOIN recipient_emails AS email
		  ON email.recipient_access_generation_id = access.id AND email.is_current
		WHERE batch.id = ? AND batch.cadence = 'weekly' AND batch.status = 'pending'`+lockClause,
		batchID).Scan(ctx, &accessID, &recipient, &truncated)
	if errors.Is(err, sql.ErrNoRows) {
		return assembledWeekly{Empty: true}, nil
	}
	if err != nil {
		return assembledWeekly{}, err
	}
	previewAllowed, err := loadPreviewAcknowledgment(ctx, db, accessID, lock)
	if err != nil {
		return assembledWeekly{}, err
	}
	var items []batchItem
	if err := db.NewRaw(`SELECT id, kind, publication_id, comment_id, activity_created_at
		FROM notification_batch_items WHERE batch_id = ? ORDER BY activity_created_at, id
		LIMIT ?`, batchID, maxEmailBatchItems+1).Scan(ctx, &items); err != nil {
		return assembledWeekly{}, err
	}
	if len(items) > maxEmailBatchItems {
		items, truncated = items[:maxEmailBatchItems], true
	}
	result := assembledWeekly{Recipient: recipient}
	lines := make([]string, 0, len(items)+1)
	lineBytes := 0
	seenPreviews := make(map[uuid.UUID]struct{})
	for _, item := range items {
		var line string
		var mediaID, assetID uuid.UUID
		var survives bool
		switch item.Kind {
		case publicationBatchItem:
			if item.PublicationID == nil {
				return assembledWeekly{}, fmt.Errorf("%w: item %d has no publication_id", errMalformedEmailItem, item.ID)
			}
			line, mediaID, assetID, survives, err = assemblePublication(ctx, db, accessID, *item.PublicationID)
		case commentBatchItem:
			if item.CommentID == nil {
				return assembledWeekly{}, fmt.Errorf("%w: item %d has no comment_id", errMalformedEmailItem, item.ID)
			}
			var comment emailComment
			comment, survives, err = loadEmailComment(ctx, db, accessID, *item.CommentID, "weekly", lock)
			if survives {
				line, mediaID, assetID = comment.Author+" commented on an item you can access.", comment.MediaID, comment.AssetID
			}
		default:
			return assembledWeekly{}, fmt.Errorf("%w: %q", errUnsupportedEmailItemKind, item.Kind)
		}
		if err != nil {
			return assembledWeekly{}, err
		}
		if !survives {
			continue
		}
		if lineBytes+len(line)+1 > emailBodyLineBudget {
			truncated = true
			break
		}
		lines, lineBytes = append(lines, line), lineBytes+len(line)+1
		if previewAllowed && len(result.Previews) < maxWeeklyPreviews {
			if item.Kind == publicationBatchItem {
				candidates, err := weeklyPublicationPreviews(ctx, db, accessID, *item.PublicationID, maxWeeklyPreviews-len(result.Previews))
				if err != nil {
					return assembledWeekly{}, err
				}
				for _, candidate := range candidates {
					if _, exists := seenPreviews[candidate.MediaID]; exists {
						continue
					}
					seenPreviews[candidate.MediaID] = struct{}{}
					result.Previews = append(result.Previews, candidate)
				}
			} else if mediaID != uuid.Nil {
				if _, exists := seenPreviews[mediaID]; !exists {
					seenPreviews[mediaID] = struct{}{}
					result.Previews = append(result.Previews, weeklyPreview{MediaID: mediaID, AssetID: assetID})
				}
			}
		}
	}
	if len(lines) == 0 {
		return assembledWeekly{Empty: true}, nil
	}
	if truncated {
		lines = append(lines, "Additional activity is available in Memento.")
	}
	result.Body = "Your weekly Memento activity is ready:\n\n" + strings.Join(lines, "\n") +
		"\n\nThis digest includes only activity you can currently access. It excludes hidden item counts and Moment details. Sign in to Memento to view it."
	return result, nil
}

func weeklyPublicationPreviews(ctx context.Context, db bun.IDB, accessID, publicationID uuid.UUID, limit int) ([]weeklyPreview, error) {
	if limit <= 0 {
		return nil, nil
	}
	var previews []weeklyPreview
	err := db.NewRaw(`WITH bounded_candidates AS MATERIALIZED (
			SELECT candidate.media_item_id
			FROM publication_notification_media AS candidate
			WHERE candidate.publication_id = ? AND candidate.recipient_access_generation_id = ?
			ORDER BY candidate.media_item_id
			LIMIT ?
		)
		SELECT candidate.media_item_id AS media_id, media.immich_asset_id AS asset_id
		FROM publications AS source
		JOIN bounded_candidates AS candidate ON true
		JOIN current_audience_entitlements AS entitlement
		  ON entitlement.event_id = source.event_id
		 AND entitlement.recipient_access_generation_id = ?
		 AND entitlement.media_item_id = candidate.media_item_id
		JOIN current_published_placements AS placement
		  ON placement.event_id = entitlement.event_id AND placement.media_item_id = entitlement.media_item_id
		JOIN published_moments AS moment ON moment.id = placement.published_moment_id
		JOIN media_items AS media ON media.id = candidate.media_item_id AND media.availability = 'current'
		WHERE source.id = ? AND source.notify_recipients
		  AND NOT content_is_withdrawn(placement.event_id, moment.draft_moment_id, placement.media_item_id)
		GROUP BY candidate.media_item_id, media.immich_asset_id
		ORDER BY min(placement.position), candidate.media_item_id LIMIT ?`,
		publicationID, accessID, maxEmailPublicationMedia+1, accessID, publicationID, limit).
		Scan(ctx, &previews)
	return previews, err
}
