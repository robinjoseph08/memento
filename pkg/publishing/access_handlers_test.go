package publishing_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/binder"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type accessUseCases struct {
	publishing.AccessUseCases
	album    string
	person   string
	decision publishing.Decision
	failure  error
}

func (f *accessUseCases) SetAlbumAccess(_ context.Context, album string, r publishing.SetAlbumAccessRequest) (publishing.AccessResult, error) {
	f.album, f.person, f.decision = album, r.PersonID, r.Decision
	return publishing.AccessResult{Album: publishing.AlbumDetail{Album: publishing.Album{ID: album}, Access: []publishing.AccessPerson{{PersonID: r.PersonID, Decision: r.Decision, Effective: true, AccessibleCount: 2, ExcludedCount: 1}}}}, f.failure
}

func TestAccessHTTPGuardsAllMutationsAndReturnsStructuredErrors(t *testing.T) {
	t.Parallel()
	module := &accessUseCases{}
	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	b, err := binder.New()
	require.NoError(t, err)
	e.Binder = b
	curator := false
	guard := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if !curator {
				return echo.ErrForbidden
			}
			return next(c)
		}
	}
	publishing.RegisterAccessRoutes(e, module, guard)
	post := func(path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/curator/albums/album"+path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		e.ServeHTTP(response, request)
		return response
	}
	for _, path := range []string{"/access", "/access/undo", "/access/remove-all/preview", "/access/remove-all", "/moments/moment/access", "/moments/moment/access/suggestions", "/moments/moment/access/undo", "/moments/moment/rules", "/entries/entry/access", "/entries/entry/access/undo", "/entries/entry/rules"} {
		assert.Equal(t, 403, post(path, `{}`).Code, path)
	}
	curator = true
	invalid := post("/access", `{"person_id":"bad","decision":"deny"}`)
	assert.Equal(t, 422, invalid.Code)
	assert.Contains(t, invalid.Body.String(), `"fields"`)
	assert.Contains(t, invalid.Body.String(), `"person_id"`)
	assert.Empty(t, module.album)
	body := `{"person_id":"018f0000-0000-7000-8000-000000000001","decision":"allow"}`
	response := post("/access", body)
	require.Equal(t, 200, response.Code, response.Body.String())
	assert.Equal(t, "album", module.album)
	assert.Equal(t, publishing.DecisionAllow, module.decision)
	assert.Contains(t, response.Body.String(), `"accessible_count":2`)
	assert.Contains(t, response.Body.String(), `"excluded_count":1`)
	assert.Contains(t, response.Body.String(), `"undo"`)
	module.failure = errcodes.ValidationFields("Check the highlighted fields.", map[string]string{"person_id": "Choose an active Person."})
	rejected := post("/access", body)
	assert.Equal(t, 422, rejected.Code)
	assert.Contains(t, rejected.Body.String(), "Choose an active Person.")
}
