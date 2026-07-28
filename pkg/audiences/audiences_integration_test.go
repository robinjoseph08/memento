//go:build integration

package audiences

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/staging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type faceConnector struct {
	faces map[uuid.UUID][]immich.FaceSummary
	err   error
}

func (connector faceConnector) Faces(_ context.Context, id uuid.UUID) ([]immich.FaceSummary, error) {
	if connector.err != nil {
		return nil, connector.err
	}
	return connector.faces[id], nil
}

type audienceFixture struct {
	db                                     *bun.DB
	actor                                  setup.CuratorSession
	momentID, looseID, mediaID, assetID    uuid.UUID
	immichPersonID, knownFace, unknownFace uuid.UUID
	people                                 map[string]uuid.UUID
	access                                 map[string]uuid.UUID
	service                                *Service
}

func newAudienceFixture(t *testing.T) audienceFixture {
	t.Helper()
	ctx := context.Background()
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(ctx, db))
	f := audienceFixture{db: db, actor: setup.CuratorSession{PersonID: uuid.New(), SessionID: uuid.New()}, momentID: uuid.New(), looseID: uuid.New(), mediaID: uuid.New(), assetID: uuid.New(), people: map[string]uuid.UUID{}, access: map[string]uuid.UUID{}}
	for _, name := range []string{"present", "interested", "both", "manual", "suspended", "attended", "attended2", "relationship"} {
		f.people[name] = uuid.New()
	}
	_, err := db.NewRaw(`INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Curator', 'Curator')`, f.actor.PersonID).Exec(ctx)
	require.NoError(t, err)
	_, err = db.NewRaw(`INSERT INTO person_roles (person_id, role) VALUES (?, 'curator'), (?, 'recipient')`, f.actor.PersonID, f.actor.PersonID).Exec(ctx)
	require.NoError(t, err)
	for _, name := range []string{"present", "interested", "both", "manual", "suspended"} {
		id, accessID := f.people[name], uuid.New()
		f.access[name] = accessID
		state := "completed"
		if name == "suspended" {
			state = "suspended"
		}
		_, err = db.NewRaw(`INSERT INTO people (id, display_name, sort_name) VALUES (?, ?, ?); INSERT INTO person_roles (person_id, role) VALUES (?, 'recipient'); INSERT INTO recipient_access_generations (id, person_id, generation, state, onboarding_completed_at) VALUES (?, ?, 1, ?, CASE WHEN ? = 'completed' THEN now() ELSE NULL END)`, id, name, name, id, accessID, id, state, state).Exec(ctx)
		require.NoError(t, err)
	}
	for _, name := range []string{"attended", "attended2", "relationship"} {
		_, err = db.NewRaw(`INSERT INTO people (id, display_name, sort_name) VALUES (?, ?, ?)`, f.people[name], name, name).Exec(ctx)
		require.NoError(t, err)
	}
	curatorAccess := uuid.New()
	_, err = db.NewRaw(`INSERT INTO recipient_access_generations (id, person_id, generation, state, onboarding_completed_at) VALUES (?, ?, 1, 'completed', now()); INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at) SELECT ?, decode(repeat('12', 32), 'hex'), ?, ?, security_epoch, 'trusted', now() + interval '1 day' FROM system_settings WHERE id = 1`, curatorAccess, f.actor.PersonID, f.actor.SessionID, f.actor.PersonID, curatorAccess).Exec(ctx)
	require.NoError(t, err)
	_, err = db.NewRaw(`INSERT INTO interest_list_entries (recipient_person_id, selected_person_id, state, chosen_at, updated_at) VALUES (?, ?, 'active', now(), now()), (?, ?, 'active', now(), now()), (?, ?, 'active', now(), now())`, f.people["interested"], f.people["attended"], f.people["both"], f.people["attended"], f.people["both"], f.people["attended2"]).Exec(ctx)
	require.NoError(t, err)
	// Relationships and Visibility circles are intentionally present but do not participate in proposal calculation.
	circle := uuid.New()
	_, err = db.NewRaw(`INSERT INTO visibility_circles (id, name) VALUES (?, 'Unrelated'); INSERT INTO visibility_circle_members (circle_id, person_id) VALUES (?, ?), (?, ?)`, circle, circle, f.people["relationship"], circle, f.people["manual"]).Exec(ctx)
	require.NoError(t, err)
	eventID := uuid.New()
	_, err = db.NewRaw(`INSERT INTO events (id, title, grouping_timezone) VALUES (?, 'Review event', 'UTC'); INSERT INTO draft_moments (id, event_id, position, proposed_day, grouping_timezone, source_days) VALUES (?, ?, 0, '2026-01-01', 'UTC', ARRAY['2026-01-01'::date]); INSERT INTO media_items (id, immich_asset_id, media_type, first_seen_at, last_seen_at) VALUES (?, ?, 'image', now(), now()); INSERT INTO draft_media_placements (event_id, media_item_id, draft_moment_id, position) VALUES (?, ?, ?, 0); INSERT INTO loose_items (id, media_item_id, grouping_timezone) VALUES (?, ?, 'UTC')`, eventID, f.momentID, eventID, f.mediaID, f.assetID, eventID, f.mediaID, f.momentID, f.looseID, f.mediaID).Exec(ctx)
	require.NoError(t, err)
	_, err = db.NewRaw(`INSERT INTO media_backings (id, media_item_id, immich_asset_id, state, linked_at, confirmed_at) VALUES (?, ?, ?, 'confirmed', now(), now())`, uuid.New(), f.mediaID, f.assetID).Exec(ctx)
	require.NoError(t, err)
	f.immichPersonID = uuid.New()
	_, err = db.NewRaw(`INSERT INTO immich_person_links (person_id, immich_person_id, state, confirmed_at, confirmed_by_person_id) VALUES (?, ?, 'linked', now(), ?)`, f.people["attended"], f.immichPersonID, f.actor.PersonID).Exec(ctx)
	require.NoError(t, err)
	f.knownFace, f.unknownFace = uuid.New(), uuid.New()
	f.service = New(db, faceConnector{faces: map[uuid.UUID][]immich.FaceSummary{f.assetID: {{SourceID: f.knownFace, PersonID: &f.immichPersonID, ImageWidth: 100, ImageHeight: 80, X1: 1, Y1: 2, X2: 20, Y2: 30}, {SourceID: f.unknownFace, ImageWidth: 100, ImageHeight: 80, X1: 3, Y1: 4, X2: 25, Y2: 35}}}})
	f.service.now = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	return f
}

