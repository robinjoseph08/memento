//go:build integration

package people

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/internal/testdb"
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
	_, err := db.NewRaw(`INSERT INTO recipient_access_generations (id, person_id, generation, state, is_current, onboarding_completed_at, created_at, updated_at) VALUES (?, ?, 1, 'completed', ?, ?, ?, ?)`, accessID, personID, current, now, now, now).Exec(ctx)
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

func TestMergePreviewAndConfirmationEnforceAuthorityAndGenerationGates(t *testing.T) {
	fixture := newPeopleFixture(t)
	ctx := context.Background()
	source := addPerson(t, fixture.db, "Source", "Source")
	survivor := addPerson(t, fixture.db, "Survivor", "Survivor")
	addAccess(t, fixture.db, source, true, "source@example.com")
	addAccess(t, fixture.db, survivor, true, "survivor@example.com")
	preview, err := fixture.service.PreviewMerge(ctx, source, survivor)
	require.NoError(t, err)
	assert.False(t, preview.CanMerge)
	assert.Contains(t, preview.Blockers, "Resolve one current Recipient access generation before merging.")
	_, err = fixture.service.Merge(ctx, fixture.actor, MergeRequest{SourcePersonID: source.String(), SurvivorPersonID: survivor.String(), SourceVersion: 1, SurvivorVersion: 1, TransferCurrentAccessGeneration: true})
	require.ErrorIs(t, err, ErrTwoCurrentGenerations)

	curatorPreview, err := fixture.service.PreviewMerge(ctx, fixture.actor.PersonID, survivor)
	require.NoError(t, err)
	assert.False(t, curatorPreview.CanMerge)
	assert.Contains(t, curatorPreview.Blockers, "The Curator Person must be the survivor.")
	_, err = fixture.service.Merge(ctx, fixture.actor, MergeRequest{SourcePersonID: fixture.actor.PersonID.String(), SurvivorPersonID: survivor.String(), SourceVersion: 1, SurvivorVersion: 1})
	require.ErrorIs(t, err, ErrCuratorMustSurvive)
}

func TestMergeExplicitlyTransfersGenerationResolvesEmailInvalidatesSessionsAndPreservesAttribution(t *testing.T) {
	fixture := newPeopleFixture(t)
	ctx := context.Background()
	source := addPerson(t, fixture.db, "Duplicate Robin", "Robin, Duplicate")
	survivor := addPerson(t, fixture.db, "Robin", "Robin")
	sourceAccess := addAccess(t, fixture.db, source, true, "duplicate@example.com")
	survivorOldAccess := addAccess(t, fixture.db, survivor, false, "robin@example.com")
	sourceSession := addSession(t, fixture.db, source, sourceAccess)
	survivorSession := addSession(t, fixture.db, survivor, survivorOldAccess)
	_, err := fixture.db.NewRaw(`INSERT INTO security_audit_events (actor_person_id, subject_person_id, action, outcome, session_id) VALUES (?, ?, 'source_history', 'success', ?), (?, ?, 'survivor_history', 'success', ?)`, fixture.actor.PersonID, source, fixture.actor.SessionID, fixture.actor.PersonID, survivor, fixture.actor.SessionID).Exec(ctx)
	require.NoError(t, err)

	preview, err := fixture.service.PreviewMerge(ctx, source, survivor)
	require.NoError(t, err)
	assert.True(t, preview.CanMerge)
	assert.True(t, preview.RequiresGenerationTransfer)
	assert.True(t, preview.RequiresEmailResolution)
	assert.Equal(t, sourceAccess.String(), preview.References.CurrentRecipientGenerationID)
	assert.Equal(t, 2, preview.References.SessionsInvalidated)
	assert.True(t, preview.RolesWillNotBeUnioned)
	assert.True(t, preview.AudienceAuthorityUnchanged)

	_, err = fixture.service.Merge(ctx, fixture.actor, MergeRequest{SourcePersonID: source.String(), SurvivorPersonID: survivor.String(), SourceVersion: 1, SurvivorVersion: 1})
	require.ErrorIs(t, err, ErrGenerationTransferNeeded)
	_, err = fixture.service.Merge(ctx, fixture.actor, MergeRequest{SourcePersonID: source.String(), SurvivorPersonID: survivor.String(), SourceVersion: 1, SurvivorVersion: 1, TransferCurrentAccessGeneration: true})
	require.ErrorIs(t, err, ErrEmailResolutionNeeded)

	merged, err := fixture.service.Merge(ctx, fixture.actor, MergeRequest{SourcePersonID: source.String(), SurvivorPersonID: survivor.String(), SourceVersion: 1, SurvivorVersion: 1, TransferCurrentAccessGeneration: true, EmailResolution: "keep_survivor"})
	require.NoError(t, err)
	assert.Equal(t, survivor.String(), merged.ID)
	assert.Equal(t, int64(2), merged.Version)
	assert.Equal(t, "robin@example.com", merged.CurrentLoginEmail)
	var generationPerson uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT person_id FROM recipient_access_generations WHERE id = ?`, sourceAccess).Scan(ctx, &generationPerson))
	assert.Equal(t, survivor, generationPerson)
	var activeSessions int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM sessions WHERE id IN (?, ?) AND revoked_at IS NULL`, sourceSession, survivorSession).Scan(ctx, &activeSessions))
	assert.Zero(t, activeSessions)
	storedSource, err := fixture.service.Get(ctx, source)
	require.NoError(t, err)
	assert.Equal(t, "merged", storedSource.Status)
	assert.Equal(t, survivor.String(), storedSource.MergedIntoPersonID)
	assert.NotZero(t, storedSource.HistoricalAuditCount)
	var sourceHistorySubject, survivorHistorySubject uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT subject_person_id FROM security_audit_events WHERE action = 'source_history'`).Scan(ctx, &sourceHistorySubject))
	require.NoError(t, fixture.db.NewRaw(`SELECT subject_person_id FROM security_audit_events WHERE action = 'survivor_history'`).Scan(ctx, &survivorHistorySubject))
	assert.Equal(t, source, sourceHistorySubject)
	assert.Equal(t, survivor, survivorHistorySubject)
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

	merged, err := fixture.service.Merge(ctx, fixture.actor, MergeRequest{
		SourcePersonID: source.String(), SurvivorPersonID: survivor.String(),
		SourceVersion: 1, SurvivorVersion: 1, TransferCurrentAccessGeneration: true,
		EmailResolution: "keep_source",
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
	_, err := fixture.db.ExecContext(ctx, `CREATE FUNCTION reject_people_merge_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action = 'people_merged' THEN RAISE EXCEPTION 'late merge failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_people_merge_audit BEFORE INSERT ON security_audit_events FOR EACH ROW EXECUTE FUNCTION reject_people_merge_audit()`)
	require.NoError(t, err)

	_, err = fixture.service.Merge(ctx, fixture.actor, MergeRequest{SourcePersonID: source.String(), SurvivorPersonID: survivor.String(), SourceVersion: 1, SurvivorVersion: 1, TransferCurrentAccessGeneration: true})
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
