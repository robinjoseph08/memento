//go:build integration

package visibility

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type visibilityFixture struct {
	db         *bun.DB
	service    *Service
	actor      setup.SessionActor
	curator    uuid.UUID
	credential string
}

func newVisibilityFixture(t *testing.T) visibilityFixture {
	t.Helper()
	ctx := context.Background()
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(ctx, db))
	curator := addVisibilityPerson(t, db, "Curator", true)
	_, err := db.NewRaw(`INSERT INTO person_roles (person_id, role) VALUES (?, 'curator')`, curator).Exec(ctx)
	require.NoError(t, err)
	accessID := addVisibilityAccess(t, db, curator, "completed")
	var epoch []byte
	require.NoError(t, db.NewRaw(`SELECT security_epoch FROM system_settings WHERE id = 1`).Scan(ctx, &epoch))
	sessionID := uuid.New()
	credential := sha256.Sum256([]byte("visibility-test-session"))
	credentialHash := sha256.Sum256(credential[:])
	_, err = db.NewRaw(`INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at) VALUES (?, ?, ?, ?, ?, 'trusted', ?)`, sessionID, credentialHash[:], curator, accessID, epoch, time.Now().Add(time.Hour)).Exec(ctx)
	require.NoError(t, err)
	return visibilityFixture{db: db, service: New(db), actor: setup.SessionActor{PersonID: curator, SessionID: sessionID, Curator: true}, curator: curator, credential: hex.EncodeToString(credential[:])}
}

func addVisibilityPerson(t *testing.T, db *bun.DB, name string, recipient bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := db.NewRaw(`INSERT INTO people (id, display_name, sort_name) VALUES (?, ?, ?)`, id, name, name).Exec(context.Background())
	require.NoError(t, err)
	if recipient {
		_, err = db.NewRaw(`INSERT INTO person_roles (person_id, role) VALUES (?, 'recipient')`, id).Exec(context.Background())
		require.NoError(t, err)
	}
	return id
}

func addVisibilityAccess(t *testing.T, db *bun.DB, personID uuid.UUID, state string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var completedAt any
	if state == "completed" {
		completedAt = time.Now().UTC()
	}
	_, err := db.NewRaw(`INSERT INTO recipient_access_generations (id, person_id, generation, state, is_current, onboarding_completed_at) VALUES (?, ?, 1, ?, true, ?)`, id, personID, state, completedAt).Exec(context.Background())
	require.NoError(t, err)
	return id
}

func boolPointer(value bool) *bool { return &value }

func addVisibilitySession(t *testing.T, db *bun.DB, personID, accessID uuid.UUID, seed string) string {
	t.Helper()
	raw := sha256.Sum256([]byte(seed))
	hash := sha256.Sum256(raw[:])
	var epoch []byte
	require.NoError(t, db.NewRaw(`SELECT security_epoch FROM system_settings WHERE id = 1`).Scan(context.Background(), &epoch))
	_, err := db.NewRaw(`INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at) VALUES (?, ?, ?, ?, ?, 'trusted', ?)`, uuid.New(), hash[:], personID, accessID, epoch, time.Now().Add(time.Hour)).Exec(context.Background())
	require.NoError(t, err)
	return hex.EncodeToString(raw[:])
}

func createCircleWithMembers(t *testing.T, fixture visibilityFixture, name string, members ...uuid.UUID) Circle {
	t.Helper()
	circle, err := fixture.service.CreateCircle(context.Background(), fixture.actor, CircleRequest{Name: name})
	require.NoError(t, err)
	for _, member := range members {
		circle, err = fixture.service.SetMembership(context.Background(), fixture.actor, uuid.MustParse(circle.ID), member, MembershipRequest{Included: boolPointer(true), Version: circle.Version})
		require.NoError(t, err)
	}
	return circle
}

func TestOverlappingCirclesExposeOnlyTheDirectUnionWithoutPrivateFieldsOrTransitivity(t *testing.T) {
	fixture := newVisibilityFixture(t)
	recipient := addVisibilityPerson(t, fixture.db, "Recipient", true)
	visibleA := addVisibilityPerson(t, fixture.db, "Visible A", false)
	visibleB := addVisibilityPerson(t, fixture.db, "Visible B", false)
	transitive := addVisibilityPerson(t, fixture.db, "Hidden transitive", false)

	createCircleWithMembers(t, fixture, "First", recipient, visibleA)
	createCircleWithMembers(t, fixture, "Second", visibleA, transitive)
	createCircleWithMembers(t, fixture, "Overlapping", recipient, visibleB, visibleA)

	discovered, err := fixture.service.Discover(context.Background(), fixture.actor, recipient, DiscoveryPageRequest{})
	require.NoError(t, err)
	assert.Equal(t, []Person{
		{ID: visibleA.String(), DisplayName: "Visible A", SortName: "Visible A"},
		{ID: visibleB.String(), DisplayName: "Visible B", SortName: "Visible B"},
	}, discovered.People)
	encoded, err := json.Marshal(discovered)
	require.NoError(t, err)
	assert.JSONEq(t, `{"people":[{"id":"`+visibleA.String()+`","display_name":"Visible A","sort_name":"Visible A"},{"id":"`+visibleB.String()+`","display_name":"Visible B","sort_name":"Visible B"}]}`, string(encoded))
	assert.NotContains(t, string(encoded), "email")
	assert.NotContains(t, string(encoded), "circle")
	assert.NotContains(t, string(encoded), transitive.String())
}