func attendanceRequest(ids ...string) AttendanceRequest {
	return AttendanceRequest{PersonIDs: &ids}
}

func TestConfirmedAttendanceBuildsExplainedProposalWithoutTrustingAdvisoryInputs(t *testing.T) {
	f := newAudienceFixture(t)
	ctx := context.Background()
	before, err := f.service.ReviewMoment(ctx, f.momentID)
	require.NoError(t, err)
	require.Len(t, before.FaceEvidence, 2)
	encoded, err := json.Marshal(before)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), f.assetID.String(), "Curator evidence uses portal Media and opaque evidence identities")
	assert.NotContains(t, string(encoded), f.immichPersonID.String())
	assert.NotContains(t, string(encoded), f.knownFace.String())
	assert.NotContains(t, string(encoded), f.unknownFace.String())
	for _, evidence := range before.FaceEvidence {
		assert.Regexp(t, `^[0-9a-f]{64}$`, evidence.EvidenceID)
	}
	again, err := f.service.ReviewMoment(ctx, f.momentID)
	require.NoError(t, err)
	assert.Equal(t, []string{before.FaceEvidence[0].EvidenceID, before.FaceEvidence[1].EvidenceID}, []string{again.FaceEvidence[0].EvidenceID, again.FaceEvidence[1].EvidenceID})
	assert.Empty(t, before.Attendance)
	assert.Empty(t, before.Proposal)
	var suggested, unmatched int
	for _, evidence := range before.FaceEvidence {
		if evidence.SuggestedPerson == nil {
			unmatched++
		} else {
			suggested++
		}
	}
	assert.Equal(t, 1, suggested)
	assert.Equal(t, 1, unmatched)
	_, err = f.db.NewRaw(`UPDATE events SET final_review_complete = true WHERE id = (SELECT event_id FROM draft_moments WHERE id = ?)`, f.momentID).Exec(ctx)
	require.NoError(t, err)
	var eventVersionBefore int64
	require.NoError(t, f.db.NewRaw(`SELECT version FROM events WHERE id = (SELECT event_id FROM draft_moments WHERE id = ?)`, f.momentID).Scan(ctx, &eventVersionBefore))
	review, err := f.service.ConfirmAttendance(ctx, f.actor, f.momentID, before.Version, attendanceRequest(f.people["present"].String(), f.people["both"].String(), f.people["attended"].String(), f.people["attended2"].String()))
	require.NoError(t, err)
	var eventVersionAfter int64
	var finalReviewComplete bool
	require.NoError(t, f.db.NewRaw(`SELECT version, final_review_complete FROM events WHERE id = (SELECT event_id FROM draft_moments WHERE id = ?)`, f.momentID).Scan(ctx, &eventVersionAfter, &finalReviewComplete))
	assert.Equal(t, eventVersionBefore+1, eventVersionAfter, "Audience work advances the editable Event version")
	assert.False(t, finalReviewComplete, "Audience work invalidates final review")
	byName := map[string]ProposalRecipient{}
	for _, proposal := range review.Proposal {
		byName[proposal.Recipient.DisplayName] = proposal
	}
	assert.ElementsMatch(t, []string{"present", "interested", "both"}, mapKeys(byName))
	assert.True(t, byName["present"].Included)
	assert.Equal(t, []string{"present", "interested", "interested"}, reasonKinds(byName["both"]))
	assert.ElementsMatch(t, []string{f.people["attended"].String(), f.people["attended2"].String()}, []string{byName["both"].Reasons[1].MatchingPerson.ID, byName["both"].Reasons[2].MatchingPerson.ID})
	assert.NotContains(t, byName, "Curator")
	assert.NotContains(t, byName, "suspended")
	assert.NotContains(t, byName, "manual")
	assert.NotContains(t, byName, "relationship")
}

