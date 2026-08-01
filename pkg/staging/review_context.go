package staging

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// PreserveMomentReview stores review evidence before a published Moment's
// editable Media membership is invalidated. The first private change owns the
// restoration context until the change cancels or is published.
func PreserveMomentReview(ctx context.Context, tx bun.Tx, eventID, momentID uuid.UUID, now time.Time) error {
	_, err := tx.NewRaw(`
		INSERT INTO staged_moment_review_restorations (
			event_id, draft_moment_id, base_publication_id,
			attendance_complete, audience_complete, moment_state, review_context,
			current_snapshot_id, created_at
		)
		SELECT event.id, moment.id, event.current_publication_id,
			moment.attendance_complete, moment.audience_complete, to_jsonb(moment),
			jsonb_build_object(
				'attendance', COALESCE((
					SELECT jsonb_agg(to_jsonb(item) ORDER BY item.person_id)
					FROM attendance AS item WHERE item.moment_id = moment.id
				), '[]'::jsonb),
				'overrides', COALESCE((
					SELECT jsonb_agg(to_jsonb(item) ORDER BY item.recipient_person_id)
					FROM audience_overrides AS item
					WHERE item.target_kind = 'moment' AND item.target_id = moment.id
				), '[]'::jsonb),
				'proposals', COALESCE((
					SELECT jsonb_agg(to_jsonb(item) ORDER BY item.recipient_person_id)
					FROM audience_proposals AS item
					WHERE item.target_kind = 'moment' AND item.target_id = moment.id
				), '[]'::jsonb),
				'reasons', COALESCE((
					SELECT jsonb_agg(to_jsonb(item) - 'id' ORDER BY item.recipient_person_id, item.kind, item.matching_person_id)
					FROM audience_reasons AS item
					WHERE item.target_kind = 'moment' AND item.target_id = moment.id
				), '[]'::jsonb)
			), current.snapshot_id, ?
		FROM events AS event
		JOIN draft_moments AS moment ON moment.event_id = event.id AND moment.id = ?
		JOIN published_moments AS published
		  ON published.publication_id = event.current_publication_id
		 AND published.draft_moment_id = moment.id
		LEFT JOIN current_audience_snapshots AS current
		  ON current.target_kind = 'moment' AND current.target_id = moment.id
		WHERE event.id = ? AND event.lifecycle = 'published'
		ON CONFLICT (event_id, draft_moment_id) DO NOTHING
	`, now, momentID, eventID).Exec(ctx)
	return err
}

// LoadMomentState returns the exact editable Moment row captured before a
// private published-membership change removed the Moment.
func LoadMomentState(ctx context.Context, db bun.IDB, eventID, momentID, publicationID uuid.UUID) ([]byte, error) {
	var state []byte
	err := db.NewRaw(`
		SELECT moment_state FROM staged_moment_review_restorations
		WHERE event_id = ? AND draft_moment_id = ? AND base_publication_id = ?
	`, eventID, momentID, publicationID).Scan(ctx, &state)
	return state, err
}

