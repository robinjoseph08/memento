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
	familypkg "github.com/robinjoseph08/memento/pkg/family"
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

func addInvitation(t *testing.T, db *bun.DB, accessID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var emailID uuid.UUID
	require.NoError(t, db.NewRaw(`SELECT id FROM recipient_emails WHERE recipient_access_generation_id = ? AND is_current`, accessID).Scan(ctx, &emailID))
	invitationID := uuid.New()
	now := time.Now().UTC()
	token := sha256.Sum256(invitationID[:])
	_, err := db.NewRaw(`
		INSERT INTO invitations (id, recipient_access_generation_id, recipient_email_id, token_hash, issued_at, expires_at, automatic_reminder_scheduled_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, invitationID, accessID, emailID, token[:], now, now.Add(14*24*time.Hour), now.Add(7*24*time.Hour)).Exec(ctx)
	require.NoError(t, err)
	return invitationID
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
	request.Header.Set("User-Agent", "memento-integration-test")
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
	child := addPerson(t, fixture.db, "Newly related child", "Newly related child")
	_, err = fixture.db.NewRaw(`INSERT INTO family_relationships (id, relationship_type, person_a_id, person_b_id) VALUES (?, 'parent_child', ?, ?)`, uuid.New(), source, child).Exec(ctx)
	require.NoError(t, err)
	staleMerge := servePeople(e, http.MethodPost, "/api/people/merge", curatorCredential, curatorSession.CSRFToken, MergeRequest{
		SourcePersonID: source.String(), SurvivorPersonID: survivor.String(), SourceVersion: preview.Source.Version, SurvivorVersion: preview.Survivor.Version,
		PreviewFingerprint: preview.PreviewFingerprint,
	})
	assert.Equal(t, http.StatusConflict, staleMerge.Code)
	assert.Contains(t, staleMerge.Body.String(), "A Person or affected reference changed")
}

func TestFamilyRoutesEnforceCuratorAuthorizationCSRFAndErrorMapping(t *testing.T) {
	fixture := newPeopleFixture(t)
	ctx := context.Background()
	_, err := fixture.db.NewRaw(`UPDATE system_settings SET setup_complete = true WHERE id = 1`).Exec(ctx)
	require.NoError(t, err)
	var curatorAccess uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT id FROM recipient_access_generations WHERE person_id = ? AND is_current`, fixture.actor.PersonID).Scan(ctx, &curatorAccess))
	curatorCredential := addBrowserSession(t, fixture.db, fixture.actor.PersonID, curatorAccess, "family-curator-browser-session")
	nonCurator := addPerson(t, fixture.db, "Recipient", "Recipient")
	nonCuratorAccess := addAccess(t, fixture.db, nonCurator, true, "family-recipient@example.com")
	nonCuratorCredential := addBrowserSession(t, fixture.db, nonCurator, nonCuratorAccess, "family-recipient-browser-session")
	auth := setup.New(fixture.db, nil, config.SecurityConfig{Secret: "family-route-test-secret-32-bytes"})
	curatorSession, err := auth.Session(ctx, curatorCredential)
	require.NoError(t, err)
	e := echo.New()
	requestBinder, err := binder.New()
	require.NoError(t, err)
	e.Binder = requestBinder
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	familypkg.RegisterRoutes(e, familypkg.NewHandler(familypkg.New(fixture.db), auth))

	unauthorized := servePeople(e, http.MethodGet, "/api/relationships", "", "", nil)
	assert.Equal(t, http.StatusUnauthorized, unauthorized.Code)
	assert.Equal(t, "no-store", unauthorized.Header().Get(echo.HeaderCacheControl))
	assert.Equal(t, http.StatusNotFound, servePeople(e, http.MethodGet, "/api/relationships", nonCuratorCredential, "", nil).Code)
	request := familypkg.MutationRequest{RelationshipType: "parent_child", PersonAID: fixture.actor.PersonID.String(), PersonBID: nonCurator.String()}
	assert.Equal(t, http.StatusForbidden, servePeople(e, http.MethodPost, "/api/relationships", curatorCredential, "", request).Code)
	assert.Equal(t, http.StatusForbidden, servePeople(e, http.MethodPost, "/api/relationships", curatorCredential, "invalid", request).Code)
	created := servePeople(e, http.MethodPost, "/api/relationships", curatorCredential, curatorSession.CSRFToken, request)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	cycle := servePeople(e, http.MethodPost, "/api/relationships", curatorCredential, curatorSession.CSRFToken, familypkg.MutationRequest{RelationshipType: "parent_child", PersonAID: nonCurator.String(), PersonBID: fixture.actor.PersonID.String()})
	assert.Equal(t, http.StatusConflict, cycle.Code)
	assert.Contains(t, cycle.Body.String(), "would create a cycle")
	var clientIP, userAgent string
	require.NoError(t, fixture.db.NewRaw(`SELECT client_ip::text, user_agent FROM security_audit_events WHERE action = 'family_relationship_created'`).Scan(ctx, &clientIP, &userAgent))
	assert.Equal(t, "192.0.2.1/32", clientIP)
	assert.Equal(t, "memento-integration-test", userAgent)
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