func TestOverridesSurviveRecalculationAndApprovedSnapshotsStayFixed(t *testing.T) {
	f := newAudienceFixture(t)
	ctx := context.Background()
	review, err := f.service.ConfirmAttendance(ctx, f.actor, f.momentID, 1, attendanceRequest(f.people["present"].String(), f.people["attended"].String()))
	require.NoError(t, err)
	review, err = f.service.SetOverride(ctx, f.actor, targetMoment, f.momentID, review.Version, OverrideRequest{RecipientPersonID: f.people["interested"].String(), State: "included"})
	require.NoError(t, err)
	assert.Equal(t, []string{"interested", "manually_included"}, reasonKinds(findProposal(t, review, "interested")))
	review, err = f.service.SetOverride(ctx, f.actor, targetMoment, f.momentID, review.Version, OverrideRequest{RecipientPersonID: f.people["present"].String(), State: "excluded"})
	require.NoError(t, err)
	review, err = f.service.SetOverride(ctx, f.actor, targetMoment, f.momentID, review.Version, OverrideRequest{RecipientPersonID: f.people["manual"].String(), State: "included"})
	require.NoError(t, err)
	assert.Equal(t, []string{"present", "manually_excluded"}, reasonKinds(findProposal(t, review, "present")))
	assert.Equal(t, []string{"manually_included"}, reasonKinds(findProposal(t, review, "manual")))
	approved, err := f.service.Approve(ctx, f.actor, targetMoment, f.momentID, review.Version)
	require.NoError(t, err)
	assert.Equal(t, "Shared", approved.Audience.Label)
	assert.ElementsMatch(t, []string{"both", "interested", "manual"}, personNames(approved.Audience.Recipients))
	review, err = f.service.ConfirmAttendance(ctx, f.actor, f.momentID, approved.Version, attendanceRequest(f.people["both"].String()))
	require.NoError(t, err)
	recalculated, err := f.service.Recalculate(ctx, f.actor, targetMoment, f.momentID, review.Version)
	require.NoError(t, err)
	assert.False(t, findProposal(t, recalculated, "present").Included)
	assert.True(t, findProposal(t, recalculated, "manual").Included)
	assert.ElementsMatch(t, []string{"both", "interested", "manual"}, personNames(recalculated.ApprovedAudience.Recipients), "approved entries must not recalculate from later Attendance or Interest changes")
	var lifecycle, availability string
	require.NoError(t, f.db.NewRaw(`SELECT event.lifecycle, media.availability FROM draft_moments AS moment JOIN events AS event ON event.id = moment.event_id JOIN draft_media_placements AS placement ON placement.draft_moment_id = moment.id JOIN media_items AS media ON media.id = placement.media_item_id WHERE moment.id = ?`, f.momentID).Scan(ctx, &lifecycle, &availability))
	assert.Equal(t, "draft", lifecycle)
	assert.Equal(t, "current", availability, "Attendance, proposal inputs, and approved draft snapshots do not publish or authorize Media")
}

