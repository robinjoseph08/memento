package identity

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
)

// MobileExchangePath is the one browser-API mutation made without a session
// or a browser Origin, so the server's Origin check lets it through by path.
const MobileExchangePath = "/api/identity/mobile/exchange"

var errInvalidReturn = &errcodes.Error{HTTPCode: 400, Code: "invalid_return", Message: "This sign-in can only return to the Memento app."}

// mobileReturnURL accepts a return link only if it opens the Mobile App: its
// own scheme, or Expo Go's while developing. A fragment is refused because
// the code is appended as a query parameter and must reach the app.
func mobileReturnURL(raw string, development bool) (*url.URL, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Fragment != "" || strings.Contains(raw, "#") {
		return nil, false
	}
	switch parsed.Scheme {
	case "memento":
		return parsed, true
	case "exp":
		return parsed, development
	}
	return nil, false
}

// mobileReturn ends the web sign-in for the Mobile App: the browser sheet is
// sent back to the app with a single-use code for the session it just made.
func (h *Handlers) mobileReturn(c *echo.Context) error {
	returnTo, ok := mobileReturnURL(c.QueryParam("return_to"), h.development)
	if !ok {
		return errInvalidReturn
	}
	// Onboarding happens on the web; the app has no screen for it. The web
	// sends a Person there before coming here, and a direct visit is sent
	// the same way with the return link kept.
	if person, ok := c.Get("identity.person").(Person); ok && person.OnboardingCompletedAt == nil {
		return errorstack.CaptureContext(c.Request().Context(), c.Redirect(http.StatusFound, "/welcome?return_to="+url.QueryEscape(returnTo.String())))
	}
	token, _ := h.credential(c)
	code, err := h.module.IssueMobileCode(c.Request().Context(), token)
	if err != nil {
		return err
	}
	if returnTo.RawQuery != "" {
		returnTo.RawQuery += "&"
	}
	returnTo.RawQuery += "code=" + url.QueryEscape(code)
	return errorstack.CaptureContext(c.Request().Context(), c.Redirect(http.StatusFound, returnTo.String()))
}

func (h *Handlers) mobileExchange(c *echo.Context) error {
	var request ExchangeMobileCodeRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	session, err := h.module.ExchangeMobileCode(c.Request().Context(), request.Code, request.Platform)
	if err != nil {
		return err
	}
	return errorstack.CaptureContext(c.Request().Context(), c.JSON(http.StatusOK, MobileSession{Token: session.Token, Person: session.Person}))
}
