//go:build integration

package suggestions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/people"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type suggestionFixture struct {
	db             *bun.DB
	service        *Service
	curator        setup.CuratorSession
	requester      setup.SessionActor
	otherRequester setup.SessionActor
	existingID     uuid.UUID
	now            time.Time
}

func newSuggestionFixture(t *testing.T) suggestionFixture {
	t.Helper()
	ctx := context.Background()
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(ctx, db))
	curatorID, requesterID, otherID, existingID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err := db.NewRaw(`
		INSERT INTO people (id, display_name, sort_name) VALUES
			(?, 'Robin Curator', 'Curator, Robin'),
			(?, 'Alex Requester', 'Requester, Alex'),
			(?, 'Morgan Other', 'Other, Morgan'),
			(?, 'Taylor Existing', 'Existing, Taylor');
		INSERT INTO person_roles (person_id, role) VALUES
			(?, 'curator'), (?, 'recipient'), (?, 'recipient'), (?, 'recipient'), (?, 'recipient')
	`, curatorID, requesterID, otherID, existingID, curatorID, curatorID, requesterID, otherID, existingID).Exec(ctx)
	require.NoError(t, err)
	curatorAccess, requesterAccess, otherAccess, existingAccess := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err = db.NewRaw(`
		INSERT INTO recipient_access_generations (id, person_id, generation, state, is_current, onboarding_completed_at) VALUES
			(?, ?, 1, 'completed', true, now()), (?, ?, 1, 'completed', true, now()),
			(?, ?, 1, 'completed', true, now()), (?, ?, 1, 'completed', true, now());
		INSERT INTO recipient_emails (id, recipient_access_generation_id, email, normalized_email) VALUES
			(?, ?, 'robin@example.com', 'robin@example.com'), (?, ?, 'alex@example.com', 'alex@example.com'),
			(?, ?, 'morgan@example.com', 'morgan@example.com'), (?, ?, 'taylor@example.com', 'taylor@example.com')
	`, curatorAccess, curatorID, requesterAccess, requesterID, otherAccess, otherID, existingAccess, existingID,
		uuid.New(), curatorAccess, uuid.New(), requesterAccess, uuid.New(), otherAccess, uuid.New(), existingAccess).Exec(ctx)
	require.NoError(t, err)
	var epoch []byte
	require.NoError(t, db.NewRaw(`UPDATE system_settings SET setup_complete = true RETURNING security_epoch`).Scan(ctx, &epoch))
	curatorSession, requesterSession, otherSession := uuid.New(), uuid.New(), uuid.New()
	_, err = db.NewRaw(`
		INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at) VALUES
			(?, decode(repeat('11', 32), 'hex'), ?, ?, ?, 'trusted', now() + interval '1 hour'),
			(?, decode(repeat('22', 32), 'hex'), ?, ?, ?, 'trusted', now() + interval '1 hour'),
			(?, decode(repeat('33', 32), 'hex'), ?, ?, ?, 'trusted', now() + interval '1 hour')
	`, curatorSession, curatorID, curatorAccess, epoch, requesterSession, requesterID, requesterAccess, epoch,
		otherSession, otherID, otherAccess, epoch).Exec(ctx)
	require.NoError(t, err)
	now := time.Date(2026, 7, 27, 12, 30, 0, 0, time.UTC)
	service := New(db, people.New(db))
	service.now = func() time.Time { return now }
	return suggestionFixture{
		db: db, service: service, existingID: existingID, now: now,
		curator:        setup.CuratorSession{PersonID: curatorID, SessionID: curatorSession},
		requester:      setup.SessionActor{PersonID: requesterID, AccessID: requesterAccess, SessionID: requesterSession},
		otherRequester: setup.SessionActor{PersonID: otherID, AccessID: otherAccess, SessionID: otherSession},
	}
}

func boolPointer(value bool) *bool { return &value }

func submitSuggestion(t *testing.T, fixture suggestionFixture, actor setup.SessionActor, name, email string) RequesterSuggestion {
	t.Helper()
	request := httptest.NewRequest("POST", "/api/invitation-suggestions", nil)
	request.RemoteAddr = "192.0.2.8:1234"
	request.Header.Set("User-Agent", "suggestion-test")
	ctx := setup.New(nil, nil, config.SecurityConfig{}).ContextWithRequestMetadata(context.Background(), request)
	result, err := fixture.service.Submit(ctx, actor, SubmitRequest{
		Name: name, Email: email, RelationshipContext: "Cousin <script>alert('family')</script> & trusted notes",
		SpokeWithPerson: boolPointer(false),
	})
	require.NoError(t, err)
	return result
}

