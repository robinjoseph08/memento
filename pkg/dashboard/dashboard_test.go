package dashboard_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/dashboard"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakes struct {
	pending    int
	albums     []publishing.Album
	chapters   []publishing.ChapterFailure
	deliveries []notifications.DeliveryWork
	preview    notifications.Preview
	err        error
}

func (f *fakes) PendingAccessRequests(context.Context) (int, error) { return f.pending, f.err }
func (f *fakes) ListAlbums(context.Context, string) ([]publishing.Album, error) {
	return f.albums, nil
}
func (f *fakes) ChapterFailures(context.Context) ([]publishing.ChapterFailure, error) {
	return f.chapters, nil
}
func (f *fakes) OpenDeliveries(context.Context) ([]notifications.DeliveryWork, error) {
	return f.deliveries, nil
}
func (f *fakes) PreviewUpdates(context.Context) (notifications.Preview, error) { return f.preview, nil }

func work(delivery notifications.Delivery, kind, recipient, personID, personName, invitationID string) notifications.DeliveryWork {
	return notifications.DeliveryWork{Delivery: delivery, Kind: kind, Recipient: recipient, PersonID: personID, PersonName: personName, InvitationID: invitationID}
}

func TestOverviewSortsWorkIntoAttentionAndReady(t *testing.T) {
	t.Parallel()
	updated := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	f := &fakes{
		pending: 2,
		albums: []publishing.Album{
			{ID: "failed", Title: "Failed", Status: "failed", Message: "Import failed."},
			{ID: "interrupted", Title: "Interrupted", Status: "interrupted", Message: "Import stopped."},
			{ID: "processing", Title: "Processing", Status: "processing"},
			{ID: "unpublished", Title: "Unpublished", Status: "complete"},
			{ID: "ready", Title: "Ready", Status: "complete", Ready: true},
			{ID: "published", Title: "Published", Status: "complete", Published: true},
		},
		chapters: []publishing.ChapterFailure{{AlbumID: "ready", AlbumTitle: "Ready", MomentID: "m1", EntryID: "e1", Title: "Surf lesson", Message: "Chapter extraction failed."}},
		deliveries: []notifications.DeliveryWork{
			work(notifications.Delivery{ID: "d1", Status: "failed", Message: "The mail server replied 550.", Attempts: 1, UpdatedAt: updated}, "update", "alex@example.test", "p1", "Alex", ""),
			work(notifications.Delivery{ID: "d2", Status: "uncertain", Message: "Memento restarted.", Attempts: 1, UpdatedAt: updated}, "invitation", "sam@example.test", "p2", "Sam", "i1"),
			work(notifications.Delivery{ID: "d3", Status: "queued"}, "update", "pat@example.test", "", "", ""),
		},
		preview: notifications.Preview{People: []notifications.PreviewPerson{{PersonID: "p1"}, {PersonID: "p3"}}},
	}
	module := dashboard.New(f, f, f)
	result, err := module.Overview(t.Context())
	require.NoError(t, err)
	assert.Equal(t, dashboard.Dashboard{
		NeedsAttention: dashboard.Attention{
			PendingRequests: 2,
			Imports: []dashboard.AlbumWork{
				{ID: "failed", Title: "Failed", Status: "failed", Message: "Import failed."},
				{ID: "interrupted", Title: "Interrupted", Status: "interrupted", Message: "Import stopped."},
			},
			Deliveries: []dashboard.Delivery{
				{ID: "d1", Kind: "update", Recipient: "alex@example.test", Status: "failed", Message: "The mail server replied 550.", Attempts: 1, UpdatedAt: updated, PersonID: "p1", PersonName: "Alex"},
				{ID: "d2", Kind: "invitation", Recipient: "sam@example.test", Status: "uncertain", Message: "Memento restarted.", Attempts: 1, UpdatedAt: updated, PersonID: "p2", PersonName: "Sam", InvitationID: "i1"},
			},
			Chapters: []dashboard.ChapterWork{{AlbumID: "ready", AlbumTitle: "Ready", MomentID: "m1", EntryID: "e1", Title: "Surf lesson", Message: "Chapter extraction failed."}},
		},
		Ready: dashboard.Ready{
			Unpublished: []dashboard.AlbumWork{
				{ID: "unpublished", Title: "Unpublished", Status: "complete"},
				{ID: "ready", Title: "Ready", Status: "complete", Ready: true},
			},
			UnannouncedPeople: 2,
		},
		Active: true,
	}, result)

	// Nothing running and nothing waiting is an honest, empty dashboard.
	quiet := &fakes{albums: []publishing.Album{{ID: "published", Status: "complete", Published: true}}, preview: notifications.Preview{People: []notifications.PreviewPerson{}}}
	result, err = dashboard.New(quiet, quiet, quiet).Overview(t.Context())
	require.NoError(t, err)
	assert.False(t, result.Active)
	assert.Empty(t, result.NeedsAttention.Imports)
	assert.Empty(t, result.NeedsAttention.Deliveries)
	assert.Empty(t, result.NeedsAttention.Chapters)
	assert.Zero(t, result.NeedsAttention.PendingRequests)
	assert.Empty(t, result.Ready.Unpublished)
	assert.Zero(t, result.Ready.UnannouncedPeople)

	// Work still running keeps the page polling without needing attention.
	running := &fakes{albums: []publishing.Album{{ID: "queued", Status: "queued"}}}
	result, err = dashboard.New(running, running, running).Overview(t.Context())
	require.NoError(t, err)
	assert.True(t, result.Active)
	assert.Empty(t, result.NeedsAttention.Imports)
	assert.Empty(t, result.Ready.Unpublished, "an import in progress is neither failed nor ready")
}

func TestOverviewRouteRequiresCuratorAndReportsErrors(t *testing.T) {
	t.Parallel()
	for name, scenario := range map[string]struct {
		curator  bool
		err      error
		status   int
		contains string
	}{
		"curator":     {curator: true, status: 200, contains: `"pending_requests":1`},
		"member":      {curator: false, status: 403, contains: "forbidden"},
		"owner error": {curator: true, err: errors.New("private database detail"), status: 500, contains: "internal_server_error"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := echo.New()
			e.HTTPErrorHandler = errcodes.NewHandler().Handle
			requireCurator := func(next echo.HandlerFunc) echo.HandlerFunc {
				return func(c *echo.Context) error {
					if !scenario.curator {
						return echo.ErrForbidden
					}
					return next(c)
				}
			}
			f := &fakes{pending: 1, err: scenario.err, preview: notifications.Preview{}}
			dashboard.RegisterRoutes(e, dashboard.New(f, f, f), requireCurator)
			recorder := httptest.NewRecorder()
			e.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/curator/dashboard", nil))
			require.Equal(t, scenario.status, recorder.Code, recorder.Body.String())
			assert.Contains(t, recorder.Body.String(), scenario.contains)
			assert.NotContains(t, recorder.Body.String(), "private database detail")
		})
	}
}
