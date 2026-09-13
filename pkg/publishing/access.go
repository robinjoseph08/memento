package publishing

import (
	"context"
	"database/sql"
	"errors"
	"maps"
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
				AvatarURL: avatarURL, Decision: decision, Detected: supporting > 0, SupportingEntries: supporting})
		}
		result[moment.ID] = access
	}
	return result, nil
}

// attachAccess computes the Curator scope summaries from the same saved
// rules the viewer evaluator uses. Entries carry only their explicit rules.
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
	for i := range album.Moments {
		for j := range album.Moments[i].Entries {
			album.Moments[i].Entries[j].Decisions = map[string]Decision{}
		}
	}
	entryDecisions := map[string]map[string]Decision{}
	for _, row := range entryRows {
		id := row.EntryID.String()
		if entryDecisions[id] == nil {
			entryDecisions[id] = map[string]Decision{}
		}
		entryDecisions[id][row.PersonID.String()] = Decision(row.Decision)
	}
	for i := range album.Moments {
		for j := range album.Moments[i].Entries {
			entry := &album.Moments[i].Entries[j]
			maps.Copy(entry.Decisions, entryDecisions[entry.ID])
		}
	}
	people, err := accessPeople(ctx, db)
	if err != nil {
		return err
	}
	// accessByMoment lists every active Person on every Moment in the same order.
	momentPeople := make([]map[string]*AccessPerson, len(album.Moments))
	for i := range album.Moments {
		momentPeople[i] = make(map[string]*AccessPerson, len(album.Moments[i].Access.People))
		for j := range album.Moments[i].Access.People {
			momentPeople[i][album.Moments[i].Access.People[j].PersonID] = &album.Moments[i].Access.People[j]
		}
	}
	album.Access = []AccessPerson{}
	for _, row := range people {
		person := AccessPerson{PersonID: row.ID.String(), DisplayName: row.DisplayName, Decision: allows[row.ID.String()]}
		if row.AvatarFaceID != nil {
			person.AvatarURL = media.AvatarURL(row.ID.String(), *row.AvatarFaceID, row.AvatarVersion)
		}
		for i := range album.Moments {
			moment := &album.Moments[i]
			p := momentPeople[i][person.PersonID]
			if p != nil {
				p.Inherited = person.Decision == DecisionAllow
				p.Effective = entryAllowed(person.Decision, p.Decision, "")
				p.Suggested = p.Detected && p.Decision == "" && !p.Effective
				p.AccessibleCount = 0
				for _, entry := range moment.Entries {
					if entryAllowed(person.Decision, p.Decision, entry.Decisions[person.PersonID]) {
						p.AccessibleCount++
					}
					if entry.Decisions[person.PersonID] != "" {
						person.Exceptions++
					}
				}
				if p.Decision != "" {
					person.Exceptions++
				}
				person.AccessibleCount += p.AccessibleCount
				person.SupportingEntries += p.SupportingEntries
				if p.Detected {
					person.MomentsDetected++
				}
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

// albumAccessAfter applies the reviewed Album-wide choices to a copy of the
// current structure so the preview and the save describe the same effect.
func albumAccessAfter(ctx context.Context, db bun.IDB, state structureState, request SaveAlbumAccessRequest) (structureState, error) {
	after := state.clone()
	seen := map[string]bool{}
	for _, choice := range request.People {
		person, err := uuid.Parse(choice.PersonID)
		if err != nil || seen[person.String()] {
			return after, structureField("people", "Choose each Person once.")
		}
		seen[person.String()] = true
		if err := activeAccessPerson(ctx, db, person.String()); err != nil {
			if _, expected := errors.AsType[*errcodes.Error](err); expected {
				return after, structureField("people", "Choose active non-Curator Persons.")
			}
			return after, err
		}
		if choice.Allowed {
			after.AlbumDecisions[person.String()] = DecisionAllow
		} else {
			delete(after.AlbumDecisions, person.String())
		}
	}
	return after, nil
}

func (m *Module) PreviewAlbumAccess(ctx context.Context, albumID string, request SaveAlbumAccessRequest) (AlbumAccessPreview, error) {
	result := AlbumAccessPreview{Changes: []AudienceChange{}}
	err := m.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := albumRow(ctx, tx, albumID, false); err != nil {
			return err
		}
		state, err := m.loadStructure(ctx, tx, albumID, false)
		if err != nil {
			return err
		}
		after, err := albumAccessAfter(ctx, tx, state, request)
		if err != nil {
			return err
		}
		result.Changes = reviewedChanges(state, after)
		return nil
	})
	return result, transactionError(ctx, err)
}

// SaveAlbumAccess replaces only the Album-wide allows named in the request.
// Unchecking removes the Album allow and leaves narrower decisions alone.
func (m *Module) SaveAlbumAccess(ctx context.Context, albumID string, request SaveAlbumAccessRequest) (AlbumDetail, error) {
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := albumRow(ctx, tx, albumID, true); err != nil {
			return err
		}
		state, err := m.loadStructure(ctx, tx, albumID, true)
		if err != nil {
			return err
		}
		if _, err := albumAccessAfter(ctx, tx, state, request); err != nil {
			return err
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		for _, choice := range request.People {
			decision := DecisionInherit
			if choice.Allowed {
				decision = DecisionAllow
			}
			if err := writeAccessDecision(ctx, tx, accessTarget{albumID: albumID}, choice.PersonID, decision, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return AlbumDetail{}, transactionError(ctx, err)
	}
	return m.GetAlbum(ctx, albumID)
}

func removalPreview(state structureState, personID string) (RemoveAccessPreview, error) {
	result := RemoveAccessPreview{PersonID: personID, DisplayName: state.People[personID]}
	after := state.clone()
	delete(after.AlbumDecisions, personID)
	for _, decisions := range after.Decisions {
		delete(decisions, personID)
	}
	for _, decisions := range after.EntryDecisions {
		delete(decisions, personID)
	}
	result.Changes = reviewedChanges(state, after)
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
		for _, table := range []string{"album_access_decisions", "moment_access_decisions", "entry_access_decisions"} {
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

// accessTarget is internal to the persistence adapter. Scope-specific tables
// retain their foreign keys while sharing one write path.
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

// writeAccessDecision saves one explicit rule or, for inherit, removes it.
func writeAccessDecision(ctx context.Context, db bun.IDB, target accessTarget, personID string, decision Decision, now time.Time) error {
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
