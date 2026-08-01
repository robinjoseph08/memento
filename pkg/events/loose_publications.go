package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/internal/placementlock"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/staging"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// LooseItemSummary supports Curator navigation without exposing Audience membership.
type LooseItemSummary struct {
	ID               string    `json:"id"`
	Lifecycle        string    `json:"lifecycle"`
	Title            string    `json:"title"`
	Version          int64     `json:"version"`
	AudienceComplete bool      `json:"audience_complete"`
	HasStagedUpdate  bool      `json:"has_staged_update"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// LooseItemListResponse is the private Curator Loose item collection.
type LooseItemListResponse struct {
	LooseItems []LooseItemSummary `json:"loose_items"`
}

// UpdateLooseItemRequest replaces editable presentation state optimistically.
type UpdateLooseItemRequest struct {
	Version          int64    `json:"version" validate:"required,min=1"`
	Title            string   `json:"title,omitempty" validate:"max=240" mod:"trim"`
	Description      string   `json:"description,omitempty" validate:"max=2000" mod:"trim"`
	GroupingTimezone string   `json:"grouping_timezone" validate:"required,max=100"`
	ProposedDay      *string  `json:"proposed_day" tstype:"string | null,required"`
	PlaceLabels      []string `json:"place_labels"`
}

// PublishLooseItemRequest protects Publication from stale editable state.
type PublishLooseItemRequest struct {
	Version          int64 `json:"version" validate:"required,min=1"`
	NotifyRecipients *bool `json:"notify_recipients,omitempty" tstype:"boolean | null"`
}

// LoosePublicationResponse identifies the immutable Loose revision now current.
type LoosePublicationResponse struct {
	ID               string    `json:"id"`
	LooseItemID      string    `json:"loose_item_id"`
	Revision         int64     `json:"revision"`
	EditableVersion  int64     `json:"editable_version"`
	NotifyRecipients bool      `json:"notify_recipients"`
	CommittedAt      time.Time `json:"committed_at"`
}

// PublishedLooseItemView is one authorization-filtered independently published item.
type PublishedLooseItemView struct {
	Authorized    bool                `json:"authorized"`
	LooseItemID   string              `json:"loose_item_id"`
	PublicationID string              `json:"publication_id"`
	Title         string              `json:"title"`
	Description   string              `json:"description"`
	PlaceLabels   []string            `json:"place_labels"`
	ProposedDay   *string             `json:"proposed_day" tstype:"string | null,required"`
	Media         PublishedMedia      `json:"media"`
	Preview       bool                `json:"preview"`
	Capabilities  PreviewCapabilities `json:"capabilities"`
}

func (s *Service) ListLooseItems(ctx context.Context, actor setup.CuratorSession) (LooseItemListResponse, error) {
	if err := requireCurator(ctx, s.db, actor.PersonID); err != nil {
		return LooseItemListResponse{}, err
	}
	response := LooseItemListResponse{LooseItems: make([]LooseItemSummary, 0)}
	err := s.db.NewRaw(`SELECT id, lifecycle, title, version, audience_complete,
		current_staged_update_id IS NOT NULL AS has_staged_update, updated_at
		FROM loose_items WHERE lifecycle IN ('draft', 'published')
		ORDER BY updated_at DESC, id`).Scan(ctx, &response.LooseItems)
	return response, err
}

func (s *Service) UpdateLooseItem(ctx context.Context, actor setup.CuratorSession, id uuid.UUID, request UpdateLooseItemRequest) (LooseItem, error) {
	location, err := draftLocation(request.GroupingTimezone)
	labels, labelsValid := normalizePlaceLabels(request.PlaceLabels)
	if err != nil || !labelsValid || request.Version < 1 || utf8.RuneCountInString(strings.TrimSpace(request.Title)) > 240 || utf8.RuneCountInString(strings.TrimSpace(request.Description)) > 2000 {
		return LooseItem{}, ErrInvalid
	}
	var proposedDay *string
	if request.ProposedDay != nil {
		if _, err := time.Parse(time.DateOnly, *request.ProposedDay); err != nil {
			return LooseItem{}, ErrInvalid
		}
		value := *request.ProposedDay
		proposedDay = &value
	}
	now := s.now().UTC()
	var item LooseItem
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := requireCurator(ctx, tx, actor.PersonID); err != nil {
			return err
		}
		if err := staging.LockAccessSummaryRefresh(ctx, tx); err != nil {
			return err
		}
		var version int64
		var lifecycle string
		var publicationID *uuid.UUID
		if err := tx.NewRaw(`SELECT version, lifecycle, current_publication_id FROM loose_items WHERE id = ? FOR UPDATE`, id).Scan(ctx, &version, &lifecycle, &publicationID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if version != request.Version {
			return ErrVersionConflict
		}
		if lifecycle != "draft" && lifecycle != "published" {
			return ErrInvalid
		}
		if _, err := tx.NewRaw(`UPDATE loose_items SET title = ?, description = ?, grouping_timezone = ?, proposed_day = ?::date,
			place_labels = ?, version = version + 1, updated_at = ? WHERE id = ?`, strings.TrimSpace(request.Title),
			strings.TrimSpace(request.Description), location.String(), proposedDay, pgdialect.Array(labels), now, id).Exec(ctx); err != nil {
			return err
		}
		if publicationID != nil {
			if err := refreshLooseStagedUpdate(ctx, tx, id, *publicationID, now); err != nil {
				return err
			}
		}
		if err := appendDraftAudit(ctx, tx, actor, "loose_item_updated", map[string]any{"loose_item_id": id, "prior_version": version}); err != nil {
			return err
		}
		item, err = getLooseItem(ctx, tx, id)
		return err
	})
	return item, err
}

func refreshLooseStagedUpdate(ctx context.Context, tx bun.Tx, looseID, publicationID uuid.UUID, now time.Time) error {
	return staging.RefreshLoose(ctx, tx, looseID, publicationID, now)
}

// PublishLooseItem atomically appends immutable history and replaces every Loose projection.
func (s *Service) PublishLooseItem(ctx context.Context, actor setup.CuratorSession, looseID uuid.UUID, request PublishLooseItemRequest) (LoosePublicationResponse, error) {
	notify := true
	if request.NotifyRecipients != nil {
		notify = *request.NotifyRecipients
	}
	publicationID, now := uuid.New(), s.now().UTC()
	var response LoosePublicationResponse
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := requireCurator(ctx, tx, actor.PersonID); err != nil {
			return err
		}
		if err := staging.LockAccessSummaryReplacement(ctx, tx); err != nil {
			return err
		}
		if err := placementlock.Acquire(ctx, tx, placementlock.Shared); err != nil {
			return err
		}
		var mediaID uuid.UUID
		var title, description, timezone, lifecycle, labelsJSON string
		var proposedDay *string
		var version int64
		var audienceComplete bool
		var priorID, stagedID *uuid.UUID
		if err := tx.NewRaw(`SELECT media_item_id, title, description, grouping_timezone, lifecycle, proposed_day::text,
			version, audience_complete, current_publication_id, current_staged_update_id, to_json(place_labels)::text
			FROM loose_items WHERE id = ? FOR UPDATE`, looseID).Scan(ctx, &mediaID, &title, &description, &timezone,
			&lifecycle, &proposedDay, &version, &audienceComplete, &priorID, &stagedID, &labelsJSON); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if err := s.publicationBoundary(PublicationStepLocked); err != nil {
			return err
		}
		if version != request.Version {
			return ErrVersionConflict
		}
		if lifecycle != "draft" && lifecycle != "published" {
			return ErrPublicationNotReady
		}
		if priorID != nil {
			var pendingWithdrawal bool
			if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM content_withdrawals
				WHERE restored_at IS NULL AND ((target_kind = 'loose_item' AND target_id = ?)
				 OR (target_kind = 'media' AND target_id = ?)))`, looseID, mediaID).Scan(ctx, &pendingWithdrawal); err != nil {
				return err
			}
			if stagedID == nil && !pendingWithdrawal {
				return ErrPublicationNotReady
			}
			var priorVersion int64
			if err := tx.NewRaw(`SELECT editable_version FROM publications WHERE id = ?`, *priorID).Scan(ctx, &priorVersion); err != nil {
				return err
			}
			if priorVersion == version {
				return ErrPublicationNotReady
			}
		}
		var snapshotID uuid.UUID
		if err := tx.NewRaw(`SELECT snapshot_id FROM current_audience_snapshots WHERE target_kind = 'loose_item' AND target_id = ?`, looseID).Scan(ctx, &snapshotID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrPublicationNotReady
			}
			return err
		}
		if !audienceComplete {
			return ErrPublicationNotReady
		}
		type lockedAccess struct {
			Current bool   `bun:"is_current"`
			State   string `bun:"state"`
		}
		var accesses []lockedAccess
		if err := tx.NewRaw(`SELECT access.is_current, access.state FROM audience_snapshot_entries AS entry
			JOIN recipient_access_generations AS access ON access.id = entry.recipient_access_generation_id
			WHERE entry.snapshot_id = ? FOR SHARE OF access`, snapshotID).Scan(ctx, &accesses); err != nil {
			return err
		}
		for _, access := range accesses {
			if !access.Current || access.State == "suspended" || access.State == "revoked" {
				return ErrAudienceNotCurrent
			}
		}
		var availability string
		var activeBacking bool
		if err := tx.NewRaw(`SELECT availability,
			EXISTS (SELECT 1 FROM media_backings WHERE media_item_id = media_items.id AND active)
			FROM media_items WHERE id = ? FOR SHARE`, mediaID).Scan(ctx, &availability, &activeBacking); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrMediaUnavailable
			}
			return err
		}
		if availability != "current" || !activeBacking {
			return ErrMediaUnavailable
		}
		if _, err := tx.NewRaw(`CREATE TEMPORARY TABLE memento_prior_effective_entitlements
			ON COMMIT DROP AS SELECT DISTINCT recipient_access_generation_id AS access_id, media_item_id
			FROM current_media_entitlements WHERE media_item_id=?`, mediaID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `CREATE UNIQUE INDEX ON memento_prior_effective_entitlements (access_id, media_item_id)`); err != nil {
			return err
		}
		if err := s.publicationBoundary(PublicationStepValidated); err != nil {
			return err
		}
		var revision, contentRevision int64
		if err := tx.NewRaw(`SELECT COALESCE(max(revision), 0) + 1 FROM publications WHERE loose_item_id = ?`, looseID).Scan(ctx, &revision); err != nil {
			return err
		}
		if err := tx.NewRaw(`UPDATE system_settings SET content_revision = content_revision + 1 WHERE id = 1 RETURNING content_revision`).Scan(ctx, &contentRevision); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO publications (id, loose_item_id, revision, editable_version, prior_publication_id,
			published_by_person_id, notify_recipients, committed_at, content_revision) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, publicationID,
			looseID, revision, version, priorID, actor.PersonID, notify, now, contentRevision).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO published_loose_item_revisions (publication_id, loose_item_id, media_item_id,
			audience_snapshot_id, title, description, grouping_timezone, proposed_day, place_labels, media_type, width, height, local_date_time, created_at)
			SELECT ?, loose.id, loose.media_item_id, ?, loose.title, loose.description, loose.grouping_timezone, loose.proposed_day,
			loose.place_labels, media.media_type, media.width, media.height, media.local_date_time, ? FROM loose_items AS loose
			JOIN media_items AS media ON media.id = loose.media_item_id WHERE loose.id = ?`, publicationID, snapshotID, now, looseID).Exec(ctx); err != nil {
			return err
		}
		if err := s.publicationBoundary(PublicationStepHistory); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO published_loose_audience_entries (publication_id, recipient_person_id, recipient_access_generation_id)
			SELECT ?, recipient_person_id, recipient_access_generation_id FROM audience_snapshot_entries WHERE snapshot_id = ?`, publicationID, snapshotID).Exec(ctx); err != nil {
			return err
		}
		if err := s.publicationBoundary(PublicationStepAudiences); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO current_published_loose_items (loose_item_id, publication_id, media_item_id, title, description,
			grouping_timezone, proposed_day, place_labels, media_type, width, height, local_date_time, committed_at)
			SELECT loose_item_id, publication_id, media_item_id, title, description, grouping_timezone, proposed_day, place_labels,
			media_type, width, height, local_date_time, ? FROM published_loose_item_revisions WHERE publication_id = ?
			ON CONFLICT (loose_item_id) DO UPDATE SET publication_id=EXCLUDED.publication_id, media_item_id=EXCLUDED.media_item_id,
			title=EXCLUDED.title, description=EXCLUDED.description, grouping_timezone=EXCLUDED.grouping_timezone,
			proposed_day=EXCLUDED.proposed_day, place_labels=EXCLUDED.place_labels, media_type=EXCLUDED.media_type,
			width=EXCLUDED.width, height=EXCLUDED.height, local_date_time=EXCLUDED.local_date_time, committed_at=EXCLUDED.committed_at`, now, publicationID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE loose_items SET lifecycle='published', current_publication_id=?, updated_at=? WHERE id=?`, publicationID, now, looseID).Exec(ctx); err != nil {
			return err
		}
		if err := s.publicationBoundary(PublicationStepMetadata); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM current_loose_item_entitlements WHERE loose_item_id = ?`, looseID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO current_loose_item_entitlements (loose_item_id, publication_id, recipient_person_id,
			recipient_access_generation_id, media_item_id) SELECT ?, ?, recipient_person_id, recipient_access_generation_id, ?
			FROM published_loose_audience_entries WHERE publication_id = ?`, looseID, publicationID, mediaID, publicationID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM published_loose_search_documents WHERE loose_item_id = ?`, looseID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO published_loose_search_documents (loose_item_id, publication_id, media_item_id, search_text, capture_date, place_text)
			SELECT loose_item_id, publication_id, media_item_id, concat_ws(' ', title, description), memento_local_capture_date(local_date_time),
			array_to_string(place_labels, ' ') FROM current_published_loose_items WHERE loose_item_id = ?
			AND EXISTS (SELECT 1 FROM current_loose_item_entitlements WHERE loose_item_id = ?)`, looseID, looseID).Exec(ctx); err != nil {
			return err
		}
		if err := restoreEligibleLooseWithdrawals(ctx, tx, looseID, mediaID, publicationID, now, actor); err != nil {
			return err
		}
		if err := projectDeferredRestorationActivity(ctx, tx, publicationID, now); err != nil {
			return err
		}
		if err := staging.RefreshDependentAccessUpdates(ctx, tx, uuid.Nil, now); err != nil {
			return err
		}
		if err := s.publicationBoundary(PublicationStepEntitlements); err != nil {
			return err
		}
		if priorID != nil {
			if _, err := tx.NewRaw(`DELETE FROM new_for_you_entries WHERE publication_id IN (SELECT id FROM publications WHERE loose_item_id=? AND id<>?)`, looseID, publicationID).Exec(ctx); err != nil {
				return err
			}
		}
		if _, err := tx.NewRaw(`WITH qualified AS (SELECT entitlement.recipient_access_generation_id FROM current_loose_item_entitlements AS entitlement
			JOIN recipient_access_generations AS access ON access.id=entitlement.recipient_access_generation_id
			WHERE entitlement.loose_item_id=? AND access.is_current AND access.state='completed'
			AND NOT EXISTS (SELECT 1 FROM memento_prior_effective_entitlements AS prior
			 WHERE prior.access_id=entitlement.recipient_access_generation_id AND prior.media_item_id=entitlement.media_item_id))
			INSERT INTO new_for_you_entries (recipient_access_generation_id, publication_id) SELECT recipient_access_generation_id, ? FROM qualified`, looseID, publicationID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO publication_activity_items (publication_id, recipient_access_generation_id, created_at)
			SELECT ?, recipient_access_generation_id, ? FROM current_loose_item_entitlements AS entitlement
			JOIN recipient_access_generations AS access ON access.id=entitlement.recipient_access_generation_id
			WHERE loose_item_id=? AND access.is_current AND access.state='completed'
			AND NOT EXISTS (SELECT 1 FROM memento_prior_effective_entitlements AS prior
			 WHERE prior.access_id=entitlement.recipient_access_generation_id AND prior.media_item_id=entitlement.media_item_id)`, publicationID, now, looseID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO publication_notification_media (publication_id, recipient_access_generation_id, media_item_id)
			SELECT ?, recipient_access_generation_id, media_item_id FROM current_loose_item_entitlements WHERE loose_item_id=?
			AND recipient_access_generation_id IN (SELECT recipient_access_generation_id FROM publication_activity_items WHERE publication_id=?)`, publicationID, looseID, publicationID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO publication_curator_activity_items (publication_id, actor_person_id, created_at) VALUES (?, ?, ?)`, publicationID, actor.PersonID, now).Exec(ctx); err != nil {
			return err
		}
		if err := s.publicationBoundary(PublicationStepActivity); err != nil {
			return err
		}
		metadata, _ := json.Marshal(map[string]any{"publication_id": publicationID, "revision": revision, "editable_version": version, "notify_recipients": notify})
		if _, err := tx.NewRaw(`INSERT INTO publication_audit_events (event_id, target_kind, target_id, actor_person_id, action, metadata, created_at)
			VALUES (NULL, 'loose_item', ?, ?, 'loose_item_published', ?::jsonb, ?)`, looseID, actor.PersonID, string(metadata), now).Exec(ctx); err != nil {
			return err
		}
		if err := s.publicationBoundary(PublicationStepAudit); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"loose_item_id": looseID, "publication_id": publicationID, "notify_recipients": notify})
		if _, err := tx.NewRaw(`INSERT INTO outbox_events (kind, aggregate_kind, aggregate_id, aggregate_version, payload, available_at, created_at)
			VALUES ('publication_committed', 'loose_item_publication', ?, ?, ?::jsonb, ?, ?)`, looseID.String(), revision, string(payload), now, now).Exec(ctx); err != nil {
			return err
		}
		if err := s.publicationBoundary(PublicationStepOutbox); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE loose_items SET current_staged_update_id=NULL WHERE id=?`, looseID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM loose_staged_updates WHERE loose_item_id=?`, looseID).Exec(ctx); err != nil {
			return err
		}
		if err := s.publicationBoundary(PublicationStepStaged); err != nil {
			return err
		}
		response = LoosePublicationResponse{ID: publicationID.String(), LooseItemID: looseID.String(), Revision: revision, EditableVersion: version, NotifyRecipients: notify, CommittedAt: now}
		return nil
	})
	return response, err
}

func (s *Service) PreviewLooseItem(ctx context.Context, actor setup.CuratorSession, looseID, recipientPersonID uuid.UUID) (PublishedLooseItemView, error) {
	view := PublishedLooseItemView{LooseItemID: looseID.String(), Preview: true, PlaceLabels: make([]string, 0)}
	err := s.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead}, func(ctx context.Context, tx bun.Tx) error {
		if err := requireCurator(ctx, tx, actor.PersonID); err != nil {
			return err
		}
		var accessID uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM recipient_access_generations
			WHERE person_id = ? AND is_current AND state NOT IN ('suspended', 'revoked')`, recipientPersonID).Scan(ctx, &accessID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNoPublication
			}
			return err
		}
		var title, description, labelsJSON string
		var proposedDay *string
		var version int64
		var publicationID *uuid.UUID
		var media PublishedMedia
		var authorized bool
		err := tx.NewRaw(`SELECT loose.title, loose.description, loose.proposed_day::text, loose.version,
			loose.current_publication_id, to_json(loose.place_labels)::text,
			media.id, media.media_type, media.width, media.height, media.local_date_time,
			media.availability = 'current', EXISTS (
				SELECT 1 FROM audience_snapshot_entries AS entry
				WHERE entry.snapshot_id = snapshot.snapshot_id
				  AND entry.recipient_access_generation_id = ?)
			FROM loose_items AS loose
			JOIN media_items AS media ON media.id = loose.media_item_id
			JOIN current_audience_snapshots AS snapshot
			  ON snapshot.target_kind = 'loose_item' AND snapshot.target_id = loose.id
			WHERE loose.id = ? AND loose.lifecycle IN ('draft', 'published')`, accessID, looseID).Scan(ctx,
			&title, &description, &proposedDay, &version, &publicationID, &labelsJSON,
			&media.ID, &media.MediaType, &media.Width, &media.Height, &media.LocalDateTime,
			&media.Available, &authorized)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoPublication
		}
		if err != nil {
			return err
		}
		if publicationID != nil {
			view.PublicationID = publicationID.String()
		}
		if authorized {
			view.Authorized = true
			view.Title = title
			view.Description = description
			view.ProposedDay = proposedDay
			view.Media = media
			if err := json.Unmarshal([]byte(labelsJSON), &view.PlaceLabels); err != nil {
				return err
			}
		}
		if _, err := tx.NewRaw(`INSERT INTO loose_publication_preview_audit_events (
			loose_item_id, publication_id, editable_version, actor_person_id,
			recipient_person_id, recipient_access_generation_id, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`, looseID, publicationID, version, actor.PersonID,
			recipientPersonID, accessID, s.now().UTC()).Exec(ctx); err != nil {
			return err
		}
		return nil
	})
	return view, err
}