func TestPublishedEventAudienceChangeStaysPrivateAndCancelsWhenRestored(t *testing.T) {
	f := newAudienceFixture(t)
	ctx := context.Background()

	review, err := f.service.ConfirmAttendance(ctx, f.actor, f.momentID, 1, attendanceRequest(f.people["present"].String()))
	require.NoError(t, err)
	original, err := f.service.Approve(ctx, f.actor, targetMoment, f.momentID, review.Version)
	require.NoError(t, err)
	assert.Equal(t, []string{"present"}, personNames(original.Audience.Recipients))

	var eventID uuid.UUID
	require.NoError(t, f.db.NewRaw(`SELECT event_id FROM draft_moments WHERE id = ?`, f.momentID).Scan(ctx, &eventID))
	publicationID, publishedMomentID := uuid.New(), uuid.New()
	originalSnapshotID := uuid.MustParse(original.Audience.ID)
	_, err = f.db.NewRaw(`
		INSERT INTO publications (
			id, event_id, revision, editable_version, published_by_person_id,
			notify_recipients, committed_at
		) VALUES (?, ?, 1, ?, ?, true, now());
		INSERT INTO published_event_revisions (
			publication_id, event_id, title, description, grouping_timezone, created_at
		) SELECT ?, id, title, description, grouping_timezone, now() FROM events WHERE id = ?;
		INSERT INTO published_moments (
			id, publication_id, draft_moment_id, audience_snapshot_id, position,
			title, proposed_day, cover_media_item_id
		) SELECT ?, ?, id, ?, position, title, proposed_day, cover_media_item_id
		  FROM draft_moments WHERE id = ?;
		INSERT INTO published_media_placements (
			published_moment_id, media_item_id, position, media_type, width, height, local_date_time
		) SELECT ?, media.id, placement.position, media.media_type, media.width, media.height, media.local_date_time
		  FROM draft_media_placements AS placement
		  JOIN media_items AS media ON media.id = placement.media_item_id
		  WHERE placement.event_id = ? AND placement.media_item_id = ?;
		INSERT INTO audience_entries (
			published_moment_id, recipient_person_id, recipient_access_generation_id
		) VALUES (?, ?, ?);
		INSERT INTO current_published_events (
			event_id, publication_id, title, description, grouping_timezone, committed_at
		) SELECT id, ?, title, description, grouping_timezone, now() FROM events WHERE id = ?;
		INSERT INTO current_published_placements (
			event_id, publication_id, published_moment_id, media_item_id, position
		) VALUES (?, ?, ?, ?, 0);
		INSERT INTO current_audience_entitlements (
			event_id, publication_id, recipient_person_id,
			recipient_access_generation_id, media_item_id
		) VALUES (?, ?, ?, ?, ?);
		UPDATE events SET lifecycle = 'published', current_publication_id = ? WHERE id = ?
	`, publicationID, eventID, original.Version, f.actor.PersonID,
		publicationID, eventID, publishedMomentID, publicationID, originalSnapshotID, f.momentID,
		publishedMomentID, eventID, f.mediaID, publishedMomentID, f.people["present"], f.access["present"],
		publicationID, eventID, eventID, publicationID, publishedMomentID, f.mediaID,
		eventID, publicationID, f.people["present"], f.access["present"], f.mediaID,
		publicationID, eventID).Exec(ctx)
	require.NoError(t, err)

	review, err = f.service.SetOverride(ctx, f.actor, targetMoment, f.momentID, original.Version, OverrideRequest{RecipientPersonID: f.people["present"].String(), State: "excluded"})
	require.NoError(t, err)
	review, err = f.service.SetOverride(ctx, f.actor, targetMoment, f.momentID, review.Version, OverrideRequest{RecipientPersonID: f.people["manual"].String(), State: "included"})
	require.NoError(t, err)
	changed, err := f.service.Approve(ctx, f.actor, targetMoment, f.momentID, review.Version)
	require.NoError(t, err)
	assert.Equal(t, []string{"manual"}, personNames(changed.Audience.Recipients))

	update, err := staging.Load(ctx, f.db, eventID)
	require.NoError(t, err)
	require.NotNil(t, update)
	require.Len(t, update.Changes, 1)
	assert.Equal(t, staging.ChangeKindAccess, update.Changes[0].Kind)
	assert.Equal(t, 1, update.Changes[0].Count)
	assert.Equal(t, []string{f.momentID.String()}, update.Changes[0].MomentIDs)
	var stagedRows int
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM staged_updates WHERE event_id = ?`, eventID).Scan(ctx, &stagedRows))
	assert.Equal(t, 1, stagedRows)
	var oldPublished, oldCurrent, newPublished, newCurrent bool
	require.NoError(t, f.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM audience_entries WHERE published_moment_id = ? AND recipient_access_generation_id = ?)`, publishedMomentID, f.access["present"]).Scan(ctx, &oldPublished))
	require.NoError(t, f.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM current_audience_entitlements WHERE event_id = ? AND recipient_access_generation_id = ?)`, eventID, f.access["present"]).Scan(ctx, &oldCurrent))
	require.NoError(t, f.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM audience_entries WHERE published_moment_id = ? AND recipient_access_generation_id = ?)`, publishedMomentID, f.access["manual"]).Scan(ctx, &newPublished))
	require.NoError(t, f.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM current_audience_entitlements WHERE event_id = ? AND recipient_access_generation_id = ?)`, eventID, f.access["manual"]).Scan(ctx, &newCurrent))
	assert.True(t, oldPublished)
	assert.True(t, oldCurrent, "the old Audience remains active until Publication")
	assert.False(t, newPublished)
	assert.False(t, newCurrent, "the replacement Audience remains private until Publication")

	review, err = f.service.SetOverride(ctx, f.actor, targetMoment, f.momentID, changed.Version, OverrideRequest{RecipientPersonID: f.people["present"].String(), State: "automatic"})
	require.NoError(t, err)
	review, err = f.service.SetOverride(ctx, f.actor, targetMoment, f.momentID, review.Version, OverrideRequest{RecipientPersonID: f.people["manual"].String(), State: "automatic"})
	require.NoError(t, err)
	restored, err := f.service.Approve(ctx, f.actor, targetMoment, f.momentID, review.Version)
	require.NoError(t, err)
	assert.Equal(t, []string{"present"}, personNames(restored.Audience.Recipients))
	cancelled, err := staging.Load(ctx, f.db, eventID)
	require.NoError(t, err)
	assert.Nil(t, cancelled)
	var stagedPointer *uuid.UUID
	require.NoError(t, f.db.NewRaw(`SELECT current_staged_update_id FROM events WHERE id = ?`, eventID).Scan(ctx, &stagedPointer))
	assert.Nil(t, stagedPointer)
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM staged_updates WHERE event_id = ?`, eventID).Scan(ctx, &stagedRows))
	assert.Zero(t, stagedRows, "restoring the published Audience removes empty Staged work")
}