func TestArchiveAndMergeUseOnePersonRepairLockOrder(t *testing.T) {
	fixture := newPeopleFixture(t)
	ctx := context.Background()
	source := addPerson(t, fixture.db, "Source", "Source")
	survivor := addPerson(t, fixture.db, "Survivor", "Survivor")
	candidateID := uuid.New()
	_, err := fixture.db.ExecContext(ctx, `
		INSERT INTO person_repair_candidates (
			id, person_id, previous_immich_person_id, candidate_immich_person_id, created_at
		) VALUES (?, ?, ?, ?, now())
	`, candidateID, source, uuid.New(), uuid.New())
	require.NoError(t, err)
	preview, err := fixture.service.PreviewMerge(ctx, fixture.actor, source, survivor)
	require.NoError(t, err)

	blocker, err := fixture.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = blocker.NewRaw(`SELECT id FROM person_repair_candidates WHERE id = ? FOR UPDATE`, candidateID).Exec(ctx)
	require.NoError(t, err)
	archiveResult := make(chan error, 1)
	go func() {
		_, archiveErr := fixture.service.Archive(ctx, fixture.actor, source, 1)
		archiveResult <- archiveErr
	}()
	select {
	case archiveErr := <-archiveResult:
		t.Fatalf("archive completed while its repair candidate was locked: %v", archiveErr)
	case <-time.After(100 * time.Millisecond):
	}
	mergeResult := make(chan error, 1)
	go func() {
		_, mergeErr := fixture.service.Merge(ctx, fixture.actor, MergeRequest{
			SourcePersonID: source.String(), SurvivorPersonID: survivor.String(), SourceVersion: 1, SurvivorVersion: 1,
			PreviewFingerprint: preview.PreviewFingerprint,
		})
		mergeResult <- mergeErr
	}()
	select {
	case mergeErr := <-mergeResult:
		t.Fatalf("merge completed while archive held the Person lock: %v", mergeErr)
	case <-time.After(100 * time.Millisecond):
	}
	require.NoError(t, blocker.Commit())
	select {
	case archiveErr := <-archiveResult:
		require.NoError(t, archiveErr)
	case <-time.After(3 * time.Second):
		t.Fatal("archive did not complete after candidate lock release")
	}
	select {
	case mergeErr := <-mergeResult:
		assert.ErrorIs(t, mergeErr, ErrMergeStale)
	case <-time.After(3 * time.Second):
		t.Fatal("merge did not complete after archive")
	}
}

