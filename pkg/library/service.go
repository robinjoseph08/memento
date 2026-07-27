// Package library exposes only current, authorization-filtered Recipient content.
package library

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/uptrace/bun"
)

const (
	defaultPageSize = 40
	maxPageSize     = 100
)

var (
	ErrInvalidCursor = errors.New("invalid library cursor")
	ErrNotFound      = errors.New("authorized content not found")
)

type Media struct {
	ID            string  `json:"id"`
	MediaType     string  `json:"media_type"`
	Width         *int    `json:"width" tstype:"number | null,required"`
	Height        *int    `json:"height" tstype:"number | null,required"`
	LocalDateTime *string `json:"local_date_time" tstype:"string | null,required"`
	Available     bool    `json:"available"`
	ThumbnailURL  string  `json:"thumbnail_url"`
}

type MediaPage struct {
	Media      []Media `json:"media"`
	NextCursor *string `json:"next_cursor" tstype:"string | null,required"`
}

type EventSummary struct {
	ID             string    `json:"id"`
	PublicationID  string    `json:"publication_id"`
	Title          string    `json:"title"`
	Description    string    `json:"description"`
	CommittedAt    time.Time `json:"committed_at"`
	CoverMediaID   string    `json:"cover_media_id"`
	CoverWidth     *int      `json:"cover_width" tstype:"number | null,required"`
	CoverHeight    *int      `json:"cover_height" tstype:"number | null,required"`
	CoverAvailable bool      `json:"cover_available"`
	ThumbnailURL   string    `json:"thumbnail_url"`
	MediaCount     int       `json:"media_count"`
}

type EventPage struct {
	Events     []EventSummary `json:"events"`
	NextCursor *string        `json:"next_cursor" tstype:"string | null,required"`
}

type NewForYouResponse struct {
	Events []EventSummary `json:"events"`
}

type Event struct {
	ID             string    `json:"id"`
	PublicationID  string    `json:"publication_id"`
	Title          string    `json:"title"`
	Description    string    `json:"description"`
	CommittedAt    time.Time `json:"committed_at"`
	CoverMediaID   string    `json:"cover_media_id"`
	CoverAvailable bool      `json:"cover_available"`
	MediaCount     int       `json:"media_count"`
	Media          []Media   `json:"media"`
	NextCursor     *string   `json:"next_cursor" tstype:"string | null,required"`
}

type thumbnailSource interface {
	Thumbnail(ctx context.Context, assetID uuid.UUID) (immich.MediaResponse, error)
}

type Service struct {
	db     *bun.DB
	immich thumbnailSource
}

func New(db *bun.DB, source thumbnailSource) *Service {
	return &Service{db: db, immich: source}
}

type cursorKind string

const (
	cursorKindPhotos     cursorKind = "photos"
	cursorKindFavorites  cursorKind = "favorites"
	cursorKindEvents     cursorKind = "events"
	cursorKindEventMedia cursorKind = "event_media"
)

type cursor struct {
	Kind          cursorKind `json:"k"`
	Sort          string     `json:"s"`
	ID            string     `json:"i"`
	ResourceID    string     `json:"r,omitempty"`
	PublicationID string     `json:"p,omitempty"`
}

func pageSize(raw string) (int, error) {
	if raw == "" {
		return defaultPageSize, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > maxPageSize {
		return 0, ErrInvalidCursor
	}
	return value, nil
}

func decodeCursor(raw string, kind cursorKind) (*cursor, error) {
	if raw == "" {
		return nil, nil
	}
	contents, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	var value cursor
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return nil, ErrInvalidCursor
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidCursor
	}
	id, err := uuid.Parse(value.ID)
	if value.Kind != kind || err != nil || id == uuid.Nil {
		return nil, ErrInvalidCursor
	}
	switch kind {
	case cursorKindPhotos, cursorKindFavorites:
		if value.ResourceID != "" || value.PublicationID != "" {
			return nil, ErrInvalidCursor
		}
	case cursorKindEvents:
		publicationID, publicationErr := uuid.Parse(value.PublicationID)
		if publicationErr != nil || publicationID == uuid.Nil || value.ResourceID != "" {
			return nil, ErrInvalidCursor
		}
	case cursorKindEventMedia:
		resourceID, resourceErr := uuid.Parse(value.ResourceID)
		publicationID, publicationErr := uuid.Parse(value.PublicationID)
		if resourceErr != nil || resourceID == uuid.Nil || publicationErr != nil || publicationID == uuid.Nil || value.Sort != "" {
			return nil, ErrInvalidCursor
		}
	}
	return &value, nil
}

