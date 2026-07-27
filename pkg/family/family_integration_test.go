//go:build integration

package family

import (
	"bytes"
	"context"
	"crypto/sha256"
	"sync"
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

type familyFixture struct {
	db      *bun.DB
	service *Service
	actor   setup.CuratorSession
}

func newFamilyFixture(t *testing.T) familyFixture {
	t.Helper()
	ctx := context.Background()
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(ctx, db))
	curator := addFamilyPerson(t, db, "Curator")
	accessID := uuid.New()
	now := time.Now().UTC()
	_, err := db.NewRaw(`INSERT INTO recipient_access_generations (id, person_id, generation, state, is_current, onboarding_completed_at, created_at, updated_at) VALUES (?, ?, 1, 'completed', true, ?, ?, ?)`, accessID, curator, now, now, now).Exec(ctx)
	require.NoError(t, err)
	var epoch []byte
	require.NoError(t, db.NewRaw(`SELECT security_epoch FROM system_settings WHERE id = 1`).Scan(ctx, &epoch))
	sessionID := uuid.New()
	credential := sha256.Sum256([]byte("family-test-session"))
	_, err = db.NewRaw(`INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at) VALUES (?, ?, ?, ?, ?, 'trusted', ?)`, sessionID, credential[:], curator, accessID, epoch, now.Add(time.Hour)).Exec(ctx)
	require.NoError(t, err)
	return familyFixture{db: db, service: New(db), actor: setup.CuratorSession{PersonID: curator, SessionID: sessionID}}
}

func addFamilyPerson(t *testing.T, db *bun.DB, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := db.NewRaw(`INSERT INTO people (id, display_name, sort_name) VALUES (?, ?, ?)`, id, name, name).Exec(context.Background())
	require.NoError(t, err)
	return id
}

func createFamilyRelationship(t *testing.T, fixture familyFixture, relationshipType string, personA, personB uuid.UUID, partnerStatus string) Relationship {
	t.Helper()
	relationship, err := fixture.service.Create(context.Background(), fixture.actor, MutationRequest{
		RelationshipType: relationshipType,
		PersonAID:        personA.String(),
		PersonBID:        personB.String(),
		PartnerStatus:    partnerStatus,
	})
	require.NoError(t, err)
	return relationship
}

func TestCuratorCreatesEditsAndArchivesEveryFamilyConnectionType(t *testing.T) {
	fixture := newFamilyFixture(t)
	ctx := context.Background()
	alex := addFamilyPerson(t, fixture.db, "Alex")
	blair := addFamilyPerson(t, fixture.db, "Blair")

	parentChild := createFamilyRelationship(t, fixture, "parent_child", alex, blair, "")
	assert.Equal(t, alex.String(), parentChild.PersonA.ID)
	assert.Equal(t, blair.String(), parentChild.PersonB.ID)
	sibling := createFamilyRelationship(t, fixture, "sibling", blair, alex, "")
	assert.Less(t, sibling.PersonA.ID, sibling.PersonB.ID, "sibling endpoints are canonical regardless of input order")
	partner := createFamilyRelationship(t, fixture, "partner", blair, alex, "current")

	updated, err := fixture.service.Update(ctx, fixture.actor, uuid.MustParse(partner.ID), MutationRequest{
		RelationshipType: "partner", PersonAID: alex.String(), PersonBID: blair.String(), PartnerStatus: "former", Version: partner.Version,
	})
	require.NoError(t, err)
	assert.Equal(t, "former", updated.PartnerStatus)
	assert.Equal(t, int64(2), updated.Version)

	archived, err := fixture.service.Archive(ctx, fixture.actor, uuid.MustParse(sibling.ID), sibling.Version)
	require.NoError(t, err)
	assert.NotNil(t, archived.ArchivedAt)
	active, err := fixture.service.List(ctx, false)
	require.NoError(t, err)
	assert.Len(t, active.Relationships, 2)
	all, err := fixture.service.List(ctx, true)
	require.NoError(t, err)
	assert.Len(t, all.Relationships, 3)
	assert.NotNil(t, all.Relationships[2].ArchivedAt)

	var actions []string
	require.NoError(t, fixture.db.NewRaw(`SELECT action FROM security_audit_events WHERE action LIKE 'family_relationship_%' ORDER BY id`).Scan(ctx, &actions))
	assert.Equal(t, []string{
		"family_relationship_created", "family_relationship_created", "family_relationship_created",
		"family_relationship_updated", "family_relationship_archived",
	}, actions)
}

