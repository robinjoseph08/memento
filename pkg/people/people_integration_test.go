//go:build integration

package people

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/binder"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type peopleFixture struct {
	db      *bun.DB
	service *Service
	actor   setup.CuratorSession
}

type queryCounter struct {
	count atomic.Int64
}

func (counter *queryCounter) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context {
	counter.count.Add(1)
	return ctx
}

func (*queryCounter) AfterQuery(context.Context, *bun.QueryEvent) {}

func newPeopleFixture(t *testing.T) peopleFixture {
	t.Helper()
	ctx := context.Background()
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(ctx, db))
	curatorID := addPerson(t, db, "Curator", "Curator")
	_, err := db.NewRaw(`INSERT INTO person_roles (person_id, role) VALUES (?, 'curator'), (?, 'recipient')`, curatorID, curatorID).Exec(ctx)
	require.NoError(t, err)
	accessID := addAccess(t, db, curatorID, true, "curator@example.com")
	sessionID := addSession(t, db, curatorID, accessID)
	return peopleFixture{db: db, service: New(db), actor: setup.CuratorSession{PersonID: curatorID, SessionID: sessionID}}
}

func addPerson(t *testing.T, db *bun.DB, displayName, sortName string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := db.NewRaw(`INSERT INTO people (id, display_name, sort_name) VALUES (?, ?, ?)`, id, displayName, sortName).Exec(context.Background())
	require.NoError(t, err)
	return id
}

func addAccess(t *testing.T, db *bun.DB, personID uuid.UUID, current bool, email string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	accessID := uuid.New()
	now := time.Now().UTC()
	var generation int
	require.NoError(t, db.NewRaw(`SELECT COALESCE(max(generation), 0) + 1 FROM recipient_access_generations WHERE person_id = ?`, personID).Scan(ctx, &generation))
	_, err := db.NewRaw(`INSERT INTO recipient_access_generations (id, person_id, generation, state, is_current, onboarding_completed_at, created_at, updated_at) VALUES (?, ?, ?, 'completed', ?, ?, ?, ?)`, accessID, personID, generation, current, now, now, now).Exec(ctx)
	require.NoError(t, err)
	_, err = db.NewRaw(`INSERT INTO recipient_emails (id, recipient_access_generation_id, email, normalized_email) VALUES (?, ?, ?, lower(?))`, uuid.New(), accessID, email, email).Exec(ctx)
	require.NoError(t, err)
	_, err = db.NewRaw(`INSERT INTO person_roles (person_id, role) VALUES (?, 'recipient') ON CONFLICT DO NOTHING`, personID).Exec(ctx)
	require.NoError(t, err)
	return accessID
}

func addSession(t *testing.T, db *bun.DB, personID, accessID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	randomCredential := uuid.New()
	hash := sha256.Sum256(randomCredential[:])
	var epoch []byte
	require.NoError(t, db.NewRaw(`SELECT security_epoch FROM system_settings WHERE id = 1`).Scan(ctx, &epoch))
	now := time.Now().UTC()
	_, err := db.NewRaw(`INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, created_at, last_activity_at, idle_expires_at) VALUES (?, ?, ?, ?, ?, 'trusted', ?, ?, ?)`, id, hash[:], personID, accessID, epoch, now, now, now.Add(time.Hour)).Exec(ctx)
	require.NoError(t, err)
	return id
}

func addBrowserSession(t *testing.T, db *bun.DB, personID, accessID uuid.UUID, seed string) string {
	t.Helper()
	ctx := context.Background()
	raw := sha256.Sum256([]byte(seed))
	hash := sha256.Sum256(raw[:])
	var epoch []byte
	require.NoError(t, db.NewRaw(`SELECT security_epoch FROM system_settings WHERE id = 1`).Scan(ctx, &epoch))
	now := time.Now().UTC()
	_, err := db.NewRaw(`INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, created_at, last_activity_at, idle_expires_at) VALUES (?, ?, ?, ?, ?, 'trusted', ?, ?, ?)`, uuid.New(), hash[:], personID, accessID, epoch, now, now, now.Add(time.Hour)).Exec(ctx)
	require.NoError(t, err)
	return hex.EncodeToString(raw[:])
}

func newPeopleRouter(t *testing.T, fixture peopleFixture, auth *setup.Service) *echo.Echo {
	t.Helper()
	e := echo.New()
	requestBinder, err := binder.New()
	require.NoError(t, err)
	e.Binder = requestBinder
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	RegisterRoutes(e, NewHandler(fixture.service, auth))
	return e
}

