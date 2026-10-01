package identity_test

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// codeClock is a controllable clock for code expiry and send limits.
type codeClock struct{ now time.Time }

func (c *codeClock) read() time.Time         { return c.now }
func (c *codeClock) advance(d time.Duration) { c.now = c.now.Add(d) }

type codeHarness struct {
	db       *bun.DB
	module   *identity.Module
	recorder *notifications.Recorder
	// alerts records Curator alerts queued for Access Requests.
	alerts *mailQueue
	clock  *codeClock
}

func newCodeHarness(t *testing.T) *codeHarness {
	t.Helper()
	clock := &codeClock{now: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)}
	db := testdb.New(t)
	module := identity.New(db, clock.read)
	module.PublicURL = "https://memento.example.test"
	recorder := &notifications.Recorder{}
	alerts := &mailQueue{}
	module.Mail = notifications.New(db, recorder, alerts.enqueue, nil, clock.read)
	module.Sender = recorder
	return &codeHarness{db: db, module: module, recorder: recorder, alerts: alerts, clock: clock}
}

var sixDigits = regexp.MustCompile(`\d{6}`)

// request asks for a code and returns the one emailed, or "" when nothing was sent.
func (h *codeHarness) request(t *testing.T, email string) string {
	t.Helper()
	before := len(h.recorder.Sent())
	require.NoError(t, h.module.RequestSignInCode(t.Context(), identity.RequestSignInCodeRequest{Email: email}))
	sent := h.recorder.Sent()
	if len(sent) == before {
		return ""
	}
	require.Len(t, sent, before+1)
	return sixDigits.FindString(sent[len(sent)-1].Subject)
}

func (h *codeHarness) verify(t *testing.T, email, code, name string) (identity.Session, error) {
	t.Helper()
	return h.module.VerifySignInCode(t.Context(), identity.VerifySignInCodeRequest{Email: email, Code: code, DisplayName: name})
}

func requireInvalidCode(t *testing.T, err error) {
	t.Helper()
	field, ok := errors.AsType[*errcodes.FieldError](err)
	require.True(t, ok, "expected a code field error, got %v", err)
	assert.Contains(t, field.Fields, "code")
}

// codeMessage is what the code field says after a failed verification.
func codeMessage(t *testing.T, err error) string {
	t.Helper()
	requireInvalidCode(t, err)
	field, _ := errors.AsType[*errcodes.FieldError](err)
	return field.Fields["code"]
}

// requireWrongCode means the Person is told to check the code and retype it.
func requireWrongCode(t *testing.T, err error) {
	t.Helper()
	assert.Contains(t, codeMessage(t, err), "isn't right")
}

// requireDeadCode means the Person is told to send a new code.
func requireDeadCode(t *testing.T, err error) {
	t.Helper()
	assert.Contains(t, codeMessage(t, err), "no longer works")
}

func TestSignInCodeEmail(t *testing.T) {
	t.Parallel()
	h := newCodeHarness(t)
	code := h.request(t, " Owner@Example.test ")
	require.Len(t, code, 6)
	sent := h.recorder.Sent()
	require.Len(t, sent, 1)
	message := sent[0]
	assert.Equal(t, "owner@example.test", message.To)
	assert.Equal(t, "sign_in_code", message.Kind)
	assert.Contains(t, message.Subject, code)
	assert.Contains(t, message.Body, code)
	assert.Contains(t, message.Body, "10 minutes")
	assert.Contains(t, message.Body, "ignore")

	var rows []models.SignInCode
	require.NoError(t, h.db.NewSelect().Model(&rows).Scan(t.Context()))
	require.Len(t, rows, 1)
	assert.Len(t, rows[0].CodeHash, 32, "only a hash is stored")
	assert.NotContains(t, string(rows[0].CodeHash), code)
	assert.False(t, rows[0].Known, "nobody is known before the Installation is claimed")
}

