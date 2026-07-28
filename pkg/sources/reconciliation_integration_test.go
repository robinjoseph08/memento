//go:build integration

package sources

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/library"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/staging"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type reconciliationConnector struct {
	mu             sync.Mutex
	summary        immich.AlbumSummary
	after          *immich.AlbumSummary
	pages          map[int]immich.AssetPage
	albumCalls     int
	pageCalls      []int
	albumIDs       []uuid.UUID
	pageAlbumIDs   []uuid.UUID
	albumErrAt     int
	pageErrAt      int
	dependency     error
	checkErr       error
	servedAssets   map[uuid.UUID]bool
	thumbnailCalls []uuid.UUID
}

func (connector *reconciliationConnector) Check(context.Context) error {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	return connector.checkErr
}

func (connector *reconciliationConnector) setCheckError(err error) {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	connector.checkErr = err
}
func (connector *reconciliationConnector) OwnedAlbums(context.Context) ([]immich.AlbumSummary, error) {
	return []immich.AlbumSummary{connector.summary}, nil
}
func (connector *reconciliationConnector) Album(_ context.Context, albumID uuid.UUID) (immich.AlbumSummary, error) {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	connector.albumCalls++
	connector.albumIDs = append(connector.albumIDs, albumID)
	if connector.albumErrAt > 0 && connector.albumCalls == connector.albumErrAt {
		return immich.AlbumSummary{}, connector.dependency
	}
	if connector.after != nil && connector.albumCalls%2 == 0 {
		return *connector.after, nil
	}
	return connector.summary, nil
}
func (connector *reconciliationConnector) AlbumAssetsPage(_ context.Context, albumID uuid.UUID, page int) (immich.AssetPage, error) {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	connector.pageCalls = append(connector.pageCalls, page)
	connector.pageAlbumIDs = append(connector.pageAlbumIDs, albumID)
	if connector.pageErrAt > 0 && len(connector.pageCalls) == connector.pageErrAt {
		return immich.AssetPage{}, connector.dependency
	}
	return connector.pages[page], nil
}
func (connector *reconciliationConnector) AssetExists(_ context.Context, assetID uuid.UUID) (bool, error) {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	if exists, configured := connector.servedAssets[assetID]; configured {
		return exists, nil
	}
	for _, page := range connector.pages {
		for _, asset := range page.Items {
			if asset.SourceID == assetID {
				return true, nil
			}
		}
	}
	return false, nil
}

func (connector *reconciliationConnector) Thumbnail(_ context.Context, assetID uuid.UUID) (immich.MediaResponse, error) {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	connector.thumbnailCalls = append(connector.thumbnailCalls, assetID)
	return immich.MediaResponse{
		Body: io.NopCloser(strings.NewReader("thumbnail")), ContentType: "image/webp", ContentLength: 9,
	}, nil
}

func (connector *reconciliationConnector) requestedThumbnails() []uuid.UUID {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	return append([]uuid.UUID(nil), connector.thumbnailCalls...)
}

func (connector *reconciliationConnector) setAssetExists(assetID uuid.UUID, exists bool) {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	if connector.servedAssets == nil {
		connector.servedAssets = make(map[uuid.UUID]bool)
	}
	connector.servedAssets[assetID] = exists
}

func (connector *reconciliationConnector) setMembership(assets ...immich.AssetSummary) {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	connector.summary.AssetCount = len(assets)
	connector.summary.UpdatedAt = connector.summary.UpdatedAt.Add(time.Second)
	connector.summary.LastModifiedAssetTimestamp = &connector.summary.UpdatedAt
	connector.after = nil
	connector.albumCalls = 0
	connector.pageCalls = nil
	connector.albumIDs = nil
	connector.pageAlbumIDs = nil
	connector.albumErrAt = 0
	connector.pageErrAt = 0
	connector.pages = map[int]immich.AssetPage{1: {Items: assets}}
}

func reconciliationAsset(id uuid.UUID) immich.AssetSummary {
	width, height := 1200, 800
	localDateTime := "2026-01-01T10:00:00Z"
	return immich.AssetSummary{
		SourceID: id, MediaType: "image", Width: &width, Height: &height,
		LocalDateTime: &localDateTime,
	}
}

func repairableReconciliationAsset(id uuid.UUID, path string) immich.AssetSummary {
	asset := reconciliationAsset(id)
	asset.CaptureAt = "2026-01-01T10:00:00Z"
	asset.Checksum = "1111111111111111111111111111111111111111"
	asset.Filename = "family.jpg"
	asset.OriginalPath = path
	return asset
}

func newReconciliationService(t *testing.T, connector Connector) (*Service, uuid.UUID) {
	t.Helper()
	service := newSourceService(t, connector)
	reconciledAt := time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC)
	service.now = func() time.Time { return reconciledAt }
	require.NoError(t, discover(service))
	listed, err := service.List(context.Background(), "unreviewed", "", 10)
	require.NoError(t, err)
	require.Len(t, listed.Albums, 1)
	return service, uuid.MustParse(listed.Albums[0].ID)
}

type stagedSourceEvent struct {
	eventID, momentID, mediaID, publicationID uuid.UUID
	recipient                                 setup.SessionActor
}

func publishSourceEventFixture(t *testing.T, service *Service, sourceAlbumID uuid.UUID, assetID uuid.UUID) stagedSourceEvent {
	t.Helper()
	ctx := context.Background()
	fixture := stagedSourceEvent{eventID: uuid.New(), momentID: uuid.New(), publicationID: uuid.New()}
	curatorID, snapshotID, publishedMomentID := uuid.New(), uuid.New(), uuid.New()
	require.NoError(t, service.db.NewRaw(`SELECT id FROM media_items WHERE immich_asset_id = ?`, assetID).Scan(ctx, &fixture.mediaID))
	_, err := service.db.NewRaw(`
		INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Source Curator', 'source curator');
		INSERT INTO person_roles (person_id, role) VALUES (?, 'curator');
		INSERT INTO events (
			id, lifecycle, title, grouping_timezone, version, final_review_complete, created_at, updated_at
		) VALUES (?, 'published', 'Source Event', 'UTC', 1, true, ?, ?);
		INSERT INTO event_sources (
			event_id, source_album_id, source_order, initialized_name, initialized_description, initialized_at
		) SELECT ?, id, 0, name, description, ? FROM source_albums WHERE id = ?;
		INSERT INTO draft_moments (
			id, event_id, position, proposed_day, grouping_timezone, source_days, title,
			cover_media_item_id, attendance_complete, audience_complete
		) VALUES (?, ?, 0, '2026-01-01', 'UTC', ARRAY['2026-01-01'::date], 'Source Moment', ?, true, true);
		INSERT INTO draft_media_placements (event_id, media_item_id, draft_moment_id, position, created_at)
		VALUES (?, ?, ?, 0, ?);
		INSERT INTO audience_snapshots (id, target_kind, target_id, approved_by_person_id, approved_at, label)
		VALUES (?, 'moment', ?, ?, ?, 'Curator only');
		INSERT INTO current_audience_snapshots (target_kind, target_id, snapshot_id)
		VALUES ('moment', ?, ?);
		INSERT INTO publications (
			id, event_id, revision, editable_version, published_by_person_id, notify_recipients, committed_at
		) VALUES (?, ?, 1, 1, ?, true, ?);
		INSERT INTO published_event_revisions (
			publication_id, event_id, title, description, grouping_timezone, created_at
		) VALUES (?, ?, 'Source Event', '', 'UTC', ?);
		INSERT INTO published_moments (
			id, publication_id, draft_moment_id, audience_snapshot_id, position, title, proposed_day, cover_media_item_id
		) VALUES (?, ?, ?, ?, 0, 'Source Moment', '2026-01-01', ?);
		INSERT INTO published_media_placements (
			published_moment_id, media_item_id, position, media_type, width, height, local_date_time
		) SELECT ?, id, 0, media_type, width, height, local_date_time FROM media_items WHERE id = ?;
		INSERT INTO current_published_events (
			event_id, publication_id, title, description, grouping_timezone, committed_at
		) VALUES (?, ?, 'Source Event', '', 'UTC', ?);
		INSERT INTO current_published_placements (
			event_id, publication_id, published_moment_id, media_item_id, position
		) VALUES (?, ?, ?, ?, 0);
		UPDATE events SET current_publication_id = ? WHERE id = ?
	`, curatorID, curatorID, fixture.eventID, service.now(), service.now(), fixture.eventID, service.now(), sourceAlbumID,
		fixture.momentID, fixture.eventID, fixture.mediaID, fixture.eventID, fixture.mediaID, fixture.momentID, service.now(),
		snapshotID, fixture.momentID, curatorID, service.now(), fixture.momentID, snapshotID,
		fixture.publicationID, fixture.eventID, curatorID, service.now(), fixture.publicationID, fixture.eventID, service.now(),
		publishedMomentID, fixture.publicationID, fixture.momentID, snapshotID, fixture.mediaID,
		publishedMomentID, fixture.mediaID, fixture.eventID, fixture.publicationID, service.now(),
		fixture.eventID, fixture.publicationID, publishedMomentID, fixture.mediaID, fixture.publicationID, fixture.eventID).Exec(ctx)
	require.NoError(t, err)
	return fixture
}

