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
	album   string
	people  []publishing.AlbumAccessChoice
	failure error
}

func (f *accessUseCases) SaveAlbumAccess(_ context.Context, album string, r publishing.SaveAlbumAccessRequest) (publishing.AlbumDetail, error) {
	f.album, f.people = album, r.People
	return publishing.AlbumDetail{ID: album, Access: []publishing.AccessPerson{{PersonID: r.People[0].PersonID, Decision: publishing.DecisionAllow, Effective: true, AccessibleCount: 2, Exceptions: 1}}}, f.failure
}

func (f *accessUseCases) PreviewAlbumAccess(_ context.Context, album string, r publishing.SaveAlbumAccessRequest) (publishing.AlbumAccessPreview, error) {
	f.album, f.people = album, r.People
	return publishing.AlbumAccessPreview{Changes: []publishing.AudienceChange{{PersonID: r.People[0].PersonID, DisplayName: "Alex", GainedEntryIDs: []string{"one"}, LostEntryIDs: []string{}}}}, f.failure
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
	for _, path := range []string{"/access/preview", "/access", "/access/remove-all/preview", "/access/remove-all", "/moments/moment/rules", "/entries/entry/rules"} {
		assert.Equal(t, 403, post(path, `{}`).Code, path)
	}
	curator = true
	invalid := post("/access", `{"people":[{"person_id":"bad","allowed":true}]}`)
	assert.Equal(t, 422, invalid.Code)
	assert.Contains(t, invalid.Body.String(), `"fields"`)
	assert.Contains(t, invalid.Body.String(), "Choose active non-Curator Persons.")
	assert.Empty(t, module.album)
	body := `{"people":[{"person_id":"018f0000-0000-7000-8000-000000000001","allowed":true}]}`
	preview := post("/access/preview", body)
	require.Equal(t, 200, preview.Code, preview.Body.String())
	assert.Contains(t, preview.Body.String(), `"gained_entry_ids":["one"]`)
	response := post("/access", body)
	require.Equal(t, 200, response.Code, response.Body.String())
	assert.Equal(t, "album", module.album)
	assert.True(t, module.people[0].Allowed)
	assert.Contains(t, response.Body.String(), `"accessible_count":2`)
	assert.Contains(t, response.Body.String(), `"exceptions":1`)
	assert.NotContains(t, response.Body.String(), `"undo"`)
	module.failure = errcodes.ValidationFields("Check the highlighted fields.", map[string]string{"people": "Choose an active Person."})
	rejected := post("/access", body)
	assert.Equal(t, 422, rejected.Code)
	assert.Contains(t, rejected.Body.String(), "Choose an active Person.")
}