func TestSignInCodeLifecycle(t *testing.T) {
	t.Parallel()
	h := newCodeHarness(t)
	curator := claimCurator(t, h.module)
	authorizePerson(t, h.module, curator, "Alex", "alex@example.test")

	// A pasted code with spaces works, once.
	code := h.request(t, "alex@example.test")
	session, err := h.verify(t, "ALEX@example.test", code[:3]+" "+code[3:], "")
	require.NoError(t, err)
	assert.Equal(t, "Alex", session.Person.DisplayName)
	assert.Len(t, session.Token, 43)
	_, err = h.verify(t, "alex@example.test", code, "")
	requireDeadCode(t, err)

	// A code works for ten minutes after it was sent, and not after.
	h.clock.advance(time.Minute)
	code = h.request(t, "alex@example.test")
	h.clock.advance(identity.SignInCodeLifetime - time.Second)
	_, err = h.verify(t, "alex@example.test", code, "")
	require.NoError(t, err)
	h.clock.advance(time.Second)
	code = h.request(t, "alex@example.test")
	h.clock.advance(identity.SignInCodeLifetime)
	_, err = h.verify(t, "alex@example.test", code, "")
	requireDeadCode(t, err)

	// Five wrong attempts kill it, even for the right code afterwards.
	code = h.request(t, "alex@example.test")
	digits, err := strconv.Atoi(code)
	require.NoError(t, err)
	wrong := fmt.Sprintf("%06d", (digits+1)%1000000)
	for range 4 {
		_, err = h.verify(t, "alex@example.test", wrong, "")
		requireWrongCode(t, err)
	}
	_, err = h.verify(t, "alex@example.test", wrong, "")
	requireDeadCode(t, err)
	_, err = h.verify(t, "alex@example.test", code, "")
	requireDeadCode(t, err)

	// Four wrong attempts do not.
	h.clock.advance(time.Minute)
	code = h.request(t, "alex@example.test")
	for range 4 {
		_, err = h.verify(t, "alex@example.test", wrong, "")
		requireWrongCode(t, err)
	}
	_, err = h.verify(t, "alex@example.test", code, "")
	require.NoError(t, err)

	// A new code replaces the old one.
	h.clock.advance(time.Minute)
	old := h.request(t, "alex@example.test")
	h.clock.advance(time.Minute)
	replacement := h.request(t, "alex@example.test")
	if old != replacement {
		_, err = h.verify(t, "alex@example.test", old, "")
		requireWrongCode(t, err)
	}
	_, err = h.verify(t, "alex@example.test", replacement, "")
	require.NoError(t, err)

	// A code for one address does not sign in another.
	h.clock.advance(time.Minute)
	code = h.request(t, "alex@example.test")
	_, err = h.verify(t, "curator@example.test", code, "")
	requireInvalidCode(t, err)
	_, err = h.verify(t, "nobody@example.test", "123456", "")
	requireWrongCode(t, err)
}

// Once the unknown-address cap is spent, a known and an unknown address must
// still answer wrong guesses alike, or the answers would show who belongs.
func TestSignInCodeAnswersAlikeOverTheCap(t *testing.T) {
	t.Parallel()
	h := newCodeHarness(t)
	curator := claimCurator(t, h.module)
	authorizePerson(t, h.module, curator, "Alex", "alex@example.test")
	h.clock.advance(time.Minute)

	// Each address has a dead code left over from an earlier attempt.
	require.NotEmpty(t, h.request(t, "alex@example.test"))
	require.NotEmpty(t, h.request(t, "stranger@example.test"))
	h.clock.advance(identity.SignInCodeLifetime)
	for i := range 9 {
		require.NotEmpty(t, h.request(t, fmt.Sprintf("filler-%d@example.test", i)))
	}

	// Now only the known address is actually emailed.
	alex := h.request(t, "alex@example.test")
	require.NotEmpty(t, alex)
	require.Empty(t, h.request(t, "stranger@example.test"))
	digits, err := strconv.Atoi(alex)
	require.NoError(t, err)
	wrong := fmt.Sprintf("%06d", (digits+1)%1000000)
	answers := func(email string) []string {
		var messages []string
		for range 6 {
			_, err := h.verify(t, email, wrong, "")
			messages = append(messages, codeMessage(t, err))
		}
		return messages
	}
	known := answers("alex@example.test")
	assert.Equal(t, known, answers("stranger@example.test"))
	assert.Contains(t, known[0], "isn't right")
	assert.Contains(t, known[5], "no longer works")

	// The stand-in for the unsent code never counts toward the cap: once the
	// first stranger code ages out, nine sent codes remain and one more fits.
	h.clock.advance(time.Hour - identity.SignInCodeLifetime)
	assert.NotEmpty(t, h.request(t, "late@example.test"))
}

