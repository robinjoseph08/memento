package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/uptrace/bun"
)

const PublicationJobKind = "publication_committed"

var (
	ErrPublicationNotReady = errors.New("Event is not ready for Publication")
	ErrAudienceNotCurrent  = errors.New("an Audience contains an ineligible access generation")
	ErrNoPublication       = errors.New("published Event not found")
)

// PublicationStep identifies a durable write boundary used by rollback tests.
type PublicationStep string

const (
	PublicationStepLocked       PublicationStep = "locked"
	PublicationStepValidated    PublicationStep = "validated"
	PublicationStepHistory      PublicationStep = "history"
	PublicationStepMetadata     PublicationStep = "metadata"
	PublicationStepPlacements   PublicationStep = "placements"
	PublicationStepAudiences    PublicationStep = "audiences"
	PublicationStepEntitlements PublicationStep = "entitlements"
	PublicationStepActivity     PublicationStep = "activity"
	PublicationStepAudit        PublicationStep = "audit"
	PublicationStepOutbox       PublicationStep = "outbox"
)

// PublishEventRequest protects Publication from an older editable browser state.
type PublishEventRequest struct {
	Version          int64 `json:"version" validate:"required,min=1"`
	NotifyRecipients *bool `json:"notify_recipients,omitempty" tstype:"boolean | null"`
}

// PublicationResponse identifies the immutable revision that became current.
type PublicationResponse struct {
	ID               string    `json:"id"`
	EventID          string    `json:"event_id"`
	Revision         int64     `json:"revision"`
	EditableVersion  int64     `json:"editable_version"`
	NotifyRecipients bool      `json:"notify_recipients"`
	CommittedAt      time.Time `json:"committed_at"`
}

// PreviewRecipient is a current Recipient generation selectable by the Curator.
type PreviewRecipient struct {
	PersonID    string `json:"person_id"`
	AccessID    string `json:"access_id"`
	Name        string `json:"name"`
	AccessState string `json:"access_state"`
}

// PreviewRecipientsResponse lists safe Recipient labels without Audience details.
type PreviewRecipientsResponse struct {
	Recipients []PreviewRecipient `json:"recipients"`
}

// PublishedMedia is the path-free Recipient representation of a placement.
type PublishedMedia struct {
	ID            string  `json:"id"`
	MediaType     string  `json:"media_type"`
	Width         *int    `json:"width" tstype:"number | null,required"`
	Height        *int    `json:"height" tstype:"number | null,required"`
	LocalDateTime *string `json:"local_date_time" tstype:"string | null,required"`
	Available     bool    `json:"available"`
}

// PreviewCapabilities makes every prohibited interaction explicit to clients.
type PreviewCapabilities struct {
	Comments         bool `json:"comments"`
	Favorites        bool `json:"favorites"`
	Settings         bool `json:"settings"`
	Downloads        bool `json:"downloads"`
	RecordEngagement bool `json:"record_engagement"`
}

// PublishedEventView is a seamless authorization-filtered Event without Moment boundaries.
type PublishedEventView struct {
	Authorized    bool                `json:"authorized"`
	EventID       string              `json:"event_id"`
	PublicationID string              `json:"publication_id"`
	Title         string              `json:"title"`
	Description   string              `json:"description"`
	CoverMediaID  *string             `json:"cover_media_id" tstype:"string | null,required"`
	MediaCount    int                 `json:"media_count"`
	Media         []PublishedMedia    `json:"media"`
	Preview       bool                `json:"preview"`
	Capabilities  PreviewCapabilities `json:"capabilities"`
}

func (s *Service) publicationBoundary(step PublicationStep) error {
	if s.failPublicationStep == nil {
		return nil
	}
	return s.failPublicationStep(step)
}

