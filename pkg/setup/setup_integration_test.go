//go:build integration

package setup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/binder"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/emaildelivery"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/smtp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

const integrationSecret = "test-only-security-secret-32-bytes"

type acceptingSender struct{}

func (acceptingSender) Send(context.Context, smtp.Message) error { return nil }

func newSetupService(t *testing.T) (*bun.DB, *Service) {
	t.Helper()
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(context.Background(), db))
	delivery := emaildelivery.New(db, config.SMTPConfig{Enabled: true}, acceptingSender{})
	return db, New(db, delivery, integrationSecret)
}

func newSetupHTTP(t *testing.T, service *Service) *echo.Echo {
	t.Helper()
	e := echo.New()
	requestBinder, err := binder.New()
	require.NoError(t, err)
	e.Binder = requestBinder
	RegisterRoutes(e, NewHandler(service))
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	return e
}

func requestedChallenge(t *testing.T, db *bun.DB, service *Service, name, email string) RequestCodeResponse {
	t.Helper()
	response, err := service.RequestCode(context.Background(), RequestCodeRequest{DisplayName: name, Email: email})
	require.NoError(t, err)
	return response
}

func latestCode(t *testing.T, db *bun.DB) string {
	t.Helper()
	var body string
	require.NoError(t, db.NewRaw(`SELECT body FROM email_deliveries WHERE kind = 'setup_code' ORDER BY id DESC LIMIT 1`).Scan(context.Background(), &body))
	code := regexp.MustCompile(`\b[0-9]{8}\b`).FindString(body)
	require.Len(t, code, 8)
	return code
}

func verifiedChallenge(t *testing.T, db *bun.DB, service *Service, name, email string) VerifyCodeResponse {
	t.Helper()
	challenge := requestedChallenge(t, db, service, name, email)
	response, err := service.VerifyCode(context.Background(), VerifyCodeRequest{ChallengeID: challenge.ChallengeID, Code: latestCode(t, db)})
	require.NoError(t, err)
	return response
}

func completionRequest(token, sessionType string) CompleteRequest {
	return CompleteRequest{
		VerificationToken:        token,
		PrivacyAcknowledged:      true,
		EngagementAcknowledged:   true,
		InterestListAcknowledged: true,
		EmailPreference:          "weekly",
		SessionType:              sessionType,
	}
}

func performJSON(e *echo.Echo, method, path, body string, headers map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	return response
}

func TestCodeRequestRollsBackWhenRequiredEmailIsUnavailable(t *testing.T) {
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(context.Background(), db))
	service := New(db, emaildelivery.New(db, config.SMTPConfig{}, nil), integrationSecret)

	_, err := service.RequestCode(context.Background(), RequestCodeRequest{DisplayName: "Robin", Email: "robin@example.com"})
	require.ErrorIs(t, err, emaildelivery.ErrNotConfigured)
	for _, table := range []string{"email_deliveries", "outbox_events", "login_challenges"} {
		var count int
		require.NoError(t, db.NewRaw(`SELECT count(*) FROM `+table).Scan(context.Background(), &count))
		assert.Zero(t, count, table)
	}
}

func TestCodeRequestAndVerificationCreateNoIdentityOrSession(t *testing.T) {
	db, service := newSetupService(t)
	challenge := requestedChallenge(t, db, service, "Robin Joseph", "Robin@Example.com")
	assert.Equal(t, "code_sent", challenge.Status)
	assert.Len(t, challenge.ChallengeID, 64)

	verified, err := service.VerifyCode(context.Background(), VerifyCodeRequest{ChallengeID: challenge.ChallengeID, Code: latestCode(t, db)})
	require.NoError(t, err)
	assert.Equal(t, "verified", verified.Status)
	assert.Len(t, verified.VerificationToken, 64)

	for _, table := range []string{"people", "person_roles", "recipient_access_generations", "sessions"} {
		var count int
		require.NoError(t, db.NewRaw(`SELECT count(*) FROM `+table).Scan(context.Background(), &count))
		assert.Zero(t, count, table)
	}
	_, err = service.VerifyCode(context.Background(), VerifyCodeRequest{ChallengeID: challenge.ChallengeID, Code: latestCode(t, db)})
	require.ErrorIs(t, err, ErrInvalidCode)
}