func TestSubmissionIsIsolatedAuditedAndHasNoIdentityOrAccessSideEffects(t *testing.T) {
	fixture := newSuggestionFixture(t)
	ctx := context.Background()
	var peopleBefore, accessBefore, invitationsBefore, sessionsBefore int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM people`).Scan(ctx, &peopleBefore))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM recipient_access_generations`).Scan(ctx, &accessBefore))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM invitations`).Scan(ctx, &invitationsBefore))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM sessions`).Scan(ctx, &sessionsBefore))

	submitted := submitSuggestion(t, fixture, fixture.requester, "  Taylor Existing  ", "taylor@example.com")
	assert.Equal(t, "Taylor Existing", submitted.Name)
	assert.Equal(t, "Cousin <script>alert('family')</script> & trusted notes", submitted.RelationshipContext)
	assert.Equal(t, "submitted", submitted.Status)
	assert.False(t, submitted.SpokeWithPerson)

	mine, err := fixture.service.ListRequester(ctx, fixture.requester)
	require.NoError(t, err)
	require.Len(t, mine.Suggestions, 1)
	assert.Equal(t, submitted.ID, mine.Suggestions[0].ID)
	other, err := fixture.service.ListRequester(ctx, fixture.otherRequester)
	require.NoError(t, err)
	assert.Empty(t, other.Suggestions, "another Recipient must not discover a request by ID or list")

	curator, err := fixture.service.ListCurator(ctx, "submitted")
	require.NoError(t, err)
	require.Len(t, curator.Suggestions, 1)
	require.Len(t, curator.Suggestions[0].MatchingPeople, 1)
	assert.Equal(t, fixture.existingID.String(), curator.Suggestions[0].MatchingPeople[0].PersonID)
	assert.ElementsMatch(t, []string{"same_name", "same_recipient_email"}, curator.Suggestions[0].MatchingPeople[0].Reasons)

	var peopleAfter, accessAfter, invitationsAfter, sessionsAfter, recipientActivity, curatorActivity int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM people`).Scan(ctx, &peopleAfter))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM recipient_access_generations`).Scan(ctx, &accessAfter))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM invitations`).Scan(ctx, &invitationsAfter))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM sessions`).Scan(ctx, &sessionsAfter))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM recipient_activity_items WHERE invitation_suggestion_id = ?`, submitted.ID).Scan(ctx, &recipientActivity))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM curator_activity_items WHERE invitation_suggestion_id = ?`, submitted.ID).Scan(ctx, &curatorActivity))
	assert.Equal(t, peopleBefore, peopleAfter)
	assert.Equal(t, accessBefore, accessAfter)
	assert.Equal(t, invitationsBefore, invitationsAfter)
	assert.Equal(t, sessionsBefore, sessionsAfter)
	assert.Equal(t, 1, recipientActivity)
	assert.Equal(t, 1, curatorActivity)

	var actorID, sessionID uuid.UUID
	var clientIP, userAgent string
	require.NoError(t, fixture.db.NewRaw(`SELECT actor_person_id, session_id, client_ip::text, user_agent FROM security_audit_events WHERE action = 'invitation_suggestion_submitted'`).Scan(ctx, &actorID, &sessionID, &clientIP, &userAgent))
	assert.Equal(t, fixture.requester.PersonID, actorID)
	assert.Equal(t, fixture.requester.SessionID, sessionID)
	assert.Equal(t, "192.0.2.8/32", clientIP)
	assert.Equal(t, "suggestion-test", userAgent)

	encoded, err := json.Marshal(mine.Suggestions[0])
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "matched_person")
	assert.NotContains(t, string(encoded), "invitation")
	assert.NotContains(t, string(encoded), "onboarding")
	assert.NotContains(t, string(encoded), "recipient_access")
}

