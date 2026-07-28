// Package comments owns authorized item-level Comments, moderation, and subscriptions.
package comments

import (
	"bytes"
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
	"github.com/robinjoseph08/memento/internal/placementlock"
	"github.com/robinjoseph08/memento/pkg/mediaaccess"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/uptrace/bun"
)

const CommentJobKind = "comment_activity_created"

var (
	ErrNotFound             = errors.New("comment not found")
	ErrInvalidBody          = errors.New("comment body is invalid")
	ErrInvalidMute          = errors.New("comment subscription not found")
	ErrNotCurator           = errors.New("curator authority is required")
	ErrVersionConflict      = errors.New("comment version is stale")
	ErrInvalidCursor        = errors.New("comment cursor is invalid")
	ErrIdempotencyConflict  = errors.New("comment idempotency key was reused for another request")
	ErrHandoffNotConfigured = errors.New("comment activity handoff is not configured")
)

type Comment struct {
	ID             string     `json:"id"`
	MediaItemID    string     `json:"media_item_id"`
	AuthorPersonID string     `json:"author_person_id"`
	AuthorName     string     `json:"author_name"`
	Body           string     `json:"body"`
	State          string     `json:"state"`
	Version        int64      `json:"version"`
	CreatedAt      time.Time  `json:"created_at"`
	EditedAt       *time.Time `json:"edited_at" tstype:"string | null,required"`
	ModeratedAt    *time.Time `json:"moderated_at" tstype:"string | null,required"`
	ModeratorName  *string    `json:"moderator_name" tstype:"string | null,required"`
	AuthoredByMe   bool       `json:"authored_by_me"`
	CanEdit        bool       `json:"can_edit"`
	CanDelete      bool       `json:"can_delete"`
	CanModerate    bool       `json:"can_moderate"`
}

type ListResponse struct {
	Comments   []Comment `json:"comments"`
	CanMute    bool      `json:"can_mute"`
	Muted      bool      `json:"muted"`
	NextCursor *string   `json:"next_cursor" tstype:"string | null,required"`
}

type CuratorListResponse struct {
	Comments   []Comment `json:"comments"`
	NextCursor *string   `json:"next_cursor" tstype:"string | null,required"`
}

type PageRequest struct {
	Cursor string `json:"-"`
	Limit  int    `json:"-"`
}

type BodyRequest struct {
	Body string `json:"body" mod:"trim" validate:"required,max=2000"`
}

type MuteRequest struct {
	Muted *bool `json:"muted" validate:"required" tstype:"boolean,required"`
}

type ModerateRequest struct {
	Reason string `json:"reason" mod:"trim" validate:"required,max=500"`
}

type ModerationHistory struct {
	ID         int64     `json:"-"`
	PriorState string    `json:"prior_state"`
	PriorBody  string    `json:"prior_body"`
	Reason     string    `json:"reason"`
	ActorName  string    `json:"actor_name"`
	CreatedAt  time.Time `json:"created_at"`
}

type HistoryResponse struct {
	History    []ModerationHistory `json:"history"`
	NextCursor *string             `json:"next_cursor" tstype:"string | null,required"`
}

type pageCursor struct {
	Kind       string    `json:"k"`
	ResourceID string    `json:"r,omitempty"`
	CreatedAt  time.Time `json:"t"`
	UUID       uuid.UUID `json:"u,omitempty"`
	Sequence   int64     `json:"s,omitempty"`
}

type Handoff func(context.Context, bun.Tx, uuid.UUID, uuid.UUID) error

type Service struct {
	db      *bun.DB
	now     func() time.Time
	handoff Handoff
}

func New(db *bun.DB) *Service { return &Service{db: db, now: time.Now} }

func (s *Service) SetHandoff(handoff Handoff) { s.handoff = handoff }

func normalizeBody(body string, maximum int) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" || len([]rune(body)) > maximum {
		return "", ErrInvalidBody
	}
	return body, nil
}

