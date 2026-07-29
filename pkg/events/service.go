// Package events owns Event drafts, atomic Publication, and current Recipient projections.
package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	// Embed IANA timezone data for the minimal production image.
	_ "time/tzdata"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/staging"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

const (
	maxDraftSourceAlbums = 100
	maxDraftMediaItems   = 100000
)

var (
	ErrNotFound           = errors.New("draft not found")
	ErrInvalid            = errors.New("draft request is invalid")
	ErrPlaceLabelsInvalid = errors.New("place labels are invalid")
	ErrSourceUnavailable  = errors.New("source album is unavailable for drafting")
	ErrSourceTooLarge     = errors.New("source album has too many media items")
	ErrMediaUnavailable   = errors.New("media item is unavailable for drafting")
	ErrNoMediaAvailable   = errors.New("no media items are available for drafting")
	ErrVersionConflict    = errors.New("draft version is stale")
	errUnknownMoment      = errors.New("draft placement references unknown Moment")
)

// CreateEventRequest initializes one portal-owned Event from selected Source material.
type CreateEventRequest struct {
	SourceAlbumIDs []string `json:"source_album_ids" validate:"required,min=1,max=100"`
	MediaItemIDs   []string `json:"media_item_ids,omitempty" validate:"max=100000"`
	Timezone       string   `json:"timezone" validate:"required,max=100"`
	Title          string   `json:"title,omitempty" validate:"max=240" mod:"trim"`
	Description    string   `json:"description,omitempty" validate:"max=2000" mod:"trim"`
}

// CreateLooseItemRequest initializes a private Loose item around an existing Media identity.
type CreateLooseItemRequest struct {
	MediaItemID string `json:"media_item_id" validate:"required"`
	Timezone    string `json:"timezone" validate:"required,max=100"`
	Title       string `json:"title,omitempty" validate:"max=240" mod:"trim"`
	Description string `json:"description,omitempty" validate:"max=2000" mod:"trim"`
}

// MediaItem is the portal-owned, path-free representation used while organizing drafts.
type MediaItem struct {
	ID            string  `json:"id"`
	MediaType     string  `json:"media_type"`
	Width         *int    `json:"width" tstype:"number | null,required"`
	Height        *int    `json:"height" tstype:"number | null,required"`
	LocalDateTime *string `json:"local_date_time" tstype:"string | null,required"`
}

// Moment is an initial local-day proposal. Recipients never receive this draft type.
type Moment struct {
	ID                 string      `json:"id"`
	Title              string      `json:"title"`
	PlaceLabels        []string    `json:"place_labels"`
	ProposedDay        string      `json:"proposed_day"`
	GroupingTimezone   string      `json:"grouping_timezone"`
	SourceDays         []string    `json:"source_days"`
	ProposalKind       string      `json:"proposal_kind"`
	CoverMediaItemID   *string     `json:"cover_media_item_id" tstype:"string | null,required"`
	AttendanceComplete bool        `json:"attendance_complete"`
	AudienceComplete   bool        `json:"audience_complete"`
	MediaItems         []MediaItem `json:"media_items"`
}

// OrganizeMoment is one complete Moment snapshot submitted by the Curator workspace.
type OrganizeMoment struct {
	ID               string   `json:"id" validate:"required"`
	Title            string   `json:"title,omitempty" validate:"max=240" mod:"trim"`
	PlaceLabels      []string `json:"place_labels"`
	ProposedDay      string   `json:"proposed_day" validate:"required"`
	CoverMediaItemID *string  `json:"cover_media_item_id" tstype:"string | null,required"`
	MediaItemIDs     []string `json:"media_item_ids" validate:"required,min=1,max=100000"`
}

// OrganizeEventRequest atomically replaces draft organization at an expected version.
type OrganizeEventRequest struct {
	Version             int64            `json:"version" validate:"required,min=1"`
	Title               *string          `json:"title" tstype:"string"`
	Description         *string          `json:"description" tstype:"string"`
	PlaceLabels         []string         `json:"place_labels"`
	GroupingTimezone    *string          `json:"grouping_timezone" tstype:"string"`
	Moments             []OrganizeMoment `json:"moments" validate:"max=100000,dive"`
	UnassignedMediaIDs  []string         `json:"unassigned_media_ids" validate:"max=100000"`
	FinalReviewComplete bool             `json:"final_review_complete"`
}

// RestorePublishedMediaRequest cancels one safe Media omission at an expected version.
type RestorePublishedMediaRequest struct {
	Version     int64  `json:"version" validate:"required,min=1"`
	MediaItemID string `json:"media_item_id" validate:"required"`
}