func TestCycleAttemptsLeaveFamilyGraphAndAuditHistoryUnchanged(t *testing.T) {
	fixture := newFamilyFixture(t)
	ctx := context.Background()
	grandparent := addFamilyPerson(t, fixture.db, "Grandparent")
	parent := addFamilyPerson(t, fixture.db, "Parent")
	child := addFamilyPerson(t, fixture.db, "Child")
	createFamilyRelationship(t, fixture, "parent_child", grandparent, parent, "")
	createFamilyRelationship(t, fixture, "parent_child", parent, child, "")

	var beforeRelationships, beforeAudits int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM family_relationships`).Scan(ctx, &beforeRelationships))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM security_audit_events`).Scan(ctx, &beforeAudits))
	_, err := fixture.service.Create(ctx, fixture.actor, MutationRequest{RelationshipType: "parent_child", PersonAID: child.String(), PersonBID: grandparent.String()})
	require.ErrorIs(t, err, ErrCycle)

	var afterRelationships, afterAudits int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM family_relationships`).Scan(ctx, &afterRelationships))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM security_audit_events`).Scan(ctx, &afterAudits))
	assert.Equal(t, beforeRelationships, afterRelationships)
	assert.Equal(t, beforeAudits, afterAudits)
}

func TestEditingParentChildConnectionRejectsCycleWithoutChangingRelationship(t *testing.T) {
	fixture := newFamilyFixture(t)
	ctx := context.Background()
	a := addFamilyPerson(t, fixture.db, "A")
	b := addFamilyPerson(t, fixture.db, "B")
	c := addFamilyPerson(t, fixture.db, "C")
	d := addFamilyPerson(t, fixture.db, "D")
	createFamilyRelationship(t, fixture, "parent_child", a, b, "")
	createFamilyRelationship(t, fixture, "parent_child", b, c, "")
	candidate := createFamilyRelationship(t, fixture, "sibling", a, d, "")
	var beforeAudits int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM security_audit_events`).Scan(ctx, &beforeAudits))

	_, err := fixture.service.Update(ctx, fixture.actor, uuid.MustParse(candidate.ID), MutationRequest{
		RelationshipType: "parent_child", PersonAID: c.String(), PersonBID: a.String(), Version: candidate.Version,
	})
	require.ErrorIs(t, err, ErrCycle)
	stored, err := getRelationship(ctx, fixture.db, uuid.MustParse(candidate.ID), false)
	require.NoError(t, err)
	assert.Equal(t, "sibling", stored.Type)
	assert.Equal(t, candidate.PersonA.ID, stored.PersonA.ID)
	assert.Equal(t, candidate.PersonB.ID, stored.PersonB.ID)
	assert.Equal(t, int64(1), stored.Version)
	var afterAudits int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM security_audit_events`).Scan(ctx, &afterAudits))
	assert.Equal(t, beforeAudits, afterAudits)
}

