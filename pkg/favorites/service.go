// Package favorites owns each Recipient's private item selections.
package favorites

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/internal/placementlock"
	"github.com/robinjoseph08/memento/pkg/mediaaccess"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/uptrace/bun"
)

var (
	ErrNotFound   = errors.New("favorite not found")
	ErrNotCurator = errors.New("curator authority is required")
)

type State struct {
	MediaItemID string `json:"media_item_id"`
	Favorite    bool   `json:"favorite"`
}

type CuratorListResponse struct {
	RecipientPersonID string   `json:"recipient_person_id"`
	MediaItemIDs      []string `json:"media_item_ids"`
}

type Service struct {
	db  *bun.DB
	now func() time.Time
}

func New(db *bun.DB) *Service { return &Service{db: db, now: time.Now} }

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
		if err := placementlock.Acquire(ctx, tx, placementlock.Shared); err != nil {
			return err
		}
		if err := mediaaccess.Require(ctx, tx, actor, mediaID); err != nil {
			return err
		}
		var current bool
		err := tx.NewRaw(`SELECT is_current FROM favorites
			WHERE recipient_person_id = ? AND media_item_id = ?`, actor.PersonID, mediaID).Scan(ctx, &current)
		absent := errors.Is(err, sql.ErrNoRows)
		if err != nil && !absent {
			return err
		}
		if (absent && !favorite) || (!absent && current == favorite) {
			return nil
		}
		if _, err := tx.NewRaw(`INSERT INTO favorites
			(recipient_person_id, media_item_id, is_current, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (recipient_person_id, media_item_id) DO UPDATE
			SET is_current = EXCLUDED.is_current, updated_at = EXCLUDED.updated_at`, actor.PersonID, mediaID, favorite, now, now).Exec(ctx); err != nil {
			return err
		}
		action := "removed"
		if favorite {
			action = "added"
		}
		_, err = tx.NewRaw(`INSERT INTO favorite_curator_activity_items
			(recipient_person_id, media_item_id, action, created_at) VALUES (?, ?, ?, ?)`,
			actor.PersonID, mediaID, action, now).Exec(ctx)
		return err
	})
	if err != nil {
		return State{}, mapAccessError(err)
	}
	return State{MediaItemID: mediaID.String(), Favorite: favorite}, nil
}

func (s *Service) CuratorList(ctx context.Context, actor setup.SessionActor, recipientID uuid.UUID) (CuratorListResponse, error) {
	if !actor.Curator {
		return CuratorListResponse{}, ErrNotCurator
	}
	response := CuratorListResponse{RecipientPersonID: recipientID.String(), MediaItemIDs: make([]string, 0)}
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
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
		return tx.NewRaw(`SELECT media_item_id FROM favorites
			WHERE recipient_person_id = ? AND is_current ORDER BY updated_at DESC, media_item_id`, recipientID).Scan(ctx, &response.MediaItemIDs)
	})
	return response, err
}

func mapAccessError(err error) error {
	if errors.Is(err, mediaaccess.ErrNotFound) {
		return ErrNotFound
	}
	return err
}