// EventSummary supports Curator work navigation without loading every Media item.
type EventSummary struct {
	ID              string    `json:"id"`
	Lifecycle       string    `json:"lifecycle"`
	Title           string    `json:"title"`
	Version         int64     `json:"version"`
	MomentCount     int       `json:"moment_count"`
	UnassignedCount int       `json:"unassigned_count"`
	HasStagedUpdate bool      `json:"has_staged_update"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type EventListResponse struct {
	Events []EventSummary `json:"events"`
}

// SourceMetadataSuggestion reports current Source metadata without changing Event metadata.
type SourceMetadataSuggestion struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// EventSource records private provenance through a stable portal Source identity.
type EventSource struct {
	ID                 string                    `json:"id"`
	MetadataSuggestion *SourceMetadataSuggestion `json:"metadata_suggestion" tstype:"SourceMetadataSuggestion | null,required"`
}

// Event is a portal-owned private draft.
type Event struct {
	ID                                  string             `json:"id"`
	Lifecycle                           string             `json:"lifecycle"`
	Title                               string             `json:"title"`
	Description                         string             `json:"description"`
	PlaceLabels                         []string           `json:"place_labels"`
	GroupingTimezone                    string             `json:"grouping_timezone"`
	Version                             int64              `json:"version"`
	FinalReviewComplete                 bool               `json:"final_review_complete"`
	PublishedEditableVersion            *int64             `json:"published_editable_version" tstype:"number | null,required"`
	PublishedAttendanceRecoveryRequired bool               `json:"published_attendance_recovery_required"`
	StagedUpdate                        *StagedUpdate      `json:"staged_update" tstype:"StagedUpdate | null,required"`
	Sources                             []EventSource      `json:"sources"`
	Moments                             []Moment           `json:"moments"`
	UnassignedMedia                     []MediaItem        `json:"unassigned_media"`
	WithdrawalTargets                   []WithdrawalTarget `json:"withdrawal_targets"`
	Withdrawals                         []Withdrawal       `json:"withdrawals"`
	CreatedAt                           time.Time          `json:"created_at"`
	UpdatedAt                           time.Time          `json:"updated_at"`

	// PendingWithdrawalPublication is server-authoritative because active Withdrawal
	// history alone cannot identify which shared-Media placements are still stale.
	PendingWithdrawalPublication bool `json:"pending_withdrawal_publication"`
}

// LooseItem is a private independently publishable Media draft.
type LooseItem struct {
	ID               string    `json:"id"`
	Lifecycle        string    `json:"lifecycle"`
	Title            string    `json:"title"`
	Description      string    `json:"description"`
	GroupingTimezone string    `json:"grouping_timezone"`
	ProposedDay      *string   `json:"proposed_day" tstype:"string | null,required"`
	Version          int64     `json:"version"`
	AudienceComplete bool      `json:"audience_complete"`
	MediaItem        MediaItem `json:"media_item"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// SourceMediaResponse lists stable Media identities available for selection.
type SourceMediaResponse struct {
	MediaItems []MediaItem `json:"media_items"`
}

// Service persists editable and published Event state without mutating Immich.
type Service struct {
	db                    *bun.DB
	now                   func() time.Time
	failPublicationStep   func(PublicationStep) error
	failWithdrawalStep    func(WithdrawalStep) error
	recipientReadBoundary func()
	publicationHandoff    PublicationHandoff
}

func New(db *bun.DB) *Service {
	return &Service{db: db, now: time.Now}
}

type sourceRecord struct {
	ID          uuid.UUID
	Name        string
	Description string
	Disposition string
	Missing     bool
}

type mediaRecord struct {
	ID            uuid.UUID
	MediaType     string
	Width         *int
	Height        *int
	LocalDateTime *string
	day           *string
	instant       *time.Time
	momentID      uuid.UUID
}

type draftMomentRow struct {
	ID                 uuid.UUID  `json:"id"`
	Position           int        `json:"position"`
	ProposedDay        string     `json:"proposed_day"`
	SourceDays         []string   `json:"source_days,omitempty"`
	ProposalKind       string     `json:"proposal_kind,omitempty"`
	Title              string     `json:"title,omitempty"`
	PlaceLabels        []string   `json:"place_labels,omitempty"`
	CoverMediaItemID   *uuid.UUID `json:"cover_media_item_id,omitempty"`
	AttendanceComplete bool       `json:"attendance_complete,omitempty"`
	AudienceComplete   bool       `json:"audience_complete,omitempty"`
	ReviewVersion      int64      `json:"review_version,omitempty"`
}

type draftPlacementRow struct {
	MediaItemID   uuid.UUID  `json:"media_item_id"`
	DraftMomentID *uuid.UUID `json:"draft_moment_id"`
	Position      int        `json:"position"`
}

type editablePlacementOrder struct {
	MediaItemID       uuid.UUID  `bun:"media_item_id"`
	DraftMomentID     *uuid.UUID `bun:"draft_moment_id"`
	PublishedPosition *int       `bun:"published_position"`
	MomentPosition    *int       `bun:"moment_position"`
}

type priorMomentState struct {
	ID               uuid.UUID
	Position         int
	Title            string
	PlaceLabels      []string
	PlaceLabelsJSON  string `bun:"place_labels_json"`
	ProposedDay      string
	CoverMediaItemID *uuid.UUID
}

type priorPlacementState struct {
	MediaItemID   uuid.UUID
	DraftMomentID *uuid.UUID
	Position      int
}

// CreateEvent creates stable Event and Moment identities in one transaction.
func (s *Service) CreateEvent(ctx context.Context, actor setup.CuratorSession, request CreateEventRequest) (Event, error) {
	location, err := draftLocation(request.Timezone)
	if err != nil {
		return Event{}, ErrInvalid
	}
	sourceIDs, err := parseUniqueIDs(request.SourceAlbumIDs)
	if err != nil || len(sourceIDs) == 0 || len(sourceIDs) > maxDraftSourceAlbums {
		return Event{}, ErrInvalid
	}
	if len(request.MediaItemIDs) > maxDraftMediaItems {
		return Event{}, ErrInvalid
	}
	selectedIDs, err := parseUniqueIDs(request.MediaItemIDs)
	if err != nil {
		return Event{}, ErrInvalid
	}

	eventID := uuid.New()
	now := s.now().UTC()
	var created Event
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		sources, err := lockSources(ctx, tx, sourceIDs)
		if err != nil {
			return err
		}
		media, err := selectMedia(ctx, tx, sourceIDs, selectedIDs)
		if err != nil {
			return err
		}
		if len(media) == 0 {
			return ErrNoMediaAvailable
		}
		prepareProposals(media, location)

		title := strings.TrimSpace(request.Title)
		description := strings.TrimSpace(request.Description)
		if title == "" {
			title = sources[sourceIDs[0]].Name
		}
		if description == "" && len(sourceIDs) == 1 {
			description = sources[sourceIDs[0]].Description
		}
		if title == "" || utf8.RuneCountInString(title) > 240 || utf8.RuneCountInString(description) > 2000 {
			return ErrInvalid
		}
		if _, err := tx.NewRaw(`
			INSERT INTO events (id, title, description, grouping_timezone, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, eventID, title, description, location.String(), now, now).Exec(ctx); err != nil {
			return err
		}
		includeFutureMedia := len(selectedIDs) == 0
		for position, sourceID := range sourceIDs {
			source := sources[sourceID]
			if _, err := tx.NewRaw(`
				INSERT INTO event_sources (
					event_id, source_album_id, source_order, initialized_name,
					initialized_description, initialized_at, include_future_media
				) VALUES (?, ?, ?, ?, ?, ?, ?)
			`, eventID, sourceID, position, source.Name, source.Description, now, includeFutureMedia).Exec(ctx); err != nil {
				return err
			}
		}

		momentByDay := make(map[string]uuid.UUID)
		moments := make([]draftMomentRow, 0)
		for index := range media {
			if media[index].day == nil {
				continue
			}
			momentID, exists := momentByDay[*media[index].day]
			if !exists {
				momentID = uuid.New()
				momentByDay[*media[index].day] = momentID
				moments = append(moments, draftMomentRow{
					ID: momentID, Position: len(moments), ProposedDay: *media[index].day,
					SourceDays: []string{*media[index].day}, ProposalKind: "local_day",
				})
			}
			media[index].momentID = momentID
		}
		if err := insertDraftMoments(ctx, tx, eventID, location.String(), moments); err != nil {
			return err
		}
		placements := make([]draftPlacementRow, 0, len(media))
		for position, item := range media {
			var momentID *uuid.UUID
			if item.momentID != uuid.Nil {
				id := item.momentID
				momentID = &id
			}
			placements = append(placements, draftPlacementRow{
				MediaItemID: item.ID, DraftMomentID: momentID, Position: position,
			})
		}
		if err := insertDraftPlacements(ctx, tx, eventID, now, placements); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`
			UPDATE source_albums SET disposition = 'drafted', ignored_at = NULL,
				version = version + 1, updated_at = ?
			WHERE id IN (?) AND disposition <> 'drafted'
		`, now, bun.List(sourceIDs)).Exec(ctx); err != nil {
			return err
		}
		if err := appendDraftAudit(ctx, tx, actor, "event_draft_created", map[string]any{
			"event_id": eventID.String(), "source_count": len(sourceIDs), "media_count": len(media),
		}); err != nil {
			return err
		}
		created, err = getEvent(ctx, tx, eventID)
		return err
	})
	if err != nil {
		return Event{}, err
	}
	return created, nil
}

func insertDraftMoments(ctx context.Context, tx bun.Tx, eventID uuid.UUID, timezone string, moments []draftMomentRow) error {
	if len(moments) == 0 {
		return nil
	}
	payload, err := json.Marshal(moments)
	if err != nil {
		return err
	}
	_, err = tx.NewRaw(`
		INSERT INTO draft_moments (
			id, event_id, position, proposed_day, grouping_timezone, source_days,
			proposal_kind, title, place_labels, cover_media_item_id, attendance_complete, audience_complete,
			review_version
		)
		SELECT incoming.id, ?, incoming.position, incoming.proposed_day::date, ?,
			COALESCE(incoming.source_days, '{}'::date[]),
			COALESCE(incoming.proposal_kind, 'manual'), COALESCE(incoming.title, ''),
			COALESCE(incoming.place_labels, '{}'::text[]), incoming.cover_media_item_id,
			COALESCE(incoming.attendance_complete, false),
			COALESCE(incoming.audience_complete, false), COALESCE(incoming.review_version, 1)
		FROM jsonb_to_recordset(?::jsonb) AS incoming(
			id uuid, position integer, proposed_day text, source_days date[],
			proposal_kind text, title text, place_labels text[], cover_media_item_id uuid,
			attendance_complete boolean, audience_complete boolean, review_version bigint
		)
	`, eventID, timezone, string(payload)).Exec(ctx)
	return err
}

func insertDraftPlacements(ctx context.Context, tx bun.Tx, eventID uuid.UUID, now time.Time, placements []draftPlacementRow) error {
	payload, err := json.Marshal(placements)
	if err != nil {
		return err
	}
	_, err = tx.NewRaw(`
		INSERT INTO draft_media_placements (event_id, media_item_id, draft_moment_id, position, created_at)
		SELECT ?, incoming.media_item_id, incoming.draft_moment_id, incoming.position, ?
		FROM jsonb_to_recordset(?::jsonb) AS incoming(
			media_item_id uuid, draft_moment_id uuid, position integer
		)
	`, eventID, now, string(payload)).Exec(ctx)
	return err
}

func lockSources(ctx context.Context, tx bun.Tx, sourceIDs []uuid.UUID) (map[uuid.UUID]sourceRecord, error) {
	lockOrder := append([]uuid.UUID(nil), sourceIDs...)
	sort.Slice(lockOrder, func(i, j int) bool { return lockOrder[i].String() < lockOrder[j].String() })
	result := make(map[uuid.UUID]sourceRecord, len(sourceIDs))
	for _, id := range lockOrder {
		var source sourceRecord
		err := tx.NewRaw(`
			SELECT id, name, description, disposition, source_missing
			FROM source_albums WHERE id = ? FOR UPDATE
		`, id).Scan(ctx, &source.ID, &source.Name, &source.Description, &source.Disposition, &source.Missing)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSourceUnavailable
		}
		if err != nil {
			return nil, err
		}
		if source.Missing || source.Disposition == "ignored" {
			return nil, ErrSourceUnavailable
		}
		result[id] = source
	}
	return result, nil
}

func selectMedia(ctx context.Context, tx bun.Tx, sourceIDs, selectedIDs []uuid.UUID) ([]mediaRecord, error) {
	query := `
		SELECT media.id, media.media_type, media.width, media.height, media.local_date_time
		FROM media_items AS media
		WHERE media.availability = 'current'
		  AND EXISTS (
			SELECT 1 FROM source_album_memberships AS membership
			WHERE membership.media_item_id = media.id AND membership.source_album_id IN (?)
		)`
	args := []any{bun.List(sourceIDs)}
	if len(selectedIDs) > 0 {
		query += ` AND media.id IN (?)`
		args = append(args, bun.List(selectedIDs))
	}
	query += ` ORDER BY media.id LIMIT ? FOR NO KEY UPDATE OF media`
	args = append(args, maxDraftMediaItems+1)
	var media []mediaRecord
	if err := tx.NewRaw(query, args...).Scan(ctx, &media); err != nil {
		return nil, err
	}
	if len(selectedIDs) > 0 && len(media) != len(selectedIDs) {
		return nil, ErrMediaUnavailable
	}
	if len(media) > maxDraftMediaItems {
		return nil, ErrInvalid
	}
	return media, nil
}

func prepareProposals(media []mediaRecord, location *time.Location) {
	for index := range media {
		media[index].day, media[index].instant = captureDay(media[index].LocalDateTime, location)
	}
	sort.Slice(media, func(i, j int) bool {
		leftDay, rightDay := media[i].day, media[j].day
		if leftDay == nil || rightDay == nil {
			if leftDay == nil && rightDay == nil {
				return media[i].ID.String() < media[j].ID.String()
			}
			return rightDay == nil
		}
		if *leftDay != *rightDay {
			return *leftDay < *rightDay
		}
		left, right := media[i].instant, media[j].instant
		if left.Equal(*right) {
			return media[i].ID.String() < media[j].ID.String()
		}
		return left.Before(*right)
	})
}

func captureDay(raw *string, location *time.Location) (*string, *time.Time) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, nil
	}
	value := strings.TrimSpace(*raw)
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil && parsed.Year() > 0 {
		day := parsed.Format(time.DateOnly)
		instant := parsed.UTC()
		return &day, &instant
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05", "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, value, location); err == nil && parsed.Year() > 0 {
			day := parsed.Format(time.DateOnly)
			instant := parsed.UTC()
			return &day, &instant
		}
	}
	return nil, nil
}

// ListEvents returns private editable Events ordered by recent activity.
func (s *Service) ListEvents(ctx context.Context) (EventListResponse, error) {
	result := EventListResponse{Events: make([]EventSummary, 0)}
	err := s.db.NewRaw(`
		SELECT event.id, event.lifecycle, event.title, event.version,
			COALESCE(moment.moment_count, 0)::integer AS moment_count,
			COALESCE(placement.unassigned_count, 0)::integer AS unassigned_count,
			(event.current_staged_update_id IS NOT NULL) AS has_staged_update,
			event.updated_at
		FROM events AS event
		LEFT JOIN (
			SELECT event_id, count(*) AS moment_count
			FROM draft_moments GROUP BY event_id
		) AS moment ON moment.event_id = event.id
		LEFT JOIN (
			SELECT event_id, count(*) FILTER (WHERE draft_moment_id IS NULL) AS unassigned_count
			FROM draft_media_placements GROUP BY event_id
		) AS placement ON placement.event_id = event.id
		WHERE event.lifecycle IN ('draft', 'published')
		ORDER BY event.updated_at DESC, event.id
	`).Scan(ctx, &result.Events)
	return result, err
}

// OrganizeEvent atomically saves one complete organization snapshot.
func (s *Service) OrganizeEvent(ctx context.Context, actor setup.CuratorSession, id uuid.UUID, request OrganizeEventRequest) (Event, error) {
	if request.Version < 1 || len(request.Moments) > maxDraftMediaItems || len(request.UnassignedMediaIDs) > maxDraftMediaItems {
		return Event{}, ErrInvalid
	}
	momentRows := make([]draftMomentRow, 0, len(request.Moments))
	placements := make([]draftPlacementRow, 0)
	seenMoments := make(map[uuid.UUID]struct{}, len(request.Moments))
	seenMedia := make(map[uuid.UUID]struct{})
	for position, moment := range request.Moments {
		momentID, err := uuid.Parse(moment.ID)
		if err != nil || momentID == uuid.Nil {
			return Event{}, ErrInvalid
		}
		if _, duplicate := seenMoments[momentID]; duplicate {
			return Event{}, ErrInvalid
		}
		seenMoments[momentID] = struct{}{}
		placeLabels, valid := normalizePlaceLabels(moment.PlaceLabels)
		if !valid {
			return Event{}, ErrPlaceLabelsInvalid
		}
		if _, err := time.Parse(time.DateOnly, moment.ProposedDay); err != nil || utf8.RuneCountInString(strings.TrimSpace(moment.Title)) > 240 {
			return Event{}, ErrInvalid
		}
		mediaIDs, err := parseUniqueIDs(moment.MediaItemIDs)
		if err != nil || len(mediaIDs) == 0 {
			return Event{}, ErrInvalid
		}
		var coverID *uuid.UUID
		if moment.CoverMediaItemID != nil {
			parsed, err := uuid.Parse(*moment.CoverMediaItemID)
			if err != nil || parsed == uuid.Nil || !containsUUID(mediaIDs, parsed) {
				return Event{}, ErrInvalid
			}
			coverID = &parsed
		}
		momentRows = append(momentRows, draftMomentRow{
			ID: momentID, Position: position, ProposedDay: moment.ProposedDay,
			Title: strings.TrimSpace(moment.Title), PlaceLabels: placeLabels, CoverMediaItemID: coverID,
		})
		for _, mediaID := range mediaIDs {
			if _, duplicate := seenMedia[mediaID]; duplicate {
				return Event{}, ErrInvalid
			}
			seenMedia[mediaID] = struct{}{}
			momentCopy := momentID
			placements = append(placements, draftPlacementRow{MediaItemID: mediaID, DraftMomentID: &momentCopy, Position: len(placements)})
		}
	}
	unassigned, err := parseUniqueIDs(request.UnassignedMediaIDs)
	if err != nil {
		return Event{}, ErrInvalid
	}
	for _, mediaID := range unassigned {
		if _, duplicate := seenMedia[mediaID]; duplicate {
			return Event{}, ErrInvalid
		}
		seenMedia[mediaID] = struct{}{}
		placements = append(placements, draftPlacementRow{MediaItemID: mediaID, Position: len(placements)})
	}

	if len(seenMedia) == 0 {
		return Event{}, ErrNoMediaAvailable
	}
	eventPlaceLabels, valid := normalizePlaceLabels(request.PlaceLabels)
	if !valid {
		return Event{}, ErrPlaceLabelsInvalid
	}

	now := s.now().UTC()
	var organized Event
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := staging.LockAccessSummaryRefresh(ctx, tx); err != nil {
			return err
		}
		if err := staging.LockMediaOrganization(ctx, tx); err != nil {
			return err
		}
		var currentVersion int64
		var title, description, timezone, priorEventPlaceLabelsJSON string
		err := tx.NewRaw(`SELECT version, title, description, grouping_timezone, to_json(place_labels)::text FROM events WHERE id = ? AND lifecycle IN ('draft', 'published') FOR UPDATE`, id).Scan(ctx, &currentVersion, &title, &description, &timezone, &priorEventPlaceLabelsJSON)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if currentVersion != request.Version {
			return ErrVersionConflict
		}
		var priorEventPlaceLabels []string
		if err := json.Unmarshal([]byte(priorEventPlaceLabelsJSON), &priorEventPlaceLabels); err != nil {
			return err
		}
		nextTitle, nextDescription, nextTimezone := title, description, timezone
		if request.Title != nil {
			nextTitle = strings.TrimSpace(*request.Title)
		}
		if request.Description != nil {
			nextDescription = strings.TrimSpace(*request.Description)
		}
		if request.GroupingTimezone != nil {
			location, err := draftLocation(*request.GroupingTimezone)
			if err != nil {
				return ErrInvalid
			}
			nextTimezone = location.String()
		}
		if nextTitle == "" || utf8.RuneCountInString(nextTitle) > 240 || utf8.RuneCountInString(nextDescription) > 2000 {
			return ErrInvalid
		}
		metadataChanged := title != nextTitle || description != nextDescription || timezone != nextTimezone
		var priorMoments []priorMomentState
		if err := tx.NewRaw(`
			SELECT id, position, title, to_json(place_labels)::text AS place_labels_json,
			       proposed_day::text, cover_media_item_id
			FROM draft_moments WHERE event_id = ? ORDER BY position
		`, id).Scan(ctx, &priorMoments); err != nil {
			return err
		}
		for index := range priorMoments {
			if err := json.Unmarshal([]byte(priorMoments[index].PlaceLabelsJSON), &priorMoments[index].PlaceLabels); err != nil {
				return err
			}
		}
		var priorPlacements []priorPlacementState
		if err := tx.NewRaw(`
			SELECT media_item_id, draft_moment_id, position
			FROM draft_media_placements WHERE event_id = ? ORDER BY position
		`, id).Scan(ctx, &priorPlacements); err != nil {
			return err
		}
		if len(seenMedia) > len(priorPlacements) {
			return ErrInvalid
		}
		priorMedia := make(map[uuid.UUID]struct{}, len(priorPlacements))
		for _, placement := range priorPlacements {
			priorMedia[placement.MediaItemID] = struct{}{}
		}
		for mediaID := range seenMedia {
			if _, exists := priorMedia[mediaID]; !exists {
				return ErrInvalid
			}
		}
		organizationChanged := !slices.Equal(priorEventPlaceLabels, eventPlaceLabels) || !sameOrganization(priorMoments, priorPlacements, momentRows, placements)
		priorMediaByMoment := make(map[uuid.UUID][]uuid.UUID)
		for _, placement := range priorPlacements {
			if placement.DraftMomentID != nil {
				priorMediaByMoment[*placement.DraftMomentID] = append(priorMediaByMoment[*placement.DraftMomentID], placement.MediaItemID)
			}
		}
		nextMediaByMoment := make(map[uuid.UUID][]uuid.UUID)
		for _, placement := range placements {
			if placement.DraftMomentID != nil {
				nextMediaByMoment[*placement.DraftMomentID] = append(nextMediaByMoment[*placement.DraftMomentID], placement.MediaItemID)
			}
		}
		type reviewState struct {
			ID                 uuid.UUID
			AttendanceComplete bool
			AudienceComplete   bool
			ReviewVersion      int64
		}
		var priorReviews []reviewState
		if err := tx.NewRaw(`SELECT id, attendance_complete, audience_complete, review_version FROM draft_moments WHERE event_id = ? ORDER BY id FOR UPDATE`, id).Scan(ctx, &priorReviews); err != nil {
			return err
		}
		priorByID := make(map[uuid.UUID]reviewState, len(priorReviews))
		for _, prior := range priorReviews {
			priorByID[prior.ID] = prior
		}
		invalidatedReviews := make(map[uuid.UUID]struct{})
		for index := range momentRows {
			prior, exists := priorByID[momentRows[index].ID]
			if exists && sameUUIDMembers(priorMediaByMoment[momentRows[index].ID], nextMediaByMoment[momentRows[index].ID]) {
				momentRows[index].AttendanceComplete = prior.AttendanceComplete
				momentRows[index].AudienceComplete = prior.AudienceComplete
				momentRows[index].ReviewVersion = prior.ReviewVersion
			} else {
				momentRows[index].AttendanceComplete = false
				momentRows[index].AudienceComplete = false
				if exists {
					momentRows[index].ReviewVersion = prior.ReviewVersion + 1
					invalidatedReviews[momentRows[index].ID] = struct{}{}
				} else {
					momentRows[index].ReviewVersion = 1
				}
			}
		}
		for _, prior := range priorReviews {
			_, retained := seenMoments[prior.ID]
			_, invalidated := invalidatedReviews[prior.ID]
			if retained && !invalidated {
				continue
			}
			if err := staging.PreserveMomentReview(ctx, tx, id, prior.ID, now); err != nil {
				return err
			}
		}
		if err := applyMomentProvenance(ctx, tx, id, momentRows, placements); err != nil {
			return err
		}
		for _, prior := range priorReviews {
			_, retained := seenMoments[prior.ID]
			_, invalidated := invalidatedReviews[prior.ID]
			if retained && !invalidated {
				continue
			}
			for _, statement := range []string{
				`DELETE FROM attendance WHERE moment_id = ?`,
				`DELETE FROM audience_proposals WHERE target_kind = 'moment' AND target_id = ?`,
				`DELETE FROM audience_overrides WHERE target_kind = 'moment' AND target_id = ?`,
				`DELETE FROM current_audience_snapshots WHERE target_kind = 'moment' AND target_id = ?`,
			} {
				if _, err := tx.NewRaw(statement, prior.ID).Exec(ctx); err != nil {
					return err
				}
			}
		}
		if _, err := tx.NewRaw(`DELETE FROM draft_media_placements WHERE event_id = ?`, id).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM draft_moments WHERE event_id = ?`, id).Exec(ctx); err != nil {
			return err
		}
		if err := insertDraftMoments(ctx, tx, id, nextTimezone, momentRows); err != nil {
			return err
		}
		if err := insertDraftPlacements(ctx, tx, id, now, placements); err != nil {
			return err
		}
		finalReviewComplete := request.FinalReviewComplete && !organizationChanged && !metadataChanged
		if _, err := tx.NewRaw(`
			UPDATE events SET title = ?, description = ?, place_labels = ?, grouping_timezone = ?,
				final_review_complete = ?, version = version + 1, updated_at = ? WHERE id = ?
		`, nextTitle, nextDescription, pgdialect.Array(eventPlaceLabels), nextTimezone, finalReviewComplete, now, id).Exec(ctx); err != nil {
			return err
		}
		if err := appendDraftAudit(ctx, tx, actor, "event_draft_organized", map[string]any{
			"event_id": id.String(), "prior_version": currentVersion, "moment_count": len(momentRows),
			"removed_media_count": len(priorPlacements) - len(placements), "metadata_changed": metadataChanged,
		}); err != nil {
			return err
		}
		if _, err := refreshStagedUpdate(ctx, tx, id, now); err != nil {
			return err
		}
		organized, err = getEvent(ctx, tx, id)
		return err
	})
	return organized, err
}