func TestMomentAndLooseItemCanApproveExplicitlyEmptyCuratorOnlyAudiences(t *testing.T) {
	f := newAudienceFixture(t)
	ctx := context.Background()
	review, err := f.service.ConfirmAttendance(ctx, f.actor, f.momentID, 1, attendanceRequest())
	require.NoError(t, err)
	moment, err := f.service.Approve(ctx, f.actor, targetMoment, f.momentID, review.Version)
	require.NoError(t, err)
	assert.Equal(t, "Curator only", moment.Audience.Label)
	assert.Empty(t, moment.Audience.Recipients)
	looseReview, err := f.service.Recalculate(ctx, f.actor, targetLoose, f.looseID, 1)
	require.NoError(t, err)
	loose, err := f.service.Approve(ctx, f.actor, targetLoose, f.looseID, looseReview.Version)
	require.NoError(t, err)
	assert.Equal(t, "Curator only", loose.Audience.Label)
	assert.Empty(t, loose.Audience.Recipients)
	var momentComplete, looseComplete bool
	require.NoError(t, f.db.NewRaw(`SELECT attendance_complete AND audience_complete FROM draft_moments WHERE id = ?`, f.momentID).Scan(ctx, &momentComplete))
	require.NoError(t, f.db.NewRaw(`SELECT audience_complete FROM loose_items WHERE id = ?`, f.looseID).Scan(ctx, &looseComplete))
	assert.True(t, momentComplete)
	assert.True(t, looseComplete)
}

func TestAttendanceAndOverrideFailuresLeaveReviewStateUnchanged(t *testing.T) {
	f := newAudienceFixture(t)
	ctx := context.Background()
	_, err := f.service.ConfirmAttendance(ctx, f.actor, f.momentID, 1, attendanceRequest(f.people["present"].String(), f.people["present"].String()))
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = f.service.ConfirmAttendance(ctx, f.actor, f.momentID, 1, attendanceRequest(uuid.NewString()))
	assert.ErrorIs(t, err, ErrPersonUnavailable)
	_, err = f.service.SetOverride(ctx, f.actor, targetMoment, f.momentID, 1, OverrideRequest{RecipientPersonID: f.people["suspended"].String(), State: "included"})
	assert.ErrorIs(t, err, ErrRecipientIneligible)
	_, err = f.service.Recalculate(ctx, f.actor, targetMoment, uuid.New(), 1)
	assert.ErrorIs(t, err, ErrNotFound)
	review, err := f.service.ReviewMoment(ctx, f.momentID)
	require.NoError(t, err)
	assert.Empty(t, review.Attendance)
	assert.Empty(t, review.Proposal)
	assert.Nil(t, review.ApprovedAudience)
}

