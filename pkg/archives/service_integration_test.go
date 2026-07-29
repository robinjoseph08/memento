//go:build integration

package archives

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/internal/placementlock"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/library"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/repairs"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type archiveStub struct {
	planned        []immich.ArchivePart
	companions     map[uuid.UUID]uuid.UUID
	archiveCalls   [][]uuid.UUID
	infoCalls      [][]uuid.UUID
	archiveStarted chan struct{}
	releaseArchive <-chan struct{}
	archivePayload []byte
	archiveNames   map[uuid.UUID]string
	infoErr        error
	archiveErr     error
	archiveOnce    sync.Once
}

func (stub *archiveStub) ArchiveInfo(_ context.Context, ids []uuid.UUID) ([]immich.ArchivePart, error) {
	stub.infoCalls = append(stub.infoCalls, slices.Clone(ids))
	if stub.infoErr != nil {
		return nil, stub.infoErr
	}
	if len(stub.infoCalls) == 1 && stub.planned != nil {
		return stub.planned, nil
	}
	part := immich.ArchivePart{Size: int64(len(ids) * 10), CompanionOf: make(map[uuid.UUID]uuid.UUID)}
	for _, id := range ids {
		part.AssetIDs = append(part.AssetIDs, id)
		if companion := stub.companions[id]; companion != uuid.Nil {
			part.AssetIDs = append(part.AssetIDs, companion)
			part.CompanionOf[companion] = id
		}
	}
	return []immich.ArchivePart{part}, nil
}

func (stub *archiveStub) Archive(ctx context.Context, ids []uuid.UUID) (immich.ArchiveResponse, error) {
	stub.archiveCalls = append(stub.archiveCalls, slices.Clone(ids))
	if stub.archiveErr != nil {
		return immich.ArchiveResponse{}, stub.archiveErr
	}
	if stub.archiveStarted != nil {
		stub.archiveOnce.Do(func() { close(stub.archiveStarted) })
	}
	if stub.releaseArchive != nil {
		select {
		case <-stub.releaseArchive:
		case <-ctx.Done():
			return immich.ArchiveResponse{}, ctx.Err()
		}
	}
	contents := stub.archivePayload
	if contents == nil {
		var buffer bytes.Buffer
		archive := zip.NewWriter(&buffer)
		rawSize := int64(len(ids) * 10)
		for _, part := range stub.planned {
			if slices.Equal(part.AssetIDs, ids) {
				rawSize = part.Size
				break
			}
		}
		remaining := rawSize
		for index, id := range ids {
			entrySize := remaining
			if entriesLeft := int64(len(ids) - index); entriesLeft > 1 {
				entrySize = remaining / entriesLeft
			}
			remaining -= entrySize
			name := stub.archiveNames[id]
			if name == "" {
				name = "Immich source " + id.String() + ".jpg"
			}
			entry, err := archive.Create(name)
			if err != nil {
				return immich.ArchiveResponse{}, err
			}
			if _, err := entry.Write(bytes.Repeat([]byte{'x'}, int(entrySize))); err != nil {
				return immich.ArchiveResponse{}, err
			}
		}
		if err := archive.Close(); err != nil {
			return immich.ArchiveResponse{}, err
		}
		contents = buffer.Bytes()
	}
	return immich.ArchiveResponse{Body: io.NopCloser(bytes.NewReader(contents)), ContentLength: -1}, nil
}

type simultaneousArchiveSource struct {
	part       immich.ArchivePart
	payload    []byte
	bothOpened chan struct{}
	release    <-chan struct{}
	mu         sync.Mutex
	opened     int
}

func (source *simultaneousArchiveSource) ArchiveInfo(_ context.Context, _ []uuid.UUID) ([]immich.ArchivePart, error) {
	return []immich.ArchivePart{source.part}, nil
}

func (source *simultaneousArchiveSource) Archive(ctx context.Context, _ []uuid.UUID) (immich.ArchiveResponse, error) {
	source.mu.Lock()
	source.opened++
	if source.opened == 2 {
		close(source.bothOpened)
	}
	source.mu.Unlock()
	select {
	case <-source.release:
		return immich.ArchiveResponse{Body: io.NopCloser(bytes.NewReader(source.payload)), ContentLength: -1}, nil
	case <-ctx.Done():
		return immich.ArchiveResponse{}, ctx.Err()
	}
}

func singleEntryArchive(t *testing.T, size int) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.Create("Immich source.jpg")
	require.NoError(t, err)
	_, err = entry.Write(bytes.Repeat([]byte{'x'}, size))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return buffer.Bytes()
}

type archiveFixture struct {
	db          *bun.DB
	service     *Service
	source      *archiveStub
	actor       setup.SessionActor
	event       uuid.UUID
	draftMoment uuid.UUID
	media       []uuid.UUID
	assets      []uuid.UUID
	hiddenActor setup.SessionActor
}