// PublishEvent atomically appends history and replaces every Recipient projection.
func (s *Service) PublishEvent(ctx context.Context, actor setup.CuratorSession, eventID uuid.UUID, request PublishEventRequest) (PublicationResponse, error) {
	notify := true
	if request.NotifyRecipients != nil {
		notify = *request.NotifyRecipients
	}
	publicationID := uuid.New()
	now := s.now().UTC()
	var response PublicationResponse
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var title, description, timezone, lifecycle string
		var version int64
		var finalReview bool
		var priorID *uuid.UUID
		if err := tx.NewRaw(`
			SELECT title, description, grouping_timezone, lifecycle, version,
			       final_review_complete, current_publication_id
			FROM events WHERE id = ? FOR UPDATE
		`, eventID).Scan(ctx, &title, &description, &timezone, &lifecycle, &version, &finalReview, &priorID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		var curator bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM person_roles WHERE person_id = ? AND role = 'curator')`, actor.PersonID).Scan(ctx, &curator); err != nil {
			return err
		}
		if !curator {
			return setup.ErrNotCurator
		}
		// Audience approval locks and updates the Moment row. Holding matching locks
		// through commit keeps the snapshots validated below from changing midway
		// through Publication.
		var lockedMomentIDs []uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM draft_moments WHERE event_id = ? ORDER BY id FOR SHARE`, eventID).Scan(ctx, &lockedMomentIDs); err != nil {
			return err
		}
		if err := s.publicationBoundary(PublicationStepLocked); err != nil {
			return err
		}
		if request.Version != version {
			return ErrVersionConflict
		}
		if lifecycle != "draft" && lifecycle != "published" {
			return ErrPublicationNotReady
		}
		if priorID != nil {
			var priorEditableVersion int64
			if err := tx.NewRaw(`SELECT editable_version FROM publications WHERE id = ?`, *priorID).Scan(ctx, &priorEditableVersion); err != nil {
				return err
			}
			if priorEditableVersion == version {
				return ErrPublicationNotReady
			}
		}
		var momentCount, unassignedCount, incompleteCount int
		if err := tx.NewRaw(`
			SELECT
				(SELECT count(*) FROM draft_moments WHERE event_id = ?),
				(SELECT count(*) FROM draft_media_placements WHERE event_id = ? AND draft_moment_id IS NULL),
				(SELECT count(*)
				 FROM draft_moments AS moment
				 LEFT JOIN current_audience_snapshots AS snapshot
				   ON snapshot.target_kind = 'moment' AND snapshot.target_id = moment.id
				 WHERE moment.event_id = ?
				   AND (NOT moment.audience_complete OR snapshot.snapshot_id IS NULL))
		`, eventID, eventID, eventID).Scan(ctx, &momentCount, &unassignedCount, &incompleteCount); err != nil {
			return err
		}
		if momentCount == 0 || unassignedCount != 0 || incompleteCount != 0 || !finalReview {
			return ErrPublicationNotReady
		}
		type lockedAccess struct {
			Current bool   `bun:"is_current"`
			State   string `bun:"state"`
		}
		var audienceAccess []lockedAccess
		if err := tx.NewRaw(`
			SELECT access.is_current, access.state
			FROM draft_moments AS moment
			JOIN current_audience_snapshots AS snapshot
			  ON snapshot.target_kind = 'moment' AND snapshot.target_id = moment.id
			JOIN audience_snapshot_entries AS entry ON entry.snapshot_id = snapshot.snapshot_id
			JOIN recipient_access_generations AS access ON access.id = entry.recipient_access_generation_id
			WHERE moment.event_id = ?
			FOR SHARE OF access
		`, eventID).Scan(ctx, &audienceAccess); err != nil {
			return err
		}
		for _, access := range audienceAccess {
			if !access.Current || access.State == "suspended" || access.State == "revoked" {
				return ErrAudienceNotCurrent
			}
		}
		if err := s.publicationBoundary(PublicationStepValidated); err != nil {
			return err
		}

		var revision, contentRevision int64
		if err := tx.NewRaw(`SELECT COALESCE(max(revision), 0) + 1 FROM publications WHERE event_id = ?`, eventID).Scan(ctx, &revision); err != nil {
			return err
		}
		if err := tx.NewRaw(`UPDATE system_settings SET content_revision = content_revision + 1 WHERE id = 1 RETURNING content_revision`).Scan(ctx, &contentRevision); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`
			INSERT INTO publications (
				id, event_id, revision, editable_version, prior_publication_id,
				published_by_person_id, notify_recipients, committed_at, content_revision
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, publicationID, eventID, revision, version, priorID, actor.PersonID, notify, now, contentRevision).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`
			INSERT INTO published_event_revisions (
				publication_id, event_id, title, description, grouping_timezone, created_at
			) VALUES (?, ?, ?, ?, ?, ?)
		`, publicationID, eventID, title, description, timezone, now).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`
			INSERT INTO published_moments (
				id, publication_id, draft_moment_id, audience_snapshot_id,
				position, title, proposed_day, cover_media_item_id
			)
			SELECT gen_random_uuid(), ?, moment.id, snapshot.snapshot_id,
			       moment.position, moment.title, moment.proposed_day, moment.cover_media_item_id
			FROM draft_moments AS moment
			JOIN current_audience_snapshots AS snapshot
			  ON snapshot.target_kind = 'moment' AND snapshot.target_id = moment.id
			WHERE moment.event_id = ? ORDER BY moment.position
		`, publicationID, eventID).Exec(ctx); err != nil {
			return err
		}
		if err := s.publicationBoundary(PublicationStepHistory); err != nil {
			return err
		}

		if _, err := tx.NewRaw(`
			INSERT INTO current_published_events (
				event_id, publication_id, title, description, grouping_timezone, committed_at
			) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (event_id) DO UPDATE SET
				publication_id = EXCLUDED.publication_id, title = EXCLUDED.title,
				description = EXCLUDED.description, grouping_timezone = EXCLUDED.grouping_timezone,
				committed_at = EXCLUDED.committed_at
		`, eventID, publicationID, title, description, timezone, now).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE events SET lifecycle = 'published', current_publication_id = ?, updated_at = ? WHERE id = ?`, publicationID, now, eventID).Exec(ctx); err != nil {
			return err
		}
		if err := s.publicationBoundary(PublicationStepMetadata); err != nil {
			return err
		}

		if _, err := tx.NewRaw(`
			INSERT INTO published_media_placements (
				published_moment_id, media_item_id, position,
				media_type, width, height, local_date_time
			)
			SELECT published.id, placement.media_item_id, placement.position,
			       media.media_type, media.width, media.height, media.local_date_time
			FROM published_moments AS published
			JOIN draft_media_placements AS placement
			  ON placement.draft_moment_id = published.draft_moment_id
			JOIN media_items AS media ON media.id = placement.media_item_id
			WHERE published.publication_id = ?
		`, publicationID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM current_published_placements WHERE event_id = ?`, eventID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`
			INSERT INTO current_published_placements (
				event_id, publication_id, published_moment_id, media_item_id, position
			)
			SELECT ?, ?, published.id, placement.media_item_id,
			       row_number() OVER (ORDER BY published.position, placement.position) - 1
			FROM published_moments AS published
			JOIN published_media_placements AS placement ON placement.published_moment_id = published.id
			WHERE published.publication_id = ?
		`, eventID, publicationID, publicationID).Exec(ctx); err != nil {
			return err
		}
		if err := s.publicationBoundary(PublicationStepPlacements); err != nil {
			return err
		}

		if _, err := tx.NewRaw(`
			INSERT INTO audience_entries (
				published_moment_id, recipient_person_id, recipient_access_generation_id
			)
			SELECT published.id, entry.recipient_person_id, entry.recipient_access_generation_id
			FROM published_moments AS published
			JOIN audience_snapshot_entries AS entry ON entry.snapshot_id = published.audience_snapshot_id
			WHERE published.publication_id = ?
		`, publicationID).Exec(ctx); err != nil {
			return err
		}
		if err := s.publicationBoundary(PublicationStepAudiences); err != nil {
			return err
		}

		for _, statement := range []string{
			`DELETE FROM published_search_documents WHERE event_id = ?`,
			`DELETE FROM current_recipient_event_covers WHERE event_id = ?`,
			`DELETE FROM current_audience_entitlements WHERE event_id = ?`,
		} {
			if _, err := tx.NewRaw(statement, eventID).Exec(ctx); err != nil {
				return err
			}
		}
		if _, err := tx.NewRaw(`
			INSERT INTO current_audience_entitlements (
				event_id, publication_id, recipient_person_id,
				recipient_access_generation_id, media_item_id
			)
			SELECT DISTINCT ?::uuid, ?::uuid, audience.recipient_person_id,
			       audience.recipient_access_generation_id, placement.media_item_id
			FROM audience_entries AS audience
			JOIN published_media_placements AS placement ON placement.published_moment_id = audience.published_moment_id
			JOIN published_moments AS moment ON moment.id = audience.published_moment_id
			WHERE moment.publication_id = ?
		`, eventID, publicationID, publicationID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`
			INSERT INTO current_recipient_event_covers (event_id, recipient_access_generation_id, media_item_id)
			SELECT DISTINCT ON (entitlement.recipient_access_generation_id)
			       ?::uuid, entitlement.recipient_access_generation_id, entitlement.media_item_id
			FROM current_audience_entitlements AS entitlement
			JOIN current_published_placements AS placement
			  ON placement.event_id = entitlement.event_id AND placement.media_item_id = entitlement.media_item_id
			JOIN media_items AS media ON media.id = entitlement.media_item_id
			JOIN published_moments AS moment ON moment.id = placement.published_moment_id
			WHERE entitlement.event_id = ?
			ORDER BY entitlement.recipient_access_generation_id,
			         (media.availability = 'current') DESC,
			         (moment.cover_media_item_id = entitlement.media_item_id) DESC,
			         placement.position
		`, eventID, eventID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`
			INSERT INTO published_search_documents (
				event_id, publication_id, recipient_access_generation_id, media_item_id, search_text
			)
			SELECT entitlement.event_id, entitlement.publication_id,
			       entitlement.recipient_access_generation_id, entitlement.media_item_id,
			       concat_ws(' ', current.title, current.description, published.local_date_time)
			FROM current_audience_entitlements AS entitlement
			JOIN current_published_events AS current ON current.event_id = entitlement.event_id
			JOIN current_published_placements AS placement
			  ON placement.event_id = entitlement.event_id AND placement.media_item_id = entitlement.media_item_id
			JOIN published_media_placements AS published
			  ON published.published_moment_id = placement.published_moment_id
			 AND published.media_item_id = placement.media_item_id
			WHERE entitlement.event_id = ?
		`, eventID).Exec(ctx); err != nil {
			return err
		}
		if err := restoreEligibleWithdrawals(ctx, tx, eventID, publicationID, now, actor); err != nil {
			return err
		}
		if err := s.publicationBoundary(PublicationStepEntitlements); err != nil {
			return err
		}

		if priorID != nil {
			if _, err := tx.NewRaw(`
				DELETE FROM new_for_you_entries
				WHERE publication_id IN (SELECT id FROM publications WHERE event_id = ? AND id <> ?)
			`, eventID, publicationID).Exec(ctx); err != nil {
				return err
			}
		}
		if _, err := tx.NewRaw(`
			INSERT INTO new_for_you_entries (recipient_access_generation_id, publication_id)
			SELECT DISTINCT entitlement.recipient_access_generation_id, ?::uuid
			FROM current_audience_entitlements AS entitlement
			JOIN recipient_access_generations AS access
			  ON access.id = entitlement.recipient_access_generation_id
			WHERE entitlement.event_id = ? AND access.is_current AND access.state = 'completed'
		`, publicationID, eventID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`
			INSERT INTO publication_activity_items (publication_id, recipient_access_generation_id, created_at)
			SELECT DISTINCT ?::uuid, entitlement.recipient_access_generation_id, ?::timestamptz
			FROM current_audience_entitlements AS entitlement
			JOIN recipient_access_generations AS access
			  ON access.id = entitlement.recipient_access_generation_id
			WHERE entitlement.event_id = ? AND access.is_current AND access.state = 'completed'
		`, publicationID, now, eventID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO publication_curator_activity_items (publication_id, actor_person_id, created_at) VALUES (?, ?, ?)`, publicationID, actor.PersonID, now).Exec(ctx); err != nil {
			return err
		}
		if err := s.publicationBoundary(PublicationStepActivity); err != nil {
			return err
		}

		var firstMomentID uuid.UUID
		if err := tx.NewRaw(`SELECT draft_moment_id FROM published_moments WHERE publication_id = ? ORDER BY position LIMIT 1`, publicationID).Scan(ctx, &firstMomentID); err != nil {
			return err
		}
		auditMetadata, err := json.Marshal(map[string]any{"publication_id": publicationID, "revision": revision, "editable_version": version, "notify_recipients": notify})
		if err != nil {
			return err
		}
		if _, err := tx.NewRaw(`
			INSERT INTO publication_audit_events (
				event_id, target_kind, target_id, actor_person_id, action, metadata, created_at
			) VALUES (?, 'moment', ?, ?, 'event_published', ?::jsonb, ?)
		`, eventID, firstMomentID, actor.PersonID, string(auditMetadata), now).Exec(ctx); err != nil {
			return err
		}
		if err := s.publicationBoundary(PublicationStepAudit); err != nil {
			return err
		}

		payload, err := json.Marshal(map[string]any{"event_id": eventID, "publication_id": publicationID, "notify_recipients": notify})
		if err != nil {
			return err
		}
		if _, err := tx.NewRaw(`
			INSERT INTO outbox_events (
				kind, aggregate_kind, aggregate_id, aggregate_version, payload, available_at, created_at
			) VALUES ('publication_committed', 'event_publication', ?, ?, ?::jsonb, ?, ?)
		`, eventID.String(), revision, string(payload), now, now).Exec(ctx); err != nil {
			return err
		}
		if err := s.publicationBoundary(PublicationStepOutbox); err != nil {
			return err
		}

		response = PublicationResponse{ID: publicationID.String(), EventID: eventID.String(), Revision: revision, EditableVersion: version, NotifyRecipients: notify, CommittedAt: now}
		return nil
	})
	if err != nil {
		return PublicationResponse{}, err
	}
	return response, nil
}