func TestDiscoveryAnnotatesVisibleFamilyChoicesWithoutExposingIntermediates(t *testing.T) {
	fixture := newVisibilityFixture(t)
	ctx := context.Background()
	recipient := addVisibilityPerson(t, fixture.db, "Recipient", true)
	hiddenChild := addVisibilityPerson(t, fixture.db, "Hidden Child", false)
	visibleGrandchild := addVisibilityPerson(t, fixture.db, "Visible Grandchild", false)
	visibleSibling := addVisibilityPerson(t, fixture.db, "Visible Sibling", false)
	createCircleWithMembers(t, fixture, "Family", recipient, visibleGrandchild, visibleSibling)
	_, err := fixture.db.NewRaw(`INSERT INTO family_relationships (id, relationship_type, person_a_id, person_b_id) VALUES (?, 'parent_child', ?, ?), (?, 'parent_child', ?, ?), (?, 'sibling', LEAST(?::uuid, ?::uuid), GREATEST(?::uuid, ?::uuid))`, uuid.New(), recipient, hiddenChild, uuid.New(), hiddenChild, visibleGrandchild, uuid.New(), recipient, visibleSibling, recipient, visibleSibling).Exec(ctx)
	require.NoError(t, err)

	discovered, err := fixture.service.Discover(ctx, fixture.actor, recipient, DiscoveryPageRequest{})
	require.NoError(t, err)
	require.Len(t, discovered.People, 2)
	assert.Equal(t, visibleGrandchild.String(), discovered.People[0].ID)
	assert.Equal(t, &RelationshipAnnotation{ConnectionType: "descendant", Generation: 2}, discovered.People[0].Relationship)
	assert.Equal(t, visibleSibling.String(), discovered.People[1].ID)
	assert.Equal(t, &RelationshipAnnotation{ConnectionType: "sibling"}, discovered.People[1].Relationship)
	encoded, err := json.Marshal(discovered)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), hiddenChild.String())
	assert.NotContains(t, string(encoded), "Hidden Child")
}

func TestDiscoveryUsesAnOpaqueDeterministicCursor(t *testing.T) {
	fixture := newVisibilityFixture(t)
	ctx := context.Background()
	recipient := addVisibilityPerson(t, fixture.db, "Recipient", true)
	alex := addVisibilityPerson(t, fixture.db, "Alex", false)
	blair := addVisibilityPerson(t, fixture.db, "Blair", false)
	createCircleWithMembers(t, fixture, "Family", recipient, alex, blair)

	first, err := fixture.service.Discover(ctx, fixture.actor, recipient, DiscoveryPageRequest{Limit: 1})
	require.NoError(t, err)
	require.Len(t, first.People, 1)
	assert.Equal(t, alex.String(), first.People[0].ID)
	require.NotNil(t, first.NextCursor)
	assert.NotContains(t, *first.NextCursor, alex.String(), "the cursor must not expose a raw Person ID")

	second, err := fixture.service.Discover(ctx, fixture.actor, recipient, DiscoveryPageRequest{Cursor: *first.NextCursor, Limit: 1})
	require.NoError(t, err)
	require.Len(t, second.People, 1)
	assert.Equal(t, blair.String(), second.People[0].ID)
	assert.Nil(t, second.NextCursor)
	_, err = fixture.service.Discover(ctx, fixture.actor, recipient, DiscoveryPageRequest{Cursor: "not-a-cursor", Limit: 1})
	require.ErrorIs(t, err, ErrInvalidCursor)
}

