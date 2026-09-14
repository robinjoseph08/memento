package notifications

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

const (
	AlbumNew     = "new"
	AlbumUpdated = "updated"

	ResultNotified = "notified"
	ResultSkipped  = "skipped"

	// payloadVersion names the stored JSON shape. Bump it when the shape
	// changes and keep decoding the older versions.
	payloadVersion = 1
)

// payload is the immutable body of one Update Notification.
type payload struct {
	Albums []NotificationAlbum `json:"albums"`
	Note   string              `json:"note"`
}

// unannouncedAlbum is a preview row's Album with the exact Album Entries that
// make it new, kept out of the visible summary.
type unannouncedAlbum struct {
	NotificationAlbum
	EntryIDs []string
}

// notifiablePeople selects the fields eligibility and destinations need.
func notifiablePeople(db bun.IDB, model any) *bun.SelectQuery {
	return db.NewSelect().Model(model).
		Column("person.id", "person.display_name", "person.is_curator", "person.onboarding_completed_at", "person.deactivated_at", "person.email_updates").
		ColumnExpr("coalesce(updates.email, '') AS update_email").
		Join("LEFT JOIN identities AS updates ON updates.id = person.update_identity_id")
}

// ineligibleReason says why a Person cannot receive viewer Update
// Notifications, or is empty. Curators bypass viewer access, so their content
// is never viewer-visible content; someone who has not completed Onboarding
// has no baseline yet and will get one from everything visible at completion.
func ineligibleReason(person models.Person) string {
	switch {
	case person.DeactivatedAt != nil:
		return "This person is deactivated."
	case person.IsCurator:
		return "Curators do not receive update notifications."
	case person.OnboardingCompletedAt == nil:
		return "This person has not signed in yet. Everything visible when they do becomes their starting point."
	}
	return ""
}