func newArchiveFixture(t *testing.T, source *archiveStub) archiveFixture {
	t.Helper()
	ctx := context.Background()
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(ctx, db))
	fixture := archiveFixture{db: db, source: source, event: uuid.New(), draftMoment: uuid.New(), media: []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}, assets: []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}}
	curator, personID, accessID, sessionID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	fixture.actor = setup.SessionActor{PersonID: personID, AccessID: accessID, SessionID: sessionID}
	fixture.hiddenActor = setup.SessionActor{PersonID: uuid.New(), AccessID: uuid.New(), SessionID: uuid.New()}
	publication, publishedMoment, snapshot := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Second)
	_, err := db.NewRaw(`
		UPDATE system_settings SET setup_complete = true WHERE id = 1;
		INSERT INTO people (id, display_name, sort_name) VALUES
			(?, 'Curator', 'curator'), (?, 'Alex', 'alex'), (?, 'Hidden', 'hidden');
		INSERT INTO person_roles (person_id, role) VALUES (?, 'curator'), (?, 'recipient'), (?, 'recipient');
		INSERT INTO recipient_access_generations
			(id, person_id, generation, state, is_current, onboarding_completed_at)
		VALUES (?, ?, 1, 'completed', true, ?), (?, ?, 1, 'completed', true, ?);
		INSERT INTO sessions
			(id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at)
		SELECT ?, decode(repeat('42', 32), 'hex'), ?, ?, security_epoch, 'trusted', ?::timestamptz + interval '1 hour'
		FROM system_settings WHERE id = 1;
		INSERT INTO events (id, lifecycle, title, description, grouping_timezone, version, created_at, updated_at)
		VALUES (?, 'published', '../Family / Weekend', '', 'UTC', 1, ?, ?);
		INSERT INTO publications
			(id, event_id, revision, editable_version, published_by_person_id, notify_recipients, committed_at)
		VALUES (?, ?, 1, 1, ?, false, ?);
		UPDATE events SET current_publication_id = ? WHERE id = ?;
		INSERT INTO current_published_events
			(event_id, publication_id, title, description, grouping_timezone, committed_at)
		VALUES (?, ?, '../Family / Weekend', '', 'UTC', ?);
		INSERT INTO audience_snapshots (id, target_kind, target_id, approved_by_person_id, approved_at, label)
		VALUES (?, 'moment', ?, ?, ?, 'Shared');
		INSERT INTO published_moments
			(id, publication_id, draft_moment_id, audience_snapshot_id, position, title, proposed_day)
		VALUES (?, ?, ?, ?, 0, '', '2026-07-27')
	`, curator, personID, fixture.hiddenActor.PersonID, curator, personID, fixture.hiddenActor.PersonID,
		accessID, personID, now, fixture.hiddenActor.AccessID, fixture.hiddenActor.PersonID, now,
		sessionID, personID, accessID, now,
		fixture.event, now, now, publication, fixture.event, curator, now,
		publication, fixture.event, fixture.event, publication, now,
		snapshot, fixture.draftMoment, curator, now, publishedMoment, publication, fixture.draftMoment, snapshot).Exec(ctx)
	require.NoError(t, err)
	for index := range fixture.media {
		_, err = db.NewRaw(`
			INSERT INTO media_items
				(id, immich_asset_id, media_type, width, height, local_date_time, availability, first_seen_at, last_seen_at)
			VALUES (?, ?, 'image', 100, 100, ?, 'current', ?, ?);
			INSERT INTO media_backings (id, media_item_id, immich_asset_id, linked_at)
			VALUES (gen_random_uuid(), ?, ?, ?);
			INSERT INTO published_media_placements
				(published_moment_id, media_item_id, position, media_type, width, height, local_date_time)
			VALUES (?, ?, ?, 'image', 100, 100, ?);
			INSERT INTO current_published_placements
				(event_id, publication_id, published_moment_id, media_item_id, position)
			VALUES (?, ?, ?, ?, ?)
		`, fixture.media[index], fixture.assets[index], now.Format(time.RFC3339), now, now,
			fixture.media[index], fixture.assets[index], now,
			publishedMoment, fixture.media[index], index, now.Format(time.RFC3339),
			fixture.event, publication, publishedMoment, fixture.media[index], index).Exec(ctx)
		require.NoError(t, err)
		entitledAccess := accessID
		if index == 2 {
			entitledAccess = fixture.hiddenActor.AccessID
		}
		_, err = db.NewRaw(`INSERT INTO current_audience_entitlements
			(event_id, publication_id, recipient_person_id, recipient_access_generation_id, media_item_id)
		SELECT ?, ?, person_id, ?, ? FROM recipient_access_generations WHERE id = ?`, fixture.event,
			publication, entitledAccess, fixture.media[index], entitledAccess).Exec(ctx)
		require.NoError(t, err)
	}
	_, err = db.NewRaw(`INSERT INTO sessions
		(id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at)
		SELECT ?, decode(repeat('44', 32), 'hex'), ?, ?, security_epoch, 'trusted', now() + interval '1 hour'
		FROM system_settings WHERE id = 1`, fixture.hiddenActor.SessionID, fixture.hiddenActor.PersonID, fixture.hiddenActor.AccessID).Exec(ctx)
	require.NoError(t, err)
	fixture.service = New(db, source)
	return fixture
}

func addAuthorizedReuse(t *testing.T, fixture archiveFixture, mediaIndex int) {
	t.Helper()
	ctx := context.Background()
	eventID, publicationID := uuid.New(), uuid.New()
	publishedMomentID, draftMomentID, snapshotID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Second)
	_, err := fixture.db.NewRaw(`
		INSERT INTO events (id, lifecycle, title, description, grouping_timezone, version, created_at, updated_at)
		VALUES (?, 'published', 'Other Event', '', 'UTC', 1, ?, ?);
		INSERT INTO publications
			(id, event_id, revision, editable_version, published_by_person_id, notify_recipients, committed_at)
		VALUES (?, ?, 1, 1, ?, false, ?);
		UPDATE events SET current_publication_id = ? WHERE id = ?;
		INSERT INTO current_published_events
			(event_id, publication_id, title, description, grouping_timezone, committed_at)
		VALUES (?, ?, 'Other Event', '', 'UTC', ?);
		INSERT INTO audience_snapshots (id, target_kind, target_id, approved_by_person_id, approved_at, label)
		VALUES (?, 'moment', ?, ?, ?, 'Shared');
		INSERT INTO published_moments
			(id, publication_id, draft_moment_id, audience_snapshot_id, position, title, proposed_day)
		VALUES (?, ?, ?, ?, 0, '', '2026-07-28');
		INSERT INTO published_media_placements
			(published_moment_id, media_item_id, position, media_type, width, height, local_date_time)
		VALUES (?, ?, 0, 'image', 100, 100, ?);
		INSERT INTO current_published_placements
			(event_id, publication_id, published_moment_id, media_item_id, position)
		VALUES (?, ?, ?, ?, 0);
		INSERT INTO current_audience_entitlements
			(event_id, publication_id, recipient_person_id, recipient_access_generation_id, media_item_id)
		VALUES (?, ?, ?, ?, ?)
	`, eventID, now, now, publicationID, eventID, fixture.actor.PersonID, now,
		publicationID, eventID, eventID, publicationID, now,
		snapshotID, draftMomentID, fixture.actor.PersonID, now,
		publishedMomentID, publicationID, draftMomentID, snapshotID,
		publishedMomentID, fixture.media[mediaIndex], now.Format(time.RFC3339),
		eventID, publicationID, publishedMomentID, fixture.media[mediaIndex],
		eventID, publicationID, fixture.actor.PersonID, fixture.actor.AccessID, fixture.media[mediaIndex]).Exec(ctx)
	require.NoError(t, err)
}