func TestMergeIntoCuratorPreservesCurrentSessionAndAuthority(t *testing.T) {
	fixture := newPeopleFixture(t)
	ctx := context.Background()
	source := addPerson(t, fixture.db, "Duplicate Curator", "Curator, Duplicate")
	immichPersonID, candidateID := uuid.New(), uuid.New()
	_, err := fixture.db.ExecContext(ctx, `
		INSERT INTO immich_people_inventory (immich_person_id, name, first_seen_at, last_seen_at)
		VALUES (?, 'Merged identity', now(), now());
		INSERT INTO immich_person_links (
			person_id, immich_person_id, state, last_seen_at, confirmed_at, confirmed_by_person_id
		) VALUES (?, ?, 'needs_review', now(), now(), ?);
		INSERT INTO person_repair_candidates (
			id, person_id, previous_immich_person_id, candidate_immich_person_id, created_at
		) VALUES (?, ?, ?, ?, now())
	`, immichPersonID, source, immichPersonID, fixture.actor.PersonID,
		candidateID, source, immichPersonID, immichPersonID)
	require.NoError(t, err)

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
	var linkedPersonID uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT person_id FROM immich_person_links WHERE immich_person_id = ?`, immichPersonID).Scan(ctx, &linkedPersonID))
	assert.Equal(t, fixture.actor.PersonID, linkedPersonID)
	var candidateState string
	var resolvedBy uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`
		SELECT state, resolved_by_person_id FROM person_repair_candidates WHERE id = ?
	`, candidateID).Scan(ctx, &candidateState, &resolvedBy))
	assert.Equal(t, "superseded", candidateState)
	assert.Equal(t, fixture.actor.PersonID, resolvedBy)
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

func TestMergeSupersedesInvitationBeforeTransferringAccess(t *testing.T) {
	fixture := newPeopleFixture(t)
	ctx := context.Background()
	source := addPerson(t, fixture.db, "Source", "Source")
	survivor := addPerson(t, fixture.db, "Survivor", "Survivor")
	sourceAccess := addAccess(t, fixture.db, source, true, "source@example.com")
	invitationID := addInvitation(t, fixture.db, sourceAccess)
	preview, err := fixture.service.PreviewMerge(ctx, fixture.actor, source, survivor)
	require.NoError(t, err)
	merged, err := fixture.service.Merge(ctx, fixture.actor, MergeRequest{
		SourcePersonID: source.String(), SurvivorPersonID: survivor.String(), SourceVersion: 1, SurvivorVersion: 1,
		TransferCurrentAccessGeneration: true, ExpectedRecipientGeneration: preview.References.ResultingRecipientGeneration,
		PreviewFingerprint: preview.PreviewFingerprint,
	})
	require.NoError(t, err)
	assert.Equal(t, survivor.String(), merged.ID)
	var superseded bool
	require.NoError(t, fixture.db.NewRaw(`SELECT superseded_at IS NOT NULL FROM invitations WHERE id = ?`, invitationID).Scan(ctx, &superseded))
	assert.True(t, superseded)
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

func TestMergePreviewsAndMovesEveryFamilyRelationshipEndpointAtomically(t *testing.T) {
	fixture := newPeopleFixture(t)
	ctx := context.Background()
	source := addPerson(t, fixture.db, "Family source", "Family source")
	survivor := addPerson(t, fixture.db, "Family survivor", "Family survivor")
	sharedChild := addPerson(t, fixture.db, "Shared child", "Shared child")
	otherChild := addPerson(t, fixture.db, "Other child", "Other child")
	parent := addPerson(t, fixture.db, "Parent", "Parent")
	sibling := addPerson(t, fixture.db, "Sibling", "Sibling")
	partner := addPerson(t, fixture.db, "Partner", "Partner")
	formerPartner := addPerson(t, fixture.db, "Former partner", "Former partner")
	for _, endpoints := range [][2]uuid.UUID{{source, sharedChild}, {survivor, sharedChild}, {source, otherChild}, {parent, source}} {
		_, err := fixture.db.NewRaw(`INSERT INTO family_relationships (id, relationship_type, person_a_id, person_b_id) VALUES (?, 'parent_child', ?, ?)`, uuid.New(), endpoints[0], endpoints[1]).Exec(ctx)
		require.NoError(t, err)
	}
	for _, relationship := range []struct {
		person           uuid.UUID
		relationshipType string
		partnerStatus    any
		archived         bool
	}{
		{person: sibling, relationshipType: "sibling"},
		{person: partner, relationshipType: "partner", partnerStatus: "current"},
		{person: formerPartner, relationshipType: "partner", partnerStatus: "former", archived: true},
	} {
		personA, personB := source, relationship.person
		if bytes.Compare(personA[:], personB[:]) > 0 {
			personA, personB = personB, personA
		}
		_, err := fixture.db.NewRaw(`INSERT INTO family_relationships (id, relationship_type, person_a_id, person_b_id, partner_status, archived_at) VALUES (?, ?, ?, ?, ?, CASE WHEN ? THEN now() END)`, uuid.New(), relationship.relationshipType, personA, personB, relationship.partnerStatus, relationship.archived).Exec(ctx)
		require.NoError(t, err)
	}

	preview, err := fixture.service.PreviewMerge(ctx, fixture.actor, source, survivor)
	require.NoError(t, err)
	assert.True(t, preview.CanMerge)
	assert.Equal(t, 6, preview.References.FamilyRelationshipsMoved)
	assert.Equal(t, 1, preview.References.FamilyRelationshipsArchived)
	assert.Len(t, preview.References.FamilyReferenceFingerprint, 64)
	_, err = fixture.service.Merge(ctx, fixture.actor, MergeRequest{
		SourcePersonID: source.String(), SurvivorPersonID: survivor.String(), SourceVersion: 1, SurvivorVersion: 1,
		PreviewFingerprint: preview.PreviewFingerprint,
	})
	require.NoError(t, err)

	var sourceReferences, survivorActiveReferences, archivedDuplicates int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM family_relationships WHERE person_a_id = ? OR person_b_id = ?`, source, source).Scan(ctx, &sourceReferences))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM family_relationships WHERE (person_a_id = ? OR person_b_id = ?) AND archived_at IS NULL`, survivor, survivor).Scan(ctx, &survivorActiveReferences))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM family_relationships WHERE person_a_id = ? AND person_b_id = ? AND archived_at IS NOT NULL`, survivor, sharedChild).Scan(ctx, &archivedDuplicates))
	assert.Zero(t, sourceReferences)
	assert.Equal(t, 5, survivorActiveReferences)
	assert.Equal(t, 1, archivedDuplicates)
	var auditMetadata string
	require.NoError(t, fixture.db.NewRaw(`SELECT metadata::text FROM security_audit_events WHERE action = 'people_merged'`).Scan(ctx, &auditMetadata))
	assert.Contains(t, auditMetadata, `"family_relationships_moved": 6`)
	assert.Contains(t, auditMetadata, `"family_relationships_archived": 1`)
}