func authorizeSourceRecipient(t *testing.T, service *Service, fixture *stagedSourceEvent) {
	t.Helper()
	recipientPersonID, recipientAccessID, recipientSessionID := uuid.New(), uuid.New(), uuid.New()
	fixture.recipient = setup.SessionActor{
		PersonID: recipientPersonID, AccessID: recipientAccessID, SessionID: recipientSessionID,
	}
	_, err := service.db.NewRaw(`
		UPDATE system_settings SET setup_complete = true WHERE id = 1;
		INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Source Recipient', 'source recipient');
		INSERT INTO person_roles (person_id, role) VALUES (?, 'recipient');
		INSERT INTO recipient_access_generations (
			id, person_id, generation, state, is_current, onboarding_completed_at, created_at, updated_at
		) VALUES (?, ?, 1, 'completed', true, ?, ?, ?);
		INSERT INTO sessions (
			id, credential_hash, person_id, recipient_access_generation_id,
			security_epoch, session_type, idle_expires_at
		) SELECT ?, decode(repeat('42', 32), 'hex'), ?, ?, security_epoch, 'trusted', '2100-01-01T00:00:00Z'
		FROM system_settings WHERE id = 1;
		INSERT INTO audience_snapshot_entries (
			snapshot_id, recipient_person_id, recipient_access_generation_id
		) SELECT snapshot_id, ?, ? FROM current_audience_snapshots
		WHERE target_kind = 'moment' AND target_id = ?;
		INSERT INTO audience_entries (
			published_moment_id, recipient_person_id, recipient_access_generation_id
		) SELECT id, ?, ? FROM published_moments WHERE publication_id = ? AND draft_moment_id = ?;
		INSERT INTO current_audience_entitlements (
			event_id, publication_id, recipient_person_id, recipient_access_generation_id, media_item_id
		) VALUES (?, ?, ?, ?, ?)
	`, recipientPersonID, recipientPersonID,
		recipientAccessID, recipientPersonID, service.now(), service.now(), service.now(),
		recipientSessionID, recipientPersonID, recipientAccessID,
		recipientPersonID, recipientAccessID, fixture.momentID,
		recipientPersonID, recipientAccessID, fixture.publicationID, fixture.momentID,
		fixture.eventID, fixture.publicationID, recipientPersonID, recipientAccessID, fixture.mediaID).Exec(context.Background())
	require.NoError(t, err)
}

func reconciliationWorkerConfig() config.WorkerConfig {
	return config.WorkerConfig{
		PollInterval: 5 * time.Millisecond, HeartbeatInterval: 5 * time.Millisecond,
		HeartbeatMaxAge: time.Second, LeaseDuration: time.Second, DrainTimeout: time.Second,
		RetryBase: 10 * time.Millisecond, RetryMax: 100 * time.Millisecond,
	}
}

func TestRealReconciliationHandlerReschedulesSuccessfulWork(t *testing.T) {
	connector := &reconciliationConnector{summary: sourceAlbum(uuid.New(), "Scheduled album", 0)}
	connector.pages = map[int]immich.AssetPage{1: {}}
	service, sourceAlbumID := newReconciliationService(t, connector)
	service.reconciliationInterval = time.Hour
	jobWorker, err := worker.New(service.db, reconciliationWorkerConfig(), "source-success", map[string]worker.Handler{
		ReconciliationJobKind: service.HandleReconciliationJob,
	})
	require.NoError(t, err)
	jobWorker.Start(context.Background())
	t.Cleanup(func() {
		jobWorker.StopClaims()
		require.NoError(t, jobWorker.Drain(context.Background()))
	})
	require.Eventually(t, func() bool {
		var validated int
		var scheduled bool
		err := service.db.NewRaw(`
			SELECT count(*) FILTER (WHERE run.status = 'validated'),
				bool_and(job.status = 'pending' AND job.attempts = 0 AND job.available_at > now() + interval '50 minutes')
			FROM jobs AS job
			LEFT JOIN reconciliation_runs AS run ON run.source_album_id = ?
			WHERE job.idempotency_key = ?
		`, sourceAlbumID, "source-reconcile:"+sourceAlbumID.String()).Scan(context.Background(), &validated, &scheduled)
		return err == nil && validated > 0 && scheduled
	}, time.Second, 10*time.Millisecond)
}

func TestRealReconciliationHandlerRetriesDependencyFailureAndRecovers(t *testing.T) {
	connector := &reconciliationConnector{summary: sourceAlbum(uuid.New(), "Retrying album", 0)}
	connector.pages = map[int]immich.AssetPage{1: {}}
	service, sourceAlbumID := newReconciliationService(t, connector)
	service.reconciliationInterval = time.Hour
	connector.setCheckError(errors.New("private dependency"))
	jobWorker, err := worker.New(service.db, reconciliationWorkerConfig(), "source-retry", map[string]worker.Handler{
		ReconciliationJobKind: service.HandleReconciliationJob,
	})
	require.NoError(t, err)
	jobWorker.Start(context.Background())
	t.Cleanup(func() {
		jobWorker.StopClaims()
		require.NoError(t, jobWorker.Drain(context.Background()))
	})
	require.Eventually(t, func() bool {
		var attempts int
		var diagnostic string
		err := service.db.NewRaw(`
			SELECT attempts, last_safe_error FROM jobs WHERE idempotency_key = ?
		`, "source-reconcile:"+sourceAlbumID.String()).Scan(context.Background(), &attempts, &diagnostic)
		return err == nil && attempts > 0 && diagnostic == "handler_unavailable"
	}, time.Second, 10*time.Millisecond)

	connector.setCheckError(nil)
	require.Eventually(t, func() bool {
		var validated int
		var attempts int
		var scheduled bool
		err := service.db.NewRaw(`
			SELECT count(*) FILTER (WHERE run.status = 'validated'), max(job.attempts),
				bool_and(job.status = 'pending' AND job.available_at > now() + interval '50 minutes')
			FROM jobs AS job
			LEFT JOIN reconciliation_runs AS run ON run.source_album_id = ?
			WHERE job.idempotency_key = ?
		`, sourceAlbumID, "source-reconcile:"+sourceAlbumID.String()).Scan(context.Background(), &validated, &attempts, &scheduled)
		return err == nil && validated > 0 && attempts == 0 && scheduled
	}, time.Second, 10*time.Millisecond)
}