func TestPlansCompleteEventAndRejectsIncompleteSubset(t *testing.T) {
	source := &archiveStub{}
	fixture := newArchiveFixture(t, source)
	source.planned = []immich.ArchivePart{
		{Size: 12, AssetIDs: []uuid.UUID{fixture.assets[0]}, CompanionOf: map[uuid.UUID]uuid.UUID{}},
		{Size: 15, AssetIDs: []uuid.UUID{fixture.assets[1]}, CompanionOf: map[uuid.UUID]uuid.UUID{}},
	}
	fixedNow := time.Now().UTC().Truncate(time.Second)
	fixture.service.now = func() time.Time { return fixedNow }

	response, err := fixture.service.Plan(context.Background(), fixture.actor, PlanRequest{Scope: "event", EventID: stringPointer(fixture.event.String())})
	require.NoError(t, err)
	assert.Equal(t, 2, response.ItemCount)
	assert.Equal(t, int64(27), response.TotalSize)
	assert.Equal(t, fixedNow.Add(15*time.Minute), response.ExpiresAt)
	assert.Equal(t, "Family-Weekend", response.Name)
	require.Len(t, response.Parts, 2)
	assert.Equal(t, PartSummary{
		PartNumber: 1, Size: 12, Filename: "Family-Weekend-part-1-of-2.zip",
		DownloadURL: response.Parts[0].DownloadURL,
	}, response.Parts[0])
	assert.Equal(t, PartSummary{
		PartNumber: 2, Size: 15, Filename: "Family-Weekend-part-2-of-2.zip",
		DownloadURL: response.Parts[1].DownloadURL,
	}, response.Parts[1])
	var planToken string
	for index, part := range response.Parts {
		assert.NotContains(t, part.Filename, "..")
		assert.NotContains(t, part.Filename, "/")
		downloadURL, parseErr := url.Parse(part.DownloadURL)
		require.NoError(t, parseErr)
		assert.Equal(t, fmt.Sprintf("/api/me/archives/parts/%d", index+1), downloadURL.Path,
			"archive secrets stay out of request-log paths")
		if index == 0 {
			planToken = downloadURL.Query().Get("token")
			assert.NotEmpty(t, planToken)
		} else {
			assert.Equal(t, planToken, downloadURL.Query().Get("token"))
		}
	}
	assert.ElementsMatch(t, fixture.assets[:2], source.infoCalls[0])

	_, err = fixture.service.Plan(context.Background(), fixture.actor, PlanRequest{Scope: "subset", MediaIDs: []string{fixture.media[0].String(), fixture.media[2].String()}})
	assert.ErrorIs(t, err, ErrInvalidSelection)
	assert.Len(t, source.infoCalls, 1, "the complete selection is authorized before Immich is contacted")
}

func TestArchiveNotFoundMarksPublishedMediaUnavailableAndPreservesHistory(t *testing.T) {
	for _, path := range []string{"info", "original"} {
		t.Run(path, func(t *testing.T) {
			source := &archiveStub{}
			fixture := newArchiveFixture(t, source)
			source.planned = []immich.ArchivePart{{
				Size: 10, AssetIDs: []uuid.UUID{fixture.assets[0]}, CompanionOf: map[uuid.UUID]uuid.UUID{},
			}}
			plan, err := fixture.service.Plan(context.Background(), fixture.actor, PlanRequest{
				Scope: "subset", MediaIDs: []string{fixture.media[0].String()},
			})
			require.NoError(t, err)
			if path == "info" {
				source.infoErr = immich.ErrNotFound
			} else {
				source.archiveErr = immich.ErrNotFound
			}

			_, err = fixture.service.StreamPart(context.Background(), fixture.actor, tokenFromURL(plan.Parts[0].DownloadURL), 1)
			assert.ErrorIs(t, err, ErrNotFound)
			var availability string
			var missingSince *time.Time
			require.NoError(t, fixture.db.NewRaw(`SELECT availability, missing_since FROM media_items WHERE id = ?`, fixture.media[0]).Scan(context.Background(), &availability, &missingSince))
			assert.Equal(t, "source_missing", availability)
			assert.NotNil(t, missingSince)

			page, err := library.New(fixture.db, nil).Photos(context.Background(), fixture.actor, "10", "", false)
			require.NoError(t, err)
			var listed *library.Media
			for index := range page.Media {
				if page.Media[index].ID == fixture.media[0].String() {
					listed = &page.Media[index]
				}
			}
			require.NotNil(t, listed, "published archive Media remains in Recipient history")
			assert.False(t, listed.Available)
			assert.Empty(t, listed.OriginalURL)
			problems, err := repairs.New(fixture.db, nil).List(context.Background())
			require.NoError(t, err)
			require.NotEmpty(t, problems.SourceProblems)
			assert.Equal(t, fixture.media[0].String(), problems.SourceProblems[0].ID)
			assert.True(t, problems.SourceProblems[0].Published)

			beforeCalls := len(source.infoCalls)
			_, err = fixture.service.Plan(context.Background(), fixture.actor, PlanRequest{
				Scope: "subset", MediaIDs: []string{fixture.media[0].String()},
			})
			assert.ErrorIs(t, err, ErrInvalidSelection)
			assert.Len(t, source.infoCalls, beforeCalls, "subsequent archive delivery fails before Immich")
		})
	}
}

