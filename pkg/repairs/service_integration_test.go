//go:build integration

package repairs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/events"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/people"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/staging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type identityConnector struct {
	people            []immich.PersonSummary
	faces             map[uuid.UUID][]immich.FaceSummary
	faceErrs          map[uuid.UUID]error
	faceCalls         []uuid.UUID
	faceMu            sync.Mutex
	faceStarted       chan struct{}
	releaseFace       chan struct{}
	assets            map[uuid.UUID]immich.AssetSummary
	assetMissing      map[uuid.UUID]bool
	assetErr          error
	deliveryAvailable map[uuid.UUID]bool
	deliveryErr       error
	deliveryMu        sync.Mutex
	deliveryCalls     []uuid.UUID
	deliveryTypes     []string
	deliveryStarted   chan uuid.UUID
	releaseDelivery   <-chan struct{}
	err               error
}

func (connector *identityConnector) Check(context.Context) error { return connector.err }
func (connector *identityConnector) People(context.Context) ([]immich.PersonSummary, error) {
	return connector.people, connector.err
}
func (connector *identityConnector) Asset(_ context.Context, assetID uuid.UUID) (immich.AssetSummary, error) {
	if connector.assetErr != nil {
		return immich.AssetSummary{}, connector.assetErr
	}
	if connector.assetMissing[assetID] {
		return immich.AssetSummary{}, immich.ErrNotFound
	}
	if asset, configured := connector.assets[assetID]; configured {
		return asset, nil
	}
	return immich.AssetSummary{SourceID: assetID, MediaType: "image"}, nil
}

func (connector *identityConnector) AssetDeliveryAvailable(ctx context.Context, assetID uuid.UUID, mediaType string) (bool, error) {
	connector.deliveryMu.Lock()
	connector.deliveryCalls = append(connector.deliveryCalls, assetID)
	connector.deliveryTypes = append(connector.deliveryTypes, mediaType)
	started, release := connector.deliveryStarted, connector.releaseDelivery
	deliveryErr := connector.deliveryErr
	available, configured := connector.deliveryAvailable[assetID]
	connector.deliveryMu.Unlock()
	if started != nil {
		select {
		case started <- assetID:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	if deliveryErr != nil {
		return false, deliveryErr
	}
	if configured {
		return available, nil
	}
	return true, nil
}

func (connector *identityConnector) setDeliveryAvailable(assetID uuid.UUID, available bool) {
	connector.deliveryMu.Lock()
	defer connector.deliveryMu.Unlock()
	if connector.deliveryAvailable == nil {
		connector.deliveryAvailable = make(map[uuid.UUID]bool)
	}
	connector.deliveryAvailable[assetID] = available
}

func (connector *identityConnector) Faces(_ context.Context, assetID uuid.UUID) ([]immich.FaceSummary, error) {
	connector.faceMu.Lock()
	started, release := connector.faceStarted, connector.releaseFace
	connector.faceStarted = nil
	connector.faceMu.Unlock()
	if started != nil {
		close(started)
		<-release
	}
	connector.faceCalls = append(connector.faceCalls, assetID)
	if err := connector.faceErrs[assetID]; err != nil {
		return nil, err
	}
	return connector.faces[assetID], connector.err
}

type repairFixture struct {
	service   *Service
	db        *bun.DB
	connector *identityConnector
	actor     setup.CuratorSession
	accessID  uuid.UUID
	personID  uuid.UUID
	assetIDs  []uuid.UUID
	faceIDs   []uuid.UUID
	oldID     uuid.UUID
}

func newRepairFixture(t *testing.T, anchorCount int) repairFixture {
	t.Helper()
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(context.Background(), db))
	actorID, personID := uuid.New(), uuid.New()
	accessID, sessionID := uuid.New(), uuid.New()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Curator', 'Curator'), (?, 'Family member', 'Family member');
		INSERT INTO person_roles (person_id, role) VALUES (?, 'curator');
		INSERT INTO recipient_access_generations (id, person_id, generation, state, is_current, onboarding_completed_at) VALUES (?, ?, 1, 'completed', true, now());
		INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at)
		VALUES (?, ?, ?, ?, ?, 'trusted', now() + interval '1 hour');
	`, actorID, personID, actorID, accessID, actorID, sessionID, hashBytes("session"), actorID, accessID, hashBytes("epoch"))
	require.NoError(t, err)
	oldID := uuid.New()
	connector := &identityConnector{people: []immich.PersonSummary{{SourceID: oldID, Name: "Immich member"}}, faces: map[uuid.UUID][]immich.FaceSummary{}, faceErrs: map[uuid.UUID]error{}}
	assetIDs := make([]uuid.UUID, anchorCount)
	faceIDs := make([]uuid.UUID, anchorCount)
	for index := range anchorCount {
		assetIDs[index], faceIDs[index] = uuid.New(), uuid.New()
		mediaID, backingID := uuid.New(), uuid.New()
		_, err := db.ExecContext(context.Background(), `
			INSERT INTO media_items (id, immich_asset_id, media_type, local_date_time, first_seen_at, last_seen_at)
			VALUES (?, ?, 'image', '2026-01-01T00:00:00Z', now(), now());
			INSERT INTO media_backings (id, media_item_id, immich_asset_id, checksum, capture_at, filename, original_path, linked_at)
			VALUES (?, ?, ?, ?, '2026-01-01T00:00:00Z', ?, ?, now());
		`, mediaID, assetIDs[index], backingID, mediaID, assetIDs[index], checksum("shared"), "photo.jpg", "/private/photo.jpg")
		require.NoError(t, err)
		person := oldID
		connector.faces[assetIDs[index]] = []immich.FaceSummary{{SourceID: faceIDs[index], PersonID: &person, ImageWidth: 100, ImageHeight: 80, X1: 1, Y1: 2, X2: 20, Y2: 30}}
	}
	service := New(db, connector)
	actor := setup.CuratorSession{PersonID: actorID, SessionID: sessionID}
	require.NoError(t, reconcile(service))
	_, err = service.LinkPerson(context.Background(), actor, LinkPersonRequest{PersonID: personID.String(), ImmichPersonID: oldID.String()})
	require.NoError(t, err)
	return repairFixture{service: service, db: db, connector: connector, actor: actor, accessID: accessID, personID: personID, assetIDs: assetIDs, faceIDs: faceIDs, oldID: oldID}
}

func reconcile(service *Service) error {
	_, err := service.ReconcilePeople(context.Background())
	return err
}

func hashBytes(value string) []byte {
	hash := sha256.Sum256([]byte(value))
	return hash[:]
}

func reviewedMediaToken(t *testing.T, fixture repairFixture, candidateID uuid.UUID) string {
	t.Helper()
	listed, err := fixture.service.List(context.Background())
	require.NoError(t, err)
	for _, candidate := range listed.MediaCandidates {
		if candidate.ID == candidateID.String() {
			require.NotEmpty(t, candidate.ReviewToken)
			return candidate.ReviewToken
		}
	}
	t.Fatalf("Media repair candidate %s was not listed", candidateID)
	return ""
}

func setFreshAssetFromBacking(t *testing.T, fixture repairFixture, assetID uuid.UUID) {
	t.Helper()
	var asset immich.AssetSummary
	asset.SourceID = assetID
	require.NoError(t, fixture.db.NewRaw(`
		SELECT media.media_type, COALESCE(backing.checksum, ''), COALESCE(backing.capture_at, ''),
			backing.filename, backing.original_path
		FROM media_backings AS backing
		JOIN media_items AS media ON media.id = backing.media_item_id
		WHERE backing.immich_asset_id = ? AND backing.active
	`, assetID).Scan(context.Background(), &asset.MediaType, &asset.Checksum,
		&asset.CaptureAt, &asset.Filename, &asset.OriginalPath))
	if fixture.connector.assets == nil {
		fixture.connector.assets = make(map[uuid.UUID]immich.AssetSummary)
	}
	fixture.connector.assets[assetID] = asset
}

func checksum(value string) string { return hex.EncodeToString(hashBytes(value)[:20]) }

func waitForRepairBlockedQuery(t *testing.T, db *bun.DB, blockerPID int, pattern string) int {
	t.Helper()
	timer := time.NewTimer(4 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	lastState := "no matching backend"
	for {
		type waitState struct {
			PID       int
			WaitType  string
			WaitEvent string
			Blockers  string
			Query     string
		}
		var states []waitState
		err := db.NewRaw(`
			SELECT pid, COALESCE(wait_event_type, '') AS wait_type,
				COALESCE(wait_event, '') AS wait_event,
				array_to_string(pg_blocking_pids(pid), ',') AS blockers, query
			FROM pg_stat_activity
			WHERE datname = current_database() AND query LIKE ?
			ORDER BY pid
		`, pattern).Scan(context.Background(), &states)
		require.NoError(t, err)
		for _, state := range states {
			lastState = fmt.Sprintf("pid=%d wait_type=%q wait_event=%q blockers=%q query=%q", state.PID, state.WaitType, state.WaitEvent, state.Blockers, state.Query)
			if state.WaitType == "Lock" && slices.Contains(strings.Split(state.Blockers, ","), strconv.Itoa(blockerPID)) {
				return state.PID
			}
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			require.FailNow(t, "expected query did not wait for the controlled lock", "pattern=%q blocker_pid=%d last_state=%s", pattern, blockerPID, lastState)
		}
	}
}

func addAnchorBacking(t *testing.T, fixture repairFixture, linkedAt time.Time, active bool) uuid.UUID {
	t.Helper()
	mediaID, assetID := uuid.New(), uuid.New()
	var endedAt *time.Time
	if !active {
		endedAt = &linkedAt
	}
	_, err := fixture.db.ExecContext(context.Background(), `
		INSERT INTO media_items (id, immich_asset_id, media_type, local_date_time, first_seen_at, last_seen_at)
		VALUES (?, ?, 'image', '2026-01-01T00:00:00Z', ?, ?);
		INSERT INTO media_backings (id, media_item_id, immich_asset_id, checksum, linked_at, active, ended_at)
		VALUES (gen_random_uuid(), ?, ?, ?, ?, ?, ?)
	`, mediaID, assetID, linkedAt, linkedAt, mediaID, assetID, checksum(assetID.String()), linkedAt, active, endedAt)
	require.NoError(t, err)
	return assetID
}

func TestPersonMergeBecomesReviewAndSuppressesAttendanceUntilExplicitConfirmation(t *testing.T) {
	fixture := newRepairFixture(t, 2)
	destination := uuid.New()
	fixture.connector.people = []immich.PersonSummary{{SourceID: destination, Name: "Merged destination"}}
	for _, assetID := range fixture.assetIDs {
		faces := fixture.connector.faces[assetID]
		faces[0].PersonID = &destination
		fixture.connector.faces[assetID] = faces
	}

	require.NoError(t, reconcile(fixture.service))
	suggestions, err := fixture.service.SuggestionPersonIDs(context.Background(), []uuid.UUID{destination, fixture.oldID})
	require.NoError(t, err)
	assert.Empty(t, suggestions)
	listed, err := fixture.service.List(context.Background())
	require.NoError(t, err)
	require.Len(t, listed.PersonCandidates, 1)
	candidate := listed.PersonCandidates[0]
	assert.Equal(t, destination.String(), candidate.CandidateImmichPersonID)
	assert.Equal(t, "pending", candidate.State)
	assert.Len(t, candidate.Anchors, 2)
	assert.Contains(t, candidate.Conflicts, "immich_person_missing")

	var recipientBefore string
	require.NoError(t, fixture.db.NewRaw(`SELECT row_to_json(g)::text FROM recipient_access_generations AS g`).Scan(context.Background(), &recipientBefore))
	candidateID := uuid.MustParse(candidate.ID)
	fixture.connector.faceCalls = nil
	_, err = fixture.service.ConfirmPerson(context.Background(), fixture.actor, candidateID)
	require.NoError(t, err)
	assert.Len(t, fixture.connector.faceCalls, len(fixture.assetIDs), "confirmation must store the exact anchors it validated")
	suggestions, err = fixture.service.SuggestionPersonIDs(context.Background(), []uuid.UUID{destination})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{fixture.personID}, suggestions)
	listed, err = fixture.service.List(context.Background())
	require.NoError(t, err)
	require.Len(t, listed.PersonCandidates, 1)
	assert.Equal(t, "confirmed", listed.PersonCandidates[0].State)
	assert.NotNil(t, listed.PersonCandidates[0].ResolvedAt)
	assert.Len(t, listed.PersonCandidates[0].Anchors, 2)
	_, err = fixture.service.ConfirmPerson(context.Background(), fixture.actor, candidateID)
	assert.ErrorIs(t, err, ErrAlreadyResolved)

	var recipientAfter string
	require.NoError(t, fixture.db.NewRaw(`SELECT row_to_json(g)::text FROM recipient_access_generations AS g`).Scan(context.Background(), &recipientAfter))
	assert.JSONEq(t, recipientBefore, recipientAfter)
	// Audience tables arrive with publication work; repair confirmation deliberately has no write seam to them.
}

func TestPersonConfirmationRejectsChangedFaceEvidence(t *testing.T) {
	fixture := newRepairFixture(t, 1)
	replacement := uuid.New()
	fixture.connector.people = []immich.PersonSummary{{SourceID: replacement, Name: "Replacement"}}
	faces := fixture.connector.faces[fixture.assetIDs[0]]
	faces[0].PersonID = &replacement
	fixture.connector.faces[fixture.assetIDs[0]] = faces
	require.NoError(t, reconcile(fixture.service))
	listed, err := fixture.service.List(context.Background())
	require.NoError(t, err)
	require.Len(t, listed.PersonCandidates, 1)

	other := uuid.New()
	fixture.connector.people = append(fixture.connector.people, immich.PersonSummary{SourceID: other, Name: "Other"})
	faces[0].PersonID = &other
	fixture.connector.faces[fixture.assetIDs[0]] = faces
	_, err = fixture.service.ConfirmPerson(context.Background(), fixture.actor, uuid.MustParse(listed.PersonCandidates[0].ID))
	assert.ErrorIs(t, err, ErrConflict)
	suggestions, err := fixture.service.SuggestionPersonIDs(context.Background(), []uuid.UUID{replacement, other})
	require.NoError(t, err)
	assert.Empty(t, suggestions)
}

func TestPersonConfirmationRevalidatesIdentityAfterAnchorEvaluation(t *testing.T) {
	fixture := newRepairFixture(t, 1)
	replacement := uuid.New()
	fixture.connector.people = []immich.PersonSummary{{SourceID: replacement, Name: "Replacement"}}
	faces := fixture.connector.faces[fixture.assetIDs[0]]
	faces[0].PersonID = &replacement
	fixture.connector.faces[fixture.assetIDs[0]] = faces
	require.NoError(t, reconcile(fixture.service))
	listed, err := fixture.service.List(context.Background())
	require.NoError(t, err)
	require.Len(t, listed.PersonCandidates, 1)
	fixture.connector.faceStarted = make(chan struct{})
	fixture.connector.releaseFace = make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, confirmErr := fixture.service.ConfirmPerson(context.Background(), fixture.actor, uuid.MustParse(listed.PersonCandidates[0].ID))
		result <- confirmErr
	}()
	select {
	case <-fixture.connector.faceStarted:
	case <-time.After(time.Second):
		t.Fatal("Person evidence evaluation did not start")
	}
	fixture.connector.people = nil
	close(fixture.connector.releaseFace)
	assert.ErrorIs(t, <-result, ErrConflict)
	var state string
	require.NoError(t, fixture.db.NewRaw(`SELECT state FROM immich_person_links WHERE person_id = ?`, fixture.personID).Scan(context.Background(), &state))
	assert.Equal(t, "needs_review", state)
}

func TestRecoveredPersonLinkStillRequiresExplicitConfirmation(t *testing.T) {
	fixture := newRepairFixture(t, 0)
	fixture.connector.people = nil
	require.NoError(t, reconcile(fixture.service))

	fixture.connector.people = []immich.PersonSummary{{SourceID: fixture.oldID, Name: "Immich member"}}
	require.NoError(t, reconcile(fixture.service))
	suggestions, err := fixture.service.SuggestionPersonIDs(context.Background(), []uuid.UUID{fixture.oldID})
	require.NoError(t, err)
	assert.Empty(t, suggestions)
	listed, err := fixture.service.List(context.Background())
	require.NoError(t, err)
	require.Len(t, listed.PersonCandidates, 1)
	candidate := listed.PersonCandidates[0]
	assert.Equal(t, "pending", candidate.State)
	assert.Equal(t, fixture.oldID.String(), candidate.CandidateImmichPersonID)

	_, err = fixture.service.ConfirmPerson(context.Background(), fixture.actor, uuid.MustParse(candidate.ID))
	require.NoError(t, err)
	suggestions, err = fixture.service.SuggestionPersonIDs(context.Background(), []uuid.UUID{fixture.oldID})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{fixture.personID}, suggestions)
}

func TestInFlightReconciliationCannotUndoLaterPersonConfirmation(t *testing.T) {
	fixture := newRepairFixture(t, 1)
	replacement := uuid.New()
	fixture.connector.people = append(fixture.connector.people, immich.PersonSummary{SourceID: replacement, Name: "Replacement"})
	_, err := fixture.db.ExecContext(context.Background(), `
		INSERT INTO immich_people_inventory (immich_person_id, name, first_seen_at, last_seen_at)
		VALUES (?, 'Replacement', now(), now())
	`, replacement)
	require.NoError(t, err)
	fixture.connector.faceStarted = make(chan struct{})
	fixture.connector.releaseFace = make(chan struct{})
	reconcileResult := make(chan error, 1)
	go func() { reconcileResult <- reconcile(fixture.service) }()
	<-fixture.connector.faceStarted

	_, err = fixture.service.LinkPerson(context.Background(), fixture.actor, LinkPersonRequest{
		PersonID: fixture.personID.String(), ImmichPersonID: replacement.String(),
	})
	require.NoError(t, err)
	close(fixture.connector.releaseFace)
	require.NoError(t, <-reconcileResult)

	suggestions, err := fixture.service.SuggestionPersonIDs(context.Background(), []uuid.UUID{replacement})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{fixture.personID}, suggestions)
	var state string
	require.NoError(t, fixture.db.NewRaw(`SELECT state FROM immich_person_links WHERE person_id = ?`, fixture.personID).Scan(context.Background(), &state))
	assert.Equal(t, "linked", state)
}

func TestPersonReconciliationLocksPeopleBeforeLinks(t *testing.T) {
	fixture := newRepairFixture(t, 1)
	replacement := uuid.New()
	fixture.connector.people = []immich.PersonSummary{{SourceID: replacement, Name: "Replacement"}}
	faces := fixture.connector.faces[fixture.assetIDs[0]]
	faces[0].PersonID = &replacement
	fixture.connector.faces[fixture.assetIDs[0]] = faces

	personBlocker, err := fixture.db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	_, err = personBlocker.NewRaw(`SELECT id FROM people WHERE id = ? FOR UPDATE`, fixture.personID).Exec(context.Background())
	require.NoError(t, err)
	var personBlockerPID int
	require.NoError(t, personBlocker.NewRaw(`SELECT pg_backend_pid()`).Scan(context.Background(), &personBlockerPID))
	result := make(chan error, 1)
	go func() { result <- reconcile(fixture.service) }()
	waitForRepairBlockedQuery(t, fixture.db, personBlockerPID, `%SELECT person.id FROM people AS person%`)
	linkProbe, err := fixture.db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	_, err = linkProbe.NewRaw(`SELECT person_id FROM immich_person_links WHERE person_id = ? FOR UPDATE NOWAIT`, fixture.personID).Exec(context.Background())
	require.NoError(t, err, "reconciliation must lock People before Person links")
	require.NoError(t, linkProbe.Rollback())
	require.NoError(t, personBlocker.Commit())
	select {
	case reconcileErr := <-result:
		require.NoError(t, reconcileErr)
	case <-time.After(time.Second):
		t.Fatal("reconciliation did not complete after Person lock release")
	}
}

func TestFaceReassignmentAndAnchorConflictRequireReview(t *testing.T) {
	fixture := newRepairFixture(t, 2)
	other := uuid.New()
	fixture.connector.people = append(fixture.connector.people, immich.PersonSummary{SourceID: other, Name: "Other cluster"})
	faces := fixture.connector.faces[fixture.assetIDs[0]]
	faces[0].PersonID = &other
	fixture.connector.faces[fixture.assetIDs[0]] = faces

	require.NoError(t, reconcile(fixture.service))
	listed, err := fixture.service.List(context.Background())
	require.NoError(t, err)
	require.Len(t, listed.PersonCandidates, 1)
	candidate := listed.PersonCandidates[0]
	assert.Empty(t, candidate.CandidateImmichPersonID)
	assert.True(t, candidate.PreviousImmichPersonPresent)
	assert.Contains(t, candidate.Conflicts, "anchors_split_across_people")
	suggestions, err := fixture.service.SuggestionPersonIDs(context.Background(), []uuid.UUID{fixture.oldID, other})
	require.NoError(t, err)
	assert.Empty(t, suggestions)

	_, err = fixture.service.ConfirmPerson(context.Background(), fixture.actor, uuid.MustParse(candidate.ID))
	require.NoError(t, err)
	suggestions, err = fixture.service.SuggestionPersonIDs(context.Background(), []uuid.UUID{fixture.oldID, other})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{fixture.personID}, suggestions)
	var anchors int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM immich_face_anchors WHERE person_id = ?`, fixture.personID).Scan(context.Background(), &anchors))
	assert.Equal(t, 1, anchors, "confirmation keeps only anchors still assigned to the confirmed identity")
}

