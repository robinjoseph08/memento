package publishing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
	"golang.org/x/sync/errgroup"
)

// newMomentPrefix keys a Moment the apply would create for one capture day.
const newMomentPrefix = "new:"

// placeholderPrefix keys an Album Entry the apply would create for one source
// asset, so a review can refer to it before it has an ID.
const placeholderPrefix = "source:"

// sourceReadConcurrency bounds parallel Immich reads during a check.
const sourceReadConcurrency = 8

// syncEntry is one of this Album's Album Entries, active or retained, with
// its Media Item's stored source facts.
type syncEntry struct {
	ID         models.UUID
	MomentID   *models.UUID
	RemovedAt  *time.Time
	ExcludedAt *time.Time
	Item       models.MediaItem
}

// syncState is everything local that a check compares and an apply changes.
type syncState struct {
	album     models.Album
	structure structureState
	// entries is every Album Entry by Immich asset ID, including removed and
	// excluded ones, so a returning asset keeps its identity.
	entries     map[string]syncEntry
	entriesByID map[string]syncEntry
	moments     []models.Moment
	labels      map[string]string
	// dates lists, per Moment, the sorted capture days of its active media.
	dates map[string][]string
	// otherAlbums lists, by Immich asset ID, the other Albums currently
	// showing that Media Item.
	otherAlbums map[string][]SyncAlbumRef
}

func (m *Module) loadSyncState(ctx context.Context, db bun.IDB, albumID string, lock bool) (syncState, error) {
	state := syncState{entries: map[string]syncEntry{}, entriesByID: map[string]syncEntry{}, labels: map[string]string{}, dates: map[string][]string{}, otherAlbums: map[string][]SyncAlbumRef{}}
	album, err := albumRow(ctx, db, albumID, lock)
	if err != nil {
		return state, err
	}
	if album.ImportStatus != "complete" {
		return state, &errcodes.Error{HTTPCode: http.StatusConflict, Code: "import_incomplete", Message: "Wait for the import to finish before checking for changes."}
	}
	state.album = album
	state.structure, err = m.loadStructure(ctx, db, albumID, lock)
	if err != nil {
		return state, err
	}
	type entryRow struct {
		EntryID          models.UUID
		MomentID         *models.UUID
		RemovedAt        *time.Time
		ExcludedAt       *time.Time
		models.MediaItem `bun:"embed:"`
	}
	var rows []entryRow
	entryQuery := db.NewSelect().TableExpr("album_entries AS entry").
		ColumnExpr("entry.id AS entry_id, entry.moment_id, entry.removed_at, entry.excluded_at, item.*").
		Join("JOIN media_items AS item ON item.id = entry.media_item_id").
		Where("entry.album_id = ?", albumID).
		OrderExpr("item.captured_at, item.source_id COLLATE \"C\", entry.id")
	if lock {
		entryQuery = entryQuery.For("UPDATE OF entry")
	}
	if err := entryQuery.Scan(ctx, &rows); err != nil {
		return state, errorstack.CaptureContext(ctx, err)
	}
	momentDates := map[models.UUID][]string{}
	for _, row := range rows {
		entry := syncEntry{ID: row.EntryID, MomentID: row.MomentID, RemovedAt: row.RemovedAt, ExcludedAt: row.ExcludedAt, Item: row.MediaItem}
		state.entries[entry.Item.SourceID] = entry
		state.entriesByID[entry.ID.String()] = entry
		if entry.MomentID != nil && entry.RemovedAt == nil {
			momentDates[*entry.MomentID] = append(momentDates[*entry.MomentID], entry.Item.CapturedAt.Format("2006-01-02"))
		}
	}
	for _, dates := range momentDates {
		sort.Strings(dates)
	}
	if err := db.NewSelect().Model(&state.moments).Where("moment.album_id = ?", albumID).Scan(ctx); err != nil {
		return state, errorstack.CaptureContext(ctx, err)
	}
	sortMoments(state.moments, momentDates)
	for id, label := range momentLabels(state.moments, momentDates) {
		state.labels[id.String()] = label
		state.dates[id.String()] = momentDates[id]
	}
	return state, nil
}

// sortMoments orders Moments the way the editor shows them: by the first
// capture day of their media, then Curator order, then ID. Each date list
// must already be sorted.
func sortMoments(moments []models.Moment, momentDates map[models.UUID][]string) {
	sort.Slice(moments, func(i, j int) bool {
		left, right := momentDates[moments[i].ID], momentDates[moments[j].ID]
		if len(left) > 0 && len(right) > 0 && left[0] != right[0] {
			return left[0] < right[0]
		}
		if moments[i].SortOrder != moments[j].SortOrder {
			return moments[i].SortOrder < moments[j].SortOrder
		}
		return moments[i].ID.String() < moments[j].ID.String()
	})
}

// attachOtherAlbums records which other Albums show each listed asset, so
// the review can preview shared-metadata consequences.
func (state *syncState) attachOtherAlbums(ctx context.Context, db bun.IDB, source syncSource) error {
	sourceIDs := make([]string, 0, len(source.members)+len(source.removals))
	for _, member := range source.members {
		sourceIDs = append(sourceIDs, member.item.SourceID)
	}
	for sourceID := range source.removals {
		sourceIDs = append(sourceIDs, sourceID)
	}
	state.otherAlbums = map[string][]SyncAlbumRef{}
	if len(sourceIDs) == 0 {
		return nil
	}
	type otherRow struct {
		SourceID string
		AlbumID  models.UUID
		Title    string
	}
	var others []otherRow
	err := db.NewSelect().TableExpr("album_entries AS entry").ColumnExpr("item.source_id, album.id AS album_id, album.title").
		Join("JOIN media_items AS item ON item.id = entry.media_item_id").
		Join("JOIN albums AS album ON album.id = entry.album_id").
		Where("item.source_id IN (?) AND entry.album_id <> ? AND entry.removed_at IS NULL", bun.List(sourceIDs), state.album.ID).
		OrderExpr("lower(album.title), album.id").Scan(ctx, &others)
	if err != nil {
		return errorstack.CaptureContext(ctx, err)
	}
	for _, row := range others {
		state.otherAlbums[row.SourceID] = append(state.otherAlbums[row.SourceID], SyncAlbumRef{ID: row.AlbumID.String(), Title: row.Title})
	}
	return nil
}