func (s *Service) RecipientLooseItem(ctx context.Context, actor setup.SessionActor, looseID uuid.UUID) (PublishedLooseItemView, error) {
	var view PublishedLooseItemView
	err := s.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var labelsJSON string
		if err := tx.NewRaw(`SELECT current.publication_id, current.title, current.description, current.proposed_day::text,
			to_json(current.place_labels)::text, current.media_item_id, current.media_type, current.width, current.height,
			current.local_date_time, media.availability='current'
			FROM current_published_loose_items AS current JOIN loose_items AS loose ON loose.id=current.loose_item_id AND loose.lifecycle='published'
			JOIN media_items AS media ON media.id=current.media_item_id
			JOIN current_media_entitlements AS entitlement ON entitlement.origin_kind='loose_item' AND entitlement.origin_id=current.loose_item_id
			 AND entitlement.publication_id=current.publication_id AND entitlement.recipient_access_generation_id=?
			JOIN recipient_access_generations AS access ON access.id=entitlement.recipient_access_generation_id AND access.person_id=?
			 AND access.is_current AND access.state='completed'
			WHERE current.loose_item_id=?`, actor.AccessID, actor.PersonID, looseID).Scan(ctx, &view.PublicationID, &view.Title,
			&view.Description, &view.ProposedDay, &labelsJSON, &view.Media.ID, &view.Media.MediaType, &view.Media.Width,
			&view.Media.Height, &view.Media.LocalDateTime, &view.Media.Available); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNoPublication
			}
			return err
		}
		if err := json.Unmarshal([]byte(labelsJSON), &view.PlaceLabels); err != nil {
			return err
		}
		view.Authorized, view.LooseItemID = true, looseID.String()
		view.Capabilities = PreviewCapabilities{Comments: true, Favorites: true, Settings: true, Downloads: true, RecordEngagement: true}
		return nil
	})
	return view, err
}
