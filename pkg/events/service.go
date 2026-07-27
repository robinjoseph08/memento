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

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/uptrace/bun"
)

var (
	ErrNotFound          = errors.New("draft not found")
	ErrInvalid           = errors.New("draft request is invalid")
	ErrSourceUnavailable = errors.New("source album is unavailable for drafting")
	ErrMediaUnavailable  = errors.New("media item is unavailable for drafting")
)

// CreateEventRequest initializes one portal-owned Event from selected Source material.
type CreateEventRequest struct {
	SourceAlbumIDs []string `json:"source_album_ids" validate:"required,min=1,max=100"`
	MediaItemIDs   []string `json:"media_item_ids" validate:"max=100000"`
	Timezone       string   `json:"timezone" validate:"required,max=100"`
	Title          string   `json:"title" validate:"max=240" mod:"trim"`
	Description    string   `json:"description" validate:"max=2000" mod:"trim"`
}

// CreateLooseItemRequest initializes a private Loose item around an existing Media identity.
type CreateLooseItemRequest struct {
	MediaItemID string `json:"media_item_id" validate:"required"`
	Timezone    string `json:"timezone" validate:"required,max=100"`
	Title       string `json:"title" validate:"max=240" mod:"trim"`
	Description string `json:"description" validate:"max=2000" mod:"trim"`
}

// MediaItem is the portal-owned, path-free representation used while organizing drafts.
type MediaItem struct {
	ID            string  `json:"id"`
	MediaType     string  `json:"media_type"`
	Width         *int    `json:"width"`
	Height        *int    `json:"height"`
	LocalDateTime *string `json:"local_date_time"`
}

// Moment is an initial local-day proposal. Recipients never receive this draft type.
type Moment struct {
	ID               string      `json:"id"`
	ProposedDay      string      `json:"proposed_day"`
	GroupingTimezone string      `json:"grouping_timezone"`
	MediaItems       []MediaItem `json:"media_items"`
}

// SourceMetadataSuggestion reports current Source metadata without changing Event metadata.
type SourceMetadataSuggestion struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// EventSource records private provenance through a stable portal Source identity.
type EventSource struct {
	ID                 string                    `json:"id"`
	MetadataSuggestion *SourceMetadataSuggestion `json:"metadata_suggestion"`
}

