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

// CuratorWorkItem is a safe unresolved problem on the Curator's prioritized work surface.
type CuratorWorkItem struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	SourceKind string    `json:"source_kind"`
	SourceID   string    `json:"source_id"`
	Diagnostic string    `json:"diagnostic"`
	CreatedAt  time.Time `json:"created_at"`
}

// CuratorWorkResponse is the bounded Curator work queue response.
type CuratorWorkResponse struct {
	Items []CuratorWorkItem `json:"items"`
}

// ListCuratorWork returns unresolved delivery failures without recipient addresses or provider details.
func (s *Service) ListCuratorWork(ctx context.Context) (CuratorWorkResponse, error) {
	var items []CuratorWorkItem
	if err := s.db.NewRaw(`
		SELECT problem.id::text AS id, 'delivery_problem' AS kind,
		       CASE WHEN problem.notification_batch_id IS NOT NULL THEN 'notification_batch' ELSE 'email_delivery' END AS source_kind,
		       COALESCE(batch.public_id::text, delivery.public_id::text) AS source_id,
		       problem.diagnostic, problem.created_at
		FROM delivery_problems AS problem
		LEFT JOIN notification_batches AS batch ON batch.id = problem.notification_batch_id
		LEFT JOIN email_deliveries AS delivery ON delivery.id = problem.email_delivery_id
		WHERE problem.resolved_at IS NULL
		ORDER BY problem.created_at DESC, problem.id DESC
		LIMIT 100
	`).Scan(ctx, &items); err != nil {
		return CuratorWorkResponse{}, err
	}
	for index := range items {
		items[index].Diagnostic = safeDeliveryDiagnostic(items[index].Diagnostic)
	}
	return CuratorWorkResponse{Items: items}, nil
}

func safeDeliveryDiagnostic(diagnostic string) string {
	switch diagnostic {
	case "authentication_rejected", "authentication_unavailable", "delivery_expired", "invalid_recipient",
		"recipient_rejected", "retry_window_exhausted", "smtp_rejected", "smtp_unavailable",
		"tls_required", "tls_verification_failed":
		return diagnostic
	default:
		return "delivery_failed"
	}
}

// RecordComment completes a Comment handoff idempotently in the caller's transaction.
func (s *Service) RecordComment(ctx context.Context, tx bun.Tx, accessID, commentID uuid.UUID) error {
	result, err := tx.NewRaw(`INSERT INTO interaction_activity_items
		(kind, recipient_access_generation_id, actor_person_id, media_item_id, comment_id, action, created_at)
		SELECT 'comment', ?, comment.author_person_id, comment.media_item_id, comment.id, 'comment_created', comment.created_at
		FROM comments AS comment WHERE comment.id = ?
		ON CONFLICT (comment_id, recipient_access_generation_id) WHERE kind = 'comment' DO NOTHING`, accessID, commentID).Exec(ctx)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		var exists bool
		if err := tx.NewRaw(`SELECT EXISTS (
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