func TestArchiveFailuresOtherThanNotFoundPreserveAvailability(t *testing.T) {
	for _, failure := range []struct {
		name string
		err  error
	}{
		{name: "malformed", err: errors.New("Immich returned malformed archive metadata")},
		{name: "unauthorized", err: errors.New("Immich API key is invalid")},
		{name: "dependency", err: context.DeadlineExceeded},
	} {
		t.Run(failure.name, func(t *testing.T) {
			source := &archiveStub{infoErr: failure.err}
			fixture := newArchiveFixture(t, source)
			_, err := fixture.service.Plan(context.Background(), fixture.actor, PlanRequest{
				Scope: "subset", MediaIDs: []string{fixture.media[0].String()},
			})
			assert.ErrorIs(t, err, ErrUnavailable)
			var availability string
			var missingSince *time.Time
			require.NoError(t, fixture.db.NewRaw(`SELECT availability, missing_since FROM media_items WHERE id = ?`, fixture.media[0]).Scan(context.Background(), &availability, &missingSince))
			assert.Equal(t, "current", availability)
			assert.Nil(t, missingSince)
		})
	}
}

func TestCleanupExpiredPlansCascadesAndRetainsActivePlans(t *testing.T) {
	source := &archiveStub{}
	fixture := newArchiveFixture(t, source)
	source.planned = []immich.ArchivePart{{
		Size: 12, AssetIDs: []uuid.UUID{fixture.assets[0]}, CompanionOf: map[uuid.UUID]uuid.UUID{},
	}}
	now := time.Now().UTC().Truncate(time.Second)
	fixture.service.now = func() time.Time { return now }
	_, err := fixture.service.Plan(context.Background(), fixture.actor, PlanRequest{
		Scope: "subset", MediaIDs: []string{fixture.media[0].String()},
	})
	require.NoError(t, err)

	now = now.Add(planLifetime)
	_, err = fixture.service.Plan(context.Background(), fixture.actor, PlanRequest{
		Scope: "subset", MediaIDs: []string{fixture.media[0].String()},
	})
	require.NoError(t, err)
	deleted, err := fixture.service.cleanupExpiredPlans(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	for _, table := range []string{"archive_plans", "archive_parts", "archive_part_items"} {
		var count int
		require.NoError(t, fixture.db.NewRaw("SELECT count(*) FROM "+table).Scan(context.Background(), &count))
		assert.Equal(t, 1, count, "%s should retain only the active plan's records", table)
	}
	var jobs int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM jobs
		WHERE kind = ? AND idempotency_key = 'archive-plans-cleanup'`, CleanupJobKind).Scan(context.Background(), &jobs))
	assert.Equal(t, 1, jobs, "the durable cleanup job should be seeded once")
}

func TestCleanupExpiredPlansBoundsEachPass(t *testing.T) {
	fixture := newArchiveFixture(t, &archiveStub{})
	now := time.Now().UTC().Truncate(time.Second)
	fixture.service.now = func() time.Time { return now }
	_, err := fixture.db.NewRaw(`INSERT INTO archive_plans
		(id, token_hash, recipient_person_id, recipient_access_generation_id, session_id,
		 scope, name, item_count, total_size, created_at, expires_at)
		SELECT gen_random_uuid(), decode(md5('expired-a-' || sequence) || md5('expired-b-' || sequence), 'hex'),
		       ?, ?, ?, 'subset', 'Expired', 1, 1, ?::timestamptz - interval '30 minutes',
		       ?::timestamptz - interval '15 minutes'
		FROM generate_series(1, ?) AS sequence`, fixture.actor.PersonID, fixture.actor.AccessID,
		fixture.actor.SessionID, now, now, cleanupBatchSize+1).Exec(context.Background())
	require.NoError(t, err)

	deleted, err := fixture.service.cleanupExpiredPlans(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(cleanupBatchSize), deleted)
	var remaining int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM archive_plans`).Scan(context.Background(), &remaining))
	assert.Equal(t, 1, remaining)

	deleted, err = fixture.service.cleanupExpiredPlans(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
}