// unannounced is current viewer-visible content minus everything already
// announced to the Person, grouped by Album with new or updated status.
func (m *Module) unannounced(ctx context.Context, db bun.IDB, personID string) ([]unannouncedAlbum, error) {
	visible, err := m.content.VisibleEntries(ctx, db, personID)
	if err != nil {
		return nil, err
	}
	if len(visible) == 0 {
		return nil, nil
	}
	announcedEntries := []string{}
	err = db.NewSelect().Model((*models.AnnouncedEntry)(nil)).ColumnExpr("entry_id::text").Where("person_id = ?", personID).Scan(ctx, &announcedEntries)
	if err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	announcedAlbums := []string{}
	err = db.NewSelect().Model((*models.AnnouncedAlbum)(nil)).ColumnExpr("album_id::text").Where("person_id = ?", personID).Scan(ctx, &announcedAlbums)
	if err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	seenEntries := make(map[string]bool, len(announcedEntries))
	for _, id := range announcedEntries {
		seenEntries[id] = true
	}
	seenAlbums := make(map[string]bool, len(announcedAlbums))
	for _, id := range announcedAlbums {
		seenAlbums[id] = true
	}
	byAlbum := map[string]*unannouncedAlbum{}
	for _, entry := range visible {
		if seenEntries[entry.EntryID] {
			continue
		}
		album := byAlbum[entry.AlbumID]
		if album == nil {
			status := AlbumNew
			if seenAlbums[entry.AlbumID] {
				status = AlbumUpdated
			}
			album = &unannouncedAlbum{NotificationAlbum{ID: entry.AlbumID, Title: entry.AlbumTitle, Status: status, VideoTitles: []string{}}, nil}
			byAlbum[entry.AlbumID] = album
		}
		album.EntryIDs = append(album.EntryIDs, entry.EntryID)
		if entry.Kind == "VIDEO" {
			album.VideoCount++
			album.VideoTitles = append(album.VideoTitles, entry.Title)
		} else {
			album.PhotoCount++
		}
	}
	result := make([]unannouncedAlbum, 0, len(byAlbum))
	for _, album := range byAlbum {
		sort.Strings(album.EntryIDs)
		result = append(result, *album)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Title != result[j].Title {
			return strings.ToLower(result[i].Title) < strings.ToLower(result[j].Title)
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

// reviewToken covers the exact content a row announced: which Album Entries,
// under which Albums, with which status. Titles may change without another
// review; membership may not.
func reviewToken(personID string, albums []unannouncedAlbum) (string, error) {
	type reviewedAlbum struct {
		ID       string   `json:"id"`
		Status   string   `json:"status"`
		EntryIDs []string `json:"entry_ids"`
	}
	reviewed := make([]reviewedAlbum, 0, len(albums))
	for _, album := range albums {
		reviewed = append(reviewed, reviewedAlbum{ID: album.ID, Status: album.Status, EntryIDs: album.EntryIDs})
	}
	data, err := json.Marshal(struct {
		PersonID string          `json:"person_id"`
		Albums   []reviewedAlbum `json:"albums"`
	}{personID, reviewed})
	if err != nil {
		return "", errorstack.Capture(err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

// PreviewUpdates lists every eligible Person with unannounced content. It
// changes nothing; approval freezes what each row showed through its token.
func (m *Module) PreviewUpdates(ctx context.Context) (Preview, error) {
	result := Preview{People: []PreviewPerson{}}
	err := m.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var people []models.Person
		err := notifiablePeople(tx, &people).
			Where("person.deactivated_at IS NULL AND NOT person.is_curator AND person.onboarding_completed_at IS NOT NULL").
			OrderExpr("lower(person.display_name), person.id").Scan(ctx)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		for _, person := range people {
			albums, err := m.unannounced(ctx, tx, person.ID.String())
			if err != nil {
				return err
			}
			if len(albums) == 0 {
				continue
			}
			token, err := reviewToken(person.ID.String(), albums)
			if err != nil {
				return err
			}
			row := PreviewPerson{PersonID: person.ID.String(), DisplayName: person.DisplayName, UpdateEmail: person.UpdateEmail,
				EmailUpdates: person.EmailUpdates, EmailEligible: person.EmailUpdates && person.UpdateEmail != "", Albums: make([]NotificationAlbum, 0, len(albums)), ReviewToken: token}
			for _, album := range albums {
				row.Albums = append(row.Albums, album.NotificationAlbum)
			}
			result.People = append(result.People, row)
		}
		return nil
	})
	return result, transactionError(ctx, err)
}

// ApproveUpdates creates one immutable notification per reviewed Person and
// records exactly its Album Entries as announced, all in one transaction. A row
// whose content no longer matches its token is skipped with the reason, never
// reconciled into something the Curator did not review; the Person rows are
// locked so two overlapping approvals cannot both announce the same content.
func (m *Module) ApproveUpdates(ctx context.Context, request ApproveRequest) (Approval, error) {
	result := Approval{People: []PersonResult{}}
	// The binder already checks shape; module callers get the same guard, and
	// the same Person listed twice is an invariant only the module can see.
	if len(request.People) == 0 {
		return result, errcodes.ValidationFields("Check the highlighted fields.", map[string]string{"people": request.ValidationMessage("people", "min")})
	}
	ids := make([]string, 0, len(request.People))
	seen := map[string]bool{}
	for _, row := range request.People {
		if _, err := uuid.Parse(row.PersonID); err != nil || seen[row.PersonID] {
			return result, errcodes.ValidationFields("Check the highlighted fields.", map[string]string{"people": request.ValidationMessage("people", "dive")})
		}
		seen[row.PersonID] = true
		ids = append(ids, row.PersonID)
	}
	note := strings.TrimSpace(request.Note)
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// Lock in ID order so concurrent approvals of overlapping rows queue
		// instead of deadlocking, then decide each row against committed state.
		// Only the lock matters; the selected IDs are not needed.
		_, err := tx.NewSelect().Model((*models.Person)(nil)).Column("person.id").
			Where("person.id IN (?)", bun.List(ids)).OrderExpr("person.id").For("UPDATE").Exec(ctx)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		now := m.now().UTC()
		for _, row := range request.People {
			outcome, err := m.approvePerson(ctx, tx, row, note, now)
			if err != nil {
				return err
			}
			result.People = append(result.People, outcome)
		}
		return nil
	})
	return result, transactionError(ctx, err)
}

func (m *Module) approvePerson(ctx context.Context, tx bun.Tx, row ApprovePerson, note string, now time.Time) (PersonResult, error) {
	outcome := PersonResult{PersonID: row.PersonID, Status: ResultSkipped}
	var person models.Person
	err := notifiablePeople(tx, &person).Where("person.id = ?", row.PersonID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		outcome.Message = "This person no longer exists."
		return outcome, nil
	}
	if err != nil {
		return outcome, errorstack.CaptureContext(ctx, err)
	}
	outcome.DisplayName = person.DisplayName
	if reason := ineligibleReason(person); reason != "" {
		outcome.Message = reason
		return outcome, nil
	}
	albums, err := m.unannounced(ctx, tx, row.PersonID)
	if err != nil {
		return outcome, err
	}
	if len(albums) == 0 {
		outcome.Message = "Nothing new to announce. These updates were already sent."
		return outcome, nil
	}
	token, err := reviewToken(row.PersonID, albums)
	if err != nil {
		return outcome, err
	}
	if token != row.ReviewToken {
		outcome.Message = "Their updates changed since this preview. Review the updates again to send them."
		return outcome, nil
	}
	excluded := map[string]bool{}
	for _, id := range row.ExcludedAlbumIDs {
		excluded[id] = true
	}
	body := payload{Albums: []NotificationAlbum{}, Note: note}
	notification := models.UpdateNotification{ID: models.NewUUIDv7(), PersonID: person.ID, Version: payloadVersion, CreatedAt: now}
	albumRows := []models.AnnouncedAlbum{}
	entryRows := []models.AnnouncedEntry{}
	for _, album := range albums {
		if excluded[album.ID] {
			continue
		}
		albumID, err := uuid.Parse(album.ID)
		if err != nil {
			return outcome, errorstack.Capture(err)
		}
		body.Albums = append(body.Albums, album.NotificationAlbum)
		albumRows = append(albumRows, models.AnnouncedAlbum{PersonID: person.ID, AlbumID: models.UUID(albumID), AnnouncedAt: now})
		for _, id := range album.EntryIDs {
			entryID, err := uuid.Parse(id)
			if err != nil {
				return outcome, errorstack.Capture(err)
			}
			entryRows = append(entryRows, models.AnnouncedEntry{PersonID: person.ID, EntryID: models.UUID(entryID), NotificationID: &notification.ID, AnnouncedAt: now})
		}
		outcome.AlbumCount++
		outcome.PhotoCount += album.PhotoCount
		outcome.VideoCount += album.VideoCount
	}
	if len(body.Albums) == 0 {
		outcome.Message = "Every update for this person was excluded."
		return outcome, nil
	}
	notification.Payload, err = json.Marshal(body)
	if err != nil {
		return outcome, errorstack.Capture(err)
	}
	if _, err := tx.NewInsert().Model(&notification).Exec(ctx); err != nil {
		return outcome, errorstack.CaptureContext(ctx, err)
	}
	// An updated Album already has its association; a new one gets it now.
	if _, err := tx.NewInsert().Model(&albumRows).On("CONFLICT DO NOTHING").Exec(ctx); err != nil {
		return outcome, errorstack.CaptureContext(ctx, err)
	}
	// Entries were subtracted against the locked, committed announcement
	// state, so a conflict here is a real bug rather than a race to absorb.
	if _, err := tx.NewInsert().Model(&entryRows).Exec(ctx); err != nil {
		return outcome, errorstack.CaptureContext(ctx, fmt.Errorf("announce entries for %s: %w", person.ID, err))
	}
	outcome.Status = ResultNotified
	outcome.NotificationID = notification.ID.String()
	return outcome, nil
}

func projectNotification(row models.UpdateNotification) (Notification, error) {
	result := Notification{ID: row.ID.String(), CreatedAt: row.CreatedAt, ReadAt: row.ReadAt, Albums: []NotificationAlbum{}}
	if row.Version != payloadVersion {
		return result, errorstack.Capture(fmt.Errorf("notification %s has unsupported payload version %d", row.ID, row.Version))
	}
	var body payload
	if err := json.Unmarshal(row.Payload, &body); err != nil {
		return result, errorstack.Capture(err)
	}
	result.Note = body.Note
	for _, album := range body.Albums {
		if album.VideoTitles == nil {
			album.VideoTitles = []string{}
		}
		result.Albums = append(result.Albums, album)
	}
	return result, nil
}

func listNotifications(ctx context.Context, db bun.IDB, personID string) (NotificationList, error) {
	result := NotificationList{Notifications: []Notification{}}
	if _, err := uuid.Parse(personID); err != nil {
		return result, errcodes.NotFound("Person")
	}
	var rows []models.UpdateNotification
	err := db.NewSelect().Model(&rows).Where("person_id = ?", personID).OrderExpr("created_at DESC, id DESC").Scan(ctx)
	if err != nil {
		return result, errorstack.CaptureContext(ctx, err)
	}
	for _, row := range rows {
		// A row written by a newer release and left behind by a rollback must
		// not take the whole list down with it.
		if row.Version != payloadVersion {
			continue
		}
		notification, err := projectNotification(row)
		if err != nil {
			return result, err
		}
		if notification.ReadAt == nil {
			result.Unread++
		}
		result.Notifications = append(result.Notifications, notification)
	}
	return result, nil
}

// ListNotifications returns the Person's own notifications, newest first.
func (m *Module) ListNotifications(ctx context.Context, personID string) (NotificationList, error) {
	return listNotifications(ctx, m.db, personID)
}

// MarkRead sets the read time once. Reading is independent from browsing and
// from announcement state, and never changes the stored summary.
func (m *Module) MarkRead(ctx context.Context, personID, notificationID string) (Notification, error) {
	if _, err := uuid.Parse(personID); err != nil {
		return Notification{}, errcodes.NotFound("Notification")
	}
	if _, err := uuid.Parse(notificationID); err != nil {
		return Notification{}, errcodes.NotFound("Notification")
	}
	var row models.UpdateNotification
	err := m.db.NewUpdate().Model(&row).Set("read_at = coalesce(read_at, ?)", m.now().UTC()).
		Where("id = ? AND person_id = ?", notificationID, personID).Returning("*").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return Notification{}, errcodes.NotFound("Notification")
	}
	if err != nil {
		return Notification{}, errorstack.CaptureContext(ctx, err)
	}
	return projectNotification(row)
}

// MarkAllRead is the bulk action; already-read rows keep their original time.
func (m *Module) MarkAllRead(ctx context.Context, personID string) (NotificationList, error) {
	if _, err := uuid.Parse(personID); err != nil {
		return NotificationList{}, errcodes.NotFound("Person")
	}
	_, err := m.db.NewUpdate().Model((*models.UpdateNotification)(nil)).Set("read_at = ?", m.now().UTC()).
		Where("person_id = ? AND read_at IS NULL", personID).Exec(ctx)
	if err != nil {
		return NotificationList{}, errorstack.CaptureContext(ctx, err)
	}
	return listNotifications(ctx, m.db, personID)
}
