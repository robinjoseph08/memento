//go:build integration

package audiences

import (
	"context"
	"encoding/json"
	"errors"
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
	db                                  *bun.DB
	actor                               setup.CuratorSession
	momentID, looseID, mediaID, assetID uuid.UUID
	people                              map[string]uuid.UUID
	access                              map[string]uuid.UUID
	service                             *Service
}

func newAudienceFixture(t *testing.T) audienceFixture {
	t.Helper()
	ctx := context.Background()
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(ctx, db))
	f := audienceFixture{db: db, actor: setup.CuratorSession{PersonID: uuid.New(), SessionID: uuid.New()}, momentID: uuid.New(), looseID: uuid.New(), mediaID: uuid.New(), assetID: uuid.New(), people: map[string]uuid.UUID{}, access: map[string]uuid.UUID{}}
	for _, name := range []string{"present", "interested", "both", "manual", "suspended", "attended", "relationship"} {
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
	for _, name := range []string{"attended", "relationship"} {
		_, err = db.NewRaw(`INSERT INTO people (id, display_name, sort_name) VALUES (?, ?, ?)`, f.people[name], name, name).Exec(ctx)
		require.NoError(t, err)
	}
	curatorAccess := uuid.New()
	_, err = db.NewRaw(`INSERT INTO recipient_access_generations (id, person_id, generation, state, onboarding_completed_at) VALUES (?, ?, 1, 'completed', now()); INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at) SELECT ?, decode(repeat('12', 32), 'hex'), ?, ?, security_epoch, 'trusted', now() + interval '1 day' FROM system_settings WHERE id = 1`, curatorAccess, f.actor.PersonID, f.actor.SessionID, f.actor.PersonID, curatorAccess).Exec(ctx)
	require.NoError(t, err)
	_, err = db.NewRaw(`INSERT INTO interest_list_entries (recipient_person_id, selected_person_id, state, chosen_at, updated_at) VALUES (?, ?, 'active', now(), now()), (?, ?, 'active', now(), now())`, f.people["interested"], f.people["attended"], f.people["both"], f.people["attended"]).Exec(ctx)
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
	immichPersonID := uuid.New()
	_, err = db.NewRaw(`INSERT INTO immich_person_links (person_id, immich_person_id, state, confirmed_at, confirmed_by_person_id) VALUES (?, ?, 'linked', now(), ?)`, f.people["attended"], immichPersonID, f.actor.PersonID).Exec(ctx)
	require.NoError(t, err)
	knownFace, unknownFace := uuid.New(), uuid.New()
	f.service = New(db, faceConnector{faces: map[uuid.UUID][]immich.FaceSummary{f.assetID: {{SourceID: knownFace, PersonID: &immichPersonID, ImageWidth: 100, ImageHeight: 80, X1: 1, Y1: 2, X2: 20, Y2: 30}, {SourceID: unknownFace, ImageWidth: 100, ImageHeight: 80, X1: 3, Y1: 4, X2: 25, Y2: 35}}}})
	f.service.now = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	return f
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
	review, err := f.service.ConfirmAttendance(ctx, f.actor, f.momentID, before.Version, AttendanceRequest{PersonIDs: []string{f.people["present"].String(), f.people["both"].String(), f.people["attended"].String()}})
	require.NoError(t, err)
	byName := map[string]ProposalRecipient{}
	for _, proposal := range review.Proposal {
		byName[proposal.Recipient.DisplayName] = proposal
	}
	assert.ElementsMatch(t, []string{"present", "interested", "both"}, mapKeys(byName))
	assert.True(t, byName["present"].Included)
	assert.Len(t, byName["both"].Reasons, 2)
	assert.Equal(t, "present", byName["both"].Reasons[0].Kind)
	assert.Equal(t, "interested", byName["both"].Reasons[1].Kind)
	assert.Equal(t, f.people["attended"].String(), byName["both"].Reasons[1].MatchingPerson.ID)
	assert.NotContains(t, byName, "Curator")
	assert.NotContains(t, byName, "suspended")
	assert.NotContains(t, byName, "manual")
	assert.NotContains(t, byName, "relationship")
}

func TestOverridesSurviveRecalculationAndApprovedSnapshotsStayFixed(t *testing.T) {
	f := newAudienceFixture(t)
	ctx := context.Background()
	review, err := f.service.ConfirmAttendance(ctx, f.actor, f.momentID, 1, AttendanceRequest{PersonIDs: []string{f.people["present"].String(), f.people["attended"].String()}})
	require.NoError(t, err)
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
	review, err = f.service.ConfirmAttendance(ctx, f.actor, f.momentID, approved.Version, AttendanceRequest{PersonIDs: []string{f.people["both"].String()}})
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

func TestMomentAndLooseItemCanApproveExplicitlyEmptyCuratorOnlyAudiences(t *testing.T) {
	f := newAudienceFixture(t)
	ctx := context.Background()
	review, err := f.service.ConfirmAttendance(ctx, f.actor, f.momentID, 1, AttendanceRequest{})
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
	_, err := f.service.ConfirmAttendance(ctx, f.actor, f.momentID, 1, AttendanceRequest{PersonIDs: []string{f.people["present"].String(), f.people["present"].String()}})
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = f.service.ConfirmAttendance(ctx, f.actor, f.momentID, 1, AttendanceRequest{PersonIDs: []string{uuid.NewString()}})
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

func TestStaleReviewMutationsCannotReplaceNewerCuratorDecisions(t *testing.T) {
	f := newAudienceFixture(t)
	ctx := context.Background()
	current, err := f.service.ConfirmAttendance(ctx, f.actor, f.momentID, 1, AttendanceRequest{PersonIDs: []string{f.people["present"].String()}})
	require.NoError(t, err)
	_, err = f.service.ConfirmAttendance(ctx, f.actor, f.momentID, 1, AttendanceRequest{PersonIDs: []string{f.people["both"].String()}})
	assert.ErrorIs(t, err, ErrStale)
	reloaded, err := f.service.ReviewMoment(ctx, f.momentID)
	require.NoError(t, err)
	assert.Equal(t, current.Version, reloaded.Version)
	assert.Equal(t, []string{"present"}, personNames(reloaded.Attendance))
}

func TestAudienceChangesUsePublicationAuditHistory(t *testing.T) {
	f := newAudienceFixture(t)
	ctx := context.Background()
	review, err := f.service.ConfirmAttendance(ctx, f.actor, f.momentID, 1, AttendanceRequest{})
	require.NoError(t, err)
	_, err = f.service.Approve(ctx, f.actor, targetMoment, f.momentID, review.Version)
	require.NoError(t, err)
	var publicationAudits, securityAudits int
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM publication_audit_events WHERE target_kind = 'moment' AND target_id = ?`, f.momentID).Scan(ctx, &publicationAudits))
	require.NoError(t, f.db.NewRaw(`SELECT count(*) FROM security_audit_events WHERE action IN ('attendance_confirmed', 'audience_approved')`).Scan(ctx, &securityAudits))
	assert.Equal(t, 2, publicationAudits)
	assert.Zero(t, securityAudits)
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
