// Package activity integrates interaction events into first-party activity feeds.
package activity

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

var (
	ErrCommentUnavailable = errors.New("comment activity is unavailable")
	ErrInvalidReadState   = errors.New("curator read state is invalid")
	ErrVersionConflict    = errors.New("curator item version is stale")
	ErrWorkNotFound       = errors.New("curator work item not found")
	ErrInvalidCursor      = errors.New("curator activity cursor is invalid")
)

// Service records interaction activity at the boundary shared by Comments and Favorites.
type Service struct {
	db *bun.DB
}

func New(db *bun.DB) *Service { return &Service{db: db} }

// CuratorWorkItem is safe, versioned work with one clear next action.
type CuratorWorkItem struct {
	ID                 string       `json:"id"`
	Kind               string       `json:"kind"`
	SourceKind         string       `json:"source_kind"`
	SourceID           string       `json:"source_id"`
	Version            string       `json:"version"`
	Priority           int          `json:"priority"`
	Title              string       `json:"title"`
	Summary            string       `json:"summary"`
	CompletedSteps     []string     `json:"completed_steps"`
	RemainingSteps     []string     `json:"remaining_steps"`
	NextAction         string       `json:"next_action"`
	NextActionLabel    string       `json:"next_action_label"`
	NextActionTargetID string       `json:"next_action_target_id,omitempty"`
	Subject            *Attribution `json:"subject,omitempty"`
	Read               bool         `json:"read"`
	UpdatedAt          time.Time    `json:"updated_at"`
}

// MarkReadRequest binds durable read state to the exact work or activity version observed.
type MarkReadRequest struct {
	Surface    string `json:"surface" validate:"required,oneof=work activity"`
	SourceKind string `json:"source_kind" validate:"required"`
	SourceID   string `json:"source_id" validate:"required"`
	Version    string `json:"version" validate:"required"`
}

// WorkPageRequest selects one bounded work queue page.
type WorkPageRequest struct {
	Cursor string
	Limit  int
}

// CuratorWorkResponse is the bounded Curator work queue response.
type CuratorWorkResponse struct {
	Items      []CuratorWorkItem `json:"items"`
	NextCursor *string           `json:"next_cursor" tstype:"string | null,required"`
}

// Attribution names the Person responsible for or affected by an activity item.
type Attribution struct {
	PersonID   string `json:"person_id"`
	PersonName string `json:"person_name"`
}

// CuratorActivityItem is a safe immutable projection over authoritative audit and activity records.
type CuratorActivityItem struct {
	ID          string       `json:"id"`
	SourceKind  string       `json:"source_kind"`
	SourceID    string       `json:"source_id"`
	Version     string       `json:"version"`
	Category    string       `json:"category"`
	Action      string       `json:"action"`
	Actor       *Attribution `json:"actor" tstype:"Attribution | null,required"`
	Subject     *Attribution `json:"subject" tstype:"Attribution | null,required"`
	TargetKind  *string      `json:"target_kind" tstype:"string | null,required"`
	TargetID    *string      `json:"target_id" tstype:"string | null,required"`
	TargetLabel *string      `json:"target_label" tstype:"string | null,required"`
	Outcome     *string      `json:"outcome" tstype:"string | null,required"`
	Read        bool         `json:"read"`
	CreatedAt   time.Time    `json:"created_at"`
}

// CuratorActivityResponse is one deterministic chronological page.
type CuratorActivityResponse struct {
	Items      []CuratorActivityItem `json:"items"`
	NextCursor *string               `json:"next_cursor" tstype:"string | null,required"`
}

// PageRequest selects one safe category and an optional unread-only page.
type PageRequest struct {
	Category string
	Cursor   string
	Limit    int
	Unread   bool
}

type activityCursor struct {
	CreatedAt time.Time `json:"t"`
	ID        string    `json:"i"`
}

type workCursor struct {
	Priority  int       `json:"p"`
	UpdatedAt time.Time `json:"t"`
	ID        string    `json:"i"`
}