func servePeople(e *echo.Echo, method, path, credential, csrf string, body any) *httptest.ResponseRecorder {
	var encoded []byte
	if body != nil {
		encoded, _ = json.Marshal(body)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	if body != nil {
		request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	if credential != "" {
		request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: credential})
	}
	if csrf != "" {
		request.Header.Set(setup.CSRFHeader, csrf)
	}
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	return response
}

func TestPeopleSearchNormalizesCaseAccentsAndWhitespace(t *testing.T) {
	fixture := newPeopleFixture(t)
	ctx := context.Background()
	_, err := fixture.service.Create(ctx, fixture.actor, CreateRequest{DisplayName: "  José   Álvarez  ", SortName: "Álvarez, José"})
	require.NoError(t, err)
	archived, err := fixture.service.Create(ctx, fixture.actor, CreateRequest{DisplayName: "Joëlle Archived"})
	require.NoError(t, err)
	archivedID := uuid.MustParse(archived.ID)
	_, err = fixture.service.Archive(ctx, fixture.actor, archivedID, archived.Version)
	require.NoError(t, err)

	result, err := fixture.service.List(ctx, "  JOSE alvarez ", false)
	require.NoError(t, err)
	require.Len(t, result.People, 1)
	assert.Equal(t, "José   Álvarez", result.People[0].DisplayName)

	result, err = fixture.service.List(ctx, "joelle", false)
	require.NoError(t, err)
	assert.Empty(t, result.People)
	result, err = fixture.service.List(ctx, "joelle", true)
	require.NoError(t, err)
	require.Len(t, result.People, 1)
	assert.Equal(t, "archived", result.People[0].Status)

	_, err = fixture.service.Create(ctx, fixture.actor, CreateRequest{DisplayName: "100% Person"})
	require.NoError(t, err)
	result, err = fixture.service.List(ctx, "%", false)
	require.NoError(t, err)
	require.Len(t, result.People, 1)
	assert.Equal(t, "100% Person", result.People[0].DisplayName)
	result, err = fixture.service.List(ctx, "_", false)
	require.NoError(t, err)
	assert.Empty(t, result.People)
}

func TestPeopleListUsesBoundedQueries(t *testing.T) {
	fixture := newPeopleFixture(t)
	ctx := context.Background()
	for i := range 20 {
		addPerson(t, fixture.db, fmt.Sprintf("Person %02d", i), fmt.Sprintf("Person %02d", i))
	}
	counter := new(queryCounter)
	fixture.db.AddQueryHook(counter)
	result, err := fixture.service.List(ctx, "", false)
	require.NoError(t, err)
	assert.Len(t, result.People, 21)
	assert.LessOrEqual(t, counter.count.Load(), int64(7))
}

