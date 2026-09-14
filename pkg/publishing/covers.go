package publishing

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/media"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

// SaveCoverOrder replaces the Album's Cover Order. Every listed Moment must
// belong to the Album and appear once; Moments left out fall back to capture
// order. Positions are cleared first so the unique place constraint never
// trips while the order is rewritten.
func (m *Module) SaveCoverOrder(ctx context.Context, albumID string, request SaveCoverOrderRequest) (AlbumDetail, error) {
	invalid := errcodes.ValidationFields("Check the highlighted fields.", map[string]string{"moment_ids": "Choose Moments from this Album."})
	seen := map[string]bool{}
	for _, id := range request.MomentIDs {
		if _, err := uuid.Parse(id); err != nil || seen[id] {
			return AlbumDetail{}, invalid
		}
		seen[id] = true
	}
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := albumRow(ctx, tx, albumID, true); err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model((*models.Moment)(nil)).Set("cover_position = NULL").Where("album_id = ?", albumID).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		for index, id := range request.MomentIDs {
			updated, err := tx.NewUpdate().Model((*models.Moment)(nil)).Set("cover_position = ?", index+1).Where("id = ? AND album_id = ?", id, albumID).Exec(ctx)
			if err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
			count, err := updated.RowsAffected()
			if err != nil {
				return errorstack.Capture(err)
			}
			if count != 1 {
				return invalid
			}
		}
		return nil
	})
	if err != nil {
		return AlbumDetail{}, transactionError(ctx, err)
	}
	return m.GetAlbum(ctx, albumID)
}

// ViewingGroups derives who will see which cover from the same saved rules
// the viewer uses. Publication state is ignored: the Curator is preparing
// the Album, and the groups describe the view after publication.
func (m *Module) ViewingGroups(ctx context.Context, albumID string) (ViewingGroups, error) {
	var result ViewingGroups
	err := m.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var err error
		result, err = viewingGroups(ctx, tx, m, albumID)
		return err
	})
	if err != nil {
		return ViewingGroups{}, transactionError(ctx, err)
	}
	return result, nil
}

// viewingGroups reads everything in one snapshot so a structural change
// underway elsewhere cannot produce a group with half-updated Moments.
func viewingGroups(ctx context.Context, db bun.IDB, m *Module, albumID string) (ViewingGroups, error) {
	result := ViewingGroups{Groups: []ViewingGroup{}, Placeholder: []ViewingPerson{}}
	if _, err := albumRow(ctx, db, albumID, false); err != nil {
		return result, err
	}
	state, err := m.loadStructure(ctx, db, albumID, false)
	if err != nil {
		return result, err
	}
	// Moment covers in Album display order, with the Media Item state that
	// keeps a cover out of the running.
	type coverRow struct {
		MomentID  string
		EntryID   string
		Available bool
	}
	var covers []coverRow
	err = db.NewSelect().TableExpr("moments AS moment").
		ColumnExpr("moment.id AS moment_id, entry.id AS entry_id, NOT item.offline AND NOT item.trashed AS available").
		Join("JOIN album_entries AS entry ON entry.id = moment.cover_entry_id").
		Join("JOIN media_items AS item ON item.id = entry.media_item_id").
		Where("moment.album_id = ?", albumID).
		OrderExpr(momentCaptureOrder).
		Scan(ctx, &covers)
	if err != nil {
		return result, errorstack.CaptureContext(ctx, err)
	}
	people, err := accessPeople(ctx, db)
	if err != nil {
		return result, err
	}
	facts := state.facts()
	groups := map[string]*ViewingGroup{}
	order := []string{}
	for _, person := range people {
		visible := map[string]bool{}
		for _, entryID := range visibleEntries(facts, person.ID.String()) {
			visible[entryID] = true
		}
		momentIDs := []string{}
		for _, cover := range covers {
			if cover.Available && visible[cover.EntryID] {
				momentIDs = append(momentIDs, cover.MomentID)
			}
		}
		avatarURL := ""
		if person.AvatarFaceID != nil {
			avatarURL = media.AvatarURL(person.ID.String(), *person.AvatarFaceID, person.AvatarVersion)
		}
		member := ViewingPerson{PersonID: person.ID.String(), DisplayName: person.DisplayName, AvatarURL: avatarURL}
		if len(momentIDs) == 0 {
			if len(visible) > 0 {
				result.Placeholder = append(result.Placeholder, member)
			}
			continue
		}
		key := strings.Join(momentIDs, ",")
		group, ok := groups[key]
		if !ok {
			group = &ViewingGroup{MomentIDs: momentIDs, People: []ViewingPerson{}}
			groups[key] = group
			order = append(order, key)
		}
		group.People = append(group.People, member)
	}
	// Largest groups first; ties keep the order People were first seen in,
	// which follows display names.
	sort.SliceStable(order, func(i, j int) bool {
		return len(groups[order[i]].People) > len(groups[order[j]].People)
	})
	for _, key := range order {
		result.Groups = append(result.Groups, *groups[key])
	}
	return result, nil
}