func TestFamilyMutationAndPersonMergeUseOneDeadlockFreeLockOrder(t *testing.T) {
	fixture := newPeopleFixture(t)
	ctx := context.Background()
	source := addPerson(t, fixture.db, "Source", "Source")
	survivor := addPerson(t, fixture.db, "Survivor", "Survivor")
	child := addPerson(t, fixture.db, "Child", "Child")
	preview, err := fixture.service.PreviewMerge(ctx, fixture.actor, source, survivor)
	require.NoError(t, err)
	_, err = fixture.db.ExecContext(ctx, `
		CREATE FUNCTION delay_merge_racing_family_insert() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN PERFORM pg_sleep(0.3); RETURN NEW; END $$;
		CREATE TRIGGER delay_merge_racing_family_insert BEFORE INSERT ON family_relationships
		FOR EACH ROW EXECUTE FUNCTION delay_merge_racing_family_insert()`)
	require.NoError(t, err)

	createResult := make(chan error, 1)
	go func() {
		_, createErr := familypkg.New(fixture.db).Create(ctx, fixture.actor, familypkg.MutationRequest{
			RelationshipType: "parent_child", PersonAID: source.String(), PersonBID: child.String(),
		})
		createResult <- createErr
	}()
	time.Sleep(100 * time.Millisecond)
	_, mergeErr := fixture.service.Merge(ctx, fixture.actor, MergeRequest{
		SourcePersonID: source.String(), SurvivorPersonID: survivor.String(), SourceVersion: 1, SurvivorVersion: 1,
		PreviewFingerprint: preview.PreviewFingerprint,
	})
	require.NoError(t, <-createResult)
	require.ErrorIs(t, mergeErr, ErrStale)
}