func TestPeopleRoutesEnforceCuratorCSRFAndPreserveMergeDirection(t *testing.T) {
	fixture := newPeopleFixture(t)
	ctx := context.Background()
	_, err := fixture.db.NewRaw(`UPDATE system_settings SET setup_complete = true WHERE id = 1`).Exec(ctx)
	require.NoError(t, err)
	var curatorAccess uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT id FROM recipient_access_generations WHERE person_id = ? AND is_current`, fixture.actor.PersonID).Scan(ctx, &curatorAccess))
	curatorCredential := addBrowserSession(t, fixture.db, fixture.actor.PersonID, curatorAccess, "curator-browser-session")
	nonCurator := addPerson(t, fixture.db, "Recipient", "Recipient")
	nonCuratorAccess := addAccess(t, fixture.db, nonCurator, true, "recipient@example.com")
	nonCuratorCredential := addBrowserSession(t, fixture.db, nonCurator, nonCuratorAccess, "recipient-browser-session")
	auth := setup.New(fixture.db, nil, config.SecurityConfig{Secret: "people-route-test-secret-32-bytes"})
	curatorSession, err := auth.Session(ctx, curatorCredential)
	require.NoError(t, err)
	e := newPeopleRouter(t, fixture, auth)

	assert.Equal(t, http.StatusNotFound, servePeople(e, http.MethodGet, "/api/people", nonCuratorCredential, "", nil).Code)
	assert.Equal(t, http.StatusOK, servePeople(e, http.MethodGet, "/api/people", curatorCredential, "", nil).Code)

	mutationPaths := []string{
		"/api/people",
		"/api/people/" + nonCurator.String(),
		"/api/people/" + nonCurator.String() + "/archive",
		"/api/people/merge-preview",
		"/api/people/merge",
	}
	mutationMethods := []string{http.MethodPost, http.MethodPatch, http.MethodPost, http.MethodPost, http.MethodPost}
	for i, path := range mutationPaths {
		assert.Equal(t, http.StatusForbidden, servePeople(e, mutationMethods[i], path, curatorCredential, "", nil).Code, path+" missing CSRF")
		assert.Equal(t, http.StatusForbidden, servePeople(e, mutationMethods[i], path, curatorCredential, "invalid", nil).Code, path+" invalid CSRF")
		assert.Equal(t, http.StatusUnsupportedMediaType, servePeople(e, mutationMethods[i], path, curatorCredential, curatorSession.CSRFToken, nil).Code, path+" valid CSRF reaches binding")
	}

	source := addPerson(t, fixture.db, "Duplicate", "Duplicate, Source")
	survivor := addPerson(t, fixture.db, "Duplicate", "Duplicate, Survivor")
	response := servePeople(e, http.MethodPost, "/api/people/merge-preview", curatorCredential, curatorSession.CSRFToken, MergePreviewRequest{
		SourcePersonID: source.String(), SurvivorPersonID: survivor.String(),
	})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var preview MergePreview
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &preview))
	assert.Equal(t, source.String(), preview.Source.ID)
	assert.Equal(t, survivor.String(), preview.Survivor.ID)
}

func TestPeopleUpdatesRejectStaleVersionsWithoutLostUpdates(t *testing.T) {
	fixture := newPeopleFixture(t)
	ctx := context.Background()
	person, err := fixture.service.Create(ctx, fixture.actor, CreateRequest{DisplayName: "Original"})
	require.NoError(t, err)
	id := uuid.MustParse(person.ID)
	updated, err := fixture.service.Update(ctx, fixture.actor, id, UpdateRequest{DisplayName: "First edit", SortName: "First edit", Version: person.Version})
	require.NoError(t, err)
	_, err = fixture.service.Update(ctx, fixture.actor, id, UpdateRequest{DisplayName: "Stale edit", SortName: "Stale edit", Version: person.Version})
	require.ErrorIs(t, err, ErrStale)
	stored, err := fixture.service.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, updated.DisplayName, stored.DisplayName)
	assert.Equal(t, int64(2), stored.Version)
}

func TestMergeRejectsStaleSourceAndSurvivorVersionsWithoutEffects(t *testing.T) {
	for _, staleSide := range []string{"source", "survivor"} {
		t.Run(staleSide, func(t *testing.T) {
			fixture := newPeopleFixture(t)
			ctx := context.Background()
			source := addPerson(t, fixture.db, "Source", "Source")
			survivor := addPerson(t, fixture.db, "Survivor", "Survivor")
			preview, err := fixture.service.PreviewMerge(ctx, fixture.actor, source, survivor)
			require.NoError(t, err)
			staleID := source
			if staleSide == "survivor" {
				staleID = survivor
			}
			_, err = fixture.service.Update(ctx, fixture.actor, staleID, UpdateRequest{DisplayName: "Changed", SortName: "Changed", Version: 1})
			require.NoError(t, err)

			_, err = fixture.service.Merge(ctx, fixture.actor, MergeRequest{
				SourcePersonID: source.String(), SurvivorPersonID: survivor.String(), SourceVersion: 1, SurvivorVersion: 1,
				PreviewFingerprint: preview.PreviewFingerprint,
			})
			require.ErrorIs(t, err, ErrStale)
			storedSource, getErr := fixture.service.Get(ctx, source)
			require.NoError(t, getErr)
			storedSurvivor, getErr := fixture.service.Get(ctx, survivor)
			require.NoError(t, getErr)
			assert.Equal(t, "current", storedSource.Status)
			assert.Equal(t, "current", storedSurvivor.Status)
			var mergeAudits int
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM security_audit_events WHERE action = 'people_merged'`).Scan(ctx, &mergeAudits))
			assert.Zero(t, mergeAudits)
		})
	}
}