// ListCuratorWork returns delivery and privacy problems before editable work and new Sources.
func (s *Service) ListCuratorWork(ctx context.Context, page WorkPageRequest) (CuratorWorkResponse, error) {
	if page.Limit <= 0 {
		page.Limit = 50
	}
	if page.Limit > 100 {
		return CuratorWorkResponse{}, ErrInvalidCursor
	}
	cursor, err := decodeWorkCursor(page.Cursor)
	if err != nil {
		return CuratorWorkResponse{}, err
	}
	items := make([]CuratorWorkItem, 0)
	if err := s.listDeliveryWork(ctx, &items); err != nil {
		return CuratorWorkResponse{}, err
	}
	if err := s.listPrivacyWork(ctx, &items); err != nil {
		return CuratorWorkResponse{}, err
	}
	if err := s.listEventWork(ctx, &items); err != nil {
		return CuratorWorkResponse{}, err
	}
	if err := s.listSourceWork(ctx, &items); err != nil {
		return CuratorWorkResponse{}, err
	}
	sort.Slice(items, func(left, right int) bool { return workItemBefore(items[left], items[right]) })
	if cursor != nil {
		items = slices.DeleteFunc(items, func(item CuratorWorkItem) bool {
			return !workItemAfterCursor(item, *cursor)
		})
	}
	response := CuratorWorkResponse{Items: items}
	if len(items) > page.Limit {
		last := items[page.Limit-1]
		encoded := encodeWorkCursor(workCursor{Priority: last.Priority, UpdatedAt: last.UpdatedAt, ID: last.ID})
		response.NextCursor = &encoded
		response.Items = items[:page.Limit]
	}
	return response, nil
}

func workItemBefore(left, right CuratorWorkItem) bool {
	if left.Priority != right.Priority {
		return left.Priority < right.Priority
	}
	if !left.UpdatedAt.Equal(right.UpdatedAt) {
		return left.UpdatedAt.Before(right.UpdatedAt)
	}
	return left.ID < right.ID
}

func workItemAfterCursor(item CuratorWorkItem, cursor workCursor) bool {
	return item.Priority > cursor.Priority ||
		(item.Priority == cursor.Priority && item.UpdatedAt.After(cursor.UpdatedAt)) ||
		(item.Priority == cursor.Priority && item.UpdatedAt.Equal(cursor.UpdatedAt) && item.ID > cursor.ID)
}

func (s *Service) listDeliveryWork(ctx context.Context, items *[]CuratorWorkItem) error {
	var rows []struct {
		ID            string
		Diagnostic    string
		CreatedAt     time.Time
		RecipientID   *string
		RecipientName *string
		ReadVersion   *string
	}
	if err := s.db.NewRaw(`SELECT problem.id::text AS id, problem.diagnostic, problem.created_at,
		recipient.id::text AS recipient_id, recipient.display_name AS recipient_name, read.read_version
		FROM delivery_problems AS problem
		LEFT JOIN notification_batches AS batch ON batch.id = problem.notification_batch_id
		LEFT JOIN recipient_access_generations AS access ON access.id = batch.recipient_access_generation_id
		LEFT JOIN people AS recipient ON recipient.id = access.person_id
		LEFT JOIN curator_item_read_states AS read ON read.surface = 'work'
		 AND read.source_kind = 'delivery_problem' AND read.source_id = problem.id::text
		WHERE problem.resolved_at IS NULL`).Scan(ctx, &rows); err != nil {
		return err
	}
	for _, row := range rows {
		version := "problem " + row.ID
		item := CuratorWorkItem{
			ID: "delivery_problem:" + row.ID, Kind: "delivery_problem", SourceKind: "delivery_problem",
			SourceID: row.ID, Version: version, Priority: 0, Title: "Delivery problem",
			Summary: safeDeliveryDiagnostic(row.Diagnostic), NextAction: "review_delivery",
			NextActionLabel: "Review Recipient delivery", Read: row.ReadVersion != nil && *row.ReadVersion == version,
			CompletedSteps: []string{}, RemainingSteps: []string{"review_delivery"}, UpdatedAt: row.CreatedAt,
		}
		if row.RecipientID != nil && row.RecipientName != nil {
			item.Title = "Delivery problem for " + *row.RecipientName
			item.NextActionTargetID = *row.RecipientID
			item.Subject = &Attribution{PersonID: *row.RecipientID, PersonName: *row.RecipientName}
		}
		*items = append(*items, item)
	}
	return nil
}

