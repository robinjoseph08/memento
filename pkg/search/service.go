// Package search performs authorization-first Recipient search over current Publication projections.
package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/uptrace/bun"
	"golang.org/x/text/unicode/norm"
)

const (
	maxEventResults        = 100
	maxPhotoResults        = 200
	maxPersonResults       = 100
	maxSearchTerms         = 12
	searchStatementTimeout = "3s"
)

var (
	ErrInvalidRequest = errors.New("invalid search request")
	ErrTooManyTerms   = fmt.Errorf("%w: too many unique search terms", ErrInvalidRequest)
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
	Date  *DateFilter `json:"date,omitempty" tstype:"({ kind: 'year'; year: number } | { kind: 'month'; month: string } | { kind: 'date'; date: string } | { kind: 'range'; start_date: string; end_date: string }) | null"`
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

// EventResult contains authorized totals and spans. Its cover prefers matching
// authorized Media, then falls back to authorized Event Media for a range-only match.
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

// Response groups matching Media by Event and deduplicates global Photos.
type Response struct {
	Events      []EventResult            `json:"events"`
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
	if len(terms) > maxSearchTerms {
		return nil, nil, ErrTooManyTerms
	}
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
	candidates := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.IsMark(r)
	})
	terms := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		containsWordCharacter := false
		for _, r := range candidate {
			if unicode.IsLetter(r) || unicode.IsNumber(r) {
				containsWordCharacter = true
				break
			}
		}
		if !containsWordCharacter {
			continue
		}
		key := normalizedTermKey(candidate)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		terms = append(terms, candidate)
	}
	return terms
}

func normalizedTermKey(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return unicode.ToLower(r)
	}, norm.NFD.String(value))
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
	       placement.published_moment_id, moment.proposed_day AS event_day, placement.position,
	       published.media_type, published.width, published.height, published.local_date_time,
	       media.availability = 'current' AS available
	FROM current_audience_entitlements AS entitlement
	JOIN published_search_documents AS document
	  ON document.event_id = entitlement.event_id
	 AND document.publication_id = entitlement.publication_id
	 AND document.media_item_id = entitlement.media_item_id
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
	WHERE entitlement.recipient_access_generation_id = ?
	  AND NOT content_is_withdrawn(placement.event_id, moment.draft_moment_id, placement.media_item_id)
	UNION ALL
	SELECT NULL::uuid AS event_id, document.publication_id, document.media_item_id,
	       document.search_vector, document.normalized_search_text, document.capture_date,
	       current.title, current.description, current.committed_at,
	       NULL::uuid AS published_moment_id, current.proposed_day AS event_day, 0 AS position,
	       current.media_type, current.width, current.height, current.local_date_time,
	       media.availability = 'current' AS available
	FROM published_loose_search_documents AS document
	JOIN current_published_loose_items AS current
	  ON current.loose_item_id = document.loose_item_id AND current.publication_id = document.publication_id
	JOIN current_media_entitlements AS entitlement
	  ON entitlement.origin_kind = 'loose_item' AND entitlement.origin_id = current.loose_item_id
	 AND entitlement.publication_id = current.publication_id AND entitlement.media_item_id = document.media_item_id
	JOIN media_items AS media ON media.id = document.media_item_id
	WHERE entitlement.recipient_access_generation_id = ?