func TestDuplicateSubmissionAndExplicitExistingPersonMatchNeverCreateAccess(t *testing.T) {
	fixture := newSuggestionFixture(t)
	first := submitSuggestion(t, fixture, fixture.requester, "Taylor Existing", "taylor@example.com")
	_ = submitSuggestion(t, fixture, fixture.otherRequester, "Taylor E.", "TAYLOR@example.com")
	queue, err := fixture.service.ListCurator(context.Background(), "submitted")
	require.NoError(t, err)
	var duplicateCount int
	for _, item := range queue.Suggestions {
		if item.ID == first.ID {
			duplicateCount = item.DuplicateSuggestionCount
		}
	}
	assert.Equal(t, 1, duplicateCount)

	var peopleBefore, accessBefore int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM people`).Scan(context.Background(), &peopleBefore))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM recipient_access_generations`).Scan(context.Background(), &accessBefore))
	accepted, err := fixture.service.AcceptExisting(context.Background(), fixture.curator, uuid.MustParse(first.ID), fixture.existingID)
	require.NoError(t, err)
	assert.Equal(t, "accepted", accepted.Status)
	assert.Equal(t, fixture.existingID.String(), accepted.MatchedPersonID)
	var peopleAfter, accessAfter, invitations int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM people`).Scan(context.Background(), &peopleAfter))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM recipient_access_generations`).Scan(context.Background(), &accessAfter))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM invitations`).Scan(context.Background(), &invitations))
	assert.Equal(t, peopleBefore, peopleAfter)
	assert.Equal(t, accessBefore, accessAfter)
	assert.Zero(t, invitations)
	var auditActor, auditSubject, auditSession uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`
		SELECT actor_person_id, subject_person_id, session_id FROM security_audit_events
		WHERE action = 'invitation_suggestion_accepted' AND metadata->>'invitation_suggestion_id' = ?
	`, first.ID).Scan(context.Background(), &auditActor, &auditSubject, &auditSession))
	assert.Equal(t, fixture.curator.PersonID, auditActor)
	assert.Equal(t, fixture.requester.PersonID, auditSubject)
	assert.Equal(t, fixture.curator.SessionID, auditSession)

	mine, err := fixture.service.ListRequester(context.Background(), fixture.requester)
	require.NoError(t, err)
	assert.Equal(t, "accepted", mine.Suggestions[0].Status)
	encoded, err := json.Marshal(mine)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), fixture.existingID.String(), "requester status must not reveal the Curator's Person match")
}

func TestAcceptWithNewPersonAndRejectAreExplicitAndDoNotDesignateRecipientAccess(t *testing.T) {
	fixture := newSuggestionFixture(t)
	acceptedSuggestion := submitSuggestion(t, fixture, fixture.requester, "New Relative", "new@example.com")
	rejectedSuggestion := submitSuggestion(t, fixture, fixture.otherRequester, "No Match", "nomatch@example.com")

	accepted, err := fixture.service.AcceptNew(context.Background(), fixture.curator, uuid.MustParse(acceptedSuggestion.ID), people.CreateRequest{DisplayName: "New Relative", SortName: "Relative, New"})
	require.NoError(t, err)
	assert.Equal(t, "accepted", accepted.Status)
	assert.Equal(t, "New Relative", accepted.MatchedPersonName)
	assert.NotEmpty(t, accepted.MatchedPersonID)
	var roles, access, invitations, sessions int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM person_roles WHERE person_id = ?`, accepted.MatchedPersonID).Scan(context.Background(), &roles))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM recipient_access_generations WHERE person_id = ?`, accepted.MatchedPersonID).Scan(context.Background(), &access))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM invitations`).Scan(context.Background(), &invitations))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM sessions WHERE person_id = ?`, accepted.MatchedPersonID).Scan(context.Background(), &sessions))
	assert.Zero(t, roles)
	assert.Zero(t, access)
	assert.Zero(t, invitations)
	assert.Zero(t, sessions)

	rejected, err := fixture.service.Reject(context.Background(), fixture.curator, uuid.MustParse(rejectedSuggestion.ID))
	require.NoError(t, err)
	assert.Equal(t, "rejected", rejected.Status)
	var recipientActivity, curatorActivity int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM recipient_activity_items WHERE invitation_suggestion_id IN (?, ?)`, acceptedSuggestion.ID, rejectedSuggestion.ID).Scan(context.Background(), &recipientActivity))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM curator_activity_items WHERE invitation_suggestion_id IN (?, ?)`, acceptedSuggestion.ID, rejectedSuggestion.ID).Scan(context.Background(), &curatorActivity))
	assert.Equal(t, 4, recipientActivity)
	assert.Equal(t, 4, curatorActivity)
}

func TestPersonMergeMovesSuggestionOwnershipAndMatchWithoutRewritingAttribution(t *testing.T) {
	fixture := newSuggestionFixture(t)
	ctx := context.Background()
	submitted := submitSuggestion(t, fixture, fixture.requester, "Alex Requester", "alex@example.com")
	_, err := fixture.service.AcceptExisting(ctx, fixture.curator, uuid.MustParse(submitted.ID), fixture.requester.PersonID)
	require.NoError(t, err)

	survivorID := uuid.New()
	_, err = fixture.db.NewRaw(`INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Alex Survivor', 'Survivor, Alex')`, survivorID).Exec(ctx)
	require.NoError(t, err)
	peopleService := people.New(fixture.db)
	preview, err := peopleService.PreviewMerge(ctx, fixture.curator, fixture.requester.PersonID, survivorID)
	require.NoError(t, err)
	require.True(t, preview.RequiresGenerationTransfer)
	_, err = peopleService.Merge(ctx, fixture.curator, people.MergeRequest{
		SourcePersonID: fixture.requester.PersonID.String(), SurvivorPersonID: survivorID.String(),
		SourceVersion: 1, SurvivorVersion: 1, TransferCurrentAccessGeneration: true,
		ExpectedRecipientGeneration: preview.References.ResultingRecipientGeneration,
		PreviewFingerprint:          preview.PreviewFingerprint,
	})
	require.NoError(t, err)

	mergedActor := fixture.requester
	mergedActor.PersonID = survivorID
	history, err := fixture.service.ListRequester(ctx, mergedActor)
	require.NoError(t, err)
	require.Len(t, history.Suggestions, 1)
	assert.Equal(t, "accepted", history.Suggestions[0].Status)
	var requesterID, matchedID uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT requester_person_id, matched_person_id FROM invitation_suggestions WHERE id = ?`, submitted.ID).Scan(ctx, &requesterID, &matchedID))
	assert.Equal(t, survivorID, requesterID)
	assert.Equal(t, survivorID, matchedID)
	var movedActivity, attributedAudits int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM recipient_activity_items WHERE invitation_suggestion_id = ? AND recipient_person_id = ?`, submitted.ID, survivorID).Scan(ctx, &movedActivity))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM security_audit_events WHERE metadata->>'invitation_suggestion_id' = ? AND subject_person_id = ?`, submitted.ID, fixture.requester.PersonID).Scan(ctx, &attributedAudits))
	assert.Equal(t, 2, movedActivity)
	assert.Equal(t, 2, attributedAudits, "historical audit attribution remains attached to the original Person")
}

func TestWithdrawalAndCuratorResolutionSerializeWithOneWinner(t *testing.T) {
	fixture := newSuggestionFixture(t)
	submitted := submitSuggestion(t, fixture, fixture.requester, "Race Relative", "race@example.com")
	id := uuid.MustParse(submitted.ID)
	start := make(chan struct{})
	errorsFound := make(chan error, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		_, err := fixture.service.Withdraw(context.Background(), fixture.requester, id)
		errorsFound <- err
	}()
	go func() {
		defer wait.Done()
		<-start
		_, err := fixture.service.Reject(context.Background(), fixture.curator, id)
		errorsFound <- err
	}()
	close(start)
	wait.Wait()
	close(errorsFound)
	var successes, conflicts int
	for err := range errorsFound {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrNotSubmitted) {
			conflicts++
		} else {
			require.NoError(t, err)
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, conflicts)
	mine, err := fixture.service.ListRequester(context.Background(), fixture.requester)
	require.NoError(t, err)
	require.Len(t, mine.Suggestions, 1)
	assert.Contains(t, []string{"withdrawn", "rejected"}, mine.Suggestions[0].Status)
	_, err = fixture.service.Withdraw(context.Background(), fixture.otherRequester, id)
	assert.ErrorIs(t, err, ErrNotFound, "a requester must not use another Recipient's suggestion ID")
}

func TestInvalidFreeFormAndMissingSpokenAnswerFailBeforePersistence(t *testing.T) {
	fixture := newSuggestionFixture(t)
	tests := []SubmitRequest{
		{Name: "Relative", Email: "relative@example.com", RelationshipContext: "Context"},
		{Name: "Relative\x00", Email: "relative@example.com", RelationshipContext: "Context", SpokeWithPerson: boolPointer(true)},
		{Name: "Relative", Email: "not an email", RelationshipContext: "Context", SpokeWithPerson: boolPointer(true)},
		{Name: "Relative", Email: "relative@example.com", RelationshipContext: "   ", SpokeWithPerson: boolPointer(true)},
	}
	for _, request := range tests {
		_, err := fixture.service.Submit(context.Background(), fixture.requester, request)
		assert.ErrorIs(t, err, ErrInvalidSuggestion)
	}
	var count int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM invitation_suggestions`).Scan(context.Background(), &count))
	assert.Zero(t, count)
}
