// Package events owns private Event, Moment, and Loose item drafts.
package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	// Embed IANA timezone data for the minimal production image.
	_ "time/tzdata"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/uptrace/bun"
)

const (
	maxDraftSourceAlbums = 100
	maxDraftMediaItems   = 100000
)

var (
	ErrNotFound          = errors.New("draft not found")
	ErrInvalid           = errors.New("draft request is invalid")
	ErrSourceUnavailable = errors.New("source album is unavailable for drafting")
	ErrSourceTooLarge    = errors.New("source album has too many media items")
	ErrMediaUnavailable  = errors.New("media item is unavailable for drafting")
	ErrNoMediaAvailable  = errors.New("no media items are available for drafting")
	ErrVersionConflict   = errors.New("draft version is stale")
	errUnknownMoment     = errors.New("draft placement references unknown Moment")
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
	ID                 string   `json:"id" validate:"required"`
	Title              string   `json:"title,omitempty" validate:"max=240" mod:"trim"`
	ProposedDay        string   `json:"proposed_day" validate:"required"`
	CoverMediaItemID   *string  `json:"cover_media_item_id" tstype:"string | null,required"`
	AttendanceComplete bool     `json:"attendance_complete"`
	AudienceComplete   bool     `json:"audience_complete"`
	MediaItemIDs       []string `json:"media_item_ids" validate:"required,min=1,max=100000"`
}

// OrganizeEventRequest atomically replaces draft organization at an expected version.
type OrganizeEventRequest struct {
	Version             int64            `json:"version" validate:"required,min=1"`
	Moments             []OrganizeMoment `json:"moments" validate:"max=100000,dive"`
	UnassignedMediaIDs  []string         `json:"unassigned_media_ids" validate:"max=100000"`
	FinalReviewComplete bool             `json:"final_review_complete"`
}

// EventSummary supports Curator work navigation without loading every Media item.
type EventSummary struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Version         int64     `json:"version"`
	MomentCount     int       `json:"moment_count"`
	UnassignedCount int       `json:"unassigned_count"`
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
	ID                  string        `json:"id"`
	Lifecycle           string        `json:"lifecycle"`
	Title               string        `json:"title"`
	Description         string        `json:"description"`
	GroupingTimezone    string        `json:"grouping_timezone"`
	Version             int64         `json:"version"`
	FinalReviewComplete bool          `json:"final_review_complete"`
	Sources             []EventSource `json:"sources"`
	Moments             []Moment      `json:"moments"`
	UnassignedMedia     []MediaItem   `json:"unassigned_media"`
	CreatedAt           time.Time     `json:"created_at"`
	UpdatedAt           time.Time     `json:"updated_at"`
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
	MediaItem        MediaItem `json:"media_item"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// SourceMediaResponse lists stable Media identities available for selection.
type SourceMediaResponse struct {
	MediaItems []MediaItem `json:"media_items"`
}

// Service persists private drafts without contacting or mutating Immich.
type Service struct {
	db  *bun.DB
	now func() time.Time
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
	CoverMediaItemID   *uuid.UUID `json:"cover_media_item_id,omitempty"`
	AttendanceComplete bool       `json:"attendance_complete,omitempty"`
	AudienceComplete   bool       `json:"audience_complete,omitempty"`
}

type draftPlacementRow struct {
	MediaItemID   uuid.UUID  `json:"media_item_id"`
	DraftMomentID *uuid.UUID `json:"draft_moment_id"`
	Position      int        `json:"position"`
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
		for position, sourceID := range sourceIDs {
			source := sources[sourceID]
			if _, err := tx.NewRaw(`
				INSERT INTO event_sources (
					event_id, source_album_id, source_order, initialized_name,
					initialized_description, initialized_at
				) VALUES (?, ?, ?, ?, ?, ?)
			`, eventID, sourceID, position, source.Name, source.Description, now).Exec(ctx); err != nil {
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
			proposal_kind, title, cover_media_item_id, attendance_complete, audience_complete
		)
		SELECT incoming.id, ?, incoming.position, incoming.proposed_day::date, ?,
			COALESCE(incoming.source_days, '{}'::date[]),
			COALESCE(incoming.proposal_kind, 'manual'), COALESCE(incoming.title, ''),
			incoming.cover_media_item_id, COALESCE(incoming.attendance_complete, false),
			COALESCE(incoming.audience_complete, false)
		FROM jsonb_to_recordset(?::jsonb) AS incoming(
			id uuid, position integer, proposed_day text, source_days date[],
			proposal_kind text, title text, cover_media_item_id uuid,
			attendance_complete boolean, audience_complete boolean
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
		WHERE EXISTS (
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

// ListEvents returns private draft work ordered by recent activity.
func (s *Service) ListEvents(ctx context.Context) (EventListResponse, error) {
	result := EventListResponse{Events: make([]EventSummary, 0)}
	err := s.db.NewRaw(`
		SELECT event.id, event.title, event.version,
			COALESCE(moment.moment_count, 0)::integer AS moment_count,
			COALESCE(placement.unassigned_count, 0)::integer AS unassigned_count,
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
		WHERE event.lifecycle = 'draft'
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
			Title: strings.TrimSpace(moment.Title), CoverMediaItemID: coverID,
			AttendanceComplete: moment.AttendanceComplete, AudienceComplete: moment.AudienceComplete,
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

	now := s.now().UTC()
	var organized Event
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var currentVersion int64
		var timezone string
		err := tx.NewRaw(`SELECT version, grouping_timezone FROM events WHERE id = ? AND lifecycle = 'draft' FOR UPDATE`, id).Scan(ctx, &currentVersion, &timezone)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if currentVersion != request.Version {
			return ErrVersionConflict
		}
		var persisted []uuid.UUID
		if err := tx.NewRaw(`SELECT media_item_id FROM draft_media_placements WHERE event_id = ? ORDER BY media_item_id`, id).Scan(ctx, &persisted); err != nil {
			return err
		}
		if len(persisted) != len(seenMedia) {
			return ErrInvalid
		}
		for _, mediaID := range persisted {
			if _, exists := seenMedia[mediaID]; !exists {
				return ErrInvalid
			}
		}
		if err := applyMomentProvenance(ctx, tx, id, momentRows, placements); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM draft_media_placements WHERE event_id = ?`, id).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM draft_moments WHERE event_id = ?`, id).Exec(ctx); err != nil {
			return err
		}
		if err := insertDraftMoments(ctx, tx, id, timezone, momentRows); err != nil {
			return err
		}
		if err := insertDraftPlacements(ctx, tx, id, now, placements); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`
			UPDATE events SET final_review_complete = ?, version = version + 1, updated_at = ? WHERE id = ?
		`, request.FinalReviewComplete, now, id).Exec(ctx); err != nil {
			return err
		}
		if err := appendDraftAudit(ctx, tx, actor, "event_draft_organized", map[string]any{
			"event_id": id.String(), "prior_version": currentVersion, "moment_count": len(momentRows),
		}); err != nil {
			return err
		}
		organized, err = getEvent(ctx, tx, id)
		return err
	})
	return organized, err
}