func TestMergeRejectsStaleFamilyRelationshipPreview(t *testing.T) {
	fixture := newPeopleFixture(t)
	ctx := context.Background()
	source := addPerson(t, fixture.db, "Source", "Source")
	survivor := addPerson(t, fixture.db, "Survivor", "Survivor")
	firstParent := addPerson(t, fixture.db, "First parent", "First parent")
	secondParent := addPerson(t, fixture.db, "Second parent", "Second parent")
	relationshipID := uuid.New()
	_, err := fixture.db.NewRaw(`INSERT INTO family_relationships (id, relationship_type, person_a_id, person_b_id) VALUES (?, 'parent_child', ?, ?)`, relationshipID, firstParent, source).Exec(ctx)
	require.NoError(t, err)
	preview, err := fixture.service.PreviewMerge(ctx, fixture.actor, source, survivor)
	require.NoError(t, err)
	_, err = fixture.db.NewRaw(`UPDATE family_relationships SET person_a_id = ?, version = version + 1 WHERE id = ?`, secondParent, relationshipID).Exec(ctx)
	require.NoError(t, err)

	_, err = fixture.service.Merge(ctx, fixture.actor, MergeRequest{
		SourcePersonID: source.String(), SurvivorPersonID: survivor.String(), SourceVersion: 1, SurvivorVersion: 1,
		PreviewFingerprint: preview.PreviewFingerprint,
	})
	require.ErrorIs(t, err, ErrMergeStale)
	storedSource, getErr := fixture.service.Get(ctx, source)
	require.NoError(t, getErr)
	assert.Equal(t, "current", storedSource.Status)
}

func TestMergeRejectsConflictingPartnerStatuses(t *testing.T) {
	fixture := newPeopleFixture(t)
	ctx := context.Background()
	source := addPerson(t, fixture.db, "Source", "Source")
	survivor := addPerson(t, fixture.db, "Survivor", "Survivor")
	partner := addPerson(t, fixture.db, "Partner", "Partner")
	for _, relationship := range []struct {
		person uuid.UUID
		status string
	}{
		{person: source, status: "current"},
		{person: survivor, status: "former"},
	} {
		personA, personB := relationship.person, partner
		if bytes.Compare(personA[:], personB[:]) > 0 {
			personA, personB = personB, personA
		}
		_, err := fixture.db.NewRaw(`INSERT INTO family_relationships (id, relationship_type, person_a_id, person_b_id, partner_status) VALUES (?, 'partner', ?, ?, ?)`, uuid.New(), personA, personB, relationship.status).Exec(ctx)
		require.NoError(t, err)
	}

	preview, err := fixture.service.PreviewMerge(ctx, fixture.actor, source, survivor)
	require.NoError(t, err)
	assert.False(t, preview.CanMerge)
	assert.Contains(t, preview.Blockers, "Resolve conflicting current and former partner relationships before merging these People.")
	_, err = fixture.service.Merge(ctx, fixture.actor, MergeRequest{
		SourcePersonID: source.String(), SurvivorPersonID: survivor.String(), SourceVersion: 1, SurvivorVersion: 1,
		PreviewFingerprint: preview.PreviewFingerprint,
	})
	require.ErrorIs(t, err, ErrFamilyPartnerConflict)
}

