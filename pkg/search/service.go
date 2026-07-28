// Package search performs authorization-first Recipient search over current Publication projections.
package search

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/uptrace/bun"
)

const (
	maxEventResults  = 100
	maxPhotoResults  = 200
	maxPersonResults = 100
)

var (
	ErrInvalidRequest = errors.New("invalid search request")
	ErrNotFound       = errors.New("search unavailable")
)

// DateFilter is one explicit, local-calendar date constraint.
type DateFilter struct {
	Kind      string  `json:"kind" validate:"required,oneof=year month date range"`
	Year      *int    `json:"year,omitempty" tstype:"number | null"`
	Month     *string `json:"month,omitempty" tstype:"string | null"`
	Date      *string `json:"date,omitempty" tstype:"string | null"`
	StartDate *string `json:"start_date,omitempty" tstype:"string | null"`
	EndDate   *string `json:"end_date,omitempty" tstype:"string | null"`
}

// Request keeps private free text in a POST body and transient browser state.
type Request struct {
	Query string      `json:"query,omitempty" validate:"max=200" mod:"trim"`
	Date  *DateFilter `json:"date,omitempty" tstype:"DateFilter | null"`
}

// Media is a path-free, authorized search result.
type Media struct {
	ID            string  `json:"id"`
	MediaType     string  `json:"media_type"`
	Width         *int    `json:"width" tstype:"number | null,required"`
	Height        *int    `json:"height" tstype:"number | null,required"`
	LocalDateTime *string `json:"local_date_time" tstype:"string | null,required"`
	Available     bool    `json:"available"`
	ThumbnailURL  string  `json:"thumbnail_url"`
	PreviewURL    string  `json:"preview_url"`
	VideoURL      string  `json:"video_url"`
	OriginalURL   string  `json:"original_url"`
}

// EventResult contains totals, spans, and a cover computed only from matching authorized Media.
type EventResult struct {
	ID             string  `json:"id"`
	Title          string  `json:"title"`
	Description    string  `json:"description"`
	MediaCount     int     `json:"media_count"`
	DateStart      *string `json:"date_start" tstype:"string | null,required"`
	DateEnd        *string `json:"date_end" tstype:"string | null,required"`
	CoverMediaID   string  `json:"cover_media_id"`
	CoverWidth     *int    `json:"cover_width" tstype:"number | null,required"`
	CoverHeight    *int    `json:"cover_height" tstype:"number | null,required"`
	CoverAvailable bool    `json:"cover_available"`
	ThumbnailURL   string  `json:"thumbnail_url"`
}

// PersonAttendanceResult states only confirmed Attendance in an authorized part of an Event.
type PersonAttendanceResult struct {
	PersonID   string `json:"person_id"`
	PersonName string `json:"person_name"`
	EventID    string `json:"event_id"`
	EventTitle string `json:"event_title"`
}

// Response groups Events and Shared content separately and deduplicates global Photos.
type Response struct {
	Events      []EventResult            `json:"events"`
	Shared      []Media                  `json:"shared"`
	Photos      []Media                  `json:"photos"`
	People      []PersonAttendanceResult `json:"people"`
	TotalEvents int                      `json:"total_events"`
	TotalPhotos int                      `json:"total_photos"`
	HasMore     bool                     `json:"has_more"`
}

type Service struct{ db *bun.DB }

func New(db *bun.DB) *Service { return &Service{db: db} }

type bounds struct {
	start time.Time
	end   time.Time
}

func parseRequest(request Request) ([]string, *bounds, error) {
	query := strings.TrimSpace(request.Query)
	if utf8.RuneCountInString(query) > 200 {
		return nil, nil, ErrInvalidRequest
	}
	terms := tokenize(query)
	var dateBounds *bounds
	if request.Date != nil {
		parsed, err := parseDateFilter(*request.Date)
		if err != nil {
			return nil, nil, err
		}
		dateBounds = &parsed
	}
	if len(terms) == 0 && dateBounds == nil {
		return nil, nil, ErrInvalidRequest
	}
	return terms, dateBounds, nil
}

func tokenize(value string) []string {
	return strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.IsMark(r)
	})
}