// syncMember is one asset the Immich album currently holds, or a trashed
// asset Memento retains as unavailable. Full members carry EXIF from a
// complete read; the rest matched the stored facts at listing level.
type syncMember struct {
	item models.MediaItem
	full bool
}

type syncSource struct {
	album   immich.Album
	members []syncMember
	// removals maps an active asset the album no longer lists to whether
	// Immich deleted it outright.
	removals map[string]bool
}

func sourceMissing() error {
	return &errcodes.Error{HTTPCode: http.StatusConflict, Code: "source_missing", Message: "The Immich album could not be found. Memento kept this Album as it is."}
}

func syncStale() error {
	return &errcodes.Error{HTTPCode: http.StatusConflict, Code: "sync_changed", Message: "Immich or this Album changed after this review. Check for changes again before applying."}
}

// listVerifiedMembers reads the album's membership twice around the album
// record, so a listing shifted by a concurrent Immich edit is retried rather
// than trusted.
func (m *Module) listVerifiedMembers(ctx context.Context, sourceID string) (immich.Album, []immich.Asset, error) {
	album, err := m.source.GetAlbum(ctx, sourceID)
	if immich.IsNotFound(err) {
		return album, nil, sourceMissing()
	}
	if err != nil {
		return album, nil, err
	}
	if album.ID != sourceID {
		return album, nil, sourceChanged()
	}
	listed := []immich.Asset{}
	seen := map[string]bool{}
	for page := 1; page != 0; {
		members, next, err := m.source.ListMembers(ctx, sourceID, page)
		if err != nil {
			return album, nil, err
		}
		if next != 0 && next <= page {
			return album, nil, sourceChanged()
		}
		for _, member := range members {
			if seen[member.ID] {
				return album, nil, sourceChanged()
			}
			seen[member.ID] = true
			listed = append(listed, member)
		}
		page = next
	}
	verified := make(map[string]bool, len(seen))
	for page := 1; page != 0; {
		members, next, err := m.source.ListMembers(ctx, sourceID, page)
		if err != nil {
			return album, nil, err
		}
		if next != 0 && next <= page {
			return album, nil, sourceChanged()
		}
		for _, member := range members {
			if !seen[member.ID] || verified[member.ID] {
				return album, nil, sourceChanged()
			}
			verified[member.ID] = true
		}
		page = next
	}
	after, err := m.source.GetAlbum(ctx, sourceID)
	if err != nil {
		return album, nil, err
	}
	if len(verified) != len(seen) || after.Count != album.Count || after.UpdatedAt != album.UpdatedAt {
		return album, nil, sourceChanged()
	}
	return album, listed, nil
}