func TestCuratorRequestDuringActiveScanRequestsImmediateRerun(t *testing.T) {
	connector := &reconciliationConnector{summary: sourceAlbum(uuid.New(), "Active album", 0)}
	connector.pages = map[int]immich.AssetPage{1: {}}
	service, sourceAlbumID := newReconciliationService(t, connector)
	_, err := service.db.NewRaw(`
		UPDATE jobs SET status = 'running', lease_owner = 'active-worker',
			lease_expires_at = now() + interval '1 hour'
		WHERE idempotency_key = ?
	`, "source-reconcile:"+sourceAlbumID.String()).Exec(context.Background())
	require.NoError(t, err)

	response, err := service.QueueReconciliation(context.Background(), sourceAlbumID)
	require.NoError(t, err)
	assert.Equal(t, "queued", response.Status)
	var status string
	var rerunRequested bool
	require.NoError(t, service.db.NewRaw(`
		SELECT status, rerun_requested FROM jobs WHERE idempotency_key = ?
	`, "source-reconcile:"+sourceAlbumID.String()).Scan(context.Background(), &status, &rerunRequested))
	assert.Equal(t, "running", status)
	assert.True(t, rerunRequested)
}

func TestCuratorRequestMakesExistingJobsDueAndRepairsPayload(t *testing.T) {
	for _, existingStatus := range []string{"pending", "completed", "failed"} {
		t.Run(existingStatus, func(t *testing.T) {
			connector := &reconciliationConnector{summary: sourceAlbum(uuid.New(), "Queued album", 0)}
			connector.pages = map[int]immich.AssetPage{1: {}}
			service, sourceAlbumID := newReconciliationService(t, connector)
			_, err := service.db.NewRaw(`
				UPDATE jobs SET status = ?, payload = '{"source_album_id":"not-an-id"}'::jsonb,
					available_at = now() + interval '1 hour', attempts = 3,
					last_safe_error = 'old_failure'
				WHERE idempotency_key = ?
			`, existingStatus, "source-reconcile:"+sourceAlbumID.String()).Exec(context.Background())
			require.NoError(t, err)

			_, err = service.QueueReconciliation(context.Background(), sourceAlbumID)
			require.NoError(t, err)
			var status string
			var payloadSourceID string
			var immediatelyAvailable bool
			require.NoError(t, service.db.NewRaw(`
				SELECT status, payload->>'source_album_id', available_at <= now()
				FROM jobs WHERE idempotency_key = ?
			`, "source-reconcile:"+sourceAlbumID.String()).Scan(context.Background(), &status, &payloadSourceID, &immediatelyAvailable))
			assert.Equal(t, "pending", status)
			assert.Equal(t, sourceAlbumID.String(), payloadSourceID)
			assert.True(t, immediatelyAvailable)
		})
	}
}

func TestReconciliationConsumesMoreThanOneThousandItemsAndDeduplicatesIdentifiers(t *testing.T) {
	albumID := uuid.New()
	connector := &reconciliationConnector{summary: sourceAlbum(albumID, "Large album", 1002), pages: map[int]immich.AssetPage{}}
	assets := make([]immich.AssetSummary, 1002)
	for index := range assets {
		assets[index] = reconciliationAsset(uuid.New())
	}
	second, third := 2, 3
	connector.pages[1] = immich.AssetPage{Items: assets[:1000], NextPage: &second}
	connector.pages[2] = immich.AssetPage{Items: []immich.AssetSummary{assets[0], assets[1000]}, NextPage: &third}
	connector.pages[3] = immich.AssetPage{Items: []immich.AssetSummary{assets[1001]}}
	service, sourceAlbumID := newReconciliationService(t, connector)

	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	assert.Equal(t, []uuid.UUID{albumID, albumID}, connector.albumIDs)
	assert.Equal(t, []uuid.UUID{albumID, albumID, albumID}, connector.pageAlbumIDs)
	assert.Equal(t, []int{1, 2, 3}, connector.pageCalls)
	assertTableCount(t, service, "source_album_memberships", 1002)
	assertTableCount(t, service, "media_items", 1002)

	var stablePasses, additions int
	require.NoError(t, service.db.NewRaw(`
		SELECT stable_passes, addition_count FROM reconciliation_runs
		WHERE source_album_id = ? ORDER BY started_at DESC LIMIT 1
	`, sourceAlbumID).Scan(context.Background(), &stablePasses, &additions))
	assert.Equal(t, 1, stablePasses)
	assert.Equal(t, 1002, additions)
}

func TestReconciliationPersistsZonedUnzonedAndUnknownCaptureDates(t *testing.T) {
	zoned := reconciliationAsset(uuid.New())
	unzoned := reconciliationAsset(uuid.New())
	unzonedValue := "2026-01-01T10:00:00"
	unzoned.LocalDateTime = &unzonedValue
	unknown := reconciliationAsset(uuid.New())
	unknown.LocalDateTime = nil
	connector := &reconciliationConnector{summary: sourceAlbum(uuid.New(), "Capture dates", 3)}
	connector.pages = map[int]immich.AssetPage{1: {Items: []immich.AssetSummary{zoned, unzoned, unknown}}}
	service, sourceAlbumID := newReconciliationService(t, connector)

	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	for _, test := range []struct {
		asset immich.AssetSummary
		want  *string
	}{
		{zoned, zoned.LocalDateTime},
		{unzoned, unzoned.LocalDateTime},
		{unknown, nil},
	} {
		var localDateTime *string
		require.NoError(t, service.db.NewRaw(`
			SELECT local_date_time FROM media_items WHERE immich_asset_id = ?
		`, test.asset.SourceID).Scan(context.Background(), &localDateTime))
		assert.Equal(t, test.want, localDateTime)
	}
}