func TestCodeExpiryAndFiveAttemptLimit(t *testing.T) {
	t.Run("expired code", func(t *testing.T) {
		db, service := newSetupService(t)
		challenge := requestedChallenge(t, db, service, "Expired Person", "expired@example.com")
		code := latestCode(t, db)
		_, err := db.NewRaw(`UPDATE login_challenges SET expires_at = now() - interval '1 second'`).Exec(context.Background())
		require.NoError(t, err)

		_, err = service.VerifyCode(context.Background(), VerifyCodeRequest{ChallengeID: challenge.ChallengeID, Code: code})
		require.ErrorIs(t, err, ErrInvalidCode)
	})

	t.Run("verified challenge expires before completion", func(t *testing.T) {
		db, service := newSetupService(t)
		verified := verifiedChallenge(t, db, service, "Expired Completion", "expired-completion@example.com")
		_, err := db.NewRaw(`UPDATE login_challenges SET expires_at = now() - interval '1 second'`).Exec(context.Background())
		require.NoError(t, err)

		_, err = service.complete(context.Background(), completionRequest(verified.VerificationToken, "trusted"))
		require.ErrorIs(t, err, ErrInvalidToken)
		var people, sessions int
		require.NoError(t, db.NewRaw(`SELECT count(*) FROM people`).Scan(context.Background(), &people))
		require.NoError(t, db.NewRaw(`SELECT count(*) FROM sessions`).Scan(context.Background(), &sessions))
		assert.Zero(t, people)
		assert.Zero(t, sessions)
	})

	t.Run("attempt limit", func(t *testing.T) {
		db, service := newSetupService(t)
		challenge := requestedChallenge(t, db, service, "Limited Person", "limited@example.com")
		correctCode := latestCode(t, db)
		invalidCode := "00000000"
		if correctCode == invalidCode {
			invalidCode = "11111111"
		}
		for range 5 {
			_, err := service.VerifyCode(context.Background(), VerifyCodeRequest{ChallengeID: challenge.ChallengeID, Code: invalidCode})
			require.ErrorIs(t, err, ErrInvalidCode)
		}
		_, err := service.VerifyCode(context.Background(), VerifyCodeRequest{ChallengeID: challenge.ChallengeID, Code: correctCode})
		require.ErrorIs(t, err, ErrInvalidCode)
		var attempts int
		require.NoError(t, db.NewRaw(`SELECT attempts FROM login_challenges`).Scan(context.Background(), &attempts))
		assert.Equal(t, 5, attempts)
	})
}

