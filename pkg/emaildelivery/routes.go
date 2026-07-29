package emaildelivery

import (
	"bytes"
	"errors"
	"html/template"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
)

const (
	setupOnlyPolicy          = "policy:setup_only"
	tokenInspectPolicy       = "policy:token_inspect"
	preferenceMutationPolicy = "policy:token_exchange"
)

var preferencePage = template.Must(template.New("email-preferences").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Memento email preferences</title></head><body><main><h1>Optional email</h1><p>These choices never change your Media access or required identity and security email.</p><form method="post" action=""><label>Email frequency <select name="email_preference"><option value="immediate"{{if eq .EmailPreference "immediate"}} selected{{end}}>Immediate</option><option value="weekly"{{if eq .EmailPreference "weekly"}} selected{{end}}>Weekly</option><option value="none"{{if eq .EmailPreference "none"}} selected{{end}}>None</option></select></label><label>Weekly day <select name="weekly_day">{{range .Days}}<option value="{{.}}"{{if eq . $.WeeklyDay}} selected{{end}}>{{.}}</option>{{end}}</select></label><label>Weekly local time <input name="weekly_local_time" type="time" required value="{{.WeeklyLocalTime}}"></label><label>Timezone <input name="weekly_timezone" required value="{{.WeeklyTimezone}}"></label><button type="submit">Save email preferences</button></form><form method="post" action=""><input type="hidden" name="List-Unsubscribe" value="One-Click"><button type="submit">Unsubscribe</button></form></main></body></html>`))

const preferenceSavedPage = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Memento email preferences</title></head><body><main><h1>Email preferences saved</h1><p>This single-use link has been consumed. Required identity and security messages remain available.</p></main></body></html>`

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
	preference, err := h.service.PreferenceToken(c.Request().Context(), c.QueryParam("token"))
	if err != nil {
		if errors.Is(err, ErrUnsubscribeToken) {
			return errcodes.NotFound("Email preference")
		}
		return err
	}
	var page bytes.Buffer
	if err := preferencePage.Execute(&page, struct {
		Preference
		Days []string
	}{Preference: preference, Days: []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}}); err != nil {
		return err
	}
	return c.HTMLBlob(http.StatusOK, page.Bytes())
}

func (h *Handler) Unsubscribe(c echo.Context) error {
	var err error
	if c.FormValue("List-Unsubscribe") == "One-Click" {
		err = h.service.Unsubscribe(c.Request().Context(), c.QueryParam("token"))
	} else {
		err = h.service.UpdatePreferenceToken(c.Request().Context(), c.QueryParam("token"), PreferenceUpdate{
			EmailPreference: c.FormValue("email_preference"),
			WeeklyDay:       c.FormValue("weekly_day"),
			WeeklyLocalTime: c.FormValue("weekly_local_time"),
			WeeklyTimezone:  c.FormValue("weekly_timezone"),
		})
	}
	if err != nil {
		switch {
		case errors.Is(err, ErrUnsubscribeToken):
			return errcodes.NotFound("Email preference")
		case errors.Is(err, ErrNotificationPreference):
			return errcodes.ValidationError("Choose a valid email frequency, weekly day, local time, and timezone.")
		default:
			return err
		}
	}
	return c.HTML(http.StatusOK, preferenceSavedPage)
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