func TestDiscoveryCursorDoesNotSkipEqualNormalizedSortNames(t *testing.T) {
	fixture := newVisibilityFixture(t)
	ctx := context.Background()
	recipient := addVisibilityPerson(t, fixture.db, "Recipient", true)
	first := addVisibilityPerson(t, fixture.db, "Same", false)
	second := addVisibilityPerson(t, fixture.db, "Same", false)
	third := addVisibilityPerson(t, fixture.db, "Same", false)
	createCircleWithMembers(t, fixture, "Family", recipient, first, second, third)

	ids := make([]string, 0, 3)
	cursor := ""
	for {
		page, err := fixture.service.Discover(ctx, fixture.actor, recipient, DiscoveryPageRequest{Cursor: cursor, Limit: 1})
		require.NoError(t, err)
		require.Len(t, page.People, 1)
		ids = append(ids, page.People[0].ID)
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	assert.ElementsMatch(t, []string{first.String(), second.String(), third.String()}, ids)
	assert.Len(t, ids, 3)
}

func TestVisibilityMutationsRejectInvalidStaleAndIneligibleChanges(t *testing.T) {
	fixture := newVisibilityFixture(t)
	ctx := context.Background()
	recipient := addVisibilityPerson(t, fixture.db, "Recipient", true)
	nonRecipient := addVisibilityPerson(t, fixture.db, "Not recipient", false)
	hidden := addVisibilityPerson(t, fixture.db, "Hidden", false)

	_, err := fixture.service.CreateCircle(ctx, fixture.actor, CircleRequest{Name: "   "})
	require.ErrorIs(t, err, ErrInvalid)
	circle := createCircleWithMembers(t, fixture, "Family", recipient)
	_, err = fixture.service.CreateCircle(ctx, fixture.actor, CircleRequest{Name: "family"})
	require.ErrorIs(t, err, ErrDuplicateName)
	_, err = fixture.service.UpdateCircle(ctx, fixture.actor, uuid.MustParse(circle.ID), CircleRequest{Name: "Renamed", Version: circle.Version - 1})
	require.ErrorIs(t, err, ErrStale)
	_, err = fixture.service.ArchiveCircle(ctx, fixture.actor, uuid.MustParse(circle.ID), circle.Version-1)
	require.ErrorIs(t, err, ErrStale)
	_, err = fixture.service.UpdateCircle(ctx, fixture.actor, uuid.New(), CircleRequest{Name: "Missing", Version: 1})
	require.ErrorIs(t, err, ErrCircleNotFound)
	editable, err := fixture.service.CreateCircle(ctx, fixture.actor, CircleRequest{Name: "Editable"})
	require.NoError(t, err)
	edited, err := fixture.service.UpdateCircle(ctx, fixture.actor, uuid.MustParse(editable.ID), CircleRequest{Name: "Renamed", Version: editable.Version})
	require.NoError(t, err)
	assert.Equal(t, int64(2), edited.Version)
	archived, err := fixture.service.ArchiveCircle(ctx, fixture.actor, uuid.MustParse(edited.ID), edited.Version)
	require.NoError(t, err)
	assert.NotNil(t, archived.ArchivedAt)
	active, err := fixture.service.ListCircles(ctx, fixture.actor, false)
	require.NoError(t, err)
	assert.NotContains(t, []string{active.Circles[0].Name}, "Renamed")
	all, err := fixture.service.ListCircles(ctx, fixture.actor, true)
	require.NoError(t, err)
	assert.Len(t, all.Circles, 2)
	assert.Equal(t, "Renamed", all.Circles[1].Name)
	_, err = fixture.service.Discover(ctx, fixture.actor, nonRecipient, DiscoveryPageRequest{})
	require.ErrorIs(t, err, ErrRecipientRequired)
	_, err = fixture.service.MutateInterest(ctx, fixture.actor, recipient, hidden, true)
	require.ErrorIs(t, err, ErrNotDiscoverable)
	_, err = fixture.service.MutateInterest(ctx, fixture.actor, recipient, uuid.New(), false)
	require.ErrorIs(t, err, ErrPersonNotFound)

	_, err = fixture.db.NewRaw(`UPDATE people SET archived_at = now() WHERE id = ?`, hidden).Exec(ctx)
	require.NoError(t, err)
	_, err = fixture.service.SetMembership(ctx, fixture.actor, uuid.MustParse(circle.ID), hidden, MembershipRequest{Included: boolPointer(true), Version: circle.Version})
	require.ErrorIs(t, err, ErrPersonUnavailable)
}

func TestInterestChoicesAreExplicitAuditedAndStayInactiveAcrossEligibilityReturn(t *testing.T) {
	fixture := newVisibilityFixture(t)
	ctx := context.Background()
	recipient := addVisibilityPerson(t, fixture.db, "Recipient", true)
	addVisibilityAccess(t, fixture.db, recipient, "completed")
	selected := addVisibilityPerson(t, fixture.db, "Selected", false)
	circle := createCircleWithMembers(t, fixture, "Family", recipient, selected)
	overlap := createCircleWithMembers(t, fixture, "Overlapping family", recipient, selected)

	empty, err := fixture.service.InterestList(ctx, fixture.actor, recipient, HistoryPageRequest{})
	require.NoError(t, err)
	assert.Empty(t, empty.Entries)
	assert.Empty(t, empty.History)
	_, err = fixture.service.MutateInterest(ctx, fixture.actor, recipient, recipient, true)
	require.ErrorIs(t, err, ErrSelfSelection)

	chosen, err := fixture.service.MutateInterest(ctx, fixture.actor, recipient, selected, true)
	require.NoError(t, err)
	require.Len(t, chosen.Entries, 1)
	assert.Equal(t, "active", chosen.Entries[0].State)
	require.Len(t, chosen.History, 1)
	assert.Equal(t, recipient.String(), chosen.Recipient.ID)
	assert.Equal(t, selected.String(), chosen.History[0].Person.ID)
	assert.Equal(t, fixture.curator.String(), chosen.History[0].Actor.ID)
	assert.Equal(t, "selected", chosen.History[0].Action)
	assert.Equal(t, "active", chosen.History[0].Result)
	assert.Equal(t, "explicit", chosen.History[0].Reason)
	assert.False(t, chosen.History[0].CreatedAt.IsZero())

	proposals, err := fixture.service.ProposeRecipients(ctx, fixture.actor, []uuid.UUID{selected})
	require.NoError(t, err)
	require.Len(t, proposals, 1)
	assert.Equal(t, recipient.String(), proposals[0].Recipient.ID)
	assert.Equal(t, []string{"interested"}, proposals[0].Reasons)

	circle, err = fixture.service.SetMembership(ctx, fixture.actor, uuid.MustParse(circle.ID), selected, MembershipRequest{Included: boolPointer(false), Version: circle.Version})
	require.NoError(t, err)
	stillEligible, err := fixture.service.InterestList(ctx, fixture.actor, recipient, HistoryPageRequest{})
	require.NoError(t, err)
	assert.Equal(t, "active", stillEligible.Entries[0].State)
	assert.Len(t, stillEligible.History, 1, "losing one of two shared circles must not deactivate the choice")
	overlap, err = fixture.service.SetMembership(ctx, fixture.actor, uuid.MustParse(overlap.ID), selected, MembershipRequest{Included: boolPointer(false), Version: overlap.Version})
	require.NoError(t, err)
	inactive, err := fixture.service.InterestList(ctx, fixture.actor, recipient, HistoryPageRequest{})
	require.NoError(t, err)
	require.Len(t, inactive.Entries, 1)
	assert.Equal(t, "ineligible", inactive.Entries[0].State)
	require.Len(t, inactive.History, 2)
	assert.Equal(t, selected.String(), inactive.History[0].Person.ID)
	assert.Equal(t, fixture.curator.String(), inactive.History[0].Actor.ID)
	assert.Equal(t, "deactivated", inactive.History[0].Action)
	assert.Equal(t, "ineligible", inactive.History[0].Result)
	assert.Equal(t, "visibility_lost", inactive.History[0].Reason)
	proposals, err = fixture.service.ProposeRecipients(ctx, fixture.actor, []uuid.UUID{selected})
	require.NoError(t, err)
	assert.Empty(t, proposals)

	circle, err = fixture.service.SetMembership(ctx, fixture.actor, uuid.MustParse(circle.ID), selected, MembershipRequest{Included: boolPointer(true), Version: circle.Version})
	require.NoError(t, err)
	stillInactive, err := fixture.service.InterestList(ctx, fixture.actor, recipient, HistoryPageRequest{})
	require.NoError(t, err)
	assert.Equal(t, "ineligible", stillInactive.Entries[0].State)
	proposals, err = fixture.service.ProposeRecipients(ctx, fixture.actor, []uuid.UUID{selected})
	require.NoError(t, err)
	assert.Empty(t, proposals)

	reselected, err := fixture.service.MutateInterest(ctx, fixture.actor, recipient, selected, true)
	require.NoError(t, err)
	assert.Equal(t, "active", reselected.Entries[0].State)
	assert.Len(t, reselected.History, 3)
	assert.Equal(t, selected.String(), reselected.History[0].Person.ID)
	assert.Equal(t, fixture.curator.String(), reselected.History[0].Actor.ID)
	assert.Equal(t, "selected", reselected.History[0].Action)
	assert.Equal(t, "active", reselected.History[0].Result)
	assert.Equal(t, "explicit", reselected.History[0].Reason)
}

func TestInterestHistoryUsesAnOpaqueDeterministicCursor(t *testing.T) {
	fixture := newVisibilityFixture(t)
	ctx := context.Background()
	recipient := addVisibilityPerson(t, fixture.db, "Recipient", true)
	selected := addVisibilityPerson(t, fixture.db, "Selected", false)
	createCircleWithMembers(t, fixture, "Family", recipient, selected)
	_, err := fixture.service.MutateInterest(ctx, fixture.actor, recipient, selected, true)
	require.NoError(t, err)
	_, err = fixture.service.MutateInterest(ctx, fixture.actor, recipient, selected, false)
	require.NoError(t, err)
	_, err = fixture.service.MutateInterest(ctx, fixture.actor, recipient, selected, true)
	require.NoError(t, err)

	first, err := fixture.service.InterestList(ctx, fixture.actor, recipient, HistoryPageRequest{Limit: 2})
	require.NoError(t, err)
	require.Len(t, first.History, 2)
	require.NotNil(t, first.HistoryNextCursor)
	assert.NotContains(t, *first.HistoryNextCursor, selected.String())
	second, err := fixture.service.InterestList(ctx, fixture.actor, recipient, HistoryPageRequest{Cursor: *first.HistoryNextCursor, Limit: 2})
	require.NoError(t, err)
	require.Len(t, second.History, 1)
	assert.Nil(t, second.HistoryNextCursor)
	assert.NotEqual(t, first.History[0].ID, first.History[1].ID)
	assert.NotEqual(t, first.History[1].ID, second.History[0].ID)
	_, err = fixture.service.InterestList(ctx, fixture.actor, recipient, HistoryPageRequest{Cursor: "not-a-cursor"})
	require.ErrorIs(t, err, ErrInvalidCursor)
}

func TestRecipientAndCuratorProposalRulesDoNotTreatVisibilityAsAuthority(t *testing.T) {
	fixture := newVisibilityFixture(t)
	ctx := context.Background()
	pending := addVisibilityPerson(t, fixture.db, "Pending", true)
	addVisibilityAccess(t, fixture.db, pending, "pending")
	suspended := addVisibilityPerson(t, fixture.db, "Suspended", true)
	addVisibilityAccess(t, fixture.db, suspended, "suspended")
	completed := addVisibilityPerson(t, fixture.db, "Completed", true)
	addVisibilityAccess(t, fixture.db, completed, "completed")
	attendee := addVisibilityPerson(t, fixture.db, "Attendee A", false)
	secondAttendee := addVisibilityPerson(t, fixture.db, "Attendee B", false)
	createCircleWithMembers(t, fixture, "Proposal circle", pending, suspended, completed, attendee, secondAttendee, fixture.curator)
	_, err := fixture.service.MutateInterest(ctx, fixture.actor, pending, attendee, true)
	require.NoError(t, err)
	_, err = fixture.service.MutateInterest(ctx, fixture.actor, suspended, attendee, true)
	require.NoError(t, err)
	_, err = fixture.service.MutateInterest(ctx, fixture.actor, fixture.curator, attendee, true)
	require.NoError(t, err)
	_, err = fixture.service.MutateInterest(ctx, fixture.actor, pending, secondAttendee, true)
	require.NoError(t, err)

	proposals, err := fixture.service.ProposeRecipients(ctx, fixture.actor, []uuid.UUID{attendee, secondAttendee, completed})
	require.NoError(t, err)
	require.Len(t, proposals, 2)
	assert.Equal(t, completed.String(), proposals[0].Recipient.ID)
	assert.Equal(t, []string{"present"}, proposals[0].Reasons)
	assert.Equal(t, []Person{{ID: completed.String(), DisplayName: "Completed", SortName: "Completed"}}, proposals[0].MatchingPeople)
	assert.Equal(t, pending.String(), proposals[1].Recipient.ID, "pending access is proposal-eligible without gaining content authority")
	assert.Equal(t, []string{"interested"}, proposals[1].Reasons)
	assert.Equal(t, []Person{
		{ID: attendee.String(), DisplayName: "Attendee A", SortName: "Attendee A"},
		{ID: secondAttendee.String(), DisplayName: "Attendee B", SortName: "Attendee B"},
	}, proposals[1].MatchingPeople)

	var pendingState, suspendedState string
	require.NoError(t, fixture.db.NewRaw(`SELECT state FROM recipient_access_generations WHERE person_id = ? AND is_current`, pending).Scan(ctx, &pendingState))
	require.NoError(t, fixture.db.NewRaw(`SELECT state FROM recipient_access_generations WHERE person_id = ? AND is_current`, suspended).Scan(ctx, &suspendedState))
	assert.Equal(t, "pending", pendingState, "Interest choices do not complete Onboarding or grant access")
	assert.Equal(t, "suspended", suspendedState, "Interest choices do not restore access")
	var recipientSessions int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM sessions WHERE person_id IN (?, ?)`, pending, suspended).Scan(ctx, &recipientSessions))
	assert.Zero(t, recipientSessions, "Visibility and Interest mutations do not create Sessions")
}

func TestPersonLifecycleChangesDeactivateOrMoveInterestReferencesWithoutLosingHistory(t *testing.T) {
	fixture := newVisibilityFixture(t)
	ctx := context.Background()
	recipient := addVisibilityPerson(t, fixture.db, "Recipient", true)
	selected := addVisibilityPerson(t, fixture.db, "Selected", false)
	survivor := addVisibilityPerson(t, fixture.db, "Survivor", false)
	circle := createCircleWithMembers(t, fixture, "Family", recipient, selected, survivor)
	_, err := fixture.service.MutateInterest(ctx, fixture.actor, recipient, selected, true)
	require.NoError(t, err)

	now := time.Now().UTC()
	require.NoError(t, fixture.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`UPDATE people SET archived_at = ? WHERE id = ?`, now, selected).Exec(ctx); err != nil {
			return err
		}
		return ArchivePersonReferences(ctx, tx, selected, fixture.actor, now)
	}))
	inactive, err := fixture.service.InterestList(ctx, fixture.actor, recipient, HistoryPageRequest{})
	require.NoError(t, err)
	assert.Equal(t, "ineligible", inactive.Entries[0].State)
	assert.Equal(t, "deactivated", inactive.History[0].Action)
	removed, err := fixture.service.MutateInterest(ctx, fixture.actor, recipient, selected, false)
	require.NoError(t, err)
	assert.Empty(t, removed.Entries, "an unavailable Person can still be explicitly removed")
	assert.Equal(t, "deselected", removed.History[0].Result)

	_, err = fixture.db.NewRaw(`UPDATE people SET archived_at = NULL WHERE id = ?`, selected).Exec(ctx)
	require.NoError(t, err)
	listed, err := fixture.service.ListCircles(ctx, fixture.actor, false)
	require.NoError(t, err)
	require.Len(t, listed.Circles, 1)
	circle = listed.Circles[0]
	circle, err = fixture.service.SetMembership(ctx, fixture.actor, uuid.MustParse(circle.ID), selected, MembershipRequest{Included: boolPointer(true), Version: circle.Version})
	require.NoError(t, err)
	_, err = fixture.service.MutateInterest(ctx, fixture.actor, recipient, selected, true)
	require.NoError(t, err)
	now = now.Add(time.Second)
	require.NoError(t, fixture.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return MergePersonReferences(ctx, tx, selected, survivor, fixture.actor, now)
	}))
	moved, err := fixture.service.InterestList(ctx, fixture.actor, recipient, HistoryPageRequest{})
	require.NoError(t, err)
	require.Len(t, moved.Entries, 1)
	assert.Equal(t, survivor.String(), moved.Entries[0].Person.ID)
	assert.Equal(t, "active", moved.Entries[0].State)
	assert.Equal(t, "moved", moved.History[0].Action)
	assert.Equal(t, "person_merged", moved.History[0].Reason)
	assert.GreaterOrEqual(t, len(moved.History), 5, "explicit, lifecycle, removal, reselection, and merge history are retained")

	var sourceMemberships int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM visibility_circle_members WHERE person_id = ?`, selected).Scan(ctx, &sourceMemberships))
	assert.Zero(t, sourceMemberships)
}