// RestoreMomentReviewIfPublishedResult restores preserved evidence only when
// the editable Moment has returned to the exact current-published Media set and
// no fresh review work has replaced the invalidated state.
func RestoreMomentReviewIfPublishedResult(ctx context.Context, tx bun.Tx, eventID, momentID uuid.UUID) (bool, error) {
	var publicationID uuid.UUID
	var attendanceComplete, audienceComplete, superseded bool
	var reviewContext []byte
	var snapshotID *uuid.UUID
	err := tx.NewRaw(`
		SELECT base_publication_id, attendance_complete, audience_complete,
			review_context, current_snapshot_id, superseded
		FROM staged_moment_review_restorations
		WHERE event_id = ? AND draft_moment_id = ? FOR UPDATE
	`, eventID, momentID).Scan(ctx, &publicationID, &attendanceComplete, &audienceComplete, &reviewContext, &snapshotID, &superseded)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	var currentPublicationID *uuid.UUID
	if err := tx.NewRaw(`SELECT current_publication_id FROM events WHERE id = ?`, eventID).Scan(ctx, &currentPublicationID); err != nil {
		return false, err
	}
	if currentPublicationID == nil || *currentPublicationID != publicationID {
		if err := discardMomentReview(ctx, tx, eventID, momentID); err != nil {
			return false, err
		}
		return false, nil
	}
	if superseded {
		return false, nil
	}

	var draftAttendanceComplete, draftAudienceComplete bool
	var evidenceRows int
	if err := tx.NewRaw(`
		SELECT moment.attendance_complete, moment.audience_complete,
			(SELECT count(*) FROM attendance WHERE moment_id = moment.id) +
			(SELECT count(*) FROM audience_overrides WHERE target_kind = 'moment' AND target_id = moment.id) +
			(SELECT count(*) FROM audience_proposals WHERE target_kind = 'moment' AND target_id = moment.id) +
			(SELECT count(*) FROM current_audience_snapshots WHERE target_kind = 'moment' AND target_id = moment.id)
		FROM draft_moments AS moment WHERE moment.event_id = ? AND moment.id = ?
	`, eventID, momentID).Scan(ctx, &draftAttendanceComplete, &draftAudienceComplete, &evidenceRows); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if draftAttendanceComplete || draftAudienceComplete || evidenceRows != 0 {
		if err := discardMomentReview(ctx, tx, eventID, momentID); err != nil {
			return false, err
		}
		return false, nil
	}

	var exact bool
	if err := tx.NewRaw(`
		WITH published AS (
			SELECT placement.media_item_id
			FROM published_moments AS moment
			JOIN published_media_placements AS placement ON placement.published_moment_id = moment.id
			WHERE moment.publication_id = ? AND moment.draft_moment_id = ?
		), editable AS (
			SELECT media_item_id FROM draft_media_placements
			WHERE event_id = ? AND draft_moment_id = ?
		)
		SELECT NOT EXISTS (
			(SELECT media_item_id FROM published EXCEPT SELECT media_item_id FROM editable)
			UNION ALL
			(SELECT media_item_id FROM editable EXCEPT SELECT media_item_id FROM published)
		)
	`, publicationID, momentID, eventID, momentID).Scan(ctx, &exact); err != nil {
		return false, err
	}
	if !exact {
		return false, nil
	}

	encoded := string(reviewContext)
	statements := []string{
		`INSERT INTO attendance (moment_id, person_id, source, confirmed_by_person_id, confirmed_at)
		 SELECT moment_id, person_id, source, confirmed_by_person_id, confirmed_at
		 FROM jsonb_to_recordset((?::jsonb)->'attendance') AS item(
			moment_id uuid, person_id uuid, source text, confirmed_by_person_id uuid, confirmed_at timestamptz)`,
		`INSERT INTO audience_overrides (target_kind, target_id, recipient_person_id, state, updated_by_person_id, updated_at)
		 SELECT target_kind, target_id, recipient_person_id, state, updated_by_person_id, updated_at
		 FROM jsonb_to_recordset((?::jsonb)->'overrides') AS item(
			target_kind text, target_id uuid, recipient_person_id uuid, state text, updated_by_person_id uuid, updated_at timestamptz)`,
		`INSERT INTO audience_proposals (target_kind, target_id, recipient_person_id, recipient_access_generation_id, included, recalculated_at)
		 SELECT target_kind, target_id, recipient_person_id, recipient_access_generation_id, included, recalculated_at
		 FROM jsonb_to_recordset((?::jsonb)->'proposals') AS item(
			target_kind text, target_id uuid, recipient_person_id uuid, recipient_access_generation_id uuid, included boolean, recalculated_at timestamptz)`,
		`INSERT INTO audience_reasons (target_kind, target_id, recipient_person_id, kind, matching_person_id)
		 SELECT target_kind, target_id, recipient_person_id, kind, matching_person_id
		 FROM jsonb_to_recordset((?::jsonb)->'reasons') AS item(
			target_kind text, target_id uuid, recipient_person_id uuid, kind text, matching_person_id uuid)`,
	}
	for _, statement := range statements {
		if _, err := tx.NewRaw(statement, encoded).Exec(ctx); err != nil {
			return false, fmt.Errorf("restore staged Moment review: %w", err)
		}
	}
	if snapshotID != nil {
		if _, err := tx.NewRaw(`
			INSERT INTO current_audience_snapshots (target_kind, target_id, snapshot_id)
			VALUES ('moment', ?, ?)
		`, momentID, *snapshotID).Exec(ctx); err != nil {
			return false, err
		}
	}
	if _, err := tx.NewRaw(`
		UPDATE draft_moments SET attendance_complete = ?, audience_complete = ?
		WHERE event_id = ? AND id = ?
	`, attendanceComplete, audienceComplete, eventID, momentID).Exec(ctx); err != nil {
		return false, err
	}
	if err := discardMomentReview(ctx, tx, eventID, momentID); err != nil {
		return false, err
	}
	return true, nil
}

// SupersedeMomentReviewRestoration prevents preserved evidence from being
// restored or replaced after the Curator starts a fresh review. The marker is
// retained until the private change completely cancels or is published.
func SupersedeMomentReviewRestoration(ctx context.Context, tx bun.Tx, momentID uuid.UUID) error {
	_, err := tx.NewRaw(`
		UPDATE staged_moment_review_restorations SET superseded = true
		WHERE draft_moment_id = ?
	`, momentID).Exec(ctx)
	return err
}

// ClearMomentReviewRestorations discards private restoration evidence after
// the corresponding editable result is published.
func ClearMomentReviewRestorations(ctx context.Context, tx bun.Tx, eventID uuid.UUID) error {
	_, err := tx.NewRaw(`DELETE FROM staged_moment_review_restorations WHERE event_id = ?`, eventID).Exec(ctx)
	return err
}

func discardMomentReview(ctx context.Context, tx bun.Tx, eventID, momentID uuid.UUID) error {
	_, err := tx.NewRaw(`
		DELETE FROM staged_moment_review_restorations
		WHERE event_id = ? AND draft_moment_id = ?
	`, eventID, momentID).Exec(ctx)
	return err
}