func TestFinalSetupStoresCompletedIdentityChoicesAndOpaqueSession(t *testing.T) {
	db, service := newSetupService(t)
	verified := verifiedChallenge(t, db, service, "Robin Joseph", "Robin@Example.com")
	completed, err := service.complete(context.Background(), completionRequest(verified.VerificationToken, "trusted"))
	require.NoError(t, err)
	assert.Len(t, completed.Credential, 64)
	assert.Len(t, completed.CSRFToken, 64)

	var people, roles, generations, emails, choices, preferences, sessions int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM people`).Scan(context.Background(), &people))
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM person_roles`).Scan(context.Background(), &roles))
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM recipient_access_generations WHERE state = 'completed' AND onboarding_completed_at IS NOT NULL`).Scan(context.Background(), &generations))
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM recipient_emails WHERE normalized_email = 'robin@example.com' AND is_current`).Scan(context.Background(), &emails))
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM onboarding_choices WHERE privacy_acknowledged AND engagement_acknowledged AND interest_list_acknowledged AND email_preference = 'weekly'`).Scan(context.Background(), &choices))
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM notification_preferences WHERE email_preference = 'weekly'`).Scan(context.Background(), &preferences))
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM sessions WHERE session_type = 'trusted' AND credential_hash IS NOT NULL`).Scan(context.Background(), &sessions))
	assert.Equal(t, 1, people)
	assert.Equal(t, 2, roles)
	assert.Equal(t, 1, generations)
	assert.Equal(t, 1, emails)
	assert.Equal(t, 1, choices)
	assert.Equal(t, 1, preferences)
	assert.Equal(t, 1, sessions)

	var storedCredential bool
	require.NoError(t, db.NewRaw(`SELECT EXISTS (SELECT 1 FROM sessions WHERE encode(credential_hash, 'hex') = ?)`, completed.Credential).Scan(context.Background(), &storedCredential))
	assert.False(t, storedCredential, "the browser credential must never be stored directly")
}

func TestConcurrentFinalSetupHasOneWinnerAndOneConflict(t *testing.T) {
	db, service := newSetupService(t)
	first := verifiedChallenge(t, db, service, "First Browser", "first@example.com")
	second := verifiedChallenge(t, db, service, "Second Browser", "second@example.com")
	e := newSetupHTTP(t, service)
	requests := []CompleteRequest{
		completionRequest(first.VerificationToken, "trusted"),
		completionRequest(second.VerificationToken, "trusted"),
	}
	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, 2)
	var wait sync.WaitGroup
	for _, request := range requests {
		payload, err := json.Marshal(request)
		require.NoError(t, err)
		wait.Add(1)
		go func(body string) {
			defer wait.Done()
			<-start
			responses <- performJSON(e, http.MethodPost, "/api/setup/complete", body, nil)
		}(string(payload))
	}
	close(start)
	wait.Wait()
	close(responses)
	statuses := make([]int, 0, 2)
	for response := range responses {
		statuses = append(statuses, response.Code)
	}
	assert.ElementsMatch(t, []int{http.StatusCreated, http.StatusConflict}, statuses)

	for table, expected := range map[string]int{
		"people": 1, "person_roles": 2, "recipient_access_generations": 1,
		"recipient_emails": 1, "onboarding_choices": 1, "notification_preferences": 1, "sessions": 1,
	} {
		var count int
		require.NoError(t, db.NewRaw(`SELECT count(*) FROM `+table).Scan(context.Background(), &count))
		assert.Equal(t, expected, count, table)
	}
	var consumed int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM login_challenges WHERE consumed_at IS NOT NULL`).Scan(context.Background(), &consumed))
	assert.Equal(t, 1, consumed)
}

func TestSecureCookiesAndSessionBoundCSRF(t *testing.T) {
	db, service := newSetupService(t)
	verified := verifiedChallenge(t, db, service, "Cookie Person", "cookie@example.com")
	e := newSetupHTTP(t, service)
	payload, err := json.Marshal(completionRequest(verified.VerificationToken, "public"))
	require.NoError(t, err)
	completeResponse := performJSON(e, http.MethodPost, "/api/setup/complete", string(payload), nil)
	require.Equal(t, http.StatusCreated, completeResponse.Code)
	assert.Equal(t, "no-store", completeResponse.Header().Get(echo.HeaderCacheControl))
	var completed CompleteResponse
	require.NoError(t, json.Unmarshal(completeResponse.Body.Bytes(), &completed))
	cookies := completeResponse.Result().Cookies()
	require.Len(t, cookies, 1)
	cookie := cookies[0]
	assert.Equal(t, CookieName, cookie.Name)
	assert.True(t, cookie.Secure)
	assert.True(t, cookie.HttpOnly)
	assert.Equal(t, "/", cookie.Path)
	assert.Empty(t, cookie.Domain)
	assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
	assert.Zero(t, cookie.MaxAge, "a Public-computer Session must use a browser-session cookie")
	session, err := service.Session(context.Background(), cookie.Value)
	require.NoError(t, err)
	assert.Equal(t, "Cookie Person", session.DisplayName)
	assert.Equal(t, "public", session.SessionType)
	assert.Equal(t, completed.CSRFToken, session.CSRFToken)

	withoutCSRF := performJSON(e, http.MethodPost, "/api/session/logout", "", nil, cookie)
	assert.Equal(t, http.StatusForbidden, withoutCSRF.Code)
	var active int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM sessions WHERE revoked_at IS NULL`).Scan(context.Background(), &active))
	assert.Equal(t, 1, active)

	withCSRF := performJSON(e, http.MethodPost, "/api/session/logout", "", map[string]string{CSRFHeader: completed.CSRFToken}, cookie)
	assert.Equal(t, http.StatusNoContent, withCSRF.Code)
	cleared := withCSRF.Result().Cookies()
	require.Len(t, cleared, 1)
	assert.Equal(t, -1, cleared[0].MaxAge)
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM sessions WHERE revoked_at IS NULL`).Scan(context.Background(), &active))
	assert.Zero(t, active)
	_, err = service.Session(context.Background(), cookie.Value)
	require.ErrorIs(t, err, ErrUnauthenticated)
}

