package publishing

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"time"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/media"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

func accessByMoment(ctx context.Context, db bun.IDB, albumID string, moments []models.Moment, immichURL string) (map[models.UUID]MomentAccess, error) {
	result := make(map[models.UUID]MomentAccess, len(moments))
	for _, moment := range moments {
		result[moment.ID] = MomentAccess{People: []AccessPerson{}, Faces: []FaceRecord{}}
	}
	var people []models.Person
	if err := db.NewSelect().Model(&people).ColumnExpr("person.*").
		ColumnExpr("(SELECT coalesce(max(face.source_version), '') FROM media_face_associations AS face WHERE face.source_face_id = person.avatar_face_id) AS avatar_version").
		Where("person.deactivated_at IS NULL AND NOT person.is_curator").
		OrderExpr("lower(person.display_name), person.id").Scan(ctx); err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	var decisions []models.MomentAccessDecision
	if err := db.NewSelect().Model(&decisions).Where("decision.album_id = ?", albumID).Scan(ctx); err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	byMomentPerson := map[models.UUID]map[models.UUID]Decision{}
	for _, decision := range decisions {
		if byMomentPerson[decision.MomentID] == nil {
			byMomentPerson[decision.MomentID] = map[models.UUID]Decision{}
		}
		byMomentPerson[decision.MomentID][decision.PersonID] = Decision(decision.Decision)
	}
	type detectionRow struct {
		MomentID          models.UUID
		PersonID          models.UUID
		SupportingEntries int
	}
	var detections []detectionRow
	if err := db.NewSelect().TableExpr("album_entries AS entry").
		ColumnExpr("entry.moment_id, link.person_id, count(DISTINCT entry.id) AS supporting_entries").
		Join("JOIN media_face_associations AS face ON face.media_item_id = entry.media_item_id").
		Join("JOIN immich_face_links AS link ON link.source_id = face.source_face_id AND link.person_id IS NOT NULL AND NOT link.ignored").
		Join("JOIN persons AS person ON person.id = link.person_id AND person.deactivated_at IS NULL AND NOT person.is_curator").
		Where("entry.album_id = ? AND entry.removed_at IS NULL", albumID).
		Group("entry.moment_id", "link.person_id").Scan(ctx, &detections); err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	detected := map[models.UUID]map[models.UUID]int{}
	for _, row := range detections {
		if detected[row.MomentID] == nil {
			detected[row.MomentID] = map[models.UUID]int{}
		}
		detected[row.MomentID][row.PersonID] = row.SupportingEntries
	}
	type faceRow struct {
		MomentID      models.UUID
		SourceID      string
		SourceName    string
		SourceVersion string
		PersonID      *models.UUID
		PersonName    string
		Ignored       bool
		Occurrences   int
	}
	var faces []faceRow
	if err := db.NewSelect().TableExpr("album_entries AS entry").
		ColumnExpr("entry.moment_id, face.source_face_id AS source_id, min(face.source_name) AS source_name, max(face.source_version) AS source_version, link.person_id, coalesce(person.display_name, '') AS person_name, coalesce(link.ignored, false) AS ignored, count(DISTINCT entry.id) AS occurrences").
		Join("JOIN media_face_associations AS face ON face.media_item_id = entry.media_item_id").
		Join("LEFT JOIN immich_face_links AS link ON link.source_id = face.source_face_id").
		Join("LEFT JOIN persons AS person ON person.id = link.person_id").
		Where("entry.album_id = ? AND entry.removed_at IS NULL", albumID).
		Group("entry.moment_id", "face.source_face_id", "link.person_id", "person.display_name", "link.ignored").
		OrderExpr("min(face.source_name), face.source_face_id").Scan(ctx, &faces); err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	for _, row := range faces {
		access := result[row.MomentID]
		personID := ""
		if row.PersonID != nil {
			personID = row.PersonID.String()
		}
		immichLink := ""
		if immichURL != "" {
			immichLink = immichURL + "/people/" + url.PathEscape(row.SourceID)
		}
		access.Faces = append(access.Faces, FaceRecord{SourceID: row.SourceID, SourceName: row.SourceName,
			ThumbnailURL: media.FaceThumbnailURL(row.SourceID, row.SourceVersion), ImmichURL: immichLink, PersonID: personID,
			PersonName: row.PersonName, Ignored: row.Ignored, Occurrences: row.Occurrences})
		result[row.MomentID] = access
	}
	type refreshRow struct {
		MomentID    models.UUID
		RefreshedAt time.Time
	}
	var refreshes []refreshRow
	if err := db.NewSelect().TableExpr("album_entries AS entry").
		ColumnExpr("entry.moment_id, min(refresh.refreshed_at) AS refreshed_at").
		Join("LEFT JOIN media_face_refreshes AS refresh ON refresh.media_item_id = entry.media_item_id").
		Where("entry.album_id = ? AND entry.removed_at IS NULL", albumID).
		Group("entry.moment_id").
		Having("count(refresh.media_item_id) = count(entry.id)").Scan(ctx, &refreshes); err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	for _, row := range refreshes {
		access := result[row.MomentID]
		refreshedAt := row.RefreshedAt
		access.RefreshedAt = &refreshedAt
		result[row.MomentID] = access
	}
	for _, moment := range moments {
		access := result[moment.ID]
		for _, person := range people {
			avatarURL := ""
			if person.AvatarFaceID != nil {
				avatarURL = media.AvatarURL(person.ID.String(), *person.AvatarFaceID, person.AvatarVersion)
			}
			supporting := detected[moment.ID][person.ID]
			decision := byMomentPerson[moment.ID][person.ID]
			access.People = append(access.People, AccessPerson{PersonID: person.ID.String(), DisplayName: person.DisplayName,
				AvatarURL: avatarURL, Decision: decision, Detected: supporting > 0, Suggested: supporting > 0 && decision == "", SupportingEntries: supporting})
		}
		result[moment.ID] = access
	}
	return result, nil
}

