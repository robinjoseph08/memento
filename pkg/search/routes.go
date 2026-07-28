package search

import (
	"context"
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
)

type Authorizer interface {
	AuthorizeSession(ctx context.Context, credential, csrfToken string, mutation bool) (setup.SessionActor, error)
}

const invalidRequestMessage = "Enter search text or choose one complete year, month, date, or date range."

type Handler struct {
	service    *Service
	authorizer Authorizer
	limiter    *searchLimiter
}

func NewHandler(service *Service, authorizer Authorizer) *Handler {
	return &Handler{service: service, authorizer: authorizer, limiter: newSearchLimiter()}
}

func (h *Handler) Search(c echo.Context) error {
	cookie, err := c.Cookie(setup.CookieName)
	if err != nil || cookie.Value == "" {
		return errcodes.Unauthorized("A valid Recipient Session is required.")
	}
	actor, err := h.authorizer.AuthorizeSession(c.Request().Context(), cookie.Value, "", false)
	switch {
	case errors.Is(err, setup.ErrUnauthenticated):
		return errcodes.Unauthorized("A valid Recipient Session is required.")
	case err != nil:
		return err
	}
	var request Request
	if err := c.Bind(&request); err != nil {
		return err
	}
	if _, _, err := parseRequest(request); err != nil {
		return errcodes.ValidationError(invalidRequestMessage)
	}
	if !h.limiter.allow(actor.AccessID) {
		return errcodes.TooManyRequests("Too many searches. Try again later.")
	}
	response, err := h.service.Search(c.Request().Context(), actor, request)
	switch {
	case errors.Is(err, ErrInvalidRequest):
		return errcodes.ValidationError(invalidRequestMessage)
	case errors.Is(err, ErrNotFound):
		return errcodes.NotFound("Search")
	case err != nil:
		return err
	}
	return c.JSON(http.StatusOK, response)
}

func noStore(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "private, no-store")
		return next(c)
	}
}

func RegisterRoutes(e *echo.Echo, handler *Handler) {
	route := e.POST("/api/search", handler.Search, noStore)
	route.Name = "policy:recipient_content"
}
