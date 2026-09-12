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

func accessPeople(ctx context.Context, db bun.IDB) ([]models.Person, error) {
	var people []models.Person
	err := db.NewSelect().Model(&people).ColumnExpr("person.*").
		ColumnExpr("(SELECT coalesce(max(face.source_version), '') FROM media_face_associations AS face WHERE face.source_face_id = person.avatar_face_id) AS avatar_version").
		Where("person.deactivated_at IS NULL AND NOT person.is_curator").
		OrderExpr("lower(person.display_name), person.id").Scan(ctx)
	return people, errorstack.CaptureContext(ctx, err)
}

func accessByMoment(ctx context.Context, db bun.IDB, albumID string, moments []models.Moment, immichURL string) (map[models.UUID]MomentAccess, error) {
	result := make(map[models.UUID]MomentAccess, len(moments))
	for _, moment := range moments {
		result[moment.ID] = MomentAccess{People: []AccessPerson{}, Faces: []FaceRecord{}}
	}
	people, err := accessPeople(ctx, db)
	if err != nil {
		return nil, err
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

// attachAccess computes all Curator scope summaries from the same saved rules.
func attachAccess(ctx context.Context, db bun.IDB, album *AlbumDetail) error {
	var rows []models.AlbumAccessDecision
	if err := db.NewSelect().Model(&rows).Where("decision.album_id = ?", album.ID).Scan(ctx); err != nil {
		return errorstack.CaptureContext(ctx, err)
	}
	allows := map[string]Decision{}
	for _, row := range rows {
		allows[row.PersonID.String()] = Decision(row.Decision)
	}
	var entryRows []models.EntryAccessDecision
	if err := db.NewSelect().Model(&entryRows).Where("decision.album_id = ?", album.ID).Scan(ctx); err != nil {
		return errorstack.CaptureContext(ctx, err)
	}
	entryDecisions := map[string]map[string]Decision{}
	for _, row := range entryRows {
		if entryDecisions[row.EntryID.String()] == nil {
			entryDecisions[row.EntryID.String()] = map[string]Decision{}
		}
		entryDecisions[row.EntryID.String()][row.PersonID.String()] = Decision(row.Decision)
	}
	var detections []struct{ EntryID, PersonID string }
	if err := db.NewSelect().TableExpr("album_entries AS entry").Distinct().
		ColumnExpr("entry.id AS entry_id, link.person_id").
		Join("JOIN media_face_associations AS face ON face.media_item_id = entry.media_item_id").
		Join("JOIN immich_face_links AS link ON link.source_id = face.source_face_id AND link.person_id IS NOT NULL AND NOT link.ignored").
		Where("entry.album_id = ? AND entry.removed_at IS NULL", album.ID).Scan(ctx, &detections); err != nil {
		return errorstack.CaptureContext(ctx, err)
	}
	detected := map[string]map[string]bool{}
	for _, row := range detections {
		if detected[row.EntryID] == nil {
			detected[row.EntryID] = map[string]bool{}
		}
		detected[row.EntryID][row.PersonID] = true
	}
	album.Access = []AccessPerson{}
	for i := range album.Moments {
		for j := range album.Moments[i].Entries {
			album.Moments[i].Entries[j].Access = []AccessPerson{}
		}
	}
	people, err := accessPeople(ctx, db)
	if err != nil {
		return err
	}
	for _, row := range people {
		person := AccessPerson{PersonID: row.ID.String(), DisplayName: row.DisplayName}
		if row.AvatarFaceID != nil {
			person.AvatarURL = media.AvatarURL(row.ID.String(), *row.AvatarFaceID, row.AvatarVersion)
		}
		person.Decision = allows[person.PersonID]
		person.Detected, person.Suggested = false, false
		person.SupportingEntries = 0
		person.Inherited = person.Decision == ""
		for i := range album.Moments {
			moment := &album.Moments[i]
			for j := range moment.Access.People {
				p := &moment.Access.People[j]
				if p.PersonID != person.PersonID {
					continue
				}
				p.Inherited = p.Decision == ""
				p.Effective = entryAllowed(person.Decision, p.Decision, "")
				p.Suggested = p.Detected && p.Decision == "" && !p.Effective
				p.AccessibleCount, p.ExcludedCount = 0, 0
				for k := range moment.Entries {
					entry := &moment.Entries[k]
					item := *p
					item.Decision = entryDecisions[entry.ID][person.PersonID]
					item.Inherited = item.Decision == ""
					item.Effective = entryAllowed(person.Decision, p.Decision, item.Decision)
					item.Detected = detected[entry.ID][person.PersonID]
					item.Suggested = item.Detected && item.Decision == "" && !item.Effective
					item.SupportingEntries = 0
					if item.Detected {
						item.SupportingEntries = 1
					}
					item.AccessibleCount, item.ExcludedCount = 0, 0
					if item.Effective {
						item.AccessibleCount = 1
						p.AccessibleCount++
					} else {
						item.ExcludedCount = 1
						p.ExcludedCount++
					}
					entry.Access = append(entry.Access, item)
				}
				person.AccessibleCount += p.AccessibleCount
				person.ExcludedCount += p.ExcludedCount
				person.SupportingEntries += p.SupportingEntries
			}
		}
		person.Effective = person.AccessibleCount > 0
		person.Detected = person.SupportingEntries > 0
		person.Suggested = person.Detected && person.Decision == "" && !person.Effective
		album.Access = append(album.Access, person)
	}
	return nil
}

func activeAccessPerson(ctx context.Context, db bun.IDB, personID string) error {
	if _, err := uuid.Parse(personID); err != nil {
		return structureField("person_id", "Choose a Person.")
	}
	exists, err := db.NewSelect().Model((*models.Person)(nil)).Where("person.id = ? AND person.deactivated_at IS NULL AND NOT person.is_curator", personID).Exists(ctx)
	if err != nil {
		return errorstack.CaptureContext(ctx, err)
	}
	if !exists {
		return structureField("person_id", "Choose an active non-Curator Person.")
	}
	return nil
}

func (m *Module) SetAlbumAccess(ctx context.Context, albumID string, request SetAlbumAccessRequest) (AccessResult, error) {
	if request.Decision != DecisionAllow && request.Decision != DecisionInherit {
		return AccessResult{}, structureField("decision", "Choose allow or inherit.")
	}
	return m.setAccess(ctx, accessTarget{albumID: albumID}, request.PersonID, request.Decision)
}

func accessEntry(ctx context.Context, db bun.IDB, albumID, entryID string) (models.AlbumEntry, error) {
	var entry models.AlbumEntry
	if _, err := uuid.Parse(entryID); err != nil {
		return entry, structureField("entry_id", "Choose an item in this Album.")
	}
	err := db.NewSelect().Model(&entry).Where("album_entry.id = ? AND album_entry.album_id = ? AND album_entry.removed_at IS NULL", entryID, albumID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return entry, structureField("entry_id", "Choose an item in this Album.")
	}
	return entry, errorstack.CaptureContext(ctx, err)
}

func (m *Module) SetEntryAccess(ctx context.Context, albumID, entryID string, request SetEntryAccessRequest) (AccessResult, error) {
	if _, err := uuid.Parse(entryID); err != nil {
		return AccessResult{}, structureField("entry_id", "Choose an item in this Album.")
	}
	return m.setAccess(ctx, accessTarget{albumID: albumID, entryID: entryID}, request.PersonID, request.Decision)
}

func (m *Module) SetMomentAccess(ctx context.Context, albumID, momentID string, request SetMomentAccessRequest) (MomentAccessResult, error) {
	if _, err := uuid.Parse(momentID); err != nil {
		return AccessResult{}, structureField("moment_id", "Choose a Moment in this Album.")
	}
	return m.setAccess(ctx, accessTarget{albumID: albumID, momentID: momentID}, request.PersonID, request.Decision)
}

func (m *Module) setAccess(ctx context.Context, target accessTarget, personID string, decision Decision) (AccessResult, error) {
	if !validDecision(decision) && decision != DecisionInherit {
		return AccessResult{}, structureField("decision", "Choose allow, exclude, or inherit.")
	}
	change := UndoAccessChange{PersonID: personID, Current: decision}
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := albumRow(ctx, tx, target.albumID, true); err != nil {
			return err
		}
		if err := target.validate(ctx, tx); err != nil {
			return err
		}
		if err := activeAccessPerson(ctx, tx, personID); err != nil {
			return err
		}
		table, column, id := target.table()
		var previous Decision
		err := tx.NewSelect().Table(table).Column("decision").Where("? = ? AND person_id = ?", bun.Ident(column), id, personID).Scan(ctx, &previous)
		if err == nil {
			change.Previous = previous
		} else if !errors.Is(err, sql.ErrNoRows) {
			return errorstack.CaptureContext(ctx, err)
		}
		change.CurrentUpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
		return writeAccessDecision(ctx, tx, target, personID, decision, change.CurrentUpdatedAt)
	})
	if err != nil {
		return AccessResult{}, transactionError(ctx, err)
	}
	album, err := m.GetAlbum(ctx, target.albumID)
	return AccessResult{Album: album, Undo: UndoMomentAccessRequest{Changes: []UndoAccessChange{change}}}, err
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
			Where("link.person_id NOT IN (?)", decided).
			Where("link.person_id NOT IN (?)", tx.NewSelect().Model((*models.AlbumAccessDecision)(nil)).Column("person_id").Where("album_id = ?", albumID)).
			OrderExpr("link.person_id::text").Scan(ctx, &personIDs)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		rows := make([]models.MomentAccessDecision, 0, len(personIDs))
		now := time.Now().UTC().Truncate(time.Microsecond)
		for _, personID := range personIDs {
			if err := recordAccessDeletion(ctx, tx, accessTarget{albumID: albumID, momentID: momentID}, personID, DecisionAllow, now); err != nil {
				return err
			}
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

func removalPreview(state structureState, personID string) (RemoveAccessPreview, error) {
	result := RemoveAccessPreview{PersonID: personID, DisplayName: state.People[personID], AccessibleCount: len(visibleEntries(state.facts(), personID))}
	after := state.clone()
	if after.AlbumDecisions[personID] != "" {
		result.AlbumDecisions = 1
		delete(after.AlbumDecisions, personID)
	}
	for _, decisions := range after.Decisions {
		if decisions[personID] != "" {
			result.MomentDecisions++
			delete(decisions, personID)
		}
	}
	for _, decisions := range after.EntryDecisions {
		if decisions[personID] != "" {
			result.EntryDecisions++
			delete(decisions, personID)
		}
	}
	token, err := reviewToken("remove-access", state, personID, after)
	result.ReviewToken = token
	return result, err
}

func (m *Module) PreviewRemoveAccess(ctx context.Context, albumID string, request RemoveAccessPreviewRequest) (RemoveAccessPreview, error) {
	var result RemoveAccessPreview
	err := m.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := albumRow(ctx, tx, albumID, false); err != nil {
			return err
		}
		if err := activeAccessPerson(ctx, tx, request.PersonID); err != nil {
			return err
		}
		state, err := m.loadStructure(ctx, tx, albumID, false)
		if err != nil {
			return err
		}
		result, err = removalPreview(state, uuid.MustParse(request.PersonID).String())
		return err
	})
	return result, transactionError(ctx, err)
}