func TestDeletedAnchorAssetDoesNotBlockPersonReconciliation(t *testing.T) {
	fixture := newRepairFixture(t, 1)
	fixture.connector.faceErrs[fixture.assetIDs[0]] = immich.ErrNotFound

	require.NoError(t, reconcile(fixture.service))
	listed, err := fixture.service.List(context.Background())
	require.NoError(t, err)
	require.Len(t, listed.PersonCandidates, 1)
	assert.Contains(t, listed.PersonCandidates[0].Conflicts, "face_anchor_missing")
	suggestions, err := fixture.service.SuggestionPersonIDs(context.Background(), []uuid.UUID{fixture.oldID})
	require.NoError(t, err)
	assert.Empty(t, suggestions)
}

func TestRejectedRepairStaysRejectedAndCannotRestoreSuggestions(t *testing.T) {
	fixture := newRepairFixture(t, 1)
	replacement := uuid.New()
	fixture.connector.people = []immich.PersonSummary{{SourceID: replacement, Name: "Replacement"}}
	faces := fixture.connector.faces[fixture.assetIDs[0]]
	faces[0].PersonID = &replacement
	fixture.connector.faces[fixture.assetIDs[0]] = faces
	require.NoError(t, reconcile(fixture.service))
	listed, err := fixture.service.List(context.Background())
	require.NoError(t, err)
	candidateID := uuid.MustParse(listed.PersonCandidates[0].ID)
	_, err = fixture.service.RejectPerson(context.Background(), fixture.actor, candidateID)
	require.NoError(t, err)
	require.NoError(t, reconcile(fixture.service))

	listed, err = fixture.service.List(context.Background())
	require.NoError(t, err)
	require.Len(t, listed.PersonCandidates, 1)
	assert.Equal(t, "rejected", listed.PersonCandidates[0].State)
	suggestions, err := fixture.service.SuggestionPersonIDs(context.Background(), []uuid.UUID{replacement})
	require.NoError(t, err)
	assert.Empty(t, suggestions)
}

func TestPersonRejectionLocksPeopleBeforeCandidate(t *testing.T) {
	fixture := newRepairFixture(t, 1)
	replacement := uuid.New()
	fixture.connector.people = []immich.PersonSummary{{SourceID: replacement, Name: "Replacement"}}
	faces := fixture.connector.faces[fixture.assetIDs[0]]
	faces[0].PersonID = &replacement
	fixture.connector.faces[fixture.assetIDs[0]] = faces
	require.NoError(t, reconcile(fixture.service))
	listed, err := fixture.service.List(context.Background())
	require.NoError(t, err)
	require.Len(t, listed.PersonCandidates, 1)
	candidateID := uuid.MustParse(listed.PersonCandidates[0].ID)

	personBlocker, err := fixture.db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	_, err = personBlocker.NewRaw(`SELECT id FROM people WHERE id = ? FOR UPDATE`, fixture.actor.PersonID).Exec(context.Background())
	require.NoError(t, err)
	var personBlockerPID int
	require.NoError(t, personBlocker.NewRaw(`SELECT pg_backend_pid()`).Scan(context.Background(), &personBlockerPID))
	rejection := make(chan error, 1)
	go func() {
		_, rejectErr := fixture.service.RejectPerson(context.Background(), fixture.actor, candidateID)
		rejection <- rejectErr
	}()
	waitForRepairBlockedQuery(t, fixture.db, personBlockerPID, `%SELECT id FROM people WHERE id IN%`)
	candidateProbe, err := fixture.db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	_, err = candidateProbe.NewRaw(`SELECT id FROM person_repair_candidates WHERE id = ? FOR UPDATE NOWAIT`, candidateID).Exec(context.Background())
	require.NoError(t, err, "rejection must lock People before its candidate")
	require.NoError(t, candidateProbe.Rollback())
	require.NoError(t, personBlocker.Commit())
	select {
	case rejectErr := <-rejection:
		require.NoError(t, rejectErr)
	case <-time.After(time.Second):
		t.Fatal("rejection did not complete after Person lock release")
	}
}

func TestNewImmichPersonRemainsAdditionUntilCuratorLinksIt(t *testing.T) {
	fixture := newRepairFixture(t, 0)
	addition := uuid.New()
	fixture.connector.people = append(fixture.connector.people, immich.PersonSummary{SourceID: addition, Name: "New identity"})
	require.NoError(t, reconcile(fixture.service))
	listed, err := fixture.service.List(context.Background())
	require.NoError(t, err)
	require.Len(t, listed.UnlinkedImmichPeople, 1)
	assert.Equal(t, addition.String(), listed.UnlinkedImmichPeople[0].ImmichPersonID)
	suggestions, err := fixture.service.SuggestionPersonIDs(context.Background(), []uuid.UUID{addition})
	require.NoError(t, err)
	assert.Empty(t, suggestions)

	_, err = fixture.service.LinkPerson(context.Background(), fixture.actor, LinkPersonRequest{PersonID: fixture.personID.String(), ImmichPersonID: addition.String()})
	require.NoError(t, err)
	suggestions, err = fixture.service.SuggestionPersonIDs(context.Background(), []uuid.UUID{addition})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{fixture.personID}, suggestions)
}