// readSource reads the Immich album outside any transaction. Assets whose
// listing facts match the stored Media Item are not re-read; the rest are
// read completely, in parallel. Active assets missing from the listing are
// read once more to tell a trashed asset, which stays as unavailable media,
// from one that left the album or no longer exists.
func (m *Module) readSource(ctx context.Context, state syncState) (syncSource, error) {
	album, listed, err := m.listVerifiedMembers(ctx, state.album.SourceID)
	if err != nil {
		return syncSource{}, err
	}
	source := syncSource{album: album, removals: map[string]bool{}}
	listedSet := make(map[string]bool, len(listed))
	unchanged := []syncMember{}
	reread := []string{}
	for _, asset := range listed {
		listedSet[asset.ID] = true
		candidate, err := importItem(asset)
		if err != nil {
			return syncSource{}, err
		}
		if entry, ok := state.entries[asset.ID]; ok && entry.RemovedAt == nil && factsEqual(entry.Item, candidate, false) {
			unchanged = append(unchanged, syncMember{item: entry.Item})
			continue
		}
		reread = append(reread, asset.ID)
	}
	missing := []string{}
	for sourceID, entry := range state.entries {
		if entry.RemovedAt == nil && !listedSet[sourceID] {
			missing = append(missing, sourceID)
		}
	}
	sort.Strings(missing)
	full := make([]models.MediaItem, len(reread))
	trashed := make([]*models.MediaItem, len(missing))
	deleted := make([]bool, len(missing))
	group, groupContext := errgroup.WithContext(ctx)
	group.SetLimit(sourceReadConcurrency)
	for index, sourceID := range reread {
		group.Go(func() error {
			asset, err := m.source.GetAsset(groupContext, sourceID)
			if err != nil {
				return err
			}
			if asset.ID != sourceID {
				return sourceChanged()
			}
			full[index], err = importItem(asset)
			return err
		})
	}
	for index, sourceID := range missing {
		group.Go(func() error {
			asset, err := m.source.GetAsset(groupContext, sourceID)
			if immich.IsNotFound(err) {
				deleted[index] = true
				return nil
			}
			if err != nil {
				return err
			}
			if asset.ID != sourceID {
				return sourceChanged()
			}
			if !asset.Trashed {
				return nil
			}
			candidate, err := importItem(asset)
			if err != nil {
				return err
			}
			trashed[index] = &candidate
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return syncSource{}, err
	}
	source.members = unchanged
	for _, item := range full {
		source.members = append(source.members, syncMember{item: item, full: true})
	}
	for index, sourceID := range missing {
		if trashed[index] != nil {
			source.members = append(source.members, syncMember{item: *trashed[index], full: true})
			continue
		}
		source.removals[sourceID] = deleted[index]
	}
	sort.SliceStable(source.members, func(i, j int) bool {
		left, right := source.members[i].item, source.members[j].item
		if !left.CapturedAt.Equal(right.CapturedAt) {
			return left.CapturedAt.Before(right.CapturedAt)
		}
		return left.SourceID < right.SourceID
	})
	return source, nil
}

// factsEqual compares stored source facts. EXIF is compared only after a
// complete read because the membership listing omits it.
func factsEqual(stored, candidate models.MediaItem, withEXIF bool) bool {
	stored.ID, candidate.ID = models.UUID{}, models.UUID{}
	stored.VideoTitle, candidate.VideoTitle = nil, nil
	if !withEXIF {
		stored.EXIF, candidate.EXIF = nil, nil
	}
	// PostgreSQL keeps microseconds; compare what a round trip preserves.
	stored.CapturedAt, candidate.CapturedAt = stored.CapturedAt.UTC().Truncate(time.Microsecond), candidate.CapturedAt.UTC().Truncate(time.Microsecond)
	stored.SourceCreatedAt, candidate.SourceCreatedAt = stored.SourceCreatedAt.UTC().Truncate(time.Microsecond), candidate.SourceCreatedAt.UTC().Truncate(time.Microsecond)
	stored.SourceUpdatedAt, candidate.SourceUpdatedAt = stored.SourceUpdatedAt.UTC().Truncate(time.Microsecond), candidate.SourceUpdatedAt.UTC().Truncate(time.Microsecond)
	return reflect.DeepEqual(stored, candidate)
}

// changedFields names what differs. Anything outside the displayed facts,
// including Immich's own updated time that versions the browser URLs, reads
// as details.
func changedFields(stored, candidate models.MediaItem) []string {
	fields := []string{}
	if stored.Checksum != candidate.Checksum {
		fields = append(fields, "checksum")
	}
	if !stored.CapturedAt.Equal(candidate.CapturedAt) {
		fields = append(fields, "capture_time")
	}
	if stored.Offline != candidate.Offline || stored.Trashed != candidate.Trashed {
		fields = append(fields, "availability")
	}
	if stored.Filename != candidate.Filename {
		fields = append(fields, "filename")
	}
	rest, candidateRest := stored, candidate
	rest.Checksum, candidateRest.Checksum = "", ""
	rest.CapturedAt, candidateRest.CapturedAt = time.Time{}, time.Time{}
	rest.Offline, rest.Trashed, candidateRest.Offline, candidateRest.Trashed = false, false, false, false
	rest.Filename, candidateRest.Filename = "", ""
	if !factsEqual(rest, candidateRest, true) {
		fields = append(fields, "details")
	}
	return fields
}

func captureLabel(value time.Time) string {
	return value.Format("2006-01-02T15:04:05.999999999")
}

func entryThumbnailURL(entryID, version string) string {
	return "/api/media/entries/" + entryID + "/thumbnail?v=" + url.QueryEscape(version)
}

// SourceAssetThumbnailURL is the Curator-only preview of an asset that is not
// yet, or no longer, part of any Album.
func SourceAssetThumbnailURL(sourceID string) string {
	return "/api/media/sources/assets/" + url.PathEscape(sourceID) + "/thumbnail"
}

// plannedAddition is one asset joining, rejoining, or staying out of the Album.
type plannedAddition struct {
	member syncMember
	// entryID is the retained Album Entry, or a placeholder for a new one.
	entryID  string
	existing *syncEntry
	momentID string
	exclude  bool
}

// syncPlan is the reviewed effect of one check: the exact rows an apply writes.
type syncPlan struct {
	after          structureState
	additions      []plannedAddition
	removals       []syncEntry
	changes        []syncMember
	newMoments     []string
	removedMoments []string
	covers         map[string]string
	description    bool
}

func placeholderEntryID(sourceID string) string { return placeholderPrefix + sourceID }

// suggestMoment picks the Moment for a capture day: one that already holds
// that day, then one whose span covers it, else a Moment to create.
func suggestMoment(state syncState, date string) string {
	for _, moment := range state.moments {
		if slices.Contains(state.dates[moment.ID.String()], date) {
			return moment.ID.String()
		}
	}
	for _, moment := range state.moments {
		dates := state.dates[moment.ID.String()]
		if len(dates) > 0 && dates[0] <= date && date <= dates[len(dates)-1] {
			return moment.ID.String()
		}
	}
	return newMomentPrefix + date
}

// planRemovals lists the active entries whose assets left Immich and takes
// them out of the after state. Removals come first so a Moment they empty
// can still receive an addition and survive.
func planRemovals(state syncState, source syncSource, review *SyncReview, plan *syncPlan) map[string]bool {
	removed := map[string]bool{}
	sourceIDs := make([]string, 0, len(source.removals))
	for sourceID := range source.removals {
		sourceIDs = append(sourceIDs, sourceID)
	}
	sort.Strings(sourceIDs)
	for _, sourceID := range sourceIDs {
		entry, ok := state.entries[sourceID]
		if !ok || entry.RemovedAt != nil || entry.MomentID == nil {
			continue
		}
		momentID := entry.MomentID.String()
		plan.removals = append(plan.removals, entry)
		removed[entry.ID.String()] = true
		delete(plan.after.EntryMoments, entry.ID.String())
		review.Removals = append(review.Removals, SyncRemoval{EntryID: entry.ID.String(), Filename: entry.Item.Filename, Kind: entry.Item.Kind,
			CapturedAt: captureLabel(entry.Item.CapturedAt), ThumbnailURL: entryThumbnailURL(entry.ID.String(), entry.Item.ContentVersion),
			MomentID: momentID, MomentLabel: state.labels[momentID], Cover: state.structure.Moments[momentID].CoverID == entry.ID.String(), Deleted: source.removals[sourceID]})
	}
	sort.Slice(review.Removals, func(i, j int) bool {
		if review.Removals[i].CapturedAt != review.Removals[j].CapturedAt {
			return review.Removals[i].CapturedAt < review.Removals[j].CapturedAt
		}
		return review.Removals[i].EntryID < review.Removals[j].EntryID
	})
	sort.Slice(plan.removals, func(i, j int) bool { return plan.removals[i].ID.String() < plan.removals[j].ID.String() })
	return removed
}

// planAdditions walks the source members: an active entry whose facts differ
// is a change, anything else is an addition placed by the request or by
// suggestion. It returns the Moment keys the review proposes to create.
func planAdditions(state syncState, source syncSource, request SyncRequest, review *SyncReview, plan *syncPlan) (map[string]bool, error) {
	placements := map[string]SyncPlacement{}
	for _, placement := range request.Placements {
		if _, duplicate := placements[placement.SourceID]; duplicate {
			return nil, structureField("placements", "Choose one destination for each new item.")
		}
		placements[placement.SourceID] = placement
	}
	maxOrder := int64(0)
	for _, moment := range state.structure.Moments {
		maxOrder = max(maxOrder, moment.SortOrder)
	}
	proposed := map[string]bool{}
	after := &plan.after
	for _, member := range source.members {
		entry, exists := state.entries[member.item.SourceID]
		if exists && entry.RemovedAt == nil {
			if factsEqual(entry.Item, member.item, member.full) {
				continue
			}
			if !member.full {
				return nil, syncStale()
			}
			plan.changes = append(plan.changes, member)
			review.Changes = append(review.Changes, SyncChange{EntryID: entry.ID.String(), Filename: entry.Item.Filename, Kind: entry.Item.Kind,
				ThumbnailURL: entryThumbnailURL(entry.ID.String(), entry.Item.ContentVersion), Fields: changedFields(entry.Item, member.item),
				CapturedAt: captureLabel(entry.Item.CapturedAt), NewCapturedAt: captureLabel(member.item.CapturedAt),
				Available: !entry.Item.Offline && !entry.Item.Trashed, NewAvailable: !member.item.Offline && !member.item.Trashed,
				OtherAlbums: orEmpty(state.otherAlbums[member.item.SourceID])})
			continue
		}
		if !member.full {
			return nil, syncStale()
		}
		date := member.item.CapturedAt.Format("2006-01-02")
		addition := SyncAddition{SourceID: member.item.SourceID, Filename: member.item.Filename, Kind: member.item.Kind, CapturedAt: captureLabel(member.item.CapturedAt),
			ThumbnailURL: SourceAssetThumbnailURL(member.item.SourceID), Returning: exists, SuggestedMomentID: suggestMoment(state, date), OtherAlbums: orEmpty(state.otherAlbums[member.item.SourceID])}
		planned := plannedAddition{member: member, entryID: placeholderEntryID(member.item.SourceID)}
		if exists {
			planned.entryID = entry.ID.String()
			copied := entry
			planned.existing = &copied
			addition.PreviouslyExcluded = entry.ExcludedAt != nil
		}
		addition.MomentID, addition.Exclude = addition.SuggestedMomentID, addition.PreviouslyExcluded
		if placement, chosen := placements[member.item.SourceID]; chosen {
			delete(placements, member.item.SourceID)
			addition.Exclude = placement.Exclude
			if !placement.Exclude {
				addition.MomentID = placement.MomentID
				if !validDestination(state, placement.MomentID) {
					return nil, structureField("placements", "Choose a Moment in this Album for each new item.")
				}
			}
		}
		planned.momentID, planned.exclude = addition.MomentID, addition.Exclude
		if strings.HasPrefix(addition.SuggestedMomentID, newMomentPrefix) {
			proposed[addition.SuggestedMomentID] = true
		}
		if !planned.exclude {
			if _, ok := after.Moments[planned.momentID]; !ok {
				proposed[planned.momentID] = true
				plan.newMoments = append(plan.newMoments, planned.momentID)
				after.Moments[planned.momentID] = structureMoment{ID: planned.momentID, CaptureDate: strings.TrimPrefix(planned.momentID, newMomentPrefix), SortOrder: maxOrder + int64(len(plan.newMoments))}
				after.Decisions[planned.momentID] = map[string]Decision{}
			}
			after.EntryMoments[planned.entryID] = planned.momentID
			// A Moment created here takes its first placed item as the cover.
			if moment := after.Moments[planned.momentID]; moment.CoverID == "" {
				moment.CoverID = planned.entryID
				after.Moments[planned.momentID] = moment
			}
		}
		plan.additions = append(plan.additions, planned)
		review.Additions = append(review.Additions, addition)
	}
	if len(placements) > 0 {
		return nil, structureField("placements", "Choose destinations only for the listed new items.")
	}
	return proposed, nil
}

// validDestination accepts an existing Moment or a well-formed new Moment key.
func validDestination(state syncState, momentID string) bool {
	if _, existing := state.structure.Moments[momentID]; existing {
		return true
	}
	if !strings.HasPrefix(momentID, newMomentPrefix) {
		return false
	}
	_, err := time.Parse("2006-01-02", strings.TrimPrefix(momentID, newMomentPrefix))
	return err == nil
}

// planCovers removes Moments left without media and, for every surviving
// Moment whose configured cover is leaving, requires a Curator choice from
// what remains, including media this review places there.
func planCovers(state syncState, request SyncRequest, removed map[string]bool, review *SyncReview, plan *syncPlan) error {
	after := &plan.after
	for momentID := range state.structure.Moments {
		if len(after.remainingEntries(momentID, nil)) == 0 {
			delete(after.Moments, momentID)
			delete(after.Decisions, momentID)
			plan.removedMoments = append(plan.removedMoments, momentID)
		}
	}
	sort.Strings(plan.removedMoments)
	for _, momentID := range plan.removedMoments {
		review.RemovedMoments = append(review.RemovedMoments, SyncMomentOption{ID: momentID, Label: state.labels[momentID]})
	}
	covers := map[string]SyncCover{}
	for _, cover := range request.Covers {
		if _, duplicate := covers[cover.MomentID]; duplicate {
			return structureField("covers", "Choose one cover for each Moment.")
		}
		covers[cover.MomentID] = cover
	}
	for _, moment := range state.moments {
		momentID := moment.ID.String()
		current, survives := after.Moments[momentID]
		if !survives || !removed[current.CoverID] {
			continue
		}
		choice := SyncCoverChoice{MomentID: momentID, MomentLabel: state.labels[momentID], Options: []SyncCoverOption{}}
		remaining := after.remainingEntries(momentID, nil)
		ordered := make([]string, 0, len(remaining))
		for entryID := range remaining {
			ordered = append(ordered, entryID)
		}
		// Existing media first in gallery order, then placed additions.
		sort.Slice(ordered, func(i, j int) bool {
			left, leftExisting := state.structure.EntryOrder[ordered[i]]
			right, rightExisting := state.structure.EntryOrder[ordered[j]]
			if leftExisting != rightExisting {
				return leftExisting
			}
			if leftExisting {
				return left < right
			}
			return ordered[i] < ordered[j]
		})
		valid := map[string]bool{}
		for _, entryID := range ordered {
			valid[entryID] = true
			option := SyncCoverOption{EntryID: entryID}
			if existing, ok := state.entriesByID[entryID]; ok {
				option.Filename, option.ThumbnailURL = existing.Item.Filename, entryThumbnailURL(entryID, existing.Item.ContentVersion)
			}
			for _, planned := range plan.additions {
				if planned.entryID == entryID {
					option.Filename, option.ThumbnailURL = planned.member.item.Filename, SourceAssetThumbnailURL(planned.member.item.SourceID)
				}
			}
			choice.Options = append(choice.Options, option)
		}
		if chosen, ok := covers[momentID]; ok {
			delete(covers, momentID)
			if !valid[chosen.EntryID] {
				return structureField("covers", "Choose a cover from the Moment's remaining media.")
			}
			choice.EntryID = chosen.EntryID
			plan.covers[momentID] = chosen.EntryID
			current.CoverID = chosen.EntryID
			after.Moments[momentID] = current
		} else {
			review.Blockers = append(review.Blockers, "Choose a new cover for "+state.labels[momentID]+".")
		}
		review.CoverChoices = append(review.CoverChoices, choice)
	}
	if len(covers) > 0 {
		return structureField("covers", "Choose covers only for the listed Moments.")
	}
	return nil
}

// buildSyncReview computes the temporary diff and the plan behind it. The
// request's placements and covers are validated against this exact diff.
func buildSyncReview(state syncState, source syncSource, request SyncRequest) (SyncReview, syncPlan, error) {
	review := SyncReview{Additions: []SyncAddition{}, Removals: []SyncRemoval{}, Changes: []SyncChange{}, CoverChoices: []SyncCoverChoice{},
		RemovedMoments: []SyncMomentOption{}, Moments: []SyncMomentOption{}, Audience: []AudienceChange{}, Blockers: []string{}}
	plan := syncPlan{after: state.structure.clone(), covers: map[string]string{}}
	if source.album.Description != state.album.Description {
		review.Description = &SyncDescription{Before: state.album.Description, After: source.album.Description}
		plan.description = true
	}
	removed := planRemovals(state, source, &review, &plan)
	proposed, err := planAdditions(state, source, request, &review, &plan)
	if err != nil {
		return review, plan, err
	}
	if err := planCovers(state, request, removed, &review, &plan); err != nil {
		return review, plan, err
	}
	for _, moment := range state.moments {
		review.Moments = append(review.Moments, SyncMomentOption{ID: moment.ID.String(), Label: state.labels[moment.ID.String()]})
	}
	proposals := make([]string, 0, len(proposed))
	for key := range proposed {
		proposals = append(proposals, key)
	}
	sort.Strings(proposals)
	for _, key := range proposals {
		date := strings.TrimPrefix(key, newMomentPrefix)
		review.Moments = append(review.Moments, SyncMomentOption{ID: key, Label: generatedMomentLabel(date, date), New: true})
	}
	review.Audience = reviewedChanges(state.structure, plan.after)
	review.UpToDate = len(review.Removals) == 0 && len(review.Changes) == 0 && review.Description == nil && !hasEffectiveAddition(plan.additions)
	review.Ready = len(review.Blockers) == 0 && !review.UpToDate
	review.ReviewToken, err = syncReviewToken(state, plan, review)
	return review, plan, err
}

// hasEffectiveAddition ignores an exclusion that already exists, which a
// recheck must not turn into a change.
func hasEffectiveAddition(additions []plannedAddition) bool {
	for _, planned := range additions {
		if !planned.exclude || planned.existing == nil || planned.existing.ExcludedAt == nil {
			return true
		}
	}
	return false
}

func orEmpty(refs []SyncAlbumRef) []SyncAlbumRef {
	if refs == nil {
		return []SyncAlbumRef{}
	}
	return refs
}

// reviewedItem is the source facts a review compared, in a stable shape for
// hashing. It leaves out the row identity and the Curator's video title.
type reviewedItem struct {
	SourceID             string         `json:"source_id"`
	Checksum             string         `json:"checksum"`
	Filename             string         `json:"filename"`
	Kind                 string         `json:"kind"`
	CapturedAt           time.Time      `json:"captured_at"`
	SourceCreatedAt      time.Time      `json:"source_created_at"`
	SourceUpdatedAt      time.Time      `json:"source_updated_at"`
	Offline              bool           `json:"offline"`
	Trashed              bool           `json:"trashed"`
	Width                *int           `json:"width"`
	Height               *int           `json:"height"`
	Duration             *int           `json:"duration"`
	Thumbhash            *string        `json:"thumbhash"`
	LivePhotoVideoID     *string        `json:"live_photo_video_id"`
	SourceStackID        *string        `json:"source_stack_id"`
	SourceStackPrimaryID *string        `json:"source_stack_primary_id"`
	SourceStackCount     *int           `json:"source_stack_count"`
	EXIF                 map[string]any `json:"exif"`
	ContentVersion       string         `json:"content_version"`
}

func reviewedItemOf(item models.MediaItem) reviewedItem {
	return reviewedItem{SourceID: item.SourceID, Checksum: item.Checksum, Filename: item.Filename, Kind: item.Kind, CapturedAt: item.CapturedAt,
		SourceCreatedAt: item.SourceCreatedAt, SourceUpdatedAt: item.SourceUpdatedAt, Offline: item.Offline, Trashed: item.Trashed,
		Width: item.Width, Height: item.Height, Duration: item.Duration, Thumbhash: item.Thumbhash, LivePhotoVideoID: item.LivePhotoVideoID,
		SourceStackID: item.SourceStackID, SourceStackPrimaryID: item.SourceStackPrimaryID, SourceStackCount: item.SourceStackCount,
		EXIF: item.EXIF, ContentVersion: item.ContentVersion}
}

// syncReviewToken covers the diff and every decision about it, so an apply
// after any relevant source or local change is refused rather than committing
// something the Curator did not see.
func syncReviewToken(state syncState, plan syncPlan, review SyncReview) (string, error) {
	type reviewedMember struct {
		Item    reviewedItem `json:"item"`
		EntryID string       `json:"entry_id"`
		Moment  string       `json:"moment"`
		Exclude bool         `json:"exclude"`
	}
	type reviewed struct {
		AlbumID        string            `json:"album_id"`
		Description    *SyncDescription  `json:"description"`
		Additions      []reviewedMember  `json:"additions"`
		Changes        []reviewedItem    `json:"changes"`
		Removals       []string          `json:"removals"`
		RemovedMoments []string          `json:"removed_moments"`
		Covers         map[string]string `json:"covers"`
		Blockers       []string          `json:"blockers"`
		Before         reviewedFacts     `json:"before"`
		After          reviewedFacts     `json:"after"`
	}
	additions := make([]reviewedMember, 0, len(plan.additions))
	for _, planned := range plan.additions {
		additions = append(additions, reviewedMember{Item: reviewedItemOf(planned.member.item), EntryID: planned.entryID, Moment: planned.momentID, Exclude: planned.exclude})
	}
	changes := make([]reviewedItem, 0, len(plan.changes))
	for _, member := range plan.changes {
		changes = append(changes, reviewedItemOf(member.item))
	}
	removals := make([]string, 0, len(plan.removals))
	for _, entry := range plan.removals {
		removals = append(removals, entry.ID.String())
	}
	payload, err := json.Marshal(reviewed{
		AlbumID: state.album.ID.String(), Description: review.Description, Additions: additions, Changes: changes, Removals: removals,
		RemovedMoments: plan.removedMoments, Covers: plan.covers, Blockers: review.Blockers, Before: state.structure.reviewed(), After: plan.after.reviewed(),
	})
	if err != nil {
		return "", errorstack.Capture(err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

// refreshAlbumFaces replaces the cached faces of every active Media Item. A
// failed Immich read leaves the cache alone and is reported, not fatal: it is
// the one thing a check persists.
func (m *Module) refreshAlbumFaces(ctx context.Context, albumID string) (bool, string, error) {
	var items []faceRefreshItem
	if err := m.db.NewSelect().TableExpr("album_entries AS entry").
		ColumnExpr("entry.media_item_id, item.source_id, refresh.refreshed_at").
		Join("JOIN media_items AS item ON item.id = entry.media_item_id").
		Join("LEFT JOIN media_face_refreshes AS refresh ON refresh.media_item_id = entry.media_item_id").
		Where("entry.album_id = ? AND entry.removed_at IS NULL", albumID).
		OrderExpr("item.source_id COLLATE \"C\", entry.id").Scan(ctx, &items); err != nil {
		return false, "", errorstack.CaptureContext(ctx, err)
	}
	if err := m.readFaces(ctx, items); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false, "", err
		}
		return false, "Faces could not be refreshed from Immich. The cached faces remain, and access suggestions may be out of date.", nil
	}
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := albumRow(ctx, tx, albumID, true); err != nil {
			return err
		}
		return storeFaces(ctx, tx, items, time.Now().UTC())
	})
	if err != nil {
		return false, "", transactionError(ctx, err)
	}
	return true, "", nil
}

// CheckSync compares the Album with its Immich album and returns a temporary
// review. The opening check, the one without decisions, also refreshes cached
// faces; otherwise it changes nothing. Cancelling the review discards it and
// a later check recomputes the source state.
func (m *Module) CheckSync(ctx context.Context, albumID string, request SyncRequest) (SyncReview, error) {
	state, err := m.loadSyncState(ctx, m.db, albumID, false)
	if err != nil {
		return SyncReview{}, err
	}
	if err := m.source.CheckImport(ctx); err != nil {
		return SyncReview{}, err
	}
	source, err := m.readSource(ctx, state)
	if err != nil {
		return SyncReview{}, err
	}
	if err := state.attachOtherAlbums(ctx, m.db, source); err != nil {
		return SyncReview{}, err
	}
	refreshed, message := false, ""
	if len(request.Placements) == 0 && len(request.Covers) == 0 {
		if refreshed, message, err = m.refreshAlbumFaces(ctx, albumID); err != nil {
			return SyncReview{}, err
		}
	}
	review, _, err := buildSyncReview(state, source, request)
	if err != nil {
		return SyncReview{}, err
	}
	review.FacesRefreshed, review.FacesMessage = refreshed, message
	return review, nil
}

// readAdditionFaces reads faces for media joining the Album, outside the
// transaction, so it starts with recommendations like imported media. A
// failed read is not fatal: the next check refreshes the cache.
func (m *Module) readAdditionFaces(ctx context.Context, plan syncPlan) (map[string][]immich.Face, error) {
	var items []faceRefreshItem
	for _, planned := range plan.additions {
		if !planned.exclude {
			items = append(items, faceRefreshItem{SourceID: planned.member.item.SourceID})
		}
	}
	faces := make(map[string][]immich.Face, len(items))
	if err := m.readFaces(ctx, items); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return faces, nil
	}
	for _, item := range items {
		faces[item.SourceID] = item.Faces
	}
	return faces, nil
}