func (s *Service) listPrivacyWork(ctx context.Context, items *[]CuratorWorkItem) error {
	var albums []struct {
		ID           string
		Name         string
		Version      int64
		MissingSince time.Time
		ReadVersion  *string
	}
	if err := s.db.NewRaw(`SELECT album.id::text AS id, album.name, album.version, album.missing_since,
		read.read_version FROM source_albums AS album
		LEFT JOIN curator_item_read_states AS read ON read.surface = 'work'
		 AND read.source_kind = 'source_problem' AND read.source_id = album.id::text
		WHERE album.source_missing`).Scan(ctx, &albums); err != nil {
		return err
	}
	for _, row := range albums {
		version := fmt.Sprintf("missing %d %d", row.Version, row.MissingSince.UTC().UnixNano())
		*items = append(*items, CuratorWorkItem{
			ID: "source_problem:" + row.ID, Kind: "privacy_problem", SourceKind: "source_problem",
			SourceID: row.ID, Version: version, Priority: 10, Title: row.Name,
			Summary: "Source album is missing", NextAction: "review_source_problem",
			NextActionLabel: "Review source problem", Read: row.ReadVersion != nil && *row.ReadVersion == version,
			CompletedSteps: []string{}, RemainingSteps: []string{"repair_source"}, UpdatedAt: row.MissingSince,
		})
	}
	var media []struct {
		ID               string
		MissingSince     time.Time
		CandidateCount   int
		CandidateVersion string
		ReadVersion      *string
	}
	if err := s.db.NewRaw(`SELECT media.id::text AS id, media.missing_since,
		(SELECT count(*) FROM media_repair_candidates AS candidate
		 WHERE candidate.media_item_id = media.id AND candidate.state = 'pending')::integer AS candidate_count,
		COALESCE((SELECT string_agg(candidate.id::text || ':' || extract(epoch FROM candidate.created_at)::text, ',' ORDER BY candidate.id)
		 FROM media_repair_candidates AS candidate
		 WHERE candidate.media_item_id = media.id AND candidate.state = 'pending'), '') AS candidate_version,
		read.read_version FROM media_items AS media
		LEFT JOIN curator_item_read_states AS read ON read.surface = 'work'
		 AND read.source_kind = 'media_problem' AND read.source_id = media.id::text
		WHERE media.availability = 'source_missing'`).Scan(ctx, &media); err != nil {
		return err
	}
	for _, row := range media {
		version := fmt.Sprintf("missing %d %s", row.MissingSince.UTC().UnixNano(), row.CandidateVersion)
		action, label := "review_source_problem", "Review unavailable Media"
		if row.CandidateCount > 0 {
			action, label = "review_repair", "Review repair candidate"
		}
		*items = append(*items, CuratorWorkItem{
			ID: "media_problem:" + row.ID, Kind: "privacy_problem", SourceKind: "media_problem",
			SourceID: row.ID, Version: version, Priority: 10, Title: "Published Media unavailable",
			Summary: "Media delivery is blocked until the Source is repaired", NextAction: action,
			NextActionLabel: label, Read: row.ReadVersion != nil && *row.ReadVersion == version,
			CompletedSteps: []string{}, RemainingSteps: []string{"repair_media"}, UpdatedAt: row.MissingSince,
		})
	}
	return nil
}