func TestFamilyBranchTraversesDeepDescendantsWithMultipleParentsAndPartnersDeterministically(t *testing.T) {
	fixture := newFamilyFixture(t)
	ctx := context.Background()
	root := addFamilyPerson(t, fixture.db, "Root")
	rootPartnerA := addFamilyPerson(t, fixture.db, "A Root partner")
	rootPartnerB := addFamilyPerson(t, fixture.db, "B Root partner")
	otherParent := addFamilyPerson(t, fixture.db, "Other parent")
	child := addFamilyPerson(t, fixture.db, "Child")
	childPartnerA := addFamilyPerson(t, fixture.db, "A Child partner")
	childPartnerB := addFamilyPerson(t, fixture.db, "B Child partner")
	grandchild := addFamilyPerson(t, fixture.db, "Grandchild")
	grandchildPartner := addFamilyPerson(t, fixture.db, "Grandchild partner")
	greatGrandchild := addFamilyPerson(t, fixture.db, "Great-grandchild")
	formerPartner := addFamilyPerson(t, fixture.db, "Former partner")
	sibling := addFamilyPerson(t, fixture.db, "Sibling")
	siblingChild := addFamilyPerson(t, fixture.db, "Sibling child")
	archivedConnectionPerson := addFamilyPerson(t, fixture.db, "Archived connection")

	createFamilyRelationship(t, fixture, "partner", root, rootPartnerA, "current")
	createFamilyRelationship(t, fixture, "partner", rootPartnerB, root, "current")
	createFamilyRelationship(t, fixture, "parent_child", root, child, "")
	createFamilyRelationship(t, fixture, "parent_child", otherParent, child, "")
	createFamilyRelationship(t, fixture, "partner", child, childPartnerA, "current")
	createFamilyRelationship(t, fixture, "partner", childPartnerB, child, "current")
	createFamilyRelationship(t, fixture, "parent_child", child, grandchild, "")
	createFamilyRelationship(t, fixture, "parent_child", childPartnerA, grandchild, "")
	createFamilyRelationship(t, fixture, "partner", grandchild, grandchildPartner, "current")
	createFamilyRelationship(t, fixture, "parent_child", grandchild, greatGrandchild, "")
	createFamilyRelationship(t, fixture, "partner", root, formerPartner, "former")
	createFamilyRelationship(t, fixture, "sibling", root, sibling, "")
	createFamilyRelationship(t, fixture, "parent_child", sibling, siblingChild, "")
	archived := createFamilyRelationship(t, fixture, "parent_child", root, archivedConnectionPerson, "")
	_, err := fixture.service.Archive(ctx, fixture.actor, uuid.MustParse(archived.ID), archived.Version)
	require.NoError(t, err)

	first, err := fixture.service.Branch(ctx, root)
	require.NoError(t, err)
	second, err := fixture.service.Branch(ctx, root)
	require.NoError(t, err)
	assert.Equal(t, first, second)

	names := make([]string, 0, len(first.Members))
	connections := make([]string, 0, len(first.Members))
	generations := make([]int, 0, len(first.Members))
	for _, member := range first.Members {
		names = append(names, member.Person.DisplayName)
		connections = append(connections, member.ConnectionType)
		generations = append(generations, member.Generation)
	}
	assert.Equal(t, []string{
		"A Root partner", "B Root partner",
		"Child", "A Child partner", "B Child partner",
		"Grandchild", "Grandchild partner",
		"Great-grandchild",
	}, names)
	assert.Equal(t, []string{
		"current_partner", "current_partner",
		"descendant", "descendant_current_partner", "descendant_current_partner",
		"descendant", "descendant_current_partner",
		"descendant",
	}, connections)
	assert.Equal(t, []int{0, 0, 1, 1, 1, 2, 2, 3}, generations)
	assert.NotContains(t, names, "Other parent")
	assert.NotContains(t, names, "Former partner")
	assert.NotContains(t, names, "Sibling")
	assert.NotContains(t, names, "Sibling child")
	assert.NotContains(t, names, "Archived connection")
}

func TestFamilyMutationAuditFailuresRollBackGraphChanges(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		fixture := newFamilyFixture(t)
		a := addFamilyPerson(t, fixture.db, "A")
		b := addFamilyPerson(t, fixture.db, "B")
		installRejectFamilyAudit(t, fixture.db)
		_, err := fixture.service.Create(context.Background(), fixture.actor, MutationRequest{RelationshipType: "sibling", PersonAID: a.String(), PersonBID: b.String()})
		require.ErrorContains(t, err, "rejected Family audit")
		var relationships int
		require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM family_relationships`).Scan(context.Background(), &relationships))
		assert.Zero(t, relationships)
	})

	for _, mutation := range []string{"update", "archive"} {
		t.Run(mutation, func(t *testing.T) {
			fixture := newFamilyFixture(t)
			ctx := context.Background()
			a := addFamilyPerson(t, fixture.db, "A")
			b := addFamilyPerson(t, fixture.db, "B")
			relationship := createFamilyRelationship(t, fixture, "sibling", a, b, "")
			installRejectFamilyAudit(t, fixture.db)
			var err error
			if mutation == "update" {
				_, err = fixture.service.Update(ctx, fixture.actor, uuid.MustParse(relationship.ID), MutationRequest{RelationshipType: "partner", PersonAID: a.String(), PersonBID: b.String(), PartnerStatus: "former", Version: relationship.Version})
			} else {
				_, err = fixture.service.Archive(ctx, fixture.actor, uuid.MustParse(relationship.ID), relationship.Version)
			}
			require.ErrorContains(t, err, "rejected Family audit")
			stored, getErr := getRelationship(ctx, fixture.db, uuid.MustParse(relationship.ID), false)
			require.NoError(t, getErr)
			assert.Equal(t, "sibling", stored.Type)
			assert.Equal(t, int64(1), stored.Version)
			assert.Nil(t, stored.ArchivedAt)
		})
	}
}

func installRejectFamilyAudit(t *testing.T, db *bun.DB) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		CREATE FUNCTION reject_family_audit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.action LIKE 'family_relationship_%' THEN
				RAISE EXCEPTION 'rejected Family audit';
			END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER reject_family_audit BEFORE INSERT ON security_audit_events
		FOR EACH ROW EXECUTE FUNCTION reject_family_audit()`)
	require.NoError(t, err)
}