func encodeCursor(value cursor) *string {
	contents, _ := json.Marshal(value)
	encoded := base64.RawURLEncoding.EncodeToString(contents)
	return &encoded
}

const validPlacements = `
	SELECT placement.event_id, placement.publication_id, placement.published_moment_id,
	       placement.media_item_id, placement.position, current.committed_at AS publication_committed_at,
	       published.media_type, published.width, published.height, published.local_date_time,
	       media.availability = 'current' AS available,
	       (moment.cover_media_item_id = placement.media_item_id) IS TRUE AS is_cover
	FROM current_published_placements AS placement
	JOIN events AS event ON event.id = placement.event_id AND event.lifecycle = 'published'
	JOIN current_published_events AS current
	  ON current.event_id = placement.event_id AND current.publication_id = placement.publication_id
	JOIN current_audience_entitlements AS entitlement
	  ON entitlement.event_id = placement.event_id
	 AND entitlement.publication_id = placement.publication_id
	 AND entitlement.media_item_id = placement.media_item_id
	 AND entitlement.recipient_access_generation_id = ?
	JOIN published_media_placements AS published
	  ON published.published_moment_id = placement.published_moment_id
	 AND published.media_item_id = placement.media_item_id
	JOIN published_moments AS moment ON moment.id = placement.published_moment_id
	JOIN media_items AS media ON media.id = placement.media_item_id
	WHERE NOT EXISTS (
		SELECT 1 FROM content_withdrawals
		WHERE restored_at IS NULL AND target_kind = 'event' AND target_id = placement.event_id
	) AND NOT EXISTS (
		SELECT 1 FROM content_withdrawals
		WHERE restored_at IS NULL AND target_kind = 'moment' AND target_id = moment.draft_moment_id
	) AND NOT EXISTS (
		SELECT 1 FROM content_withdrawals
		WHERE restored_at IS NULL AND target_kind = 'media' AND target_id = placement.media_item_id
	)
`

