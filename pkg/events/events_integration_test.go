//go:build integration

package events

import (
	"context"
	"crypto/sha256"
	"errors"
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

type draftFixture struct {
	db      *bun.DB
	service *Service
	actor   setup.CuratorSession
	sources map[string]uuid.UUID
	media   map[string]uuid.UUID
}

func newDraftFixture(t *testing.T) draftFixture {
	t.Helper()
	ctx := context.Background()
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(ctx, db))
	now := time.Date(2026, 5, 3, 4, 5, 6, 0, time.UTC)
	personID := uuid.New()
	accessID := uuid.New()
	sessionID := uuid.New()
	credential := sha256.Sum256([]byte("draft-session"))
	_, err := db.NewRaw(`
		INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Curator', 'Curator');
		INSERT INTO recipient_access_generations (
			id, person_id, generation, state, is_current, onboarding_completed_at, created_at, updated_at
		) VALUES (?, ?, 1, 'completed', true, ?, ?, ?);
		INSERT INTO sessions (
			id, credential_hash, person_id, recipient_access_generation_id, security_epoch,
			session_type, idle_expires_at
		) SELECT ?, ?, ?, ?, security_epoch, 'trusted', ? FROM system_settings WHERE id = 1
	`, personID, accessID, personID, now, now, now,
		sessionID, credential[:], personID, accessID, now.Add(time.Hour)).Exec(ctx)
	require.NoError(t, err)

	fixture := draftFixture{
		db: db, service: New(db), actor: setup.CuratorSession{PersonID: personID, SessionID: sessionID},
		sources: map[string]uuid.UUID{"first": uuid.New(), "second": uuid.New(), "ignored": uuid.New()},
		media:   map[string]uuid.UUID{"shared": uuid.New(), "first_only": uuid.New(), "second_only": uuid.New(), "unknown": uuid.New()},
	}
	fixture.service.now = func() time.Time { return now }
	for name, sourceID := range fixture.sources {
		disposition := "unreviewed"
		var ignoredAt any
		if name == "ignored" {
			disposition = "ignored"
			ignoredAt = now
		}
		_, err := db.NewRaw(`
			INSERT INTO source_albums (
				id, immich_album_id, name, description, asset_count, source_created_at,
				source_updated_at, disposition, ignored_at, first_seen_at, last_seen_at,
				source_fingerprint, next_reconciliation_at
			) VALUES (?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, decode(repeat('00', 32), 'hex'), ?)
		`, sourceID, uuid.New(), name+" source", name+" description", now, now,
			disposition, ignoredAt, now, now, now).Exec(ctx)
		require.NoError(t, err)
	}

	fixture.addMedia(t, "shared", "2026-05-02T01:30:00Z", "first", "second")
	fixture.addMedia(t, "first_only", "2026-05-01T22:00:00", "first")
	fixture.addMedia(t, "second_only", "2026-05-02T08:00:00Z", "second")
	fixture.addMedia(t, "unknown", "", "first")
	return fixture
}

func (fixture draftFixture) addMedia(t *testing.T, name, capture string, sourceNames ...string) {
	t.Helper()
	ctx := context.Background()
	mediaID := fixture.media[name]
	immichID := uuid.New()
	var captureValue any
	if capture != "" {
		captureValue = capture
	}
	_, err := fixture.db.NewRaw(`
		INSERT INTO media_items (
			id, immich_asset_id, media_type, width, height, local_date_time, first_seen_at, last_seen_at
		) VALUES (?, ?, 'image', 1200, 800, ?, now(), now())
	`, mediaID, immichID, captureValue).Exec(ctx)
	require.NoError(t, err)
	for _, sourceName := range sourceNames {
		_, err := fixture.db.NewRaw(`
			INSERT INTO source_album_memberships (
				source_album_id, immich_asset_id, media_item_id, first_seen_at,
				last_seen_at, source_fingerprint
			) VALUES (?, ?, ?, now(), now(), decode(repeat('11', 32), 'hex'))
		`, fixture.sources[sourceName], immichID, mediaID).Exec(ctx)
		require.NoError(t, err)
	}
}

