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
	Restorable    bool    `json:"restorable"`
}

// StagedDeletedMoment identifies published structure absent from the resulting Event.
type StagedDeletedMoment struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	ProposedDay string `json:"proposed_day"`
}

// StagedRecipientAccess describes a Recipient's net Media authorization change.
type StagedRecipientAccess struct {
	RecipientPersonID string `json:"recipient_person_id"`
	RecipientName     string `json:"recipient_name"`
	GrantedMediaCount int    `json:"granted_media_count"`
	RevokedMediaCount int    `json:"revoked_media_count"`
}

// StagedChange is one category in the coalesced difference from the current Publication.
type StagedChange struct {
	Kind                staging.ChangeKind      `json:"kind" tstype:"\"addition\" | \"removal\" | \"move\" | \"metadata\" | \"moment_structure\" | \"access\""`
	Count               int                     `json:"count"`
	MediaItemIDs        []string                `json:"media_item_ids"`
	MomentIDs           []string                `json:"moment_ids"`
	EventMetadataFields []string                `json:"event_metadata_fields,omitempty" tstype:"(\"title\" | \"description\" | \"place_labels\" | \"grouping_timezone\")[]"`
	RemovedMedia        []StagedRemovedMedia    `json:"removed_media,omitempty"`
	DeletedMoments      []StagedDeletedMoment   `json:"deleted_moments,omitempty"`
	RecipientAccess     []StagedRecipientAccess `json:"recipient_access,omitempty"`
	Detail              string                  `json:"detail"`
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
				Restorable: item.Restorable,
			})
		}
		deletedMoments := make([]StagedDeletedMoment, 0, len(change.DeletedMoments))
		for _, moment := range change.DeletedMoments {
			deletedMoments = append(deletedMoments, StagedDeletedMoment{
				ID: moment.ID, Title: moment.Title, ProposedDay: moment.ProposedDay,
			})
		}
		recipientAccess := make([]StagedRecipientAccess, 0, len(change.RecipientAccess))
		for _, access := range change.RecipientAccess {
			recipientAccess = append(recipientAccess, StagedRecipientAccess{
				RecipientPersonID: access.RecipientPersonID, RecipientName: access.RecipientName,
				GrantedMediaCount: access.GrantedMediaCount, RevokedMediaCount: access.RevokedMediaCount,
			})
		}
		changes = append(changes, StagedChange{
			Kind: change.Kind, Count: change.Count, MediaItemIDs: change.MediaItemIDs,
			MomentIDs: change.MomentIDs, EventMetadataFields: change.EventMetadataFields,
			RemovedMedia: removedMedia, DeletedMoments: deletedMoments,
			RecipientAccess: recipientAccess, Detail: change.Detail,
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