// RestorePublishedMedia restores an omitted current-published Media item only
// when its stable identity is still present in an Event-linked Source.
func (s *Service) RestorePublishedMedia(ctx context.Context, actor setup.CuratorSession, id uuid.UUID, request RestorePublishedMediaRequest) (Event, error) {
	mediaID, err := uuid.Parse(request.MediaItemID)
	if request.Version < 1 || err != nil || mediaID == uuid.Nil {
		return Event{}, ErrInvalid
	}
	now := s.now().UTC()
	var restored Event
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := staging.LockAccessSummaryRefresh(ctx, tx); err != nil {
			return err
		}
		var currentVersion int64
		var lifecycle string
		var publicationID *uuid.UUID
		if err := tx.NewRaw(`
			SELECT version, lifecycle, current_publication_id FROM events WHERE id = ? FOR UPDATE
		`, id).Scan(ctx, &currentVersion, &lifecycle, &publicationID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if currentVersion != request.Version {
			return ErrVersionConflict
		}
		if lifecycle != "published" || publicationID == nil {
			return ErrInvalid
		}
		var sourceCurrent bool
		if err := tx.NewRaw(`
			SELECT EXISTS (
				SELECT 1
				FROM event_sources AS source
				JOIN source_album_memberships AS membership
				  ON membership.source_album_id = source.source_album_id
				JOIN media_items AS media ON media.id = membership.media_item_id
				WHERE source.event_id = ? AND membership.media_item_id = ?
				  AND media.availability = 'current'
			)
		`, id, mediaID).Scan(ctx, &sourceCurrent); err != nil {
			return err
		}
		if !sourceCurrent {
			return ErrMediaUnavailable
		}

		var momentID uuid.UUID
		var publishedCoverID *uuid.UUID
		var publishedMediaPosition, publishedMomentPosition int
		if err := tx.NewRaw(`
			SELECT moment.draft_moment_id, moment.cover_media_item_id,
				placement.position, moment.position
			FROM published_moments AS moment
			JOIN published_media_placements AS placement ON placement.published_moment_id = moment.id
			WHERE moment.publication_id = ? AND placement.media_item_id = ?
		`, *publicationID, mediaID).Scan(ctx, &momentID, &publishedCoverID, &publishedMediaPosition, &publishedMomentPosition); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrInvalid
			}
			return err
		}
		var alreadyEditable bool
		if err := tx.NewRaw(`
			SELECT EXISTS (SELECT 1 FROM draft_media_placements WHERE event_id = ? AND media_item_id = ?)
		`, id, mediaID).Scan(ctx, &alreadyEditable); err != nil {
			return err
		}
		if alreadyEditable {
			return ErrInvalid
		}
		momentState, err := staging.LoadMomentState(ctx, tx, id, momentID, *publicationID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrInvalid
			}
			return err
		}

		var momentExists bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM draft_moments WHERE event_id = ? AND id = ?)`, id, momentID).Scan(ctx, &momentExists); err != nil {
			return err
		}
		if !momentExists {
			var momentIDs []uuid.UUID
			if err := tx.NewRaw(`SELECT id FROM draft_moments WHERE event_id = ? ORDER BY position, id`, id).Scan(ctx, &momentIDs); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`UPDATE draft_moments SET position = position + ? WHERE event_id = ?`, maxDraftMediaItems+1, id).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`
				INSERT INTO draft_moments (
					id, event_id, position, proposed_day, grouping_timezone, proposal_kind,
					created_at, source_days, title, place_labels, cover_media_item_id,
					attendance_complete, audience_complete, review_version
				)
				SELECT item.id, item.event_id, ?, item.proposed_day, item.grouping_timezone,
					item.proposal_kind, item.created_at, item.source_days, item.title, item.place_labels,
					CASE WHEN item.cover_media_item_id = ? THEN ?::uuid ELSE NULL END,
					false, NOT true, item.review_version + 1
				FROM jsonb_to_record(?::jsonb) AS item(
					id uuid, event_id uuid, proposed_day date, grouping_timezone text,
					proposal_kind text, created_at timestamptz, source_days date[], title text,
					place_labels text[], cover_media_item_id uuid, review_version bigint)
			`, maxDraftMediaItems*3, mediaID, mediaID, string(momentState)).Exec(ctx); err != nil {
				return err
			}
			insertAt := publishedMomentPosition
			if insertAt > len(momentIDs) {
				insertAt = len(momentIDs)
			}
			momentIDs = append(momentIDs, uuid.Nil)
			copy(momentIDs[insertAt+1:], momentIDs[insertAt:])
			momentIDs[insertAt] = momentID
			for position, orderedID := range momentIDs {
				if _, err := tx.NewRaw(`UPDATE draft_moments SET position = ? WHERE event_id = ? AND id = ?`, position, id, orderedID).Exec(ctx); err != nil {
					return err
				}
			}
		}

		var momentPosition int
		if err := tx.NewRaw(`SELECT position FROM draft_moments WHERE event_id = ? AND id = ?`, id, momentID).Scan(ctx, &momentPosition); err != nil {
			return err
		}
		var placementOrder []editablePlacementOrder
		if err := tx.NewRaw(`
			SELECT editable.media_item_id, editable.draft_moment_id,
				published.position AS published_position, moment.position AS moment_position
			FROM draft_media_placements AS editable
			LEFT JOIN draft_moments AS moment
			  ON moment.event_id = editable.event_id AND moment.id = editable.draft_moment_id
			LEFT JOIN published_moments AS published_moment
			  ON published_moment.publication_id = ? AND published_moment.draft_moment_id = editable.draft_moment_id
			LEFT JOIN published_media_placements AS published
			  ON published.published_moment_id = published_moment.id AND published.media_item_id = editable.media_item_id
			WHERE editable.event_id = ?
			ORDER BY editable.position, editable.media_item_id
		`, *publicationID, id).Scan(ctx, &placementOrder); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE draft_media_placements SET position = position + ? WHERE event_id = ?`, maxDraftMediaItems+1, id).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`
			INSERT INTO draft_media_placements (event_id, media_item_id, draft_moment_id, position, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, id, mediaID, momentID, maxDraftMediaItems*3, now).Exec(ctx); err != nil {
			return err
		}
		insertAt := restoredPlacementInsertAt(placementOrder, momentID, momentPosition, publishedMediaPosition)
		placementIDs := make([]uuid.UUID, 0, len(placementOrder)+1)
		for _, placement := range placementOrder {
			placementIDs = append(placementIDs, placement.MediaItemID)
		}
		placementIDs = append(placementIDs, uuid.Nil)
		copy(placementIDs[insertAt+1:], placementIDs[insertAt:])
		placementIDs[insertAt] = mediaID
		for position, orderedID := range placementIDs {
			if _, err := tx.NewRaw(`UPDATE draft_media_placements SET position = ? WHERE event_id = ? AND media_item_id = ?`, position, id, orderedID).Exec(ctx); err != nil {
				return err
			}
		}
		if publishedCoverID != nil && *publishedCoverID == mediaID {
			if _, err := tx.NewRaw(`
				UPDATE draft_moments SET cover_media_item_id = ?
				WHERE event_id = ? AND id = ? AND cover_media_item_id IS NULL
			`, mediaID, id, momentID).Exec(ctx); err != nil {
				return err
			}
		}
		if _, err := staging.RestoreMomentReviewIfPublishedResult(ctx, tx, id, momentID); err != nil {
			return err
		}
		if _, err := staging.InvalidateEvent(ctx, tx, id, now); err != nil {
			return err
		}
		if err := appendDraftAudit(ctx, tx, actor, "event_published_media_restored", map[string]any{
			"event_id": id.String(), "media_item_id": mediaID.String(), "base_publication_id": publicationID.String(),
		}); err != nil {
			return err
		}
		restored, err = getEvent(ctx, tx, id)
		return err
	})
	return restored, err
}

func restoredPlacementInsertAt(placements []editablePlacementOrder, momentID uuid.UUID, momentPosition, publishedPosition int) int {
	lastInMoment := -1
	for index, placement := range placements {
		if placement.DraftMomentID == nil || *placement.DraftMomentID != momentID {
			continue
		}
		lastInMoment = index
		if placement.PublishedPosition != nil && *placement.PublishedPosition > publishedPosition {
			return index
		}
	}
	if lastInMoment >= 0 {
		return lastInMoment + 1
	}
	for index, placement := range placements {
		if placement.MomentPosition == nil || *placement.MomentPosition > momentPosition {
			return index
		}
	}
	return len(placements)
}

func sameOrganization(priorMoments []priorMomentState, priorPlacements []priorPlacementState, moments []draftMomentRow, placements []draftPlacementRow) bool {
	if len(priorMoments) != len(moments) || len(priorPlacements) != len(placements) {
		return false
	}
	for index, prior := range priorMoments {
		next := moments[index]
		if prior.ID != next.ID || prior.Position != next.Position || prior.Title != next.Title || !slices.Equal(prior.PlaceLabels, next.PlaceLabels) || prior.ProposedDay != next.ProposedDay || !uuidPointersEqual(prior.CoverMediaItemID, next.CoverMediaItemID) {
			return false
		}
	}
	for index, prior := range priorPlacements {
		next := placements[index]
		if prior.MediaItemID != next.MediaItemID || prior.Position != next.Position || !uuidPointersEqual(prior.DraftMomentID, next.DraftMomentID) {
			return false
		}
	}
	return true
}

func uuidPointersEqual(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sameUUIDMembers(left, right []uuid.UUID) bool {
	if len(left) != len(right) {
		return false
	}
	members := make(map[uuid.UUID]struct{}, len(left))
	for _, id := range left {
		members[id] = struct{}{}
	}
	for _, id := range right {
		if _, exists := members[id]; !exists {
			return false
		}
	}
	return true
}

func containsUUID(values []uuid.UUID, target uuid.UUID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func normalizePlaceLabels(values []string) ([]string, bool) {
	if len(values) > 20 {
		return nil, false
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		label := strings.TrimSpace(value)
		key := strings.ToLower(label)
		if label == "" || utf8.RuneCountInString(label) > 120 {
			return nil, false
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, label)
	}
	return result, true
}

func applyMomentProvenance(ctx context.Context, tx bun.Tx, eventID uuid.UUID, moments []draftMomentRow, placements []draftPlacementRow) error {
	type priorMoment struct {
		sourceDays []string
	}
	priorMoments := make(map[uuid.UUID]priorMoment)
	rows, err := tx.QueryContext(ctx, `
		SELECT id, to_json(source_days)::text
		FROM draft_moments WHERE event_id = ?
	`, eventID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id uuid.UUID
		var sourceDaysJSON string
		if err := rows.Scan(&id, &sourceDaysJSON); err != nil {
			_ = rows.Close()
			return err
		}
		var sourceDays []string
		if err := json.Unmarshal([]byte(sourceDaysJSON), &sourceDays); err != nil {
			_ = rows.Close()
			return err
		}
		priorMoments[id] = priorMoment{sourceDays: sourceDays}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}

	priorPlacement := make(map[uuid.UUID]uuid.UUID)
	rows, err = tx.QueryContext(ctx, `
		SELECT media_item_id, draft_moment_id
		FROM draft_media_placements
		WHERE event_id = ? AND draft_moment_id IS NOT NULL
	`, eventID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var mediaID, momentID uuid.UUID
		if err := rows.Scan(&mediaID, &momentID); err != nil {
			_ = rows.Close()
			return err
		}
		priorPlacement[mediaID] = momentID
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}

	targetIndex := make(map[uuid.UUID]int, len(moments))
	sourceDaysByTarget := make([]map[string]struct{}, len(moments))
	for index, moment := range moments {
		targetIndex[moment.ID] = index
		sourceDaysByTarget[index] = make(map[string]struct{})
	}
	for _, placement := range placements {
		if placement.DraftMomentID == nil {
			continue
		}
		priorMomentID, exists := priorPlacement[placement.MediaItemID]
		if !exists {
			continue
		}
		index, exists := targetIndex[*placement.DraftMomentID]
		if !exists {
			return ErrInvalid
		}
		for _, day := range priorMoments[priorMomentID].sourceDays {
			sourceDaysByTarget[index][day] = struct{}{}
		}
	}

	targetsBySourceDay := make(map[string]int)
	for _, sourceDays := range sourceDaysByTarget {
		for day := range sourceDays {
			targetsBySourceDay[day]++
		}
	}
	for index, sourceDays := range sourceDaysByTarget {
		moments[index].SourceDays = make([]string, 0, len(sourceDays))
		for day := range sourceDays {
			moments[index].SourceDays = append(moments[index].SourceDays, day)
		}
		sort.Strings(moments[index].SourceDays)
		split := false
		for _, day := range moments[index].SourceDays {
			if targetsBySourceDay[day] > 1 {
				split = true
				break
			}
		}
		switch {
		case len(moments[index].SourceDays) == 0:
			moments[index].ProposalKind = "manual"
		case split:
			moments[index].ProposalKind = "split_day"
		case len(moments[index].SourceDays) > 1:
			moments[index].ProposalKind = "merged_days"
		default:
			moments[index].ProposalKind = "local_day"
		}
	}
	return nil
}

// GetEvent returns one Curator-only draft and computes optional Source suggestions.
func (s *Service) GetEvent(ctx context.Context, id uuid.UUID) (Event, error) {
	return getEvent(ctx, s.db, id)
}

func getEvent(ctx context.Context, db bun.IDB, id uuid.UUID) (Event, error) {
	var event Event
	var placeLabelsJSON, withdrawalTargetsJSON, withdrawalsJSON string
	var stagedID, stagedPublicationID *uuid.UUID
	var stagedChanges []byte
	var stagedUpdatedAt *time.Time
	err := db.NewRaw(`
		SELECT event.id, event.lifecycle, event.title, event.description,
			event.grouping_timezone, event.version, event.final_review_complete,
			publication.editable_version,
			publication.id IS NOT NULL AND NOT COALESCE(current.attendance_projection_ready, false),
			`+pendingWithdrawalPublicationSQL+`,
			event.created_at, event.updated_at, to_json(event.place_labels)::text,
			COALESCE((
				SELECT jsonb_agg(jsonb_build_object(
					'id', withdrawal.id, 'target_kind', withdrawal.target_kind,
					'target_id', withdrawal.target_id, 'reason', withdrawal.reason,
					'withdrawn_by_name', person.display_name,
					'withdrawn_at', withdrawal.withdrawn_at,
					'restored_by_publication_id', withdrawal.restored_by_publication_id,
					'restored_at', withdrawal.restored_at,
					'affected_recipient_count', 0, 'affected_media_count', 0,
					'affected_event_count', 0
				) ORDER BY withdrawal.withdrawn_at DESC, withdrawal.id)
				FROM content_withdrawals AS withdrawal
				JOIN people AS person ON person.id = withdrawal.withdrawn_by_person_id
				WHERE (withdrawal.target_kind = 'event' AND withdrawal.target_id = event.id)
				   OR (withdrawal.target_kind = 'moment' AND EXISTS (
					SELECT 1 FROM published_moments AS moment
					JOIN publications AS history ON history.id = moment.publication_id
					WHERE history.event_id = event.id AND moment.draft_moment_id = withdrawal.target_id
				   ))
				   OR (withdrawal.target_kind = 'media' AND EXISTS (
					SELECT 1 FROM published_media_placements AS placement
					JOIN published_moments AS moment ON moment.id = placement.published_moment_id
					JOIN publications AS history ON history.id = moment.publication_id
					WHERE history.event_id = event.id AND placement.media_item_id = withdrawal.target_id
				   ))
			), '[]'::jsonb)::text,
			COALESCE((
				SELECT jsonb_agg(jsonb_build_object(
					'target_kind', target.target_kind,
					'target_id', target.target_id,
					'label', target.label
				) ORDER BY target.kind_order, target.target_order, target.target_id)
				FROM (
					SELECT 0 AS kind_order, 0 AS target_order, 'event'::text AS target_kind,
					       event.id AS target_id, 'Event: ' || revision.title AS label
					FROM published_event_revisions AS revision
					WHERE revision.publication_id = publication.id
					UNION ALL
					SELECT 1, moment.position, 'moment', moment.draft_moment_id,
					       'Moment: ' || COALESCE(NULLIF(moment.title, ''), 'Moment ' || (moment.position + 1)::text)
					FROM published_moments AS moment
					WHERE moment.publication_id = publication.id
					UNION ALL
					SELECT 2, placement.position, 'media', placement.media_item_id,
					       'Media: ' || CASE
					           WHEN published.local_date_time IS NULL THEN 'Undated ' || published.media_type
					           ELSE published.local_date_time || ' ' || published.media_type
					       END
					FROM current_published_placements AS placement
					JOIN published_media_placements AS published
					  ON published.published_moment_id = placement.published_moment_id
					 AND published.media_item_id = placement.media_item_id
					WHERE placement.event_id = event.id AND placement.publication_id = publication.id
				) AS target
				WHERE EXISTS (
					SELECT 1 FROM current_published_placements AS placement
					JOIN published_moments AS moment ON moment.id = placement.published_moment_id
					WHERE (
						(target.target_kind = 'event' AND placement.event_id = target.target_id)
						OR (target.target_kind = 'moment' AND moment.draft_moment_id = target.target_id)
						OR (target.target_kind = 'media' AND placement.media_item_id = target.target_id)
					) AND NOT content_is_withdrawn(
						placement.event_id, moment.draft_moment_id, placement.media_item_id
					)
				)
			), '[]'::jsonb)::text,
			staged.id, staged.base_publication_id, staged.net_changes, staged.updated_at
		FROM events AS event
		LEFT JOIN publications AS publication ON publication.id = event.current_publication_id
		LEFT JOIN current_published_events AS current ON current.publication_id = publication.id
		LEFT JOIN staged_updates AS staged ON staged.id = event.current_staged_update_id
		WHERE event.id = ?
	`, id, id, id, id).Scan(ctx, &event.ID, &event.Lifecycle, &event.Title, &event.Description,
		&event.GroupingTimezone, &event.Version, &event.FinalReviewComplete,
		&event.PublishedEditableVersion, &event.PublishedAttendanceRecoveryRequired,
		&event.PendingWithdrawalPublication, &event.CreatedAt, &event.UpdatedAt,
		&placeLabelsJSON, &withdrawalsJSON, &withdrawalTargetsJSON,
		&stagedID, &stagedPublicationID, &stagedChanges, &stagedUpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, ErrNotFound
	}
	if err != nil {
		return Event{}, err
	}
	if err := json.Unmarshal([]byte(placeLabelsJSON), &event.PlaceLabels); err != nil {
		return Event{}, err
	}
	if err := json.Unmarshal([]byte(withdrawalsJSON), &event.Withdrawals); err != nil {
		return Event{}, err
	}
	if err := json.Unmarshal([]byte(withdrawalTargetsJSON), &event.WithdrawalTargets); err != nil {
		return Event{}, err
	}
	event.Sources = make([]EventSource, 0)
	rows, err := db.QueryContext(ctx, `
		SELECT source.id, source.name, source.description,
			event_source.initialized_name, event_source.initialized_description
		FROM event_sources AS event_source
		JOIN source_albums AS source ON source.id = event_source.source_album_id
		WHERE event_source.event_id = ? ORDER BY event_source.source_order
	`, id)
	if err != nil {
		return Event{}, err
	}
	for rows.Next() {
		var source EventSource
		var currentName, currentDescription, initializedName, initializedDescription string
		if err := rows.Scan(&source.ID, &currentName, &currentDescription, &initializedName, &initializedDescription); err != nil {
			_ = rows.Close()
			return Event{}, err
		}
		if currentName != initializedName || currentDescription != initializedDescription {
			source.MetadataSuggestion = &SourceMetadataSuggestion{Name: currentName, Description: currentDescription}
		}
		event.Sources = append(event.Sources, source)
	}
	if err := rows.Close(); err != nil {
		return Event{}, err
	}
	if err := rows.Err(); err != nil {
		return Event{}, err
	}

	event.Moments = make([]Moment, 0)
	momentRows, err := db.QueryContext(ctx, `
		SELECT id, title, to_json(place_labels)::text, proposed_day::text, grouping_timezone,
			to_json(source_days)::text, proposal_kind, cover_media_item_id::text,
			attendance_complete, audience_complete
		FROM draft_moments WHERE event_id = ? ORDER BY position
	`, id)
	if err != nil {
		return Event{}, err
	}
	for momentRows.Next() {
		var moment Moment
		var placeLabelsJSON, sourceDaysJSON string
		if err := momentRows.Scan(&moment.ID, &moment.Title, &placeLabelsJSON, &moment.ProposedDay,
			&moment.GroupingTimezone, &sourceDaysJSON, &moment.ProposalKind,
			&moment.CoverMediaItemID, &moment.AttendanceComplete,
			&moment.AudienceComplete); err != nil {
			_ = momentRows.Close()
			return Event{}, err
		}
		if err := json.Unmarshal([]byte(placeLabelsJSON), &moment.PlaceLabels); err != nil {
			_ = momentRows.Close()
			return Event{}, err
		}
		if err := json.Unmarshal([]byte(sourceDaysJSON), &moment.SourceDays); err != nil {
			_ = momentRows.Close()
			return Event{}, err
		}
		event.Moments = append(event.Moments, moment)
	}
	if err := momentRows.Close(); err != nil {
		return Event{}, err
	}
	if err := momentRows.Err(); err != nil {
		return Event{}, err
	}
	event.Moments, event.UnassignedMedia, err = loadEventMedia(ctx, db, id, event.Moments)
	if err != nil {
		return Event{}, err
	}
	if stagedID != nil && stagedPublicationID != nil && stagedUpdatedAt != nil {
		changes := make([]staging.Change, 0)
		if err := json.Unmarshal(stagedChanges, &changes); err != nil {
			return Event{}, err
		}
		event.StagedUpdate = stagedUpdateFromDomain(&staging.Update{
			ID: stagedID.String(), BasePublicationID: stagedPublicationID.String(),
			Changes: changes, UpdatedAt: *stagedUpdatedAt,
		})
	}
	return event, nil
}

func loadEventMedia(ctx context.Context, db bun.IDB, eventID uuid.UUID, moments []Moment) ([]Moment, []MediaItem, error) {
	momentIndexes := make(map[string]int, len(moments))
	for index := range moments {
		moments[index].MediaItems = make([]MediaItem, 0)
		momentIndexes[moments[index].ID] = index
	}
	unassigned := make([]MediaItem, 0)
	rows, err := db.QueryContext(ctx, `
		SELECT placement.draft_moment_id::text, media.id, media.media_type,
			media.width, media.height, media.local_date_time
		FROM draft_media_placements AS placement
		JOIN media_items AS media ON media.id = placement.media_item_id
		WHERE placement.event_id = ?
		ORDER BY placement.position
	`, eventID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var momentID sql.NullString
		var media MediaItem
		if err := rows.Scan(&momentID, &media.ID, &media.MediaType, &media.Width, &media.Height, &media.LocalDateTime); err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		if !momentID.Valid {
			unassigned = append(unassigned, media)
			continue
		}
		index, exists := momentIndexes[momentID.String]
		if !exists {
			_ = rows.Close()
			return nil, nil, fmt.Errorf("%w: %s", errUnknownMoment, momentID.String)
		}
		moments[index].MediaItems = append(moments[index].MediaItems, media)
	}
	if err := rows.Close(); err != nil {
		return nil, nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return moments, unassigned, nil
}

// SourceMedia lists stable portal Media IDs without exposing Immich identifiers.
func (s *Service) SourceMedia(ctx context.Context, sourceID uuid.UUID) (SourceMediaResponse, error) {
	var disposition string
	var missing bool
	err := s.db.NewRaw(`
		SELECT disposition, source_missing FROM source_albums WHERE id = ?
	`, sourceID).Scan(ctx, &disposition, &missing)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceMediaResponse{}, ErrNotFound
	}
	if err != nil {
		return SourceMediaResponse{}, err
	}
	if missing || disposition == "ignored" {
		return SourceMediaResponse{}, ErrSourceUnavailable
	}
	var media []MediaItem
	if err := s.db.NewRaw(`
		SELECT media.id, media.media_type, media.width, media.height, media.local_date_time
		FROM source_album_memberships AS membership
		JOIN media_items AS media ON media.id = membership.media_item_id
		WHERE membership.source_album_id = ? AND media.availability = 'current'
		ORDER BY media.local_date_time NULLS LAST, media.id
		LIMIT ?
	`, sourceID, maxDraftMediaItems+1).Scan(ctx, &media); err != nil {
		return SourceMediaResponse{}, err
	}
	if len(media) > maxDraftMediaItems {
		return SourceMediaResponse{}, ErrSourceTooLarge
	}
	if media == nil {
		media = make([]MediaItem, 0)
	}
	return SourceMediaResponse{MediaItems: media}, nil
}

// CreateLooseItem creates or returns the one stable Loose identity for a Media item.
func (s *Service) CreateLooseItem(ctx context.Context, actor setup.CuratorSession, request CreateLooseItemRequest) (LooseItem, bool, error) {
	mediaID, err := uuid.Parse(request.MediaItemID)
	if err != nil || mediaID == uuid.Nil {
		return LooseItem{}, false, ErrInvalid
	}
	location, err := draftLocation(request.Timezone)
	if err != nil {
		return LooseItem{}, false, ErrInvalid
	}
	title := strings.TrimSpace(request.Title)
	description := strings.TrimSpace(request.Description)
	if utf8.RuneCountInString(title) > 240 || utf8.RuneCountInString(description) > 2000 {
		return LooseItem{}, false, ErrInvalid
	}
	looseID := uuid.New()
	now := s.now().UTC()
	inserted := false
	var result LooseItem
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var capture *string
		var availability string
		err := tx.NewRaw(`SELECT local_date_time, availability FROM media_items WHERE id = ? FOR UPDATE`, mediaID).Scan(ctx, &capture, &availability)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrMediaUnavailable
		}
		if err != nil {
			return err
		}
		var existing uuid.UUID
		err = tx.NewRaw(`SELECT id FROM loose_items WHERE media_item_id = ?`, mediaID).Scan(ctx, &existing)
		if err == nil {
			looseID = existing
			result, err = getLooseItem(ctx, tx, looseID)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if availability != "current" {
			return ErrMediaUnavailable
		}
		day, _ := captureDay(capture, location)
		if _, err := tx.NewRaw(`
			INSERT INTO loose_items (
				id, media_item_id, title, description, grouping_timezone, proposed_day, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?::date, ?, ?)
		`, looseID, mediaID, title, description, location.String(), day, now, now).Exec(ctx); err != nil {
			return err
		}
		if err := appendDraftAudit(ctx, tx, actor, "loose_item_draft_created", map[string]any{
			"loose_item_id": looseID.String(), "media_item_id": mediaID.String(),
		}); err != nil {
			return err
		}
		inserted = true
		result, err = getLooseItem(ctx, tx, looseID)
		return err
	})
	if err != nil {
		return LooseItem{}, false, err
	}
	return result, inserted, nil
}

// GetLooseItem returns one Curator-only Loose draft.
func (s *Service) GetLooseItem(ctx context.Context, id uuid.UUID) (LooseItem, error) {
	return getLooseItem(ctx, s.db, id)
}

func getLooseItem(ctx context.Context, db bun.IDB, id uuid.UUID) (LooseItem, error) {
	var item LooseItem
	err := db.NewRaw(`
		SELECT loose.id, loose.lifecycle, loose.title, loose.description, loose.grouping_timezone,
			loose.proposed_day::text, loose.version, loose.audience_complete, loose.created_at, loose.updated_at,
			media.id, media.media_type, media.width, media.height, media.local_date_time
		FROM loose_items AS loose
		JOIN media_items AS media ON media.id = loose.media_item_id
		WHERE loose.id = ?
	`, id).Scan(ctx, &item.ID, &item.Lifecycle, &item.Title, &item.Description, &item.GroupingTimezone,
		&item.ProposedDay, &item.Version, &item.AudienceComplete, &item.CreatedAt, &item.UpdatedAt,
		&item.MediaItem.ID, &item.MediaItem.MediaType, &item.MediaItem.Width,
		&item.MediaItem.Height, &item.MediaItem.LocalDateTime)
	if errors.Is(err, sql.ErrNoRows) {
		return LooseItem{}, ErrNotFound
	}
	return item, err
}

func draftLocation(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "Local" {
		return nil, ErrInvalid
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, ErrInvalid
	}
	return location, nil
}

func parseUniqueIDs(raw []string) ([]uuid.UUID, error) {
	result := make([]uuid.UUID, 0, len(raw))
	seen := make(map[uuid.UUID]struct{}, len(raw))
	for _, value := range raw {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil {
			return nil, ErrInvalid
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, ErrInvalid
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result, nil
}

func appendDraftAudit(ctx context.Context, tx bun.Tx, actor setup.CuratorSession, action string, metadata map[string]any) error {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	request := setup.RequestMetadataFromContext(ctx)
	_, err = tx.NewRaw(`
		INSERT INTO security_audit_events (
			actor_person_id, action, outcome, client_ip, user_agent, session_id, metadata
		) VALUES (?, ?, 'success', NULLIF(?, '')::inet, ?, ?, ?::jsonb)
	`, actor.PersonID, action, request.ClientIP, request.UserAgent, actor.SessionID, string(encoded)).Exec(ctx)
	if err != nil {
		return fmt.Errorf("append draft audit: %w", err)
	}
	return nil
}