func TestMergeIntoCuratorPreservesCurrentSessionAndAuthority(t *testing.T) {
	fixture := newPeopleFixture(t)
	ctx := context.Background()
	source := addPerson(t, fixture.db, "Duplicate Curator", "Curator, Duplicate")

	preview, err := fixture.service.PreviewMerge(ctx, fixture.actor, source, fixture.actor.PersonID)
	require.NoError(t, err)
	assert.True(t, preview.CanMerge)
	assert.True(t, preview.CurrentCuratorSessionKept)
	assert.Zero(t, preview.References.SessionsInvalidated)
	merged, err := fixture.service.Merge(ctx, fixture.actor, MergeRequest{
		SourcePersonID: source.String(), SurvivorPersonID: fixture.actor.PersonID.String(), SourceVersion: 1, SurvivorVersion: 1,
		PreviewFingerprint: preview.PreviewFingerprint,
	})
	require.NoError(t, err)
	assert.Equal(t, fixture.actor.PersonID.String(), merged.ID)
	assert.Equal(t, []string{"curator", "recipient"}, merged.Roles)
	var revoked bool
	require.NoError(t, fixture.db.NewRaw(`SELECT revoked_at IS NOT NULL FROM sessions WHERE id = ?`, fixture.actor.SessionID).Scan(ctx, &revoked))
	assert.False(t, revoked)
}

func TestMergePreviewAndConfirmationEnforceAuthorityAndGenerationGates(t *testing.T) {
	fixture := newPeopleFixture(t)
	ctx := context.Background()
	source := addPerson(t, fixture.db, "Source", "Source")
	survivor := addPerson(t, fixture.db, "Survivor", "Survivor")
	addAccess(t, fixture.db, source, true, "source@example.com")
	addAccess(t, fixture.db, survivor, true, "survivor@example.com")
	preview, err := fixture.service.PreviewMerge(ctx, fixture.actor, source, survivor)
	require.NoError(t, err)
	assert.False(t, preview.CanMerge)
	assert.Contains(t, preview.Blockers, "Resolve one current Recipient access generation before merging.")
	_, err = fixture.service.Merge(ctx, fixture.actor, MergeRequest{SourcePersonID: source.String(), SurvivorPersonID: survivor.String(), SourceVersion: 1, SurvivorVersion: 1, TransferCurrentAccessGeneration: true, PreviewFingerprint: preview.PreviewFingerprint})
	require.ErrorIs(t, err, ErrTwoCurrentGenerations)

	curatorPreview, err := fixture.service.PreviewMerge(ctx, fixture.actor, fixture.actor.PersonID, survivor)
	require.NoError(t, err)
	assert.False(t, curatorPreview.CanMerge)
	assert.Contains(t, curatorPreview.Blockers, "The Curator Person must be the survivor.")
	_, err = fixture.service.Merge(ctx, fixture.actor, MergeRequest{SourcePersonID: fixture.actor.PersonID.String(), SurvivorPersonID: survivor.String(), SourceVersion: 1, SurvivorVersion: 1, PreviewFingerprint: curatorPreview.PreviewFingerprint})
	require.ErrorIs(t, err, ErrCuratorMustSurvive)
}