func TestRealSessionsEnforceOwnerAndCuratorVisibilityPoliciesOverHTTP(t *testing.T) {
	fixture := newVisibilityFixture(t)
	ctx := context.Background()
	recipient := addVisibilityPerson(t, fixture.db, "Recipient", true)
	recipientAccess := addVisibilityAccess(t, fixture.db, recipient, "completed")
	recipientCredential := addVisibilitySession(t, fixture.db, recipient, recipientAccess, "recipient-session")
	otherRecipient := addVisibilityPerson(t, fixture.db, "Other Recipient", true)
	addVisibilityAccess(t, fixture.db, otherRecipient, "completed")
	selected := addVisibilityPerson(t, fixture.db, "Selected", false)
	createCircleWithMembers(t, fixture, "Family", recipient, selected)
	_, err := fixture.db.NewRaw(`UPDATE system_settings SET setup_complete = true WHERE id = 1`).Exec(ctx)
	require.NoError(t, err)
	setupService := setup.New(fixture.db, nil, config.SecurityConfig{Secret: "visibility-http-policy-secret"})
	recipientSession, err := setupService.Session(ctx, recipientCredential)
	require.NoError(t, err)
	curatorSession, err := setupService.Session(ctx, fixture.credential)
	require.NoError(t, err)
	e := echo.New()
	RegisterRoutes(e, NewHandler(fixture.service, setupService))
	e.HTTPErrorHandler = errcodes.NewHandler().Handle

	self := visibilityRequest(e, "GET", "/api/me/people?limit=50", recipientCredential, "", "")
	require.Equal(t, 200, self.Code)
	assert.Contains(t, self.Body.String(), selected.String())
	assert.NotContains(t, self.Body.String(), "email")
	curator := visibilityRequest(e, "GET", "/api/interest-lists/"+recipient.String(), fixture.credential, "", "")
	require.Equal(t, 200, curator.Code)
	assert.Contains(t, curator.Body.String(), recipient.String())
	denied := visibilityRequest(e, "GET", "/api/interest-lists/"+otherRecipient.String(), recipientCredential, "", "")
	assert.Equal(t, 404, denied.Code)
	mutation := visibilityRequest(e, "PUT", "/api/me/interest-list/"+selected.String(), recipientCredential, recipientSession.CSRFToken, `{"selected":true}`)
	require.Equal(t, 200, mutation.Code)
	assert.Contains(t, mutation.Body.String(), `"actor":{"id":"`+recipient.String())
	badCSRF := visibilityRequest(e, "PUT", "/api/interest-lists/"+recipient.String()+"/people/"+selected.String(), fixture.credential, curatorSession.CSRFToken+"wrong", `{"selected":false}`)
	assert.Equal(t, 403, badCSRF.Code)
}