func TestPartsAreSessionBoundExpiringAndIndividuallySingleUse(t *testing.T) {
	source := &archiveStub{archiveNames: make(map[uuid.UUID]string)}
	fixture := newArchiveFixture(t, source)
	source.planned = []immich.ArchivePart{
		{Size: 12, AssetIDs: []uuid.UUID{fixture.assets[0]}, CompanionOf: map[uuid.UUID]uuid.UUID{}},
		{Size: 15, AssetIDs: []uuid.UUID{fixture.assets[1]}, CompanionOf: map[uuid.UUID]uuid.UUID{}},
	}
	now := time.Now().UTC().Truncate(time.Second)
	fixture.service.now = func() time.Time { return now }
	source.archiveNames[fixture.assets[0]] = "../../private/source-library-name.JPG"
	plan, err := fixture.service.Plan(context.Background(), fixture.actor, PlanRequest{Scope: "event", EventID: stringPointer(fixture.event.String())})
	require.NoError(t, err)
	token := tokenFromURL(plan.Parts[0].DownloadURL)

	otherSession := fixture.actor
	otherSession.SessionID = uuid.New()
	_, err = fixture.db.NewRaw(`INSERT INTO sessions
		(id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at)
		SELECT ?, decode(repeat('43', 32), 'hex'), ?, ?, security_epoch, 'trusted', now() + interval '1 hour'
		FROM system_settings WHERE id = 1`, otherSession.SessionID, otherSession.PersonID, otherSession.AccessID).Exec(context.Background())
	require.NoError(t, err)
	_, err = fixture.service.StreamPart(context.Background(), otherSession, token, 1)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = fixture.service.StreamPart(context.Background(), fixture.hiddenActor, token, 1)
	assert.ErrorIs(t, err, ErrNotFound)

	stream, err := fixture.service.StreamPart(context.Background(), fixture.actor, token, 1)
	require.NoError(t, err)
	contents, err := io.ReadAll(stream.Body)
	require.NoError(t, err)
	require.NoError(t, stream.Body.Close())
	assert.Equal(t, int64(len(contents)), stream.ContentLength)
	assert.Equal(t, "Family-Weekend-part-1-of-2.zip", stream.Filename)
	archive, err := zip.NewReader(bytes.NewReader(contents), int64(len(contents)))
	require.NoError(t, err)
	require.Len(t, archive.File, 1)
	assert.Equal(t, "Family-Weekend/0001-media.jpg", archive.File[0].Name)
	assert.NotContains(t, archive.File[0].Name, "source-library-name")
	entry, err := archive.File[0].Open()
	require.NoError(t, err)
	entryContents, err := io.ReadAll(entry)
	require.NoError(t, err)
	require.NoError(t, entry.Close())
	assert.Len(t, entryContents, 12)
	require.Len(t, source.archiveCalls, 1)
	assert.Equal(t, []uuid.UUID{fixture.assets[0]}, source.archiveCalls[0])
	_, err = fixture.service.StreamPart(context.Background(), fixture.actor, token, 1)
	assert.ErrorIs(t, err, ErrNotFound)
	assert.Len(t, source.archiveCalls, 1, "replay is rejected before opening Immich")

	_, err = fixture.db.NewRaw(`UPDATE recipient_access_generations SET state = 'revoked', is_current = false, ended_at = now() WHERE id = ?`, fixture.actor.AccessID).Exec(context.Background())
	require.NoError(t, err)
	_, err = fixture.service.StreamPart(context.Background(), fixture.actor, token, 2)
	assert.ErrorIs(t, err, ErrNotFound)
	assert.Len(t, source.archiveCalls, 1)

	_, err = fixture.db.NewRaw(`UPDATE recipient_access_generations SET state = 'completed', is_current = true, ended_at = NULL WHERE id = ?`, fixture.actor.AccessID).Exec(context.Background())
	require.NoError(t, err)
	now = plan.ExpiresAt
	_, err = fixture.service.StreamPart(context.Background(), fixture.actor, token, 2)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestMultipartPartsUseDistinctAssetsAndSingleUseState(t *testing.T) {
	source := &archiveStub{}
	fixture := newArchiveFixture(t, source)
	source.planned = []immich.ArchivePart{
		{Size: 12, AssetIDs: []uuid.UUID{fixture.assets[0]}, CompanionOf: map[uuid.UUID]uuid.UUID{}},
		{Size: 15, AssetIDs: []uuid.UUID{fixture.assets[1]}, CompanionOf: map[uuid.UUID]uuid.UUID{}},
	}
	plan, err := fixture.service.Plan(context.Background(), fixture.actor, PlanRequest{
		Scope: "subset", MediaIDs: []string{fixture.media[0].String(), fixture.media[1].String()},
	})
	require.NoError(t, err)
	require.Len(t, plan.Parts, 2)
	token := tokenFromURL(plan.Parts[0].DownloadURL)
	assert.Equal(t, token, tokenFromURL(plan.Parts[1].DownloadURL))

	second, err := fixture.service.StreamPart(context.Background(), fixture.actor, token, 2)
	require.NoError(t, err)
	assert.Equal(t, "Memento-selection-part-2-of-2.zip", second.Filename)
	secondContents, err := io.ReadAll(second.Body)
	require.NoError(t, err)
	require.NoError(t, second.Body.Close())
	assert.Equal(t, int64(len(secondContents)), second.ContentLength)

	first, err := fixture.service.StreamPart(context.Background(), fixture.actor, token, 1)
	require.NoError(t, err, "consuming part two must not consume part one")
	assert.Equal(t, "Memento-selection-part-1-of-2.zip", first.Filename)
	firstContents, err := io.ReadAll(first.Body)
	require.NoError(t, err)
	require.NoError(t, first.Body.Close())
	assert.Equal(t, int64(len(firstContents)), first.ContentLength)

	assert.Equal(t, [][]uuid.UUID{{fixture.assets[1]}, {fixture.assets[0]}}, source.archiveCalls)
	_, err = fixture.service.StreamPart(context.Background(), fixture.actor, token, 1)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = fixture.service.StreamPart(context.Background(), fixture.actor, token, 2)
	assert.ErrorIs(t, err, ErrNotFound)
	assert.Len(t, source.archiveCalls, 2, "each consumed part is rejected before reopening Immich")
}

func TestPartRejectsMalformedArchiveWithoutConsumingPart(t *testing.T) {
	source := &archiveStub{}
	fixture := newArchiveFixture(t, source)
	source.planned = []immich.ArchivePart{{
		Size: 12, AssetIDs: []uuid.UUID{fixture.assets[0]}, CompanionOf: map[uuid.UUID]uuid.UUID{},
	}}
	plan, err := fixture.service.Plan(context.Background(), fixture.actor, PlanRequest{
		Scope: "subset", MediaIDs: []string{fixture.media[0].String()},
	})
	require.NoError(t, err)
	token := tokenFromURL(plan.Parts[0].DownloadURL)

	source.archivePayload = []byte("not a ZIP")
	_, err = fixture.service.StreamPart(context.Background(), fixture.actor, token, 1)
	assert.ErrorIs(t, err, ErrUnavailable)

	source.archivePayload = nil
	stream, err := fixture.service.StreamPart(context.Background(), fixture.actor, token, 1)
	require.NoError(t, err)
	require.NoError(t, stream.Body.Close())
}

func TestPartReauthorizationKeepsThePlannedPlacementIdentity(t *testing.T) {
	for _, test := range []struct {
		name       string
		targetKind string
		targetID   func(archiveFixture) uuid.UUID
	}{
		{name: "Event Withdrawal", targetKind: "event", targetID: func(fixture archiveFixture) uuid.UUID { return fixture.event }},
		{name: "Moment Withdrawal", targetKind: "moment", targetID: func(fixture archiveFixture) uuid.UUID { return fixture.draftMoment }},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := &archiveStub{}
			fixture := newArchiveFixture(t, source)
			source.planned = []immich.ArchivePart{{
				Size: 12, AssetIDs: []uuid.UUID{fixture.assets[0]}, CompanionOf: map[uuid.UUID]uuid.UUID{},
			}}
			plan, err := fixture.service.Plan(context.Background(), fixture.actor, PlanRequest{
				Scope: "subset", MediaIDs: []string{fixture.media[0].String()},
			})
			require.NoError(t, err)
			addAuthorizedReuse(t, fixture, 0)
			_, err = fixture.db.NewRaw(`INSERT INTO content_withdrawals
				(id, target_kind, target_id, withdrawn_by_person_id, withdrawn_at, reason)
				VALUES (?, ?, ?, ?, now(), 'Archive placement test')`, uuid.New(), test.targetKind,
				test.targetID(fixture), fixture.actor.PersonID).Exec(context.Background())
			require.NoError(t, err)

			_, err = fixture.service.StreamPart(context.Background(), fixture.actor,
				tokenFromURL(plan.Parts[0].DownloadURL), 1)
			assert.ErrorIs(t, err, ErrNotFound)
			assert.Empty(t, source.archiveCalls, "authorization through another placement must not preserve the planned placement")
		})
	}
}