func (s *Service) List(ctx context.Context, actor setup.SessionActor, mediaID uuid.UUID, pages ...PageRequest) (ListResponse, error) {
	page := PageRequest{}
	if len(pages) > 0 {
		page = pages[0]
	}
	page = normalizedPage(page)
	cursor, err := decodePageCursor(page.Cursor, "thread", mediaID.String())
	if err != nil {
		return ListResponse{}, err
	}
	response := ListResponse{Comments: make([]Comment, 0)}
	err = s.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if err := mediaaccess.Require(ctx, tx, actor, mediaID); err != nil {
			return err
		}
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM comment_subscriptions
			WHERE media_item_id = ? AND recipient_access_generation_id = ?
		), EXISTS (
			SELECT 1 FROM comment_subscriptions
			WHERE media_item_id = ? AND recipient_access_generation_id = ? AND muted
		)`, mediaID, actor.AccessID, mediaID, actor.AccessID).Scan(ctx, &response.CanMute, &response.Muted); err != nil {
			return err
		}
		filter := ""
		args := []any{mediaID}
		if cursor != nil {
			filter = ` AND (comment.created_at, comment.id) > (?, ?)`
			args = append(args, cursor.CreatedAt, cursor.UUID)
		}
		args = append(args, page.Limit+1)
		return tx.NewRaw(`SELECT comment.id, comment.media_item_id, comment.author_person_id,
			author.display_name AS author_name, comment.body, comment.state, comment.version, comment.created_at,
			comment.edited_at, comment.moderated_at, moderator.display_name AS moderator_name
			FROM comments AS comment
			JOIN people AS author ON author.id = comment.author_person_id
			LEFT JOIN people AS moderator ON moderator.id = comment.moderated_by_person_id
			WHERE comment.media_item_id = ?`+filter+`
			ORDER BY comment.created_at, comment.id LIMIT ?`, args...).Scan(ctx, &response.Comments)
	})
	if err != nil {
		return ListResponse{}, mapAccessError(err)
	}
	if len(response.Comments) > page.Limit {
		last := response.Comments[page.Limit-1]
		response.NextCursor = encodePageCursor(pageCursor{Kind: "thread", ResourceID: mediaID.String(), CreatedAt: last.CreatedAt, UUID: uuid.MustParse(last.ID)})
		response.Comments = response.Comments[:page.Limit]
	}
	prepareComments(response.Comments, actor)
	return response, nil
}

func (s *Service) CuratorList(ctx context.Context, actor setup.SessionActor, page PageRequest) (CuratorListResponse, error) {
	if !actor.Curator {
		return CuratorListResponse{}, ErrNotCurator
	}
	page = normalizedPage(page)
	cursor, err := decodePageCursor(page.Cursor, "curator", "")
	if err != nil {
		return CuratorListResponse{}, err
	}
	response := CuratorListResponse{Comments: make([]Comment, 0)}
	err = s.db.RunInTx(ctx, &sql.TxOptions{ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var curator bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM person_roles WHERE person_id = ? AND role = 'curator')`, actor.PersonID).Scan(ctx, &curator); err != nil {
			return err
		}
		if !curator {
			return ErrNotCurator
		}
		filter := ""
		args := make([]any, 0, 3)
		if cursor != nil {
			filter = `WHERE (comment.created_at, comment.id) < (?, ?)`
			args = append(args, cursor.CreatedAt, cursor.UUID)
		}
		args = append(args, page.Limit+1)
		return tx.NewRaw(`SELECT comment.id, comment.media_item_id, comment.author_person_id,
			author.display_name AS author_name, comment.body, comment.state, comment.version, comment.created_at,
			comment.edited_at, comment.moderated_at, moderator.display_name AS moderator_name
			FROM comments AS comment
			JOIN people AS author ON author.id = comment.author_person_id
			LEFT JOIN people AS moderator ON moderator.id = comment.moderated_by_person_id
			`+filter+` ORDER BY comment.created_at DESC, comment.id DESC LIMIT ?`, args...).Scan(ctx, &response.Comments)
	})
	if err != nil {
		return CuratorListResponse{}, err
	}
	if len(response.Comments) > page.Limit {
		last := response.Comments[page.Limit-1]
		response.NextCursor = encodePageCursor(pageCursor{Kind: "curator", CreatedAt: last.CreatedAt, UUID: uuid.MustParse(last.ID)})
		response.Comments = response.Comments[:page.Limit]
	}
	prepareComments(response.Comments, actor)
	return response, nil
}