func TestManualPersonLinkRejectsIdentityThatDisappearedAfterReconciliation(t *testing.T) {
	fixture := newRepairFixture(t, 0)
	addition := uuid.New()
	fixture.connector.people = append(fixture.connector.people, immich.PersonSummary{SourceID: addition, Name: "Temporary identity"})
	require.NoError(t, reconcile(fixture.service))

	fixture.connector.people = []immich.PersonSummary{{SourceID: fixture.oldID, Name: "Immich member"}}
	_, err := fixture.service.LinkPerson(context.Background(), fixture.actor, LinkPersonRequest{
		PersonID: fixture.personID.String(), ImmichPersonID: addition.String(),
	})
	assert.ErrorIs(t, err, ErrConflict)
	suggestions, err := fixture.service.SuggestionPersonIDs(context.Background(), []uuid.UUID{fixture.oldID, addition})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{fixture.personID}, suggestions)
}

func TestManualPersonLinkRevalidatesIdentityAfterAnchorCollection(t *testing.T) {
	fixture := newRepairFixture(t, 1)
	addition := uuid.New()
	fixture.connector.people = append(fixture.connector.people, immich.PersonSummary{SourceID: addition, Name: "Temporary identity"})
	faces := fixture.connector.faces[fixture.assetIDs[0]]
	faces[0].PersonID = &addition
	fixture.connector.faces[fixture.assetIDs[0]] = faces
	require.NoError(t, reconcile(fixture.service))
	fixture.connector.faceStarted = make(chan struct{})
	fixture.connector.releaseFace = make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := fixture.service.LinkPerson(context.Background(), fixture.actor, LinkPersonRequest{
			PersonID: fixture.personID.String(), ImmichPersonID: addition.String(),
		})
		result <- err
	}()
	select {
	case <-fixture.connector.faceStarted:
	case <-time.After(time.Second):
		t.Fatal("anchor collection did not start")
	}
	fixture.connector.people = []immich.PersonSummary{{SourceID: fixture.oldID, Name: "Immich member"}}
	close(fixture.connector.releaseFace)
	assert.ErrorIs(t, <-result, ErrConflict)
	var linkedID uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT immich_person_id FROM immich_person_links WHERE person_id = ?`, fixture.personID).Scan(context.Background(), &linkedID))
	assert.Equal(t, fixture.oldID, linkedID)
}

func TestManualPersonLinkCannotRecreateLinkAfterConcurrentArchive(t *testing.T) {
	fixture := newRepairFixture(t, 0)
	tx, err := fixture.db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(context.Background(), `
		UPDATE people SET archived_at = now() WHERE id = ?;
		DELETE FROM immich_person_links WHERE person_id = ?
	`, fixture.personID, fixture.personID)
	require.NoError(t, err)
	var lifecycleBlockerPID int
	require.NoError(t, tx.NewRaw(`SELECT pg_backend_pid()`).Scan(context.Background(), &lifecycleBlockerPID))

	result := make(chan error, 1)
	go func() {
		_, linkErr := fixture.service.LinkPerson(context.Background(), fixture.actor, LinkPersonRequest{
			PersonID: fixture.personID.String(), ImmichPersonID: fixture.oldID.String(),
		})
		result <- linkErr
	}()
	waitForRepairBlockedQuery(t, fixture.db, lifecycleBlockerPID, `%SELECT id FROM people WHERE id IN%`)
	require.NoError(t, tx.Commit())
	select {
	case linkErr := <-result:
		assert.ErrorIs(t, linkErr, ErrInvalid)
	case <-time.After(time.Second):
		t.Fatal("link did not finish after lifecycle transaction committed")
	}
	var links int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM immich_person_links WHERE person_id = ?`, fixture.personID).Scan(context.Background(), &links))
	assert.Zero(t, links)
}

func TestManualPersonLinkDoesNotConfirmUnrelatedPendingProposal(t *testing.T) {
	fixture := newRepairFixture(t, 1)
	proposed, selected := uuid.New(), uuid.New()
	fixture.connector.people = []immich.PersonSummary{{SourceID: proposed, Name: "Proposed"}, {SourceID: selected, Name: "Selected"}}
	faces := fixture.connector.faces[fixture.assetIDs[0]]
	faces[0].PersonID = &proposed
	fixture.connector.faces[fixture.assetIDs[0]] = faces
	require.NoError(t, reconcile(fixture.service))

	_, err := fixture.service.LinkPerson(context.Background(), fixture.actor, LinkPersonRequest{PersonID: fixture.personID.String(), ImmichPersonID: selected.String()})
	require.NoError(t, err)
	var state string
	require.NoError(t, fixture.db.NewRaw(`SELECT state FROM person_repair_candidates WHERE person_id = ? AND candidate_immich_person_id = ?`, fixture.personID, proposed).Scan(context.Background(), &state))
	assert.Equal(t, "rejected", state)
	suggestions, err := fixture.service.SuggestionPersonIDs(context.Background(), []uuid.UUID{proposed, selected})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{fixture.personID}, suggestions)
}

func TestImmichPersonCannotBeClaimedByTwoPortalPeople(t *testing.T) {
	fixture := newRepairFixture(t, 0)
	otherPersonID := uuid.New()
	_, err := fixture.db.ExecContext(context.Background(), `
		INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Other Person', 'Other Person')
	`, otherPersonID)
	require.NoError(t, err)

	_, err = fixture.service.LinkPerson(context.Background(), fixture.actor, LinkPersonRequest{
		PersonID: otherPersonID.String(), ImmichPersonID: fixture.oldID.String(),
	})
	assert.ErrorIs(t, err, ErrConflict)
	var linkedPersonID uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT person_id FROM immich_person_links WHERE immich_person_id = ?`, fixture.oldID).Scan(context.Background(), &linkedPersonID))
	assert.Equal(t, fixture.personID, linkedPersonID)
	_, err = fixture.db.ExecContext(context.Background(), `
		INSERT INTO immich_person_links (
			person_id, immich_person_id, state, last_seen_at, confirmed_at, confirmed_by_person_id
		) VALUES (?, ?, 'linked', now(), now(), ?)
	`, otherPersonID, fixture.oldID, fixture.actor.PersonID)
	require.Error(t, err)
	assert.ErrorContains(t, err, "immich_person_links_source_idx")
}

func TestArchivedPersonStopsSuggestionsAndReleasesImmichIdentity(t *testing.T) {
	fixture := newRepairFixture(t, 0)
	candidateID := uuid.New()
	_, err := fixture.db.ExecContext(context.Background(), `
		INSERT INTO person_repair_candidates (
			id, person_id, previous_immich_person_id, candidate_immich_person_id, created_at
		) VALUES (?, ?, ?, ?, now())
	`, candidateID, fixture.personID, fixture.oldID, fixture.oldID)
	require.NoError(t, err)
	_, err = people.New(fixture.db).Archive(context.Background(), fixture.actor, fixture.personID, 1)
	require.NoError(t, err)

	suggestions, err := fixture.service.SuggestionPersonIDs(context.Background(), []uuid.UUID{fixture.oldID})
	require.NoError(t, err)
	assert.Empty(t, suggestions)
	listed, err := fixture.service.List(context.Background())
	require.NoError(t, err)
	require.Len(t, listed.UnlinkedImmichPeople, 1)
	assert.Equal(t, fixture.oldID.String(), listed.UnlinkedImmichPeople[0].ImmichPersonID)
	var candidateState string
	var resolvedBy uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`
		SELECT state, resolved_by_person_id FROM person_repair_candidates WHERE id = ?
	`, candidateID).Scan(context.Background(), &candidateState, &resolvedBy))
	assert.Equal(t, "superseded", candidateState)
	assert.Equal(t, fixture.actor.PersonID, resolvedBy)
}

func TestPersonAnchorCaptureUsesFiveOfFiftyNewestActiveBackings(t *testing.T) {
	t.Run("stores at most five and replaces prior anchors", func(t *testing.T) {
		fixture := newRepairFixture(t, 0)
		base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
		for index := range 6 {
			assetID := addAnchorBacking(t, fixture, base.Add(time.Duration(index)*time.Hour), true)
			personID := fixture.oldID
			fixture.connector.faces[assetID] = []immich.FaceSummary{{SourceID: uuid.New(), PersonID: &personID}}
		}
		_, err := fixture.service.LinkPerson(context.Background(), fixture.actor, LinkPersonRequest{PersonID: fixture.personID.String(), ImmichPersonID: fixture.oldID.String()})
		require.NoError(t, err)
		var anchors int
		require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM immich_face_anchors WHERE person_id = ?`, fixture.personID).Scan(context.Background(), &anchors))
		assert.Equal(t, 5, anchors)

		fixture.connector.faces = map[uuid.UUID][]immich.FaceSummary{}
		_, err = fixture.service.LinkPerson(context.Background(), fixture.actor, LinkPersonRequest{PersonID: fixture.personID.String(), ImmichPersonID: fixture.oldID.String()})
		require.NoError(t, err)
		require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM immich_face_anchors WHERE person_id = ?`, fixture.personID).Scan(context.Background(), &anchors))
		assert.Zero(t, anchors)
	})

	t.Run("reads only the fifty newest active backings", func(t *testing.T) {
		fixture := newRepairFixture(t, 0)
		base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
		var oldestActive uuid.UUID
		for index := range 51 {
			assetID := addAnchorBacking(t, fixture, base.Add(time.Duration(index)*time.Hour), true)
			if index == 0 {
				oldestActive = assetID
			}
		}
		inactive := addAnchorBacking(t, fixture, base.Add(100*time.Hour), false)
		personID := fixture.oldID
		fixture.connector.faces[oldestActive] = []immich.FaceSummary{{SourceID: uuid.New(), PersonID: &personID}}
		fixture.connector.faces[inactive] = []immich.FaceSummary{{SourceID: uuid.New(), PersonID: &personID}}
		fixture.connector.faceCalls = nil

		_, err := fixture.service.LinkPerson(context.Background(), fixture.actor, LinkPersonRequest{PersonID: fixture.personID.String(), ImmichPersonID: fixture.oldID.String()})
		require.NoError(t, err)
		assert.Len(t, fixture.connector.faceCalls, 50)
		assert.NotContains(t, fixture.connector.faceCalls, oldestActive)
		assert.NotContains(t, fixture.connector.faceCalls, inactive)
		var anchors int
		require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM immich_face_anchors WHERE person_id = ?`, fixture.personID).Scan(context.Background(), &anchors))
		assert.Zero(t, anchors)
	})
}

func TestSourceProblemsOrderPriorityThenMediaBeforeAlbums(t *testing.T) {
	fixture := newRepairFixture(t, 0)
	ctx := context.Background()
	criticalMediaID, highMediaID := uuid.New(), uuid.New()
	criticalAssetID, highAssetID := uuid.New(), uuid.New()
	criticalAlbumID, highAlbumID := uuid.New(), uuid.New()
	eventID, momentID, snapshotID := uuid.New(), uuid.New(), uuid.New()
	publicationID, publishedMomentID := uuid.New(), uuid.New()
	_, err := fixture.db.NewRaw(`
		INSERT INTO source_albums (
			id, immich_album_id, name, asset_count, source_created_at, source_updated_at,
			first_seen_at, last_seen_at, source_fingerprint, source_missing, missing_since,
			next_reconciliation_at
		) VALUES
			(?, gen_random_uuid(), 'Critical album', 1, now(), now(), now(), now(), decode(repeat('21', 32), 'hex'), true, now() - interval '4 hours', now()),
			(?, gen_random_uuid(), 'High album', 1, now(), now(), now(), now(), decode(repeat('22', 32), 'hex'), true, now() - interval '3 hours', now());
		INSERT INTO media_items (
			id, immich_asset_id, media_type, availability, missing_since, first_seen_at, last_seen_at
		) VALUES
			(?, ?, 'image', 'source_missing', now() - interval '2 hours', now(), now()),
			(?, ?, 'image', 'source_missing', now() - interval '1 hour', now(), now());
		INSERT INTO media_backings (id, media_item_id, immich_asset_id, filename, linked_at)
		VALUES (gen_random_uuid(), ?, ?, 'Critical media.jpg', now()),
		       (gen_random_uuid(), ?, ?, 'High media.jpg', now());
		INSERT INTO source_album_memberships (
			source_album_id, immich_asset_id, media_item_id, first_seen_at, last_seen_at, source_fingerprint
		) VALUES (?, ?, ?, now(), now(), decode(repeat('23', 32), 'hex')),
		         (?, ?, ?, now(), now(), decode(repeat('24', 32), 'hex'));
		INSERT INTO events (id, lifecycle, title, grouping_timezone) VALUES (?, 'published', 'Published repair', 'UTC');
		INSERT INTO draft_moments (id, event_id, position, proposed_day, grouping_timezone)
		VALUES (?, ?, 0, '2026-01-01', 'UTC');
		INSERT INTO audience_snapshots (id, target_kind, target_id, approved_by_person_id, approved_at, label)
		VALUES (?, 'moment', ?, ?, now(), 'Curator only');
		INSERT INTO publications (id, event_id, revision, editable_version, published_by_person_id, notify_recipients, committed_at)
		VALUES (?, ?, 1, 1, ?, false, now());
		INSERT INTO published_moments (
			id, publication_id, draft_moment_id, audience_snapshot_id, position, title, proposed_day
		) VALUES (?, ?, ?, ?, 0, '', '2026-01-01');
		INSERT INTO current_published_placements (
			event_id, publication_id, published_moment_id, media_item_id, position
		) VALUES (?, ?, ?, ?, 0)
	`, criticalAlbumID, highAlbumID,
		criticalMediaID, criticalAssetID, highMediaID, highAssetID,
		criticalMediaID, criticalAssetID, highMediaID, highAssetID,
		criticalAlbumID, criticalAssetID, criticalMediaID,
		highAlbumID, highAssetID, highMediaID,
		eventID, momentID, eventID, snapshotID, momentID, fixture.actor.PersonID,
		publicationID, eventID, fixture.actor.PersonID,
		publishedMomentID, publicationID, momentID, snapshotID,
		eventID, publicationID, publishedMomentID, criticalMediaID).Exec(ctx)
	require.NoError(t, err)

	listed, err := fixture.service.List(ctx)
	require.NoError(t, err)
	require.Len(t, listed.SourceProblems, 4)
	assert.Equal(t, []string{"critical", "critical", "high", "high"}, []string{
		listed.SourceProblems[0].Priority, listed.SourceProblems[1].Priority,
		listed.SourceProblems[2].Priority, listed.SourceProblems[3].Priority,
	})
	assert.Equal(t, []string{"media_item", "source_album", "media_item", "source_album"}, []string{
		listed.SourceProblems[0].Kind, listed.SourceProblems[1].Kind,
		listed.SourceProblems[2].Kind, listed.SourceProblems[3].Kind,
	})
	assert.Equal(t, []string{"Critical media.jpg", "Critical album", "High media.jpg", "High album"}, []string{
		listed.SourceProblems[0].Label, listed.SourceProblems[1].Label,
		listed.SourceProblems[2].Label, listed.SourceProblems[3].Label,
	})
}