func TestEveryAccessLossBlocksAnUnstartedPart(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(context.Context, archiveFixture) error
	}{
		{name: "suspension", change: func(ctx context.Context, fixture archiveFixture) error {
			_, err := fixture.db.NewRaw(`UPDATE recipient_access_generations SET state = 'suspended' WHERE id = ?`, fixture.actor.AccessID).Exec(ctx)
			return err
		}},
		{name: "revocation", change: func(ctx context.Context, fixture archiveFixture) error {
			_, err := fixture.db.NewRaw(`UPDATE recipient_access_generations SET state = 'revoked', is_current = false, ended_at = now() WHERE id = ?`, fixture.actor.AccessID).Exec(ctx)
			return err
		}},
		{name: "Session revocation", change: func(ctx context.Context, fixture archiveFixture) error {
			_, err := fixture.db.NewRaw(`UPDATE sessions SET revoked_at = now() WHERE id = ?`, fixture.actor.SessionID).Exec(ctx)
			return err
		}},
		{name: "Withdrawal", change: func(ctx context.Context, fixture archiveFixture) error {
			_, err := fixture.db.NewRaw(`INSERT INTO content_withdrawals
				(id, target_kind, target_id, withdrawn_by_person_id, withdrawn_at, reason)
				VALUES (?, 'media', ?, ?, now(), 'Archive test')`, uuid.New(), fixture.media[0], fixture.actor.PersonID).Exec(ctx)
			return err
		}},
		{name: "entitlement loss", change: func(ctx context.Context, fixture archiveFixture) error {
			_, err := fixture.db.NewRaw(`DELETE FROM current_audience_entitlements
				WHERE recipient_access_generation_id = ? AND media_item_id = ?`, fixture.actor.AccessID, fixture.media[0]).Exec(ctx)
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := &archiveStub{}
			fixture := newArchiveFixture(t, source)
			source.planned = []immich.ArchivePart{{Size: 12, AssetIDs: []uuid.UUID{fixture.assets[0]}, CompanionOf: map[uuid.UUID]uuid.UUID{}}}
			plan, err := fixture.service.Plan(context.Background(), fixture.actor, PlanRequest{Scope: "subset", MediaIDs: []string{fixture.media[0].String()}})
			require.NoError(t, err)
			require.NoError(t, test.change(context.Background(), fixture))

			_, err = fixture.service.StreamPart(context.Background(), fixture.actor, tokenFromURL(plan.Parts[0].DownloadURL), 1)
			assert.ErrorIs(t, err, ErrNotFound)
			assert.Empty(t, source.archiveCalls)
		})
	}
}

func TestArchiveOpeningHonorsCancellationWithoutConsumingPart(t *testing.T) {
	release := make(chan struct{})
	source := &archiveStub{archiveStarted: make(chan struct{}), releaseArchive: release}
	fixture := newArchiveFixture(t, source)
	source.planned = []immich.ArchivePart{{
		Size: 12, AssetIDs: []uuid.UUID{fixture.assets[0]}, CompanionOf: map[uuid.UUID]uuid.UUID{},
	}}
	plan, err := fixture.service.Plan(context.Background(), fixture.actor, PlanRequest{
		Scope: "subset", MediaIDs: []string{fixture.media[0].String()},
	})
	require.NoError(t, err)
	token := tokenFromURL(plan.Parts[0].DownloadURL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	result := make(chan error, 1)
	go func() {
		_, streamErr := fixture.service.StreamPart(ctx, fixture.actor, token, 1)
		result <- streamErr
	}()
	select {
	case <-source.archiveStarted:
	case <-ctx.Done():
		t.Fatalf("upstream archive did not open before deadline: %v", ctx.Err())
	}
	cancel()
	select {
	case streamErr := <-result:
		assert.ErrorIs(t, streamErr, ErrUnavailable)
	case <-time.After(5 * time.Second):
		t.Fatal("canceled archive did not return before deadline")
	}

	close(release)
	source.releaseArchive = nil
	stream, err := fixture.service.StreamPart(context.Background(), fixture.actor, token, 1)
	require.NoError(t, err)
	require.NoError(t, stream.Body.Close())
}

func TestPartReauthorizesAfterOpeningTheUpstreamStream(t *testing.T) {
	release := make(chan struct{})
	source := &archiveStub{archiveStarted: make(chan struct{}), releaseArchive: release}
	fixture := newArchiveFixture(t, source)
	source.planned = []immich.ArchivePart{{
		Size: 12, AssetIDs: []uuid.UUID{fixture.assets[0]}, CompanionOf: map[uuid.UUID]uuid.UUID{},
	}}
	plan, err := fixture.service.Plan(context.Background(), fixture.actor, PlanRequest{Scope: "subset", MediaIDs: []string{fixture.media[0].String()}})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		stream, streamErr := fixture.service.StreamPart(ctx, fixture.actor, tokenFromURL(plan.Parts[0].DownloadURL), 1)
		if stream.Body != nil {
			_ = stream.Body.Close()
		}
		result <- streamErr
	}()
	select {
	case <-source.archiveStarted:
	case <-ctx.Done():
		t.Fatalf("upstream stream did not open before deadline: %v", ctx.Err())
	}
	_, err = fixture.db.NewRaw(`DELETE FROM current_audience_entitlements
		WHERE recipient_access_generation_id = ? AND media_item_id = ?`, fixture.actor.AccessID, fixture.media[0]).Exec(ctx)
	require.NoError(t, err)
	close(release)
	select {
	case streamErr := <-result:
		assert.ErrorIs(t, streamErr, ErrNotFound)
	case <-ctx.Done():
		t.Fatalf("final archive authorization did not finish before deadline: %v", ctx.Err())
	}
}