func (s *Service) Create(ctx context.Context, actor setup.SessionActor, mediaID, idempotencyKey uuid.UUID, request BodyRequest) (Comment, error) {
	body, err := normalizeBody(request.Body, 2000)
	if err != nil {
		return Comment{}, err
	}
	commentID := uuid.New()
	now := s.now().UTC()
	var result Comment
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := mediaaccess.RequireForMutation(ctx, tx, actor, mediaID); err != nil {
			return err
		}
		inserted, err := tx.NewRaw(`INSERT INTO comments
			(id, media_item_id, author_person_id, author_access_generation_id, idempotency_key, body, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (author_access_generation_id, idempotency_key) DO NOTHING`, commentID, mediaID, actor.PersonID, actor.AccessID, idempotencyKey, body, now, now).Exec(ctx)
		if err != nil {
			return err
		}
		if affected, _ := inserted.RowsAffected(); affected == 0 {
			var existingMediaID, existingID uuid.UUID
			var existingBody string
			if err := tx.NewRaw(`SELECT id, media_item_id, body FROM comments
				WHERE author_access_generation_id = ? AND idempotency_key = ?`, actor.AccessID, idempotencyKey).Scan(ctx, &existingID, &existingMediaID, &existingBody); err != nil {
				return err
			}
			if existingMediaID != mediaID || existingBody != body {
				return ErrIdempotencyConflict
			}
			return loadComment(ctx, tx, existingID, &result)
		}
		if _, err := tx.NewRaw(`INSERT INTO comment_subscriptions
			(media_item_id, recipient_access_generation_id, created_at, updated_at)
			VALUES (?, ?, ?, ?) ON CONFLICT (media_item_id, recipient_access_generation_id) DO NOTHING`, mediaID, actor.AccessID, now, now).Exec(ctx); err != nil {
			return err
		}
		var subscribers []uuid.UUID
		if err := tx.NewRaw(`SELECT candidate.recipient_access_generation_id FROM (
			SELECT subscription.recipient_access_generation_id
			FROM comment_subscriptions AS subscription
			JOIN recipient_access_generations AS access ON access.id = subscription.recipient_access_generation_id
			WHERE subscription.media_item_id = ? AND NOT subscription.muted
			  AND access.person_id <> ? AND access.is_current AND access.state = 'completed'
			UNION
			SELECT access.id
			FROM recipient_access_generations AS access
			JOIN person_roles AS role ON role.person_id = access.person_id AND role.role = 'curator'
			WHERE access.person_id <> ? AND access.is_current AND access.state = 'completed'
			) AS candidate ORDER BY candidate.recipient_access_generation_id`, mediaID, actor.PersonID, actor.PersonID).Scan(ctx, &subscribers); err != nil {
			return err
		}
		for _, accessID := range subscribers {
			authorized, err := mediaaccess.GenerationCanAccess(ctx, tx, accessID, mediaID)
			if err != nil {
				return err
			}
			if !authorized {
				continue
			}
			var activityID int64
			if err := tx.NewRaw(`INSERT INTO comment_activity_items
				(comment_id, recipient_access_generation_id, created_at)
				VALUES (?, ?, ?) RETURNING id`, commentID, accessID, now).Scan(ctx, &activityID); err != nil {
				return err
			}
			payload, err := json.Marshal(map[string]int64{"activity_id": activityID})
			if err != nil {
				return err
			}
			if _, err := tx.NewRaw(`INSERT INTO outbox_events
				(kind, aggregate_kind, aggregate_id, aggregate_version, payload, available_at, created_at)
				VALUES (?, 'comment_activity', ?, 1, ?::jsonb, ?, ?)`, CommentJobKind,
				strconv.FormatInt(activityID, 10), string(payload), now.Add(15*time.Minute), now).Exec(ctx); err != nil {
				return err
			}
		}
		return loadComment(ctx, tx, commentID, &result)
	})
	if err != nil {
		return Comment{}, mapAccessError(err)
	}
	prepared := []Comment{result}
	prepareComments(prepared, actor)
	return prepared[0], nil
}