func TestRejectedMediaRepairLeavesAddRemoveAndBackingUntouched(t *testing.T) {
	fixture := newRepairFixture(t, 0)
	oldMediaID, newMediaID := uuid.New(), uuid.New()
	oldAssetID, newAssetID := uuid.New(), uuid.New()
	candidateID := uuid.New()
	_, err := fixture.db.ExecContext(context.Background(), `
		INSERT INTO media_items (id, immich_asset_id, media_type, local_date_time, availability, missing_since, first_seen_at, last_seen_at)
		VALUES (?, ?, 'image', '2026-01-01T00:00:00Z', 'source_missing', now(), now(), now()),
		       (?, ?, 'image', '2026-01-01T00:00:00Z', 'current', NULL, now(), now());
		INSERT INTO media_backings (id, media_item_id, immich_asset_id, checksum, linked_at)
		VALUES (gen_random_uuid(), ?, ?, ?, now()), (gen_random_uuid(), ?, ?, ?, now());
		INSERT INTO media_repair_candidates (id, media_item_id, candidate_media_item_id, previous_immich_asset_id, candidate_immich_asset_id, created_at)
		VALUES (?, ?, ?, ?, ?, now());
	`, oldMediaID, oldAssetID, newMediaID, newAssetID, oldMediaID, oldAssetID, checksum("same"), newMediaID, newAssetID, checksum("same"),
		candidateID, oldMediaID, newMediaID, oldAssetID, newAssetID)
	require.NoError(t, err)
	_, err = fixture.service.RejectMedia(context.Background(), fixture.actor, candidateID)
	require.NoError(t, err)
	var state string
	require.NoError(t, fixture.db.NewRaw(`SELECT state FROM media_repair_candidates WHERE id = ?`, candidateID).Scan(context.Background(), &state))
	assert.Equal(t, "rejected", state)
	var items, currentBackings int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM media_items WHERE id IN (?, ?)`, oldMediaID, newMediaID).Scan(context.Background(), &items))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM media_backings WHERE immich_asset_id IN (?, ?) AND active`, oldAssetID, newAssetID).Scan(context.Background(), &currentBackings))
	assert.Equal(t, 2, items)
	assert.Equal(t, 2, currentBackings)
}

func TestMediaConfirmationRevalidatesReplacementAvailability(t *testing.T) {
	fixture := newRepairFixture(t, 0)
	oldMediaID, newMediaID := uuid.New(), uuid.New()
	oldAssetID, newAssetID, candidateID := uuid.New(), uuid.New(), uuid.New()
	_, err := fixture.db.ExecContext(context.Background(), `
		INSERT INTO media_items (id, immich_asset_id, media_type, local_date_time, availability, missing_since, first_seen_at, last_seen_at)
		VALUES (?, ?, 'image', '2026-01-01T00:00:00Z', 'source_missing', now(), now(), now()),
		       (?, ?, 'image', '2026-01-01T00:00:00Z', 'current', NULL, now(), now());
		INSERT INTO media_backings (id, media_item_id, immich_asset_id, checksum, linked_at)
		VALUES (gen_random_uuid(), ?, ?, ?, now()), (gen_random_uuid(), ?, ?, ?, now());
		INSERT INTO media_repair_candidates (
			id, media_item_id, candidate_media_item_id, previous_immich_asset_id,
			candidate_immich_asset_id, previous_evidence, candidate_evidence, created_at
		) VALUES (?, ?, ?, ?, ?,
			jsonb_build_object('checksum', ?, 'capture', NULL, 'filename', '', 'path', ''),
			jsonb_build_object('checksum', ?, 'capture', NULL, 'filename', '', 'path', ''), now());
	`, oldMediaID, oldAssetID, newMediaID, newAssetID,
		oldMediaID, oldAssetID, checksum("same"), newMediaID, newAssetID, checksum("same"),
		candidateID, oldMediaID, newMediaID, oldAssetID, newAssetID, checksum("same"), checksum("same"))
	require.NoError(t, err)
	setFreshAssetFromBacking(t, fixture, newAssetID)
	reviewToken := reviewedMediaToken(t, fixture, candidateID)

	fixture.connector.assetErr = errors.New("malformed or unauthorized Immich response")
	_, err = fixture.service.ConfirmMedia(context.Background(), fixture.actor, candidateID, reviewToken)
	assert.ErrorIs(t, err, ErrDependency)
	fixture.connector.assetErr = nil
	fixture.connector.assetMissing = map[uuid.UUID]bool{newAssetID: true}
	_, err = fixture.service.ConfirmMedia(context.Background(), fixture.actor, candidateID, reviewToken)
	assert.ErrorIs(t, err, ErrConflict)
	fixture.connector.assetMissing[newAssetID] = false
	deliveryCallCount := func() int {
		fixture.connector.deliveryMu.Lock()
		defer fixture.connector.deliveryMu.Unlock()
		return len(fixture.connector.deliveryCalls)
	}

	_, err = fixture.db.NewRaw(`UPDATE media_repair_candidates SET candidate_evidence = '{}'::jsonb WHERE id = ?`, candidateID).Exec(context.Background())
	require.NoError(t, err)
	_, err = fixture.service.ConfirmMedia(context.Background(), fixture.actor, candidateID, reviewToken)
	assert.ErrorIs(t, err, ErrConflict, "malformed reviewed evidence remains pending")
	_, err = fixture.db.NewRaw(`UPDATE media_repair_candidates SET candidate_evidence = jsonb_build_object(
		'checksum', ?, 'capture', NULL, 'filename', '', 'path', ''
	) WHERE id = ?`, checksum("same"), candidateID).Exec(context.Background())
	require.NoError(t, err)

	_, err = fixture.db.NewRaw(`UPDATE media_repair_candidates SET candidate_evidence = jsonb_set(candidate_evidence, '{filename}', '"stale-review.jpg"') WHERE id = ?`, candidateID).Exec(context.Background())
	require.NoError(t, err)
	_, err = fixture.service.ConfirmMedia(context.Background(), fixture.actor, candidateID, reviewToken)
	assert.ErrorIs(t, err, ErrConflict, "the submitted token must bind the exact displayed evidence")
	_, err = fixture.db.NewRaw(`UPDATE media_repair_candidates SET candidate_evidence = jsonb_set(candidate_evidence, '{filename}', '""') WHERE id = ?`, candidateID).Exec(context.Background())
	require.NoError(t, err)

	baseAsset := fixture.connector.assets[newAssetID]
	for _, changed := range []struct {
		name   string
		mutate func(*immich.AssetSummary)
	}{
		{name: "checksum", mutate: func(asset *immich.AssetSummary) { asset.Checksum = checksum("changed upstream") }},
		{name: "media type", mutate: func(asset *immich.AssetSummary) { asset.MediaType = "video" }},
		{name: "path", mutate: func(asset *immich.AssetSummary) { asset.OriginalPath = "/unreviewed/path.jpg" }},
	} {
		t.Run("changed "+changed.name, func(t *testing.T) {
			before := deliveryCallCount()
			asset := baseAsset
			changed.mutate(&asset)
			fixture.connector.assets[newAssetID] = asset
			_, confirmErr := fixture.service.ConfirmMedia(context.Background(), fixture.actor, candidateID, reviewToken)
			assert.ErrorIs(t, confirmErr, ErrConflict)
			assert.Equal(t, before, deliveryCallCount(), "stale evidence is rejected before delivery probes")
		})
	}
	fixture.connector.assets[newAssetID] = baseAsset

	faceID := uuid.New()
	_, err = fixture.db.NewRaw(`UPDATE media_repair_candidates SET face_anchor_evidence = jsonb_build_array(jsonb_build_object(
		'face_id', ?, 'asset_id', ?, 'checksum', ?, 'image_width', 100, 'image_height', 80,
		'x1', 1, 'y1', 2, 'x2', 20, 'y2', 30
	)) WHERE id = ?`, faceID, newAssetID, checksum("same"), candidateID).Exec(context.Background())
	require.NoError(t, err)
	anchorToken := reviewedMediaToken(t, fixture, candidateID)
	fixture.connector.faces[newAssetID] = []immich.FaceSummary{{
		SourceID: faceID, ImageWidth: 100, ImageHeight: 80, X1: 1, Y1: 2, X2: 21, Y2: 30,
	}}
	beforeAnchor := deliveryCallCount()
	_, err = fixture.service.ConfirmMedia(context.Background(), fixture.actor, candidateID, anchorToken)
	assert.ErrorIs(t, err, ErrConflict)
	assert.Equal(t, beforeAnchor, deliveryCallCount())
	_, err = fixture.db.NewRaw(`UPDATE media_repair_candidates SET face_anchor_evidence = '[]'::jsonb WHERE id = ?`, candidateID).Exec(context.Background())
	require.NoError(t, err)

	harmlessAsset := baseAsset
	harmlessAsset.Filename = "renamed-after-review.jpg"
	harmlessAsset.CaptureAt = "2026-02-02T00:00:00Z"
	fixture.connector.assets[newAssetID] = harmlessAsset
	fixture.connector.deliveryErr = errors.New("malformed or unauthorized delivery response")
	_, err = fixture.service.ConfirmMedia(context.Background(), fixture.actor, candidateID, reviewToken)
	assert.ErrorIs(t, err, ErrDependency, "harmless mutable filename and capture metadata do not invalidate identity evidence")
	fixture.connector.deliveryErr = nil
	fixture.connector.assets[newAssetID] = baseAsset
	fixture.connector.setDeliveryAvailable(newAssetID, false)
	_, err = fixture.service.ConfirmMedia(context.Background(), fixture.actor, candidateID, reviewToken)
	assert.ErrorIs(t, err, ErrConflict)
	var state, availability string
	var activeAssetID uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT state FROM media_repair_candidates WHERE id = ?`, candidateID).Scan(context.Background(), &state))
	require.NoError(t, fixture.db.NewRaw(`SELECT availability FROM media_items WHERE id = ?`, oldMediaID).Scan(context.Background(), &availability))
	require.NoError(t, fixture.db.NewRaw(`SELECT immich_asset_id FROM media_backings WHERE media_item_id = ? AND active`, oldMediaID).Scan(context.Background(), &activeAssetID))
	assert.Equal(t, "pending", state)
	assert.Equal(t, "source_missing", availability)
	assert.Equal(t, oldAssetID, activeAssetID)
}

func TestMediaConfirmationRevalidatesAfterWaitingForConcurrencyBoundary(t *testing.T) {
	fixture := newRepairFixture(t, 0)
	oldMediaID, newMediaID := uuid.New(), uuid.New()
	oldAssetID, newAssetID, candidateID, sourceAlbumID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err := fixture.db.ExecContext(context.Background(), `
		INSERT INTO source_albums (
			id, immich_album_id, name, asset_count, source_created_at, source_updated_at,
			first_seen_at, last_seen_at, source_fingerprint, next_reconciliation_at
		) VALUES (?, gen_random_uuid(), 'Waiting candidate', 1, now(), now(), now(), now(), ?, now());
		INSERT INTO media_items (id, immich_asset_id, media_type, availability, missing_since, first_seen_at, last_seen_at)
		VALUES (?, ?, 'image', 'source_missing', now(), now(), now()),
		       (?, ?, 'image', 'current', NULL, now(), now());
		INSERT INTO media_backings (id, media_item_id, immich_asset_id, checksum, original_path, linked_at)
		VALUES (gen_random_uuid(), ?, ?, ?, '/old.jpg', now()),
		       (gen_random_uuid(), ?, ?, ?, '/new.jpg', now());
		INSERT INTO source_album_memberships (
			source_album_id, immich_asset_id, media_item_id, first_seen_at, last_seen_at, source_fingerprint
		) VALUES (?, ?, ?, now(), now(), ?);
		INSERT INTO media_repair_candidates (
			id, media_item_id, candidate_media_item_id, previous_immich_asset_id,
			candidate_immich_asset_id, previous_evidence, candidate_evidence, created_at
		) VALUES (?, ?, ?, ?, ?,
			jsonb_build_object('checksum', ?, 'capture', NULL, 'filename', '', 'path', '/old.jpg'),
			jsonb_build_object('checksum', ?, 'capture', NULL, 'filename', '', 'path', '/new.jpg'), now());
	`, sourceAlbumID, hashBytes("waiting-source"), oldMediaID, oldAssetID, newMediaID, newAssetID,
		oldMediaID, oldAssetID, checksum("waiting"), newMediaID, newAssetID, checksum("waiting"),
		sourceAlbumID, newAssetID, newMediaID, hashBytes("waiting-membership"),
		candidateID, oldMediaID, newMediaID, oldAssetID, newAssetID, checksum("waiting"), checksum("waiting"))
	require.NoError(t, err)
	setFreshAssetFromBacking(t, fixture, newAssetID)
	reviewToken := reviewedMediaToken(t, fixture, candidateID)

	holder, err := fixture.db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	require.NoError(t, staging.LockMediaOrganization(context.Background(), holder))
	var holderPID int
	require.NoError(t, holder.NewRaw(`SELECT pg_backend_pid()`).Scan(context.Background(), &holderPID))

	result := make(chan error, 1)
	go func() {
		_, confirmErr := fixture.service.ConfirmMedia(context.Background(), fixture.actor, candidateID, reviewToken)
		result <- confirmErr
	}()
	waitForRepairBlockedQuery(t, fixture.db, holderPID, `%pg_advisory_xact_lock(hashtextextended%`)
	fixture.connector.assetMissing = map[uuid.UUID]bool{newAssetID: true}
	require.NoError(t, holder.Rollback())
	select {
	case confirmErr := <-result:
		assert.ErrorIs(t, confirmErr, ErrConflict)
	case <-time.After(time.Second):
		t.Fatal("confirmation did not complete after the controlled lock was released")
	}

	var state, availability string
	var activeAssetID uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT state FROM media_repair_candidates WHERE id = ?`, candidateID).Scan(context.Background(), &state))
	require.NoError(t, fixture.db.NewRaw(`SELECT availability FROM media_items WHERE id = ?`, oldMediaID).Scan(context.Background(), &availability))
	require.NoError(t, fixture.db.NewRaw(`SELECT immich_asset_id FROM media_backings WHERE media_item_id = ? AND active`, oldMediaID).Scan(context.Background(), &activeAssetID))
	assert.Equal(t, "pending", state)
	assert.Equal(t, "source_missing", availability)
	assert.Equal(t, oldAssetID, activeAssetID)
}