func TestConcurrentSameVersionMutationsChooseExactlyOneWinner(t *testing.T) {
	f := newAudienceFixture(t)
	ctx := context.Background()
	blocker, err := f.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	var version int64
	require.NoError(t, blocker.NewRaw(`SELECT review_version FROM draft_moments WHERE id = ? FOR UPDATE`, f.momentID).Scan(ctx, &version))

	resultErrors := make(chan error, 2)
	for _, person := range []uuid.UUID{f.people["present"], f.people["both"]} {
		person := person
		go func() {
			_, err := f.service.ConfirmAttendance(ctx, f.actor, f.momentID, version, attendanceRequest(person.String()))
			resultErrors <- err
		}()
	}
	time.Sleep(100 * time.Millisecond)
	assert.Empty(t, resultErrors, "both mutations must wait for the target row lock")
	require.NoError(t, blocker.Commit())
	first, second := <-resultErrors, <-resultErrors
	assert.True(t, (first == nil && errors.Is(second, ErrStale)) || (second == nil && errors.Is(first, ErrStale)))
	review, err := f.service.ReviewMoment(ctx, f.momentID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), review.Version)
	assert.Len(t, review.Attendance, 1)
	var audits int
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM publication_audit_events WHERE action = 'attendance_confirmed' AND target_id = ?`, f.momentID).Scan(ctx, &audits))
	assert.Equal(t, 1, audits)
}

func TestStaleReviewMutationsCannotReplaceNewerCuratorDecisions(t *testing.T) {
	f := newAudienceFixture(t)
	ctx := context.Background()
	current, err := f.service.ConfirmAttendance(ctx, f.actor, f.momentID, 1, attendanceRequest(f.people["present"].String()))
	require.NoError(t, err)
	_, err = f.service.ConfirmAttendance(ctx, f.actor, f.momentID, 1, attendanceRequest(f.people["both"].String()))
	assert.ErrorIs(t, err, ErrStale)
	reloaded, err := f.service.ReviewMoment(ctx, f.momentID)
	require.NoError(t, err)
	assert.Equal(t, current.Version, reloaded.Version)
	assert.Equal(t, []string{"present"}, personNames(reloaded.Attendance))
}

func TestMomentApprovalRequiresConfirmedAttendanceAndChangesRequireReapproval(t *testing.T) {
	f := newAudienceFixture(t)
	ctx := context.Background()
	_, err := f.service.Approve(ctx, f.actor, targetMoment, f.momentID, 1)
	assert.ErrorIs(t, err, ErrAttendanceUnconfirmed)
	review, err := f.service.ConfirmAttendance(ctx, f.actor, f.momentID, 1, attendanceRequest())
	require.NoError(t, err)
	approved, err := f.service.Approve(ctx, f.actor, targetMoment, f.momentID, review.Version)
	require.NoError(t, err)
	changed, err := f.service.ConfirmAttendance(ctx, f.actor, f.momentID, approved.Version, attendanceRequest(f.people["present"].String()))
	require.NoError(t, err)
	assert.False(t, changed.AudienceComplete)
	assert.NotNil(t, changed.ApprovedAudience, "the immutable previous approval remains available for history")
}

func TestRecalculationKeepsProposalAndReasonsConsistentDuringInterestChange(t *testing.T) {
	f := newAudienceFixture(t)
	ctx := context.Background()
	_, err := f.db.NewRaw(`DELETE FROM interest_list_entries WHERE recipient_person_id = ?`, f.people["both"]).Exec(ctx)
	require.NoError(t, err)
	review, err := f.service.ConfirmAttendance(ctx, f.actor, f.momentID, 1, attendanceRequest(f.people["attended"].String()))
	require.NoError(t, err)
	require.Equal(t, []string{"interested"}, reasonKinds(findProposal(t, review, "interested")))

	const advisoryKey = 360037
	connection, err := f.db.DB.Conn(ctx)
	require.NoError(t, err)
	defer connection.Close()
	_, err = connection.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, advisoryKey)
	require.NoError(t, err)
	defer func() { _, _ = connection.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, advisoryKey) }()
	_, err = f.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE FUNCTION pause_audience_proposal_insert() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM pg_advisory_xact_lock(%d);
			RETURN NULL;
		END $$;
		CREATE TRIGGER pause_audience_proposal_insert AFTER INSERT ON audience_proposals
		FOR EACH STATEMENT EXECUTE FUNCTION pause_audience_proposal_insert()
	`, advisoryKey))
	require.NoError(t, err)

	recalculated := make(chan Review, 1)
	recalculationErrors := make(chan error, 1)
	go func() {
		result, err := f.service.Recalculate(ctx, f.actor, targetMoment, f.momentID, review.Version)
		recalculated <- result
		recalculationErrors <- err
	}()
	require.Eventually(t, func() bool {
		var waiters int
		err := f.db.NewRaw(`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND objid = ? AND NOT granted`, advisoryKey).Scan(ctx, &waiters)
		return err == nil && waiters > 0
	}, 3*time.Second, 20*time.Millisecond)

	_, err = f.db.NewRaw(`DELETE FROM interest_list_entries WHERE recipient_person_id = ? AND selected_person_id = ?`, f.people["interested"], f.people["attended"]).Exec(ctx)
	require.NoError(t, err)
	_, err = connection.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, advisoryKey)
	require.NoError(t, err)
	require.NoError(t, <-recalculationErrors)
	result := <-recalculated
	proposal := findProposal(t, result, "interested")
	assert.True(t, proposal.Included)
	assert.Equal(t, []string{"interested"}, reasonKinds(proposal))

	approved, err := f.service.Approve(ctx, f.actor, targetMoment, f.momentID, result.Version)
	require.NoError(t, err)
	assert.Equal(t, []string{"interested"}, personNames(approved.Audience.Recipients))
}