func TestReconciliationCoalescesExistingMediaMetadataChangesIntoPublishedEvent(t *testing.T) {
	asset := reconciliationAsset(uuid.New())
	connector := &reconciliationConnector{summary: sourceAlbum(uuid.New(), "Initial name", 1)}
	connector.pages = map[int]immich.AssetPage{1: {Items: []immich.AssetSummary{asset}}}
	service, sourceAlbumID := newReconciliationService(t, connector)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	fixture := publishSourceEventFixture(t, service, sourceAlbumID, asset.SourceID)

	changed := asset
	changedWidth := 1600
	changed.Width = &changedWidth
	changedLocalDateTime := "2026-01-02T11:00:00Z"
	changed.LocalDateTime = &changedLocalDateTime
	connector.summary.Name = "Later Immich name"
	connector.summary.Description = "Later Immich description"
	connector.setMembership(changed)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))

	var name, description string
	require.NoError(t, service.db.NewRaw(`
		SELECT name, description FROM source_albums WHERE id = ?
	`, sourceAlbumID).Scan(context.Background(), &name, &description))
	assert.Equal(t, "Later Immich name", name)
	assert.Equal(t, "Later Immich description", description)
	var version int64
	var finalReview bool
	require.NoError(t, service.db.NewRaw(`
		SELECT version, final_review_complete FROM events WHERE id = ?
	`, fixture.eventID).Scan(context.Background(), &version, &finalReview))
	assert.EqualValues(t, 2, version)
	assert.False(t, finalReview)
	update, err := staging.Load(context.Background(), service.db, fixture.eventID)
	require.NoError(t, err)
	require.NotNil(t, update)
	require.Len(t, update.Changes, 1)
	assert.Equal(t, staging.ChangeKindMetadata, update.Changes[0].Kind)
	assert.Equal(t, []string{fixture.mediaID.String()}, update.Changes[0].MediaItemIDs)
}

func TestReconciliationIgnoresFailureAndInstabilityUntilTwoIdenticalValidatedRemovalPasses(t *testing.T) {
	kept, removed := reconciliationAsset(uuid.New()), reconciliationAsset(uuid.New())
	connector := &reconciliationConnector{summary: sourceAlbum(uuid.New(), "Stable album", 2), dependency: errors.New("private dependency")}
	connector.pages = map[int]immich.AssetPage{1: {Items: []immich.AssetSummary{kept, removed}}}
	service, sourceAlbumID := newReconciliationService(t, connector)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))

	connector.setMembership(kept)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	assertTableCount(t, service, "source_album_memberships", 2)
	assertRemovalEvidence(t, service, sourceAlbumID, 1)

	connector.setCheckError(connector.dependency)
	require.ErrorIs(t, service.Reconcile(context.Background(), sourceAlbumID), ErrDependency)
	assertRemovalEvidence(t, service, sourceAlbumID, 1)
	assertTableCount(t, service, "source_album_memberships", 2)

	connector.setCheckError(nil)
	connector.albumCalls = 0
	connector.albumErrAt = 1
	require.ErrorIs(t, service.Reconcile(context.Background(), sourceAlbumID), ErrDependency)
	assertRemovalEvidence(t, service, sourceAlbumID, 1)
	assertTableCount(t, service, "source_album_memberships", 2)

	connector.albumErrAt = 0
	changed := connector.summary
	changed.UpdatedAt = changed.UpdatedAt.Add(time.Minute)
	connector.after = &changed
	connector.albumCalls = 0
	require.ErrorIs(t, service.Reconcile(context.Background(), sourceAlbumID), ErrUnstable)
	assertRemovalEvidence(t, service, sourceAlbumID, 1)
	assertTableCount(t, service, "source_album_memberships", 2)

	connector.after = nil
	connector.albumCalls = 0
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	assertRemovalEvidence(t, service, sourceAlbumID, 2)
	assertTableCount(t, service, "source_album_memberships", 1)

	var status, diagnostic string
	require.NoError(t, service.db.NewRaw(`
		SELECT status, diagnostic FROM reconciliation_runs
		WHERE source_album_id = ? AND diagnostic = 'summary_changed' LIMIT 1
	`, sourceAlbumID).Scan(context.Background(), &status, &diagnostic))
	assert.Equal(t, "unstable", status)
	assert.Equal(t, "summary_changed", diagnostic)
}

func TestDifferingValidatedPassResetsRemovalEvidence(t *testing.T) {
	first, second := reconciliationAsset(uuid.New()), reconciliationAsset(uuid.New())
	connector := &reconciliationConnector{summary: sourceAlbum(uuid.New(), "Changing album", 2)}
	connector.pages = map[int]immich.AssetPage{1: {Items: []immich.AssetSummary{first, second}}}
	service, sourceAlbumID := newReconciliationService(t, connector)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))

	connector.setMembership(first)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	assertRemovalEvidence(t, service, sourceAlbumID, 1)

	connector.setMembership(first, second)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	assertRemovalEvidence(t, service, sourceAlbumID, 1)

	connector.setMembership(first)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	assertRemovalEvidence(t, service, sourceAlbumID, 1)
	assertTableCount(t, service, "source_album_memberships", 2)
}