func TestServiceQueriesRecheckActorAuthorityAndHideUndiscoverablePeople(t *testing.T) {
	fixture := newVisibilityFixture(t)
	ctx := context.Background()
	recipient := addVisibilityPerson(t, fixture.db, "Recipient", true)
	otherRecipient := addVisibilityPerson(t, fixture.db, "Other Recipient", true)
	hidden := addVisibilityPerson(t, fixture.db, "Hidden", false)
	actor := setup.SessionActor{PersonID: recipient, SessionID: fixture.actor.SessionID}

	_, err := fixture.service.ListCircles(ctx, actor, false)
	require.ErrorIs(t, err, ErrNotAuthorized)
	_, err = fixture.service.Discover(ctx, actor, otherRecipient, DiscoveryPageRequest{})
	require.ErrorIs(t, err, ErrNotAuthorized)
	_, err = fixture.service.InterestList(ctx, actor, otherRecipient, HistoryPageRequest{})
	require.ErrorIs(t, err, ErrNotAuthorized)
	_, err = fixture.service.MutateInterest(ctx, actor, otherRecipient, hidden, true)
	require.ErrorIs(t, err, ErrNotAuthorized)
	_, err = fixture.service.ProposeRecipients(ctx, actor, []uuid.UUID{hidden})
	require.ErrorIs(t, err, ErrNotAuthorized)

	_, err = fixture.service.MutateInterest(ctx, actor, recipient, hidden, true)
	require.ErrorIs(t, err, ErrPersonNotFound)
	_, err = fixture.service.MutateInterest(ctx, actor, recipient, hidden, false)
	require.ErrorIs(t, err, ErrPersonNotFound)
}

