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
	return notifications.Preview{People: []notifications.PreviewPerson{{PersonID: "p1", DisplayName: "Alex", ReviewToken: "token"}}}, nil
}
func (f *fakeUseCases) ApproveUpdates(_ context.Context, request notifications.ApproveRequest) (notifications.Approval, error) {
	f.calls = append(f.calls, "approve:"+request.Note+":"+request.People[0].PersonID+":"+strings.Join(request.People[0].ExcludedAlbumIDs, ","))
	return notifications.Approval{People: []notifications.PersonResult{{PersonID: request.People[0].PersonID, Status: notifications.ResultNotified}}}, nil
}
func (f *fakeUseCases) DismissUpdates(_ context.Context, request notifications.DismissRequest) (notifications.Approval, error) {
	row := request.People[0]
	f.calls = append(f.calls, "dismiss:"+row.PersonID+":"+row.ReviewToken+":"+strings.Join(row.ExcludedAlbumIDs, ","))
	return notifications.Approval{People: []notifications.PersonResult{{PersonID: row.PersonID, Status: notifications.ResultDismissed}}}, nil
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
func (f *fakeUseCases) DeliveryStates(_ context.Context, ids []string) (map[string]notifications.Delivery, error) {
	f.calls = append(f.calls, "deliveries:"+strings.Join(ids, ","))
	return map[string]notifications.Delivery{"d1": {ID: "d1", Status: "delivered"}}, nil
}
func (f *fakeUseCases) RetryDelivery(_ context.Context, id string) (notifications.Delivery, error) {
	f.calls = append(f.calls, "retry:"+id)
	return notifications.Delivery{ID: id, Status: "queued"}, nil
}
func (f *fakeUseCases) UnsubscribeStatus(_ context.Context, token string) (notifications.UnsubscribeStatus, error) {
	f.calls = append(f.calls, "unsubscribe-status:"+token)
	if token == "missing" {
		return notifications.UnsubscribeStatus{}, errcodes.NotFound("Link")
	}
	return notifications.UnsubscribeStatus{DisplayName: "Alex", Email: "alex@example.test", Subscribed: true}, nil
}
func (f *fakeUseCases) Unsubscribe(_ context.Context, token string) (notifications.UnsubscribeStatus, error) {
	f.calls = append(f.calls, "unsubscribe:"+token)
	return notifications.UnsubscribeStatus{DisplayName: "Alex", Email: "alex@example.test", Subscribed: false}, nil
}

func (f *fakeUseCases) CheckMail(context.Context) notifications.MailStatus {
	f.calls = append(f.calls, "check-mail")
	return notifications.MailStatus{Configured: true, Usable: true, Sender: "Memento <memento@example.test>", Message: "The mail server is connected."}
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
		"curator checks email":             {http.MethodGet, "/api/curator/email", "", true, 200, "check-mail", `"usable":true`},
		"member cannot check email":        {http.MethodGet, "/api/curator/email", "", false, 403, "", "forbidden"},
		"curator previews":                 {http.MethodPost, "/api/curator/notifications/preview", `{}`, true, 200, "preview", `"review_token":"token"`},
		"member cannot preview":            {http.MethodPost, "/api/curator/notifications/preview", `{}`, false, 403, "", "forbidden"},
		"curator approves":                 {http.MethodPost, "/api/curator/notifications/approve", `{"note":" Hi ","people":[{"person_id":"` + personUUID + `","review_token":"token","excluded_album_ids":["` + personUUID + `"]}]}`, true, 200, "approve:Hi:" + personUUID + ":" + personUUID, `"status":"notified"`},
		"approval needs people":            {http.MethodPost, "/api/curator/notifications/approve", `{"note":"","people":[]}`, true, 422, "", "Include at least one person."},
		"approval rejects bad person":      {http.MethodPost, "/api/curator/notifications/approve", `{"people":[{"person_id":"nope","review_token":"token"}]}`, true, 422, "", "Review the updates again before continuing."},
		"curator dismisses":                {http.MethodPost, "/api/curator/notifications/dismiss", `{"people":[{"person_id":"` + personUUID + `","review_token":"token","excluded_album_ids":["` + personUUID + `"]}]}`, true, 200, "dismiss:" + personUUID + ":token:" + personUUID, `"status":"dismissed"`},
		"member cannot dismiss":            {http.MethodPost, "/api/curator/notifications/dismiss", `{}`, false, 403, "", "forbidden"},
		"dismissal needs people":           {http.MethodPost, "/api/curator/notifications/dismiss", `{"people":[]}`, true, 422, "", "Include at least one person."},
		"dismissal rejects missing people": {http.MethodPost, "/api/curator/notifications/dismiss", `{}`, true, 422, "", "Include at least one person."},
		"dismissal rejects bad person":     {http.MethodPost, "/api/curator/notifications/dismiss", `{"people":[{"person_id":"nope","review_token":"token"}]}`, true, 422, "", "Review the updates again before continuing."},
		"dismissal needs review token":     {http.MethodPost, "/api/curator/notifications/dismiss", `{"people":[{"person_id":"` + personUUID + `"}]}`, true, 422, "", "Review the updates again before continuing."},
		"dismissal rejects bad exclusion":  {http.MethodPost, "/api/curator/notifications/dismiss", `{"people":[{"person_id":"` + personUUID + `","review_token":"token","excluded_album_ids":["nope"]}]}`, true, 422, "", `"excluded_album_ids[0]"`},
		"dismissal rejects malformed JSON": {http.MethodPost, "/api/curator/notifications/dismiss", `{"people":`, true, 400, "", ""},
		"member lists own":                 {http.MethodGet, "/api/notifications", "", false, 200, "list:" + personUUID, `"unread":1`},
		"member marks one read":            {http.MethodPost, "/api/notifications/n1/read", `{}`, false, 200, "read:" + personUUID + ":n1", `"id":"n1"`},
		"missing notification":             {http.MethodPost, "/api/notifications/missing/read", `{}`, false, 404, "read:" + personUUID + ":missing", "not_found"},
		"member marks all read":            {http.MethodPost, "/api/notifications/read-all", `{}`, false, 200, "read-all:" + personUUID, `"unread":0`},
		"curator watches deliveries":       {http.MethodGet, "/api/curator/notifications/deliveries?id=d1&id=d2", "", true, 200, "deliveries:d1,d2", `"status":"delivered"`},
		"member cannot watch":              {http.MethodGet, "/api/curator/notifications/deliveries?id=d1", "", false, 403, "", "forbidden"},
		"curator retries delivery":         {http.MethodPost, "/api/curator/notifications/deliveries/d1/retry", `{}`, true, 200, "retry:d1", `"status":"queued"`},
		"unsubscribe link reads only":      {http.MethodGet, "/api/unsubscribe?token=token-1", "", false, 200, "unsubscribe-status:token-1", `"subscribed":true`},
		"unknown unsubscribe link":         {http.MethodGet, "/api/unsubscribe?token=missing", "", false, 404, "unsubscribe-status:missing", "not_found"},
		"unsubscribe confirms":             {http.MethodPost, "/api/unsubscribe?token=token-1", `{}`, false, 200, "unsubscribe:token-1", `"subscribed":false`},
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
			notifications.RegisterRoutes(e, module, module, requirePerson, requireCurator)
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