func TestMediaConfirmationRejectsCandidateWithApprovedDraftAudience(t *testing.T) {
	fixture := newRepairFixture(t, 0)
	oldMediaID, candidateMediaID := uuid.New(), uuid.New()
	oldAssetID, candidateAssetID := uuid.New(), uuid.New()
	candidateID, sourceAlbumID, eventID, momentID, snapshotID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err := fixture.db.ExecContext(context.Background(), `
		INSERT INTO source_albums (
			id, immich_album_id, name, asset_count, source_created_at, source_updated_at,
			first_seen_at, last_seen_at, source_fingerprint, next_reconciliation_at
		) VALUES (?, gen_random_uuid(), 'Approved candidate', 1, now(), now(), now(), now(), ?, now());
		INSERT INTO media_items (id, immich_asset_id, media_type, availability, missing_since, first_seen_at, last_seen_at)
		VALUES (?, ?, 'image', 'source_missing', now(), now(), now()),
		       (?, ?, 'image', 'current', NULL, now(), now());
		INSERT INTO media_backings (id, media_item_id, immich_asset_id, checksum, original_path, linked_at)
		VALUES (gen_random_uuid(), ?, ?, ?, '/old-approved.jpg', now()),
		       (gen_random_uuid(), ?, ?, ?, '/candidate-approved.jpg', now());
		INSERT INTO source_album_memberships (
			source_album_id, immich_asset_id, media_item_id, first_seen_at, last_seen_at, source_fingerprint
		) VALUES (?, ?, ?, now(), now(), ?);
		INSERT INTO media_repair_candidates (
			id, media_item_id, candidate_media_item_id, previous_immich_asset_id,
			candidate_immich_asset_id, previous_evidence, candidate_evidence, created_at
		) VALUES (?, ?, ?, ?, ?,
			jsonb_build_object('checksum', ?, 'capture', NULL, 'filename', '', 'path', '/old-approved.jpg'),
			jsonb_build_object('checksum', ?, 'capture', NULL, 'filename', '', 'path', '/candidate-approved.jpg'), now());
		INSERT INTO events (id, title, grouping_timezone) VALUES (?, 'Approved candidate Event', 'UTC');
		INSERT INTO draft_moments (
			id, event_id, position, proposed_day, grouping_timezone, audience_complete
		) VALUES (?, ?, 0, '2026-01-01', 'UTC', true);
		INSERT INTO draft_media_placements (event_id, media_item_id, draft_moment_id, position)
		VALUES (?, ?, ?, 0);
		INSERT INTO audience_snapshots (id, target_kind, target_id, approved_by_person_id, approved_at, label)
		VALUES (?, 'moment', ?, ?, now(), 'Curator only');
		INSERT INTO current_audience_snapshots (target_kind, target_id, snapshot_id)
		VALUES ('moment', ?, ?)
	`, sourceAlbumID, hashBytes("approved-source"), oldMediaID, oldAssetID, candidateMediaID, candidateAssetID,
		oldMediaID, oldAssetID, checksum("approved"), candidateMediaID, candidateAssetID, checksum("approved"),
		sourceAlbumID, candidateAssetID, candidateMediaID, hashBytes("approved-membership"),
		candidateID, oldMediaID, candidateMediaID, oldAssetID, candidateAssetID, checksum("approved"), checksum("approved"),
		eventID, momentID, eventID, eventID, candidateMediaID, momentID,
		snapshotID, momentID, fixture.actor.PersonID, momentID, snapshotID)
	require.NoError(t, err)
	setFreshAssetFromBacking(t, fixture, candidateAssetID)
	reviewToken := reviewedMediaToken(t, fixture, candidateID)

	_, err = fixture.service.ConfirmMedia(context.Background(), fixture.actor, candidateID, reviewToken)
	assert.ErrorIs(t, err, ErrConflict)
	var state string
	var placedMediaID uuid.UUID
	var audienceComplete bool
	var currentSnapshotID uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT state FROM media_repair_candidates WHERE id = ?`, candidateID).Scan(context.Background(), &state))
	require.NoError(t, fixture.db.NewRaw(`SELECT media_item_id FROM draft_media_placements WHERE event_id = ?`, eventID).Scan(context.Background(), &placedMediaID))
	require.NoError(t, fixture.db.NewRaw(`SELECT audience_complete FROM draft_moments WHERE id = ?`, momentID).Scan(context.Background(), &audienceComplete))
	require.NoError(t, fixture.db.NewRaw(`SELECT snapshot_id FROM current_audience_snapshots WHERE target_kind = 'moment' AND target_id = ?`, momentID).Scan(context.Background(), &currentSnapshotID))
	assert.Equal(t, "pending", state)
	assert.Equal(t, candidateMediaID, placedMediaID, "approved candidate placement must not transfer")
	assert.True(t, audienceComplete)
	assert.Equal(t, snapshotID, currentSnapshotID)
}

func TestMediaConfirmationRejectsCrossTypeCandidateBeforeDeliveryProbe(t *testing.T) {
	fixture := newRepairFixture(t, 0)
	oldMediaID, newMediaID := uuid.New(), uuid.New()
	oldAssetID, newAssetID, candidateID := uuid.New(), uuid.New(), uuid.New()
	_, err := fixture.db.ExecContext(context.Background(), `
		INSERT INTO media_items (id, immich_asset_id, media_type, local_date_time, availability, missing_since, first_seen_at, last_seen_at)
		VALUES (?, ?, 'image', '2026-01-01T00:00:00Z', 'source_missing', now(), now(), now()),
		       (?, ?, 'video', '2026-01-01T00:00:00Z', 'current', NULL, now(), now());
		INSERT INTO media_backings (id, media_item_id, immich_asset_id, checksum, linked_at)
		VALUES (gen_random_uuid(), ?, ?, ?, now()), (gen_random_uuid(), ?, ?, ?, now());
		INSERT INTO media_repair_candidates (id, media_item_id, candidate_media_item_id, previous_immich_asset_id, candidate_immich_asset_id, created_at)
		VALUES (?, ?, ?, ?, ?, now());
	`, oldMediaID, oldAssetID, newMediaID, newAssetID,
		oldMediaID, oldAssetID, checksum("cross-type"), newMediaID, newAssetID, checksum("cross-type"),
		candidateID, oldMediaID, newMediaID, oldAssetID, newAssetID)
	require.NoError(t, err)

	_, err = fixture.service.ConfirmMedia(context.Background(), fixture.actor, candidateID, "")
	require.ErrorIs(t, err, ErrConflict)
	fixture.connector.deliveryMu.Lock()
	assert.Empty(t, fixture.connector.deliveryCalls, "cross-type stable metadata is rejected without probing the wrong representation contract")
	fixture.connector.deliveryMu.Unlock()
	var state string
	require.NoError(t, fixture.db.NewRaw(`SELECT state FROM media_repair_candidates WHERE id = ?`, candidateID).Scan(context.Background(), &state))
	assert.Equal(t, "pending", state)
}

func TestMediaConfirmationRejectsNonAdditionCandidateBackingAndHistory(t *testing.T) {
	for _, test := range []struct {
		name       string
		confirmed  bool
		addHistory bool
	}{
		{name: "confirmed backing from a prior relink", confirmed: true},
		{name: "candidate with backing history", addHistory: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRepairFixture(t, 0)
			oldMediaID, newMediaID := uuid.New(), uuid.New()
			oldAssetID, newAssetID, candidateID, sourceAlbumID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			_, err := fixture.db.ExecContext(context.Background(), `
				INSERT INTO source_albums (
					id, immich_album_id, name, asset_count, source_created_at, source_updated_at,
					first_seen_at, last_seen_at, source_fingerprint, next_reconciliation_at
				) VALUES (?, gen_random_uuid(), 'Candidate state', 1, now(), now(), now(), now(), ?, now());
				INSERT INTO media_items (id, immich_asset_id, media_type, availability, missing_since, first_seen_at, last_seen_at)
				VALUES (?, ?, 'image', 'source_missing', now(), now(), now()),
				       (?, ?, 'image', 'current', NULL, now(), now());
				INSERT INTO media_backings (id, media_item_id, immich_asset_id, checksum, linked_at)
				VALUES (gen_random_uuid(), ?, ?, ?, now());
				INSERT INTO media_backings (id, media_item_id, immich_asset_id, checksum, linked_at)
				VALUES (gen_random_uuid(), ?, ?, ?, now());
				INSERT INTO source_album_memberships (
					source_album_id, immich_asset_id, media_item_id, first_seen_at, last_seen_at, source_fingerprint
				) VALUES (?, ?, ?, now(), now(), ?);
				INSERT INTO media_repair_candidates (
					id, media_item_id, candidate_media_item_id, previous_immich_asset_id,
					candidate_immich_asset_id, previous_evidence, candidate_evidence, created_at
				) VALUES (?, ?, ?, ?, ?,
					jsonb_build_object('checksum', ?, 'capture', NULL, 'filename', '', 'path', ''),
					jsonb_build_object('checksum', ?, 'capture', NULL, 'filename', '', 'path', ''), now());
			`, sourceAlbumID, hashBytes("candidate-state"), oldMediaID, oldAssetID, newMediaID, newAssetID,
				oldMediaID, oldAssetID, checksum("candidate-state"),
				newMediaID, newAssetID, checksum("candidate-state"),
				sourceAlbumID, newAssetID, newMediaID, hashBytes("candidate-membership"),
				candidateID, oldMediaID, newMediaID, oldAssetID, newAssetID,
				checksum("candidate-state"), checksum("candidate-state"))
			require.NoError(t, err)
			if test.confirmed {
				_, err = fixture.db.NewRaw(`
					UPDATE media_backings SET state = 'confirmed', confirmed_at = now()
					WHERE media_item_id = ? AND active
				`, newMediaID).Exec(context.Background())
				require.NoError(t, err)
			}
			if test.addHistory {
				_, err = fixture.db.NewRaw(`
					INSERT INTO media_backings (
						id, media_item_id, immich_asset_id, checksum, active, linked_at, ended_at
					) VALUES (gen_random_uuid(), ?, gen_random_uuid(), ?, false, now() - interval '1 hour', now())
				`, newMediaID, checksum("old-candidate-history")).Exec(context.Background())
				require.NoError(t, err)
			}
			setFreshAssetFromBacking(t, fixture, newAssetID)
			reviewToken := reviewedMediaToken(t, fixture, candidateID)

			_, err = fixture.service.ConfirmMedia(context.Background(), fixture.actor, candidateID, reviewToken)
			assert.ErrorIs(t, err, ErrConflict)
			var state string
			var retainedItems int
			require.NoError(t, fixture.db.NewRaw(`SELECT state FROM media_repair_candidates WHERE id = ?`, candidateID).Scan(context.Background(), &state))
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM media_items WHERE id IN (?, ?)`, oldMediaID, newMediaID).Scan(context.Background(), &retainedItems))
			assert.Equal(t, "pending", state)
			assert.Equal(t, 2, retainedItems, "invalid candidate state returns a domain conflict without reaching a delete FK")
			fixture.connector.deliveryMu.Lock()
			assert.Empty(t, fixture.connector.deliveryCalls, "invalid persisted candidate state is rejected before dependency probes")
			fixture.connector.deliveryMu.Unlock()
		})
	}
}

