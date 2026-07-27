//go:build integration

package sources

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type reconciliationConnector struct {
	mu         sync.Mutex
	summary    immich.AlbumSummary
	after      *immich.AlbumSummary
	pages      map[int]immich.AssetPage
	albumCalls int
	pageCalls  []int
	albumErrAt int
	pageErrAt  int
	dependency error
}

func (connector *reconciliationConnector) Check(context.Context) error { return nil }
func (connector *reconciliationConnector) OwnedAlbums(context.Context) ([]immich.AlbumSummary, error) {
	return []immich.AlbumSummary{connector.summary}, nil
}
func (connector *reconciliationConnector) Album(context.Context, uuid.UUID) (immich.AlbumSummary, error) {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	connector.albumCalls++
	if connector.albumErrAt > 0 && connector.albumCalls == connector.albumErrAt {
		return immich.AlbumSummary{}, connector.dependency
	}
	if connector.after != nil && connector.albumCalls%2 == 0 {
		return *connector.after, nil
	}
	return connector.summary, nil
}
func (connector *reconciliationConnector) AlbumAssetsPage(_ context.Context, _ uuid.UUID, page int) (immich.AssetPage, error) {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	connector.pageCalls = append(connector.pageCalls, page)
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
	connector.albumErrAt = 0
	connector.pageErrAt = 0
	connector.pages = map[int]immich.AssetPage{1: {Items: assets}}
}

func reconciliationAsset(id uuid.UUID) immich.AssetSummary {
	width, height := 1200, 800
	return immich.AssetSummary{
		SourceID: id, MediaType: "image", Width: &width, Height: &height,
		LocalDateTime: "2026-01-01T10:00:00Z",
	}
}

func newReconciliationService(t *testing.T, connector *reconciliationConnector) (*Service, uuid.UUID) {
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

func TestReconciliationConsumesMoreThanOneThousandItemsAndDeduplicatesIdentifiers(t *testing.T) {
	albumID := uuid.New()
	connector := &reconciliationConnector{summary: sourceAlbum(albumID, "Large album", 1001), pages: map[int]immich.AssetPage{}}
	assets := make([]immich.AssetSummary, 1001)
	for index := range assets {
		assets[index] = reconciliationAsset(uuid.New())
	}
	next := 2
	connector.pages[1] = immich.AssetPage{Items: assets[:1000], NextPage: &next}
	connector.pages[2] = immich.AssetPage{Items: []immich.AssetSummary{assets[0], assets[1000]}}
	service, sourceAlbumID := newReconciliationService(t, connector)

	require.NoError(t, service.Reconcile(context.Background(), sourceAlbumID))
	assert.Equal(t, []int{1, 2}, connector.pageCalls)
	assertTableCount(t, service, "source_album_memberships", 1001)
	assertTableCount(t, service, "media_items", 1001)

	var stablePasses, additions int
	require.NoError(t, service.db.NewRaw(`
		SELECT stable_passes, addition_count FROM reconciliation_runs
		WHERE source_album_id = ? ORDER BY started_at DESC LIMIT 1
	`, sourceAlbumID).Scan(context.Background(), &stablePasses, &additions))
	assert.Equal(t, 1, stablePasses)
	assert.Equal(t, 1001, additions)
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

func TestConflictingDuplicateAndNonAdvancingPagesAreUnstable(t *testing.T) {
	asset := reconciliationAsset(uuid.New())
	conflict := asset
	conflict.LocalDateTime = "2026-01-02T10:00:00Z"
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
