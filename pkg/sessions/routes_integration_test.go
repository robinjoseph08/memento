//go:build integration

package sessions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/binder"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionMutationsRequireSessionBoundCSRFAndJSON(t *testing.T) {
	f := newFixture(t)
	e := echo.New()
	requestBinder, err := binder.New()
	require.NoError(t, err)
	e.Binder = requestBinder
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	RegisterRoutes(e, NewHandler(f.service, f.auth))
	request := func(contentType, csrf string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/sessions/"+f.sessionID.String(), strings.NewReader(`{"label":"Personal laptop"}`))
		req.Header.Set(echo.HeaderContentType, contentType)
		if csrf != "" {
			req.Header.Set(setup.CSRFHeader, csrf)
		}
		req.AddCookie(&http.Cookie{Name: setup.CookieName, Value: f.credential})
		response := httptest.NewRecorder()
		e.ServeHTTP(response, req)
		return response
	}
	assert.Equal(t, http.StatusForbidden, request(echo.MIMEApplicationJSON, "").Code)
	assert.Equal(t, http.StatusUnsupportedMediaType, request(echo.MIMEApplicationForm, f.csrf).Code)
	assert.Equal(t, http.StatusNoContent, request(echo.MIMEApplicationJSON, f.csrf).Code)
	var label string
	require.NoError(t, f.db.NewRaw(`SELECT label FROM sessions WHERE id = ?`, f.sessionID).Scan(context.Background(), &label))
	assert.Equal(t, "Personal laptop", label)
}
