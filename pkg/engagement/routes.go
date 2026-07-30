package engagement

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
)

const (
	recipientEngagementPolicy = "policy:recipient_self_csrf"
	curatorEngagementPolicy   = "policy:curator"
)

type engagementService interface {
	RecordBrowserEvent(ctx context.Context, actor setup.SessionActor, request BrowserEventRequest) error
	Recipient(ctx context.Context, personID uuid.UUID, cursor string, limit int) (RecipientDetail, error)
	MediaOpeners(ctx context.Context, mediaID uuid.UUID) (MediaOpenersResponse, error)
}

type authorizer interface {
	AuthorizeSession(ctx context.Context, credential, csrfToken string, mutation bool) (setup.SessionActor, error)
	AuthorizeCurator(ctx context.Context, credential, csrfToken string, mutation bool) (setup.CuratorSession, error)
}

// Handler exposes explicit Recipient signals and Curator-only inspection.
type Handler struct {
	service    engagementService
	authorizer authorizer
}

func NewHandler(service engagementService, authorizer authorizer) *Handler {
	return &Handler{service: service, authorizer: authorizer}
}

func (h *Handler) authorizeRecipient(c echo.Context) (setup.SessionActor, error) {
	cookie, err := c.Cookie(setup.CookieName)
	if err != nil || cookie.Value == "" {
		return setup.SessionActor{}, errcodes.Unauthorized("A valid Recipient Session is required.")
	}
	actor, err := h.authorizer.AuthorizeSession(c.Request().Context(), cookie.Value, c.Request().Header.Get(setup.CSRFHeader), true)
	switch {
	case errors.Is(err, setup.ErrUnauthenticated):
		return setup.SessionActor{}, errcodes.Unauthorized("A valid Recipient Session is required.")
	case errors.Is(err, setup.ErrCSRF):
		return setup.SessionActor{}, errcodes.Forbidden("This action requires a valid CSRF token")
	case err != nil:
		return setup.SessionActor{}, err
	default:
		return actor, nil
	}
}

func (h *Handler) authorizeCurator(c echo.Context) error {
	cookie, err := c.Cookie(setup.CookieName)
	if err != nil || cookie.Value == "" {
		return errcodes.Unauthorized("A valid Session is required.")
	}
	_, err = h.authorizer.AuthorizeCurator(c.Request().Context(), cookie.Value, "", false)
	switch {
	case errors.Is(err, setup.ErrUnauthenticated):
		return errcodes.Unauthorized("A valid Session is required.")
	case errors.Is(err, setup.ErrNotCurator):
		return errcodes.NotFound("Page")
	default:
		return err
	}
}

func engagementError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrInvalidCursor):
		return errcodes.ValidationError("Use a valid engagement action, target, cursor, and limit from 1 to 100.")
	case errors.Is(err, ErrNotFound):
		return errcodes.NotFound("Engagement")
	default:
		return err
	}
}

func (h *Handler) Record(c echo.Context) error {
	actor, err := h.authorizeRecipient(c)
	if err != nil {
		return err
	}
	var request BrowserEventRequest
	if err := c.Bind(&request); err != nil {
		return errcodes.ValidationError("Use a valid engagement event document.")
	}
	if mapped := engagementError(h.service.RecordBrowserEvent(c.Request().Context(), actor, request)); mapped != nil {
		return mapped
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) Recipient(c echo.Context) error {
	if err := h.authorizeCurator(c); err != nil {
		return err
	}
	personID, err := uuid.Parse(c.Param("person_id"))
	if err != nil || personID == uuid.Nil {
		return errcodes.NotFound("Recipient")
	}
	limit := 50
	if raw := c.QueryParam("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return errcodes.ValidationError("Use an engagement limit from 1 to 100.")
		}
		limit = parsed
	}
	response, err := h.service.Recipient(c.Request().Context(), personID, c.QueryParam("cursor"), limit)
	if mapped := engagementError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) MediaOpeners(c echo.Context) error {
	if err := h.authorizeCurator(c); err != nil {
		return err
	}
	mediaID, err := uuid.Parse(c.Param("media_id"))
	if err != nil || mediaID == uuid.Nil {
		return errcodes.NotFound("Media")
	}
	response, err := h.service.MediaOpeners(c.Request().Context(), mediaID)
	if mapped := engagementError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, response)
}

func noStore(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return next(c)
	}
}

func RegisterRoutes(e *echo.Echo, handler *Handler) {
	record := e.POST("/api/me/engagement", handler.Record, noStore)
	record.Name = recipientEngagementPolicy
	recipient := e.GET("/api/engagement/recipients/:person_id", handler.Recipient, noStore)
	recipient.Name = curatorEngagementPolicy
	openers := e.GET("/api/engagement/media/:media_id/openers", handler.MediaOpeners, noStore)
	openers.Name = curatorEngagementPolicy
}
