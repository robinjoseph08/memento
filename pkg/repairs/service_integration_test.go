//go:build integration

package repairs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type identityConnector struct {
	people    []immich.PersonSummary
	faces     map[uuid.UUID][]immich.FaceSummary
	faceErrs  map[uuid.UUID]error
	faceCalls []uuid.UUID
	err       error
}

func (connector *identityConnector) Check(context.Context) error { return connector.err }
func (connector *identityConnector) People(context.Context) ([]immich.PersonSummary, error) {
	return connector.people, connector.err
}
func (connector *identityConnector) Faces(_ context.Context, assetID uuid.UUID) ([]immich.FaceSummary, error) {
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
	service.now = func() time.Time { return time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC) }
	actor := setup.CuratorSession{PersonID: actorID, SessionID: sessionID}
	require.NoError(t, reconcile(service))
	_, err = service.LinkPerson(context.Background(), actor, LinkPersonRequest{PersonID: personID.String(), ImmichPersonID: oldID.String()})
	require.NoError(t, err)
	return repairFixture{service: service, db: db, connector: connector, actor: actor, personID: personID, assetIDs: assetIDs, faceIDs: faceIDs, oldID: oldID}
}

func reconcile(service *Service) error {
	_, err := service.ReconcilePeople(context.Background())
	return err
}

func hashBytes(value string) []byte {
	hash := sha256.Sum256([]byte(value))
	return hash[:]
}

func checksum(value string) string { return hex.EncodeToString(hashBytes(value)[:20]) }

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
	_, err = fixture.service.ConfirmPerson(context.Background(), fixture.actor, candidateID)
	require.NoError(t, err)
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
	assert.Empty(t, listed.PersonCandidates[0].CandidateImmichPersonID)
	assert.Contains(t, listed.PersonCandidates[0].Conflicts, "anchors_split_across_people")
	suggestions, err := fixture.service.SuggestionPersonIDs(context.Background(), []uuid.UUID{fixture.oldID, other})
	require.NoError(t, err)
	assert.Empty(t, suggestions)
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