func TestFamilySchemaEnforcesCanonicalPairsConstraintsAndIndexes(t *testing.T) {
	fixture := newFamilyFixture(t)
	ctx := context.Background()
	a := addFamilyPerson(t, fixture.db, "A")
	b := addFamilyPerson(t, fixture.db, "B")
	lower, higher := a, b
	if bytes.Compare(lower[:], higher[:]) > 0 {
		lower, higher = higher, lower
	}

	_, err := fixture.db.NewRaw(`INSERT INTO family_relationships (id, relationship_type, person_a_id, person_b_id) VALUES (?, 'sibling', ?, ?)`, uuid.New(), higher, lower).Exec(ctx)
	require.Error(t, err, "symmetric pairs must use canonical endpoint ordering")
	_, err = fixture.db.NewRaw(`INSERT INTO family_relationships (id, relationship_type, person_a_id, person_b_id) VALUES (?, 'parent_child', ?, ?)`, uuid.New(), a, a).Exec(ctx)
	require.Error(t, err, "self relationships must be rejected")
	activeID := uuid.New()
	_, err = fixture.db.NewRaw(`INSERT INTO family_relationships (id, relationship_type, person_a_id, person_b_id) VALUES (?, 'sibling', ?, ?)`, activeID, lower, higher).Exec(ctx)
	require.NoError(t, err)
	_, err = fixture.db.NewRaw(`INSERT INTO family_relationships (id, relationship_type, person_a_id, person_b_id) VALUES (?, 'sibling', ?, ?)`, uuid.New(), lower, higher).Exec(ctx)
	require.Error(t, err, "active relationship pairs must be unique")
	_, err = fixture.db.NewRaw(`UPDATE family_relationships SET archived_at = now() WHERE id = ?`, activeID).Exec(ctx)
	require.NoError(t, err)
	_, err = fixture.db.NewRaw(`INSERT INTO family_relationships (id, relationship_type, person_a_id, person_b_id) VALUES (?, 'sibling', ?, ?)`, uuid.New(), lower, higher).Exec(ctx)
	require.NoError(t, err, "an archived relationship must not prevent recreating the same active pair")
	_, err = fixture.db.NewRaw(`INSERT INTO family_relationships (id, relationship_type, person_a_id, person_b_id) VALUES (?, 'parent_child', ?, ?)`, uuid.New(), uuid.New(), b).Exec(ctx)
	require.Error(t, err, "relationship endpoints must reference People")

	var indexes []string
	require.NoError(t, fixture.db.NewRaw(`SELECT indexname FROM pg_indexes WHERE schemaname = current_schema() AND tablename = 'family_relationships' ORDER BY indexname`).Scan(ctx, &indexes))
	assert.ElementsMatch(t, []string{
		"family_relationships_active_a_idx", "family_relationships_active_b_idx",
		"family_relationships_active_unique_idx", "family_relationships_current_partner_a_idx",
		"family_relationships_current_partner_b_idx", "family_relationships_pkey",
	}, indexes)
}

