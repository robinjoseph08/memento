package notifications

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

// KindUpdate marks the optional email for an approved Update Notification.
// Unlike transactional kinds it is re-validated and re-rendered when sent.
const KindUpdate = "update"

var ErrInvitationRetry = &errcodes.Error{HTTPCode: 409, Code: "invitation_retry", Message: "Retry this invitation from the person's page."}

// unsubscribeToken returns the Person's private link secret, creating it on
// first use. Every later email carries the same link.
func (m *Module) unsubscribeToken(ctx context.Context, tx bun.Tx, personID models.UUID, now time.Time) (string, error) {
	var row models.UnsubscribeToken
	err := tx.NewSelect().Model(&row).Where("person_id = ?", personID).Scan(ctx)
	if err == nil {
		return row.Token, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", errorstack.CaptureContext(ctx, err)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", errorstack.Capture(err)
	}
	row = models.UnsubscribeToken{PersonID: personID, Token: base64.RawURLEncoding.EncodeToString(secret), CreatedAt: now}
	if _, err := tx.NewInsert().Model(&row).On("CONFLICT (person_id) DO NOTHING").Exec(ctx); err != nil {
		return "", errorstack.CaptureContext(ctx, err)
	}
	// A concurrent send may have created the token first; read the winner.
	if err := tx.NewSelect().Model(&row).Where("person_id = ?", personID).Scan(ctx); err != nil {
		return "", errorstack.CaptureContext(ctx, err)
	}
	return row.Token, nil
}

func plural(count int, singular, plural string) string {
	if count == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", count, plural)
}

func mediaLabel(photos, videos int) string {
	switch {
	case photos > 0 && videos > 0:
		return plural(photos, "photo", "photos") + " and " + plural(videos, "video", "videos")
	case videos > 0:
		return plural(videos, "video", "videos")
	default:
		return plural(photos, "photo", "photos")
	}
}

