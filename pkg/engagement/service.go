// Package engagement records disclosed, meaningful Recipient use for Curator-only inspection.
package engagement

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/mediaaccess"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/uptrace/bun"
)

const (
	RetentionJobKind = "retain_engagement"

	KindVisit                         = "visit"
	KindDestinationOpened             = "destination_opened"
	KindEventOpened                   = "event_opened"
	KindMediaOpened                   = "media_opened"
	KindVideoStarted                  = "video_started"
	KindOriginalDownloadStarted       = "original_download_started"
	KindArchiveDownloadStarted        = "archive_download_started"
	KindCommentCreated                = "comment_created"
	KindFavoriteAdded                 = "favorite_added"
	KindFavoriteRemoved               = "favorite_removed"
	KindInvitationSuggestionSubmitted = "invitation_suggestion_submitted"
	KindInvitationSuggestionWithdrawn = "invitation_suggestion_withdrawn"

	visitWindow        = 30 * time.Minute
	retentionInterval  = 24 * time.Hour
	retentionBatchSize = 1000
)

var (
	ErrInvalid       = errors.New("engagement event is invalid")
	ErrNotFound      = errors.New("engagement target not found")
	ErrInvalidCursor = errors.New("engagement cursor is invalid")
)

var browserKinds = map[string]struct{}{
	KindVisit: {}, KindDestinationOpened: {}, KindEventOpened: {}, KindMediaOpened: {},
	KindVideoStarted: {}, KindOriginalDownloadStarted: {},
}

var destinations = map[string]struct{}{
	"photos": {}, "events": {}, "favorites": {}, "search": {},
}

// BrowserEventRequest is an explicit visible-document signal. Incidental GETs never create one.
type BrowserEventRequest struct {
	Kind            string  `json:"kind" validate:"required"`
	ClientClaimID   string  `json:"client_claim_id" validate:"required,uuid"`
	Destination     *string `json:"destination" tstype:"string | null,required"`
	EventID         *string `json:"event_id" tstype:"string | null,required"`
	MediaItemID     *string `json:"media_item_id" tstype:"string | null,required"`
	DocumentVisible bool    `json:"document_visible"`
}

// ActiveDays reports UTC calendar-day activity over fixed windows.
type ActiveDays struct {
	Days7  int `json:"days_7"`
	Days30 int `json:"days_30"`
	Days90 int `json:"days_90"`
}

// VisitFrequency reports coalesced meaningful visits in the last 30 days.
type VisitFrequency struct {
	Visits30Days         int     `json:"visits_30_days"`
	ActiveVisitDays30    int     `json:"active_visit_days_30"`
	VisitsPerActiveDay30 float64 `json:"visits_per_active_day_30"`
}

// Counts reports meaningful action totals over the last 90 UTC days.
type Counts struct {
	EventOpens            int `json:"event_opens"`
	MediaOpens            int `json:"media_opens"`
	VideoStarts           int `json:"video_starts"`
	Downloads             int `json:"downloads"`
	Comments              int `json:"comments"`
	FavoriteChanges       int `json:"favorite_changes"`
	InvitationSuggestions int `json:"invitation_suggestions"`
}

// TimelineItem is a safe detail row without Comment bodies, suggestion text, secrets, or source metadata.
type TimelineItem struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	TargetKind  *string   `json:"target_kind" tstype:"string | null,required"`
	TargetID    *string   `json:"target_id" tstype:"string | null,required"`
	TargetLabel *string   `json:"target_label" tstype:"string | null,required"`
	OccurredAt  time.Time `json:"occurred_at"`
}

// RecipientDetail is visible only through Curator authorization.
type RecipientDetail struct {
	RecipientPersonID        string         `json:"recipient_person_id"`
	LatestMeaningfulActivity *time.Time     `json:"latest_meaningful_activity_at" tstype:"string | null,required"`
	ActiveDays               ActiveDays     `json:"active_days"`
	VisitFrequency           VisitFrequency `json:"visit_frequency"`
	Counts90Days             Counts         `json:"counts_90_days"`
	Timeline                 []TimelineItem `json:"timeline"`
	NextCursor               *string        `json:"next_cursor" tstype:"string | null,required"`
}