func parseDateFilter(filter DateFilter) (bounds, error) {
	only := func(values ...bool) bool {
		count := 0
		for _, value := range values {
			if value {
				count++
			}
		}
		return count == 1
	}
	hasYear := filter.Year != nil
	hasMonth := filter.Month != nil
	hasDate := filter.Date != nil
	hasRange := filter.StartDate != nil || filter.EndDate != nil
	if !only(hasYear, hasMonth, hasDate, hasRange) {
		return bounds{}, ErrInvalidRequest
	}
	switch filter.Kind {
	case "year":
		if !hasYear || *filter.Year < 1 || *filter.Year > 9999 {
			return bounds{}, ErrInvalidRequest
		}
		start := time.Date(*filter.Year, 1, 1, 0, 0, 0, 0, time.UTC)
		return bounds{start: start, end: start.AddDate(1, 0, 0).AddDate(0, 0, -1)}, nil
	case "month":
		if !hasMonth {
			return bounds{}, ErrInvalidRequest
		}
		start, err := time.Parse("2006-01", *filter.Month)
		if err != nil {
			return bounds{}, ErrInvalidRequest
		}
		return bounds{start: start, end: start.AddDate(0, 1, -1)}, nil
	case "date":
		if !hasDate {
			return bounds{}, ErrInvalidRequest
		}
		date, err := time.Parse(time.DateOnly, *filter.Date)
		if err != nil {
			return bounds{}, ErrInvalidRequest
		}
		return bounds{start: date, end: date}, nil
	case "range":
		if filter.StartDate == nil || filter.EndDate == nil {
			return bounds{}, ErrInvalidRequest
		}
		start, startErr := time.Parse(time.DateOnly, *filter.StartDate)
		end, endErr := time.Parse(time.DateOnly, *filter.EndDate)
		if startErr != nil || endErr != nil || end.Before(start) {
			return bounds{}, ErrInvalidRequest
		}
		return bounds{start: start, end: end}, nil
	default:
		return bounds{}, ErrInvalidRequest
	}
}

const authorizedDocuments = `
	SELECT document.event_id, document.publication_id, document.media_item_id,
	       document.search_vector, document.normalized_search_text, document.capture_date,
	       current.title, current.description, current.committed_at,
	       placement.published_moment_id, placement.position,
	       published.media_type, published.width, published.height, published.local_date_time,
	       media.availability = 'current' AS available
	FROM published_search_documents AS document
	JOIN current_audience_entitlements AS entitlement
	  ON entitlement.event_id = document.event_id
	 AND entitlement.publication_id = document.publication_id
	 AND entitlement.recipient_access_generation_id = document.recipient_access_generation_id
	 AND entitlement.media_item_id = document.media_item_id
	JOIN current_published_events AS current
	  ON current.event_id = document.event_id AND current.publication_id = document.publication_id
	JOIN events AS event ON event.id = current.event_id AND event.lifecycle = 'published'
	JOIN current_published_placements AS placement
	  ON placement.event_id = document.event_id
	 AND placement.publication_id = document.publication_id
	 AND placement.media_item_id = document.media_item_id
	JOIN published_media_placements AS published
	  ON published.published_moment_id = placement.published_moment_id
	 AND published.media_item_id = placement.media_item_id
	JOIN published_moments AS moment ON moment.id = placement.published_moment_id
	JOIN media_items AS media ON media.id = document.media_item_id
	WHERE document.recipient_access_generation_id = ?
	  AND NOT content_is_withdrawn(placement.event_id, moment.draft_moment_id, placement.media_item_id)
`

const discoverablePerson = `
	SELECT person.id, person.display_name,
	       memento_normalize_search_text(person.display_name) AS normalized_name
	FROM published_attendance AS attendance
	JOIN people AS person
	  ON person.id = attendance.person_id
	 AND person.archived_at IS NULL AND person.merged_at IS NULL
	WHERE attendance.published_moment_id = authorized.published_moment_id
	  AND person.id <> ?
	  AND EXISTS (
		SELECT 1
		FROM visibility_circle_members AS own_membership
		JOIN visibility_circles AS circle
		  ON circle.id = own_membership.circle_id AND circle.archived_at IS NULL
		JOIN visibility_circle_members AS shared_membership
		  ON shared_membership.circle_id = circle.id AND shared_membership.person_id = person.id
		WHERE own_membership.person_id = ?
	  )
`