func TestDraftsCombineAndDivideSourcesWhileReusingStableMediaIdentities(t *testing.T) {
	fixture := newDraftFixture(t)
	ctx := context.Background()

	combined, err := fixture.service.CreateEvent(ctx, fixture.actor, CreateEventRequest{
		SourceAlbumIDs: []string{fixture.sources["first"].String(), fixture.sources["second"].String()},
		Timezone:       "America/Los_Angeles",
		Title:          "Combined family days",
	})
	require.NoError(t, err)
	assert.Equal(t, "draft", combined.Lifecycle)
	assert.Equal(t, "Combined family days", combined.Title)
	assert.Len(t, combined.Sources, 2)
	require.Len(t, combined.Moments, 2)
	assert.Equal(t, "2026-05-01", combined.Moments[0].ProposedDay)
	assert.Equal(t, "America/Los_Angeles", combined.Moments[0].GroupingTimezone)
	assert.ElementsMatch(t, []string{fixture.media["shared"].String(), fixture.media["first_only"].String()}, mediaIDs(combined.Moments[0].MediaItems))
	assert.Equal(t, []string{fixture.media["second_only"].String()}, mediaIDs(combined.Moments[1].MediaItems))
	assert.Equal(t, []string{fixture.media["unknown"].String()}, mediaIDs(combined.UnassignedMedia))

	divided, err := fixture.service.CreateEvent(ctx, fixture.actor, CreateEventRequest{
		SourceAlbumIDs: []string{fixture.sources["first"].String()},
		MediaItemIDs:   []string{fixture.media["shared"].String(), fixture.media["first_only"].String()},
		Timezone:       "America/Los_Angeles",
	})
	require.NoError(t, err)
	assert.NotEqual(t, combined.ID, divided.ID)
	assert.Equal(t, "first source", divided.Title, "single-Source metadata initializes portal-owned presentation")
	require.Len(t, divided.Moments, 1)
	assert.ElementsMatch(t, []string{fixture.media["shared"].String(), fixture.media["first_only"].String()}, mediaIDs(divided.Moments[0].MediaItems))
	assert.Empty(t, divided.UnassignedMedia, "unselected Source Media remains unpublished")

	stableCombined, err := fixture.service.GetEvent(ctx, uuid.MustParse(combined.ID))
	require.NoError(t, err)
	stableDivided, err := fixture.service.GetEvent(ctx, uuid.MustParse(divided.ID))
	require.NoError(t, err)
	assert.Equal(t, combined.ID, stableCombined.ID)
	assert.Equal(t, divided.ID, stableDivided.ID)
	assert.Contains(t, allEventMediaIDs(stableCombined), fixture.media["shared"].String())
	assert.Contains(t, allEventMediaIDs(stableDivided), fixture.media["shared"].String())

	var eventPlacements, mediaIdentities int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM draft_media_placements`).Scan(ctx, &eventPlacements))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM media_items`).Scan(ctx, &mediaIdentities))
	assert.Equal(t, 6, eventPlacements)
	assert.Equal(t, 4, mediaIdentities, "drafting reuses reconciled Media identities instead of copying Source assets")

	var dispositions []string
	require.NoError(t, fixture.db.NewRaw(`
		SELECT disposition FROM source_albums WHERE id IN (?, ?) ORDER BY id
	`, fixture.sources["first"], fixture.sources["second"]).Scan(ctx, &dispositions))
	assert.Equal(t, []string{"drafted", "drafted"}, dispositions)
}