type recipientCursor struct {
	OccurredAt time.Time `json:"t"`
	ID         int64     `json:"i"`
}

// MediaOpener is based only on an explicit Media viewer action.
type MediaOpener struct {
	RecipientPersonID string    `json:"recipient_person_id"`
	RecipientName     string    `json:"recipient_name"`
	OpenCount         int       `json:"open_count"`
	FirstOpenedAt     time.Time `json:"first_opened_at"`
	LatestOpenedAt    time.Time `json:"latest_opened_at"`
}

// MediaOpenersResponse contains no inferred thumbnail or preview views.
type MediaOpenersResponse struct {
	MediaItemID string        `json:"media_item_id"`
	Openers     []MediaOpener `json:"openers"`
}

// Option configures deterministic service boundaries.
type Option func(*Service)

// WithClock injects UTC retention and aggregation time.
func WithClock(now func() time.Time) Option { return func(service *Service) { service.now = now } }

// Service owns detailed engagement, indefinite aggregate updates, and retention.
type Service struct {
	db  *bun.DB
	now func() time.Time
}

func New(db *bun.DB, options ...Option) *Service {
	service := &Service{db: db, now: time.Now}
	for _, option := range options {
		option(service)
	}
	return service
}

// RecordBrowserEvent accepts only explicit visible-document actions from a non-Curator Recipient.
func (s *Service) RecordBrowserEvent(ctx context.Context, actor setup.SessionActor, request BrowserEventRequest) error {
	if actor.Curator || !request.DocumentVisible {
		if actor.Curator {
			return ErrNotFound
		}
		return ErrInvalid
	}
	claimID, err := uuid.Parse(request.ClientClaimID)
	if err != nil || claimID == uuid.Nil {
		return ErrInvalid
	}
	if _, allowed := browserKinds[request.Kind]; !allowed {
		return ErrInvalid
	}
	destination, eventID, mediaID, err := parseBrowserTarget(request)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var locked uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM sessions WHERE id = ? FOR UPDATE`, actor.SessionID).Scan(ctx, &locked); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		current, err := setup.CurrentRecipientSession(ctx, tx, actor)
		if err != nil {
			return err
		}
		if !current {
			return ErrNotFound
		}
		if eventID != nil {
			authorized, err := recipientCanOpenEvent(ctx, tx, actor, *eventID)
			if err != nil {
				return err
			}
			if !authorized {
				return ErrNotFound
			}
		}
		if mediaID != nil {
			if err := mediaaccess.Require(ctx, tx, actor, *mediaID); err != nil {
				if errors.Is(err, mediaaccess.ErrNotFound) {
					return ErrNotFound
				}
				return err
			}
		}
		if request.Kind == KindVisit {
			var recent bool
			if err := tx.NewRaw(`SELECT EXISTS (
				SELECT 1 FROM engagement_events
				WHERE session_id = ? AND kind = ? AND occurred_at > ?
			)`, actor.SessionID, KindVisit, now.Add(-visitWindow)).Scan(ctx, &recent); err != nil {
				return err
			}
			if recent {
				return nil
			}
		}
		inserted, err := insertEvent(ctx, tx, eventRecord{
			Actor: actor, Kind: request.Kind, Destination: destination, EventID: eventID,
			MediaID: mediaID, ClientClaimID: &claimID, OccurredAt: now,
		})
		if err != nil || !inserted {
			return err
		}
		return incrementAggregate(ctx, tx, actor.PersonID, request.Kind, now)
	})
}

func parseBrowserTarget(request BrowserEventRequest) (*string, *uuid.UUID, *uuid.UUID, error) {
	var eventID, mediaID *uuid.UUID
	if request.EventID != nil {
		parsed, err := uuid.Parse(*request.EventID)
		if err != nil || parsed == uuid.Nil {
			return nil, nil, nil, ErrInvalid
		}
		eventID = &parsed
	}
	if request.MediaItemID != nil {
		parsed, err := uuid.Parse(*request.MediaItemID)
		if err != nil || parsed == uuid.Nil {
			return nil, nil, nil, ErrInvalid
		}
		mediaID = &parsed
	}
	switch request.Kind {
	case KindVisit:
		if request.Destination != nil || eventID != nil || mediaID != nil {
			return nil, nil, nil, ErrInvalid
		}
	case KindDestinationOpened:
		if request.Destination == nil || eventID != nil || mediaID != nil {
			return nil, nil, nil, ErrInvalid
		}
		if _, valid := destinations[*request.Destination]; !valid {
			return nil, nil, nil, ErrInvalid
		}
	case KindEventOpened:
		if request.Destination != nil || eventID == nil || mediaID != nil {
			return nil, nil, nil, ErrInvalid
		}
	case KindMediaOpened, KindVideoStarted, KindOriginalDownloadStarted:
		if request.Destination != nil || eventID != nil || mediaID == nil {
			return nil, nil, nil, ErrInvalid
		}
	default:
		return nil, nil, nil, ErrInvalid
	}
	return request.Destination, eventID, mediaID, nil
}

func recipientCanOpenEvent(ctx context.Context, db bun.IDB, actor setup.SessionActor, eventID uuid.UUID) (bool, error) {
	var authorized bool
	err := db.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM current_audience_entitlements AS entitlement
		JOIN events AS event ON event.id = entitlement.event_id AND event.lifecycle = 'published'
		JOIN current_published_placements AS placement
		  ON placement.event_id = entitlement.event_id
		 AND placement.publication_id = entitlement.publication_id
		 AND placement.media_item_id = entitlement.media_item_id
		JOIN published_moments AS moment ON moment.id = placement.published_moment_id
		WHERE entitlement.event_id = ? AND entitlement.recipient_access_generation_id = ?
		  AND NOT content_is_withdrawn(placement.event_id, moment.draft_moment_id, placement.media_item_id)
	)`, eventID, actor.AccessID).Scan(ctx, &authorized)
	return authorized, err
}

