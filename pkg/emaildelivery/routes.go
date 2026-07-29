package emaildelivery

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
)

const (
	setupOnlyPolicy          = "policy:setup_only"
	tokenInspectPolicy       = "policy:token_inspect"
	preferenceMutationPolicy = "policy:token_exchange"
)

const unsubscribePage = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Memento email preferences</title></head><body><main><h1>Optional email</h1><p>Stop immediate and weekly Memento email. Required identity and security messages will still be sent.</p><form method="post" action=""><input type="hidden" name="List-Unsubscribe" value="One-Click"><button type="submit">Unsubscribe</button></form></main></body></html>`

const unsubscribedPage = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Memento email preferences</title></head><body><main><h1>Optional email stopped</h1><p>You will no longer receive optional Memento email. Required identity and security messages will still be sent.</p></main></body></html>`

type requester interface {
	RequestTest(c echo.Context) error
	GetStatus(c echo.Context) error
	PreferencePage(c echo.Context) error
	Unsubscribe(c echo.Context) error
}

// Handler exposes the setup-only required test-email seam.
type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

func (h *Handler) RequestTest(c echo.Context) error {
	response, err := h.service.RequestTest(c.Request().Context())
	if err != nil {
		switch {
		case errors.Is(err, ErrNotConfigured):
			return errcodes.ServiceUnavailable("SMTP is not configured.")
		case errors.Is(err, ErrSetupComplete):
			return errcodes.Forbidden("Sending a setup test email")
		default:
			return err
		}
	}
	return c.JSON(http.StatusAccepted, response)
}

func (h *Handler) GetStatus(c echo.Context) error {
	response, err := h.service.Status(c.Request().Context(), c.Param("delivery_id"))
	if errors.Is(err, ErrDeliveryAbsent) {
		return errcodes.NotFound("Email delivery")
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) PreferencePage(c echo.Context) error {
	if err := h.service.ValidateUnsubscribe(c.Request().Context(), c.QueryParam("token")); err != nil {
		if errors.Is(err, ErrUnsubscribeToken) {
			return errcodes.NotFound("Email preference")
		}
		return err
	}
	return c.HTML(http.StatusOK, unsubscribePage)
}

func (h *Handler) Unsubscribe(c echo.Context) error {
	if c.FormValue("List-Unsubscribe") != "One-Click" {
		return errcodes.NotFound("Email preference")
	}
	if err := h.service.Unsubscribe(c.Request().Context(), c.QueryParam("token")); err != nil {
		if errors.Is(err, ErrUnsubscribeToken) {
			return errcodes.NotFound("Email preference")
		}
		return err
	}
	return c.HTML(http.StatusOK, unsubscribedPage)
}

func unsubscribeHeaders(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		headers := c.Response().Header()
		headers.Set(echo.HeaderCacheControl, "no-store")
		headers.Set("Referrer-Policy", "no-referrer")
		headers.Set("Content-Security-Policy", "default-src 'none'; form-action 'self'; frame-ancestors 'none'")
		headers.Set("X-Frame-Options", "DENY")
		return next(c)
	}
}

// RegisterRoutes registers setup-only delivery and token-authorized preference routes.
func RegisterRoutes(e *echo.Echo, handler requester) {
	request := e.POST("/api/setup/email/test", handler.RequestTest)
	request.Name = setupOnlyPolicy
	status := e.GET("/api/setup/email/test/:delivery_id", handler.GetStatus)
	status.Name = setupOnlyPolicy
	preferences := e.Group("/api/email/preferences", unsubscribeHeaders)
	page := preferences.GET("/unsubscribe", handler.PreferencePage)
	page.Name = tokenInspectPolicy
	unsubscribe := preferences.POST("/unsubscribe", handler.Unsubscribe)
	unsubscribe.Name = preferenceMutationPolicy
}