func TestSetupClosureSurvivesNewServiceAndSafeGETsCreateNothing(t *testing.T) {
	db, service := newSetupService(t)
	e := newSetupHTTP(t, service)
	before := httptest.NewRecorder()
	e.ServeHTTP(before, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/setup", nil))
	require.Equal(t, http.StatusOK, before.Code)
	assert.Equal(t, "no-store", before.Header().Get(echo.HeaderCacheControl))
	for _, table := range []string{"people", "sessions"} {
		var count int
		require.NoError(t, db.NewRaw(`SELECT count(*) FROM `+table).Scan(context.Background(), &count))
		assert.Zero(t, count)
	}

	verified := verifiedChallenge(t, db, service, "Permanent Curator", "permanent@example.com")
	_, err := service.complete(context.Background(), completionRequest(verified.VerificationToken, "trusted"))
	require.NoError(t, err)
	restarted := New(db, emaildelivery.New(db, config.SMTPConfig{Enabled: true}, acceptingSender{}), integrationSecret)
	restartedHTTP := newSetupHTTP(t, restarted)

	after := httptest.NewRecorder()
	restartedHTTP.ServeHTTP(after, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/setup", nil))
	assert.Equal(t, http.StatusNotFound, after.Code)
	unauthenticatedSession := httptest.NewRecorder()
	restartedHTTP.ServeHTTP(unauthenticatedSession, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/session", nil))
	assert.Equal(t, http.StatusUnauthorized, unauthenticatedSession.Code)
	_, err = restarted.RequestCode(context.Background(), RequestCodeRequest{DisplayName: "Other", Email: "other@example.com"})
	require.ErrorIs(t, err, ErrSetupComplete)

	var people, sessions int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM people`).Scan(context.Background(), &people))
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM sessions`).Scan(context.Background(), &sessions))
	assert.Equal(t, 1, people)
	assert.Equal(t, 1, sessions)
}

func TestSetupMutationRequiresJSON(t *testing.T) {
	_, service := newSetupService(t)
	e := newSetupHTTP(t, service)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/setup/code", strings.NewReader("display_name=Cross+Site&email=cross%40example.com"))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	assert.Equal(t, http.StatusUnsupportedMediaType, response.Code)
}

func TestInvalidOrMissingSetupRecordsDoNotBecomeInternalDetails(t *testing.T) {
	_, service := newSetupService(t)
	e := newSetupHTTP(t, service)
	response := performJSON(e, http.MethodPost, "/api/setup/verify", `{"challenge_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","code":"12345678"}`, nil)
	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Contains(t, response.Body.String(), "invalid or expired")
	assert.NotContains(t, response.Body.String(), "challenge_hash")
}