// PreviewRecipients lists current non-Curator Recipient generations for an editable Event, including Pending recipients.
func (s *Service) PreviewRecipients(ctx context.Context, eventID uuid.UUID) (PreviewRecipientsResponse, error) {
	var editable bool
	if err := s.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM events WHERE id = ? AND lifecycle IN ('draft', 'published'))`, eventID).Scan(ctx, &editable); err != nil {
		return PreviewRecipientsResponse{}, err
	}
	if !editable {
		return PreviewRecipientsResponse{}, ErrNoPublication
	}
	response := PreviewRecipientsResponse{Recipients: make([]PreviewRecipient, 0)}
	if err := s.db.NewRaw(`
		SELECT person.id AS person_id, access.id AS access_id,
		       person.display_name AS name, access.state AS access_state
		FROM recipient_access_generations AS access
		JOIN people AS person ON person.id = access.person_id
		WHERE access.is_current AND access.state NOT IN ('suspended', 'revoked')
		  AND NOT EXISTS (
			SELECT 1 FROM person_roles WHERE person_id = person.id AND role = 'curator'
		  )
		ORDER BY person.sort_name, person.id
	`).Scan(ctx, &response.Recipients); err != nil {
		return PreviewRecipientsResponse{}, err
	}
	return response, nil
}

// PreviewEvent records Curator-only audit and renders the saved editable result through selected Recipient authorization.
func (s *Service) PreviewEvent(ctx context.Context, actor setup.CuratorSession, eventID, recipientPersonID uuid.UUID) (PublishedEventView, error) {
	var view PublishedEventView
	err := s.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead}, func(ctx context.Context, tx bun.Tx) error {
		var accessID uuid.UUID
		if err := tx.NewRaw(`
			SELECT id FROM recipient_access_generations
			WHERE person_id = ? AND is_current AND state NOT IN ('suspended', 'revoked')
		`, recipientPersonID).Scan(ctx, &accessID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNoPublication
			}
			return err
		}
		var editableVersion int64
		var publicationID *uuid.UUID
		var err error
		view, editableVersion, publicationID, err = loadEditablePreview(ctx, tx, eventID, accessID)
		if err != nil {
			return err
		}
		if _, err := tx.NewRaw(`
			INSERT INTO publication_preview_audit_events (
				event_id, publication_id, editable_version, actor_person_id,
				recipient_person_id, recipient_access_generation_id, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)
		`, eventID, publicationID, editableVersion, actor.PersonID, recipientPersonID, accessID, s.now().UTC()).Exec(ctx); err != nil {
			return err
		}
		return nil
	})
	return view, err
}

func loadEditablePreview(ctx context.Context, db bun.IDB, eventID, accessID uuid.UUID) (PublishedEventView, int64, *uuid.UUID, error) {
	view := PublishedEventView{
		EventID: eventID.String(), Media: make([]PublishedMedia, 0), Preview: true,
		Capabilities: PreviewCapabilities{},
	}
	var title, description string
	var editableVersion int64
	var publicationID *uuid.UUID
	err := db.NewRaw(`
		SELECT title, description, version, current_publication_id
		FROM events WHERE id = ? AND lifecycle IN ('draft', 'published')
	`, eventID).Scan(ctx, &title, &description, &editableVersion, &publicationID)
	if errors.Is(err, sql.ErrNoRows) {
		return PublishedEventView{}, 0, nil, ErrNoPublication
	}
	if err != nil {
		return PublishedEventView{}, 0, nil, err
	}
	if publicationID != nil {
		view.PublicationID = publicationID.String()
	}
	if err := db.NewRaw(`
		SELECT media.id, media.media_type, media.width, media.height,
		       media.local_date_time, media.availability = 'current' AS available
		FROM draft_media_placements AS placement
		JOIN draft_moments AS moment ON moment.id = placement.draft_moment_id
		JOIN current_audience_snapshots AS snapshot
		  ON snapshot.target_kind = 'moment' AND snapshot.target_id = moment.id
		JOIN audience_snapshot_entries AS audience
		  ON audience.snapshot_id = snapshot.snapshot_id
		 AND audience.recipient_access_generation_id = ?
		JOIN media_items AS media ON media.id = placement.media_item_id
		WHERE placement.event_id = ?
		ORDER BY placement.position
	`, accessID, eventID).Scan(ctx, &view.Media); err != nil {
		return PublishedEventView{}, 0, nil, fmt.Errorf("load editable preview media: %w", err)
	}
	view.MediaCount = len(view.Media)
	if view.MediaCount == 0 {
		return view, editableVersion, publicationID, nil
	}
	view.Authorized = true
	view.Title = title
	view.Description = description
	var coverID uuid.UUID
	if err := db.NewRaw(`
		SELECT placement.media_item_id
		FROM draft_media_placements AS placement
		JOIN draft_moments AS moment ON moment.id = placement.draft_moment_id
		JOIN current_audience_snapshots AS snapshot
		  ON snapshot.target_kind = 'moment' AND snapshot.target_id = moment.id
		JOIN audience_snapshot_entries AS audience
		  ON audience.snapshot_id = snapshot.snapshot_id
		 AND audience.recipient_access_generation_id = ?
		JOIN media_items AS media ON media.id = placement.media_item_id
		WHERE placement.event_id = ?
		ORDER BY (media.availability = 'current') DESC,
		         (moment.cover_media_item_id = placement.media_item_id) DESC,
		         placement.position
		LIMIT 1
	`, accessID, eventID).Scan(ctx, &coverID); err != nil {
		return PublishedEventView{}, 0, nil, fmt.Errorf("load editable preview cover: %w", err)
	}
	cover := coverID.String()
	view.CoverMediaID = &cover
	return view, editableVersion, publicationID, nil
}

// RecipientEvent renders current projections only for a completed current generation.
func (s *Service) RecipientEvent(ctx context.Context, actor setup.SessionActor, eventID uuid.UUID) (PublishedEventView, error) {
	var view PublishedEventView
	err := s.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var completed bool
		if err := tx.NewRaw(`
			SELECT EXISTS (
				SELECT 1 FROM recipient_access_generations
				WHERE id = ? AND person_id = ? AND is_current AND state = 'completed'
			)
		`, actor.AccessID, actor.PersonID).Scan(ctx, &completed); err != nil {
			return err
		}
		if !completed {
			return ErrNoPublication
		}
		var err error
		view, err = s.loadPublishedEvent(ctx, tx, eventID, actor.AccessID)
		return err
	})
	return view, err
}

// HandlePublicationJob validates the durable handoff before acknowledging it.
// Channel-specific delivery is intentionally downstream of this privacy transaction.
func (s *Service) HandlePublicationJob(ctx context.Context, job worker.Job) error {
	var payload struct {
		EventID       uuid.UUID `json:"event_id"`
		PublicationID uuid.UUID `json:"publication_id"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.EventID == uuid.Nil || payload.PublicationID == uuid.Nil {
		return worker.Permanent("invalid_publication_payload")
	}
	var exists bool
	if err := s.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM publications WHERE id = ? AND event_id = ?)`, payload.PublicationID, payload.EventID).Scan(ctx, &exists); err != nil {
		return err
	}
	if !exists {
		return worker.Permanent("unknown_publication")
	}
	var deliverable bool
	if err := s.db.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM current_published_events AS current
		WHERE current.event_id = ? AND current.publication_id = ?
		  AND NOT EXISTS (
			SELECT 1 FROM current_published_placements AS placement
			JOIN published_moments AS moment ON moment.id = placement.published_moment_id
			WHERE placement.event_id = current.event_id
			  AND placement.publication_id = current.publication_id
			  AND content_is_withdrawn(
				placement.event_id, moment.draft_moment_id, placement.media_item_id
			  )
		  )
	)`, payload.EventID, payload.PublicationID).Scan(ctx, &deliverable); err != nil {
		return err
	}
	if !deliverable {
		return worker.Permanent("publication_withdrawn")
	}
	return nil
}