func (s *Service) Edit(ctx context.Context, actor setup.SessionActor, commentID uuid.UUID, version int64, request BodyRequest) (Comment, error) {
	body, err := normalizeBody(request.Body, 2000)
	if err != nil {
		return Comment{}, err
	}
	now := s.now().UTC()
	var result Comment
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var mediaID uuid.UUID
		if err := tx.NewRaw(`SELECT media_item_id FROM comments WHERE id = ?`, commentID).Scan(ctx, &mediaID); err != nil {
			return notFound(err)
		}
		if err := mediaaccess.RequireForMutation(ctx, tx, actor, mediaID); err != nil {
			return err
		}
		updated, err := tx.NewRaw(`UPDATE comments SET body = ?, edited_at = ?, version = version + 1, updated_at = ?
			WHERE id = ? AND author_person_id = ? AND state = 'active' AND version = ?`, body, now, now, commentID, actor.PersonID, version).Exec(ctx)
		if err != nil {
			return err
		}
		count, _ := updated.RowsAffected()
		if count != 1 {
			return mutationConflict(ctx, tx, commentID, actor.PersonID)
		}
		return loadComment(ctx, tx, commentID, &result)
	})
	if err != nil {
		return Comment{}, mapAccessError(err)
	}
	prepared := []Comment{result}
	prepareComments(prepared, actor)
	return prepared[0], nil
}

func (s *Service) Delete(ctx context.Context, actor setup.SessionActor, commentID uuid.UUID, version int64) error {
	now := s.now().UTC()
	return mapAccessError(s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var mediaID uuid.UUID
		if err := tx.NewRaw(`SELECT media_item_id FROM comments WHERE id = ?`, commentID).Scan(ctx, &mediaID); err != nil {
			return notFound(err)
		}
		if err := mediaaccess.RequireForMutation(ctx, tx, actor, mediaID); err != nil {
			return err
		}
		result, err := tx.NewRaw(`UPDATE comments SET state = 'deleted', deleted_at = ?, version = version + 1, updated_at = ?
			WHERE id = ? AND author_person_id = ? AND state = 'active' AND version = ?`, now, now, commentID, actor.PersonID, version).Exec(ctx)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			return mutationConflict(ctx, tx, commentID, actor.PersonID)
		}
		return nil
	}))
}

func (s *Service) SetMuted(ctx context.Context, actor setup.SessionActor, mediaID uuid.UUID, muted bool) error {
	now := s.now().UTC()
	return mapAccessError(s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := mediaaccess.RequireForMutation(ctx, tx, actor, mediaID); err != nil {
			return err
		}
		result, err := tx.NewRaw(`UPDATE comment_subscriptions SET muted = ?, updated_at = ?
			WHERE media_item_id = ? AND recipient_access_generation_id = ?`, muted, now, mediaID, actor.AccessID).Exec(ctx)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			return ErrInvalidMute
		}
		return nil
	}))
}

