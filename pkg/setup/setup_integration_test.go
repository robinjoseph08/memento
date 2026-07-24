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
	"time"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/binder"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/emaildelivery"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/outbox"
	"github.com/robinjoseph08/memento/pkg/smtp"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

const integrationSecret = "test-only-security-secret-32-bytes"

type acceptingSender struct {
	messages chan smtp.Message
}

func (sender *acceptingSender) Send(_ context.Context, message smtp.Message) error {
	sender.messages <- message
	return nil
}

var (
	setupSenders sync.Map
	setupCodes   sync.Map
)

func newSetupService(t *testing.T) (*bun.DB, *Service) {
	t.Helper()
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(context.Background(), db))
	sender := &acceptingSender{messages: make(chan smtp.Message, 4)}
	setupSenders.Store(db, sender)
	t.Cleanup(func() {
		setupSenders.Delete(db)
		setupCodes.Delete(db)
	})
	delivery := emaildelivery.New(db, config.SMTPConfig{Enabled: true, RetryWindow: time.Hour}, sender, integrationSecret)
	return db, New(db, delivery, testSecurityConfig())
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
	var persistedBody string
	require.NoError(t, db.NewRaw(`SELECT body FROM email_deliveries ORDER BY id DESC LIMIT 1`).Scan(context.Background(), &persistedBody))
	assert.True(t, strings.HasPrefix(persistedBody, "v1:"), "setup code email must be encrypted at rest")
	deliverLatestCode(t, db, service.delivery)
	return response
}

func deliverLatestCode(t *testing.T, db *bun.DB, delivery *emaildelivery.Service) {
	t.Helper()
	ctx := context.Background()
	dispatched, err := outbox.New(db).Dispatch(ctx, "setup-test-dispatcher", time.Minute)
	require.NoError(t, err)
	require.True(t, dispatched)
	var job worker.Job
	err = db.NewRaw(`
		WITH candidate AS (
			SELECT id FROM jobs WHERE status = 'pending' ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED
		)
		UPDATE jobs AS job SET status = 'running', lease_owner = 'setup-test-worker',
			lease_expires_at = now() + interval '1 minute'
		FROM candidate WHERE job.id = candidate.id
		RETURNING job.id, job.kind, job.payload, job.attempts
	`).Scan(ctx, &job.ID, &job.Kind, &job.Payload, &job.Attempts)
	require.NoError(t, err)
	job.LeaseOwner = "setup-test-worker"
	require.NoError(t, delivery.Handle(ctx, job))
	var persistedBody string
	require.NoError(t, db.NewRaw(`SELECT body FROM email_deliveries ORDER BY id DESC LIMIT 1`).Scan(ctx, &persistedBody))
	assert.Empty(t, persistedBody, "sensitive body must be erased after terminal delivery")
	_, err = db.NewRaw(`
		UPDATE jobs SET status = 'completed', lease_owner = NULL, lease_expires_at = NULL WHERE id = ?
	`, job.ID).Exec(ctx)
	require.NoError(t, err)

	senderValue, ok := setupSenders.Load(db)
	require.True(t, ok)
	message := <-senderValue.(*acceptingSender).messages
	code := regexp.MustCompile(`\b[0-9]{8}\b`).FindString(message.Body)
	require.Len(t, code, 8)
	setupCodes.Store(db, code)
}

func latestCode(t *testing.T, db *bun.DB) string {
	t.Helper()
	code, ok := setupCodes.Load(db)
	require.True(t, ok)
	return code.(string)
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
	service := New(db, emaildelivery.New(db, config.SMTPConfig{}, nil), testSecurityConfig())

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

	var auditActions []string
	require.NoError(t, db.NewRaw(`SELECT action FROM security_audit_events ORDER BY id`).Scan(context.Background(), &auditActions))
	assert.Equal(t, []string{"setup_code_requested", "setup_code_verified", "setup_completed", "session_created"}, auditActions)

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
	var signedOutAudit int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM security_audit_events WHERE action = 'session_signed_out' AND outcome = 'success'`).Scan(context.Background(), &signedOutAudit))
	assert.Equal(t, 1, signedOutAudit)
}

func TestTrustedSessionRefreshExtendsInactivityWithoutMutatingGET(t *testing.T) {
	db, service := newSetupService(t)
	verified := verifiedChallenge(t, db, service, "Trusted Person", "trusted@example.com")
	e := newSetupHTTP(t, service)
	payload, err := json.Marshal(completionRequest(verified.VerificationToken, "trusted"))
	require.NoError(t, err)
	completeResponse := performJSON(e, http.MethodPost, "/api/setup/complete", string(payload), nil)
	require.Equal(t, http.StatusCreated, completeResponse.Code)
	cookie := completeResponse.Result().Cookies()[0]
	var completed CompleteResponse
	require.NoError(t, json.Unmarshal(completeResponse.Body.Bytes(), &completed))

	var beforeActivity, beforeExpiry time.Time
	require.NoError(t, db.NewRaw(`SELECT last_activity_at, idle_expires_at FROM sessions`).Scan(context.Background(), &beforeActivity, &beforeExpiry))
	getSession := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/session", nil)
	request.AddCookie(cookie)
	e.ServeHTTP(getSession, request)
	require.Equal(t, http.StatusOK, getSession.Code)
	var afterGET time.Time
	require.NoError(t, db.NewRaw(`SELECT last_activity_at FROM sessions`).Scan(context.Background(), &afterGET))
	assert.Equal(t, beforeActivity, afterGET, "safe GET must not refresh Session activity")

	refreshTime := beforeActivity.Add(24 * time.Hour)
	service.now = func() time.Time { return refreshTime }
	refreshed := performJSON(
		e, http.MethodPost, "/api/session/refresh", "",
		map[string]string{CSRFHeader: completed.CSRFToken}, cookie,
	)
	require.Equal(t, http.StatusNoContent, refreshed.Code)
	cookies := refreshed.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Greater(t, cookies[0].MaxAge, 0)
	assert.WithinDuration(t, refreshTime.Add(trustedLifetime), cookies[0].Expires, time.Second)

	var afterActivity, afterExpiry time.Time
	require.NoError(t, db.NewRaw(`SELECT last_activity_at, idle_expires_at FROM sessions`).Scan(context.Background(), &afterActivity, &afterExpiry))
	assert.Equal(t, refreshTime, afterActivity)
	assert.Greater(t, afterExpiry, beforeExpiry)
}

func TestSetupRateLimitsAreNonEnumerating(t *testing.T) {
	_, service := newSetupService(t)
	service.security.SetupEmailLimit = 1
	service.security.SetupIPLimit = 2
	e := newSetupHTTP(t, service)
	body := `{"display_name":"Rate Limited","email":"rate@example.com"}`
	first := performJSON(e, http.MethodPost, "/api/setup/code", body, nil)
	require.Equal(t, http.StatusAccepted, first.Code)
	second := performJSON(e, http.MethodPost, "/api/setup/code", body, nil)
	assert.Equal(t, http.StatusTooManyRequests, second.Code)
	assert.Contains(t, second.Body.String(), "Too many setup attempts")
	assert.NotContains(t, strings.ToLower(second.Body.String()), "rate@example.com")
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
	restartedSender := &acceptingSender{messages: make(chan smtp.Message, 1)}
	restarted := New(
		db,
		emaildelivery.New(db, config.SMTPConfig{Enabled: true}, restartedSender, integrationSecret),
		testSecurityConfig(),
	)
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