// renderUpdateEmail writes the plain-text email for an approved summary. It
// lists Albums with their status and counts, never individual photos or
// videos. One Album links straight to it; several link to the Album list.
func renderUpdateEmail(publicURL, personName string, albums []NotificationAlbum, note, unsubscribeToken string) (subject, body string) {
	origin := strings.TrimRight(publicURL, "/")
	var photos, videos int
	for _, album := range albums {
		photos += album.PhotoCount
		videos += album.VideoCount
	}
	switch {
	case len(albums) == 1 && albums[0].Status == AlbumNew:
		subject = fmt.Sprintf("%s was shared with you on Memento", albums[0].Title)
	case len(albums) == 1:
		subject = fmt.Sprintf("New %s in %s on Memento", mediaKind(albums[0].PhotoCount, albums[0].VideoCount), albums[0].Title)
	default:
		subject = fmt.Sprintf("New %s in %s on Memento", mediaKind(photos, videos), plural(len(albums), "album", "albums"))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Hi %s,\n\n", personName)
	if len(albums) == 1 {
		b.WriteString("There is something new for you to see on Memento.\n\n")
	} else {
		fmt.Fprintf(&b, "There is something new for you to see in %s on Memento.\n\n", plural(len(albums), "album", "albums"))
	}
	for _, album := range albums {
		status := "updated"
		if album.Status == AlbumNew {
			status = "new album"
		}
		fmt.Fprintf(&b, "%s (%s)\n%s\n\n", album.Title, status, mediaLabel(album.PhotoCount, album.VideoCount))
	}
	if note != "" {
		fmt.Fprintf(&b, "A note from your Curator:\n%s\n\n", note)
	}
	if len(albums) == 1 {
		fmt.Fprintf(&b, "See them on Memento:\n%s/albums/%s/photos\n\n", origin, albums[0].ID)
	} else {
		fmt.Fprintf(&b, "See them on Memento:\n%s/albums\n\n", origin)
	}
	// The token travels as a query value so access logs, which record the
	// request path, never see it.
	fmt.Fprintf(&b, "You chose to get these emails. To stop them, open this link and confirm:\n%s/unsubscribe?token=%s\n", origin, unsubscribeToken)
	return subject, b.String()
}

func mediaKind(photos, videos int) string {
	switch {
	case photos > 0 && videos > 0:
		return "photos and videos"
	case videos > 0:
		return "videos"
	default:
		return "photos"
	}
}

// prepareUpdate re-validates a queued update email inside the claim
// transaction, before any network activity. It returns a Curator-facing reason
// to skip the email, or updates the record's destination and content to the
// approved entries the Person can still see. Nothing here changes the Update
// Notification or the announcement records.
func (m *Module) prepareUpdate(ctx context.Context, tx bun.Tx, row *models.MailDelivery, now time.Time) (string, error) {
	var notification models.UpdateNotification
	err := tx.NewSelect().Model(&notification).Where("delivery_id = ?", row.ID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return "This update no longer exists.", nil
	}
	if err != nil {
		return "", errorstack.CaptureContext(ctx, err)
	}
	var person models.Person
	err = notifiablePeople(tx, &person).Where("person.id = ?", notification.PersonID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return "This person no longer exists.", nil
	}
	if err != nil {
		return "", errorstack.CaptureContext(ctx, err)
	}
	if reason := ineligibleReason(person); reason != "" {
		return reason, nil
	}
	switch {
	case person.UpdateEmail == "":
		return "This person has no email selected for updates.", nil
	case !person.EmailUpdates:
		return "This person turned off update emails.", nil
	case !emailEligible(person):
		return "This person's email address is not valid.", nil
	}
	var body payload
	if err := json.Unmarshal(notification.Payload, &body); err != nil {
		return "", errorstack.Capture(err)
	}
	approved := []string{}
	err = tx.NewSelect().Model((*models.AnnouncedEntry)(nil)).ColumnExpr("entry_id::text").Where("notification_id = ?", notification.ID).Scan(ctx, &approved)
	if err != nil {
		return "", errorstack.CaptureContext(ctx, err)
	}
	visible, err := m.content.VisibleEntries(ctx, tx, person.ID.String())
	if err != nil {
		return "", err
	}
	albums := stillVisible(body.Albums, approved, visible)
	if len(albums) == 0 {
		return "Nothing in this update is shared with this person any more.", nil
	}
	token, err := m.unsubscribeToken(ctx, tx, person.ID, now)
	if err != nil {
		return "", err
	}
	row.Recipient = person.UpdateEmail
	row.Subject, row.Body = renderUpdateEmail(m.PublicURL, person.DisplayName, albums, body.Note, token)
	return "", nil
}

// stillVisible keeps the approved summary's Albums, titles, and statuses while
// recounting only the approved Album Entries the Person can still see.
// Entries allowed since approval and later Album renames never enter the
// result.
func stillVisible(albums []NotificationAlbum, approved []string, visible []VisibleEntry) []NotificationAlbum {
	approvedSet := make(map[string]bool, len(approved))
	for _, id := range approved {
		approvedSet[id] = true
	}
	counts := map[string]NotificationAlbum{}
	for _, entry := range visible {
		if !approvedSet[entry.EntryID] {
			continue
		}
		album := counts[entry.AlbumID]
		if entry.Kind == "VIDEO" {
			album.VideoCount++
		} else {
			album.PhotoCount++
		}
		counts[entry.AlbumID] = album
	}
	result := []NotificationAlbum{}
	for _, album := range albums {
		kept := counts[album.ID]
		if kept.PhotoCount+kept.VideoCount == 0 {
			continue
		}
		result = append(result, NotificationAlbum{ID: album.ID, Title: album.Title, Status: album.Status, PhotoCount: kept.PhotoCount, VideoCount: kept.VideoCount})
	}
	return result
}

// UnsubscribeStatus describes the link's owner without changing anything, so
// a mail scanner that loads the link cannot unsubscribe anyone.
func (m *Module) UnsubscribeStatus(ctx context.Context, token string) (UnsubscribeStatus, error) {
	var result UnsubscribeStatus
	person, err := m.tokenPerson(ctx, m.db, token)
	if err != nil {
		return result, err
	}
	return UnsubscribeStatus{DisplayName: person.DisplayName, Email: person.UpdateEmail, Subscribed: emailEligible(person)}, nil
}

// Unsubscribe switches off update email only. Update Notifications,
// Invitations, and account-security email are unaffected, and the selected
// destination stays so the Person can opt back in from their profile.
func (m *Module) Unsubscribe(ctx context.Context, token string) (UnsubscribeStatus, error) {
	var result UnsubscribeStatus
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		person, err := m.tokenPerson(ctx, tx, token)
		if err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model((*models.Person)(nil)).Set("email_updates = false").Where("id = ?", person.ID).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		result = UnsubscribeStatus{DisplayName: person.DisplayName, Email: person.UpdateEmail, Subscribed: false}
		return nil
	})
	return result, transactionError(ctx, err)
}

