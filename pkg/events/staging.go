package events

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/staging"
	"github.com/uptrace/bun"
)

// StagedChange is one category in the coalesced difference from the current Publication.
type StagedChange struct {
	Kind         string   `json:"kind"`
	Count        int      `json:"count"`
	MediaItemIDs []string `json:"media_item_ids"`
	MomentIDs    []string `json:"moment_ids"`
	Detail       string   `json:"detail"`
}

// StagedUpdate is the one private net update for a published Event.
type StagedUpdate struct {
	ID                string         `json:"id"`
	BasePublicationID string         `json:"base_publication_id"`
	Changes           []StagedChange `json:"changes"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

func stagedUpdateFromDomain(update *staging.Update) *StagedUpdate {
	if update == nil {
		return nil
	}
	changes := make([]StagedChange, 0, len(update.Changes))
	for _, change := range update.Changes {
		changes = append(changes, StagedChange{
			Kind: change.Kind, Count: change.Count, MediaItemIDs: change.MediaItemIDs,
			MomentIDs: change.MomentIDs, Detail: change.Detail,
		})
	}
	return &StagedUpdate{
		ID: update.ID, BasePublicationID: update.BasePublicationID,
		Changes: changes, UpdatedAt: update.UpdatedAt,
	}
}

func refreshStagedUpdate(ctx context.Context, tx bun.Tx, eventID uuid.UUID, now time.Time) (*StagedUpdate, error) {
	update, err := staging.Refresh(ctx, tx, eventID, now)
	if err != nil {
		return nil, err
	}
	return stagedUpdateFromDomain(update), nil
}

func clearStagedUpdate(ctx context.Context, tx bun.Tx, eventID uuid.UUID) error {
	return staging.Clear(ctx, tx, eventID)
}