// ApplySync recomputes the review inside one transaction and commits the
// approved changes together. Any difference from the reviewed diff, in Immich
// or in this Album, refuses the apply so nothing unreviewed is committed.
func (m *Module) ApplySync(ctx context.Context, albumID string, request SyncRequest) (AlbumDetail, error) {
	if request.ReviewToken == "" {
		return AlbumDetail{}, syncStale()
	}
	state, err := m.loadSyncState(ctx, m.db, albumID, false)
	if err != nil {
		return AlbumDetail{}, err
	}
	if err := m.source.CheckImport(ctx); err != nil {
		return AlbumDetail{}, err
	}
	source, err := m.readSource(ctx, state)
	if err != nil {
		return AlbumDetail{}, err
	}
	if err := state.attachOtherAlbums(ctx, m.db, source); err != nil {
		return AlbumDetail{}, err
	}
	review, plan, err := buildSyncReview(state, source, request)
	if err != nil {
		return AlbumDetail{}, err
	}
	if review.UpToDate {
		return AlbumDetail{}, &errcodes.Error{HTTPCode: http.StatusConflict, Code: "sync_unchanged", Message: "This Album already matches Immich. There is nothing to apply."}
	}
	if !review.Ready {
		return AlbumDetail{}, errcodes.ValidationError(strings.Join(review.Blockers, " "))
	}
	if request.ReviewToken != review.ReviewToken {
		return AlbumDetail{}, syncStale()
	}
	faces, err := m.readAdditionFaces(ctx, plan)
	if err != nil {
		return AlbumDetail{}, err
	}
	err = m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		state, err := m.loadSyncState(ctx, tx, albumID, true)
		if err != nil {
			return err
		}
		if err := state.attachOtherAlbums(ctx, tx, source); err != nil {
			return err
		}
		review, plan, err := buildSyncReview(state, source, request)
		if err != nil {
			return err
		}
		if !review.Ready || request.ReviewToken != review.ReviewToken {
			return syncStale()
		}
		return m.commitSync(ctx, tx, state, source, plan, faces)
	})
	if err != nil {
		return AlbumDetail{}, transactionError(ctx, err)
	}
	return m.GetAlbum(ctx, albumID)
}

