package media

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"time"
	"uuid"

	"github.com/labstack/echo/v5"
	echologger "github.com/robinjoseph08/golib/echo/v5/middleware/logger"
	"github.com/robinjoseph08/golib/logger"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
)

// signedLifetime outlasts a long family video with pauses, and is short enough
// that a URL left on a TV stops working by the next day.
const signedLifetime = 6 * time.Hour

const (
	variantPlayback = "playback"
	variantPreview  = "preview"
)

type signedHandlers struct {
	module    *Module
	authorize AuthorizeEntry
	publicURL string
}

func (h *signedHandlers) sign(c *echo.Context) error {
	var request SignRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	ctx := c.Request().Context()
	actorID, _ := c.Get("identity.person_id").(string)
	if actorID == "" {
		return errcodes.NotFound("Media")
	}
	if err := h.authorize(ctx, actorID, "", request.EntryID); err != nil {
		return err
	}
	path, err := h.module.SignedPath(ctx, actorID, request.EntryID, request.Variant)
	if err != nil {
		return err
	}
	echologger.FromEchoContext(c).Debug("minted signed media URL", logger.Data{"person_id": actorID, "entry_id": request.EntryID, "variant": request.Variant})
	return errorstack.CaptureContext(ctx, c.JSON(http.StatusOK, SignedURL{URL: h.publicURL + path}))
}

// serve verifies the token before anything else, so a forged, expired, or
// wrong-variant URL costs no database or Immich work.
func (h *signedHandlers) serve(c *echo.Context) error {
	ctx := c.Request().Context()
	claims, err := h.module.verifySigned(ctx, c.Param("token"))
	if err != nil {
		return err
	}
	if claims.Variant != c.Param("variant") {
		return errcodes.NotFound("Media")
	}
	if err := h.authorize(ctx, claims.PersonID, "", claims.EntryID); err != nil {
		return err
	}
	switch claims.Variant {
	case variantPlayback:
		return streamPlayback(c, h.module, claims.EntryID)
	case variantPreview:
		return serveEntryImage(c, h.module, claims.EntryID, true)
	}
	return errcodes.NotFound("Media")
}

// SignedPath mints the signed route for one Album Entry a Person may already
// view. The entry's current content version rides along unsigned: it only
// selects bytes, exactly as it does on the cookie routes.
func (m *Module) SignedPath(ctx context.Context, personID, entryID, variant string) (string, error) {
	if _, err := uuid.Parse(entryID); err != nil {
		return "", errcodes.NotFound("Media")
	}
	var item struct {
		Kind           string
		ContentVersion string
	}
	err := m.db.NewSelect().TableExpr("album_entries AS entry").ColumnExpr("item.kind, item.content_version").
		Join("JOIN media_items AS item ON item.id = entry.media_item_id").
		Where("entry.id = ?", entryID).Scan(ctx, &item)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errcodes.NotFound("Media")
	}
	if err != nil {
		return "", errorstack.CaptureContext(ctx, err)
	}
	if (variant == variantPlayback) != (item.Kind == "VIDEO") {
		return "", errcodes.NotFound("Media")
	}
	key, err := m.signingKey(ctx)
	if err != nil {
		return "", err
	}
	token := signToken(tokenClaims{PersonID: personID, EntryID: entryID, Variant: variant}, m.now().Add(signedLifetime), key)
	return "/api/media/signed/" + variant + "/" + url.PathEscape(token) + "?v=" + url.QueryEscape(item.ContentVersion), nil
}

func (m *Module) verifySigned(ctx context.Context, token string) (tokenClaims, error) {
	key, err := m.signingKey(ctx)
	if err != nil {
		return tokenClaims{}, err
	}
	claims, err := verifyToken(token, key, m.now())
	if err != nil {
		return tokenClaims{}, errcodes.NotFound("Media")
	}
	return claims, nil
}

// signingKey reads the installation's secret once and keeps it for the life
// of the process, so replacing it in the database takes effect on restart.
func (m *Module) signingKey(ctx context.Context) ([]byte, error) {
	m.keyLock.Lock()
	defer m.keyLock.Unlock()
	if m.key == nil {
		var key []byte
		if err := m.db.NewSelect().Table("installation").Column("signing_key").Scan(ctx, &key); err != nil {
			return nil, errorstack.CaptureContext(ctx, err)
		}
		m.key = key
	}
	return m.key, nil
}

func (m *Module) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}