func (s *Service) loadPublishedEvent(ctx context.Context, db bun.IDB, eventID, accessID uuid.UUID) (PublishedEventView, error) {
	view := PublishedEventView{EventID: eventID.String(), Media: make([]PublishedMedia, 0)}
	var publicationID uuid.UUID
	var coverID *uuid.UUID
	err := db.NewRaw(`
		SELECT current.publication_id, current.title, current.description, cover.media_item_id
		FROM current_published_events AS current
		LEFT JOIN current_recipient_event_covers AS cover
		  ON cover.event_id = current.event_id AND cover.recipient_access_generation_id = ?
		WHERE current.event_id = ?
		  AND EXISTS (
			SELECT 1 FROM current_audience_entitlements AS entitlement
			JOIN current_published_placements AS placement
			  ON placement.event_id = entitlement.event_id AND placement.media_item_id = entitlement.media_item_id
			JOIN published_moments AS moment ON moment.id = placement.published_moment_id
			WHERE entitlement.event_id = current.event_id AND entitlement.recipient_access_generation_id = ?
			  AND NOT content_is_withdrawn(
				placement.event_id, moment.draft_moment_id, placement.media_item_id
			  )
		  )
	`, accessID, eventID, accessID).Scan(ctx, &publicationID, &view.Title, &view.Description, &coverID)
	if errors.Is(err, sql.ErrNoRows) {
		return PublishedEventView{}, ErrNoPublication
	}
	if err != nil {
		return PublishedEventView{}, err
	}
	view.Authorized = true
	view.PublicationID = publicationID.String()
	if coverID != nil {
		value := coverID.String()
		view.CoverMediaID = &value
	}
	if s.recipientReadBoundary != nil {
		s.recipientReadBoundary()
	}
	if err := db.NewRaw(`
		SELECT media.id, published.media_type, published.width, published.height,
		       published.local_date_time, media.availability = 'current' AS available
		FROM current_published_placements AS placement
		JOIN current_audience_entitlements AS entitlement
		  ON entitlement.event_id = placement.event_id
		 AND entitlement.media_item_id = placement.media_item_id
		 AND entitlement.recipient_access_generation_id = ?
		JOIN published_media_placements AS published
		  ON published.published_moment_id = placement.published_moment_id
		 AND published.media_item_id = placement.media_item_id
		JOIN published_moments AS moment ON moment.id = placement.published_moment_id
		JOIN media_items AS media ON media.id = placement.media_item_id
		WHERE placement.event_id = ?
		  AND NOT content_is_withdrawn(
			placement.event_id, moment.draft_moment_id, placement.media_item_id
		  )
		ORDER BY placement.position
	`, accessID, eventID).Scan(ctx, &view.Media); err != nil {
		return PublishedEventView{}, fmt.Errorf("load filtered Event media: %w", err)
	}
	view.MediaCount = len(view.Media)
	if view.MediaCount == 0 {
		return PublishedEventView{}, ErrNoPublication
	}
	if view.CoverMediaID != nil {
		coverVisible := false
		for _, item := range view.Media {
			if item.ID == *view.CoverMediaID {
				coverVisible = true
				break
			}
		}
		if !coverVisible {
			cover := view.Media[0].ID
			view.CoverMediaID = &cover
		}
	}
	view.Capabilities = PreviewCapabilities{Comments: true, Favorites: true, Settings: true, Downloads: true, RecordEngagement: true}
	return view, nil
}
