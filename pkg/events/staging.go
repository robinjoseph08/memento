package events

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/staging"
	"github.com/uptrace/bun"
)

// StagedRemovedMedia identifies a published Media item absent from the resulting Event.
type StagedRemovedMedia struct {
	ID            string  `json:"id"`
	MediaType     string  `json:"media_type"`
	LocalDateTime *string `json:"local_date_time" tstype:"string | null,required"`
}

// StagedDeletedMoment identifies published structure absent from the resulting Event.
type StagedDeletedMoment struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	ProposedDay string `json:"proposed_day"`
}

// StagedChange is one category in the coalesced difference from the current Publication.
type StagedChange struct {
	Kind           staging.ChangeKind    `json:"kind" tstype:"\"addition\" | \"removal\" | \"move\" | \"metadata\" | \"moment_structure\" | \"access\""`
	Count          int                   `json:"count"`
	MediaItemIDs   []string              `json:"media_item_ids"`
	MomentIDs      []string              `json:"moment_ids"`
	RemovedMedia   []StagedRemovedMedia  `json:"removed_media,omitempty"`
	DeletedMoments []StagedDeletedMoment `json:"deleted_moments,omitempty"`
	Detail         string                `json:"detail"`
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
		removedMedia := make([]StagedRemovedMedia, 0, len(change.RemovedMedia))
		for _, item := range change.RemovedMedia {
			removedMedia = append(removedMedia, StagedRemovedMedia{
				ID: item.ID, MediaType: item.MediaType, LocalDateTime: item.LocalDateTime,
			})
		}
		deletedMoments := make([]StagedDeletedMoment, 0, len(change.DeletedMoments))
		for _, moment := range change.DeletedMoments {
			deletedMoments = append(deletedMoments, StagedDeletedMoment{
				ID: moment.ID, Title: moment.Title, ProposedDay: moment.ProposedDay,
			})
		}
		changes = append(changes, StagedChange{
			Kind: change.Kind, Count: change.Count, MediaItemIDs: change.MediaItemIDs,
			MomentIDs: change.MomentIDs, RemovedMedia: removedMedia,
			DeletedMoments: deletedMoments, Detail: change.Detail,
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
