// Package activity integrates interaction events into first-party activity feeds.
package activity

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

var ErrCommentUnavailable = errors.New("comment activity is unavailable")

// Service records interaction activity at the boundary shared by Comments and Favorites.
type Service struct {
	db *bun.DB
}

func New(db *bun.DB) *Service { return &Service{db: db} }

// RecordComment completes a Comment handoff idempotently for one Recipient generation.
func (s *Service) RecordComment(ctx context.Context, accessID, commentID uuid.UUID) error {
	result, err := s.db.NewRaw(`INSERT INTO interaction_activity_items
		(kind, recipient_access_generation_id, actor_person_id, media_item_id, comment_id, action, created_at)
		SELECT 'comment', ?, comment.author_person_id, comment.media_item_id, comment.id, 'comment_created', comment.created_at
		FROM comments AS comment WHERE comment.id = ?
		ON CONFLICT (comment_id, recipient_access_generation_id) WHERE kind = 'comment' DO NOTHING`, accessID, commentID).Exec(ctx)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		var exists bool
		if err := s.db.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM interaction_activity_items
			WHERE kind = 'comment' AND comment_id = ? AND recipient_access_generation_id = ?
		)`, commentID, accessID).Scan(ctx, &exists); err != nil {
			return err
		}
		if !exists {
			return ErrCommentUnavailable
		}
	}
	return nil
}

// RecordFavorite appends one Curator-visible Favorite state transition in the caller's transaction.
func (s *Service) RecordFavorite(ctx context.Context, tx bun.Tx, recipientID, mediaID uuid.UUID, action string, createdAt time.Time) error {
	_, err := tx.NewRaw(`INSERT INTO interaction_activity_items
		(kind, actor_person_id, media_item_id, favorite_recipient_person_id, action, created_at)
		VALUES ('favorite', ?, ?, ?, ?, ?)`, recipientID, mediaID, recipientID, "favorite_"+action, createdAt).Exec(ctx)
	return err
}
