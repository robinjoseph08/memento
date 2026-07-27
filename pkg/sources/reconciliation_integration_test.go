//go:build integration

package sources

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type reconciliationConnector struct {
	mu           sync.Mutex
	summary      immich.AlbumSummary
	after        *immich.AlbumSummary
	pages        map[int]immich.AssetPage
	albumCalls   int
	pageCalls    []int
	albumIDs     []uuid.UUID
	pageAlbumIDs []uuid.UUID
	albumErrAt   int
	pageErrAt    int
	dependency   error
	checkErr     error
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

func TestReconciliationPersistsLaterSourceMetadata(t *testing.T) {
	asset := reconciliationAsset(uuid.New())
	connector := &reconciliationConnector{summary: sourceAlbum(uuid.New(), "Initial name", 1)}
	connector.pages = map[int]immich.AssetPage{1: {Items: []immich.AssetSummary{asset}}}
	service, sourceAlbumID := newReconciliationService(t, connector)
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))

	connector.summary.Name = "Later Immich name"
	connector.summary.Description = "Later Immich description"
	connector.summary.UpdatedAt = connector.summary.UpdatedAt.Add(time.Minute)
	connector.summary.LastModifiedAssetTimestamp = &connector.summary.UpdatedAt
	connector.albumCalls = 0
	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))

	var name, description string
	require.NoError(t, service.db.NewRaw(`
		SELECT name, description FROM source_albums WHERE id = ?
	`, sourceAlbumID).Scan(context.Background(), &name, &description))
	assert.Equal(t, "Later Immich name", name)
	assert.Equal(t, "Later Immich description", description)
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
