// Package favorites owns each Recipient's private item selections.
package favorites

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/mediaaccess"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/uptrace/bun"
)

var (
	ErrNotFound              = errors.New("favorite not found")
	ErrNotCurator            = errors.New("curator authority is required")
	ErrInvalidCursor         = errors.New("favorite cursor is invalid")
	ErrActivityNotConfigured = errors.New("favorite Curator activity is not configured")
)

type State struct {
	MediaItemID string `json:"media_item_id"`
	Favorite    bool   `json:"favorite"`
}

type CuratorListResponse struct {
	RecipientPersonID string   `json:"recipient_person_id"`
	MediaItemIDs      []string `json:"media_item_ids"`
	NextCursor        *string  `json:"next_cursor" tstype:"string | null,required"`
}

type PageRequest struct {
	Cursor string `json:"-"`
	Limit  int    `json:"-"`
}

type curatorCursor struct {
	RecipientID uuid.UUID `json:"r"`
	UpdatedAt   time.Time `json:"t"`
	MediaID     uuid.UUID `json:"m"`
}

type CuratorActivity interface {
	RecordFavorite(ctx context.Context, tx bun.Tx, recipientID, mediaID uuid.UUID, action string, createdAt time.Time) error
}

type EngagementActivity interface {
	RecordFavorite(ctx context.Context, tx bun.Tx, actor setup.SessionActor, mediaID uuid.UUID, action string, createdAt time.Time) error
}

type Service struct {
	db         *bun.DB
	now        func() time.Time
	activity   CuratorActivity
	engagement EngagementActivity
}

func New(db *bun.DB, activity CuratorActivity) *Service {
	return &Service{db: db, now: time.Now, activity: activity}
}

// SetEngagementActivity installs the meaningful-use recorder for real Favorite transitions.
func (s *Service) SetEngagementActivity(engagement EngagementActivity) { s.engagement = engagement }

func (s *Service) Get(ctx context.Context, actor setup.SessionActor, mediaID uuid.UUID) (State, error) {
	if err := mediaaccess.Require(ctx, s.db, actor, mediaID); err != nil {
		return State{}, mapAccessError(err)
	}
	var favorite bool
	if err := s.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM favorites
		WHERE recipient_person_id = ? AND media_item_id = ? AND is_current)`, actor.PersonID, mediaID).Scan(ctx, &favorite); err != nil {
		return State{}, err
	}
	return State{MediaItemID: mediaID.String(), Favorite: favorite}, nil
}

func (s *Service) Set(ctx context.Context, actor setup.SessionActor, mediaID uuid.UUID, favorite bool) (State, error) {
	now := s.now().UTC()
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := mediaaccess.RequireForMutation(ctx, tx, actor, mediaID); err != nil {
			return err
		}
		var result sql.Result
		var err error
		if favorite {
			result, err = tx.NewRaw(`INSERT INTO favorites
				(recipient_person_id, media_item_id, is_current, created_at, updated_at)
				VALUES (?, ?, true, ?, ?)
				ON CONFLICT (recipient_person_id, media_item_id) DO UPDATE
				SET is_current = true, updated_at = EXCLUDED.updated_at
				WHERE NOT favorites.is_current`, actor.PersonID, mediaID, now, now).Exec(ctx)
		} else {
			result, err = tx.NewRaw(`UPDATE favorites SET is_current = false, updated_at = ?
				WHERE recipient_person_id = ? AND media_item_id = ? AND is_current`, now, actor.PersonID, mediaID).Exec(ctx)
		}
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed == 0 {
			return nil
		}
		action := "removed"
		if favorite {
			action = "added"
		}
		if s.activity == nil {
			return ErrActivityNotConfigured
		}
		if err := s.activity.RecordFavorite(ctx, tx, actor.PersonID, mediaID, action, now); err != nil {
			return err
		}
		if s.engagement != nil {
			return s.engagement.RecordFavorite(ctx, tx, actor, mediaID, action, now)
		}
		return nil
	})
	if err != nil {
		return State{}, mapAccessError(err)
	}
	return State{MediaItemID: mediaID.String(), Favorite: favorite}, nil
}

func (s *Service) CuratorList(ctx context.Context, actor setup.SessionActor, recipientID uuid.UUID, pages ...PageRequest) (CuratorListResponse, error) {
	if !actor.Curator {
		return CuratorListResponse{}, ErrNotCurator
	}
	page := PageRequest{}
	if len(pages) > 0 {
		page = pages[0]
	}
	if page.Limit <= 0 {
		page.Limit = 50
	}
	cursor, err := decodeCuratorCursor(page.Cursor, recipientID)
	if err != nil {
		return CuratorListResponse{}, err
	}
	response := CuratorListResponse{RecipientPersonID: recipientID.String(), MediaItemIDs: make([]string, 0)}
	var rows []struct {
		MediaID   uuid.UUID `bun:"media_item_id"`
		UpdatedAt time.Time `bun:"updated_at"`
	}
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var curator, recipient bool
		if err := tx.NewRaw(`SELECT
			EXISTS (SELECT 1 FROM person_roles WHERE person_id = ? AND role = 'curator'),
			EXISTS (SELECT 1 FROM person_roles WHERE person_id = ? AND role = 'recipient')`, actor.PersonID, recipientID).Scan(ctx, &curator, &recipient); err != nil {
			return err
		}
		if !curator {
			return ErrNotCurator
		}
		if !recipient {
			return ErrNotFound
		}
		filter := ""
		args := []any{recipientID}
		if cursor != nil {
			filter = ` AND (updated_at, media_item_id) < (?, ?)`
			args = append(args, cursor.UpdatedAt, cursor.MediaID)
		}
		args = append(args, page.Limit+1)
		return tx.NewRaw(`SELECT media_item_id, updated_at FROM favorites
			WHERE recipient_person_id = ? AND is_current`+filter+`
			ORDER BY updated_at DESC, media_item_id DESC LIMIT ?`, args...).Scan(ctx, &rows)
	})
	if err != nil {
		return CuratorListResponse{}, err
	}
	if len(rows) > page.Limit {
		last := rows[page.Limit-1]
		response.NextCursor = encodeCuratorCursor(curatorCursor{RecipientID: recipientID, UpdatedAt: last.UpdatedAt, MediaID: last.MediaID})
		rows = rows[:page.Limit]
	}
	for _, row := range rows {
		response.MediaItemIDs = append(response.MediaItemIDs, row.MediaID.String())
	}
	return response, nil
}

func encodeCuratorCursor(cursor curatorCursor) *string {
	contents, _ := json.Marshal(cursor)
	encoded := base64.RawURLEncoding.EncodeToString(contents)
	return &encoded
}

func decodeCuratorCursor(raw string, recipientID uuid.UUID) (*curatorCursor, error) {
	if raw == "" {
		return nil, nil
	}
	contents, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var cursor curatorCursor
	if err := decoder.Decode(&cursor); err != nil {
		return nil, ErrInvalidCursor
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidCursor
	}
	if cursor.RecipientID != recipientID || cursor.UpdatedAt.IsZero() || cursor.MediaID == uuid.Nil {
		return nil, ErrInvalidCursor
	}
	cursor.UpdatedAt = cursor.UpdatedAt.UTC()
	return &cursor, nil
}

func mapAccessError(err error) error {
	if errors.Is(err, mediaaccess.ErrNotFound) {
		return ErrNotFound
	}
	return err
}