func TestPublishedSourceChangesCoalesceAcrossRetriesConcurrentEditsAndCancellation(t *testing.T) {
	original, added := reconciliationAsset(uuid.New()), reconciliationAsset(uuid.New())
	connector := &reconciliationConnector{summary: sourceAlbum(uuid.New(), "Published source", 1)}
	connector.pages = map[int]immich.AssetPage{1: {Items: []immich.AssetSummary{original}}}
	service, sourceAlbumID := newReconciliationService(t, connector)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	fixture := publishSourceEventFixture(t, service, sourceAlbumID, original.SourceID)

	connector.setMembership(original, added)
	start := make(chan struct{})
	errorsByEdit := make(chan error, 2)
	go func() {
		<-start
		errorsByEdit <- service.Reconcile(context.Background(), sourceAlbumID)
	}()
	go func() {
		<-start
		errorsByEdit <- service.db.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
			if _, err := tx.NewRaw(`UPDATE events SET title = 'Concurrent correction', version = version + 1 WHERE id = ?`, fixture.eventID).Exec(ctx); err != nil {
				return err
			}
			_, err := staging.Refresh(ctx, tx, fixture.eventID, service.now().UTC())
			return err
		})
	}()
	close(start)
	require.NoError(t, <-errorsByEdit)
	require.NoError(t, <-errorsByEdit)

	update, err := staging.Load(context.Background(), service.db, fixture.eventID)
	require.NoError(t, err)
	require.NotNil(t, update)
	assert.Len(t, update.Changes, 2)
	firstStagedID := update.ID
	var stagedRows int
	require.NoError(t, service.db.NewRaw(`SELECT count(*) FROM staged_updates WHERE event_id = ?`, fixture.eventID).Scan(context.Background(), &stagedRows))
	assert.Equal(t, 1, stagedRows)

	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	retried, err := staging.Load(context.Background(), service.db, fixture.eventID)
	require.NoError(t, err)
	require.NotNil(t, retried)
	assert.Equal(t, firstStagedID, retried.ID, "reconciliation retry retains one mutable Staged update")

	require.NoError(t, service.db.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`UPDATE events SET title = 'Source Event', version = version + 1 WHERE id = ?`, fixture.eventID).Exec(ctx); err != nil {
			return err
		}
		_, err := staging.Refresh(ctx, tx, fixture.eventID, service.now().UTC())
		return err
	}))
	connector.setMembership(original)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	cancelled, err := staging.Load(context.Background(), service.db, fixture.eventID)
	require.NoError(t, err)
	assert.Nil(t, cancelled, "an addition removed before Publication leaves no Staged update")
	var addedPlacement bool
	require.NoError(t, service.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM draft_media_placements WHERE event_id = ? AND media_item_id = (SELECT id FROM media_items WHERE immich_asset_id = ?))`, fixture.eventID, added.SourceID).Scan(context.Background(), &addedPlacement))
	assert.False(t, addedPlacement)
}

func TestFinalPublishedSourceRemovalStaysPrivateWhileMediaRemainsAvailable(t *testing.T) {
	original := reconciliationAsset(uuid.New())
	connector := &reconciliationConnector{summary: sourceAlbum(uuid.New(), "Removal source", 1)}
	connector.pages = map[int]immich.AssetPage{1: {Items: []immich.AssetSummary{original}}}
	service, sourceAlbumID := newReconciliationService(t, connector)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	fixture := publishSourceEventFixture(t, service, sourceAlbumID, original.SourceID)
	authorizeSourceRecipient(t, service, &fixture)
	connector.setAssetExists(original.SourceID, true)

	connector.setMembership()
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	var memberships int
	var availability string
	var editablePlacement, currentPlacement, historicalPlacement bool
	require.NoError(t, service.db.NewRaw(`SELECT count(*) FROM source_album_memberships WHERE media_item_id = ?`, fixture.mediaID).Scan(context.Background(), &memberships))
	require.NoError(t, service.db.NewRaw(`SELECT availability FROM media_items WHERE id = ?`, fixture.mediaID).Scan(context.Background(), &availability))
	require.NoError(t, service.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM draft_media_placements WHERE event_id = ? AND media_item_id = ?)`, fixture.eventID, fixture.mediaID).Scan(context.Background(), &editablePlacement))
	require.NoError(t, service.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM current_published_placements WHERE event_id = ? AND media_item_id = ?)`, fixture.eventID, fixture.mediaID).Scan(context.Background(), &currentPlacement))
	require.NoError(t, service.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM published_media_placements WHERE media_item_id = ?)`, fixture.mediaID).Scan(context.Background(), &historicalPlacement))
	assert.Zero(t, memberships, "the test exercises removal from the final Source album membership")
	assert.Equal(t, "current", availability)
	assert.False(t, editablePlacement, "the source removal changes only the private editable result")
	assert.True(t, currentPlacement, "Recipients retain the prior projection until Publication")
	assert.True(t, historicalPlacement)

	recipientLibrary := library.New(service.db, connector)
	listed, err := recipientLibrary.Events(context.Background(), fixture.recipient, "10", "")
	require.NoError(t, err)
	require.Len(t, listed.Events, 1)
	assert.Equal(t, fixture.eventID.String(), listed.Events[0].ID)
	assert.Equal(t, fixture.mediaID.String(), listed.Events[0].CoverMediaID)
	assert.True(t, listed.Events[0].CoverAvailable)
	assert.Equal(t, "/api/me/media/"+fixture.mediaID.String()+"/thumbnail", listed.Events[0].ThumbnailURL)
	thumbnail, err := recipientLibrary.Thumbnail(context.Background(), fixture.recipient, fixture.mediaID)
	require.NoError(t, err)
	contents, err := io.ReadAll(thumbnail.Body)
	require.NoError(t, err)
	require.NoError(t, thumbnail.Body.Close())
	assert.Equal(t, "thumbnail", string(contents))
	assert.Equal(t, []uuid.UUID{original.SourceID}, connector.requestedThumbnails())

	require.NoError(t, service.db.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`DELETE FROM draft_moments WHERE id = ?`, fixture.momentID).Exec(ctx); err != nil {
			return err
		}
		_, err := staging.Refresh(ctx, tx, fixture.eventID, service.now().UTC())
		return err
	}))
	update, err := staging.Load(context.Background(), service.db, fixture.eventID)
	require.NoError(t, err)
	require.NotNil(t, update)
	var removalFound bool
	for _, change := range update.Changes {
		if change.Kind == "removal" {
			removalFound = true
			assert.Equal(t, []string{fixture.mediaID.String()}, change.MediaItemIDs)
			require.Len(t, change.RemovedMedia, 1)
			assert.Equal(t, fixture.mediaID.String(), change.RemovedMedia[0].ID)
			assert.Equal(t, "image", change.RemovedMedia[0].MediaType)
		}
	}
	assert.True(t, removalFound)
	var deletedMomentFound bool
	for _, change := range update.Changes {
		if change.Kind == "moment_structure" {
			deletedMomentFound = true
			require.Len(t, change.DeletedMoments, 1)
			assert.Equal(t, fixture.momentID.String(), change.DeletedMoments[0].ID)
			assert.Equal(t, "Source Moment", change.DeletedMoments[0].Title)
			assert.Equal(t, "2026-01-01", change.DeletedMoments[0].ProposedDay)
		}
	}
	assert.True(t, deletedMomentFound)
}

func TestPublishedSourceRemovalThenReappearanceRestoresPlacementAndClearsStaging(t *testing.T) {
	original := reconciliationAsset(uuid.New())
	connector := &reconciliationConnector{summary: sourceAlbum(uuid.New(), "Restored source", 1)}
	connector.pages = map[int]immich.AssetPage{1: {Items: []immich.AssetSummary{original}}}
	service, sourceAlbumID := newReconciliationService(t, connector)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	fixture := publishSourceEventFixture(t, service, sourceAlbumID, original.SourceID)
	connector.setAssetExists(original.SourceID, true)
	const originalPosition = 4
	_, err := service.db.NewRaw(`
		UPDATE draft_media_placements SET position = ? WHERE event_id = ? AND media_item_id = ?;
		UPDATE published_media_placements SET position = ?
		WHERE published_moment_id IN (SELECT id FROM published_moments WHERE publication_id = ?)
		  AND media_item_id = ?;
		UPDATE current_published_placements SET position = ? WHERE event_id = ? AND media_item_id = ?
	`, originalPosition, fixture.eventID, fixture.mediaID,
		originalPosition, fixture.publicationID, fixture.mediaID,
		originalPosition, fixture.eventID, fixture.mediaID).Exec(context.Background())
	require.NoError(t, err)

	connector.setMembership()
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	var storedMomentID *uuid.UUID
	var storedPosition int
	var storedCover bool
	require.NoError(t, service.db.NewRaw(`
		SELECT draft_moment_id, position, was_cover FROM staged_source_removals
		WHERE event_id = ? AND media_item_id = ?
	`, fixture.eventID, fixture.mediaID).Scan(context.Background(), &storedMomentID, &storedPosition, &storedCover))
	require.NotNil(t, storedMomentID)
	assert.Equal(t, fixture.momentID, *storedMomentID)
	assert.Equal(t, originalPosition, storedPosition)
	assert.True(t, storedCover)
	removed, err := staging.Load(context.Background(), service.db, fixture.eventID)
	require.NoError(t, err)
	require.NotNil(t, removed)
	require.Len(t, removed.Changes, 2)

	connector.setMembership(original)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	var restoredMomentID *uuid.UUID
	var restoredPosition int
	require.NoError(t, service.db.NewRaw(`
		SELECT draft_moment_id, position FROM draft_media_placements
		WHERE event_id = ? AND media_item_id = ?
	`, fixture.eventID, fixture.mediaID).Scan(context.Background(), &restoredMomentID, &restoredPosition))
	require.NotNil(t, restoredMomentID)
	assert.Equal(t, fixture.momentID, *restoredMomentID)
	assert.Equal(t, originalPosition, restoredPosition)
	var restoredCoverID *uuid.UUID
	require.NoError(t, service.db.NewRaw(`SELECT cover_media_item_id FROM draft_moments WHERE id = ?`, fixture.momentID).Scan(context.Background(), &restoredCoverID))
	require.NotNil(t, restoredCoverID)
	assert.Equal(t, fixture.mediaID, *restoredCoverID)
	var restorationRows, stagedRows int
	require.NoError(t, service.db.NewRaw(`SELECT count(*) FROM staged_source_removals WHERE event_id = ?`, fixture.eventID).Scan(context.Background(), &restorationRows))
	require.NoError(t, service.db.NewRaw(`SELECT count(*) FROM staged_updates WHERE event_id = ?`, fixture.eventID).Scan(context.Background(), &stagedRows))
	assert.Zero(t, restorationRows)
	assert.Zero(t, stagedRows)
	var stagedPointer *uuid.UUID
	require.NoError(t, service.db.NewRaw(`SELECT current_staged_update_id FROM events WHERE id = ?`, fixture.eventID).Scan(context.Background(), &stagedPointer))
	assert.Nil(t, stagedPointer)
	cancelled, err := staging.Load(context.Background(), service.db, fixture.eventID)
	require.NoError(t, err)
	assert.Nil(t, cancelled, "restoring the exact published result leaves no empty Staged work")
}