func matchingCTE(actor setup.SessionActor, terms []string, dateBounds *bounds) (string, []any) {
	args := []any{actor.AccessID}
	predicates := make([]string, 0, len(terms)+1)
	for _, term := range terms {
		long := utf8.RuneCountInString(term) >= 5
		predicate := `(authorized.search_vector @@ to_tsquery('simple', memento_normalize_search_text(?) || ':*')`
		args = append(args, term)
		if long {
			predicate += ` OR public.strict_word_similarity(memento_normalize_search_text(?), authorized.normalized_search_text) >= 0.3`
			args = append(args, term)
		}
		predicate += ` OR EXISTS (` + discoverablePerson + ` AND (` + personTermPredicate(long, "memento_normalize_search_text(person.display_name)") + `)))`
		args = append(args, actor.PersonID, actor.PersonID, term)
		if long {
			args = append(args, term)
		}
		predicates = append(predicates, predicate)
	}
	if dateBounds != nil {
		predicates = append(predicates, `authorized.capture_date BETWEEN ?::date AND ?::date`)
		args = append(args, dateBounds.start.Format(time.DateOnly), dateBounds.end.Format(time.DateOnly))
	}
	where := "TRUE"
	if len(predicates) > 0 {
		where = strings.Join(predicates, " AND ")
	}
	return `WITH authorized AS (` + authorizedDocuments + `), matched AS (
		SELECT authorized.* FROM authorized WHERE ` + where + `
	)`, args
}

func personTermPredicate(long bool, normalizedExpression string) string {
	predicate := `to_tsvector('simple', ` + normalizedExpression + `) @@ to_tsquery('simple', memento_normalize_search_text(?) || ':*')`
	if long {
		predicate += ` OR public.strict_word_similarity(memento_normalize_search_text(?), ` + normalizedExpression + `) >= 0.3`
	}
	return predicate
}