func TestSignInCodeSendLimits(t *testing.T) {
	t.Parallel()
	h := newCodeHarness(t)
	curator := claimCurator(t, h.module)
	authorizePerson(t, h.module, curator, "Alex", "alex@example.test")
	h.clock.advance(time.Minute)

	// One email per address per minute; the second request reports success.
	first := h.request(t, "stranger@example.test")
	require.NotEmpty(t, first)
	h.clock.advance(59 * time.Second)
	assert.Empty(t, h.request(t, "stranger@example.test"))
	h.clock.advance(time.Second)
	assert.NotEmpty(t, h.request(t, "stranger@example.test"))

	// Ten codes an hour go to unknown addresses; the eleventh is silent.
	for i := range 8 {
		assert.NotEmpty(t, h.request(t, fmt.Sprintf("stranger-%d@example.test", i)), "unknown code %d", i+3)
	}
	assert.Empty(t, h.request(t, "stranger-8@example.test"))
	// Known addresses, preauthorized or linked, are never capped.
	assert.NotEmpty(t, h.request(t, "alex@example.test"))
	assert.NotEmpty(t, h.request(t, "curator@example.test"))
	// The hour is rolling.
	h.clock.advance(time.Hour)
	assert.NotEmpty(t, h.request(t, "stranger-8@example.test"))
}