func (s *Service) Moderate(ctx context.Context, actor setup.SessionActor, commentID uuid.UUID, version int64, request ModerateRequest) error {
	reason, err := normalizeBody(request.Reason, 500)
	if err != nil {
		return err
	}
	if !actor.Curator {
		return ErrNotCurator
	}
	now := s.now().UTC()
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var priorState, priorBody string
		var currentVersion int64
		if err := tx.NewRaw(`SELECT state, body, version FROM comments WHERE id = ? AND state <> 'moderated' FOR UPDATE`, commentID).Scan(ctx, &priorState, &priorBody, &currentVersion); err != nil {
			return notFound(err)
		}
		if version != currentVersion {
			return ErrVersionConflict
		}
		var curator bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM person_roles WHERE person_id = ? AND role = 'curator')`, actor.PersonID).Scan(ctx, &curator); err != nil {
			return err
		}
		if !curator {
			return ErrNotCurator
		}
		if _, err := tx.NewRaw(`INSERT INTO comment_moderation_history
			(comment_id, actor_person_id, prior_state, prior_body, reason, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`, commentID, actor.PersonID, priorState, priorBody, reason, now).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewRaw(`UPDATE comments SET state = 'moderated', deleted_at = NULL,
			moderated_at = ?, moderated_by_person_id = ?, version = version + 1, updated_at = ? WHERE id = ? AND version = ?`, now, actor.PersonID, now, commentID, version).Exec(ctx)
		return err
	})
}

func (s *Service) ModerationHistory(ctx context.Context, actor setup.SessionActor, commentID uuid.UUID, pages ...PageRequest) (HistoryResponse, error) {
	if !actor.Curator {
		return HistoryResponse{}, ErrNotCurator
	}
	page := PageRequest{}
	if len(pages) > 0 {
		page = pages[0]
	}
	page = normalizedPage(page)
	cursor, err := decodePageCursor(page.Cursor, "history", commentID.String())
	if err != nil {
		return HistoryResponse{}, err
	}
	response := HistoryResponse{History: make([]ModerationHistory, 0)}
	err = s.db.RunInTx(ctx, &sql.TxOptions{ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var curator bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM person_roles WHERE person_id = ? AND role = 'curator')`, actor.PersonID).Scan(ctx, &curator); err != nil {
			return err
		}
		if !curator {
			return ErrNotCurator
		}
		var exists bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM comments WHERE id = ?)`, commentID).Scan(ctx, &exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		filter := ""
		args := []any{commentID}
		if cursor != nil {
			filter = ` AND (history.created_at, history.id) > (?, ?)`
			args = append(args, cursor.CreatedAt, cursor.Sequence)
		}
		args = append(args, page.Limit+1)
		return tx.NewRaw(`SELECT history.id, history.prior_state, history.prior_body, history.reason,
			actor.display_name AS actor_name, history.created_at
			FROM comment_moderation_history AS history
			JOIN people AS actor ON actor.id = history.actor_person_id
			WHERE history.comment_id = ?`+filter+` ORDER BY history.created_at, history.id LIMIT ?`, args...).Scan(ctx, &response.History)
	})
	if err != nil {
		return HistoryResponse{}, err
	}
	if len(response.History) > page.Limit {
		last := response.History[page.Limit-1]
		response.NextCursor = encodePageCursor(pageCursor{Kind: "history", ResourceID: commentID.String(), CreatedAt: last.CreatedAt, Sequence: last.ID})
		response.History = response.History[:page.Limit]
	}
	return response, nil
}

func (s *Service) HandleCommentJob(ctx context.Context, job worker.Job) error {
	var payload struct {
		ActivityID int64 `json:"activity_id"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.ActivityID <= 0 {
		return worker.Permanent("invalid_comment_activity_payload")
	}
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := placementlock.Acquire(ctx, tx, placementlock.Shared); err != nil {
			return err
		}
		var accessID, mediaID, commentID uuid.UUID
		var dispatchedAt, suppressedAt *time.Time
		err := tx.NewRaw(`SELECT activity.recipient_access_generation_id, comment.media_item_id,
			comment.id, activity.dispatched_at, activity.suppressed_at
			FROM comment_activity_items AS activity
			JOIN comments AS comment ON comment.id = activity.comment_id
			WHERE activity.id = ? FOR UPDATE OF activity`, payload.ActivityID).Scan(ctx,
			&accessID, &mediaID, &commentID, &dispatchedAt, &suppressedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return worker.Permanent("comment_activity_missing")
		}
		if err != nil || dispatchedAt != nil || suppressedAt != nil {
			return err
		}
		if err := mediaaccess.LockGenerationForCommit(ctx, tx, accessID); err != nil {
			return err
		}
		var commentState string
		if err := tx.NewRaw(`SELECT state FROM comments WHERE id = ? FOR SHARE`, commentID).Scan(ctx, &commentState); err != nil {
			return err
		}
		var muted bool
		if err := tx.NewRaw(`SELECT CASE
			WHEN EXISTS (
				SELECT 1 FROM recipient_access_generations AS access
				JOIN person_roles AS role ON role.person_id = access.person_id AND role.role = 'curator'
				WHERE access.id = ?
			) THEN false
			ELSE COALESCE((SELECT subscription.muted FROM comment_subscriptions AS subscription
				WHERE subscription.media_item_id = ? AND subscription.recipient_access_generation_id = ?
				FOR SHARE), true)
			END`, accessID, mediaID, accessID).Scan(ctx, &muted); err != nil {
			return err
		}
		authorized, err := mediaaccess.GenerationCanAccess(ctx, tx, accessID, mediaID)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		if muted || !authorized || commentState != "active" {
			_, err = tx.NewRaw(`UPDATE comment_activity_items SET suppressed_at = ? WHERE id = ?`, now, payload.ActivityID).Exec(ctx)
			return err
		}
		if s.handoff == nil {
			return ErrHandoffNotConfigured
		}
		if err := s.handoff(ctx, tx, accessID, commentID); err != nil {
			return fmt.Errorf("handoff Comment activity: %w", err)
		}
		_, err = tx.NewRaw(`UPDATE comment_activity_items SET dispatched_at = ? WHERE id = ?`, now, payload.ActivityID).Exec(ctx)
		return err
	})
}