func ensureActor(ctx context.Context, db bun.IDB, actor setup.SessionActor) error {
	var valid bool
	if err := db.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM sessions AS session
		JOIN people AS person
		  ON person.id = session.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
		JOIN recipient_access_generations AS access
		  ON access.id = session.recipient_access_generation_id
		 AND access.person_id = session.person_id AND access.is_current AND access.state = 'completed'
		JOIN system_settings AS settings
		  ON settings.id = 1 AND settings.setup_complete AND settings.security_epoch = session.security_epoch
		WHERE session.id = ? AND session.person_id = ? AND session.recipient_access_generation_id = ?
		  AND session.revoked_at IS NULL
		  AND ((session.session_type = 'trusted' AND session.idle_expires_at > now())
		    OR (session.session_type = 'public' AND session.absolute_expires_at > now()))
	)`, actor.SessionID, actor.PersonID, actor.AccessID).Scan(ctx, &valid); err != nil {
		return err
	}
	if !valid {
		return ErrNotFound
	}
	return nil
}

func (s *Service) Photos(ctx context.Context, actor setup.SessionActor, rawLimit, rawCursor string, favorites bool) (MediaPage, error) {
	limit, err := pageSize(rawLimit)
	if err != nil {
		return MediaPage{}, err
	}
	kind := cursorKindPhotos
	if favorites {
		kind = cursorKindFavorites
	}
	position, err := decodeCursor(rawCursor, kind)
	if err != nil {
		return MediaPage{}, err
	}
	response := MediaPage{Media: make([]Media, 0)}
	err = s.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if err := ensureActor(ctx, tx, actor); err != nil {
			return err
		}
		favoriteJoin := ""
		if favorites {
			favoriteJoin = `JOIN favorites AS favorite ON favorite.media_item_id = valid.media_item_id AND favorite.recipient_person_id = ? AND favorite.is_current`
		}
		cursorFilter := ""
		args := []any{actor.AccessID}
		if favorites {
			args = append(args, actor.PersonID)
		}
		if position != nil {
			cursorFilter = `WHERE (COALESCE(valid.local_date_time, ''), valid.media_item_id) < (?, ?::uuid)`
			args = append(args, position.Sort, position.ID)
		}
		args = append(args, limit+1)
		query := fmt.Sprintf(`WITH valid AS (%s), unique_media AS (
			SELECT DISTINCT ON (valid.media_item_id) valid.media_item_id, valid.media_type,
			       valid.width, valid.height, valid.local_date_time, valid.available
			FROM valid %s
			ORDER BY valid.media_item_id, valid.publication_committed_at DESC, valid.event_id DESC
		)
		SELECT media_item_id AS id, media_type, width, height, local_date_time, available
		FROM unique_media AS valid %s
		ORDER BY COALESCE(valid.local_date_time, '') DESC, valid.media_item_id DESC LIMIT ?`, validPlacements, favoriteJoin, cursorFilter)
		if err := tx.NewRaw(query, args...).Scan(ctx, &response.Media); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return MediaPage{}, err
	}
	for index := range response.Media {
		if response.Media[index].Available {
			response.Media[index].ThumbnailURL = "/api/me/media/" + response.Media[index].ID + "/thumbnail"
		}
	}
	if len(response.Media) > limit {
		last := response.Media[limit-1]
		sortValue := ""
		if last.LocalDateTime != nil {
			sortValue = *last.LocalDateTime
		}
		response.NextCursor = encodeCursor(cursor{Kind: kind, Sort: sortValue, ID: last.ID})
		response.Media = response.Media[:limit]
	}
	return response, nil
}

func (s *Service) Events(ctx context.Context, actor setup.SessionActor, rawLimit, rawCursor string) (EventPage, error) {
	limit, err := pageSize(rawLimit)
	if err != nil {
		return EventPage{}, err
	}
	position, err := decodeCursor(rawCursor, cursorKindEvents)
	if err != nil {
		return EventPage{}, err
	}
	response := EventPage{Events: make([]EventSummary, 0)}
	err = s.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if err := ensureActor(ctx, tx, actor); err != nil {
			return err
		}
		filter := ""
		args := []any{actor.AccessID}
		if position != nil {
			if _, err := time.Parse(time.RFC3339Nano, position.Sort); err != nil {
				return ErrInvalidCursor
			}
			var current bool
			if err := tx.NewRaw(fmt.Sprintf(`WITH valid AS (%s)
				SELECT EXISTS (
					SELECT 1 FROM valid
					JOIN current_published_events AS current
					  ON current.event_id = valid.event_id AND current.publication_id = valid.publication_id
					WHERE current.event_id = ? AND current.publication_id = ?
					  AND current.committed_at = ?::timestamptz
				)`, validPlacements), actor.AccessID, position.ID, position.PublicationID, position.Sort).Scan(ctx, &current); err != nil {
				return err
			}
			if !current {
				return ErrInvalidCursor
			}
			filter = `WHERE (current.committed_at, current.event_id) < (?::timestamptz, ?::uuid)`
			args = append(args, position.Sort, position.ID)
		}
		args = append(args, limit+1)
		query := fmt.Sprintf(`WITH valid AS (%s), authorized_events AS (
			SELECT valid.event_id, count(DISTINCT valid.media_item_id) AS media_count,
			       (array_agg(valid.media_item_id ORDER BY valid.available DESC, valid.is_cover DESC, valid.position))[1] AS cover_media_id,
			       (array_agg(valid.width ORDER BY valid.available DESC, valid.is_cover DESC, valid.position))[1] AS cover_width,
			       (array_agg(valid.height ORDER BY valid.available DESC, valid.is_cover DESC, valid.position))[1] AS cover_height,
			       (array_agg(valid.available ORDER BY valid.available DESC, valid.is_cover DESC, valid.position))[1] AS cover_available
			FROM valid GROUP BY valid.event_id
		)
		SELECT current.event_id AS id, current.publication_id, current.title,
		       current.description, current.committed_at, authorized.cover_media_id,
		       authorized.cover_width, authorized.cover_height,
		       authorized.cover_available, authorized.media_count
		FROM authorized_events AS authorized
		JOIN current_published_events AS current ON current.event_id = authorized.event_id
		%s ORDER BY current.committed_at DESC, current.event_id DESC LIMIT ?`, validPlacements, filter)
		return tx.NewRaw(query, args...).Scan(ctx, &response.Events)
	})
	if err != nil {
		return EventPage{}, err
	}
	for index := range response.Events {
		if response.Events[index].CoverAvailable {
			response.Events[index].ThumbnailURL = "/api/me/media/" + response.Events[index].CoverMediaID + "/thumbnail"
		}
	}
	if len(response.Events) > limit {
		last := response.Events[limit-1]
		response.NextCursor = encodeCursor(cursor{
			Kind: cursorKindEvents, Sort: last.CommittedAt.Format(time.RFC3339Nano),
			ID: last.ID, PublicationID: last.PublicationID,
		})
		response.Events = response.Events[:limit]
	}
	return response, nil
}

func (s *Service) NewForYou(ctx context.Context, actor setup.SessionActor) (NewForYouResponse, error) {
	response := NewForYouResponse{Events: make([]EventSummary, 0)}
	err := s.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if err := ensureActor(ctx, tx, actor); err != nil {
			return err
		}
		query := fmt.Sprintf(`WITH valid AS (%s), authorized_events AS (
			SELECT valid.event_id, valid.publication_id,
			       count(DISTINCT valid.media_item_id) AS media_count,
			       (array_agg(valid.media_item_id ORDER BY valid.available DESC, valid.is_cover DESC, valid.position))[1] AS cover_media_id,
			       (array_agg(valid.width ORDER BY valid.available DESC, valid.is_cover DESC, valid.position))[1] AS cover_width,
			       (array_agg(valid.height ORDER BY valid.available DESC, valid.is_cover DESC, valid.position))[1] AS cover_height,
			       (array_agg(valid.available ORDER BY valid.available DESC, valid.is_cover DESC, valid.position))[1] AS cover_available
			FROM valid GROUP BY valid.event_id, valid.publication_id
		)
		SELECT current.event_id AS id, current.publication_id, current.title,
		       current.description, current.committed_at, authorized.cover_media_id,
		       authorized.cover_width, authorized.cover_height,
		       authorized.cover_available, authorized.media_count
		FROM new_for_you_entries AS entry
		JOIN authorized_events AS authorized ON authorized.publication_id = entry.publication_id
		JOIN current_published_events AS current
		  ON current.event_id = authorized.event_id AND current.publication_id = authorized.publication_id
		WHERE entry.recipient_access_generation_id = ? AND entry.seen_at IS NULL
		ORDER BY current.committed_at DESC, current.event_id DESC LIMIT ?`, validPlacements)
		return tx.NewRaw(query, actor.AccessID, actor.AccessID, maxPageSize).Scan(ctx, &response.Events)
	})
	if err != nil {
		return NewForYouResponse{}, err
	}
	for index := range response.Events {
		if response.Events[index].CoverAvailable {
			response.Events[index].ThumbnailURL = "/api/me/media/" + response.Events[index].CoverMediaID + "/thumbnail"
		}
	}
	return response, nil
}

func (s *Service) MarkSeen(ctx context.Context, actor setup.SessionActor, publicationID uuid.UUID) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := ensureActor(ctx, tx, actor); err != nil {
			return err
		}
		query := fmt.Sprintf(`WITH valid AS (%s)
			UPDATE new_for_you_entries AS entry SET seen_at = now()
			WHERE entry.recipient_access_generation_id = ? AND entry.publication_id = ? AND entry.seen_at IS NULL
			  AND EXISTS (
				SELECT 1 FROM valid
				WHERE valid.publication_id = entry.publication_id
			  )`, validPlacements)
		result, err := tx.NewRaw(query, actor.AccessID, actor.AccessID, publicationID).Exec(ctx)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrNotFound
		}
		return nil
	})
}

func (s *Service) Event(ctx context.Context, actor setup.SessionActor, eventID uuid.UUID, rawLimit, rawCursor string) (Event, error) {
	limit, err := pageSize(rawLimit)
	if err != nil {
		return Event{}, err
	}
	position, err := decodeCursor(rawCursor, cursorKindEventMedia)
	if err != nil {
		return Event{}, err
	}
	if position != nil && position.ResourceID != eventID.String() {
		return Event{}, ErrInvalidCursor
	}
	var response Event
	err = s.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if err := ensureActor(ctx, tx, actor); err != nil {
			return err
		}
		var cover uuid.UUID
		if err := tx.NewRaw(fmt.Sprintf(`WITH valid AS (%s)
			SELECT current.event_id AS id, current.publication_id, current.title, current.description,
			       current.committed_at, count(*) AS media_count,
			       (array_agg(valid.media_item_id ORDER BY valid.available DESC, valid.is_cover DESC, valid.position))[1] AS cover,
			       (array_agg(valid.available ORDER BY valid.available DESC, valid.is_cover DESC, valid.position))[1] AS cover_available
			FROM valid JOIN current_published_events AS current ON current.event_id = valid.event_id
			WHERE valid.event_id = ? GROUP BY current.event_id, current.publication_id,
			current.title, current.description, current.committed_at`, validPlacements), actor.AccessID, eventID).Scan(ctx,
			&response.ID, &response.PublicationID, &response.Title, &response.Description,
			&response.CommittedAt, &response.MediaCount, &cover, &response.CoverAvailable); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		response.CoverMediaID = cover.String()
		filter := `WHERE valid.event_id = ?`
		args := []any{actor.AccessID, eventID}
		if position != nil {
			if position.PublicationID != response.PublicationID {
				return ErrInvalidCursor
			}
			var current bool
			if err := tx.NewRaw(fmt.Sprintf(`WITH valid AS (%s)
				SELECT EXISTS (
					SELECT 1 FROM valid WHERE valid.event_id = ? AND valid.publication_id = ?
					  AND valid.media_item_id = ?
				)`, validPlacements), actor.AccessID, eventID, response.PublicationID, position.ID).Scan(ctx, &current); err != nil {
				return err
			}
			if !current {
				return ErrInvalidCursor
			}
			filter += ` AND (valid.position, valid.media_item_id) > (
				SELECT cursor.position, cursor.media_item_id FROM valid AS cursor
				WHERE cursor.event_id = ? AND cursor.publication_id = ? AND cursor.media_item_id = ?
			)`
			args = append(args, eventID, response.PublicationID, position.ID)
		}
		args = append(args, limit+1)
		query := fmt.Sprintf(`WITH valid AS (%s)
			SELECT valid.media_item_id AS id, valid.media_type, valid.width, valid.height,
			       valid.local_date_time, valid.available, valid.position
			FROM valid %s ORDER BY valid.position, valid.media_item_id LIMIT ?`, validPlacements, filter)
		type mediaPosition struct {
			Media
			Position int `bun:"position"`
		}
		var rows []mediaPosition
		if err := tx.NewRaw(query, args...).Scan(ctx, &rows); err != nil {
			return err
		}
		response.Media = make([]Media, 0, min(len(rows), limit))
		for index, row := range rows {
			if index == limit {
				prior := rows[index-1]
				response.NextCursor = encodeCursor(cursor{
					Kind: cursorKindEventMedia, ID: prior.ID,
					ResourceID: eventID.String(), PublicationID: response.PublicationID,
				})
				break
			}
			if row.Available {
				row.ThumbnailURL = "/api/me/media/" + row.ID + "/thumbnail"
			}
			response.Media = append(response.Media, row.Media)
		}
		return nil
	})
	return response, err
}

func (s *Service) Thumbnail(ctx context.Context, actor setup.SessionActor, mediaID uuid.UUID) (immich.MediaResponse, error) {
	if s.immich == nil {
		return immich.MediaResponse{}, ErrNotFound
	}
	var assetID uuid.UUID
	err := s.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if err := ensureActor(ctx, tx, actor); err != nil {
			return err
		}
		query := fmt.Sprintf(`WITH valid AS (%s)
			SELECT backing.immich_asset_id FROM valid
			JOIN media_backings AS backing ON backing.media_item_id = valid.media_item_id AND backing.active
			WHERE valid.media_item_id = ? AND valid.available LIMIT 1`, validPlacements)
		if err := tx.NewRaw(query, actor.AccessID, mediaID).Scan(ctx, &assetID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		return nil
	})
	if err != nil {
		return immich.MediaResponse{}, err
	}
	response, err := s.immich.Thumbnail(ctx, assetID)
	if errors.Is(err, immich.ErrNotFound) {
		return immich.MediaResponse{}, ErrNotFound
	}
	return response, err
}