func (m *Module) tokenPerson(ctx context.Context, db bun.IDB, token string) (models.Person, error) {
	var person models.Person
	if len(token) < 32 || len(token) > 64 {
		return person, errcodes.NotFound("Link")
	}
	err := notifiablePeople(db, &person).Join("JOIN unsubscribe_tokens AS unsubscribe ON unsubscribe.person_id = person.id").Where("unsubscribe.token = ?", token).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return person, errcodes.NotFound("Link")
	}
	return person, errorstack.CaptureContext(ctx, err)
}

// RetryDelivery is the Curator's deliberate resend for a failed or uncertain
// update or alert email. Invitations keep their own retry, which also checks
// that the Person may still be invited.
func (m *Module) RetryDelivery(ctx context.Context, deliveryID string) (Delivery, error) {
	var result Delivery
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		row, err := deliveryRow(ctx, tx, deliveryID, true)
		if err != nil {
			return err
		}
		if row.Kind == "invitation" {
			return ErrInvitationRetry
		}
		result, err = m.retry(ctx, tx, row)
		return err
	})
	return result, transactionError(ctx, err)
}

// DeliveryStates loads Curator-facing state for the given IDs, for pages that
// watch a batch settle.
func (m *Module) DeliveryStates(ctx context.Context, ids []string) (map[string]Delivery, error) {
	valid := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, err := uuid.Parse(id); err == nil {
			valid = append(valid, id)
		}
	}
	return m.Deliveries(ctx, m.db, valid)
}

// OpenDeliveries lists every email that has not settled: queued and sending
// work still running, and failed or uncertain work waiting for a Curator.
// Invitations are read here too so one list covers the dashboard; their
// retry stays with Identity.
func (m *Module) OpenDeliveries(ctx context.Context) ([]DeliveryWork, error) {
	type row struct {
		models.MailDelivery `bun:"embed:"`
		PersonID            string
		PersonName          string
		InvitationID        string
	}
	rows := []row{}
	err := m.db.NewSelect().Model((*models.MailDelivery)(nil)).Column("delivery.*").
		ColumnExpr("coalesce(person.id::text, '') AS person_id, coalesce(person.display_name, '') AS person_name, coalesce(invitation.id::text, '') AS invitation_id").
		Join("LEFT JOIN update_notifications AS notification ON notification.delivery_id = delivery.id").
		Join("LEFT JOIN invitations AS invitation ON invitation.delivery_id = delivery.id").
		Join("LEFT JOIN persons AS person ON person.id = coalesce(notification.person_id, invitation.person_id)").
		Where("delivery.status NOT IN (?)", bun.List([]string{StatusDelivered, StatusSkipped})).
		OrderExpr("delivery.updated_at DESC, delivery.id").Scan(ctx, &rows)
	if err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	result := make([]DeliveryWork, 0, len(rows))
	for _, r := range rows {
		result = append(result, DeliveryWork{Delivery: projectDelivery(r.MailDelivery), Kind: r.Kind, Recipient: r.Recipient, PersonID: r.PersonID, PersonName: r.PersonName, InvitationID: r.InvitationID})
	}
	return result, nil
}