func (m *Module) RemoveAccess(ctx context.Context, albumID string, request RemoveAccessRequest) (AlbumDetail, error) {
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := albumRow(ctx, tx, albumID, true); err != nil {
			return err
		}
		if err := activeAccessPerson(ctx, tx, request.PersonID); err != nil {
			return err
		}
		state, err := m.loadStructure(ctx, tx, albumID, true)
		if err != nil {
			return err
		}
		preview, err := removalPreview(state, uuid.MustParse(request.PersonID).String())
		if err != nil {
			return err
		}
		if request.ReviewToken == "" || preview.ReviewToken != request.ReviewToken {
			return staleReview()
		}
		for _, table := range []string{"album_access_decisions", "moment_access_decisions", "entry_access_decisions", "access_deletion_undos"} {
			if _, err := tx.NewDelete().Table(table).Where("album_id = ? AND person_id = ?", albumID, request.PersonID).Exec(ctx); err != nil {
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

func (m *Module) SaveMomentRules(ctx context.Context, albumID, momentID string, request SaveRulesRequest) (AlbumDetail, error) {
	if _, err := uuid.Parse(momentID); err != nil {
		return AlbumDetail{}, structureField("moment_id", "Choose a Moment in this Album.")
	}
	return m.saveRules(ctx, accessTarget{albumID: albumID, momentID: momentID}, request)
}

func (m *Module) SaveEntryRules(ctx context.Context, albumID, entryID string, request SaveRulesRequest) (AlbumDetail, error) {
	if _, err := uuid.Parse(entryID); err != nil {
		return AlbumDetail{}, structureField("entry_id", "Choose an item in this Album.")
	}
	return m.saveRules(ctx, accessTarget{albumID: albumID, entryID: entryID}, request)
}

func (m *Module) saveRules(ctx context.Context, target accessTarget, request SaveRulesRequest) (AlbumDetail, error) {
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := albumRow(ctx, tx, target.albumID, true); err != nil {
			return err
		}
		if err := target.validate(ctx, tx); err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, resolution := range request.Decisions {
			person, err := uuid.Parse(resolution.PersonID)
			if err != nil || seen[person.String()] || (!validDecision(resolution.Decision) && resolution.Decision != DecisionInherit) {
				return structureField("decisions", "Choose one valid rule for each Person.")
			}
			seen[person.String()] = true
			if err := activeAccessPerson(ctx, tx, person.String()); err != nil {
				if _, expected := errors.AsType[*errcodes.Error](err); expected {
					return structureField("decisions", "Choose active non-Curator Persons.")
				}
				return err
			}
			if err := writeAccessDecision(ctx, tx, target, person.String(), resolution.Decision, time.Now().UTC().Truncate(time.Microsecond)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return AlbumDetail{}, transactionError(ctx, err)
	}
	return m.GetAlbum(ctx, target.albumID)
}

func (m *Module) UndoMomentAccess(ctx context.Context, albumID, momentID string, request UndoMomentAccessRequest) (AlbumDetail, error) {
	if _, err := uuid.Parse(momentID); err != nil {
		return AlbumDetail{}, structureField("moment_id", "Choose a Moment in this Album.")
	}
	return m.undoAccess(ctx, accessTarget{albumID: albumID, momentID: momentID}, request)
}

func (m *Module) UndoAlbumAccess(ctx context.Context, albumID string, request UndoMomentAccessRequest) (AlbumDetail, error) {
	return m.undoAccess(ctx, accessTarget{albumID: albumID}, request)
}

func (m *Module) UndoEntryAccess(ctx context.Context, albumID, entryID string, request UndoMomentAccessRequest) (AlbumDetail, error) {
	if _, err := uuid.Parse(entryID); err != nil {
		return AlbumDetail{}, structureField("entry_id", "Choose an item in this Album.")
	}
	return m.undoAccess(ctx, accessTarget{albumID: albumID, entryID: entryID}, request)
}

// accessTarget is internal to the persistence adapter. Scope-specific tables
// retain their foreign keys while sharing Undo and atomic form handling.
type accessTarget struct{ albumID, momentID, entryID string }

func (target accessTarget) table() (string, string, string) {
	if target.momentID != "" {
		return "moment_access_decisions", "moment_id", target.momentID
	}
	if target.entryID != "" {
		return "entry_access_decisions", "entry_id", target.entryID
	}
	return "album_access_decisions", "album_id", target.albumID
}

func (target accessTarget) validate(ctx context.Context, db bun.IDB) error {
	if target.momentID != "" {
		if _, err := momentRow(ctx, db, target.albumID, target.momentID, false); err != nil {
			if errors.Is(err, errcodes.NotFound("Moment")) {
				return structureField("moment_id", "Choose a Moment in this Album.")
			}
			return err
		}
	}
	if target.entryID != "" {
		_, err := accessEntry(ctx, db, target.albumID, target.entryID)
		return err
	}
	return nil
}

func deletionScope(target accessTarget, personID string) models.AccessDeletionUndo {
	row := models.AccessDeletionUndo{AlbumID: models.UUID(uuid.MustParse(target.albumID)), PersonID: models.UUID(uuid.MustParse(personID))}
	if target.momentID != "" {
		id := models.UUID(uuid.MustParse(target.momentID))
		row.MomentID = &id
	}
	if target.entryID != "" {
		id := models.UUID(uuid.MustParse(target.entryID))
		row.EntryID = &id
	}
	return row
}

func recordAccessDeletion(ctx context.Context, db bun.IDB, target accessTarget, personID string, decision Decision, now time.Time) error {
	row := deletionScope(target, personID)
	_, err := db.NewDelete().Model(&row).Where("album_id = ? AND person_id = ? AND moment_id IS NOT DISTINCT FROM ?::uuid AND entry_id IS NOT DISTINCT FROM ?::uuid", row.AlbumID, row.PersonID, row.MomentID, row.EntryID).Exec(ctx)
	if err != nil {
		return errorstack.CaptureContext(ctx, err)
	}
	if decision != DecisionInherit {
		return nil
	}
	row.ID, row.UpdatedAt = models.NewUUIDv7(), now
	_, err = db.NewInsert().Model(&row).Exec(ctx)
	return errorstack.CaptureContext(ctx, err)
}

func writeAccessDecision(ctx context.Context, db bun.IDB, target accessTarget, personID string, decision Decision, now time.Time) error {
	if err := recordAccessDeletion(ctx, db, target, personID, decision, now); err != nil {
		return err
	}
	table, column, id := target.table()
	if decision == "" || decision == DecisionInherit {
		_, err := db.NewDelete().Table(table).Where("? = ? AND person_id = ?", bun.Ident(column), id, personID).Exec(ctx)
		return errorstack.CaptureContext(ctx, err)
	}
	albumUUID, personUUID := models.UUID(uuid.MustParse(target.albumID)), models.UUID(uuid.MustParse(personID))
	query := db.NewInsert()
	switch {
	case target.momentID != "":
		row := models.MomentAccessDecision{MomentID: models.UUID(uuid.MustParse(id)), AlbumID: albumUUID, PersonID: personUUID, Decision: string(decision), UpdatedAt: now}
		query = query.Model(&row)
	case target.entryID != "":
		row := models.EntryAccessDecision{EntryID: models.UUID(uuid.MustParse(id)), AlbumID: albumUUID, PersonID: personUUID, Decision: string(decision), UpdatedAt: now}
		query = query.Model(&row)
	default:
		row := models.AlbumAccessDecision{AlbumID: albumUUID, PersonID: personUUID, Decision: string(decision), UpdatedAt: now}
		query = query.Model(&row)
	}
	_, err := query.On("CONFLICT (?,person_id) DO UPDATE", bun.Ident(column)).Set("decision = EXCLUDED.decision").Set("updated_at = EXCLUDED.updated_at").Exec(ctx)
	return errorstack.CaptureContext(ctx, err)
}

func undoStale() error {
	return &errcodes.Error{HTTPCode: http.StatusConflict, Code: "undo_stale", Message: "Access changed again, so this change can no longer be undone."}
}

func (m *Module) undoAccess(ctx context.Context, target accessTarget, request UndoMomentAccessRequest) (AlbumDetail, error) {
	if len(request.Changes) == 0 {
		return AlbumDetail{}, structureField("changes", "There is no access change to undo.")
	}
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := albumRow(ctx, tx, target.albumID, true); err != nil {
			return err
		}
		if err := target.validate(ctx, tx); err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, change := range request.Changes {
			person, parseErr := uuid.Parse(change.PersonID)
			personID := person.String()
			if parseErr != nil || seen[personID] || (!validDecision(change.Current) && change.Current != DecisionInherit) || (change.Previous != "" && !validDecision(change.Previous)) || change.CurrentUpdatedAt.IsZero() || (target.momentID == "" && target.entryID == "" && (change.Current == DecisionDeny || change.Previous == DecisionDeny)) {
				return structureField("changes", "This access change can no longer be undone.")
			}
			seen[personID] = true
			if err := activeAccessPerson(ctx, tx, personID); err != nil {
				return err
			}
			table, column, id := target.table()
			var current struct {
				Decision  Decision
				UpdatedAt time.Time
			}
			err := tx.NewSelect().Table(table).Column("decision", "updated_at").Where("? = ? AND person_id = ?", bun.Ident(column), id, personID).Scan(ctx, &current)
			if change.Current == DecisionInherit {
				if err == nil {
					return undoStale()
				}
				if !errors.Is(err, sql.ErrNoRows) {
					return errorstack.CaptureContext(ctx, err)
				}
				deletion := deletionScope(target, personID)
				err = tx.NewSelect().Model(&deletion).Where("album_id = ? AND person_id = ? AND moment_id IS NOT DISTINCT FROM ?::uuid AND entry_id IS NOT DISTINCT FROM ?::uuid", deletion.AlbumID, deletion.PersonID, deletion.MomentID, deletion.EntryID).Scan(ctx)
				if errors.Is(err, sql.ErrNoRows) || err == nil && !deletion.UpdatedAt.Equal(change.CurrentUpdatedAt) {
					return undoStale()
				}
			} else if errors.Is(err, sql.ErrNoRows) || err == nil && (current.Decision != change.Current || !current.UpdatedAt.Equal(change.CurrentUpdatedAt)) {
				return undoStale()
			}
			if err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
			if err := writeAccessDecision(ctx, tx, target, personID, change.Previous, time.Now().UTC().Truncate(time.Microsecond)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return AlbumDetail{}, transactionError(ctx, err)
	}
	return m.GetAlbum(ctx, target.albumID)
}