func loadComment(ctx context.Context, db bun.IDB, id uuid.UUID, result *Comment) error {
	return db.NewRaw(`SELECT comment.id, comment.media_item_id, comment.author_person_id,
		author.display_name AS author_name, comment.body, comment.state, comment.version, comment.created_at,
		comment.edited_at, comment.moderated_at, moderator.display_name AS moderator_name
		FROM comments AS comment JOIN people AS author ON author.id = comment.author_person_id
		LEFT JOIN people AS moderator ON moderator.id = comment.moderated_by_person_id
		WHERE comment.id = ?`, id).Scan(ctx, result)
}

func prepareComments(comments []Comment, actor setup.SessionActor) {
	for index := range comments {
		comment := &comments[index]
		comment.AuthoredByMe = comment.AuthorPersonID == actor.PersonID.String()
		comment.CanEdit = comment.AuthoredByMe && comment.State == "active"
		comment.CanDelete = comment.CanEdit
		comment.CanModerate = actor.Curator && comment.State != "moderated"
		if comment.State != "active" {
			comment.Body = ""
		}
	}
}

func mutationConflict(ctx context.Context, db bun.IDB, commentID, authorID uuid.UUID) error {
	var mutable bool
	if err := db.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM comments WHERE id = ? AND author_person_id = ? AND state = 'active'
	)`, commentID, authorID).Scan(ctx, &mutable); err != nil {
		return err
	}
	if mutable {
		return ErrVersionConflict
	}
	return ErrNotFound
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func normalizedPage(page PageRequest) PageRequest {
	if page.Limit <= 0 {
		page.Limit = 50
	}
	return page
}

func encodePageCursor(cursor pageCursor) *string {
	contents, _ := json.Marshal(cursor)
	encoded := base64.RawURLEncoding.EncodeToString(contents)
	return &encoded
}

func decodePageCursor(raw, kind, resourceID string) (*pageCursor, error) {
	if raw == "" {
		return nil, nil
	}
	contents, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var cursor pageCursor
	if err := decoder.Decode(&cursor); err != nil {
		return nil, ErrInvalidCursor
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidCursor
	}
	if cursor.Kind != kind || cursor.ResourceID != resourceID || cursor.CreatedAt.IsZero() {
		return nil, ErrInvalidCursor
	}
	if kind == "history" {
		if cursor.Sequence < 1 || cursor.UUID != uuid.Nil {
			return nil, ErrInvalidCursor
		}
	} else if cursor.UUID == uuid.Nil || cursor.Sequence != 0 {
		return nil, ErrInvalidCursor
	}
	cursor.CreatedAt = cursor.CreatedAt.UTC()
	return &cursor, nil
}

func mapAccessError(err error) error {
	if errors.Is(err, mediaaccess.ErrNotFound) {
		return ErrNotFound
	}
	return err
}