func (s *Service) listEventWork(ctx context.Context, items *[]CuratorWorkItem) error {
	type row struct {
		ID, Lifecycle, Title                                                   string
		Version                                                                int64
		Staged                                                                 bool
		FinalReview                                                            bool
		MomentCount, UnassignedCount, AttendanceIncomplete, AudienceIncomplete int
		UpdatedAt                                                              time.Time
		ReadVersion                                                            *string
	}
	var rows []row
	if err := s.db.NewRaw(`SELECT event.id::text AS id, event.lifecycle, event.title, event.version,
		(event.current_staged_update_id IS NOT NULL) AS staged, event.final_review_complete AS final_review,
		count(DISTINCT moment.id)::integer AS moment_count,
		count(DISTINCT placement.media_item_id) FILTER (WHERE placement.draft_moment_id IS NULL)::integer AS unassigned_count,
		count(DISTINCT moment.id) FILTER (WHERE NOT moment.attendance_complete)::integer AS attendance_incomplete,
		count(DISTINCT moment.id) FILTER (WHERE NOT moment.audience_complete)::integer AS audience_incomplete,
		event.updated_at, read.read_version
		FROM events AS event
		LEFT JOIN draft_moments AS moment ON moment.event_id = event.id
		LEFT JOIN draft_media_placements AS placement ON placement.event_id = event.id
		LEFT JOIN curator_item_read_states AS read ON read.surface = 'work'
		 AND read.source_kind = 'event' AND read.source_id = event.id::text
		WHERE event.lifecycle = 'draft' OR event.current_staged_update_id IS NOT NULL
		GROUP BY event.id, read.read_version`).Scan(ctx, &rows); err != nil {
		return err
	}
	for _, row := range rows {
		kind, prefix := "draft_event", "draft"
		if row.Staged {
			kind, prefix = "staged_update", "staged"
		}
		version := fmt.Sprintf("%s %d", prefix, row.Version)
		completed, remaining := eventProgress(row.MomentCount, row.UnassignedCount, row.AttendanceIncomplete, row.AudienceIncomplete, row.FinalReview)
		next := remaining[0]
		labels := map[string]string{"organize_media": "Organize Media", "review_attendance": "Review Attendance", "review_audiences": "Review Audiences", "final_review": "Complete final review", "publish": "Publish Event"}
		*items = append(*items, CuratorWorkItem{
			ID: "event:" + row.ID, Kind: kind, SourceKind: "event", SourceID: row.ID,
			Version: version, Priority: 20, Title: row.Title,
			Summary: fmt.Sprintf("%d of 5 review areas complete", len(completed)), CompletedSteps: completed,
			RemainingSteps: remaining, NextAction: next, NextActionLabel: labels[next],
			Read: row.ReadVersion != nil && *row.ReadVersion == version, UpdatedAt: row.UpdatedAt,
		})
	}
	return nil
}

func eventProgress(momentCount, unassignedCount, attendanceIncomplete, audienceIncomplete int, finalReview bool) ([]string, []string) {
	completed, remaining := make([]string, 0, 5), make([]string, 0, 5)
	if momentCount > 0 && unassignedCount == 0 {
		completed = append(completed, "organize_media")
	} else {
		remaining = append(remaining, "organize_media")
	}
	if momentCount > 0 && attendanceIncomplete == 0 {
		completed = append(completed, "review_attendance")
	} else {
		remaining = append(remaining, "review_attendance")
	}
	if momentCount > 0 && audienceIncomplete == 0 {
		completed = append(completed, "review_audiences")
	} else {
		remaining = append(remaining, "review_audiences")
	}
	if finalReview {
		completed = append(completed, "final_review")
	} else {
		remaining = append(remaining, "final_review")
	}
	remaining = append(remaining, "publish")
	return completed, remaining
}