func (m *Module) SetMomentAccess(ctx context.Context, albumID, momentID string, request SetMomentAccessRequest) (MomentAccessResult, error) {
	if !validDecision(request.Decision) {
		return MomentAccessResult{}, errcodes.ValidationFields("Check the highlighted fields.", map[string]string{"decision": "Choose allow or exclude."})
	}
	personID, err := uuid.Parse(request.PersonID)
	if err != nil {
		return MomentAccessResult{}, errcodes.ValidationFields("Check the highlighted fields.", map[string]string{"person_id": "Choose a Person."})
	}
	change := UndoAccessChange{PersonID: request.PersonID, Current: request.Decision}
	err = m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := albumRow(ctx, tx, albumID, true); err != nil {
			return err
		}
		moment, err := momentRow(ctx, tx, albumID, momentID, true)
		if err != nil {
			return err
		}
		var person models.Person
		err = tx.NewSelect().Model(&person).Where("person.id = ? AND person.deactivated_at IS NULL AND NOT person.is_curator", request.PersonID).Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return errcodes.ValidationFields("Check the highlighted fields.", map[string]string{"person_id": "Choose an active non-Curator Person."})
		}
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		var previous models.MomentAccessDecision
		err = tx.NewSelect().Model(&previous).Where("decision.moment_id = ? AND decision.person_id = ?", moment.ID, request.PersonID).For("UPDATE").Scan(ctx)
		if err == nil {
			change.Previous = Decision(previous.Decision)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return errorstack.CaptureContext(ctx, err)
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		row := models.MomentAccessDecision{MomentID: moment.ID, AlbumID: moment.AlbumID, PersonID: models.UUID(personID), Decision: string(request.Decision), UpdatedAt: now}
		change.CurrentUpdatedAt = now
		_, err = tx.NewInsert().Model(&row).On("CONFLICT (moment_id,person_id) DO UPDATE").
			Set("decision = EXCLUDED.decision").Set("updated_at = EXCLUDED.updated_at").Exec(ctx)
		return errorstack.CaptureContext(ctx, err)
	})
	if err != nil {
		return MomentAccessResult{}, transactionError(ctx, err)
	}
	album, err := m.GetAlbum(ctx, albumID)
	return MomentAccessResult{Album: album, Undo: UndoMomentAccessRequest{Changes: []UndoAccessChange{change}}}, err
}