func TestMembershipMutationRejectsAStaleCircleVersion(t *testing.T) {
	fixture := newVisibilityFixture(t)
	person := addVisibilityPerson(t, fixture.db, "Person", false)
	circle := createCircleWithMembers(t, fixture, "Family")

	_, err := fixture.service.SetMembership(context.Background(), fixture.actor, uuid.MustParse(circle.ID), person, MembershipRequest{Included: boolPointer(true), Version: circle.Version + 1})
	require.ErrorIs(t, err, ErrStale)
}

func TestConcurrentVisibilityMutationsProduceOneTypedLoser(t *testing.T) {
	fixture := newVisibilityFixture(t)
	ctx := context.Background()
	start := make(chan struct{})
	resultErrors := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := fixture.service.CreateCircle(ctx, fixture.actor, CircleRequest{Name: "Concurrent"})
			resultErrors <- err
		}()
	}
	close(start)
	wg.Wait()
	close(resultErrors)
	var successes, duplicates int
	for err := range resultErrors {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrDuplicateName) {
			duplicates++
		} else {
			require.NoError(t, err)
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, duplicates)

	circles, err := fixture.service.ListCircles(ctx, fixture.actor, false)
	require.NoError(t, err)
	require.Len(t, circles.Circles, 1)
	person := addVisibilityPerson(t, fixture.db, "Person", false)
	membershipErrors := make(chan error, 2)
	start = make(chan struct{})
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := fixture.service.SetMembership(ctx, fixture.actor, uuid.MustParse(circles.Circles[0].ID), person, MembershipRequest{Included: boolPointer(true), Version: circles.Circles[0].Version})
			membershipErrors <- err
		}()
	}
	close(start)
	wg.Wait()
	close(membershipErrors)
	var membershipSuccesses, stale int
	for err := range membershipErrors {
		if err == nil {
			membershipSuccesses++
		} else if errors.Is(err, ErrStale) {
			stale++
		} else {
			require.NoError(t, err)
		}
	}
	assert.Equal(t, 1, membershipSuccesses)
	assert.Equal(t, 1, stale)
}