func containsUUID(values []uuid.UUID, target uuid.UUID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
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
	err := db.NewRaw(`
		SELECT id, lifecycle, title, description, grouping_timezone, version,
			final_review_complete, created_at, updated_at
		FROM events WHERE id = ?
	`, id).Scan(ctx, &event.ID, &event.Lifecycle, &event.Title, &event.Description,
		&event.GroupingTimezone, &event.Version, &event.FinalReviewComplete,
		&event.CreatedAt, &event.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, ErrNotFound
	}
	if err != nil {
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
		SELECT id, title, proposed_day::text, grouping_timezone,
			to_json(source_days)::text, proposal_kind, cover_media_item_id::text,
			attendance_complete, audience_complete
		FROM draft_moments WHERE event_id = ? ORDER BY position
	`, id)
	if err != nil {
		return Event{}, err
	}
	for momentRows.Next() {
		var moment Moment
		var sourceDaysJSON string
		if err := momentRows.Scan(&moment.ID, &moment.Title, &moment.ProposedDay,
			&moment.GroupingTimezone, &sourceDaysJSON, &moment.ProposalKind,
			&moment.CoverMediaItemID, &moment.AttendanceComplete,
			&moment.AudienceComplete); err != nil {
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
		WHERE membership.source_album_id = ?
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
		err := tx.NewRaw(`SELECT local_date_time FROM media_items WHERE id = ? FOR UPDATE`, mediaID).Scan(ctx, &capture)
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
			loose.proposed_day::text, loose.version, loose.created_at, loose.updated_at,
			media.id, media.media_type, media.width, media.height, media.local_date_time
		FROM loose_items AS loose
		JOIN media_items AS media ON media.id = loose.media_item_id
		WHERE loose.id = ?
	`, id).Scan(ctx, &item.ID, &item.Lifecycle, &item.Title, &item.Description, &item.GroupingTimezone,
		&item.ProposedDay, &item.Version, &item.CreatedAt, &item.UpdatedAt,
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