func TestEventMetadataRemainsPortalOwnedWhileSourceChangesBecomeSuggestions(t *testing.T) {
	fixture := newDraftFixture(t)
	ctx := context.Background()
	event, err := fixture.service.CreateEvent(ctx, fixture.actor, CreateEventRequest{
		SourceAlbumIDs: []string{fixture.sources["first"].String()},
		MediaItemIDs:   []string{fixture.media["first_only"].String()},
		Timezone:       "UTC",
	})
	require.NoError(t, err)
	require.Len(t, event.Sources, 1)
	assert.Nil(t, event.Sources[0].MetadataSuggestion)

	_, err = fixture.db.NewRaw(`
		UPDATE source_albums SET name = 'Later Immich title', description = 'Later Immich description'
		WHERE id = ?
	`, fixture.sources["first"]).Exec(ctx)
	require.NoError(t, err)
	updated, err := fixture.service.GetEvent(ctx, uuid.MustParse(event.ID))
	require.NoError(t, err)
	assert.Equal(t, "first source", updated.Title)
	assert.Equal(t, "first description", updated.Description)
	require.NotNil(t, updated.Sources[0].MetadataSuggestion)
	assert.Equal(t, "Later Immich title", updated.Sources[0].MetadataSuggestion.Name)
	assert.Equal(t, "Later Immich description", updated.Sources[0].MetadataSuggestion.Description)
}

func TestLooseItemsReuseMediaIdentityAndKeepUnknownDatesUnassigned(t *testing.T) {
	fixture := newDraftFixture(t)
	ctx := context.Background()
	created, err := fixture.service.CreateLooseItem(ctx, fixture.actor, CreateLooseItemRequest{
		MediaItemID: fixture.media["unknown"].String(), Timezone: "Pacific/Auckland", Title: "A loose photo",
	})
	require.NoError(t, err)
	assert.Equal(t, fixture.media["unknown"].String(), created.MediaItem.ID)
	assert.Nil(t, created.ProposedDay)
	assert.Equal(t, "draft", created.Lifecycle)

	retried, err := fixture.service.CreateLooseItem(ctx, fixture.actor, CreateLooseItemRequest{
		MediaItemID: fixture.media["unknown"].String(), Timezone: "UTC", Title: "Retry must not overwrite",
	})
	require.NoError(t, err)
	assert.Equal(t, created.ID, retried.ID)
	assert.Equal(t, "A loose photo", retried.Title)
	assert.Equal(t, "Pacific/Auckland", retried.GroupingTimezone)

	var looseRows, auditRows int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM loose_items`).Scan(ctx, &looseRows))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM security_audit_events WHERE action = 'loose_item_draft_created'`).Scan(ctx, &auditRows))
	assert.Equal(t, 1, looseRows)
	assert.Equal(t, 1, auditRows)
}

func TestInvalidDraftInputsRollBackWithoutCreatingPrivateState(t *testing.T) {
	fixture := newDraftFixture(t)
	ctx := context.Background()
	tests := []struct {
		name    string
		request CreateEventRequest
		target  error
	}{
		{"invalid timezone", CreateEventRequest{SourceAlbumIDs: []string{fixture.sources["first"].String()}, Timezone: "Mars/Olympus"}, ErrInvalid},
		{"ignored Source", CreateEventRequest{SourceAlbumIDs: []string{fixture.sources["ignored"].String()}, Timezone: "UTC"}, ErrSourceUnavailable},
		{"Media outside selection", CreateEventRequest{SourceAlbumIDs: []string{fixture.sources["first"].String()}, MediaItemIDs: []string{fixture.media["second_only"].String()}, Timezone: "UTC"}, ErrMediaUnavailable},
		{"missing Source", CreateEventRequest{SourceAlbumIDs: []string{uuid.NewString()}, Timezone: "UTC"}, ErrSourceUnavailable},
		{"duplicate Source", CreateEventRequest{SourceAlbumIDs: []string{fixture.sources["first"].String(), fixture.sources["first"].String()}, Timezone: "UTC"}, ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := fixture.service.CreateEvent(ctx, fixture.actor, test.request)
			require.ErrorIs(t, err, test.target)
		})
	}
	var events, moments, placements, audits int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM events`).Scan(ctx, &events))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM draft_moments`).Scan(ctx, &moments))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM draft_media_placements`).Scan(ctx, &placements))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM security_audit_events WHERE action = 'event_draft_created'`).Scan(ctx, &audits))
	assert.Zero(t, events)
	assert.Zero(t, moments)
	assert.Zero(t, placements)
	assert.Zero(t, audits)
}