func TestMergeRejectsChangedResultingGenerationAfterPreview(t *testing.T) {
	fixture := newPeopleFixture(t)
	ctx := context.Background()
	source := addPerson(t, fixture.db, "Source", "Source")
	survivor := addPerson(t, fixture.db, "Survivor", "Survivor")
	sourceAccess := addAccess(t, fixture.db, source, true, "source@example.com")
	preview, err := fixture.service.PreviewMerge(ctx, fixture.actor, source, survivor)
	require.NoError(t, err)
	assert.Equal(t, 1, preview.References.ResultingRecipientGeneration)
	addAccess(t, fixture.db, survivor, false, "historical@example.com")

	_, err = fixture.service.Merge(ctx, fixture.actor, MergeRequest{
		SourcePersonID: source.String(), SurvivorPersonID: survivor.String(), SourceVersion: 1, SurvivorVersion: 1,
		TransferCurrentAccessGeneration: true, ExpectedRecipientGeneration: preview.References.ResultingRecipientGeneration,
		PreviewFingerprint: preview.PreviewFingerprint,
	})
	require.ErrorIs(t, err, ErrStale)
	var generationPerson uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT person_id FROM recipient_access_generations WHERE id = ?`, sourceAccess).Scan(ctx, &generationPerson))
	assert.Equal(t, source, generationPerson)
}

func TestMergeRejectsReferenceChangesAfterPreview(t *testing.T) {
	for _, changedReference := range []string{"email", "current generation"} {
		t.Run(changedReference, func(t *testing.T) {
			fixture := newPeopleFixture(t)
			ctx := context.Background()
			source := addPerson(t, fixture.db, "Source", "Source")
			survivor := addPerson(t, fixture.db, "Survivor", "Survivor")
			sourceAccess := addAccess(t, fixture.db, source, true, "source@example.com")
			preview, err := fixture.service.PreviewMerge(ctx, fixture.actor, source, survivor)
			require.NoError(t, err)
			if changedReference == "email" {
				_, err = fixture.db.NewRaw(`UPDATE recipient_emails SET email = 'changed@example.com', normalized_email = 'changed@example.com' WHERE recipient_access_generation_id = ? AND is_current`, sourceAccess).Exec(ctx)
				require.NoError(t, err)
			} else {
				_, err = fixture.db.NewRaw(`UPDATE recipient_access_generations SET is_current = false WHERE id = ?`, sourceAccess).Exec(ctx)
				require.NoError(t, err)
				sourceAccess = addAccess(t, fixture.db, source, true, "replacement@example.com")
			}

			_, err = fixture.service.Merge(ctx, fixture.actor, MergeRequest{
				SourcePersonID: source.String(), SurvivorPersonID: survivor.String(), SourceVersion: 1, SurvivorVersion: 1,
				TransferCurrentAccessGeneration: true, ExpectedRecipientGeneration: preview.References.ResultingRecipientGeneration,
				PreviewFingerprint: preview.PreviewFingerprint,
			})
			require.ErrorIs(t, err, ErrStale)
			var generationPerson uuid.UUID
			require.NoError(t, fixture.db.NewRaw(`SELECT person_id FROM recipient_access_generations WHERE id = ?`, sourceAccess).Scan(ctx, &generationPerson))
			assert.Equal(t, source, generationPerson)
			var mergeAudits int
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM security_audit_events WHERE action = 'people_merged'`).Scan(ctx, &mergeAudits))
			assert.Zero(t, mergeAudits)
		})
	}
}

