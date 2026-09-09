package publishing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"net/http"
	"sort"
	"strings"
	"time"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

type structureMoment struct {
	ID          string `json:"id"`
	CaptureDate string `json:"capture_date"`
	Title       string `json:"title"`
	SortOrder   int64  `json:"sort_order"`
	CoverID     string `json:"cover_id"`
}

type structureState struct {
	Moments      map[string]structureMoment     `json:"moments"`
	EntryMoments map[string]string              `json:"entry_moments"`
	Decisions    map[string]map[string]Decision `json:"decisions"`
	People       map[string]string              `json:"people"`
	PersonOrder  []string                       `json:"person_order"`
}

func (s structureState) facts() accessFacts {
	return accessFacts{EntryMoments: s.EntryMoments, Decisions: s.Decisions}
}

func (s structureState) clone() structureState {
	result := structureState{
		Moments:      make(map[string]structureMoment, len(s.Moments)),
		EntryMoments: make(map[string]string, len(s.EntryMoments)),
		Decisions:    make(map[string]map[string]Decision, len(s.Decisions)),
		People:       make(map[string]string, len(s.People)),
		PersonOrder:  append([]string(nil), s.PersonOrder...),
	}
	maps.Copy(result.Moments, s.Moments)
	maps.Copy(result.EntryMoments, s.EntryMoments)
	for momentID, decisions := range s.Decisions {
		result.Decisions[momentID] = make(map[string]Decision, len(decisions))
		maps.Copy(result.Decisions[momentID], decisions)
	}
	maps.Copy(result.People, s.People)
	return result
}

func (m *Module) loadStructure(ctx context.Context, db bun.IDB, albumID string, lock bool) (structureState, error) {
	state := structureState{Moments: map[string]structureMoment{}, EntryMoments: map[string]string{}, Decisions: map[string]map[string]Decision{}, People: map[string]string{}}
	var moments []models.Moment
	momentQuery := db.NewSelect().Model(&moments).Where("moment.album_id = ?", albumID).Order("moment.sort_order", "moment.id")
	if lock {
		momentQuery = momentQuery.For("UPDATE")
	}
	if err := momentQuery.Scan(ctx); err != nil {
		return state, errorstack.CaptureContext(ctx, err)
	}
	for _, moment := range moments {
		title := ""
		if moment.Title != nil {
			title = *moment.Title
		}
		state.Moments[moment.ID.String()] = structureMoment{ID: moment.ID.String(), CaptureDate: moment.CaptureDate, Title: title, SortOrder: moment.SortOrder, CoverID: moment.CoverEntryID.String()}
		state.Decisions[moment.ID.String()] = map[string]Decision{}
	}
	var entries []models.AlbumEntry
	entryQuery := db.NewSelect().Model(&entries).Where("album_entry.album_id = ? AND album_entry.removed_at IS NULL", albumID).Order("album_entry.id")
	if lock {
		entryQuery = entryQuery.For("UPDATE")
	}
	if err := entryQuery.Scan(ctx); err != nil {
		return state, errorstack.CaptureContext(ctx, err)
	}
	for _, entry := range entries {
		if entry.MomentID != nil {
			state.EntryMoments[entry.ID.String()] = entry.MomentID.String()
		}
	}
	var decisions []models.MomentAccessDecision
	decisionQuery := db.NewSelect().Model(&decisions).Where("decision.album_id = ?", albumID).Order("decision.moment_id", "decision.person_id")
	if lock {
		decisionQuery = decisionQuery.For("UPDATE")
	}
	if err := decisionQuery.Scan(ctx); err != nil {
		return state, errorstack.CaptureContext(ctx, err)
	}
	for _, decision := range decisions {
		state.Decisions[decision.MomentID.String()][decision.PersonID.String()] = Decision(decision.Decision)
	}
	var people []models.Person
	peopleQuery := db.NewSelect().Model(&people).Where("person.deactivated_at IS NULL AND NOT person.is_curator").OrderExpr("lower(person.display_name), person.id")
	if lock {
		peopleQuery = peopleQuery.For("SHARE")
	}
	if err := peopleQuery.Scan(ctx); err != nil {
		return state, errorstack.CaptureContext(ctx, err)
	}
	for _, person := range people {
		id := person.ID.String()
		state.People[id] = person.DisplayName
		state.PersonOrder = append(state.PersonOrder, id)
	}
	return state, nil
}