`

const documentTypoPredicate = `(memento_normalize_search_text(?) OPERATOR(public.<<%) authorized.normalized_search_text
	AND public.strict_word_similarity(memento_normalize_search_text(?), authorized.normalized_search_text) >= 0.3)`

const discoverablePerson = `
	SELECT person.id, person.display_name,
	       memento_normalize_search_text(person.display_name) AS normalized_name
	FROM published_attendance AS attendance
	JOIN current_published_events AS current
	  ON current.publication_id = authorized.publication_id
	 AND current.attendance_projection_ready
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
	args := []any{actor.AccessID, actor.AccessID}
	textPredicates := make([]string, 0, len(terms))
	for _, term := range terms {
		long := utf8.RuneCountInString(term) >= 5
		predicate := `(authorized.search_vector @@ to_tsquery('simple', memento_normalize_search_text(?) || ':*')`
		args = append(args, term)
		if long {
			predicate += ` OR ` + documentTypoPredicate
			args = append(args, term, term)
		}
		predicate += ` OR EXISTS (` + discoverablePerson + ` AND (` + personTermPredicate(long, "memento_normalize_search_text(person.display_name)") + `)))`
		args = append(args, actor.PersonID, actor.PersonID, term)
		if long {
			args = append(args, term)
		}
		textPredicates = append(textPredicates, predicate)
	}
	textWhere := "TRUE"
	if len(textPredicates) > 0 {
		textWhere = strings.Join(textPredicates, " AND ")
	}
	cte := `WITH authorized AS (` + authorizedDocuments + `), text_matched AS (
		SELECT authorized.* FROM authorized WHERE ` + textWhere + `
	), authorized_event_ranges AS (
		SELECT event_id, min(event_day) AS date_start, max(event_day) AS date_end
		FROM authorized WHERE event_id IS NOT NULL GROUP BY event_id
	), matched AS (
		SELECT text_matched.* FROM text_matched`
	if dateBounds != nil {
		cte += ` WHERE text_matched.capture_date BETWEEN ?::date AND ?::date`
		args = append(args, dateBounds.start.Format(time.DateOnly), dateBounds.end.Format(time.DateOnly))
	}
	cte += `
	), matching_events AS (
		SELECT DISTINCT text_matched.event_id
		FROM text_matched
		JOIN authorized_event_ranges AS event_range ON event_range.event_id = text_matched.event_id`
	if dateBounds != nil {
		cte += `
		WHERE event_range.date_start <= ?::date AND event_range.date_end >= ?::date`
		args = append(args, dateBounds.end.Format(time.DateOnly), dateBounds.start.Format(time.DateOnly))
	}
	return cte + `
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
	current, err := setup.CurrentRecipientSession(ctx, db, actor)
	if err != nil {
		return err
	}
	if !current {
		return ErrNotFound
	}
	return nil
}

func setSearchStatementTimeout(ctx context.Context, tx bun.Tx) error {
	_, err := tx.ExecContext(ctx, `SET LOCAL statement_timeout = '`+searchStatementTimeout+`'`)
	return err
}

// Search returns only current authorized candidates and computes every observable value from that set.
func (s *Service) Search(ctx context.Context, actor setup.SessionActor, request Request) (Response, error) {
	terms, dateBounds, err := parseRequest(request)
	if err != nil {
		return Response{}, err
	}
	response := Response{
		Events: make([]EventResult, 0), Photos: make([]Media, 0),
		People: make([]PersonAttendanceResult, 0),
	}
	err = s.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if err := setSearchStatementTimeout(ctx, tx); err != nil {
			return err
		}
		if err := ensureActor(ctx, tx, actor); err != nil {
			return err
		}
		// Keep the indexable strict-word operator as a conservative prefilter,
		// then retain the stable 0.3 application threshold in the exact check.
		if _, err := tx.ExecContext(ctx, `SET LOCAL pg_trgm.strict_word_similarity_threshold = 0.2`); err != nil {
			return err
		}
		cte, args := matchingCTE(actor, terms, dateBounds)
		combinedQuery := cte + `, ranked_events AS (
			SELECT candidate.event_id, candidate.title, candidate.description,
			       count(DISTINCT matched.media_item_id)::integer AS media_count,
			       min(matched.capture_date)::text AS date_start,
			       max(matched.capture_date)::text AS date_end,
			       (array_agg(candidate.media_item_id ORDER BY (matched.media_item_id IS NOT NULL) DESC, candidate.available DESC, candidate.position, candidate.media_item_id))[1] AS cover_media_id,
			       (array_agg(candidate.width ORDER BY (matched.media_item_id IS NOT NULL) DESC, candidate.available DESC, candidate.position, candidate.media_item_id))[1] AS cover_width,
			       (array_agg(candidate.height ORDER BY (matched.media_item_id IS NOT NULL) DESC, candidate.available DESC, candidate.position, candidate.media_item_id))[1] AS cover_height,
			       (array_agg(candidate.available ORDER BY (matched.media_item_id IS NOT NULL) DESC, candidate.available DESC, candidate.position, candidate.media_item_id))[1] AS cover_available,
			       max(candidate.committed_at) AS committed_at
			FROM text_matched AS candidate
			JOIN matching_events ON matching_events.event_id = candidate.event_id
			LEFT JOIN matched ON matched.event_id = candidate.event_id
			 AND matched.publication_id = candidate.publication_id
			 AND matched.media_item_id = candidate.media_item_id
			GROUP BY candidate.event_id, candidate.title, candidate.description
		), event_rows AS (
			SELECT event_id AS id, title, description, media_count, date_start, date_end,
			       cover_media_id, cover_width, cover_height, cover_available, committed_at,
			       count(*) OVER ()::integer AS total_events
			FROM ranked_events ORDER BY committed_at DESC, event_id DESC LIMIT ?
		), unique_photos AS (
			SELECT DISTINCT ON (matched.media_item_id)
			       matched.media_item_id AS id, matched.media_type, matched.width, matched.height,
			       matched.local_date_time, matched.available, matched.committed_at
			FROM matched
			ORDER BY matched.media_item_id, matched.committed_at DESC, matched.event_id DESC
		), photo_rows AS (
			SELECT *, count(*) OVER ()::integer AS total_photos
			FROM unique_photos
			ORDER BY COALESCE(local_date_time, '') DESC, id DESC LIMIT ?
		)
		SELECT COALESCE((SELECT jsonb_agg(to_jsonb(event_rows) ORDER BY committed_at DESC, id DESC) FROM event_rows), '[]'::jsonb)::text,
		       COALESCE((SELECT jsonb_agg(to_jsonb(photo_rows) ORDER BY COALESCE(local_date_time, '') DESC, id DESC) FROM photo_rows), '[]'::jsonb)::text`
		type eventRow struct {
			EventResult
			CommittedAt time.Time `json:"committed_at"`
			TotalEvents int       `json:"total_events"`
		}
		type photoRow struct {
			Media
			CommittedAt time.Time `json:"committed_at"`
			TotalPhotos int       `json:"total_photos"`
		}
		var eventJSON, photoJSON string
		combinedArgs := append(append([]any(nil), args...), maxEventResults+1, maxPhotoResults+1)
		if err := tx.NewRaw(combinedQuery, combinedArgs...).Scan(ctx, &eventJSON, &photoJSON); err != nil {
			return fmt.Errorf("search Events and Photos: %w", err)
		}
		var eventRows []eventRow
		var photos []photoRow
		if err := json.Unmarshal([]byte(eventJSON), &eventRows); err != nil {
			return fmt.Errorf("decode search Events: %w", err)
		}
		if err := json.Unmarshal([]byte(photoJSON), &photos); err != nil {
			return fmt.Errorf("decode search Photos: %w", err)
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

		var hasDiscoverablePeople bool
		if len(terms) > 0 {
			if err := tx.NewRaw(`SELECT EXISTS (
				SELECT 1 FROM visibility_circle_members AS own_membership
				JOIN visibility_circles AS circle ON circle.id = own_membership.circle_id AND circle.archived_at IS NULL
				JOIN visibility_circle_members AS shared_membership ON shared_membership.circle_id = circle.id
				WHERE own_membership.person_id = ? AND shared_membership.person_id <> ?
			)`, actor.PersonID, actor.PersonID).Scan(ctx, &hasDiscoverablePeople); err != nil {
				return err
			}
		}
		if len(terms) > 0 && hasDiscoverablePeople {
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