func (s *Service) listSourceWork(ctx context.Context, items *[]CuratorWorkItem) error {
	var rows []struct {
		ID, Name    string
		Version     int64
		FirstSeen   time.Time
		ReadVersion *string
	}
	if err := s.db.NewRaw(`SELECT album.id::text AS id, album.name, album.version,
		album.first_seen_at, read.read_version FROM source_albums AS album
		LEFT JOIN curator_item_read_states AS read ON read.surface = 'work'
		 AND read.source_kind = 'source_album' AND read.source_id = album.id::text
		WHERE album.disposition = 'unreviewed' AND NOT album.source_missing`).Scan(ctx, &rows); err != nil {
		return err
	}
	for _, row := range rows {
		version := fmt.Sprintf("source %d", row.Version)
		*items = append(*items, CuratorWorkItem{
			ID: "source_album:" + row.ID, Kind: "new_source_album", SourceKind: "source_album",
			SourceID: row.ID, Version: version, Priority: 30, Title: row.Name,
			Summary: "New Source album ready for triage", CompletedSteps: []string{},
			RemainingSteps: []string{"triage_source"}, NextAction: "triage_source",
			NextActionLabel: "Review Source album", Read: row.ReadVersion != nil && *row.ReadVersion == version,
			UpdatedAt: row.FirstSeen,
		})
	}
	return nil
}

// MarkRead persists a read only when the caller observed the current item version.
func (s *Service) MarkRead(ctx context.Context, curatorID uuid.UUID, request MarkReadRequest) error {
	if (request.Surface != "work" && request.Surface != "activity") || request.SourceKind == "" || request.SourceID == "" || request.Version == "" || curatorID == uuid.Nil {
		return ErrInvalidReadState
	}
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var current string
		var err error
		if request.Surface == "work" {
			current, err = currentWorkVersion(ctx, tx, request.SourceKind, request.SourceID)
		} else {
			current, err = currentActivityVersion(ctx, tx, request.SourceKind, request.SourceID)
		}
		if err != nil {
			return err
		}
		if current != request.Version {
			return ErrVersionConflict
		}
		_, err = tx.NewRaw(`INSERT INTO curator_item_read_states
			(surface, source_kind, source_id, read_version, read_by_person_id, read_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (surface, source_kind, source_id) DO UPDATE
			SET read_version = EXCLUDED.read_version, read_by_person_id = EXCLUDED.read_by_person_id,
				read_at = EXCLUDED.read_at`, request.Surface, request.SourceKind, request.SourceID, request.Version,
			curatorID, time.Now().UTC()).Exec(ctx)
		return err
	})
}

func currentWorkVersion(ctx context.Context, tx bun.Tx, sourceKind, sourceID string) (string, error) {
	switch sourceKind {
	case "delivery_problem":
		var id string
		err := tx.NewRaw(`SELECT id::text FROM delivery_problems WHERE id::text = ? AND resolved_at IS NULL FOR SHARE`, sourceID).Scan(ctx, &id)
		if err == nil {
			return "problem " + id, nil
		}
		return "", workNotFound(err)
	case "event":
		var version int64
		var lifecycle string
		var staged bool
		err := tx.NewRaw(`SELECT version, lifecycle, current_staged_update_id IS NOT NULL FROM events
			WHERE id::text = ? AND (lifecycle = 'draft' OR current_staged_update_id IS NOT NULL) FOR SHARE`, sourceID).Scan(ctx, &version, &lifecycle, &staged)
		if err != nil {
			return "", workNotFound(err)
		}
		prefix := "draft"
		if staged {
			prefix = "staged"
		}
		return fmt.Sprintf("%s %d", prefix, version), nil
	case "source_album":
		var version int64
		err := tx.NewRaw(`SELECT version FROM source_albums WHERE id::text = ? AND disposition = 'unreviewed' AND NOT source_missing FOR SHARE`, sourceID).Scan(ctx, &version)
		if err != nil {
			return "", workNotFound(err)
		}
		return fmt.Sprintf("source %d", version), nil
	case "source_problem":
		var version int64
		var missing time.Time
		err := tx.NewRaw(`SELECT version, missing_since FROM source_albums WHERE id::text = ? AND source_missing FOR SHARE`, sourceID).Scan(ctx, &version, &missing)
		if err != nil {
			return "", workNotFound(err)
		}
		return fmt.Sprintf("missing %d %d", version, missing.UTC().UnixNano()), nil
	case "media_problem":
		var missing time.Time
		var candidates string
		err := tx.NewRaw(`SELECT media.missing_since,
			COALESCE((SELECT string_agg(candidate.id::text || ':' || extract(epoch FROM candidate.created_at)::text, ',' ORDER BY candidate.id)
			 FROM media_repair_candidates candidate WHERE candidate.media_item_id = media.id AND candidate.state = 'pending'), '')
			FROM media_items media WHERE media.id::text = ? AND media.availability = 'source_missing' FOR SHARE OF media`, sourceID).Scan(ctx, &missing, &candidates)
		if err != nil {
			return "", workNotFound(err)
		}
		return fmt.Sprintf("missing %d %s", missing.UTC().UnixNano(), candidates), nil
	default:
		return "", ErrInvalidReadState
	}
}