// Event is a portal-owned private draft.
type Event struct {
	ID               string        `json:"id"`
	Lifecycle        string        `json:"lifecycle"`
	Title            string        `json:"title"`
	Description      string        `json:"description"`
	GroupingTimezone string        `json:"grouping_timezone"`
	Version          int64         `json:"version"`
	Sources          []EventSource `json:"sources"`
	Moments          []Moment      `json:"moments"`
	UnassignedMedia  []MediaItem   `json:"unassigned_media"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
}

// LooseItem is a private independently publishable Media draft.
type LooseItem struct {
	ID               string    `json:"id"`
	Lifecycle        string    `json:"lifecycle"`
	Title            string    `json:"title"`
	Description      string    `json:"description"`
	GroupingTimezone string    `json:"grouping_timezone"`
	ProposedDay      *string   `json:"proposed_day"`
	Version          int64     `json:"version"`
	MediaItem        MediaItem `json:"media_item"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// SourceMediaResponse lists stable Media identities available for selection.
type SourceMediaResponse struct {
	MediaItems []MediaItem `json:"media_items"`
}

// Service persists private editable drafts without contacting or mutating Immich.
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

// CreateEvent creates stable Event and Moment identities in one transaction.
func (s *Service) CreateEvent(ctx context.Context, actor setup.CuratorSession, request CreateEventRequest) (Event, error) {
	location, err := draftLocation(request.Timezone)
	if err != nil {
		return Event{}, ErrInvalid
	}
	sourceIDs, err := parseUniqueIDs(request.SourceAlbumIDs)
	if err != nil || len(sourceIDs) == 0 {
		return Event{}, ErrInvalid
	}
	selectedIDs, err := parseUniqueIDs(request.MediaItemIDs)
	if err != nil {
		return Event{}, ErrInvalid
	}

	eventID := uuid.New()
	now := s.now().UTC()
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
			return ErrMediaUnavailable
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
		if title == "" || len(title) > 240 || len(description) > 2000 {
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
		momentPosition := 0
		for index := range media {
			if media[index].day == nil {
				continue
			}
			momentID, exists := momentByDay[*media[index].day]
			if !exists {
				momentID = uuid.New()
				momentByDay[*media[index].day] = momentID
				if _, err := tx.NewRaw(`
					INSERT INTO draft_moments (id, event_id, position, proposed_day, grouping_timezone)
					VALUES (?, ?, ?, ?::date, ?)
				`, momentID, eventID, momentPosition, *media[index].day, location.String()).Exec(ctx); err != nil {
					return err
				}
				momentPosition++
			}
			media[index].momentID = momentID
		}
		for position, item := range media {
			var momentID any
			if item.momentID != uuid.Nil {
				momentID = item.momentID
			}
			if _, err := tx.NewRaw(`
				INSERT INTO draft_media_placements (event_id, media_item_id, draft_moment_id, position, created_at)
				VALUES (?, ?, ?, ?, ?)
			`, eventID, item.ID, momentID, position, now).Exec(ctx); err != nil {
				return err
			}
		}
		if _, err := tx.NewRaw(`
			UPDATE source_albums SET disposition = 'drafted', ignored_at = NULL,
				version = version + 1, updated_at = ?
			WHERE id IN (?) AND disposition <> 'drafted'
		`, now, bun.List(sourceIDs)).Exec(ctx); err != nil {
			return err
		}
		return appendDraftAudit(ctx, tx, actor, "event_draft_created", map[string]any{
			"event_id": eventID.String(), "source_count": len(sourceIDs), "media_count": len(media),
		})
	})
	if err != nil {
		return Event{}, err
	}
	return s.GetEvent(ctx, eventID)
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
		SELECT DISTINCT media.id, media.media_type, media.width, media.height, media.local_date_time
		FROM media_items AS media
		JOIN source_album_memberships AS membership ON membership.media_item_id = media.id
		WHERE membership.source_album_id IN (?)`
	args := []any{bun.List(sourceIDs)}
	if len(selectedIDs) > 0 {
		query += ` AND media.id IN (?)`
		args = append(args, bun.List(selectedIDs))
	}
	var media []mediaRecord
	if err := tx.NewRaw(query, args...).Scan(ctx, &media); err != nil {
		return nil, err
	}
	if len(selectedIDs) > 0 && len(media) != len(selectedIDs) {
		return nil, ErrMediaUnavailable
	}
	return media, nil
}

func prepareProposals(media []mediaRecord, location *time.Location) {
	for index := range media {
		media[index].day, media[index].instant = captureDay(media[index].LocalDateTime, location)
	}
	sort.Slice(media, func(i, j int) bool {
		left, right := media[i].instant, media[j].instant
		if left == nil || right == nil {
			if left == nil && right == nil {
				return media[i].ID.String() < media[j].ID.String()
			}
			return right == nil
		}
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
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		localized := parsed.In(location)
		day := localized.Format(time.DateOnly)
		instant := parsed.UTC()
		return &day, &instant
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05", "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, value, location); err == nil {
			day := parsed.Format(time.DateOnly)
			instant := parsed.UTC()
			return &day, &instant
		}
	}
	return nil, nil
}

// GetEvent returns one Curator-only draft and computes optional Source suggestions.
func (s *Service) GetEvent(ctx context.Context, id uuid.UUID) (Event, error) {
	var event Event
	err := s.db.NewRaw(`
		SELECT id, lifecycle, title, description, grouping_timezone, version, created_at, updated_at
		FROM events WHERE id = ?
	`, id).Scan(ctx, &event.ID, &event.Lifecycle, &event.Title, &event.Description,
		&event.GroupingTimezone, &event.Version, &event.CreatedAt, &event.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, ErrNotFound
	}
	if err != nil {
		return Event{}, err
	}
	event.Sources = make([]EventSource, 0)
	rows, err := s.db.QueryContext(ctx, `
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
	momentRows, err := s.db.QueryContext(ctx, `
		SELECT id, proposed_day::text, grouping_timezone
		FROM draft_moments WHERE event_id = ? ORDER BY position
	`, id)
	if err != nil {
		return Event{}, err
	}
	for momentRows.Next() {
		var moment Moment
		if err := momentRows.Scan(&moment.ID, &moment.ProposedDay, &moment.GroupingTimezone); err != nil {
			_ = momentRows.Close()
			return Event{}, err
		}
		moment.MediaItems, err = s.eventMedia(ctx, id, uuid.MustParse(moment.ID), false)
		if err != nil {
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
	event.UnassignedMedia, err = s.eventMedia(ctx, id, uuid.Nil, true)
	if err != nil {
		return Event{}, err
	}
	return event, nil
}

func (s *Service) eventMedia(ctx context.Context, eventID, momentID uuid.UUID, unassigned bool) ([]MediaItem, error) {
	query := `
		SELECT media.id, media.media_type, media.width, media.height, media.local_date_time
		FROM draft_media_placements AS placement
		JOIN media_items AS media ON media.id = placement.media_item_id
		WHERE placement.event_id = ?`
	args := []any{eventID}
	if unassigned {
		query += ` AND placement.draft_moment_id IS NULL`
	} else {
		query += ` AND placement.draft_moment_id = ?`
		args = append(args, momentID)
	}
	query += ` ORDER BY placement.position`
	var media []MediaItem
	if err := s.db.NewRaw(query, args...).Scan(ctx, &media); err != nil {
		return nil, err
	}
	if media == nil {
		media = make([]MediaItem, 0)
	}
	return media, nil
}

// SourceMedia lists stable portal Media IDs without exposing Immich identifiers.
func (s *Service) SourceMedia(ctx context.Context, sourceID uuid.UUID) (SourceMediaResponse, error) {
	var exists bool
	if err := s.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM source_albums WHERE id = ?)`, sourceID).Scan(ctx, &exists); err != nil {
		return SourceMediaResponse{}, err
	}
	if !exists {
		return SourceMediaResponse{}, ErrNotFound
	}
	var media []MediaItem
	if err := s.db.NewRaw(`
		SELECT media.id, media.media_type, media.width, media.height, media.local_date_time
		FROM source_album_memberships AS membership
		JOIN media_items AS media ON media.id = membership.media_item_id
		WHERE membership.source_album_id = ?
		ORDER BY media.local_date_time NULLS LAST, media.id
	`, sourceID).Scan(ctx, &media); err != nil {
		return SourceMediaResponse{}, err
	}
	if media == nil {
		media = make([]MediaItem, 0)
	}
	return SourceMediaResponse{MediaItems: media}, nil
}

// CreateLooseItem creates or returns the one stable Loose identity for a Media item.
func (s *Service) CreateLooseItem(ctx context.Context, actor setup.CuratorSession, request CreateLooseItemRequest) (LooseItem, error) {
	mediaID, err := uuid.Parse(request.MediaItemID)
	if err != nil || mediaID == uuid.Nil {
		return LooseItem{}, ErrInvalid
	}
	location, err := draftLocation(request.Timezone)
	if err != nil {
		return LooseItem{}, ErrInvalid
	}
	if len(request.Title) > 240 || len(request.Description) > 2000 {
		return LooseItem{}, ErrInvalid
	}
	looseID := uuid.New()
	now := s.now().UTC()
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
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		day, _ := captureDay(capture, location)
		if _, err := tx.NewRaw(`
			INSERT INTO loose_items (
				id, media_item_id, title, description, grouping_timezone, proposed_day, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?::date, ?, ?)
		`, looseID, mediaID, strings.TrimSpace(request.Title), strings.TrimSpace(request.Description),
			location.String(), day, now, now).Exec(ctx); err != nil {
			return err
		}
		return appendDraftAudit(ctx, tx, actor, "loose_item_draft_created", map[string]any{
			"loose_item_id": looseID.String(), "media_item_id": mediaID.String(),
		})
	})
	if err != nil {
		return LooseItem{}, err
	}
	return s.GetLooseItem(ctx, looseID)
}

// GetLooseItem returns one Curator-only Loose draft.
func (s *Service) GetLooseItem(ctx context.Context, id uuid.UUID) (LooseItem, error) {
	var item LooseItem
	err := s.db.NewRaw(`
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