func TestPartFinalActorAuthorizationWaitsForConcurrentAccessLoss(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(context.Context, bun.Tx, archiveFixture) error
	}{
		{name: "suspension", change: func(ctx context.Context, tx bun.Tx, fixture archiveFixture) error {
			_, err := tx.NewRaw(`UPDATE recipient_access_generations SET state = 'suspended' WHERE id = ?`,
				fixture.actor.AccessID).Exec(ctx)
			return err
		}},
		{name: "Session revocation", change: func(ctx context.Context, tx bun.Tx, fixture archiveFixture) error {
			_, err := tx.NewRaw(`UPDATE sessions SET revoked_at = now() WHERE id = ?`, fixture.actor.SessionID).Exec(ctx)
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			release := make(chan struct{})
			source := &archiveStub{archiveStarted: make(chan struct{}), releaseArchive: release}
			fixture := newArchiveFixture(t, source)
			source.planned = []immich.ArchivePart{{
				Size: 12, AssetIDs: []uuid.UUID{fixture.assets[0]}, CompanionOf: map[uuid.UUID]uuid.UUID{},
			}}
			plan, err := fixture.service.Plan(context.Background(), fixture.actor, PlanRequest{
				Scope: "subset", MediaIDs: []string{fixture.media[0].String()},
			})
			require.NoError(t, err)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				stream, streamErr := fixture.service.StreamPart(ctx, fixture.actor,
					tokenFromURL(plan.Parts[0].DownloadURL), 1)
				if stream.Body != nil {
					_ = stream.Body.Close()
				}
				result <- streamErr
			}()
			select {
			case <-source.archiveStarted:
			case <-ctx.Done():
				t.Fatalf("upstream archive did not open before deadline: %v", ctx.Err())
			}

			accessLoss, err := fixture.db.BeginTx(ctx, &sql.TxOptions{})
			require.NoError(t, err)
			defer func() { _ = accessLoss.Rollback() }()
			require.NoError(t, test.change(ctx, accessLoss, fixture))
			close(release)

			for {
				var waiting bool
				err = fixture.db.NewRaw(`SELECT EXISTS (
					SELECT 1 FROM pg_locks AS lock
					JOIN pg_stat_activity AS activity ON activity.pid = lock.pid
					WHERE NOT lock.granted AND activity.datname = current_database()
				)`).Scan(ctx, &waiting)
				require.NoError(t, err)
				if waiting {
					break
				}
				select {
				case streamErr := <-result:
					t.Fatalf("archive final actor authorization did not wait for %s: %v", test.name, streamErr)
				case <-ctx.Done():
					t.Fatalf("archive did not wait on the actor row lock: %v", ctx.Err())
				case <-time.After(10 * time.Millisecond):
				}
			}

			require.NoError(t, accessLoss.Commit())
			select {
			case streamErr := <-result:
				assert.ErrorIs(t, streamErr, ErrNotFound)
			case <-ctx.Done():
				t.Fatalf("archive did not reauthorize after %s committed: %v", test.name, ctx.Err())
			}
		})
	}
}