func TestApprovalRejectsIncludedProposalWithoutDisplayedReason(t *testing.T) {
	f := newAudienceFixture(t)
	ctx := context.Background()
	review, err := f.service.ConfirmAttendance(ctx, f.actor, f.momentID, 1, attendanceRequest())
	require.NoError(t, err)
	_, err = f.db.NewRaw(`INSERT INTO audience_proposals (target_kind, target_id, recipient_person_id, recipient_access_generation_id, included, recalculated_at) VALUES ('moment', ?, ?, ?, true, now())`, f.momentID, f.people["manual"], f.access["manual"]).Exec(ctx)
	require.NoError(t, err)

	_, err = f.service.Approve(ctx, f.actor, targetMoment, f.momentID, review.Version)
	assert.ErrorIs(t, err, ErrProposalStale)
	var snapshots int
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM audience_snapshots WHERE target_kind = 'moment' AND target_id = ?`, f.momentID).Scan(ctx, &snapshots))
	assert.Zero(t, snapshots)
}

func TestIneligibleOverrideDoesNotBlockApprovalAndCanBeCleared(t *testing.T) {
	f := newAudienceFixture(t)
	ctx := context.Background()
	review, err := f.service.SetOverride(ctx, f.actor, targetLoose, f.looseID, 1, OverrideRequest{RecipientPersonID: f.people["manual"].String(), State: "included"})
	require.NoError(t, err)
	_, err = f.db.NewRaw(`UPDATE people SET archived_at = now() WHERE id = ?`, f.people["manual"]).Exec(ctx)
	require.NoError(t, err)
	review, err = f.service.Recalculate(ctx, f.actor, targetLoose, f.looseID, review.Version)
	require.NoError(t, err)
	assert.Empty(t, review.Proposal)
	approved, err := f.service.Approve(ctx, f.actor, targetLoose, f.looseID, review.Version)
	require.NoError(t, err)
	assert.Equal(t, "Curator only", approved.Audience.Label)

	review, err = f.service.SetOverride(ctx, f.actor, targetLoose, f.looseID, approved.Version, OverrideRequest{RecipientPersonID: f.people["manual"].String(), State: "automatic"})
	require.NoError(t, err)
	assert.Empty(t, review.Proposal)
	var overrides int
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM audience_overrides WHERE target_kind = 'loose_item' AND target_id = ?`, f.looseID).Scan(ctx, &overrides))
	assert.Zero(t, overrides)
}

func TestApprovalRejectsProposalWhenRecipientEligibilityChanged(t *testing.T) {
	f := newAudienceFixture(t)
	ctx := context.Background()
	review, err := f.service.ConfirmAttendance(ctx, f.actor, f.momentID, 1, attendanceRequest(f.people["present"].String()))
	require.NoError(t, err)
	_, err = f.db.NewRaw(`UPDATE people SET archived_at = now() WHERE id = ?`, f.people["present"]).Exec(ctx)
	require.NoError(t, err)
	_, err = f.service.Approve(ctx, f.actor, targetMoment, f.momentID, review.Version)
	assert.ErrorIs(t, err, ErrProposalStale)
	var snapshots, approvalAudits int
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM audience_snapshots WHERE target_kind = 'moment' AND target_id = ?`, f.momentID).Scan(ctx, &snapshots))
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM publication_audit_events WHERE action = 'audience_approved' AND target_id = ?`, f.momentID).Scan(ctx, &approvalAudits))
	assert.Zero(t, snapshots)
	assert.Zero(t, approvalAudits)
	reloaded, err := f.service.ReviewMoment(ctx, f.momentID)
	require.NoError(t, err)
	assert.Equal(t, review.Version, reloaded.Version)
	assert.False(t, reloaded.AudienceComplete)
}

func TestApprovalUsesPersonBeforeAccessLockOrder(t *testing.T) {
	f := newAudienceFixture(t)
	ctx := context.Background()
	review, err := f.service.ConfirmAttendance(ctx, f.actor, f.momentID, 1, attendanceRequest(f.people["present"].String()))
	require.NoError(t, err)
	blocker, err := f.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = blocker.NewRaw(`SELECT id FROM people WHERE id = ? FOR UPDATE`, f.people["present"]).Exec(ctx)
	require.NoError(t, err)

	approvalResult := make(chan error, 1)
	go func() {
		_, err := f.service.Approve(ctx, f.actor, targetMoment, f.momentID, review.Version)
		approvalResult <- err
	}()
	time.Sleep(100 * time.Millisecond)
	assert.Empty(t, approvalResult, "approval must wait for the Person lock before taking the access lock")
	_, err = blocker.NewRaw(`SELECT id FROM recipient_access_generations WHERE id = ? FOR UPDATE`, f.access["present"]).Exec(ctx)
	require.NoError(t, err, "Person lifecycle work must take the access lock without deadlocking against approval")
	_, err = blocker.NewRaw(`UPDATE people SET archived_at = now() WHERE id = ?`, f.people["present"]).Exec(ctx)
	require.NoError(t, err)
	require.NoError(t, blocker.Commit())
	assert.ErrorIs(t, <-approvalResult, ErrProposalStale)
}

func TestArchivedAttendeesCanBeRemovedFromAttendance(t *testing.T) {
	f := newAudienceFixture(t)
	ctx := context.Background()
	review, err := f.service.ConfirmAttendance(ctx, f.actor, f.momentID, 1, attendanceRequest(f.people["present"].String()))
	require.NoError(t, err)
	_, err = f.db.NewRaw(`UPDATE people SET archived_at = now() WHERE id = ?`, f.people["present"]).Exec(ctx)
	require.NoError(t, err)
	reloaded, err := f.service.ReviewMoment(ctx, f.momentID)
	require.NoError(t, err)
	assert.Empty(t, reloaded.Attendance)
	cleared, err := f.service.ConfirmAttendance(ctx, f.actor, f.momentID, review.Version, attendanceRequest())
	require.NoError(t, err)
	assert.Empty(t, cleared.Attendance)
}

