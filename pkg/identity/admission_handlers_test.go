package identity_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type admissionIdentity struct {
	fakeIdentity
	calls []string
	err   error
}

func (m *admissionIdentity) record(name string) { m.calls = append(m.calls, name) }
func (m *admissionIdentity) CompleteOnboarding(_ context.Context, _ string, request identity.UpdateProfileRequest) (identity.Person, error) {
	m.record("onboarding:" + request.DisplayName)
	return identity.Person{DisplayName: request.DisplayName}, m.err
}
func (m *admissionIdentity) SendInvitation(_ context.Context, _ string, personID string, request identity.SendInvitationRequest) (identity.Invitation, error) {
	m.record("invite:" + personID + ":" + request.PreauthorizationID)
	return identity.Invitation{ID: "invitation"}, m.err
}
func (m *admissionIdentity) RetryInvitation(_ context.Context, _ string, personID, invitationID string) (identity.Invitation, error) {
	m.record("retry:" + personID + ":" + invitationID)
	return identity.Invitation{ID: invitationID}, m.err
}
func (m *admissionIdentity) RequestAlbumAccess(_ context.Context, _ string, albumID string) (identity.AccessRequest, error) {
	m.record("request:" + albumID)
	return identity.AccessRequest{AlbumID: albumID}, m.err
}
func (m *admissionIdentity) ListAccessRequests(context.Context, string) ([]identity.AccessRequest, error) {
	m.record("list")
	return []identity.AccessRequest{{ID: "request", Email: "stranger@example.test"}}, m.err
}
func (m *admissionIdentity) ApproveAccessRequest(_ context.Context, _ string, id string, request identity.ApproveAccessRequestRequest) (identity.AccessRequest, error) {
	m.record("approve:" + id + ":" + request.PersonID + ":" + request.DisplayName)
	return identity.AccessRequest{ID: id, Status: "approved"}, m.err
}
func (m *admissionIdentity) DenyAccessRequest(_ context.Context, _ string, id string) (identity.AccessRequest, error) {
	m.record("deny:" + id)
	return identity.AccessRequest{ID: id, Status: "denied"}, m.err
}
func (m *admissionIdentity) ReconsiderAccessRequest(_ context.Context, _ string, id string) (identity.AccessRequest, error) {
	m.record("reconsider:" + id)
	return identity.AccessRequest{ID: id, Status: "pending"}, m.err
}

func TestAdmissionHTTPRoutesBindAndAuthorize(t *testing.T) {
	t.Parallel()
	for name, scenario := range map[string]struct {
		method, path, body string
		curator            bool
		status             int
		call               string
		contains           string
	}{
		"member completes onboarding":       {http.MethodPost, "/api/identity/onboarding", `{"display_name":"Alex","update_email":"alex@example.test","email_updates":true}`, false, 200, "onboarding:Alex", `"display_name":"Alex"`},
		"onboarding validates name":         {http.MethodPost, "/api/identity/onboarding", `{"display_name":" ","update_email":"","email_updates":false}`, false, 422, "", "Enter a display name."},
		"member requests album access":      {http.MethodPost, "/api/albums/album-1/request-access", `{}`, false, 200, "request:album-1", `"album_id":"album-1"`},
		"member cannot list requests":       {http.MethodGet, "/api/access-requests", "", false, 403, "", "access_denied"},
		"curator lists requests":            {http.MethodGet, "/api/access-requests", "", true, 200, "list", "stranger@example.test"},
		"curator approves with new person":  {http.MethodPost, "/api/access-requests/r1/approve", `{"display_name":"New"}`, true, 200, "approve:r1::New", `"status":"approved"`},
		"curator approves with person":      {http.MethodPost, "/api/access-requests/r1/approve", `{"person_id":"0192a0f0-0000-7000-8000-000000000000"}`, true, 200, "approve:r1:0192a0f0-0000-7000-8000-000000000000:", `"approved"`},
		"approve rejects invalid person id": {http.MethodPost, "/api/access-requests/r1/approve", `{"person_id":"nope"}`, true, 422, "", "Choose an existing person."},
		"curator denies":                    {http.MethodPost, "/api/access-requests/r1/deny", `{}`, true, 200, "deny:r1", `"denied"`},
		"curator reconsiders":               {http.MethodPost, "/api/access-requests/r1/reconsider", `{}`, true, 200, "reconsider:r1", `"pending"`},
		"member cannot deny":                {http.MethodPost, "/api/access-requests/r1/deny", `{}`, false, 403, "", "access_denied"},
		"curator sends invitation":          {http.MethodPost, "/api/people/p1/invitations", `{"preauthorization_id":"0192a0f0-0000-7000-8000-000000000000"}`, true, 200, "invite:p1:0192a0f0-0000-7000-8000-000000000000", `"id":"invitation"`},
		"invitation validates approval id":  {http.MethodPost, "/api/people/p1/invitations", `{"preauthorization_id":"nope"}`, true, 422, "", "Choose an unused email approval to invite."},
		"curator retries invitation":        {http.MethodPost, "/api/people/p1/invitations/i1/retry", `{}`, true, 200, "retry:p1:i1", `"id":"i1"`},
		"member cannot invite":              {http.MethodPost, "/api/people/p1/invitations", `{"preauthorization_id":"0192a0f0-0000-7000-8000-000000000000"}`, false, 403, "", "access_denied"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			module := &admissionIdentity{}
			module.claimed = true
			module.session.Person.IsCurator = scenario.curator
			e := identityHTTP(t, config.NewForTest(), &module.fakeIdentity)
			identity.RegisterRoutes(e, config.NewForTest(), module)
			var req *http.Request
			if scenario.body == "" {
				req = httptest.NewRequest(scenario.method, scenario.path, nil)
			} else {
				req = httptest.NewRequest(scenario.method, scenario.path, strings.NewReader(scenario.body))
				req.Header.Set("Content-Type", "application/json")
			}
			req.AddCookie(&http.Cookie{Name: identity.CookieName, Value: "browser-token"})
			recorder := httptest.NewRecorder()
			e.ServeHTTP(recorder, req)
			require.Equal(t, scenario.status, recorder.Code, recorder.Body.String())
			assert.Contains(t, recorder.Body.String(), scenario.contains)
			if scenario.call == "" {
				assert.Empty(t, module.calls)
			} else {
				assert.Equal(t, []string{scenario.call}, module.calls)
			}
		})
	}
}