func TestDraftAuditFailuresRollBackEventAndLooseItemCreation(t *testing.T) {
	for _, kind := range []string{"event", "loose_item"} {
		t.Run(kind, func(t *testing.T) {
			fixture := newDraftFixture(t)
			ctx := context.Background()
			_, err := fixture.db.ExecContext(ctx, `
				CREATE FUNCTION reject_draft_audit() RETURNS trigger LANGUAGE plpgsql AS $$
				BEGIN
					IF NEW.action IN ('event_draft_created', 'loose_item_draft_created') THEN
						RAISE EXCEPTION 'rejected draft audit';
					END IF;
					RETURN NEW;
				END $$;
				CREATE TRIGGER reject_draft_audit BEFORE INSERT ON security_audit_events
				FOR EACH ROW EXECUTE FUNCTION reject_draft_audit()`)
			require.NoError(t, err)

			if kind == "event" {
				_, err = fixture.service.CreateEvent(ctx, fixture.actor, CreateEventRequest{
					SourceAlbumIDs: []string{fixture.sources["first"].String()},
					MediaItemIDs:   []string{fixture.media["first_only"].String()},
					Timezone:       "UTC",
				})
			} else {
				_, err = fixture.service.CreateLooseItem(ctx, fixture.actor, CreateLooseItemRequest{
					MediaItemID: fixture.media["first_only"].String(), Timezone: "UTC",
				})
			}
			require.ErrorContains(t, err, "rejected draft audit")

			var events, looseItems int
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM events`).Scan(ctx, &events))
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM loose_items`).Scan(ctx, &looseItems))
			assert.Zero(t, events)
			assert.Zero(t, looseItems)
			var disposition string
			require.NoError(t, fixture.db.NewRaw(`SELECT disposition FROM source_albums WHERE id = ?`, fixture.sources["first"]).Scan(ctx, &disposition))
			assert.Equal(t, "unreviewed", disposition)
		})
	}
}

func TestDraftLookupsAndLooseItemValidationFailClosed(t *testing.T) {
	fixture := newDraftFixture(t)
	ctx := context.Background()
	_, err := fixture.service.GetEvent(ctx, uuid.New())
	require.ErrorIs(t, err, ErrNotFound)
	_, err = fixture.service.GetLooseItem(ctx, uuid.New())
	require.ErrorIs(t, err, ErrNotFound)
	_, err = fixture.service.SourceMedia(ctx, uuid.New())
	require.ErrorIs(t, err, ErrNotFound)

	for _, request := range []CreateLooseItemRequest{
		{MediaItemID: "not-an-id", Timezone: "UTC"},
		{MediaItemID: fixture.media["first_only"].String(), Timezone: "Mars/Olympus"},
		{MediaItemID: uuid.NewString(), Timezone: "UTC"},
	} {
		_, err := fixture.service.CreateLooseItem(ctx, fixture.actor, request)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalid) || errors.Is(err, ErrMediaUnavailable))
	}
	var count int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM loose_items`).Scan(ctx, &count))
	assert.Zero(t, count)
}

func mediaIDs(media []MediaItem) []string {
	result := make([]string, 0, len(media))
	for _, item := range media {
		result = append(result, item.ID)
	}
	return result
}

func allEventMediaIDs(event Event) []string {
	result := mediaIDs(event.UnassignedMedia)
	for _, moment := range event.Moments {
		result = append(result, mediaIDs(moment.MediaItems)...)
	}
	return result
}