func TestAudienceChangesUsePublicationAuditHistory(t *testing.T) {
	f := newAudienceFixture(t)
	ctx := context.Background()
	review, err := f.service.ConfirmAttendance(ctx, f.actor, f.momentID, 1, attendanceRequest())
	require.NoError(t, err)
	_, err = f.service.Approve(ctx, f.actor, targetMoment, f.momentID, review.Version)
	require.NoError(t, err)
	type auditRow struct {
		EventID, TargetID, ActorID uuid.UUID
		Action                     string
		Metadata                   map[string]any
	}
	var publicationAudits []auditRow
	require.NoError(t, f.db.NewRaw(`SELECT event_id, target_id, actor_person_id AS actor_id, action, metadata FROM publication_audit_events WHERE target_kind = 'moment' AND target_id = ? ORDER BY id`, f.momentID).Scan(ctx, &publicationAudits))
	require.Len(t, publicationAudits, 2)
	assert.Equal(t, []string{"attendance_confirmed", "audience_approved"}, []string{publicationAudits[0].Action, publicationAudits[1].Action})
	for _, audit := range publicationAudits {
		assert.Equal(t, f.momentID, audit.TargetID)
		assert.Equal(t, f.actor.PersonID, audit.ActorID)
		assert.NotEqual(t, uuid.Nil, audit.EventID)
		assert.NotEmpty(t, audit.Metadata)
	}
	var securityAudits int
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM security_audit_events WHERE action IN ('attendance_confirmed', 'audience_approved')`).Scan(ctx, &securityAudits))
	assert.Zero(t, securityAudits)
}

func TestLooseItemSharedAudienceAndRepeatedApprovalsRemainImmutable(t *testing.T) {
	f := newAudienceFixture(t)
	ctx := context.Background()
	review, err := f.service.SetOverride(ctx, f.actor, targetLoose, f.looseID, 1, OverrideRequest{RecipientPersonID: f.people["manual"].String(), State: "included"})
	require.NoError(t, err)
	review, err = f.service.Recalculate(ctx, f.actor, targetLoose, f.looseID, review.Version)
	require.NoError(t, err)
	first, err := f.service.Approve(ctx, f.actor, targetLoose, f.looseID, review.Version)
	require.NoError(t, err)
	assert.Equal(t, "Shared", first.Audience.Label)
	assert.Equal(t, []string{"manual"}, personNames(first.Audience.Recipients))

	review, err = f.service.SetOverride(ctx, f.actor, targetLoose, f.looseID, first.Version, OverrideRequest{RecipientPersonID: f.people["manual"].String(), State: "excluded"})
	require.NoError(t, err)
	second, err := f.service.Approve(ctx, f.actor, targetLoose, f.looseID, review.Version)
	require.NoError(t, err)
	assert.NotEqual(t, first.Audience.ID, second.Audience.ID)
	assert.Equal(t, "Curator only", second.Audience.Label)
	var firstEntries, secondEntries int
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM audience_snapshot_entries WHERE snapshot_id = ?`, first.Audience.ID).Scan(ctx, &firstEntries))
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM audience_snapshot_entries WHERE snapshot_id = ?`, second.Audience.ID).Scan(ctx, &secondEntries))
	assert.Equal(t, 1, firstEntries)
	assert.Zero(t, secondEntries)
}

func TestFacePersonLookupFailureMarksEvidenceUnavailable(t *testing.T) {
	f := newAudienceFixture(t)
	require.NoError(t, f.db.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, `DROP TABLE immich_person_links CASCADE`)
		return err
	}))
	review, err := f.service.ReviewMoment(context.Background(), f.momentID)
	require.NoError(t, err)
	assert.False(t, review.FaceEvidenceAvailable)
	assert.Empty(t, review.FaceEvidence)
}

func TestFaceEvidenceFailureDoesNotTurnSuggestionsIntoAttendance(t *testing.T) {
	f := newAudienceFixture(t)
	f.service.connector = faceConnector{err: errors.New("offline")}
	review, err := f.service.ReviewMoment(context.Background(), f.momentID)
	require.NoError(t, err)
	assert.False(t, review.FaceEvidenceAvailable)
	assert.Empty(t, review.FaceEvidence)
	assert.Empty(t, review.Attendance)
	assert.Empty(t, review.Proposal)
}

func findProposal(t *testing.T, review Review, name string) ProposalRecipient {
	t.Helper()
	for _, proposal := range review.Proposal {
		if proposal.Recipient.DisplayName == name {
			return proposal
		}
	}
	t.Fatalf("proposal %s not found", name)
	return ProposalRecipient{}
}
func reasonKinds(proposal ProposalRecipient) []string {
	result := []string{}
	for _, reason := range proposal.Reasons {
		result = append(result, reason.Kind)
	}
	return result
}
func personNames(people []Person) []string {
	result := []string{}
	for _, person := range people {
		result = append(result, person.DisplayName)
	}
	return result
}
func mapKeys(values map[string]ProposalRecipient) []string {
	result := []string{}
	for key := range values {
		result = append(result, key)
	}
	return result
}
