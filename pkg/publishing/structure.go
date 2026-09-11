package publishing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"net/http"
	"sort"
	"time"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

type structureMoment struct {
	ID          string
	CaptureDate string
	Title       string
	SortOrder   int64
	CoverID     string
}

type structureState struct {
	Moments      map[string]structureMoment
	EntryMoments map[string]string
	// EntryOrder ranks entries chronologically, the same order the gallery
	// uses, so a Moment that loses its cover gets its earliest item instead.
	EntryOrder  map[string]int
	Decisions   map[string]map[string]Decision
	People      map[string]string
	PersonOrder []string
}

// firstEntry is the chronologically first of the given entries.
func (s structureState) firstEntry(entryIDs map[string]bool) string {
	best, bestRank := "", 0
	for id := range entryIDs {
		if rank := s.EntryOrder[id]; best == "" || rank < bestRank {
			best, bestRank = id, rank
		}
	}
	return best
}

// remainingEntries is the membership of a Moment after the excluded entries
// leave it.
func (s structureState) remainingEntries(momentID string, exclude map[string]bool) map[string]bool {
	result := map[string]bool{}
	for entryID, entryMomentID := range s.EntryMoments {
		if entryMomentID == momentID && !exclude[entryID] {
			result[entryID] = true
		}
	}
	return result
}

func (s structureState) facts() accessFacts {
	return accessFacts{EntryMoments: s.EntryMoments, Decisions: s.Decisions}
}

func (s structureState) clone() structureState {
	result := structureState{
		Moments:      make(map[string]structureMoment, len(s.Moments)),
		EntryMoments: make(map[string]string, len(s.EntryMoments)),
		EntryOrder:   make(map[string]int, len(s.EntryOrder)),
		Decisions:    make(map[string]map[string]Decision, len(s.Decisions)),
		People:       make(map[string]string, len(s.People)),
		PersonOrder:  append([]string(nil), s.PersonOrder...),
	}
	maps.Copy(result.Moments, s.Moments)
	maps.Copy(result.EntryMoments, s.EntryMoments)
	maps.Copy(result.EntryOrder, s.EntryOrder)
	for momentID, decisions := range s.Decisions {
		result.Decisions[momentID] = make(map[string]Decision, len(decisions))
		maps.Copy(result.Decisions[momentID], decisions)
	}
	maps.Copy(result.People, s.People)
	return result
}

func (m *Module) loadStructure(ctx context.Context, db bun.IDB, albumID string, lock bool) (structureState, error) {
	state := structureState{Moments: map[string]structureMoment{}, EntryMoments: map[string]string{}, EntryOrder: map[string]int{}, Decisions: map[string]map[string]Decision{}, People: map[string]string{}}
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
	type entryRow struct {
		ID       models.UUID
		MomentID *models.UUID
	}
	var entries []entryRow
	entryQuery := db.NewSelect().TableExpr("album_entries AS album_entry").ColumnExpr("album_entry.id, album_entry.moment_id").
		Join("JOIN media_items AS item ON item.id = album_entry.media_item_id").
		Where("album_entry.album_id = ? AND album_entry.removed_at IS NULL", albumID).
		OrderExpr("item.captured_at, item.source_id COLLATE \"C\", album_entry.id")
	if lock {
		entryQuery = entryQuery.For("UPDATE OF album_entry")
	}
	if err := entryQuery.Scan(ctx, &entries); err != nil {
		return state, errorstack.CaptureContext(ctx, err)
	}
	for rank, entry := range entries {
		if entry.MomentID != nil {
			state.EntryMoments[entry.ID.String()] = entry.MomentID.String()
			state.EntryOrder[entry.ID.String()] = rank
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

// reviewedFacts is the part of the structure whose change would alter the
// reviewed effect: membership, decisions, and covers. Person names and other
// Moments' titles may change between preview and commit without a new review.
type reviewedFacts struct {
	Covers       map[string]string              `json:"covers"`
	EntryMoments map[string]string              `json:"entry_moments"`
	Decisions    map[string]map[string]Decision `json:"decisions"`
}

func (s structureState) reviewed() reviewedFacts {
	covers := make(map[string]string, len(s.Moments))
	for id, moment := range s.Moments {
		covers[id] = moment.CoverID
	}
	return reviewedFacts{Covers: covers, EntryMoments: s.EntryMoments, Decisions: s.Decisions}
}

func reviewToken(operation string, before structureState, request any, after structureState) (string, error) {
	payload, err := json.Marshal(struct {
		Operation string        `json:"operation"`
		Before    reviewedFacts `json:"before"`
		Request   any           `json:"request"`
		After     reviewedFacts `json:"after"`
	}{Operation: operation, Before: before.reviewed(), Request: request, After: after.reviewed()})
	if err != nil {
		return "", errorstack.Capture(err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

// previewMomentID stands in for a split's new Moment so the reviewed effect is
// independent from the UUID generated only when the transaction commits.
const previewMomentID = "new-moment"

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

// previewMove describes a move without committing it. When the cover leaves
// a surviving Moment, its earliest remaining item becomes the cover.
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
		updated.CoverID = state.firstEntry(state.remainingEntries(sourceMomentID, selected))
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
		if cover := after.Moments[sourceMomentID].CoverID; !preview.RemovesMoment && cover != state.Moments[sourceMomentID].CoverID {
			if _, err := tx.NewUpdate().Model((*models.Moment)(nil)).Set("cover_entry_id = ?", cover).Where("id = ?", sourceMomentID).Exec(ctx); err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
		}
		updated, err := tx.NewUpdate().Model((*models.AlbumEntry)(nil)).Set("moment_id = ?", request.DestinationMomentID).
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
	// Covers follow the same rule as a move: each resulting Moment starts
	// with its earliest item unless it keeps the cover it already had.
	after := state.clone()
	if selected[source.CoverID] {
		updated := after.Moments[sourceMomentID]
		updated.CoverID = state.firstEntry(state.remainingEntries(sourceMomentID, selected))
		after.Moments[sourceMomentID] = updated
	}
	maxOrder := int64(0)
	for _, moment := range state.Moments {
		maxOrder = max(maxOrder, moment.SortOrder)
	}
	after.Moments[previewMomentID] = structureMoment{ID: previewMomentID, CaptureDate: source.CaptureDate, Title: title, SortOrder: maxOrder + 1, CoverID: state.firstEntry(selected)}
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
		resulting := after.Moments[previewMomentID]
		newID := models.NewUUIDv7()
		coverID, _ := uuid.Parse(resulting.CoverID)
		row := models.Moment{ID: newID, AlbumID: models.UUID(uuid.MustParse(albumID)), CaptureDate: resulting.CaptureDate, SortOrder: resulting.SortOrder, CoverEntryID: models.UUID(coverID)}
		if resulting.Title != "" {
			row.Title = &resulting.Title
		}
		if _, err := tx.NewInsert().Model(&row).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if cover := after.Moments[sourceMomentID].CoverID; cover != source.CoverID {
			if _, err := tx.NewUpdate().Model((*models.Moment)(nil)).Set("cover_entry_id = ?", cover).Where("id = ?", sourceMomentID).Exec(ctx); err != nil {
				return errorstack.CaptureContext(ctx, err)
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
		rows := make([]models.MomentAccessDecision, 0, len(after.Decisions[previewMomentID]))
		for personID, decision := range after.Decisions[previewMomentID] {
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
	_, sourceOK := state.Moments[sourceMomentID]
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
		if title := after.Moments[request.TargetMomentID].Title; title != state.Moments[request.TargetMomentID].Title {
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