func currentActivityVersion(ctx context.Context, tx bun.Tx, sourceKind, sourceID string) (string, error) {
	queries := map[string]string{
		"security_audit":                 `SELECT 'audit ' || id::text FROM security_audit_events WHERE id::text = ?`,
		"publication":                    `SELECT 'publication ' || revision::text FROM publications WHERE id::text = ?`,
		"publication_audit":              `SELECT 'publication audit ' || id::text FROM publication_audit_events WHERE id::text = ? AND action IN ('content_withdrawn', 'content_restored_by_publication')`,
		"comment":                        `SELECT 'comment ' || id::text FROM comments WHERE id::text = ?`,
		"interaction_favorite":           `SELECT 'favorite ' || id::text FROM interaction_activity_items WHERE id::text = ? AND kind = 'favorite'`,
		"invitation_suggestion_activity": `SELECT 'suggestion activity ' || id::text FROM curator_activity_items WHERE id::text = ?`,
		"delivery_problem":               `SELECT 'problem ' || id::text FROM delivery_problems WHERE id::text = ?`,
		"delivery_problem_resolution":    `SELECT 'problem resolution ' || id::text || ' ' || extract(epoch FROM resolved_at)::text FROM delivery_problems WHERE id::text = ? AND resolved_at IS NOT NULL`,
	}
	query, valid := queries[sourceKind]
	if !valid {
		return "", ErrInvalidReadState
	}
	var version string
	if err := tx.NewRaw(query, sourceID).Scan(ctx, &version); err != nil {
		return "", workNotFound(err)
	}
	return version, nil
}

func workNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrWorkNotFound
	}
	return err
}

var activityCategories = map[string]struct{}{
	"security": {}, "publication": {}, "withdrawal": {}, "comment": {}, "favorite": {},
	"invitation_suggestion": {}, "delivery": {}, "access": {},
}