func ensureActor(ctx context.Context, db bun.IDB, actor setup.SessionActor) error {
	var valid bool
	if err := db.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM sessions AS session
		JOIN people AS person ON person.id = session.person_id
		  AND person.archived_at IS NULL AND person.merged_at IS NULL
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

// Search returns only current authorized candidates and computes every observable value from that set.
func (s *Service) Search(ctx context.Context, actor setup.SessionActor, request Request) (Response, error) {
	terms, dateBounds, err := parseRequest(request)
	if err != nil {
		return Response{}, err
	}
	response := Response{
		Events: make([]EventResult, 0), Shared: make([]Media, 0),
		Photos: make([]Media, 0), People: make([]PersonAttendanceResult, 0),
	}
	err = s.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if err := ensureActor(ctx, tx, actor); err != nil {
			return err
		}
		cte, args := matchingCTE(actor, terms, dateBounds)
		eventQuery := cte + `, ranked_events AS (
			SELECT matched.event_id, matched.title, matched.description,
			       count(DISTINCT matched.media_item_id)::integer AS media_count,
			       min(matched.capture_date)::text AS date_start,
			       max(matched.capture_date)::text AS date_end,
			       (array_agg(matched.media_item_id ORDER BY matched.available DESC, matched.position, matched.media_item_id))[1] AS cover_media_id,
			       (array_agg(matched.width ORDER BY matched.available DESC, matched.position, matched.media_item_id))[1] AS cover_width,
			       (array_agg(matched.height ORDER BY matched.available DESC, matched.position, matched.media_item_id))[1] AS cover_height,
			       (array_agg(matched.available ORDER BY matched.available DESC, matched.position, matched.media_item_id))[1] AS cover_available,
			       max(matched.committed_at) AS committed_at
			FROM matched GROUP BY matched.event_id, matched.title, matched.description
		)
		SELECT event_id AS id, title, description, media_count, date_start, date_end,
		       cover_media_id, cover_width, cover_height, cover_available, committed_at,
		       count(*) OVER ()::integer AS total_events
		FROM ranked_events ORDER BY committed_at DESC, event_id DESC LIMIT ?`
		type eventRow struct {
			EventResult
			CommittedAt time.Time `bun:"committed_at"`
			TotalEvents int       `bun:"total_events"`
		}
		var eventRows []eventRow
		if err := tx.NewRaw(eventQuery, append(args, maxEventResults+1)...).Scan(ctx, &eventRows); err != nil {
			return fmt.Errorf("search Events: %w", err)
		}
		if len(eventRows) > 0 {
			response.TotalEvents = eventRows[0].TotalEvents
		}
		if len(eventRows) > maxEventResults {
			response.HasMore = true
			eventRows = eventRows[:maxEventResults]
		}
		for _, row := range eventRows {
			if row.CoverAvailable {
				row.ThumbnailURL = "/api/me/media/" + row.CoverMediaID + "/thumbnail"
			}
			response.Events = append(response.Events, row.EventResult)
		}

		photoQuery := cte + `, unique_photos AS (
			SELECT DISTINCT ON (matched.media_item_id)
			       matched.media_item_id AS id, matched.media_type, matched.width, matched.height,
			       matched.local_date_time, matched.available, matched.committed_at
			FROM matched
			ORDER BY matched.media_item_id, matched.committed_at DESC, matched.event_id DESC
		), ordered_photos AS (
			SELECT *, count(*) OVER ()::integer AS total_photos
			FROM unique_photos
		)
		SELECT * FROM ordered_photos
		ORDER BY COALESCE(local_date_time, '') DESC, id DESC LIMIT ?`
		type photoRow struct {
			Media
			CommittedAt time.Time `bun:"committed_at"`
			TotalPhotos int       `bun:"total_photos"`
		}
		var photos []photoRow
		if err := tx.NewRaw(photoQuery, append(args, maxPhotoResults+1)...).Scan(ctx, &photos); err != nil {
			return fmt.Errorf("search Photos: %w", err)
		}
		if len(photos) > 0 {
			response.TotalPhotos = photos[0].TotalPhotos
		}
		if len(photos) > maxPhotoResults {
			response.HasMore = true
			photos = photos[:maxPhotoResults]
		}
		for _, row := range photos {
			setMediaURLs(&row.Media)
			response.Photos = append(response.Photos, row.Media)
		}

		if len(terms) > 0 {
			personPredicates := make([]string, 0, len(terms))
			for _, term := range terms {
				long := utf8.RuneCountInString(term) >= 5
				personPredicates = append(personPredicates, `(`+personTermPredicate(long, "person.normalized_name")+`)`)
			}
			peopleQuery := cte + `
				SELECT DISTINCT person.id AS person_id, person.display_name AS person_name,
				       authorized.event_id, authorized.title AS event_title
				FROM matched AS authorized
				JOIN LATERAL (` + discoverablePerson + `) AS person ON TRUE
				WHERE ` + strings.Join(personPredicates, " AND ") + `
				ORDER BY person.display_name, person.id, authorized.event_id
				LIMIT ?`
			// The matching CTE has its own placeholders, followed by the lateral actor and Person terms.
			personArgs := append(append([]any(nil), args...), actor.PersonID, actor.PersonID)
			for _, term := range terms {
				personArgs = append(personArgs, term)
				if utf8.RuneCountInString(term) >= 5 {
					personArgs = append(personArgs, term)
				}
			}
			personArgs = append(personArgs, maxPersonResults+1)
			if err := tx.NewRaw(peopleQuery, personArgs...).Scan(ctx, &response.People); err != nil {
				return fmt.Errorf("search People: %w", err)
			}
			if len(response.People) > maxPersonResults {
				response.HasMore = true
				response.People = response.People[:maxPersonResults]
			}
		}
		return nil
	})
	if err != nil {
		return Response{}, err
	}
	return response, nil
}

func setMediaURLs(media *Media) {
	if !media.Available {
		return
	}
	base := "/api/me/media/" + media.ID
	media.ThumbnailURL = base + "/thumbnail"
	media.PreviewURL = base + "/preview"
	media.OriginalURL = base + "/original"
	if media.MediaType == "video" {
		media.VideoURL = base + "/video"
	}
}