func TestConcurrentOpposingParentChildCreatesCannotIntroduceCycle(t *testing.T) {
	fixture := newFamilyFixture(t)
	ctx := context.Background()
	a := addFamilyPerson(t, fixture.db, "A")
	b := addFamilyPerson(t, fixture.db, "B")
	_, err := fixture.db.ExecContext(ctx, `
		CREATE FUNCTION delay_family_insert() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN PERFORM pg_sleep(0.2); RETURN NEW; END $$;
		CREATE TRIGGER delay_family_insert BEFORE INSERT ON family_relationships
		FOR EACH ROW EXECUTE FUNCTION delay_family_insert()`)
	require.NoError(t, err)

	requests := []MutationRequest{
		{RelationshipType: "parent_child", PersonAID: a.String(), PersonBID: b.String()},
		{RelationshipType: "parent_child", PersonAID: b.String(), PersonBID: a.String()},
	}
	errors := make([]error, len(requests))
	start := make(chan struct{})
	var ready sync.WaitGroup
	var done sync.WaitGroup
	ready.Add(len(requests))
	done.Add(len(requests))
	for index := range requests {
		go func(index int) {
			defer done.Done()
			ready.Done()
			<-start
			_, errors[index] = fixture.service.Create(ctx, fixture.actor, requests[index])
		}(index)
	}
	ready.Wait()
	close(start)
	done.Wait()

	if errors[0] == nil {
		require.ErrorIs(t, errors[1], ErrCycle)
	} else {
		require.NoError(t, errors[1])
		require.ErrorIs(t, errors[0], ErrCycle)
	}
	var active int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM family_relationships WHERE archived_at IS NULL`).Scan(ctx, &active))
	assert.Equal(t, 1, active)
}

func TestFamilyMutationsRejectDuplicatesStaleVersionsAndUnavailablePeople(t *testing.T) {
	fixture := newFamilyFixture(t)
	ctx := context.Background()
	a := addFamilyPerson(t, fixture.db, "A")
	b := addFamilyPerson(t, fixture.db, "B")
	relationship := createFamilyRelationship(t, fixture, "partner", a, b, "current")
	_, err := fixture.service.Create(ctx, fixture.actor, MutationRequest{RelationshipType: "partner", PersonAID: b.String(), PersonBID: a.String(), PartnerStatus: "former"})
	require.ErrorIs(t, err, ErrDuplicate)

	updated, err := fixture.service.Update(ctx, fixture.actor, uuid.MustParse(relationship.ID), MutationRequest{RelationshipType: "partner", PersonAID: a.String(), PersonBID: b.String(), PartnerStatus: "former", Version: relationship.Version})
	require.NoError(t, err)
	_, err = fixture.service.Update(ctx, fixture.actor, uuid.MustParse(relationship.ID), MutationRequest{RelationshipType: "partner", PersonAID: a.String(), PersonBID: b.String(), PartnerStatus: "current", Version: relationship.Version})
	require.ErrorIs(t, err, ErrStale)
	_, err = fixture.service.Archive(ctx, fixture.actor, uuid.MustParse(relationship.ID), relationship.Version)
	require.ErrorIs(t, err, ErrStale)
	_, err = fixture.service.Update(ctx, fixture.actor, uuid.New(), MutationRequest{RelationshipType: "sibling", PersonAID: a.String(), PersonBID: b.String(), Version: 1})
	require.ErrorIs(t, err, ErrNotFound)
	_, err = fixture.service.Archive(ctx, fixture.actor, uuid.New(), 1)
	require.ErrorIs(t, err, ErrNotFound)
	assert.Equal(t, int64(2), updated.Version)

	archivedPerson := addFamilyPerson(t, fixture.db, "Archived")
	_, err = fixture.db.NewRaw(`UPDATE people SET archived_at = now() WHERE id = ?`, archivedPerson).Exec(ctx)
	require.NoError(t, err)
	_, err = fixture.service.Create(ctx, fixture.actor, MutationRequest{RelationshipType: "sibling", PersonAID: a.String(), PersonBID: archivedPerson.String()})
	require.ErrorIs(t, err, ErrPersonUnavailable)
	_, err = fixture.service.Create(ctx, fixture.actor, MutationRequest{RelationshipType: "partner", PersonAID: a.String(), PersonBID: b.String()})
	require.ErrorIs(t, err, ErrInvalid)
	missingPerson := uuid.New()
	_, err = fixture.service.Create(ctx, fixture.actor, MutationRequest{RelationshipType: "sibling", PersonAID: a.String(), PersonBID: missingPerson.String()})
	require.ErrorIs(t, err, ErrPersonNotFound)
	_, err = fixture.service.Branch(ctx, missingPerson)
	require.ErrorIs(t, err, ErrPersonNotFound)
	_, err = fixture.service.Branch(ctx, archivedPerson)
	require.ErrorIs(t, err, ErrPersonUnavailable)
}