func TestRejectedMediaRepairLeavesAddRemoveAndBackingUntouched(t *testing.T) {
	fixture := newRepairFixture(t, 0)
	oldMediaID, newMediaID := uuid.New(), uuid.New()
	oldAssetID, newAssetID := uuid.New(), uuid.New()
	candidateID := uuid.New()
	_, err := fixture.db.ExecContext(context.Background(), `
		INSERT INTO media_items (id, immich_asset_id, media_type, local_date_time, availability, first_seen_at, last_seen_at)
		VALUES (?, ?, 'image', '2026-01-01T00:00:00Z', 'source_missing', now(), now()),
		       (?, ?, 'image', '2026-01-01T00:00:00Z', 'current', now(), now());
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

func TestMediaConfirmationPreservesPortalIdentityAndMovesBacking(t *testing.T) {
	fixture := newRepairFixture(t, 0)
	oldMediaID, newMediaID := uuid.New(), uuid.New()
	oldAssetID, newAssetID := uuid.New(), uuid.New()
	oldBackingID, newBackingID, candidateID, sourceAlbumID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err := fixture.db.ExecContext(context.Background(), `
		INSERT INTO source_albums (
			id, immich_album_id, name, asset_count, source_created_at, source_updated_at,
			first_seen_at, last_seen_at, source_fingerprint, next_reconciliation_at
		) VALUES (?, gen_random_uuid(), 'Repair album', 1, now(), now(), now(), now(), ?, now());
		INSERT INTO media_items (id, immich_asset_id, media_type, local_date_time, availability, first_seen_at, last_seen_at)
		VALUES (?, ?, 'image', '2026-01-01T00:00:00Z', 'source_missing', now(), now()),
		       (?, ?, 'image', '2026-01-01T00:00:00Z', 'current', now(), now());
		INSERT INTO media_backings (id, media_item_id, immich_asset_id, checksum, capture_at, filename, original_path, linked_at)
		VALUES (?, ?, ?, ?, '2026-01-01T00:00:00Z', 'old.jpg', '/old/old.jpg', now()),
		       (?, ?, ?, ?, '2026-01-01T00:00:00Z', 'new.jpg', '/moved/new.jpg', now());
		INSERT INTO source_album_memberships (
			source_album_id, immich_asset_id, media_item_id, first_seen_at, last_seen_at, source_fingerprint
		) VALUES (?, ?, ?, now(), now(), ?);
		INSERT INTO media_repair_candidates (id, media_item_id, candidate_media_item_id, previous_immich_asset_id, candidate_immich_asset_id, created_at)
		VALUES (?, ?, ?, ?, ?, now());
	`, sourceAlbumID, hashBytes("album"), oldMediaID, oldAssetID, newMediaID, newAssetID,
		oldBackingID, oldMediaID, oldAssetID, checksum("same"), newBackingID, newMediaID, newAssetID, checksum("same"),
		sourceAlbumID, newAssetID, newMediaID, hashBytes("membership"), candidateID, oldMediaID, newMediaID, oldAssetID, newAssetID)
	require.NoError(t, err)

	listed, err := fixture.service.List(context.Background())
	require.NoError(t, err)
	require.Len(t, listed.MediaCandidates, 1)
	assert.Equal(t, checksum("same"), listed.MediaCandidates[0].Previous.Checksum)
	assert.Equal(t, "/moved/new.jpg", listed.MediaCandidates[0].Candidate.Path)

	_, err = fixture.db.ExecContext(context.Background(), `UPDATE media_backings SET checksum = ? WHERE id = ?`, checksum("changed"), newBackingID)
	require.NoError(t, err)
	_, err = fixture.service.ConfirmMedia(context.Background(), fixture.actor, candidateID)
	assert.ErrorIs(t, err, ErrConflict)
	_, err = fixture.db.ExecContext(context.Background(), `UPDATE media_backings SET checksum = ? WHERE id = ?`, checksum("same"), newBackingID)
	require.NoError(t, err)
	_, err = fixture.service.ConfirmMedia(context.Background(), fixture.actor, candidateID)
	require.NoError(t, err)
	var actualAssetID uuid.UUID
	var availability string
	require.NoError(t, fixture.db.NewRaw(`SELECT immich_asset_id, availability FROM media_items WHERE id = ?`, oldMediaID).Scan(context.Background(), &actualAssetID, &availability))
	assert.Equal(t, newAssetID, actualAssetID)
	assert.Equal(t, "current", availability)
	var candidateItemExists bool
	require.NoError(t, fixture.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM media_items WHERE id = ?)`, newMediaID).Scan(context.Background(), &candidateItemExists))
	assert.False(t, candidateItemExists)
	var activeBackingItem uuid.UUID
	var backingState string
	require.NoError(t, fixture.db.NewRaw(`SELECT media_item_id, state FROM media_backings WHERE immich_asset_id = ? AND active`, newAssetID).Scan(context.Background(), &activeBackingItem, &backingState))
	assert.Equal(t, oldMediaID, activeBackingItem)
	assert.Equal(t, "confirmed", backingState)
	var membershipMediaID uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT media_item_id FROM source_album_memberships WHERE source_album_id = ? AND immich_asset_id = ?`, sourceAlbumID, newAssetID).Scan(context.Background(), &membershipMediaID))
	assert.Equal(t, oldMediaID, membershipMediaID)
	var candidateState string
	var resolvedAt *time.Time
	require.NoError(t, fixture.db.NewRaw(`SELECT state, resolved_at FROM media_repair_candidates WHERE id = ?`, candidateID).Scan(context.Background(), &candidateState, &resolvedAt))
	assert.Equal(t, "confirmed", candidateState)
	assert.NotNil(t, resolvedAt)
	_, err = fixture.service.ConfirmMedia(context.Background(), fixture.actor, candidateID)
	assert.ErrorIs(t, err, ErrAlreadyResolved)
}
