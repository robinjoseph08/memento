package notifications_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/binder"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeUseCases struct {
	calls []string
}

func (f *fakeUseCases) PreviewUpdates(context.Context) (notifications.Preview, error) {
	f.calls = append(f.calls, "preview")
	return notifications.Preview{Recipients: []notifications.PreviewRecipient{{PersonID: "p1", DisplayName: "Alex", ReviewToken: "token"}}}, nil
}
func (f *fakeUseCases) ApproveUpdates(_ context.Context, request notifications.ApproveRequest) (notifications.Approval, error) {
	f.calls = append(f.calls, "approve:"+request.Note+":"+request.Recipients[0].PersonID+":"+strings.Join(request.Recipients[0].ExcludedAlbumIDs, ","))
	return notifications.Approval{Recipients: []notifications.RecipientResult{{PersonID: request.Recipients[0].PersonID, Status: notifications.ResultNotified}}}, nil
}
func (f *fakeUseCases) ListNotifications(_ context.Context, personID string) (notifications.NotificationList, error) {
	f.calls = append(f.calls, "list:"+personID)
	return notifications.NotificationList{Notifications: []notifications.Notification{{ID: "n1"}}, Unread: 1}, nil
}
func (f *fakeUseCases) MarkRead(_ context.Context, personID, id string) (notifications.Notification, error) {
	f.calls = append(f.calls, "read:"+personID+":"+id)
	if id == "missing" {
		return notifications.Notification{}, errcodes.NotFound("Notification")
	}
	return notifications.Notification{ID: id}, nil
}
func (f *fakeUseCases) MarkAllRead(_ context.Context, personID string) (notifications.NotificationList, error) {
	f.calls = append(f.calls, "read-all:"+personID)
	return notifications.NotificationList{Notifications: []notifications.Notification{}, Unread: 0}, nil
}

func TestNotificationHTTPRoutesBindAndAuthorize(t *testing.T) {
	t.Parallel()
	const personUUID = "0192a0f0-0000-7000-8000-000000000001"
	for name, scenario := range map[string]struct {
		method, path, body string
		curator            bool
		status             int
		call               string
		contains           string
	}{
		"curator previews":            {http.MethodPost, "/api/curator/notifications/preview", `{}`, true, 200, "preview", `"review_token":"token"`},
		"member cannot preview":       {http.MethodPost, "/api/curator/notifications/preview", `{}`, false, 403, "", "forbidden"},
		"curator approves":            {http.MethodPost, "/api/curator/notifications/approve", `{"note":" Hi ","recipients":[{"person_id":"` + personUUID + `","review_token":"token","excluded_album_ids":["` + personUUID + `"]}]}`, true, 200, "approve:Hi:" + personUUID + ":" + personUUID, `"status":"notified"`},
		"approval needs recipients":   {http.MethodPost, "/api/curator/notifications/approve", `{"note":"","recipients":[]}`, true, 422, "", "Include at least one person."},
		"approval rejects bad person": {http.MethodPost, "/api/curator/notifications/approve", `{"recipients":[{"person_id":"nope","review_token":"token"}]}`, true, 422, "", "Review the updates again before sending."},
		"member lists own":            {http.MethodGet, "/api/notifications", "", false, 200, "list:" + personUUID, `"unread":1`},
		"member marks one read":       {http.MethodPost, "/api/notifications/n1/read", `{}`, false, 200, "read:" + personUUID + ":n1", `"id":"n1"`},
		"missing notification":        {http.MethodPost, "/api/notifications/missing/read", `{}`, false, 404, "read:" + personUUID + ":missing", "not_found"},
		"member marks all read":       {http.MethodPost, "/api/notifications/read-all", `{}`, false, 200, "read-all:" + personUUID, `"unread":0`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := echo.New()
			e.HTTPErrorHandler = errcodes.NewHandler().Handle
			b, err := binder.New()
			require.NoError(t, err)
			e.Binder = b
			module := &fakeUseCases{}
			requirePerson := func(next echo.HandlerFunc) echo.HandlerFunc {
				return func(c *echo.Context) error {
					c.Set("identity.person_id", personUUID)
					return next(c)
				}
			}
			requireCurator := func(next echo.HandlerFunc) echo.HandlerFunc {
				return requirePerson(func(c *echo.Context) error {
					if !scenario.curator {
						return echo.ErrForbidden
					}
					return next(c)
				})
			}
			notifications.RegisterRoutes(e, module, requirePerson, requireCurator)
			var req *http.Request
			if scenario.body == "" {
				req = httptest.NewRequest(scenario.method, scenario.path, nil)
			} else {
				req = httptest.NewRequest(scenario.method, scenario.path, strings.NewReader(scenario.body))
				req.Header.Set("Content-Type", "application/json")
			}
			recorder := httptest.NewRecorder()
			e.ServeHTTP(recorder, req)
			require.Equal(t, scenario.status, recorder.Code, recorder.Body.String())
			assert.Contains(t, strings.ToLower(recorder.Body.String()), strings.ToLower(scenario.contains))
			if scenario.call == "" {
				assert.Empty(t, module.calls)
			} else {
				assert.Equal(t, []string{scenario.call}, module.calls)
			}
		})
	}
}