func TestMergeRejectsArchivedSurvivorBeforeTransferringAccess(t *testing.T) {
	fixture := newPeopleFixture(t)
	ctx := context.Background()
	source := addPerson(t, fixture.db, "Source", "Source")
	survivor := addPerson(t, fixture.db, "Archived survivor", "Archived survivor")
	sourceAccess := addAccess(t, fixture.db, source, true, "source@example.com")
	archivedSurvivor, err := fixture.service.Archive(ctx, fixture.actor, survivor, 1)
	require.NoError(t, err)

	preview, err := fixture.service.PreviewMerge(ctx, fixture.actor, source, survivor)
	require.NoError(t, err)
	assert.False(t, preview.CanMerge)
	assert.Contains(t, preview.Blockers, "The survivor Person must be current.")
	assert.True(t, preview.References.RecipientRoleWillTransfer)

	_, err = fixture.service.Merge(ctx, fixture.actor, MergeRequest{
		SourcePersonID: source.String(), SurvivorPersonID: survivor.String(),
		SourceVersion: 1, SurvivorVersion: archivedSurvivor.Version, TransferCurrentAccessGeneration: true,
		PreviewFingerprint: preview.PreviewFingerprint,
	})
	require.ErrorIs(t, err, ErrSurvivorMustBeCurrent)
	var generationPerson uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT person_id FROM recipient_access_generations WHERE id = ?`, sourceAccess).Scan(ctx, &generationPerson))
	assert.Equal(t, source, generationPerson)
}

func TestMergeExplicitlyTransfersGenerationResolvesEmailInvalidatesSessionsAndPreservesAttribution(t *testing.T) {
	fixture := newPeopleFixture(t)
	ctx := context.Background()
	source := addPerson(t, fixture.db, "Duplicate Robin", "Robin, Duplicate")
	survivor := addPerson(t, fixture.db, "Robin", "Robin")
	sourceHistoricalAccess := addAccess(t, fixture.db, source, false, "historical@example.com")
	sourceAccess := addAccess(t, fixture.db, source, true, "duplicate@example.com")
	survivorOldAccess := addAccess(t, fixture.db, survivor, false, "robin@example.com")
	sourceSession := addSession(t, fixture.db, source, sourceAccess)
	survivorSession := addSession(t, fixture.db, survivor, survivorOldAccess)
	_, err := fixture.db.NewRaw(`INSERT INTO security_audit_events (actor_person_id, subject_person_id, action, outcome, session_id) VALUES
		(?, ?, 'source_history', 'success', ?),
		(?, ?, 'survivor_history', 'success', ?),
		(?, ?, 'shared_history', 'success', ?)`,
		fixture.actor.PersonID, source, fixture.actor.SessionID,
		fixture.actor.PersonID, survivor, fixture.actor.SessionID,
		source, survivor, fixture.actor.SessionID).Exec(ctx)
	require.NoError(t, err)

	preview, err := fixture.service.PreviewMerge(ctx, fixture.actor, source, survivor)
	require.NoError(t, err)
	assert.True(t, preview.CanMerge)
	assert.True(t, preview.RequiresGenerationTransfer)
	assert.True(t, preview.RequiresEmailResolution)
	assert.Equal(t, sourceAccess.String(), preview.References.CurrentRecipientGenerationID)
	assert.Equal(t, 2, preview.References.SessionsInvalidated)
	assert.Equal(t, 3, preview.References.HistoricalAuditRowsPreserved)
	assert.Equal(t, []string{"recipient"}, preview.References.SourceRoles)
	assert.Equal(t, []string{"recipient"}, preview.References.SurvivorRoles)
	assert.True(t, preview.References.RecipientRoleWillTransfer)
	assert.True(t, preview.RolesWillNotBeUnioned)
	assert.True(t, preview.AudienceAuthorityUnchanged)

	_, err = fixture.service.Merge(ctx, fixture.actor, MergeRequest{SourcePersonID: source.String(), SurvivorPersonID: survivor.String(), SourceVersion: 1, SurvivorVersion: 1, PreviewFingerprint: preview.PreviewFingerprint})
	require.ErrorIs(t, err, ErrGenerationTransferNeeded)
	_, err = fixture.service.Merge(ctx, fixture.actor, MergeRequest{SourcePersonID: source.String(), SurvivorPersonID: survivor.String(), SourceVersion: 1, SurvivorVersion: 1, TransferCurrentAccessGeneration: true, ExpectedRecipientGeneration: preview.References.ResultingRecipientGeneration, PreviewFingerprint: preview.PreviewFingerprint})
	require.ErrorIs(t, err, ErrEmailResolutionNeeded)

	merged, err := fixture.service.Merge(ctx, fixture.actor, MergeRequest{SourcePersonID: source.String(), SurvivorPersonID: survivor.String(), SourceVersion: 1, SurvivorVersion: 1, TransferCurrentAccessGeneration: true, ExpectedRecipientGeneration: preview.References.ResultingRecipientGeneration, PreviewFingerprint: preview.PreviewFingerprint, EmailResolution: "keep_survivor"})
	require.NoError(t, err)
	assert.Equal(t, survivor.String(), merged.ID)
	assert.Equal(t, int64(2), merged.Version)
	assert.Equal(t, "robin@example.com", merged.CurrentLoginEmail)
	var generationPerson uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT person_id FROM recipient_access_generations WHERE id = ?`, sourceAccess).Scan(ctx, &generationPerson))
	assert.Equal(t, survivor, generationPerson)
	require.NoError(t, fixture.db.NewRaw(`SELECT person_id FROM recipient_access_generations WHERE id = ?`, sourceHistoricalAccess).Scan(ctx, &generationPerson))
	assert.Equal(t, source, generationPerson)
	var activeSessions int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM sessions WHERE id IN (?, ?) AND revoked_at IS NULL`, sourceSession, survivorSession).Scan(ctx, &activeSessions))
	assert.Zero(t, activeSessions)
	storedSource, err := fixture.service.Get(ctx, source)
	require.NoError(t, err)
	assert.Equal(t, "merged", storedSource.Status)
	assert.Equal(t, survivor.String(), storedSource.MergedIntoPersonID)
	assert.Empty(t, storedSource.Roles)
	assert.Equal(t, []string{"recipient"}, merged.Roles)
	assert.NotZero(t, storedSource.HistoricalAuditCount)
	var sourceHistorySubject, survivorHistorySubject uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT subject_person_id FROM security_audit_events WHERE action = 'source_history'`).Scan(ctx, &sourceHistorySubject))
	require.NoError(t, fixture.db.NewRaw(`SELECT subject_person_id FROM security_audit_events WHERE action = 'survivor_history'`).Scan(ctx, &survivorHistorySubject))
	assert.Equal(t, source, sourceHistorySubject)
	assert.Equal(t, survivor, survivorHistorySubject)
	var sharedHistoryActor, sharedHistorySubject uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT actor_person_id, subject_person_id FROM security_audit_events WHERE action = 'shared_history'`).Scan(ctx, &sharedHistoryActor, &sharedHistorySubject))
	assert.Equal(t, source, sharedHistoryActor)
	assert.Equal(t, survivor, sharedHistorySubject)
	var mergeAuditMetadata string
	require.NoError(t, fixture.db.NewRaw(`SELECT metadata::text FROM security_audit_events WHERE action = 'people_merged'`).Scan(ctx, &mergeAuditMetadata))
	assert.Contains(t, mergeAuditMetadata, survivor.String())
}

func TestMergeEmailResolutionCanKeepSourceEmail(t *testing.T) {
	fixture := newPeopleFixture(t)
	ctx := context.Background()
	source := addPerson(t, fixture.db, "Source email", "Source email")
	survivor := addPerson(t, fixture.db, "Survivor email", "Survivor email")
	sourceAccess := addAccess(t, fixture.db, source, true, "source-kept@example.com")
	addAccess(t, fixture.db, survivor, false, "survivor-ended@example.com")
	preview, err := fixture.service.PreviewMerge(ctx, fixture.actor, source, survivor)
	require.NoError(t, err)

	merged, err := fixture.service.Merge(ctx, fixture.actor, MergeRequest{
		SourcePersonID: source.String(), SurvivorPersonID: survivor.String(),
		SourceVersion: 1, SurvivorVersion: 1, TransferCurrentAccessGeneration: true,
		ExpectedRecipientGeneration: preview.References.ResultingRecipientGeneration,
		PreviewFingerprint:          preview.PreviewFingerprint, EmailResolution: "keep_source",
	})
	require.NoError(t, err)
	assert.Equal(t, "source-kept@example.com", merged.CurrentLoginEmail)
	var currentEmails int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM recipient_emails WHERE is_current AND recipient_access_generation_id <> ?`, sourceAccess).Scan(ctx, &currentEmails))
	assert.Equal(t, 1, currentEmails, "only the unrelated Curator email remains current outside the transferred generation")
}