func (m *Module) AddMomentSuggestions(ctx context.Context, albumID, momentID string) (MomentAccessResult, error) {
	changes := []UndoAccessChange{}
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := albumRow(ctx, tx, albumID, true); err != nil {
			return err
		}
		moment, err := momentRow(ctx, tx, albumID, momentID, true)
		if err != nil {
			return err
		}
		var personIDs []string
		decided := tx.NewSelect().Model((*models.MomentAccessDecision)(nil)).Column("person_id").Where("moment_id = ?", moment.ID)
		err = tx.NewSelect().TableExpr("album_entries AS entry").Distinct().
			ColumnExpr("link.person_id::text").
			Join("JOIN media_face_associations AS face ON face.media_item_id = entry.media_item_id").
			Join("JOIN immich_face_links AS link ON link.source_id = face.source_face_id AND link.person_id IS NOT NULL AND NOT link.ignored").
			Join("JOIN persons AS person ON person.id = link.person_id AND person.deactivated_at IS NULL AND NOT person.is_curator").
			Where("entry.album_id = ? AND entry.moment_id = ? AND entry.removed_at IS NULL", albumID, moment.ID).
			Where("link.person_id NOT IN (?)", decided).OrderExpr("link.person_id::text").Scan(ctx, &personIDs)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		rows := make([]models.MomentAccessDecision, 0, len(personIDs))
		now := time.Now().UTC().Truncate(time.Microsecond)
		for _, personID := range personIDs {
			rows = append(rows, models.MomentAccessDecision{MomentID: moment.ID, AlbumID: moment.AlbumID,
				PersonID: models.UUID(uuid.MustParse(personID)), Decision: string(DecisionAllow), UpdatedAt: now})
			changes = append(changes, UndoAccessChange{PersonID: personID, Current: DecisionAllow, CurrentUpdatedAt: now})
		}
		if len(rows) > 0 {
			if _, err := tx.NewInsert().Model(&rows).Exec(ctx); err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
		}
		return nil
	})
	if err != nil {
		return MomentAccessResult{}, transactionError(ctx, err)
	}
	album, err := m.GetAlbum(ctx, albumID)
	return MomentAccessResult{Album: album, Undo: UndoMomentAccessRequest{Changes: changes}}, err
}

func (m *Module) UndoMomentAccess(ctx context.Context, albumID, momentID string, request UndoMomentAccessRequest) (AlbumDetail, error) {
	if len(request.Changes) == 0 {
		return AlbumDetail{}, errcodes.ValidationError("There is no access change to undo.")
	}
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := albumRow(ctx, tx, albumID, true); err != nil {
			return err
		}
		moment, err := momentRow(ctx, tx, albumID, momentID, true)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, change := range request.Changes {
			_, parseErr := uuid.Parse(change.PersonID)
			if parseErr != nil || seen[change.PersonID] || !validDecision(change.Current) || change.CurrentUpdatedAt.IsZero() || (change.Previous != "" && !validDecision(change.Previous)) {
				return errcodes.ValidationError("This access change can no longer be undone.")
			}
			seen[change.PersonID] = true
			var current models.MomentAccessDecision
			err := tx.NewSelect().Model(&current).Where("decision.moment_id = ? AND decision.person_id = ?", moment.ID, change.PersonID).For("UPDATE").Scan(ctx)
			if errors.Is(err, sql.ErrNoRows) || err == nil && (Decision(current.Decision) != change.Current || !current.UpdatedAt.Equal(change.CurrentUpdatedAt)) {
				return &errcodes.Error{HTTPCode: http.StatusConflict, Code: "undo_stale", Message: "Access changed again, so this change can no longer be undone."}
			}
			if err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
			if change.Previous == "" {
				if _, err := tx.NewDelete().Model(&current).WherePK().Exec(ctx); err != nil {
					return errorstack.CaptureContext(ctx, err)
				}
				continue
			}
			current.Decision = string(change.Previous)
			current.UpdatedAt = time.Now().UTC()
			if _, err := tx.NewUpdate().Model(&current).Column("decision", "updated_at").WherePK().Exec(ctx); err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
		}
		return nil
	})
	if err != nil {
		return AlbumDetail{}, transactionError(ctx, err)
	}
	return m.GetAlbum(ctx, albumID)
}