type eventRecord struct {
	Actor         setup.SessionActor
	Kind          string
	Destination   *string
	EventID       *uuid.UUID
	MediaID       *uuid.UUID
	ClientClaimID *uuid.UUID
	OriginKey     *string
	OccurredAt    time.Time
}

func insertEvent(ctx context.Context, tx bun.Tx, record eventRecord) (bool, error) {
	var id int64
	err := tx.NewRaw(`INSERT INTO engagement_events
		(recipient_person_id, recipient_access_generation_id, session_id, kind, destination,
		 event_id, media_item_id, client_claim_id, origin_key, occurred_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING RETURNING id`, record.Actor.PersonID, record.Actor.AccessID,
		record.Actor.SessionID, record.Kind, record.Destination, record.EventID, record.MediaID,
		record.ClientClaimID, record.OriginKey, record.OccurredAt).Scan(ctx, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func incrementAggregate(ctx context.Context, tx bun.Tx, personID uuid.UUID, kind string, occurredAt time.Time) error {
	_, err := tx.NewRaw(`INSERT INTO engagement_daily_aggregates
		(recipient_person_id, activity_date, kind, event_count, first_occurred_at, last_occurred_at)
		VALUES (?, ?::date, ?, 1, ?, ?)
		ON CONFLICT (recipient_person_id, activity_date, kind) DO UPDATE
		SET event_count = engagement_daily_aggregates.event_count + 1,
			first_occurred_at = LEAST(engagement_daily_aggregates.first_occurred_at, EXCLUDED.first_occurred_at),
			last_occurred_at = GREATEST(engagement_daily_aggregates.last_occurred_at, EXCLUDED.last_occurred_at)`,
		personID, occurredAt.UTC().Format(time.DateOnly), kind, occurredAt, occurredAt).Exec(ctx)
	return err
}

func (s *Service) recordServerEvent(ctx context.Context, tx bun.Tx, record eventRecord) error {
	if record.Actor.Curator {
		return nil
	}
	current, err := setup.CurrentRecipientSession(ctx, tx, record.Actor)
	if err != nil {
		return err
	}
	if !current {
		return ErrNotFound
	}
	inserted, err := insertEvent(ctx, tx, record)
	if err != nil || !inserted {
		return err
	}
	return incrementAggregate(ctx, tx, record.Actor.PersonID, record.Kind, record.OccurredAt)
}

// RecordComment records a successful Comment creation in its domain transaction.
func (s *Service) RecordComment(ctx context.Context, tx bun.Tx, actor setup.SessionActor, commentID, mediaID uuid.UUID, occurredAt time.Time) error {
	origin := "comment:" + commentID.String()
	return s.recordServerEvent(ctx, tx, eventRecord{Actor: actor, Kind: KindCommentCreated, MediaID: &mediaID, OriginKey: &origin, OccurredAt: occurredAt.UTC()})
}

// RecordFavorite records only a real Favorite state transition.
func (s *Service) RecordFavorite(ctx context.Context, tx bun.Tx, actor setup.SessionActor, mediaID uuid.UUID, action string, occurredAt time.Time) error {
	kind := KindFavoriteRemoved
	if action == "added" {
		kind = KindFavoriteAdded
	} else if action != "removed" {
		return ErrInvalid
	}
	origin := fmt.Sprintf("favorite:%s:%s:%s:%d", actor.PersonID, mediaID, action, occurredAt.UnixNano())
	return s.recordServerEvent(ctx, tx, eventRecord{Actor: actor, Kind: kind, MediaID: &mediaID, OriginKey: &origin, OccurredAt: occurredAt.UTC()})
}

// RecordSuggestion records a submit or withdraw without retaining suggestion text or email.
func (s *Service) RecordSuggestion(ctx context.Context, tx bun.Tx, actor setup.SessionActor, suggestionID uuid.UUID, action string, occurredAt time.Time) error {
	kind := KindInvitationSuggestionSubmitted
	if action == "withdrawn" {
		kind = KindInvitationSuggestionWithdrawn
	} else if action != "submitted" {
		return ErrInvalid
	}
	origin := "suggestion:" + action + ":" + suggestionID.String()
	return s.recordServerEvent(ctx, tx, eventRecord{Actor: actor, Kind: kind, OriginKey: &origin, OccurredAt: occurredAt.UTC()})
}

// RecordArchiveDownload records the final authorized, single-use part claim.
func (s *Service) RecordArchiveDownload(ctx context.Context, tx bun.Tx, actor setup.SessionActor, partID uuid.UUID, eventID *uuid.UUID, occurredAt time.Time) error {
	origin := "archive-part:" + partID.String()
	return s.recordServerEvent(ctx, tx, eventRecord{Actor: actor, Kind: KindArchiveDownloadStarted, EventID: eventID, OriginKey: &origin, OccurredAt: occurredAt.UTC()})
}

// DeleteExpiredDetails removes only rows strictly older than one year, in bounded batches.
func (s *Service) DeleteExpiredDetails(ctx context.Context) (int64, error) {
	cutoff := s.now().UTC().AddDate(-1, 0, 0)
	result, err := s.db.NewRaw(`WITH expired AS (
		SELECT id FROM engagement_events WHERE occurred_at < ?
		ORDER BY occurred_at, id FOR UPDATE SKIP LOCKED LIMIT ?
	)
	DELETE FROM engagement_events AS event USING expired WHERE event.id = expired.id`, cutoff, retentionBatchSize).Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// HandleRetentionJob keeps detailed rows for one year while aggregates remain indefinite.
func (s *Service) HandleRetentionJob(ctx context.Context, _ worker.Job) error {
	deleted, err := s.DeleteExpiredDetails(ctx)
	if err != nil {
		return err
	}
	if deleted == retentionBatchSize {
		return worker.RescheduleAfter(0)
	}
	return worker.RescheduleAfter(retentionInterval)
}

// Recipient returns safe metrics and recent details for one Recipient Person.
func (s *Service) GetRecipientEngagement(ctx context.Context, personID uuid.UUID, encodedCursor string, limit int) (RecipientDetail, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		return RecipientDetail{}, ErrInvalidCursor
	}
	cursor, err := decodeRecipientCursor(encodedCursor)
	if err != nil {
		return RecipientDetail{}, err
	}
	response := RecipientDetail{RecipientPersonID: personID.String(), Timeline: make([]TimelineItem, 0)}
	var exists bool
	if err := s.db.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM person_roles WHERE person_id = ? AND role = 'recipient'
	)`, personID).Scan(ctx, &exists); err != nil {
		return RecipientDetail{}, err
	}
	if !exists {
		return RecipientDetail{}, ErrNotFound
	}
	now := s.now().UTC()
	if err := s.db.NewRaw(`SELECT max(last_occurred_at) FROM engagement_daily_aggregates WHERE recipient_person_id = ?`, personID).Scan(ctx, &response.LatestMeaningfulActivity); err != nil {
		return RecipientDetail{}, err
	}
	if err := s.db.NewRaw(`SELECT
		count(DISTINCT activity_date) FILTER (WHERE activity_date >= ?::date),
		count(DISTINCT activity_date) FILTER (WHERE activity_date >= ?::date),
		count(DISTINCT activity_date) FILTER (WHERE activity_date >= ?::date)
		FROM engagement_daily_aggregates WHERE recipient_person_id = ?`,
		now.AddDate(0, 0, -6).Format(time.DateOnly), now.AddDate(0, 0, -29).Format(time.DateOnly),
		now.AddDate(0, 0, -89).Format(time.DateOnly), personID).Scan(ctx,
		&response.ActiveDays.Days7, &response.ActiveDays.Days30, &response.ActiveDays.Days90); err != nil {
		return RecipientDetail{}, err
	}
	if err := s.db.NewRaw(`SELECT COALESCE(sum(event_count), 0), count(*)
		FROM engagement_daily_aggregates WHERE recipient_person_id = ? AND kind = ? AND activity_date >= ?::date`,
		personID, KindVisit, now.AddDate(0, 0, -29).Format(time.DateOnly)).Scan(ctx,
		&response.VisitFrequency.Visits30Days, &response.VisitFrequency.ActiveVisitDays30); err != nil {
		return RecipientDetail{}, err
	}
	if response.VisitFrequency.ActiveVisitDays30 > 0 {
		response.VisitFrequency.VisitsPerActiveDay30 = float64(response.VisitFrequency.Visits30Days) / float64(response.VisitFrequency.ActiveVisitDays30)
	}
	if err := s.db.NewRaw(`SELECT
		COALESCE(sum(event_count) FILTER (WHERE kind = ?), 0),
		COALESCE(sum(event_count) FILTER (WHERE kind = ?), 0),
		COALESCE(sum(event_count) FILTER (WHERE kind = ?), 0),
		COALESCE(sum(event_count) FILTER (WHERE kind IN (?, ?)), 0),
		COALESCE(sum(event_count) FILTER (WHERE kind = ?), 0),
		COALESCE(sum(event_count) FILTER (WHERE kind IN (?, ?)), 0),
		COALESCE(sum(event_count) FILTER (WHERE kind IN (?, ?)), 0)
		FROM engagement_daily_aggregates
		WHERE recipient_person_id = ? AND activity_date >= ?::date`,
		KindEventOpened, KindMediaOpened, KindVideoStarted, KindOriginalDownloadStarted, KindArchiveDownloadStarted,
		KindCommentCreated, KindFavoriteAdded, KindFavoriteRemoved,
		KindInvitationSuggestionSubmitted, KindInvitationSuggestionWithdrawn,
		personID, now.AddDate(0, 0, -89).Format(time.DateOnly)).Scan(ctx,
		&response.Counts90Days.EventOpens, &response.Counts90Days.MediaOpens, &response.Counts90Days.VideoStarts,
		&response.Counts90Days.Downloads, &response.Counts90Days.Comments, &response.Counts90Days.FavoriteChanges,
		&response.Counts90Days.InvitationSuggestions); err != nil {
		return RecipientDetail{}, err
	}
	type row struct {
		ID         int64
		Kind       string
		EventID    *uuid.UUID
		MediaID    *uuid.UUID `bun:"media_item_id"`
		EventTitle *string
		OccurredAt time.Time
	}
	var rows []row
	cursorTime, cursorID := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC), int64(math.MaxInt64)
	if cursor != nil {
		cursorTime, cursorID = cursor.OccurredAt, cursor.ID
	}
	if err := s.db.NewRaw(`SELECT engagement.id, engagement.kind, engagement.event_id,
		engagement.media_item_id, event.title AS event_title, engagement.occurred_at
		FROM engagement_events AS engagement
		LEFT JOIN events AS event ON event.id = engagement.event_id
		WHERE engagement.recipient_person_id = ?
		  AND (engagement.occurred_at, engagement.id) < (?, ?)
		ORDER BY engagement.occurred_at DESC, engagement.id DESC LIMIT ?`, personID, cursorTime, cursorID, limit+1).Scan(ctx, &rows); err != nil {
		return RecipientDetail{}, err
	}
	if len(rows) > limit {
		last := rows[limit-1]
		encoded := encodeRecipientCursor(recipientCursor{OccurredAt: last.OccurredAt, ID: last.ID})
		response.NextCursor = &encoded
		rows = rows[:limit]
	}
	for _, row := range rows {
		item := TimelineItem{ID: strconv.FormatInt(row.ID, 10), Kind: row.Kind, OccurredAt: row.OccurredAt}
		if row.EventID != nil {
			kind, id := "event", row.EventID.String()
			item.TargetKind, item.TargetID, item.TargetLabel = &kind, &id, row.EventTitle
		} else if row.MediaID != nil {
			kind, id, label := "media", row.MediaID.String(), "Media item"
			item.TargetKind, item.TargetID, item.TargetLabel = &kind, &id, &label
		}
		response.Timeline = append(response.Timeline, item)
	}
	return response, nil
}

func encodeRecipientCursor(cursor recipientCursor) string {
	value, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(value)
}

func decodeRecipientCursor(raw string) (*recipientCursor, error) {
	if raw == "" {
		return nil, nil
	}
	value, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var cursor recipientCursor
	if err := decoder.Decode(&cursor); err != nil {
		return nil, ErrInvalidCursor
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || cursor.OccurredAt.IsZero() || cursor.ID <= 0 {
		return nil, ErrInvalidCursor
	}
	cursor.OccurredAt = cursor.OccurredAt.UTC()
	return &cursor, nil
}

// MediaOpeners lists only Recipients who explicitly opened the Media viewer.
func (s *Service) MediaOpeners(ctx context.Context, mediaID uuid.UUID) (MediaOpenersResponse, error) {
	response := MediaOpenersResponse{MediaItemID: mediaID.String(), Openers: make([]MediaOpener, 0)}
	var exists bool
	if err := s.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM media_items WHERE id = ?)`, mediaID).Scan(ctx, &exists); err != nil {
		return MediaOpenersResponse{}, err
	}
	if !exists {
		return MediaOpenersResponse{}, ErrNotFound
	}
	err := s.db.NewRaw(`SELECT engagement.recipient_person_id, person.display_name AS recipient_name,
		count(*)::integer AS open_count, min(engagement.occurred_at) AS first_opened_at,
		max(engagement.occurred_at) AS latest_opened_at
		FROM engagement_events AS engagement
		JOIN people AS person ON person.id = engagement.recipient_person_id
		WHERE engagement.kind = ? AND engagement.media_item_id = ?
		GROUP BY engagement.recipient_person_id, person.display_name
		ORDER BY latest_opened_at DESC, engagement.recipient_person_id`, KindMediaOpened, mediaID).Scan(ctx, &response.Openers)
	return response, err
}