func TestMergeLateAuditFailureRollsBackEveryEffect(t *testing.T) {
	fixture := newPeopleFixture(t)
	ctx := context.Background()
	source := addPerson(t, fixture.db, "Rollback source", "Rollback source")
	survivor := addPerson(t, fixture.db, "Rollback survivor", "Rollback survivor")
	sourceAccess := addAccess(t, fixture.db, source, true, "rollback@example.com")
	sessionID := addSession(t, fixture.db, source, sourceAccess)
	preview, err := fixture.service.PreviewMerge(ctx, fixture.actor, source, survivor)
	require.NoError(t, err)
	_, err = fixture.db.ExecContext(ctx, `CREATE FUNCTION reject_people_merge_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action = 'people_merged' THEN RAISE EXCEPTION 'late merge failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_people_merge_audit BEFORE INSERT ON security_audit_events FOR EACH ROW EXECUTE FUNCTION reject_people_merge_audit()`)
	require.NoError(t, err)

	_, err = fixture.service.Merge(ctx, fixture.actor, MergeRequest{SourcePersonID: source.String(), SurvivorPersonID: survivor.String(), SourceVersion: 1, SurvivorVersion: 1, TransferCurrentAccessGeneration: true, ExpectedRecipientGeneration: preview.References.ResultingRecipientGeneration, PreviewFingerprint: preview.PreviewFingerprint})
	require.ErrorContains(t, err, "late merge failure")
	storedSource, getErr := fixture.service.Get(ctx, source)
	require.NoError(t, getErr)
	assert.Equal(t, "current", storedSource.Status)
	assert.Equal(t, int64(1), storedSource.Version)
	var generationPerson uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT person_id FROM recipient_access_generations WHERE id = ?`, sourceAccess).Scan(ctx, &generationPerson))
	assert.Equal(t, source, generationPerson)
	var revoked bool
	require.NoError(t, fixture.db.NewRaw(`SELECT revoked_at IS NOT NULL FROM sessions WHERE id = ?`, sessionID).Scan(ctx, &revoked))
	assert.False(t, revoked)
}