// ListCuratorActivity returns safe chronological history without raw metadata or private bodies.
func (s *Service) ListCuratorActivity(ctx context.Context, page PageRequest) (CuratorActivityResponse, error) {
	if page.Limit <= 0 {
		page.Limit = 50
	}
	if page.Limit > 100 || (page.Category != "" && !validActivityCategory(page.Category)) {
		return CuratorActivityResponse{}, ErrInvalidCursor
	}
	cursor, err := decodeActivityCursor(page.Cursor)
	if err != nil {
		return CuratorActivityResponse{}, err
	}
	cursorTime := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	cursorID := "~"
	if cursor != nil {
		cursorTime, cursorID = cursor.CreatedAt, cursor.ID
	}
	type row struct {
		SourceKind, SourceID, Version, Category, Action string
		ActorID, ActorName, SubjectID, SubjectName      *string
		TargetKind, TargetID, TargetLabel, Outcome      *string
		CreatedAt                                       time.Time
		Read                                            bool
	}
	var rows []row
	if err := s.db.NewRaw(`WITH base AS (
		SELECT 'security_audit'::text AS source_kind, audit.id::text AS source_id,
			'audit ' || audit.id::text AS version,
			CASE WHEN audit.action IN (
				'pending_recipient_designated', 'invitation_sent', 'invitation_reissued', 'invitation_revoked',
				'invitation_accepted', 'recipient_suspended', 'recipient_suspension_lifted',
				'recipient_access_revoked', 'recipient_email_changed', 'recipient_email_recovered'
			) THEN 'access' ELSE 'security' END AS category,
			audit.action, actor.id::text AS actor_id, actor.display_name AS actor_name,
			subject.id::text AS subject_id, subject.display_name AS subject_name,
			NULL::text AS target_kind, NULL::text AS target_id, NULL::text AS target_label,
			audit.outcome, audit.created_at
		FROM security_audit_events AS audit
		LEFT JOIN people AS actor ON actor.id = audit.actor_person_id
		LEFT JOIN people AS subject ON subject.id = audit.subject_person_id
		WHERE audit.action NOT LIKE 'invitation_suggestion_%'
		UNION ALL
		SELECT 'publication', activity.publication_id::text, 'publication ' || publication.revision::text,
			'publication', 'event_published', actor.id::text, actor.display_name,
			NULL::text, CAST(NULL AS text), 'event', event.id::text, event.title, NULL, activity.created_at
		FROM publication_curator_activity_items AS activity
		JOIN publications AS publication ON publication.id = activity.publication_id
		JOIN events AS event ON event.id = publication.event_id
		JOIN people AS actor ON actor.id = activity.actor_person_id
		UNION ALL
		SELECT 'publication_audit', audit.id::text, 'publication audit ' || audit.id::text,
			'withdrawal', audit.action, actor.id::text, actor.display_name,
			NULL::text, CAST(NULL AS text), audit.target_kind, audit.target_id::text, COALESCE(event.title, 'Content'), NULL, audit.created_at
		FROM publication_audit_events AS audit
		JOIN people AS actor ON actor.id = audit.actor_person_id
		LEFT JOIN events AS event ON event.id = audit.event_id
		WHERE audit.action IN ('content_withdrawn', 'content_restored_by_publication')
		UNION ALL
		SELECT 'comment', comment.id::text, 'comment ' || comment.id::text,
			'comment', 'comment_created', actor.id::text, actor.display_name,
			NULL::text, CAST(NULL AS text), 'media', comment.media_item_id::text, 'Media item', NULL, comment.created_at
		FROM comments AS comment
		JOIN people AS actor ON actor.id = comment.author_person_id
		UNION ALL
		SELECT 'interaction_favorite', interaction.id::text, 'favorite ' || interaction.id::text,
			'favorite', interaction.action, actor.id::text, actor.display_name,
			NULL::text, CAST(NULL AS text), 'media', interaction.media_item_id::text, 'Media item', NULL, interaction.created_at
		FROM interaction_activity_items AS interaction
		JOIN people AS actor ON actor.id = interaction.actor_person_id
		WHERE interaction.kind = 'favorite'
		UNION ALL
		SELECT 'invitation_suggestion_activity', activity.id::text, 'suggestion activity ' || activity.id::text,
			'invitation_suggestion', activity.action, actor.id::text, actor.display_name,
			requester.id::text, requester.display_name, 'invitation_suggestion', suggestion.id::text,
			'Invitation suggestion', NULL, activity.created_at
		FROM curator_activity_items AS activity
		JOIN people AS actor ON actor.id = activity.actor_person_id
		JOIN invitation_suggestions AS suggestion ON suggestion.id = activity.invitation_suggestion_id
		JOIN people AS requester ON requester.id = suggestion.requester_person_id
		UNION ALL
		SELECT 'delivery_problem', problem.id::text, 'problem ' || problem.id::text,
			'delivery', 'delivery_failed', NULL::text, CAST(NULL AS text),
			recipient.id::text, recipient.display_name, 'delivery_problem', problem.id::text,
			'Delivery problem', NULL::text, problem.created_at
		FROM delivery_problems AS problem
		LEFT JOIN notification_batches AS batch ON batch.id = problem.notification_batch_id
		LEFT JOIN recipient_access_generations AS access ON access.id = batch.recipient_access_generation_id
		LEFT JOIN people AS recipient ON recipient.id = access.person_id
		UNION ALL
		SELECT 'delivery_problem_resolution', problem.id::text,
			'problem resolution ' || problem.id::text || ' ' || extract(epoch FROM problem.resolved_at)::text,
			'delivery', 'delivery_problem_resolved', NULL::text, CAST(NULL AS text),
			recipient.id::text, recipient.display_name, 'delivery_problem', problem.id::text,
			'Delivery problem', NULL::text, problem.resolved_at
		FROM delivery_problems AS problem
		LEFT JOIN notification_batches AS batch ON batch.id = problem.notification_batch_id
		LEFT JOIN recipient_access_generations AS access ON access.id = batch.recipient_access_generation_id
		LEFT JOIN people AS recipient ON recipient.id = access.person_id
		WHERE problem.resolved_at IS NOT NULL
	), projected AS (
		SELECT base.*, COALESCE(read.read_version = base.version, false) AS read
		FROM base LEFT JOIN curator_item_read_states AS read
		 ON read.surface = 'activity' AND read.source_kind = base.source_kind AND read.source_id = base.source_id
	)
	SELECT * FROM projected
	WHERE (? = '' OR category = ?) AND (NOT ? OR NOT read)
	  AND (created_at, source_kind || ':' || source_id) < (?, ?)
	ORDER BY created_at DESC, source_kind || ':' || source_id DESC LIMIT ?`,
		page.Category, page.Category, page.Unread, cursorTime, cursorID, page.Limit+1).Scan(ctx, &rows); err != nil {
		return CuratorActivityResponse{}, err
	}
	response := CuratorActivityResponse{Items: make([]CuratorActivityItem, 0, min(len(rows), page.Limit))}
	if len(rows) > page.Limit {
		last := rows[page.Limit-1]
		encoded := encodeActivityCursor(activityCursor{CreatedAt: last.CreatedAt, ID: last.SourceKind + ":" + last.SourceID})
		response.NextCursor = &encoded
		rows = rows[:page.Limit]
	}
	for _, row := range rows {
		item := CuratorActivityItem{ID: row.SourceKind + ":" + row.SourceID, SourceKind: row.SourceKind,
			SourceID: row.SourceID, Version: row.Version, Category: row.Category, Action: row.Action,
			TargetKind: row.TargetKind, TargetID: row.TargetID, TargetLabel: row.TargetLabel,
			Outcome: row.Outcome, Read: row.Read, CreatedAt: row.CreatedAt}
		if row.ActorID != nil && row.ActorName != nil {
			item.Actor = &Attribution{PersonID: *row.ActorID, PersonName: *row.ActorName}
		}
		if row.SubjectID != nil && row.SubjectName != nil {
			item.Subject = &Attribution{PersonID: *row.SubjectID, PersonName: *row.SubjectName}
		}
		response.Items = append(response.Items, item)
	}
	return response, nil
}

func validActivityCategory(category string) bool {
	_, valid := activityCategories[category]
	return valid
}

func encodeWorkCursor(cursor workCursor) string {
	value, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(value)
}

func decodeWorkCursor(raw string) (*workCursor, error) {
	if raw == "" {
		return nil, nil
	}
	value, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var cursor workCursor
	if err := decoder.Decode(&cursor); err != nil {
		return nil, ErrInvalidCursor
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || cursor.Priority < 0 || cursor.UpdatedAt.IsZero() || cursor.ID == "" {
		return nil, ErrInvalidCursor
	}
	cursor.UpdatedAt = cursor.UpdatedAt.UTC()
	return &cursor, nil
}

func encodeActivityCursor(cursor activityCursor) string {
	value, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(value)
}

func decodeActivityCursor(raw string) (*activityCursor, error) {
	if raw == "" {
		return nil, nil
	}
	value, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var cursor activityCursor
	if err := decoder.Decode(&cursor); err != nil {
		return nil, ErrInvalidCursor
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || cursor.CreatedAt.IsZero() || cursor.ID == "" {
		return nil, ErrInvalidCursor
	}
	cursor.CreatedAt = cursor.CreatedAt.UTC()
	return &cursor, nil
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