func reviewedChanges(before, after structureState) []AudienceChange {
	changes := audienceChanges(before.facts(), after.facts(), before.PersonOrder)
	for i := range changes {
		changes[i].DisplayName = before.People[changes[i].PersonID]
	}
	return changes
}

func reviewToken(operation string, before structureState, request any, after structureState) (string, error) {
	payload, err := json.Marshal(struct {
		Operation string         `json:"operation"`
		Before    structureState `json:"before"`
		Request   any            `json:"request"`
		After     structureState `json:"after"`
	}{Operation: operation, Before: before, Request: request, After: after})
	if err != nil {
		return "", errorstack.Capture(err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func staleReview() error {
	return &errcodes.Error{HTTPCode: http.StatusConflict, Code: "audience_changed", Message: "Album access or Moment membership changed after this review. Review the audience again before saving."}
}

func selectedEntries(state structureState, sourceMomentID string, entryIDs []string) (map[string]bool, int, error) {
	if len(entryIDs) == 0 {
		return nil, 0, structureField("entry_ids", "Select at least one item.")
	}
	selected := make(map[string]bool, len(entryIDs))
	for _, entryID := range entryIDs {
		if _, err := uuid.Parse(entryID); err != nil || selected[entryID] || state.EntryMoments[entryID] != sourceMomentID {
			return nil, 0, structureField("entry_ids", "Select media from this Moment.")
		}
		selected[entryID] = true
	}
	remaining := 0
	for _, momentID := range state.EntryMoments {
		if momentID == sourceMomentID {
			remaining++
		}
	}
	return selected, remaining - len(selected), nil
}

func canonicalEntries(ids []string) []string {
	result := append([]string(nil), ids...)
	sort.Strings(result)
	return result
}

func previewMove(state structureState, sourceMomentID string, request MoveEntriesRequest) (StructurePreview, structureState, error) {
	source, sourceOK := state.Moments[sourceMomentID]
	if !sourceOK {
		return StructurePreview{}, state, errcodes.NotFound("Moment")
	}
	if request.DestinationMomentID == sourceMomentID || state.Moments[request.DestinationMomentID].ID == "" {
		return StructurePreview{}, state, structureField("destination_moment_id", "Choose another Moment in this Album.")
	}
	selected, remaining, err := selectedEntries(state, sourceMomentID, request.EntryIDs)
	if err != nil {
		return StructurePreview{}, state, err
	}
	if remaining > 0 && selected[source.CoverID] {
		if request.ReplacementCoverEntryID == "" || selected[request.ReplacementCoverEntryID] || state.EntryMoments[request.ReplacementCoverEntryID] != sourceMomentID {
			return StructurePreview{}, state, structureField("replacement_cover_entry_id", "Choose a replacement cover from the media staying in this Moment.")
		}
	}
	after := state.clone()
	for entryID := range selected {
		after.EntryMoments[entryID] = request.DestinationMomentID
	}
	removes := remaining == 0
	if removes {
		delete(after.Moments, sourceMomentID)
		delete(after.Decisions, sourceMomentID)
	} else if selected[source.CoverID] {
		updated := after.Moments[sourceMomentID]
		updated.CoverID = request.ReplacementCoverEntryID
		after.Moments[sourceMomentID] = updated
	}
	canonical := request
	canonical.EntryIDs = canonicalEntries(request.EntryIDs)
	canonical.ReviewToken = ""
	preview := StructurePreview{Ready: true, RemovesMoment: removes, Changes: reviewedChanges(state, after), Conflicts: []AccessConflict{}}
	preview.ReviewToken, err = reviewToken("move", state, canonical, after)
	return preview, after, err
}

func (m *Module) PreviewMove(ctx context.Context, albumID, sourceMomentID string, request MoveEntriesRequest) (StructurePreview, error) {
	if _, err := albumRow(ctx, m.db, albumID, false); err != nil {
		return StructurePreview{}, err
	}
	state, err := m.loadStructure(ctx, m.db, albumID, false)
	if err != nil {
		return StructurePreview{}, err
	}
	preview, _, err := previewMove(state, sourceMomentID, request)
	return preview, err
}

func (m *Module) MoveEntries(ctx context.Context, albumID, sourceMomentID string, request MoveEntriesRequest) (AlbumDetail, error) {
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := albumRow(ctx, tx, albumID, true); err != nil {
			return err
		}
		state, err := m.loadStructure(ctx, tx, albumID, true)
		if err != nil {
			return err
		}
		preview, after, err := previewMove(state, sourceMomentID, request)
		if err != nil {
			return err
		}
		if request.ReviewToken == "" || request.ReviewToken != preview.ReviewToken {
			return staleReview()
		}
		source := state.Moments[sourceMomentID]
		if !preview.RemovesMoment && source.CoverID != request.ReplacementCoverEntryID {
			for _, id := range request.EntryIDs {
				if id == source.CoverID {
					if _, err := tx.NewUpdate().Model((*models.Moment)(nil)).Set("cover_entry_id = ?", request.ReplacementCoverEntryID).Where("id = ?", sourceMomentID).Exec(ctx); err != nil {
						return errorstack.CaptureContext(ctx, err)
					}
					break
				}
			}
		}
		destinationMomentID := after.EntryMoments[request.EntryIDs[0]]
		updated, err := tx.NewUpdate().Model((*models.AlbumEntry)(nil)).Set("moment_id = ?", destinationMomentID).
			Where("album_id = ? AND moment_id = ? AND id IN (?)", albumID, sourceMomentID, bun.List(request.EntryIDs)).Exec(ctx)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		count, err := updated.RowsAffected()
		if err != nil {
			return errorstack.Capture(err)
		}
		if count != int64(len(request.EntryIDs)) {
			return staleReview()
		}
		if preview.RemovesMoment {
			if _, err := tx.NewDelete().Model((*models.Moment)(nil)).Where("id = ? AND album_id = ?", sourceMomentID, albumID).Exec(ctx); err != nil {
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

func previewSplit(state structureState, sourceMomentID string, request SplitMomentRequest) (StructurePreview, structureState, error) {
	title, err := normalizedStructureTitle("new_title", request.NewTitle)
	if err != nil {
		return StructurePreview{}, state, err
	}
	source, ok := state.Moments[sourceMomentID]
	if !ok {
		return StructurePreview{}, state, errcodes.NotFound("Moment")
	}
	selected, remaining, err := selectedEntries(state, sourceMomentID, request.EntryIDs)
	if err != nil {
		return StructurePreview{}, state, err
	}
	if remaining == 0 {
		return StructurePreview{}, state, structureField("entry_ids", "Leave at least one item in the original Moment.")
	}
	if !selected[request.NewCoverEntryID] {
		return StructurePreview{}, state, structureField("new_cover_entry_id", "Choose a cover from the selected media.")
	}
	if selected[source.CoverID] && (request.ReplacementCoverEntryID == "" || selected[request.ReplacementCoverEntryID] || state.EntryMoments[request.ReplacementCoverEntryID] != sourceMomentID) {
		return StructurePreview{}, state, structureField("replacement_cover_entry_id", "Choose a replacement cover from the media staying in this Moment.")
	}
	after := state.clone()
	updated := after.Moments[sourceMomentID]
	if selected[source.CoverID] {
		updated.CoverID = request.ReplacementCoverEntryID
		after.Moments[sourceMomentID] = updated
	}
	// A fixed placeholder keeps the reviewed effect independent from the UUID
	// generated only when the transaction commits.
	const previewMomentID = "new-moment"
	maxOrder := int64(0)
	for _, moment := range state.Moments {
		maxOrder = max(maxOrder, moment.SortOrder)
	}
	after.Moments[previewMomentID] = structureMoment{ID: previewMomentID, CaptureDate: source.CaptureDate, Title: title, SortOrder: maxOrder + 1, CoverID: request.NewCoverEntryID}
	after.Decisions[previewMomentID] = map[string]Decision{}
	maps.Copy(after.Decisions[previewMomentID], state.Decisions[sourceMomentID])
	for entryID := range selected {
		after.EntryMoments[entryID] = previewMomentID
	}
	canonical := request
	canonical.EntryIDs = canonicalEntries(request.EntryIDs)
	canonical.NewTitle = title
	canonical.ReviewToken = ""
	preview := StructurePreview{Ready: true, Changes: reviewedChanges(state, after), Conflicts: []AccessConflict{}}
	preview.ReviewToken, err = reviewToken("split", state, canonical, after)
	return preview, after, err
}

func (m *Module) PreviewSplit(ctx context.Context, albumID, sourceMomentID string, request SplitMomentRequest) (StructurePreview, error) {
	if _, err := albumRow(ctx, m.db, albumID, false); err != nil {
		return StructurePreview{}, err
	}
	state, err := m.loadStructure(ctx, m.db, albumID, false)
	if err != nil {
		return StructurePreview{}, err
	}
	preview, _, err := previewSplit(state, sourceMomentID, request)
	return preview, err
}

func (m *Module) SplitMoment(ctx context.Context, albumID, sourceMomentID string, request SplitMomentRequest) (AlbumDetail, error) {
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := albumRow(ctx, tx, albumID, true); err != nil {
			return err
		}
		state, err := m.loadStructure(ctx, tx, albumID, true)
		if err != nil {
			return err
		}
		preview, after, err := previewSplit(state, sourceMomentID, request)
		if err != nil {
			return err
		}
		if request.ReviewToken == "" || request.ReviewToken != preview.ReviewToken {
			return staleReview()
		}
		source := state.Moments[sourceMomentID]
		resulting := after.Moments["new-moment"]
		newID := models.NewUUIDv7()
		coverID, _ := uuid.Parse(resulting.CoverID)
		row := models.Moment{ID: newID, AlbumID: models.UUID(uuid.MustParse(albumID)), CaptureDate: resulting.CaptureDate, SortOrder: resulting.SortOrder, CoverEntryID: models.UUID(coverID)}
		if resulting.Title != "" {
			row.Title = &resulting.Title
		}
		if _, err := tx.NewInsert().Model(&row).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		for _, entryID := range request.EntryIDs {
			if entryID == source.CoverID {
				if _, err := tx.NewUpdate().Model((*models.Moment)(nil)).Set("cover_entry_id = ?", request.ReplacementCoverEntryID).Where("id = ?", sourceMomentID).Exec(ctx); err != nil {
					return errorstack.CaptureContext(ctx, err)
				}
				break
			}
		}
		updated, err := tx.NewUpdate().Model((*models.AlbumEntry)(nil)).Set("moment_id = ?", newID).
			Where("album_id = ? AND moment_id = ? AND id IN (?)", albumID, sourceMomentID, bun.List(request.EntryIDs)).Exec(ctx)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		count, err := updated.RowsAffected()
		if err != nil {
			return errorstack.Capture(err)
		}
		if count != int64(len(request.EntryIDs)) {
			return staleReview()
		}
		rows := make([]models.MomentAccessDecision, 0, len(after.Decisions["new-moment"]))
		for personID, decision := range after.Decisions["new-moment"] {
			rows = append(rows, models.MomentAccessDecision{MomentID: newID, AlbumID: row.AlbumID, PersonID: models.UUID(uuid.MustParse(personID)), Decision: string(decision), UpdatedAt: time.Now().UTC()})
		}
		if len(rows) > 0 {
			if _, err := tx.NewInsert().Model(&rows).Exec(ctx); err != nil {
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

func normalizedResolutionMap(resolutions []AccessResolution) (map[string]Decision, error) {
	result := map[string]Decision{}
	for _, resolution := range resolutions {
		if _, err := uuid.Parse(resolution.PersonID); err != nil || result[resolution.PersonID] != "" || (resolution.Decision != DecisionAllow && resolution.Decision != DecisionDeny && resolution.Decision != DecisionInherit) {
			return nil, structureField("resolutions", "Choose one combined access decision for each conflict.")
		}
		result[resolution.PersonID] = resolution.Decision
	}
	return result, nil
}

func previewMerge(state structureState, sourceMomentID string, request MergeMomentsRequest) (StructurePreview, structureState, error) {
	title, err := normalizedStructureTitle("title", request.Title)
	if err != nil {
		return StructurePreview{}, state, err
	}
	source, sourceOK := state.Moments[sourceMomentID]
	target, targetOK := state.Moments[request.TargetMomentID]
	if !sourceOK {
		return StructurePreview{}, state, errcodes.NotFound("Moment")
	}
	if !targetOK || sourceMomentID == request.TargetMomentID {
		return StructurePreview{}, state, structureField("target_moment_id", "Choose another Moment in this Album.")
	}
	if state.EntryMoments[request.CoverEntryID] != sourceMomentID && state.EntryMoments[request.CoverEntryID] != request.TargetMomentID {
		return StructurePreview{}, state, structureField("cover_entry_id", "Choose a cover from the merged media.")
	}
	resolutions, err := normalizedResolutionMap(request.Resolutions)
	if err != nil {
		return StructurePreview{}, state, err
	}
	conflicts := []AccessConflict{}
	conflictSet := map[string]bool{}
	for _, personID := range state.PersonOrder {
		sourceDecision := state.Decisions[sourceMomentID][personID]
		targetDecision := state.Decisions[request.TargetMomentID][personID]
		if sourceDecision == targetDecision {
			continue
		}
		if sourceDecision == "" {
			sourceDecision = DecisionInherit
		}
		if targetDecision == "" {
			targetDecision = DecisionInherit
		}
		conflictSet[personID] = true
		conflicts = append(conflicts, AccessConflict{PersonID: personID, DisplayName: state.People[personID], Source: sourceDecision, Target: targetDecision})
	}
	for personID := range resolutions {
		if !conflictSet[personID] {
			return StructurePreview{}, state, structureField("resolutions", "Choose access only for the listed conflicts.")
		}
	}
	for personID := range conflictSet {
		if resolutions[personID] == "" {
			return StructurePreview{Ready: false, Changes: []AudienceChange{}, Conflicts: conflicts}, state, nil
		}
	}
	after := state.clone()
	for entryID, momentID := range after.EntryMoments {
		if momentID == sourceMomentID {
			after.EntryMoments[entryID] = request.TargetMomentID
		}
	}
	updated := target
	updated.CoverID = request.CoverEntryID
	if title != "" {
		updated.Title = title
	}
	after.Moments[request.TargetMomentID] = updated
	delete(after.Moments, sourceMomentID)
	mergedDecisions := make(map[string]Decision, len(state.Decisions[request.TargetMomentID]))
	maps.Copy(mergedDecisions, state.Decisions[request.TargetMomentID])
	for personID, decision := range resolutions {
		if decision == DecisionInherit {
			delete(mergedDecisions, personID)
		} else {
			mergedDecisions[personID] = decision
		}
	}
	// Preserve decisions for inactive people without forcing an invisible choice.
	for personID, decision := range state.Decisions[sourceMomentID] {
		if _, active := state.People[personID]; !active {
			if _, exists := mergedDecisions[personID]; !exists {
				mergedDecisions[personID] = decision
			}
		}
	}
	after.Decisions[request.TargetMomentID] = mergedDecisions
	delete(after.Decisions, sourceMomentID)
	canonical := request
	canonical.Title = title
	canonical.ReviewToken = ""
	canonical.Resolutions = append([]AccessResolution(nil), request.Resolutions...)
	sort.Slice(canonical.Resolutions, func(i, j int) bool { return canonical.Resolutions[i].PersonID < canonical.Resolutions[j].PersonID })
	preview := StructurePreview{Ready: true, Changes: reviewedChanges(state, after), Conflicts: conflicts}
	preview.ReviewToken, err = reviewToken("merge", state, canonical, after)
	_ = source
	return preview, after, err
}

func (m *Module) PreviewMerge(ctx context.Context, albumID, sourceMomentID string, request MergeMomentsRequest) (StructurePreview, error) {
	if _, err := albumRow(ctx, m.db, albumID, false); err != nil {
		return StructurePreview{}, err
	}
	state, err := m.loadStructure(ctx, m.db, albumID, false)
	if err != nil {
		return StructurePreview{}, err
	}
	preview, _, err := previewMerge(state, sourceMomentID, request)
	return preview, err
}

func (m *Module) MergeMoments(ctx context.Context, albumID, sourceMomentID string, request MergeMomentsRequest) (AlbumDetail, error) {
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := albumRow(ctx, tx, albumID, true); err != nil {
			return err
		}
		state, err := m.loadStructure(ctx, tx, albumID, true)
		if err != nil {
			return err
		}
		preview, after, err := previewMerge(state, sourceMomentID, request)
		if err != nil {
			return err
		}
		if !preview.Ready || request.ReviewToken == "" || request.ReviewToken != preview.ReviewToken {
			return staleReview()
		}
		updated, err := tx.NewUpdate().Model((*models.AlbumEntry)(nil)).Set("moment_id = ?", request.TargetMomentID).
			Where("album_id = ? AND moment_id = ?", albumID, sourceMomentID).Exec(ctx)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		count, err := updated.RowsAffected()
		if err != nil {
			return errorstack.Capture(err)
		}
		if count == 0 {
			return staleReview()
		}
		targetUpdate := tx.NewUpdate().Model((*models.Moment)(nil)).Set("cover_entry_id = ?", request.CoverEntryID).Where("id = ? AND album_id = ?", request.TargetMomentID, albumID)
		if title := strings.TrimSpace(request.Title); title != "" {
			targetUpdate = targetUpdate.Set("title = ?", title)
		}
		if _, err := targetUpdate.Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if _, err := tx.NewDelete().Model((*models.MomentAccessDecision)(nil)).Where("moment_id = ?", request.TargetMomentID).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		rows := []models.MomentAccessDecision{}
		for personID, decision := range after.Decisions[request.TargetMomentID] {
			rows = append(rows, models.MomentAccessDecision{MomentID: models.UUID(uuid.MustParse(request.TargetMomentID)), AlbumID: models.UUID(uuid.MustParse(albumID)),
				PersonID: models.UUID(uuid.MustParse(personID)), Decision: string(decision), UpdatedAt: time.Now().UTC()})
		}
		if len(rows) > 0 {
			if _, err := tx.NewInsert().Model(&rows).Exec(ctx); err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
		}
		if _, err := tx.NewDelete().Model((*models.Moment)(nil)).Where("id = ? AND album_id = ?", sourceMomentID, albumID).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		return nil
	})
	if err != nil {
		return AlbumDetail{}, transactionError(ctx, err)
	}
	return m.GetAlbum(ctx, albumID)
}