func TestPartFinalAuthorizationWaitsForConcurrentPublication(t *testing.T) {
	source := &archiveStub{}
	fixture := newArchiveFixture(t, source)
	source.planned = []immich.ArchivePart{{
		Size: 12, AssetIDs: []uuid.UUID{fixture.assets[0]}, CompanionOf: map[uuid.UUID]uuid.UUID{},
	}}
	plan, err := fixture.service.Plan(context.Background(), fixture.actor, PlanRequest{
		Scope: "subset", MediaIDs: []string{fixture.media[0].String()},
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	publication, err := fixture.db.BeginTx(ctx, &sql.TxOptions{})
	require.NoError(t, err)
	defer func() { _ = publication.Rollback() }()
	require.NoError(t, placementlock.Acquire(ctx, publication, placementlock.Shared))
	_, err = publication.NewRaw(`DELETE FROM current_audience_entitlements
		WHERE recipient_access_generation_id = ? AND media_item_id = ?`,
		fixture.actor.AccessID, fixture.media[0]).Exec(ctx)
	require.NoError(t, err)

	result := make(chan error, 1)
	go func() {
		stream, streamErr := fixture.service.StreamPart(ctx, fixture.actor,
			tokenFromURL(plan.Parts[0].DownloadURL), 1)
		if stream.Body != nil {
			_ = stream.Body.Close()
		}
		result <- streamErr
	}()

	for {
		var waiting bool
		err = fixture.db.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM pg_locks
			WHERE locktype = 'advisory' AND NOT granted
			  AND database = (SELECT oid FROM pg_database WHERE datname = current_database())
		)`).Scan(ctx, &waiting)
		require.NoError(t, err)
		if waiting {
			break
		}
		select {
		case streamErr := <-result:
			t.Fatalf("archive final authorization did not wait for Publication: %v", streamErr)
		case <-ctx.Done():
			t.Fatalf("archive did not wait on the Publication placement lock: %v", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}

	require.NoError(t, publication.Commit())
	select {
	case streamErr := <-result:
		assert.ErrorIs(t, streamErr, ErrNotFound)
	case <-ctx.Done():
		t.Fatalf("archive did not reauthorize after Publication committed: %v", ctx.Err())
	}
}

func TestConcurrentPartDeliveryAllowsExactlyOneStream(t *testing.T) {
	release := make(chan struct{})
	assetID := uuid.New()
	source := &simultaneousArchiveSource{
		part: immich.ArchivePart{
			Size: 12, AssetIDs: []uuid.UUID{assetID}, CompanionOf: map[uuid.UUID]uuid.UUID{},
		},
		payload: singleEntryArchive(t, 12), bothOpened: make(chan struct{}), release: release,
	}
	fixture := newArchiveFixture(t, nil)
	source.part.AssetIDs[0] = fixture.assets[0]
	fixture.service = New(fixture.db, source)
	plan, err := fixture.service.Plan(context.Background(), fixture.actor, PlanRequest{
		Scope: "subset", MediaIDs: []string{fixture.media[0].String()},
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type deliveryResult struct {
		stream Stream
		err    error
	}
	start := make(chan struct{})
	results := make(chan deliveryResult, 2)
	for range 2 {
		go func() {
			<-start
			stream, streamErr := fixture.service.StreamPart(ctx, fixture.actor,
				tokenFromURL(plan.Parts[0].DownloadURL), 1)
			results <- deliveryResult{stream: stream, err: streamErr}
		}()
	}
	close(start)
	select {
	case <-source.bothOpened:
	case <-ctx.Done():
		t.Fatalf("both simultaneous deliveries did not open upstream before deadline: %v", ctx.Err())
	}
	close(release)

	succeeded, rejected := 0, 0
	for range 2 {
		select {
		case result := <-results:
			switch {
			case result.err == nil:
				succeeded++
				require.NoError(t, result.stream.Body.Close())
			case errors.Is(result.err, ErrNotFound):
				rejected++
			default:
				t.Fatalf("unexpected simultaneous delivery result: %v", result.err)
			}
		case <-ctx.Done():
			t.Fatalf("simultaneous deliveries did not finish before deadline: %v", ctx.Err())
		}
	}
	assert.Equal(t, 1, succeeded)
	assert.Equal(t, 1, rejected)
}

func TestPartStreamsCurrentLivePhotoPrimaryAndCompanion(t *testing.T) {
	companion := uuid.New()
	source := &archiveStub{}
	fixture := newArchiveFixture(t, source)
	source.planned = []immich.ArchivePart{{
		Size: 30, AssetIDs: []uuid.UUID{fixture.assets[0], companion},
		CompanionOf: map[uuid.UUID]uuid.UUID{companion: fixture.assets[0]},
	}}
	source.companions = map[uuid.UUID]uuid.UUID{fixture.assets[0]: companion}
	plan, err := fixture.service.Plan(context.Background(), fixture.actor, PlanRequest{
		Scope: "subset", MediaIDs: []string{fixture.media[0].String()},
	})
	require.NoError(t, err)

	stream, err := fixture.service.StreamPart(context.Background(), fixture.actor,
		tokenFromURL(plan.Parts[0].DownloadURL), 1)
	require.NoError(t, err)
	contents, err := io.ReadAll(stream.Body)
	require.NoError(t, err)
	require.NoError(t, stream.Body.Close())
	archive, err := zip.NewReader(bytes.NewReader(contents), int64(len(contents)))
	require.NoError(t, err)
	require.Len(t, archive.File, 2)
	assert.Equal(t, "Family-Weekend/0001-media.jpg", archive.File[0].Name)
	assert.Equal(t, "Family-Weekend/0002-live-photo.jpg", archive.File[1].Name)
	for _, file := range archive.File {
		entry, openErr := file.Open()
		require.NoError(t, openErr)
		entryContents, readErr := io.ReadAll(entry)
		require.NoError(t, readErr)
		require.NoError(t, entry.Close())
		assert.Len(t, entryContents, 15)
	}
	assert.Equal(t, [][]uuid.UUID{{fixture.assets[0]}, {fixture.assets[0]}}, source.infoCalls)
	assert.Equal(t, [][]uuid.UUID{{fixture.assets[0], companion}}, source.archiveCalls)
}

func TestPartRevalidatesCurrentLivePhotoCompanion(t *testing.T) {
	companion, replacement := uuid.New(), uuid.New()
	source := &archiveStub{}
	fixture := newArchiveFixture(t, source)
	source.planned = []immich.ArchivePart{{
		Size: 30, AssetIDs: []uuid.UUID{fixture.assets[0], companion},
		CompanionOf: map[uuid.UUID]uuid.UUID{companion: fixture.assets[0]},
	}}
	source.companions = map[uuid.UUID]uuid.UUID{fixture.assets[0]: replacement}
	plan, err := fixture.service.Plan(context.Background(), fixture.actor, PlanRequest{Scope: "subset", MediaIDs: []string{fixture.media[0].String()}})
	require.NoError(t, err)

	_, err = fixture.service.StreamPart(context.Background(), fixture.actor, tokenFromURL(plan.Parts[0].DownloadURL), 1)
	assert.ErrorIs(t, err, ErrNotFound)
	assert.Empty(t, source.archiveCalls, "a changed companion is blocked before archive streaming")
}

func stringPointer(value string) *string { return &value }

func tokenFromURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	return parsed.Query().Get("token")
}