func TestSignInCodeResolvesLikeGoogle(t *testing.T) {
	t.Parallel()
	h := newCodeHarness(t)

	// An unclaimed Installation is claimed by the first verified address,
	// named by its local part until Onboarding asks.
	code := h.request(t, "Robin.Joseph@example.test")
	owner, err := h.verify(t, "robin.joseph@example.test", code, "")
	require.NoError(t, err)
	assert.True(t, owner.Person.IsCurator)
	assert.Equal(t, "robin.joseph", owner.Person.DisplayName)
	assert.Nil(t, owner.Person.OnboardingCompletedAt)
	assert.Equal(t, "robin.joseph@example.test", owner.Person.UpdateEmail)

	// A Linked Email signs its Person in.
	h.clock.advance(time.Minute)
	code = h.request(t, "robin.joseph@example.test")
	again, err := h.verify(t, "robin.joseph@example.test", code, "")
	require.NoError(t, err)
	assert.Equal(t, owner.Person.ID, again.Person.ID)

	// A Preauthorization links the address.
	person, err := h.module.CreatePerson(t.Context(), owner.Token, identity.CreatePersonRequest{DisplayName: "Sam"})
	require.NoError(t, err)
	_, err = h.module.Preauthorize(t.Context(), owner.Token, person.ID, identity.PreauthorizeRequest{Email: "sam@example.test"})
	require.NoError(t, err)
	code = h.request(t, "sam@example.test")
	sam, err := h.verify(t, "sam@example.test", code, "")
	require.NoError(t, err)
	assert.Equal(t, person.ID, sam.Person.ID)
	assert.False(t, sam.Person.IsCurator)

	// An unknown address is asked for a name before anything is recorded,
	// and the code survives the question.
	code = h.request(t, "stranger@example.test")
	_, err = h.verify(t, "stranger@example.test", code, "")
	require.ErrorIs(t, err, identity.ErrNameRequired)
	requests, err := h.module.ListAccessRequests(t.Context(), owner.Token)
	require.NoError(t, err)
	assert.Empty(t, requests)
	assert.Zero(t, h.alerts.count())
	_, err = h.verify(t, "stranger@example.test", code, "  Jordan  ")
	require.ErrorIs(t, err, identity.ErrAccessRequested)
	assert.Equal(t, 1, h.alerts.count(), "Curators are alerted to the new request")
	requests, err = h.module.ListAccessRequests(t.Context(), owner.Token)
	require.NoError(t, err)
	require.Len(t, requests, 1)
	assert.Equal(t, "Jordan", requests[0].DisplayName)
	assert.Equal(t, "stranger@example.test", requests[0].Email)
	assert.Equal(t, 1, requests[0].SignInCount)
	_, err = h.verify(t, "stranger@example.test", code, "Jordan")
	requireInvalidCode(t, err)

	// A repeat attempt counts on the same request.
	h.clock.advance(time.Minute)
	code = h.request(t, "stranger@example.test")
	_, err = h.verify(t, "stranger@example.test", code, "Jordan Lee")
	require.ErrorIs(t, err, identity.ErrAccessRequested)
	requests, err = h.module.ListAccessRequests(t.Context(), owner.Token)
	require.NoError(t, err)
	require.Len(t, requests, 1)
	assert.Equal(t, 2, requests[0].SignInCount)
	assert.Equal(t, "Jordan Lee", requests[0].DisplayName)
	assert.Equal(t, 1, h.alerts.count(), "a repeat attempt alerts nobody again")

	// A name that is too long is a field error, and the code survives it.
	h.clock.advance(time.Minute)
	code = h.request(t, "other@example.test")
	_, err = h.verify(t, "other@example.test", code, strings.Repeat("x", 101))
	field, ok := errors.AsType[*errcodes.FieldError](err)
	require.True(t, ok, "%v", err)
	assert.Contains(t, field.Fields, "display_name")
	_, err = h.verify(t, "other@example.test", code, "Other")
	require.ErrorIs(t, err, identity.ErrAccessRequested)

	// A deactivated Person is refused.
	_, err = h.module.UpdatePerson(t.Context(), owner.Token, sam.Person.ID, identity.UpdatePersonRequest{DisplayName: "Sam", Deactivated: true})
	require.NoError(t, err)
	h.clock.advance(time.Minute)
	code = h.request(t, "sam@example.test")
	_, err = h.verify(t, "sam@example.test", code, "")
	require.ErrorIs(t, err, identity.ErrAccessDenied)
}

func TestSignInCodeSendFailure(t *testing.T) {
	t.Parallel()
	h := newCodeHarness(t)
	failing := func(context.Context, notifications.Message) error {
		return &notifications.DeliveryError{Outcome: notifications.OutcomeTransient, Summary: "The mail server could not be reached.", Cause: errors.New("dial refused")}
	}
	h.recorder.BeforeSend = failing
	err := h.module.RequestSignInCode(t.Context(), identity.RequestSignInCodeRequest{Email: "owner@example.test"})
	require.ErrorIs(t, err, identity.ErrSignInCodeUnsent)
	_, kept := errors.AsType[*notifications.DeliveryError](err)
	assert.True(t, kept, "the cause is kept for the log")

	// A code that never went out does not hold back an immediate retry.
	h.recorder.BeforeSend = nil
	first := h.request(t, "owner@example.test")
	require.NotEmpty(t, first)

	// Nor does it replace the code the Person already has.
	h.clock.advance(time.Minute)
	h.recorder.BeforeSend = failing
	err = h.module.RequestSignInCode(t.Context(), identity.RequestSignInCodeRequest{Email: "owner@example.test"})
	require.ErrorIs(t, err, identity.ErrSignInCodeUnsent)
	_, err = h.verify(t, "owner@example.test", first, "")
	require.NoError(t, err)

	// Without mail there is no code sign-in at all.
	h.module.Sender = nil
	assert.False(t, h.module.SignInCodesAvailable())
	err = h.module.RequestSignInCode(t.Context(), identity.RequestSignInCodeRequest{Email: "owner@example.test"})
	require.ErrorIs(t, err, identity.ErrSignInCodesUnavailable)
}
