package identity

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/uptrace/bun"
)

var (
	ErrRequestResolved = &errcodes.Error{HTTPCode: 409, Code: "request_resolved", Message: "This request was already approved. Manage the Person directly instead."}
	ErrEmailInUse      = &errcodes.Error{HTTPCode: 409, Code: "email_in_use", Message: "This email is already approved for or linked to another Person. Review that Person before approving this request."}
)

const (
	RequestPending  = "pending"
	RequestApproved = "approved"
	RequestDenied   = "denied"
	// RequestJoin asks to join the installation; RequestAlbum asks to view one Album.
	RequestJoin  = "join"
	RequestAlbum = "album"
)

type accessRequestRow struct {
	models.AccessRequest `bun:"embed:"`
	PersonName           string
	AlbumTitle           string
	ResolvedByName       string
}

func selectAccessRequests(db bun.IDB) *bun.SelectQuery {
	return db.NewSelect().Model((*models.AccessRequest)(nil)).Column("request.*").
		ColumnExpr("coalesce(person.display_name, '') AS person_name, coalesce(album.title, '') AS album_title, coalesce(resolver.display_name, '') AS resolved_by_name").
		Join("LEFT JOIN persons AS person ON person.id = request.person_id").
		Join("LEFT JOIN albums AS album ON album.id = request.album_id").
		Join("LEFT JOIN persons AS resolver ON resolver.id = request.resolved_by")
}

func projectAccessRequest(row accessRequestRow) AccessRequest {
	result := AccessRequest{ID: row.ID.String(), Kind: row.Kind, Provider: row.Provider, Email: row.Email, EmailVerified: row.EmailVerified, DisplayName: row.DisplayName,
		PersonName: row.PersonName, AlbumTitle: row.AlbumTitle, Status: row.Status, SignInCount: row.SignInCount,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, ResolvedAt: row.ResolvedAt, ResolvedBy: row.ResolvedByName}
	if row.PersonID != nil {
		result.PersonID = row.PersonID.String()
	}
	if row.AlbumID != nil {
		result.AlbumID = row.AlbumID.String()
	}
	return result
}

// recordAccessRequest keeps one open request per unknown identity. Repeated
// sign-ins refresh it, and a denied request absorbs them silently until a
// Curator reconsiders. Only a brand-new request alerts Curators by email.
func (m *Module) recordAccessRequest(ctx context.Context, tx bun.Tx, claims Claims) error {
	open, err := tx.NewSelect().Model((*models.AccessRequest)(nil)).
		Where("request.provider = ? AND request.subject = ? AND request.status <> 'approved' AND request.kind = 'join'", claims.Provider, claims.Subject).Exists(ctx)
	if err != nil {
		return errorstack.CaptureContext(ctx, err)
	}
	now := m.now().UTC()
	row := models.AccessRequest{ID: models.NewUUIDv7(), Kind: RequestJoin, Provider: claims.Provider, Subject: claims.Subject, Email: claims.Email, EmailVerified: claims.EmailVerified,
		DisplayName: strings.TrimSpace(claims.DisplayName), Status: RequestPending, SignInCount: 1, CreatedAt: now, UpdatedAt: now}
	_, err = tx.NewInsert().Model(&row).
		On("CONFLICT (provider, subject) WHERE status <> 'approved' AND kind = 'join' DO UPDATE").
		Set("email = EXCLUDED.email, display_name = EXCLUDED.display_name, sign_in_count = request.sign_in_count + 1, updated_at = EXCLUDED.updated_at").Exec(ctx)
	if err != nil {
		return errorstack.CaptureContext(ctx, err)
	}
	if open {
		return nil
	}
	return m.alertCurators(ctx, tx, fmt.Sprintf("%s asked to join Memento", claims.Email), fmt.Sprintf(`%s (%s) signed in to Memento with a verified Google account that has no access yet.

Review the request and decide whether to link or create a person:
%s/curator/requests

Nothing is shared until you approve it and grant album access.
`, strings.TrimSpace(claims.DisplayName), claims.Email, strings.TrimRight(m.PublicURL, "/")))
}