func TestConfirmedDeletedPublishedMediaBecomesUnavailable(t *testing.T) {
	original := repairableReconciliationAsset(uuid.New(), "/library/deleted/family.jpg")
	connector := &reconciliationConnector{summary: sourceAlbum(uuid.New(), "Deleted source", 1)}
	connector.pages = map[int]immich.AssetPage{1: {Items: []immich.AssetSummary{original}}}
	service, sourceAlbumID := newReconciliationService(t, connector)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	fixture := publishSourceEventFixture(t, service, sourceAlbumID, original.SourceID)

	connector.setMembership()
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))

	var availability string
	require.NoError(t, service.db.NewRaw(`SELECT availability FROM media_items WHERE id = ?`, fixture.mediaID).Scan(context.Background(), &availability))
	assert.Equal(t, "source_missing", availability, "a confirmed missing Immich asset stops published delivery immediately")
	var currentPlacement bool
	require.NoError(t, service.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM current_published_placements WHERE event_id = ? AND media_item_id = ?)`, fixture.eventID, fixture.mediaID).Scan(context.Background(), &currentPlacement))
	assert.True(t, currentPlacement, "the unavailable published listing remains until correction")
}

func TestNewSourceMediaFollowsOnlyEventsConfiguredForFutureMedia(t *testing.T) {
	original, added := reconciliationAsset(uuid.New()), reconciliationAsset(uuid.New())
	connector := &reconciliationConnector{summary: sourceAlbum(uuid.New(), "Divided source", 1)}
	connector.pages = map[int]immich.AssetPage{1: {Items: []immich.AssetSummary{original}}}
	service, sourceAlbumID := newReconciliationService(t, connector)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	following := publishSourceEventFixture(t, service, sourceAlbumID, original.SourceID)

	partialEventID := uuid.New()
	_, err := service.db.NewRaw(`
		INSERT INTO events (id, title, grouping_timezone, created_at, updated_at)
		VALUES (?, 'Partial Event', 'UTC', ?, ?);
		INSERT INTO event_sources (
			event_id, source_album_id, source_order, initialized_name,
			initialized_description, initialized_at, include_future_media
		) SELECT ?, id, 0, name, description, ?, false FROM source_albums WHERE id = ?;
		INSERT INTO draft_media_placements (event_id, media_item_id, position, created_at)
		VALUES (?, ?, 0, ?)
	`, partialEventID, service.now(), service.now(), partialEventID, service.now(), sourceAlbumID,
		partialEventID, following.mediaID, service.now()).Exec(context.Background())
	require.NoError(t, err)

	connector.setMembership(original, added)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	var addedMediaID uuid.UUID
	require.NoError(t, service.db.NewRaw(`SELECT id FROM media_items WHERE immich_asset_id = ?`, added.SourceID).Scan(context.Background(), &addedMediaID))
	var followingHasAddition, partialHasAddition bool
	require.NoError(t, service.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM draft_media_placements WHERE event_id = ? AND media_item_id = ?)`, following.eventID, addedMediaID).Scan(context.Background(), &followingHasAddition))
	require.NoError(t, service.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM draft_media_placements WHERE event_id = ? AND media_item_id = ?)`, partialEventID, addedMediaID).Scan(context.Background(), &partialHasAddition))
	assert.True(t, followingHasAddition)
	assert.False(t, partialHasAddition, "an explicitly divided Source selection does not receive unrelated future Media")
}

func TestAddThenRemoveBeforePublicationLeavesNoEditableResidue(t *testing.T) {
	connector := &reconciliationConnector{summary: sourceAlbum(uuid.New(), "Draft album", 0)}
	connector.pages = map[int]immich.AssetPage{1: {}}
	service, sourceAlbumID := newReconciliationService(t, connector)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))

	added := reconciliationAsset(uuid.New())
	connector.setMembership(added)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	assertTableCount(t, service, "source_album_memberships", 1)
	assertTableCount(t, service, "media_items", 1)

	connector.setMembership()
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	assertTableCount(t, service, "source_album_memberships", 1)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	assertTableCount(t, service, "source_album_memberships", 0)
	assertTableCount(t, service, "media_items", 0)

	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	var additions, removals int
	require.NoError(t, service.db.NewRaw(`
		SELECT addition_count, removal_count FROM reconciliation_runs
		WHERE source_album_id = ? ORDER BY completed_at DESC, id DESC LIMIT 1
	`, sourceAlbumID).Scan(context.Background(), &additions, &removals))
	assert.Zero(t, additions)
	assert.Zero(t, removals)
}

func TestReconciliationRetainsMediaReferencedByEventAndLooseDrafts(t *testing.T) {
	for _, kind := range []string{"event", "loose_item"} {
		t.Run(kind, func(t *testing.T) {
			asset := reconciliationAsset(uuid.New())
			connector := &reconciliationConnector{summary: sourceAlbum(uuid.New(), "Retained draft", 1)}
			connector.pages = map[int]immich.AssetPage{1: {Items: []immich.AssetSummary{asset}}}
			service, sourceAlbumID := newReconciliationService(t, connector)
			require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
			var mediaID uuid.UUID
			require.NoError(t, service.db.NewRaw(`
				SELECT id FROM media_items WHERE immich_asset_id = ?
			`, asset.SourceID).Scan(context.Background(), &mediaID))

			draftID := uuid.New()
			if kind == "event" {
				_, err := service.db.NewRaw(`
					INSERT INTO events (id, title, grouping_timezone) VALUES (?, 'Retained Event', 'UTC');
					INSERT INTO draft_media_placements (event_id, media_item_id, position)
					VALUES (?, ?, 0)
				`, draftID, draftID, mediaID).Exec(context.Background())
				require.NoError(t, err)
			} else {
				_, err := service.db.NewRaw(`
					INSERT INTO loose_items (id, media_item_id, grouping_timezone) VALUES (?, ?, 'UTC')
				`, draftID, mediaID).Exec(context.Background())
				require.NoError(t, err)
			}

			connector.setMembership()
			require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
			require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
			assertTableCount(t, service, "source_album_memberships", 0)
			assertTableCount(t, service, "media_items", 1)
			var draftCount int
			table := "loose_items"
			if kind == "event" {
				table = "events"
			}
			require.NoError(t, service.db.NewRaw(`SELECT count(*) FROM `+table+` WHERE id = ?`, draftID).Scan(context.Background(), &draftCount))
			assert.Equal(t, 1, draftCount)
		})
	}
}

func TestDeleteReimportAndPathMoveStayAddRemoveUntilRepairConfirmation(t *testing.T) {
	oldAsset := repairableReconciliationAsset(uuid.New(), "/library/old/family.jpg")
	connector := &reconciliationConnector{summary: sourceAlbum(uuid.New(), "Repair album", 1)}
	connector.pages = map[int]immich.AssetPage{1: {Items: []immich.AssetSummary{oldAsset}}}
	service, sourceAlbumID := newReconciliationService(t, connector)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	var stableMediaID uuid.UUID
	require.NoError(t, service.db.NewRaw(`SELECT id FROM media_items WHERE immich_asset_id = ?`, oldAsset.SourceID).Scan(context.Background(), &stableMediaID))

	moved := repairableReconciliationAsset(uuid.New(), "/library/moved/family.jpg")
	connector.setMembership(moved)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	assertTableCount(t, service, "source_album_memberships", 2)
	assertTableCount(t, service, "media_items", 2)
	var candidates int
	require.NoError(t, service.db.NewRaw(`SELECT count(*) FROM media_repair_candidates`).Scan(context.Background(), &candidates))
	assert.Zero(t, candidates, "one pass remains an addition plus a possible removal")

	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	assertTableCount(t, service, "source_album_memberships", 1)
	assertTableCount(t, service, "media_items", 2)
	type candidateRow struct {
		MediaItemID, PreviousID, CandidateID uuid.UUID
		State                                string
	}
	var candidate candidateRow
	require.NoError(t, service.db.NewRaw(`
		SELECT media_item_id, previous_immich_asset_id AS previous_id,
			candidate_immich_asset_id AS candidate_id, state
		FROM media_repair_candidates
	`).Scan(context.Background(), &candidate))
	assert.Equal(t, stableMediaID, candidate.MediaItemID)
	assert.Equal(t, oldAsset.SourceID, candidate.PreviousID)
	assert.Equal(t, moved.SourceID, candidate.CandidateID)
	assert.Equal(t, "pending", candidate.State)
	var oldPath, newPath, checksum string
	require.NoError(t, service.db.NewRaw(`
		SELECT old.original_path, replacement.original_path, old.checksum
		FROM media_repair_candidates AS repair
		JOIN media_backings AS old ON old.immich_asset_id = repair.previous_immich_asset_id
		JOIN media_backings AS replacement ON replacement.immich_asset_id = repair.candidate_immich_asset_id
	`).Scan(context.Background(), &oldPath, &newPath, &checksum))
	assert.Equal(t, "/library/old/family.jpg", oldPath)
	assert.Equal(t, "/library/moved/family.jpg", newPath)
	assert.Equal(t, oldAsset.Checksum, checksum)
}

func TestRejectedMediaRepairIsNotProposedAgain(t *testing.T) {
	oldAsset := repairableReconciliationAsset(uuid.New(), "/library/old/family.jpg")
	connector := &reconciliationConnector{summary: sourceAlbum(uuid.New(), "Rejected repair album", 1)}
	connector.pages = map[int]immich.AssetPage{1: {Items: []immich.AssetSummary{oldAsset}}}
	service, sourceAlbumID := newReconciliationService(t, connector)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))

	replacement := repairableReconciliationAsset(uuid.New(), "/library/new/family.jpg")
	connector.setMembership(replacement)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	result, err := service.db.NewRaw(`UPDATE media_repair_candidates SET state = 'rejected', resolved_at = now() WHERE state = 'pending'`).Exec(context.Background())
	require.NoError(t, err)
	rejected, err := result.RowsAffected()
	require.NoError(t, err)
	require.EqualValues(t, 1, rejected)

	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	var rejectedCount, pendingCount int
	require.NoError(t, service.db.NewRaw(`
		SELECT count(*) FILTER (WHERE state = 'rejected'), count(*) FILTER (WHERE state = 'pending')
		FROM media_repair_candidates
	`).Scan(context.Background(), &rejectedCount, &pendingCount))
	assert.Equal(t, 1, rejectedCount)
	assert.Zero(t, pendingCount)
}

func TestPendingMediaRepairIsSupersededWhenPreviousAssetReturns(t *testing.T) {
	oldAsset := repairableReconciliationAsset(uuid.New(), "/library/old/family.jpg")
	connector := &reconciliationConnector{summary: sourceAlbum(uuid.New(), "Reversed repair album", 1)}
	connector.pages = map[int]immich.AssetPage{1: {Items: []immich.AssetSummary{oldAsset}}}
	service, sourceAlbumID := newReconciliationService(t, connector)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))

	replacement := repairableReconciliationAsset(uuid.New(), "/library/new/family.jpg")
	connector.setMembership(replacement)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))

	connector.setMembership(oldAsset)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	var state string
	var candidateMediaItemID uuid.NullUUID
	require.NoError(t, service.db.NewRaw(`
		SELECT state, candidate_media_item_id FROM media_repair_candidates
	`).Scan(context.Background(), &state, &candidateMediaItemID))
	assert.Equal(t, "superseded", state)
	assert.False(t, candidateMediaItemID.Valid)
}

func TestMediaRepairRequiresExactChecksumDespiteMatchingMetadata(t *testing.T) {
	oldAsset := repairableReconciliationAsset(uuid.New(), "/library/old/family.jpg")
	connector := &reconciliationConnector{summary: sourceAlbum(uuid.New(), "Exact checksum album", 1)}
	connector.pages = map[int]immich.AssetPage{1: {Items: []immich.AssetSummary{oldAsset}}}
	service, sourceAlbumID := newReconciliationService(t, connector)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))

	replacement := repairableReconciliationAsset(uuid.New(), "/library/new/family.jpg")
	replacement.Checksum = "2222222222222222222222222222222222222222"
	connector.setMembership(replacement)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	var candidates int
	require.NoError(t, service.db.NewRaw(`SELECT count(*) FROM media_repair_candidates`).Scan(context.Background(), &candidates))
	assert.Zero(t, candidates)
}

func TestAmbiguousChecksumCandidatesExposeConflictEvidence(t *testing.T) {
	first := repairableReconciliationAsset(uuid.New(), "/library/a/family.jpg")
	second := repairableReconciliationAsset(uuid.New(), "/library/b/family.jpg")
	connector := &reconciliationConnector{summary: sourceAlbum(uuid.New(), "Ambiguous album", 2)}
	connector.pages = map[int]immich.AssetPage{1: {Items: []immich.AssetSummary{first, second}}}
	service, sourceAlbumID := newReconciliationService(t, connector)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))

	replacement := repairableReconciliationAsset(uuid.New(), "/library/new/family.jpg")
	connector.setMembership(replacement)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	var pending, conflicted int
	require.NoError(t, service.db.NewRaw(`
		SELECT count(*), count(*) FILTER (WHERE conflict_evidence ? 'checksum_matches_multiple_media')
		FROM media_repair_candidates WHERE state = 'pending'
	`).Scan(context.Background(), &pending, &conflicted))
	assert.Equal(t, 2, pending)
	assert.Equal(t, 2, conflicted)
}

func TestDuplicateAssetsWithChangedRepairEvidenceAreUnstable(t *testing.T) {
	for _, field := range []string{"checksum", "path"} {
		t.Run(field, func(t *testing.T) {
			asset := repairableReconciliationAsset(uuid.New(), "/library/original.jpg")
			conflict := asset
			if field == "checksum" {
				conflict.Checksum = "2222222222222222222222222222222222222222"
			} else {
				conflict.OriginalPath = "/library/changed.jpg"
			}
			next := 2
			connector := &reconciliationConnector{summary: sourceAlbum(uuid.New(), "Conflicting evidence", 1)}
			connector.pages = map[int]immich.AssetPage{
				1: {Items: []immich.AssetSummary{asset}, NextPage: &next},
				2: {Items: []immich.AssetSummary{conflict}},
			}
			service, sourceAlbumID := newReconciliationService(t, connector)

			require.ErrorIs(t, service.Reconcile(context.Background(), sourceAlbumID), ErrUnstable)
			assertTableCount(t, service, "source_album_memberships", 0)
		})
	}
}

func TestConflictingDuplicateAndNonAdvancingPagesAreUnstable(t *testing.T) {
	asset := reconciliationAsset(uuid.New())
	conflict := asset
	conflictingDateTime := "2026-01-02T10:00:00Z"
	conflict.LocalDateTime = &conflictingDateTime
	connector := &reconciliationConnector{summary: sourceAlbum(uuid.New(), "Invalid pages", 1)}
	connector.pages = map[int]immich.AssetPage{1: {Items: []immich.AssetSummary{asset, conflict}}}
	service, sourceAlbumID := newReconciliationService(t, connector)

	require.ErrorIs(t, service.Reconcile(context.Background(), sourceAlbumID), ErrUnstable)
	assertRemovalEvidence(t, service, sourceAlbumID, 0)
	assertTableCount(t, service, "source_album_memberships", 0)

	connector.summary.AssetCount = 1001
	connector.summary.UpdatedAt = connector.summary.UpdatedAt.Add(time.Second)
	nonAdvancing := 1
	connector.pages = map[int]immich.AssetPage{1: {Items: []immich.AssetSummary{asset}, NextPage: &nonAdvancing}}
	connector.albumCalls = 0
	require.ErrorIs(t, service.Reconcile(context.Background(), sourceAlbumID), ErrUnstable)
	assertRemovalEvidence(t, service, sourceAlbumID, 0)
}

type blockingReconciliationConnector struct {
	summary      immich.AlbumSummary
	albumCalls   atomic.Int32
	firstStarted chan struct{}
	releaseFirst chan struct{}
}

func (connector *blockingReconciliationConnector) Check(context.Context) error { return nil }
func (connector *blockingReconciliationConnector) OwnedAlbums(context.Context) ([]immich.AlbumSummary, error) {
	return []immich.AlbumSummary{connector.summary}, nil
}
func (connector *blockingReconciliationConnector) Album(context.Context, uuid.UUID) (immich.AlbumSummary, error) {
	if connector.albumCalls.Add(1) == 1 {
		close(connector.firstStarted)
		<-connector.releaseFirst
	}
	return connector.summary, nil
}
func (connector *blockingReconciliationConnector) AlbumAssetsPage(context.Context, uuid.UUID, int) (immich.AssetPage, error) {
	return immich.AssetPage{}, nil
}
func (connector *blockingReconciliationConnector) AssetExists(context.Context, uuid.UUID) (bool, error) {
	return true, nil
}

func TestReconciliationSerializesDependencyScansPerSourceAlbum(t *testing.T) {
	connector := &blockingReconciliationConnector{
		summary:      sourceAlbum(uuid.New(), "Serialized reconciliation", 0),
		firstStarted: make(chan struct{}), releaseFirst: make(chan struct{}),
	}
	service, sourceAlbumID := newReconciliationService(t, connector)
	results := make(chan error, 2)
	go func() { results <- service.Reconcile(context.Background(), sourceAlbumID) }()
	select {
	case <-connector.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first reconciliation did not start its dependency scan")
	}
	go func() { results <- service.Reconcile(context.Background(), sourceAlbumID) }()
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(1), connector.albumCalls.Load(), "second scan must wait for the Source album transaction lock")
	close(connector.releaseFirst)
	for range 2 {
		select {
		case err := <-results:
			require.NoError(t, err)
		case <-time.After(time.Second):
			t.Fatal("serialized reconciliation did not complete")
		}
	}
}

func TestAssetCountChangeAloneMakesSnapshotUnstable(t *testing.T) {
	asset := reconciliationAsset(uuid.New())
	before := sourceAlbum(uuid.New(), "Changing count", 2)
	after := before
	after.AssetCount = 1
	connector := &reconciliationConnector{
		summary: before, after: &after,
		pages: map[int]immich.AssetPage{1: {Items: []immich.AssetSummary{asset}}},
	}
	service, sourceAlbumID := newReconciliationService(t, connector)

	require.ErrorIs(t, service.Reconcile(context.Background(), sourceAlbumID), ErrUnstable)
	assertRemovalEvidence(t, service, sourceAlbumID, 0)
	assertTableCount(t, service, "source_album_memberships", 0)
	var diagnostic string
	require.NoError(t, service.db.NewRaw(`
		SELECT diagnostic FROM reconciliation_runs
		WHERE source_album_id = ? ORDER BY completed_at DESC, id DESC LIMIT 1
	`, sourceAlbumID).Scan(context.Background(), &diagnostic))
	assert.Equal(t, "summary_changed", diagnostic)
}

func TestIncompletePaginationDoesNotCreateValidatedRemovalEvidence(t *testing.T) {
	asset := reconciliationAsset(uuid.New())
	connector := &reconciliationConnector{summary: sourceAlbum(uuid.New(), "Incomplete album", 1)}
	connector.pages = map[int]immich.AssetPage{1: {Items: []immich.AssetSummary{asset}}}
	service, sourceAlbumID := newReconciliationService(t, connector)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))

	connector.pageCalls = nil
	connector.pageErrAt = 1
	connector.dependency = errors.New("private page dependency")
	require.ErrorIs(t, service.Reconcile(context.Background(), sourceAlbumID), ErrDependency)
	assertRemovalEvidence(t, service, sourceAlbumID, 1)
	assertTableCount(t, service, "source_album_memberships", 1)

	connector.pageErrAt = 0
	connector.summary.AssetCount = 2
	connector.summary.UpdatedAt = connector.summary.UpdatedAt.Add(time.Second)
	connector.pages = map[int]immich.AssetPage{1: {Items: []immich.AssetSummary{asset}}}
	connector.albumCalls = 0
	require.ErrorIs(t, service.Reconcile(context.Background(), sourceAlbumID), ErrUnstable)
	assertRemovalEvidence(t, service, sourceAlbumID, 1)
	assertTableCount(t, service, "source_album_memberships", 1)

	var diagnostic string
	require.NoError(t, service.db.NewRaw(`SELECT diagnostic FROM reconciliation_runs ORDER BY completed_at DESC, id DESC LIMIT 1`).Scan(context.Background(), &diagnostic))
	assert.Equal(t, "pagination_incomplete", diagnostic)
}

func assertRemovalEvidence(t *testing.T, service *Service, sourceAlbumID uuid.UUID, expected int) {
	t.Helper()
	var passes int
	require.NoError(t, service.db.NewRaw(`SELECT candidate_membership_passes FROM source_albums WHERE id = ?`, sourceAlbumID).Scan(context.Background(), &passes))
	assert.Equal(t, expected, passes)
}

func assertTableCount(t *testing.T, service *Service, table string, expected int) {
	t.Helper()
	var count int
	require.NoError(t, service.db.NewRaw(`SELECT count(*) FROM `+table).Scan(context.Background(), &count))
	assert.Equal(t, expected, count)
}