// affectedOne confirms a conditional update hit its row; anything else means
// the Album changed under the review.
func affectedOne(ctx context.Context, result interface{ RowsAffected() (int64, error) }) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return errorstack.CaptureContext(ctx, err)
	}
	if changed != 1 {
		return syncStale()
	}
	return nil
}

func (m *Module) commitSync(ctx context.Context, tx bun.Tx, state syncState, source syncSource, plan syncPlan, faces map[string][]immich.Face) error {
	now := time.Now().UTC()
	albumID := state.album.ID
	// Media Items are global. Write them in source-ID order, as imports do, so
	// overlapping syncs and imports never deadlock. Media kept out of every
	// Album gets a row for the retained entry but no extraction.
	type mediaWrite struct {
		item     *models.MediaItem
		chapters bool
	}
	writes := []mediaWrite{}
	for i := range plan.additions {
		plan.additions[i].member.item.ID = models.NewUUIDv7()
		writes = append(writes, mediaWrite{item: &plan.additions[i].member.item, chapters: !plan.additions[i].exclude})
	}
	for i := range plan.changes {
		plan.changes[i].item.ID = models.NewUUIDv7()
		writes = append(writes, mediaWrite{item: &plan.changes[i].item, chapters: true})
	}
	sort.Slice(writes, func(i, j int) bool { return writes[i].item.SourceID < writes[j].item.SourceID })
	for _, write := range writes {
		if err := m.upsertMediaItem(ctx, tx, write.item, write.chapters); err != nil {
			return err
		}
	}
	for _, entry := range plan.removals {
		result, err := tx.NewUpdate().Model((*models.AlbumEntry)(nil)).Set("removed_at = ?", now).Set("moment_id = NULL").
			Where("id = ? AND album_id = ? AND removed_at IS NULL", entry.ID, albumID).Exec(ctx)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if err := affectedOne(ctx, result); err != nil {
			return err
		}
	}
	for _, momentID := range plan.removedMoments {
		if _, err := tx.NewDelete().Model((*models.Moment)(nil)).Where("id = ? AND album_id = ?", momentID, albumID).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
	}
	// New Album Entries get their IDs before the Moments that will use one as
	// a cover; the deferred cover constraint lets the Moment go in first.
	entryIDs := map[string]models.UUID{}
	for _, planned := range plan.additions {
		if planned.existing != nil {
			entryIDs[planned.entryID] = planned.existing.ID
		} else {
			entryIDs[planned.entryID] = models.NewUUIDv7()
		}
	}
	momentIDs := map[string]models.UUID{}
	for _, key := range plan.newMoments {
		moment := plan.after.Moments[key]
		row := models.Moment{ID: models.NewUUIDv7(), AlbumID: albumID, CaptureDate: moment.CaptureDate, SortOrder: moment.SortOrder, CoverEntryID: entryIDs[moment.CoverID]}
		if _, err := tx.NewInsert().Model(&row).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		momentIDs[key] = row.ID
	}
	resolveMoment := func(key string) models.UUID {
		if id, ok := momentIDs[key]; ok {
			return id
		}
		return models.UUID(uuid.MustParse(key))
	}
	joined := []faceRefreshItem{}
	for _, planned := range plan.additions {
		entryID := entryIDs[planned.entryID]
		switch {
		case planned.existing == nil && planned.exclude:
			row := models.AlbumEntry{ID: entryID, AlbumID: albumID, MediaItemID: planned.member.item.ID, RemovedAt: &now, ExcludedAt: &now}
			if _, err := tx.NewInsert().Model(&row).Exec(ctx); err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
		case planned.existing == nil:
			momentID := resolveMoment(planned.momentID)
			row := models.AlbumEntry{ID: entryID, AlbumID: albumID, MediaItemID: planned.member.item.ID, MomentID: &momentID}
			if _, err := tx.NewInsert().Model(&row).Exec(ctx); err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
		case planned.exclude:
			if planned.existing.ExcludedAt != nil {
				continue
			}
			result, err := tx.NewUpdate().Model((*models.AlbumEntry)(nil)).Set("excluded_at = ?", now).
				Where("id = ? AND album_id = ? AND removed_at IS NOT NULL", entryID, albumID).Exec(ctx)
			if err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
			if err := affectedOne(ctx, result); err != nil {
				return err
			}
		default:
			result, err := tx.NewUpdate().Model((*models.AlbumEntry)(nil)).Set("removed_at = NULL").Set("excluded_at = NULL").Set("moment_id = ?", resolveMoment(planned.momentID)).
				Where("id = ? AND album_id = ? AND removed_at IS NOT NULL", entryID, albumID).Exec(ctx)
			if err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
			if err := affectedOne(ctx, result); err != nil {
				return err
			}
		}
		if read, ok := faces[planned.member.item.SourceID]; ok && !planned.exclude {
			joined = append(joined, faceRefreshItem{MediaItemID: planned.member.item.ID, SourceID: planned.member.item.SourceID, Faces: read})
		}
	}
	if err := storeJoinedFaces(ctx, tx, joined, now); err != nil {
		return err
	}
	for momentID, coverKey := range plan.covers {
		coverID, ok := entryIDs[coverKey]
		if !ok {
			coverID = models.UUID(uuid.MustParse(coverKey))
		}
		if _, err := tx.NewUpdate().Model((*models.Moment)(nil)).Set("cover_entry_id = ?", coverID).Where("id = ? AND album_id = ?", momentID, albumID).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
	}
	if plan.description {
		if _, err := tx.NewUpdate().Model((*models.Album)(nil)).Set("description = ?", source.album.Description).Where("id = ?", albumID).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
	}
	return nil
}

// storeJoinedFaces caches faces for media that joined the Album, whether its
// Media Item is new or returned from another Album or an earlier removal.
func storeJoinedFaces(ctx context.Context, tx bun.Tx, items []faceRefreshItem, now time.Time) error {
	if len(items) == 0 {
		return nil
	}
	mediaIDs := make([]models.UUID, 0, len(items))
	for _, item := range items {
		mediaIDs = append(mediaIDs, item.MediaItemID)
	}
	var refreshes []models.MediaFaceRefresh
	if err := tx.NewSelect().Model(&refreshes).Where("media_item_id IN (?)", bun.List(mediaIDs)).Scan(ctx); err != nil {
		return errorstack.CaptureContext(ctx, err)
	}
	refreshedAt := make(map[models.UUID]time.Time, len(refreshes))
	for _, refresh := range refreshes {
		refreshedAt[refresh.MediaItemID] = refresh.RefreshedAt
	}
	for i := range items {
		if at, ok := refreshedAt[items[i].MediaItemID]; ok {
			items[i].RefreshedAt = &at
		}
	}
	return storeFaces(ctx, tx, items, now)
}