// alertCurators emails every active Curator with a selected email once per
// new Access Request. Missing SMTP is not an error: the in-app badge remains.
func (m *Module) alertCurators(ctx context.Context, tx bun.Tx, subject, body string) error {
	if m.Mail == nil {
		return nil
	}
	var recipients []string
	err := tx.NewSelect().TableExpr("persons AS person").ColumnExpr("updates.email").
		Join("JOIN identities AS updates ON updates.id = person.update_identity_id AND updates.unlinked_at IS NULL").
		Where("person.is_curator AND person.deactivated_at IS NULL").OrderExpr("updates.email").Scan(ctx, &recipients)
	if err != nil {
		return errorstack.CaptureContext(ctx, err)
	}
	for _, recipient := range recipients {
		_, err := m.Mail.Enqueue(ctx, tx, notifications.Message{Kind: "access_request", To: recipient, Subject: subject, Body: body})
		if errors.Is(err, notifications.ErrMailUnconfigured) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// sessionIdentity returns the linked identity behind the current browser session.
func (m *Module) sessionIdentity(ctx context.Context, tx bun.Tx, token string) (models.Identity, error) {
	var identity models.Identity
	if len(token) != 43 {
		return identity, ErrUnauthenticated
	}
	hash := sha256.Sum256([]byte(token))
	err := tx.NewSelect().Model(&identity).Join("JOIN sessions AS s ON s.identity_id = identity.id").
		Where("s.token_hash = ?", hash[:]).Where("s.expires_at > ?", m.now().UTC()).Where("identity.unlinked_at IS NULL").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return identity, ErrUnauthenticated
	}
	return identity, errorstack.CaptureContext(ctx, err)
}

// RequestAlbumAccess is the explicit action for an existing Person who reached
// an Album they cannot see. Visiting alone never calls it. One open request per
// Person and Album absorbs repeated clicks.
func (m *Module) RequestAlbumAccess(ctx context.Context, token, albumID string) (AccessRequest, error) {
	var result AccessRequest
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		person, err := m.actor(ctx, tx, token, false)
		if err != nil {
			return err
		}
		identity, err := m.sessionIdentity(ctx, tx, token)
		if err != nil {
			return err
		}
		album, err := uuid.Parse(albumID)
		if err != nil {
			return errcodes.NotFound("Album")
		}
		albumUUID := models.UUID(album)
		exists, err := tx.NewSelect().Model((*models.Album)(nil)).Where("album.id = ?", albumUUID).Exists(ctx)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if !exists {
			return errcodes.NotFound("Album")
		}
		open, err := tx.NewSelect().Model((*models.AccessRequest)(nil)).
			Where("request.kind = 'album' AND request.person_id = ? AND request.album_id = ? AND request.status <> 'approved'", person.ID, albumUUID).Exists(ctx)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		now := m.now().UTC()
		row := models.AccessRequest{ID: models.NewUUIDv7(), Kind: RequestAlbum, Provider: identity.Provider, Subject: identity.Subject, Email: identity.Email, EmailVerified: true,
			DisplayName: person.DisplayName, PersonID: &person.ID, AlbumID: &albumUUID, Status: RequestPending, SignInCount: 1, CreatedAt: now, UpdatedAt: now}
		_, err = tx.NewInsert().Model(&row).
			On("CONFLICT (person_id, album_id) WHERE status <> 'approved' AND kind = 'album' DO UPDATE").
			Set("sign_in_count = request.sign_in_count + 1, updated_at = EXCLUDED.updated_at").Exec(ctx)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		var current accessRequestRow
		err = selectAccessRequests(tx).Where("request.kind = 'album' AND request.person_id = ? AND request.album_id = ? AND request.status <> 'approved'", person.ID, albumUUID).Scan(ctx, &current)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		result = projectAccessRequest(current)
		if open {
			return nil
		}
		return m.alertCurators(ctx, tx, fmt.Sprintf("%s asked to see %s", person.DisplayName, current.AlbumTitle), fmt.Sprintf(`%s asked to see the album "%s", which is not shared with them.

Review the request, then grant access in the album if you agree:
%s/curator/requests
`, person.DisplayName, current.AlbumTitle, strings.TrimRight(m.PublicURL, "/")))
	})
	return result, err
}

// ListAccessRequests shows pending work first, then recent decisions.
func (m *Module) ListAccessRequests(ctx context.Context, token string) ([]AccessRequest, error) {
	result := []AccessRequest{}
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		if _, err := m.actor(ctx, tx, token, true); err != nil {
			return err
		}
		rows := []accessRequestRow{}
		if err := selectAccessRequests(tx).OrderExpr("request.status = 'pending' DESC, request.updated_at DESC, request.id").Scan(ctx, &rows); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		for _, row := range rows {
			result = append(result, projectAccessRequest(row))
		}
		return nil
	})
	return result, err
}

func accessRequestByID(ctx context.Context, tx bun.Tx, id string) (accessRequestRow, error) {
	var row accessRequestRow
	if _, err := uuid.Parse(id); err != nil {
		return row, errcodes.NotFound("Access Request")
	}
	err := selectAccessRequests(tx).Where("request.id = ?", id).For("UPDATE OF request").Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return row, errcodes.NotFound("Access Request")
	}
	return row, errorstack.CaptureContext(ctx, err)
}