func TestMergingAnInterestListOwnerMovesTheirRetainedHistory(t *testing.T) {
	fixture := newVisibilityFixture(t)
	ctx := context.Background()
	source := addVisibilityPerson(t, fixture.db, "Source Recipient", true)
	survivor := addVisibilityPerson(t, fixture.db, "Survivor Recipient", true)
	selected := addVisibilityPerson(t, fixture.db, "Selected", false)
	createCircleWithMembers(t, fixture, "Family", source, survivor, selected)
	_, err := fixture.service.MutateInterest(ctx, fixture.actor, source, selected, true)
	require.NoError(t, err)

	now := time.Now().UTC()
	require.NoError(t, fixture.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return MergePersonReferences(ctx, tx, source, survivor, fixture.actor, now)
	}))
	moved, err := fixture.service.InterestList(ctx, fixture.actor, survivor, HistoryPageRequest{})
	require.NoError(t, err)
	require.Len(t, moved.Entries, 1)
	assert.Equal(t, selected.String(), moved.Entries[0].Person.ID)
	require.Len(t, moved.History, 2)
	assert.Equal(t, "moved", moved.History[0].Action)
	assert.Equal(t, "selected", moved.History[1].Action)

	var sourceHistory int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM interest_list_history WHERE recipient_person_id = ?`, source).Scan(ctx, &sourceHistory))
	assert.Zero(t, sourceHistory)
}

func TestCircleAuditFailureRollsBackTheMutation(t *testing.T) {
	fixture := newVisibilityFixture(t)
	ctx := context.Background()
	_, err := fixture.db.ExecContext(ctx, `CREATE FUNCTION reject_visibility_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action LIKE 'visibility_circle_%' THEN RAISE EXCEPTION 'rejected Visibility audit'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_visibility_audit BEFORE INSERT ON security_audit_events FOR EACH ROW EXECUTE FUNCTION reject_visibility_audit()`)
	require.NoError(t, err)

	_, err = fixture.service.CreateCircle(ctx, fixture.actor, CircleRequest{Name: "Family"})
	require.ErrorContains(t, err, "rejected Visibility audit")
	var circles int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM visibility_circles`).Scan(ctx, &circles))
	assert.Zero(t, circles)
}