func TestMergeArchivesDirectParentChildConnectionBetweenMergedPeople(t *testing.T) {
	for _, sourceIsParent := range []bool{true, false} {
		t.Run(fmt.Sprintf("source_is_parent_%t", sourceIsParent), func(t *testing.T) {
			fixture := newPeopleFixture(t)
			ctx := context.Background()
			source := addPerson(t, fixture.db, "Source", "Source")
			survivor := addPerson(t, fixture.db, "Survivor", "Survivor")
			personA, personB := source, survivor
			if !sourceIsParent {
				personA, personB = survivor, source
			}
			relationshipID := uuid.New()
			_, err := fixture.db.NewRaw(`INSERT INTO family_relationships (id, relationship_type, person_a_id, person_b_id) VALUES (?, 'parent_child', ?, ?)`, relationshipID, personA, personB).Exec(ctx)
			require.NoError(t, err)

			preview, err := fixture.service.PreviewMerge(ctx, fixture.actor, source, survivor)
			require.NoError(t, err)
			assert.True(t, preview.CanMerge)
			assert.Equal(t, 0, preview.References.FamilyRelationshipsMoved)
			assert.Equal(t, 1, preview.References.FamilyRelationshipsArchived)

			_, err = fixture.service.Merge(ctx, fixture.actor, MergeRequest{
				SourcePersonID: source.String(), SurvivorPersonID: survivor.String(), SourceVersion: 1, SurvivorVersion: 1,
				PreviewFingerprint: preview.PreviewFingerprint,
			})
			require.NoError(t, err)
			var archived bool
			var version int64
			require.NoError(t, fixture.db.NewRaw(`SELECT archived_at IS NOT NULL, version FROM family_relationships WHERE id = ?`, relationshipID).Scan(ctx, &archived, &version))
			assert.True(t, archived)
			assert.Equal(t, int64(2), version)
		})
	}
}

func TestMergeRejectsPeopleConnectedByIndirectParentChildPath(t *testing.T) {
	fixture := newPeopleFixture(t)
	ctx := context.Background()
	source := addPerson(t, fixture.db, "Ancestor source", "Ancestor source")
	middle := addPerson(t, fixture.db, "Middle", "Middle")
	survivor := addPerson(t, fixture.db, "Descendant survivor", "Descendant survivor")
	for _, endpoints := range [][2]uuid.UUID{{source, middle}, {middle, survivor}} {
		_, err := fixture.db.NewRaw(`INSERT INTO family_relationships (id, relationship_type, person_a_id, person_b_id) VALUES (?, 'parent_child', ?, ?)`, uuid.New(), endpoints[0], endpoints[1]).Exec(ctx)
		require.NoError(t, err)
	}

	preview, err := fixture.service.PreviewMerge(ctx, fixture.actor, source, survivor)
	require.NoError(t, err)
	assert.False(t, preview.CanMerge)
	assert.Contains(t, preview.Blockers, "Resolve the parent-child path between these People before merging them.")
	_, err = fixture.service.Merge(ctx, fixture.actor, MergeRequest{
		SourcePersonID: source.String(), SurvivorPersonID: survivor.String(), SourceVersion: 1, SurvivorVersion: 1,
		PreviewFingerprint: preview.PreviewFingerprint,
	})
	require.ErrorIs(t, err, ErrFamilyMergeCycle)
	storedSource, getErr := fixture.service.Get(ctx, source)
	require.NoError(t, getErr)
	assert.Equal(t, "current", storedSource.Status)
}

func TestMergeLateAuditFailureRollsBackEveryEffect(t *testing.T) {
	fixture := newPeopleFixture(t)
	ctx := context.Background()
	source := addPerson(t, fixture.db, "Rollback source", "Rollback source")
	survivor := addPerson(t, fixture.db, "Rollback survivor", "Rollback survivor")
	sourceAccess := addAccess(t, fixture.db, source, true, "rollback@example.com")
	sessionID := addSession(t, fixture.db, source, sourceAccess)
	child := addPerson(t, fixture.db, "Rollback child", "Rollback child")
	relationshipID := uuid.New()
	_, err := fixture.db.NewRaw(`INSERT INTO family_relationships (id, relationship_type, person_a_id, person_b_id) VALUES (?, 'parent_child', ?, ?)`, relationshipID, source, child).Exec(ctx)
	require.NoError(t, err)
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
	var relationshipParent uuid.UUID
	var relationshipVersion int64
	require.NoError(t, fixture.db.NewRaw(`SELECT person_a_id, version FROM family_relationships WHERE id = ?`, relationshipID).Scan(ctx, &relationshipParent, &relationshipVersion))
	assert.Equal(t, source, relationshipParent)
	assert.Equal(t, int64(1), relationshipVersion)
}