// ApproveAccessRequest records the decision and, for an unknown identity,
// links or creates the Person and adds the verified-email Preauthorization.
// It never writes an Access Decision: Album access stays a separate review.
func (m *Module) ApproveAccessRequest(ctx context.Context, token, id string, request ApproveAccessRequestRequest) (AccessRequest, error) {
	var result AccessRequest
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		curator, err := m.actor(ctx, tx, token, true)
		if err != nil {
			return err
		}
		row, err := accessRequestByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if row.Status == RequestApproved {
			result = projectAccessRequest(row)
			return nil
		}
		now := m.now().UTC()
		if row.Kind == RequestJoin {
			person, err := m.admitRequestedIdentity(ctx, tx, row.AccessRequest, request, now)
			if err != nil {
				return err
			}
			row.PersonID = &person.ID
			row.PersonName = person.DisplayName
		}
		row.Status = RequestApproved
		row.ResolvedAt = &now
		row.ResolvedBy = &curator.ID
		row.ResolvedByName = curator.DisplayName
		row.UpdatedAt = now
		if _, err := tx.NewUpdate().Model(&row.AccessRequest).Column("person_id", "status", "resolved_at", "resolved_by", "updated_at").WherePK().Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		result = projectAccessRequest(row)
		return nil
	})
	return result, err
}

// admitRequestedIdentity chooses or creates the Person and approves the exact
// verified email so the next ordinary sign-in links the identity.
func (m *Module) admitRequestedIdentity(ctx context.Context, tx bun.Tx, row models.AccessRequest, request ApproveAccessRequestRequest, now time.Time) (models.Person, error) {
	var person models.Person
	switch {
	case request.PersonID != "" && strings.TrimSpace(request.DisplayName) != "":
		return person, fieldError("person_id", "Choose an existing person or enter a name for a new one, not both.")
	case request.PersonID != "":
		var err error
		person, err = personByID(ctx, tx, request.PersonID)
		if errors.Is(err, errcodes.NotFound("Person")) {
			return person, fieldError("person_id", "Choose an existing person.")
		}
		if err != nil {
			return person, err
		}
		if person.DeactivatedAt != nil {
			return person, fieldError("person_id", "Choose an active person, or restore this one first.")
		}
	default:
		name, err := displayName(request.DisplayName)
		if err != nil {
			return person, err
		}
		person = models.Person{ID: models.NewUUIDv7(), DisplayName: name, CreatedAt: now}
		if _, err := tx.NewInsert().Model(&person).Exec(ctx); err != nil {
			return person, errorstack.CaptureContext(ctx, err)
		}
	}
	linkedElsewhere, err := tx.NewSelect().Model((*models.Identity)(nil)).Where("email = ? AND person_id <> ? AND unlinked_at IS NULL", row.Email, person.ID).Exists(ctx)
	if err != nil {
		return person, errorstack.CaptureContext(ctx, err)
	}
	if linkedElsewhere {
		return person, ErrEmailInUse
	}
	var existing models.Preauthorization
	err = tx.NewSelect().Model(&existing).Where("email = ? AND consumed_at IS NULL AND revoked_at IS NULL", row.Email).Scan(ctx)
	switch {
	case err == nil && existing.PersonID == person.ID:
		return person, nil
	case err == nil:
		return person, ErrEmailInUse
	case !errors.Is(err, sql.ErrNoRows):
		return person, errorstack.CaptureContext(ctx, err)
	}
	approval := models.Preauthorization{ID: models.NewUUIDv7(), PersonID: person.ID, Email: row.Email, CreatedAt: now}
	if _, err := tx.NewInsert().Model(&approval).Exec(ctx); err != nil {
		return person, errorstack.CaptureContext(ctx, err)
	}
	return person, nil
}

// DenyAccessRequest records a reviewable refusal. Repeated matching sign-ins
// keep refreshing the denied request without reopening it.
func (m *Module) DenyAccessRequest(ctx context.Context, token, id string) (AccessRequest, error) {
	return m.resolve(ctx, token, id, RequestDenied)
}

// ReconsiderAccessRequest reopens a denied request for a fresh decision.
func (m *Module) ReconsiderAccessRequest(ctx context.Context, token, id string) (AccessRequest, error) {
	return m.resolve(ctx, token, id, RequestPending)
}

func (m *Module) resolve(ctx context.Context, token, id, status string) (AccessRequest, error) {
	var result AccessRequest
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		curator, err := m.actor(ctx, tx, token, true)
		if err != nil {
			return err
		}
		row, err := accessRequestByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if row.Status == RequestApproved {
			return ErrRequestResolved
		}
		if row.Status != status {
			now := m.now().UTC()
			row.Status = status
			row.UpdatedAt = now
			row.ResolvedAt, row.ResolvedBy, row.ResolvedByName = nil, nil, ""
			if status == RequestDenied {
				row.ResolvedAt, row.ResolvedBy, row.ResolvedByName = &now, &curator.ID, curator.DisplayName
			}
			if _, err := tx.NewUpdate().Model(&row.AccessRequest).Column("status", "resolved_at", "resolved_by", "updated_at").WherePK().Exec(ctx); err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
		}
		result = projectAccessRequest(row)
		return nil
	})
	return result, err
}

// PendingAccessRequests counts requests waiting for a decision, for the
// Curator dashboard. The dashboard route already requires a Curator.
func (m *Module) PendingAccessRequests(ctx context.Context) (int, error) {
	count, err := m.db.NewSelect().Model((*models.AccessRequest)(nil)).Where("request.status = ?", RequestPending).Count(ctx)
	return count, errorstack.CaptureContext(ctx, err)
}