func TestMediaConfirmationProbesWithoutHoldingGlobalOrMediaLocksAndRejectsChangedState(t *testing.T) {
	fixture := newRepairFixture(t, 0)
	oldMediaID, newMediaID := uuid.New(), uuid.New()
	oldAssetID, newAssetID, candidateID, sourceAlbumID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err := fixture.db.ExecContext(context.Background(), `
		INSERT INTO source_albums (
			id, immich_album_id, name, asset_count, source_created_at, source_updated_at,
			first_seen_at, last_seen_at, source_fingerprint, next_reconciliation_at
		) VALUES (?, gen_random_uuid(), 'Race album', 1, now(), now(), now(), now(), ?, now());
		INSERT INTO media_items (
			id, immich_asset_id, media_type, local_date_time, availability, missing_since, first_seen_at, last_seen_at
		) VALUES (?, ?, 'image', '2026-01-01T00:00:00Z', 'source_missing', now(), now(), now()),
		         (?, ?, 'image', '2026-01-01T00:00:00Z', 'current', NULL, now(), now());
		INSERT INTO media_backings (id, media_item_id, immich_asset_id, checksum, linked_at)
		VALUES (gen_random_uuid(), ?, ?, ?, now()), (gen_random_uuid(), ?, ?, ?, now());
		INSERT INTO source_album_memberships (
			source_album_id, immich_asset_id, media_item_id, first_seen_at, last_seen_at, source_fingerprint
		) VALUES (?, ?, ?, now(), now(), ?);
		INSERT INTO media_repair_candidates (
			id, media_item_id, candidate_media_item_id, previous_immich_asset_id,
			candidate_immich_asset_id, previous_evidence, candidate_evidence, created_at
		) VALUES (?, ?, ?, ?, ?,
			jsonb_build_object('checksum', ?, 'capture', NULL, 'filename', '', 'path', ''),
			jsonb_build_object('checksum', ?, 'capture', NULL, 'filename', '', 'path', ''), now());
	`, sourceAlbumID, hashBytes("race-album"), oldMediaID, oldAssetID, newMediaID, newAssetID,
		oldMediaID, oldAssetID, checksum("race"), newMediaID, newAssetID, checksum("race"),
		sourceAlbumID, newAssetID, newMediaID, hashBytes("race-membership"),
		candidateID, oldMediaID, newMediaID, oldAssetID, newAssetID, checksum("race"), checksum("race"))
	require.NoError(t, err)
	setFreshAssetFromBacking(t, fixture, newAssetID)
	reviewToken := reviewedMediaToken(t, fixture, candidateID)

	started := make(chan uuid.UUID, 1)
	release := make(chan struct{})
	fixture.connector.deliveryStarted = started
	fixture.connector.releaseDelivery = release
	confirmation := make(chan error, 1)
	go func() {
		_, confirmErr := fixture.service.ConfirmMedia(context.Background(), fixture.actor, candidateID, reviewToken)
		confirmation <- confirmErr
	}()
	select {
	case probedAssetID := <-started:
		assert.Equal(t, newAssetID, probedAssetID)
	case <-time.After(time.Second):
		t.Fatal("confirmation did not begin delivery evidence read")
	}

	mutationCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	mutation, err := fixture.db.BeginTx(mutationCtx, nil)
	require.NoError(t, err)
	require.NoError(t, staging.LockAccessSummaryReplacement(mutationCtx, mutation),
		"delivery evidence must not hold the global staged-access lock")
	_, err = mutation.NewRaw(`UPDATE media_items SET media_type = 'video' WHERE id = ?`, newMediaID).Exec(mutationCtx)
	require.NoError(t, err, "delivery evidence must not lock candidate Media")
	require.NoError(t, mutation.Commit())
	close(release)

	select {
	case confirmErr := <-confirmation:
		require.ErrorIs(t, confirmErr, ErrConflict)
	case <-time.After(time.Second):
		t.Fatal("confirmation did not reject state changed during delivery evidence")
	}
	var state, availability string
	var activeAssetID uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT state FROM media_repair_candidates WHERE id = ?`, candidateID).Scan(context.Background(), &state))
	require.NoError(t, fixture.db.NewRaw(`SELECT availability FROM media_items WHERE id = ?`, oldMediaID).Scan(context.Background(), &availability))
	require.NoError(t, fixture.db.NewRaw(`SELECT immich_asset_id FROM media_backings WHERE media_item_id = ? AND active`, oldMediaID).Scan(context.Background(), &activeAssetID))
	assert.Equal(t, "pending", state)
	assert.Equal(t, "source_missing", availability)
	assert.Equal(t, oldAssetID, activeAssetID)
}

func TestCompetingMediaConfirmationsHaveOneWinnerAndSupersedeOverlap(t *testing.T) {
	fixture := newRepairFixture(t, 0)
	stableMediaID, firstMediaID, secondMediaID := uuid.New(), uuid.New(), uuid.New()
	stableAssetID, firstAssetID, secondAssetID := uuid.New(), uuid.New(), uuid.New()
	firstCandidateID, secondCandidateID, sourceAlbumID := uuid.New(), uuid.New(), uuid.New()
	_, err := fixture.db.ExecContext(context.Background(), `
		INSERT INTO source_albums (
			id, immich_album_id, name, asset_count, source_created_at, source_updated_at,
			first_seen_at, last_seen_at, source_fingerprint, next_reconciliation_at
		) VALUES (?, gen_random_uuid(), 'Competing repair album', 2, now(), now(), now(), now(), ?, now());
		INSERT INTO media_items (id, immich_asset_id, media_type, local_date_time, availability, missing_since, first_seen_at, last_seen_at)
		VALUES (?, ?, 'image', '2026-01-01T00:00:00Z', 'source_missing', now(), now(), now()),
		       (?, ?, 'image', '2026-01-01T00:00:00Z', 'current', NULL, now(), now()),
		       (?, ?, 'image', '2026-01-01T00:00:00Z', 'current', NULL, now(), now());
		INSERT INTO media_backings (id, media_item_id, immich_asset_id, checksum, linked_at)
		VALUES (gen_random_uuid(), ?, ?, ?, now()), (gen_random_uuid(), ?, ?, ?, now()), (gen_random_uuid(), ?, ?, ?, now());
		INSERT INTO source_album_memberships (source_album_id, immich_asset_id, media_item_id, first_seen_at, last_seen_at, source_fingerprint)
		VALUES (?, ?, ?, now(), now(), ?), (?, ?, ?, now(), now(), ?);
		INSERT INTO media_repair_candidates (
			id, media_item_id, candidate_media_item_id, previous_immich_asset_id,
			candidate_immich_asset_id, previous_evidence, candidate_evidence, created_at
		) VALUES (?, ?, ?, ?, ?,
			jsonb_build_object('checksum', ?, 'capture', NULL, 'filename', '', 'path', ''),
			jsonb_build_object('checksum', ?, 'capture', NULL, 'filename', '', 'path', ''), now()),
			(?, ?, ?, ?, ?,
			jsonb_build_object('checksum', ?, 'capture', NULL, 'filename', '', 'path', ''),
			jsonb_build_object('checksum', ?, 'capture', NULL, 'filename', '', 'path', ''), now());
	`, sourceAlbumID, hashBytes("competing-album"),
		stableMediaID, stableAssetID, firstMediaID, firstAssetID, secondMediaID, secondAssetID,
		stableMediaID, stableAssetID, checksum("competing"), firstMediaID, firstAssetID, checksum("competing"), secondMediaID, secondAssetID, checksum("competing"),
		sourceAlbumID, firstAssetID, firstMediaID, hashBytes("first-membership"),
		sourceAlbumID, secondAssetID, secondMediaID, hashBytes("second-membership"),
		firstCandidateID, stableMediaID, firstMediaID, stableAssetID, firstAssetID, checksum("competing"), checksum("competing"),
		secondCandidateID, stableMediaID, secondMediaID, stableAssetID, secondAssetID, checksum("competing"), checksum("competing"))
	require.NoError(t, err)
	setFreshAssetFromBacking(t, fixture, firstAssetID)
	setFreshAssetFromBacking(t, fixture, secondAssetID)
	reviewTokens := map[uuid.UUID]string{
		firstCandidateID:  reviewedMediaToken(t, fixture, firstCandidateID),
		secondCandidateID: reviewedMediaToken(t, fixture, secondCandidateID),
	}

	started := make(chan uuid.UUID, 2)
	release := make(chan struct{})
	fixture.connector.deliveryStarted = started
	fixture.connector.releaseDelivery = release
	type result struct {
		candidateID uuid.UUID
		err         error
	}
	results := make(chan result, 2)
	for _, candidateID := range []uuid.UUID{firstCandidateID, secondCandidateID} {
		go func() {
			_, confirmErr := fixture.service.ConfirmMedia(context.Background(), fixture.actor, candidateID, reviewTokens[candidateID])
			results <- result{candidateID: candidateID, err: confirmErr}
		}()
	}
	probed := make(map[uuid.UUID]bool, 2)
	for range 2 {
		select {
		case assetID := <-started:
			probed[assetID] = true
		case <-time.After(time.Second):
			t.Fatal("both competing confirmations did not reach fresh delivery evidence")
		}
	}
	assert.Equal(t, map[uuid.UUID]bool{firstAssetID: true, secondAssetID: true}, probed)
	close(release)

	var winner uuid.UUID
	successes := 0
	for range 2 {
		select {
		case outcome := <-results:
			if outcome.err == nil {
				successes++
				winner = outcome.candidateID
			} else {
				assert.ErrorIs(t, outcome.err, ErrAlreadyResolved)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("competing confirmation did not finish")
		}
	}
	assert.Equal(t, 1, successes)
	winningAssetID := firstAssetID
	losingCandidateID := secondCandidateID
	if winner == secondCandidateID {
		winningAssetID = secondAssetID
		losingCandidateID = firstCandidateID
	}
	var actualAssetID, confirmedID uuid.UUID
	var availability string
	require.NoError(t, fixture.db.NewRaw(`SELECT immich_asset_id, availability FROM media_items WHERE id = ?`, stableMediaID).Scan(context.Background(), &actualAssetID, &availability))
	require.NoError(t, fixture.db.NewRaw(`SELECT id FROM media_repair_candidates WHERE state = 'confirmed'`).Scan(context.Background(), &confirmedID))
	assert.Equal(t, winningAssetID, actualAssetID)
	assert.Equal(t, "current", availability)
	assert.Equal(t, winner, confirmedID)
	var losingState string
	var losingCandidateMediaID uuid.NullUUID
	require.NoError(t, fixture.db.NewRaw(`SELECT state, candidate_media_item_id FROM media_repair_candidates WHERE id = ?`, losingCandidateID).Scan(context.Background(), &losingState, &losingCandidateMediaID))
	assert.Equal(t, "superseded", losingState)
	assert.False(t, losingCandidateMediaID.Valid)
}

func TestMediaConfirmationPreservesPortalIdentityAndMovesBacking(t *testing.T) {
	fixture := newRepairFixture(t, 0)
	oldMediaID, newMediaID := uuid.New(), uuid.New()
	oldAssetID, newAssetID := uuid.New(), uuid.New()
	oldBackingID, newBackingID, candidateID, sourceAlbumID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err := fixture.db.ExecContext(context.Background(), `
		INSERT INTO source_albums (
			id, immich_album_id, name, asset_count, source_created_at, source_updated_at,
			first_seen_at, last_seen_at, source_fingerprint, next_reconciliation_at
		) VALUES (?, gen_random_uuid(), 'Repair album', 2, now(), now(), now(), now(), ?, now());
		INSERT INTO media_items (id, immich_asset_id, media_type, local_date_time, availability, missing_since, first_seen_at, last_seen_at)
		VALUES (?, ?, 'image', '2026-01-01T00:00:00Z', 'source_missing', now(), now(), now()),
		       (?, ?, 'image', '2026-01-01T00:00:00Z', 'current', NULL, now(), now());
		INSERT INTO media_backings (id, media_item_id, immich_asset_id, checksum, capture_at, filename, original_path, linked_at)
		VALUES (?, ?, ?, ?, '2026-01-01T00:00:00Z', 'old.jpg', '/old/old.jpg', now()),
		       (?, ?, ?, ?, '2026-01-01T00:00:00Z', 'new.jpg', '/moved/new.jpg', now());
		INSERT INTO source_album_memberships (
			source_album_id, immich_asset_id, media_item_id, first_seen_at, last_seen_at, source_fingerprint
		) VALUES (?, ?, ?, now(), now(), ?), (?, ?, ?, now(), now(), ?);
		INSERT INTO media_repair_candidates (
			id, media_item_id, candidate_media_item_id, previous_immich_asset_id,
			candidate_immich_asset_id, previous_evidence, candidate_evidence, created_at
		) VALUES (
			?, ?, ?, ?, ?,
			jsonb_build_object('checksum', ?, 'capture', '2026-01-01T00:00:00Z', 'filename', 'old.jpg', 'path', '/old/old.jpg'),
			jsonb_build_object('checksum', ?, 'capture', '2026-01-01T00:00:00Z', 'filename', 'new.jpg', 'path', '/moved/new.jpg'),
			now()
		);
	`, sourceAlbumID, hashBytes("album"), oldMediaID, oldAssetID, newMediaID, newAssetID,
		oldBackingID, oldMediaID, oldAssetID, checksum("same"), newBackingID, newMediaID, newAssetID, checksum("same"),
		sourceAlbumID, oldAssetID, oldMediaID, hashBytes("stale-membership"),
		sourceAlbumID, newAssetID, newMediaID, hashBytes("membership"), candidateID, oldMediaID, newMediaID, oldAssetID, newAssetID,
		checksum("same"), checksum("same"))
	require.NoError(t, err)
	setFreshAssetFromBacking(t, fixture, newAssetID)
	reviewToken := reviewedMediaToken(t, fixture, candidateID)
	eventID, momentID, candidateLooseID, stableLooseID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err = fixture.db.ExecContext(context.Background(), `
		INSERT INTO events (id, title, grouping_timezone) VALUES (?, 'Repair draft', 'UTC');
		INSERT INTO draft_moments (
			id, event_id, position, proposed_day, grouping_timezone, cover_media_item_id
		) VALUES (?, ?, 0, '2026-01-01', 'UTC', ?);
		INSERT INTO draft_media_placements (event_id, media_item_id, draft_moment_id, position) VALUES (?, ?, ?, 0);
		INSERT INTO loose_items (id, media_item_id, grouping_timezone) VALUES (?, ?, 'UTC')
	`, eventID, momentID, eventID, newMediaID, eventID, newMediaID, momentID, candidateLooseID, newMediaID)
	require.NoError(t, err)
	_, err = fixture.db.ExecContext(context.Background(), `
		INSERT INTO draft_media_placements (event_id, media_item_id, position) VALUES (?, ?, 1)
	`, eventID, oldMediaID)
	require.NoError(t, err)
	_, err = fixture.service.ConfirmMedia(context.Background(), fixture.actor, candidateID, reviewToken)
	assert.ErrorIs(t, err, ErrConflict, "colliding Event placements require explicit Curator cleanup")
	var pendingState string
	var retainedItems int
	require.NoError(t, fixture.db.NewRaw(`SELECT state FROM media_repair_candidates WHERE id = ?`, candidateID).Scan(context.Background(), &pendingState))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM media_items WHERE id IN (?, ?)`, oldMediaID, newMediaID).Scan(context.Background(), &retainedItems))
	assert.Equal(t, "pending", pendingState)
	assert.Equal(t, 2, retainedItems)
	_, err = fixture.db.ExecContext(context.Background(), `DELETE FROM draft_media_placements WHERE event_id = ? AND media_item_id = ?`, eventID, oldMediaID)
	require.NoError(t, err)

	_, err = fixture.db.ExecContext(context.Background(), `
		INSERT INTO loose_items (id, media_item_id, grouping_timezone) VALUES (?, ?, 'UTC')
	`, stableLooseID, oldMediaID)
	require.NoError(t, err)
	_, err = fixture.service.ConfirmMedia(context.Background(), fixture.actor, candidateID, reviewToken)
	assert.ErrorIs(t, err, ErrConflict, "colliding Loose items require explicit Curator cleanup")
	require.NoError(t, fixture.db.NewRaw(`SELECT state FROM media_repair_candidates WHERE id = ?`, candidateID).Scan(context.Background(), &pendingState))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM media_items WHERE id IN (?, ?)`, oldMediaID, newMediaID).Scan(context.Background(), &retainedItems))
	assert.Equal(t, "pending", pendingState)
	assert.Equal(t, 2, retainedItems)
	_, err = fixture.db.ExecContext(context.Background(), `DELETE FROM loose_items WHERE id = ?`, stableLooseID)
	require.NoError(t, err)

	listed, err := fixture.service.List(context.Background())
	require.NoError(t, err)
	require.Len(t, listed.MediaCandidates, 1)
	assert.Equal(t, checksum("same"), listed.MediaCandidates[0].Previous.Checksum)
	assert.Equal(t, "/moved/new.jpg", listed.MediaCandidates[0].Candidate.Path)

	_, err = fixture.db.ExecContext(context.Background(), `UPDATE media_backings SET checksum = ? WHERE id = ?`, checksum("changed"), newBackingID)
	require.NoError(t, err)
	_, err = fixture.service.ConfirmMedia(context.Background(), fixture.actor, candidateID, reviewToken)
	assert.ErrorIs(t, err, ErrConflict)
	_, err = fixture.db.ExecContext(context.Background(), `UPDATE media_backings SET checksum = ? WHERE id = ?`, checksum("same"), newBackingID)
	require.NoError(t, err)

	publicationID, publishedMomentID, audienceSnapshotID := uuid.New(), uuid.New(), uuid.New()
	commentID := uuid.New()
	_, err = fixture.db.ExecContext(context.Background(), `
		INSERT INTO audience_snapshots (id, target_kind, target_id, approved_by_person_id, approved_at, label)
		VALUES (?, 'moment', ?, ?, now(), 'Curator only');
		INSERT INTO current_audience_snapshots (target_kind, target_id, snapshot_id)
		VALUES ('moment', ?, ?);
		INSERT INTO audience_snapshot_entries (snapshot_id, recipient_person_id, recipient_access_generation_id)
		VALUES (?, ?, ?);
		INSERT INTO publications (
			id, event_id, revision, editable_version, published_by_person_id, notify_recipients, committed_at
		) VALUES (?, ?, 1, 1, ?, false, now());
		INSERT INTO published_event_revisions (
			publication_id, event_id, title, description, grouping_timezone, created_at
		) VALUES (?, ?, 'Repair draft', '', 'UTC', now());
		INSERT INTO published_moments (
			id, publication_id, draft_moment_id, audience_snapshot_id, position,
			title, proposed_day, cover_media_item_id
		) VALUES (?, ?, ?, ?, 0, '', '2026-01-01', ?);
		INSERT INTO audience_entries (published_moment_id, recipient_person_id, recipient_access_generation_id)
		VALUES (?, ?, ?);
		INSERT INTO published_media_placements (
			published_moment_id, media_item_id, position, media_type, local_date_time
		) VALUES (?, ?, 0, 'image', '2026-01-01T00:00:00Z');
		INSERT INTO current_published_events (
			event_id, publication_id, title, description, grouping_timezone, committed_at
		) VALUES (?, ?, 'Repair draft', '', 'UTC', now());
		INSERT INTO current_published_placements (
			event_id, publication_id, published_moment_id, media_item_id, position
		) VALUES (?, ?, ?, ?, 0);
		UPDATE events SET lifecycle = 'published', current_publication_id = ? WHERE id = ?;
		INSERT INTO current_audience_entitlements (
			event_id, publication_id, recipient_person_id, recipient_access_generation_id, media_item_id
		) VALUES (?, ?, ?, ?, ?), (?, ?, ?, ?, ?);
		INSERT INTO favorites (recipient_person_id, media_item_id) VALUES (?, ?);
		INSERT INTO comments (
			id, media_item_id, author_person_id, author_access_generation_id, idempotency_key, body
		) VALUES (?, ?, ?, ?, gen_random_uuid(), 'Preserved comment');
		INSERT INTO staged_source_removals (
			event_id, media_item_id, draft_moment_id, position, was_cover, created_at
		) VALUES (?, ?, ?, 0, true, now())
	`, audienceSnapshotID, momentID, fixture.actor.PersonID, momentID, audienceSnapshotID,
		audienceSnapshotID, fixture.actor.PersonID, fixture.accessID,
		publicationID, eventID, fixture.actor.PersonID, publicationID, eventID,
		publishedMomentID, publicationID, momentID, audienceSnapshotID, oldMediaID,
		publishedMomentID, fixture.actor.PersonID, fixture.accessID,
		publishedMomentID, oldMediaID, eventID, publicationID,
		eventID, publicationID, publishedMomentID, oldMediaID, publicationID, eventID,
		eventID, publicationID, fixture.actor.PersonID, fixture.accessID, oldMediaID,
		eventID, publicationID, fixture.actor.PersonID, fixture.accessID, newMediaID,
		fixture.actor.PersonID, oldMediaID,
		commentID, oldMediaID, fixture.actor.PersonID, fixture.accessID,
		eventID, oldMediaID, momentID)
	require.NoError(t, err)
	var candidateEntitlementBefore string
	require.NoError(t, fixture.db.NewRaw(`
		SELECT to_jsonb(entitlement)::text FROM (
			SELECT event_id, publication_id, recipient_person_id, recipient_access_generation_id, media_item_id
			FROM current_audience_entitlements
			WHERE event_id = ? AND recipient_access_generation_id = ? AND media_item_id = ?
		) AS entitlement
	`, eventID, fixture.accessID, newMediaID).Scan(context.Background(), &candidateEntitlementBefore))
	require.NotEmpty(t, candidateEntitlementBefore)
	_, err = fixture.service.ConfirmMedia(context.Background(), fixture.actor, candidateID, reviewToken)
	assert.ErrorIs(t, err, ErrConflict, "a candidate's current authorization must never transfer to the stable Media identity")
	var pendingAfterAuthorizationConflict, candidateEntitlementAfter string
	require.NoError(t, fixture.db.NewRaw(`SELECT state FROM media_repair_candidates WHERE id = ?`, candidateID).Scan(context.Background(), &pendingAfterAuthorizationConflict))
	require.NoError(t, fixture.db.NewRaw(`
		SELECT to_jsonb(entitlement)::text FROM (
			SELECT event_id, publication_id, recipient_person_id, recipient_access_generation_id, media_item_id
			FROM current_audience_entitlements
			WHERE event_id = ? AND recipient_access_generation_id = ? AND media_item_id = ?
		) AS entitlement
	`, eventID, fixture.accessID, newMediaID).Scan(context.Background(), &candidateEntitlementAfter))
	assert.Equal(t, "pending", pendingAfterAuthorizationConflict)
	assert.Equal(t, candidateEntitlementBefore, candidateEntitlementAfter, "authorization conflict must leave the candidate entitlement present and unchanged")
	_, err = fixture.db.ExecContext(context.Background(), `
		DELETE FROM current_audience_entitlements
		WHERE event_id = ? AND recipient_access_generation_id = ? AND media_item_id = ?
	`, eventID, fixture.accessID, newMediaID)
	require.NoError(t, err)
	require.NoError(t, fixture.db.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
		_, refreshErr := staging.Refresh(ctx, tx, eventID, time.Now().UTC())
		return refreshErr
	}))
	beforeRepair, err := staging.Load(context.Background(), fixture.db, eventID)
	require.NoError(t, err)
	require.NotNil(t, beforeRepair, "the candidate identity initially appears as staged add/remove work")

	partialEventID, partialMomentID := uuid.New(), uuid.New()
	partialPublicationID, partialPublishedMomentID, partialAudienceSnapshotID := uuid.New(), uuid.New(), uuid.New()
	_, err = fixture.db.ExecContext(context.Background(), `
		INSERT INTO events (id, title, grouping_timezone, lifecycle)
		VALUES (?, 'Partial repair Event', 'UTC', 'published');
		INSERT INTO event_sources (
			event_id, source_album_id, source_order, initialized_name,
			initialized_description, initialized_at, include_future_media
		) VALUES (?, ?, 0, 'Repair album', '', now(), false);
		INSERT INTO draft_moments (
			id, event_id, position, proposed_day, grouping_timezone
		) VALUES (?, ?, 0, '2026-01-01', 'UTC');
		INSERT INTO audience_snapshots (id, target_kind, target_id, approved_by_person_id, approved_at, label)
		VALUES (?, 'moment', ?, ?, now(), 'Curator only');
		INSERT INTO current_audience_snapshots (target_kind, target_id, snapshot_id)
		VALUES ('moment', ?, ?);
		INSERT INTO publications (
			id, event_id, revision, editable_version, published_by_person_id, notify_recipients, committed_at
		) VALUES (?, ?, 1, 1, ?, false, now());
		INSERT INTO published_event_revisions (
			publication_id, event_id, title, description, grouping_timezone, created_at
		) VALUES (?, ?, 'Partial repair Event', '', 'UTC', now());
		INSERT INTO published_moments (
			id, publication_id, draft_moment_id, audience_snapshot_id, position,
			title, proposed_day, cover_media_item_id
		) VALUES (?, ?, ?, ?, 0, '', '2026-01-01', ?);
		INSERT INTO published_media_placements (
			published_moment_id, media_item_id, position, media_type, local_date_time
		) VALUES (?, ?, 7, 'image', '2026-01-01T00:00:00Z');
		INSERT INTO current_published_events (
			event_id, publication_id, title, description, grouping_timezone, committed_at
		) VALUES (?, ?, 'Partial repair Event', '', 'UTC', now());
		INSERT INTO current_published_placements (
			event_id, publication_id, published_moment_id, media_item_id, position
		) VALUES (?, ?, ?, ?, 7);
		UPDATE events SET current_publication_id = ? WHERE id = ?;
		INSERT INTO staged_source_removals (
			event_id, media_item_id, draft_moment_id, position, was_cover, created_at
		) VALUES (?, ?, ?, 7, true, now())
	`, partialEventID, partialEventID, sourceAlbumID, partialMomentID, partialEventID,
		partialAudienceSnapshotID, partialMomentID, fixture.actor.PersonID, partialMomentID, partialAudienceSnapshotID,
		partialPublicationID, partialEventID, fixture.actor.PersonID, partialPublicationID, partialEventID,
		partialPublishedMomentID, partialPublicationID, partialMomentID, partialAudienceSnapshotID, oldMediaID,
		partialPublishedMomentID, oldMediaID, partialEventID, partialPublicationID,
		partialEventID, partialPublicationID, partialPublishedMomentID, oldMediaID,
		partialPublicationID, partialEventID, partialEventID, oldMediaID, partialMomentID)
	require.NoError(t, err)
	require.NoError(t, fixture.db.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
		_, refreshErr := staging.Refresh(ctx, tx, partialEventID, time.Now().UTC())
		return refreshErr
	}))
	partialBeforeRepair, err := staging.Load(context.Background(), fixture.db, partialEventID)
	require.NoError(t, err)
	require.NotNil(t, partialBeforeRepair, "the partial-source Event starts with only stable-Media removal state")

	competingMediaID, competingAssetID, competingCandidateID := uuid.New(), uuid.New(), uuid.New()
	historyID, historicalCandidateAssetID := uuid.New(), uuid.New()
	_, err = fixture.db.ExecContext(context.Background(), `
		INSERT INTO media_items (id, immich_asset_id, media_type, local_date_time, availability, missing_since, first_seen_at, last_seen_at)
		VALUES (?, ?, 'image', '2026-01-01T00:00:00Z', 'source_missing', now(), now(), now());
		INSERT INTO media_backings (id, media_item_id, immich_asset_id, checksum, linked_at)
		VALUES (gen_random_uuid(), ?, ?, ?, now());
		INSERT INTO media_repair_candidates (
			id, media_item_id, candidate_media_item_id, previous_immich_asset_id,
			candidate_immich_asset_id, created_at
		) VALUES (?, ?, ?, ?, ?, now());
		INSERT INTO media_repair_candidates (
			id, media_item_id, previous_immich_asset_id, candidate_immich_asset_id,
			state, created_at, resolved_at, resolved_by_person_id
		) VALUES (?, ?, ?, ?, 'rejected', now(), now(), ?);
	`, competingMediaID, competingAssetID, competingMediaID, competingAssetID, checksum("same"),
		competingCandidateID, competingMediaID, newMediaID, competingAssetID, newAssetID,
		historyID, newMediaID, newAssetID, historicalCandidateAssetID, fixture.actor.PersonID)
	require.NoError(t, err)
	eventBoundary, err := fixture.db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	_, err = eventBoundary.NewRaw(`SELECT id FROM events WHERE id = ? FOR UPDATE`, eventID).Exec(context.Background())
	require.NoError(t, err)
	var eventBoundaryPID int
	require.NoError(t, eventBoundary.NewRaw(`SELECT pg_backend_pid()`).Scan(context.Background(), &eventBoundaryPID))

	coverID := newMediaID.String()
	organization := make(chan error, 1)
	go func() {
		_, organizeErr := events.New(fixture.db).OrganizeEvent(context.Background(), fixture.actor, eventID, events.OrganizeEventRequest{
			Version: 1,
			Moments: []events.OrganizeMoment{{
				ID: momentID.String(), ProposedDay: "2026-01-01", CoverMediaItemID: &coverID,
				MediaItemIDs: []string{newMediaID.String()},
			}},
		})
		organization <- organizeErr
	}()
	organizerPID := waitForRepairBlockedQuery(t, fixture.db, eventBoundaryPID, `%SELECT version, title, description, grouping_timezone%`)
	confirmation := make(chan error, 1)
	go func() {
		_, confirmErr := fixture.service.ConfirmMedia(context.Background(), fixture.actor, candidateID, reviewToken)
		confirmation <- confirmErr
	}()
	waitForRepairBlockedQuery(t, fixture.db, organizerPID, `%pg_advisory_xact_lock(hashtextextended%`)
	require.NoError(t, eventBoundary.Rollback())
	select {
	case organizeErr := <-organization:
		require.NoError(t, organizeErr, "Event organization completes before the waiting relink")
	case <-time.After(time.Second):
		t.Fatal("Event organization did not complete after its controlled Event lock was released")
	}
	select {
	case confirmErr := <-confirmation:
		require.NoError(t, confirmErr)
	case <-time.After(time.Second):
		t.Fatal("confirmation did not complete after Event organization")
	}
	var actualAssetID uuid.UUID
	var availability string
	require.NoError(t, fixture.db.NewRaw(`SELECT immich_asset_id, availability FROM media_items WHERE id = ?`, oldMediaID).Scan(context.Background(), &actualAssetID, &availability))
	assert.Equal(t, newAssetID, actualAssetID)
	assert.Equal(t, "current", availability)
	var candidateItemExists bool
	require.NoError(t, fixture.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM media_items WHERE id = ?)`, newMediaID).Scan(context.Background(), &candidateItemExists))
	assert.False(t, candidateItemExists)
	afterRepair, err := staging.Load(context.Background(), fixture.db, eventID)
	require.NoError(t, err)
	assert.Nil(t, afterRepair, "confirmed identity relink refreshes and cancels stale staged add/remove work")
	var restorationRows int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM staged_source_removals WHERE event_id = ?`, eventID).Scan(context.Background(), &restorationRows))
	assert.Zero(t, restorationRows, "the restored stable identity leaves no source-removal residue")
	partialAfterRepair, err := staging.Load(context.Background(), fixture.db, partialEventID)
	require.NoError(t, err)
	assert.Nil(t, partialAfterRepair, "repair restores a previously selected partial-source Media without staging a future addition")
	var partialRestorationRows int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM staged_source_removals WHERE event_id = ?`, partialEventID).Scan(context.Background(), &partialRestorationRows))
	assert.Zero(t, partialRestorationRows)
	var partialPlacementMediaID uuid.UUID
	var partialPlacementMomentID *uuid.UUID
	var partialPlacementPosition int
	require.NoError(t, fixture.db.NewRaw(`
		SELECT media_item_id, draft_moment_id, position FROM draft_media_placements WHERE event_id = ?
	`, partialEventID).Scan(context.Background(), &partialPlacementMediaID, &partialPlacementMomentID, &partialPlacementPosition))
	assert.Equal(t, oldMediaID, partialPlacementMediaID)
	require.NotNil(t, partialPlacementMomentID)
	assert.Equal(t, partialMomentID, *partialPlacementMomentID)
	assert.Equal(t, 7, partialPlacementPosition)
	var partialCoverMediaID *uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT cover_media_item_id FROM draft_moments WHERE id = ?`, partialMomentID).Scan(context.Background(), &partialCoverMediaID))
	require.NotNil(t, partialCoverMediaID)
	assert.Equal(t, oldMediaID, *partialCoverMediaID)
	var activeBackingItem uuid.UUID
	var backingState string
	require.NoError(t, fixture.db.NewRaw(`SELECT media_item_id, state FROM media_backings WHERE immich_asset_id = ? AND active`, newAssetID).Scan(context.Background(), &activeBackingItem, &backingState))
	assert.Equal(t, oldMediaID, activeBackingItem)
	assert.Equal(t, "confirmed", backingState)
	var membershipMediaID uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT media_item_id FROM source_album_memberships WHERE source_album_id = ? AND immich_asset_id = ?`, sourceAlbumID, newAssetID).Scan(context.Background(), &membershipMediaID))
	assert.Equal(t, oldMediaID, membershipMediaID)
	var staleMemberships int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM source_album_memberships WHERE source_album_id = ? AND immich_asset_id = ?`, sourceAlbumID, oldAssetID).Scan(context.Background(), &staleMemberships))
	assert.Zero(t, staleMemberships, "explicit relink retires stale source membership metadata")
	var placementMediaID, coverMediaID, looseMediaID uuid.UUID
	var looseVersion int64
	require.NoError(t, fixture.db.NewRaw(`SELECT media_item_id FROM draft_media_placements WHERE event_id = ?`, eventID).Scan(context.Background(), &placementMediaID))
	require.NoError(t, fixture.db.NewRaw(`SELECT cover_media_item_id FROM draft_moments WHERE id = ?`, momentID).Scan(context.Background(), &coverMediaID))
	require.NoError(t, fixture.db.NewRaw(`SELECT media_item_id, version FROM loose_items WHERE id = ?`, candidateLooseID).Scan(context.Background(), &looseMediaID, &looseVersion))
	assert.Equal(t, oldMediaID, placementMediaID)
	assert.Equal(t, oldMediaID, coverMediaID)
	assert.Equal(t, oldMediaID, looseMediaID)
	assert.Equal(t, int64(2), looseVersion)
	var publicationPlacements, entitlements, comments, favorites int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM published_media_placements WHERE media_item_id = ?`, oldMediaID).Scan(context.Background(), &publicationPlacements))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM current_audience_entitlements WHERE media_item_id = ?`, oldMediaID).Scan(context.Background(), &entitlements))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM comments WHERE id = ? AND media_item_id = ?`, commentID, oldMediaID).Scan(context.Background(), &comments))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM favorites WHERE recipient_person_id = ? AND media_item_id = ? AND is_current`, fixture.actor.PersonID, oldMediaID).Scan(context.Background(), &favorites))
	assert.Equal(t, 2, publicationPlacements)
	assert.Equal(t, 1, entitlements)
	assert.Equal(t, 1, comments)
	assert.Equal(t, 1, favorites)
	var publicationRevision int64
	require.NoError(t, fixture.db.NewRaw(`SELECT revision FROM publications WHERE id = ? AND event_id = ?`, publicationID, eventID).Scan(context.Background(), &publicationRevision))
	assert.Equal(t, int64(1), publicationRevision, "relink preserves immutable Publication history")
	var auditAction, auditPreviousAssetID, auditCandidateAssetID string
	var auditTargetID uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`
		SELECT target_id, action, metadata->>'previous_immich_asset_id', metadata->>'candidate_immich_asset_id'
		FROM publication_audit_events WHERE target_kind = 'media' AND target_id = ? AND action = 'media_relinked'
	`, oldMediaID).Scan(context.Background(), &auditTargetID, &auditAction, &auditPreviousAssetID, &auditCandidateAssetID))
	assert.Equal(t, oldMediaID, auditTargetID)
	assert.Equal(t, "media_relinked", auditAction)
	assert.Equal(t, oldAssetID.String(), auditPreviousAssetID)
	assert.Equal(t, newAssetID.String(), auditCandidateAssetID)
	var eventVersion int64
	require.NoError(t, fixture.db.NewRaw(`SELECT version FROM events WHERE id = ?`, eventID).Scan(context.Background(), &eventVersion))
	assert.Equal(t, int64(3), eventVersion, "organization and relink each advance the serialized Event state")
	_, err = events.New(fixture.db).OrganizeEvent(context.Background(), fixture.actor, eventID, events.OrganizeEventRequest{
		Version: 1, UnassignedMediaIDs: []string{newMediaID.String()},
	})
	assert.ErrorIs(t, err, events.ErrVersionConflict, "repair must make an open organization snapshot stale before validating its retired Media ID")
	var candidateState string
	var resolvedAt *time.Time
	require.NoError(t, fixture.db.NewRaw(`SELECT state, resolved_at FROM media_repair_candidates WHERE id = ?`, candidateID).Scan(context.Background(), &candidateState, &resolvedAt))
	assert.Equal(t, "confirmed", candidateState)
	assert.NotNil(t, resolvedAt)
	require.NoError(t, fixture.db.NewRaw(`SELECT state, resolved_at FROM media_repair_candidates WHERE id = ?`, competingCandidateID).Scan(context.Background(), &candidateState, &resolvedAt))
	assert.Equal(t, "superseded", candidateState)
	assert.NotNil(t, resolvedAt)
	var historyMediaID uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT media_item_id FROM media_repair_candidates WHERE id = ?`, historyID).Scan(context.Background(), &historyMediaID))
	assert.Equal(t, oldMediaID, historyMediaID, "resolved repair history follows the surviving portal Media identity")
	_, err = fixture.db.ExecContext(context.Background(), `UPDATE media_backings SET filename = 'later.jpg', original_path = '/later/path.jpg' WHERE media_item_id = ?`, oldMediaID)
	require.NoError(t, err)
	listed, err = fixture.service.List(context.Background())
	require.NoError(t, err)
	var confirmedHistory *MediaCandidate
	for index := range listed.MediaCandidates {
		if listed.MediaCandidates[index].ID == candidateID.String() {
			confirmedHistory = &listed.MediaCandidates[index]
			break
		}
	}
	require.NotNil(t, confirmedHistory)
	assert.Equal(t, "old.jpg", confirmedHistory.Previous.Filename)
	assert.Equal(t, "/moved/new.jpg", confirmedHistory.Candidate.Path)
	_, err = fixture.service.ConfirmMedia(context.Background(), fixture.actor, candidateID, reviewToken)
	assert.ErrorIs(t, err, ErrAlreadyResolved)
}