func TestEligibilityHistoryFailureRollsBackMembershipAndDeactivation(t *testing.T) {
	fixture := newVisibilityFixture(t)
	ctx := context.Background()
	recipient := addVisibilityPerson(t, fixture.db, "Recipient", true)
	selected := addVisibilityPerson(t, fixture.db, "Selected", false)
	circle := createCircleWithMembers(t, fixture, "Family", recipient, selected)
	_, err := fixture.service.MutateInterest(ctx, fixture.actor, recipient, selected, true)
	require.NoError(t, err)
	_, err = fixture.db.ExecContext(ctx, `CREATE FUNCTION reject_deactivation_history() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action = 'deactivated' THEN RAISE EXCEPTION 'rejected deactivation history'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_deactivation_history BEFORE INSERT ON interest_list_history FOR EACH ROW EXECUTE FUNCTION reject_deactivation_history()`)
	require.NoError(t, err)

	_, err = fixture.service.SetMembership(ctx, fixture.actor, uuid.MustParse(circle.ID), selected, MembershipRequest{Included: boolPointer(false), Version: circle.Version})
	require.ErrorContains(t, err, "rejected deactivation history")
	stored, err := fixture.service.InterestList(ctx, fixture.actor, recipient, HistoryPageRequest{})
	require.NoError(t, err)
	assert.Equal(t, "active", stored.Entries[0].State)
	var membership int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM visibility_circle_members WHERE circle_id = ? AND person_id = ?`, circle.ID, selected).Scan(ctx, &membership))
	assert.Equal(t, 1, membership)
}

func TestInterestHistoryFailureRollsBackTheChoice(t *testing.T) {
	fixture := newVisibilityFixture(t)
	ctx := context.Background()
	recipient := addVisibilityPerson(t, fixture.db, "Recipient", true)
	selected := addVisibilityPerson(t, fixture.db, "Selected", false)
	createCircleWithMembers(t, fixture, "Family", recipient, selected)
	_, err := fixture.db.ExecContext(ctx, `CREATE FUNCTION reject_interest_history() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'rejected Interest history'; END $$; CREATE TRIGGER reject_interest_history BEFORE INSERT ON interest_list_history FOR EACH ROW EXECUTE FUNCTION reject_interest_history()`)
	require.NoError(t, err)

	_, err = fixture.service.MutateInterest(ctx, fixture.actor, recipient, selected, true)
	require.ErrorContains(t, err, "rejected Interest history")
	var entries int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM interest_list_entries`).Scan(ctx, &entries))
	assert.Zero(t, entries)
}
