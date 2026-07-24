//go:build integration

package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errConnectorUnavailable = errors.New("private Immich URL and credential")

type fakeConnector struct {
	checkErr error
	listErr  error
	albums   []immich.AlbumSummary
}

func (connector *fakeConnector) Check(context.Context) error { return connector.checkErr }
func (connector *fakeConnector) OwnedAlbums(context.Context) ([]immich.AlbumSummary, error) {
	return connector.albums, connector.listErr
}

func sourceAlbum(id uuid.UUID, name string, count int) immich.AlbumSummary {
	created := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	updated := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	start := created.Add(time.Hour)
	end := start.Add(24 * time.Hour)
	return immich.AlbumSummary{
		SourceID: id, Name: name, Description: "Normalized description", AssetCount: count,
		CreatedAt: created, UpdatedAt: updated, StartDate: &start, EndDate: &end,
		LastModifiedAssetTimestamp: &updated,
	}
}

func newSourceService(t *testing.T, connector Connector) *Service {
	t.Helper()
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(context.Background(), db))
	return New(db, connector)
}

func TestDiscoveryPersistsOnlyNormalizedSourceInventory(t *testing.T) {
	sourceID := uuid.New()
	connector := &fakeConnector{albums: []immich.AlbumSummary{sourceAlbum(sourceID, "Family trip", 7)}}
	service := newSourceService(t, connector)
	seenAt := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	service.now = func() time.Time { return seenAt }

	response, err := service.Discover(context.Background())
	require.NoError(t, err)
	assert.Equal(t, DiscoveryResponse{Status: "connected", DiscoveredCount: 1}, response)

	listed, err := service.List(context.Background(), "unreviewed", "", 50)
	require.NoError(t, err)
	require.Len(t, listed.Albums, 1)
	album := listed.Albums[0]
	assert.NotEqual(t, sourceID.String(), album.ID, "browser identity must not be the Immich album identifier")
	assert.Equal(t, "Family trip", album.Name)
	assert.Equal(t, 7, album.AssetCount)
	assert.Equal(t, seenAt, album.FirstSeenAt)
	assert.Equal(t, seenAt, album.LastSeenAt)
	assert.False(t, album.SourceMissing)

	var sourceCount, personCount, sessionCount int
	require.NoError(t, service.db.NewRaw(`SELECT count(*) FROM source_albums`).Scan(context.Background(), &sourceCount))
	require.NoError(t, service.db.NewRaw(`SELECT count(*) FROM people`).Scan(context.Background(), &personCount))
	require.NoError(t, service.db.NewRaw(`SELECT count(*) FROM sessions`).Scan(context.Background(), &sessionCount))
	assert.Equal(t, 1, sourceCount)
	assert.Zero(t, personCount)
	assert.Zero(t, sessionCount)
}

func TestIgnoreRestoreAndRediscoveryPreserveDurableIdentityAndLastSeenState(t *testing.T) {
	immichID := uuid.New()
	connector := &fakeConnector{albums: []immich.AlbumSummary{sourceAlbum(immichID, "First name", 2)}}
	service := newSourceService(t, connector)
	firstSeen := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return firstSeen }
	require.NoError(t, discover(service))
	inbox, err := service.List(context.Background(), "unreviewed", "", 50)
	require.NoError(t, err)
	id := uuid.MustParse(inbox.Albums[0].ID)

	ignored, err := service.Ignore(context.Background(), id, inbox.Albums[0].Version)
	require.NoError(t, err)
	assert.Equal(t, "ignored", ignored.Disposition)
	assert.Greater(t, ignored.Version, inbox.Albums[0].Version)
	assert.Equal(t, firstSeen, ignored.LastSeenAt)
	_, err = service.Ignore(context.Background(), id, ignored.Version)
	require.ErrorIs(t, err, ErrInvalidTransition)
	_, err = service.Restore(context.Background(), id, inbox.Albums[0].Version)
	require.ErrorIs(t, err, ErrStaleVersion)

	secondSeen := firstSeen.Add(time.Hour)
	service.now = func() time.Time { return secondSeen }
	connector.albums = []immich.AlbumSummary{sourceAlbum(immichID, "Updated name", 3)}
	require.NoError(t, discover(service))
	stillIgnored, err := service.Get(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, id.String(), stillIgnored.ID)
	assert.Equal(t, "ignored", stillIgnored.Disposition)
	assert.Equal(t, "Updated name", stillIgnored.Name)
	assert.Equal(t, firstSeen, stillIgnored.FirstSeenAt)
	assert.Equal(t, secondSeen, stillIgnored.LastSeenAt)

	restored, err := service.Restore(context.Background(), id, stillIgnored.Version)
	require.NoError(t, err)
	assert.Equal(t, id.String(), restored.ID)
	assert.Equal(t, "unreviewed", restored.Disposition)
	assert.Equal(t, secondSeen, restored.LastSeenAt)
	_, err = service.Restore(context.Background(), id, restored.Version)
	require.ErrorIs(t, err, ErrInvalidTransition)
}

func TestSuccessfulAbsentDiscoveryMarksSourceMissingWithoutErasingIt(t *testing.T) {
	connector := &fakeConnector{albums: []immich.AlbumSummary{sourceAlbum(uuid.New(), "Tracked", 1)}}
	service := newSourceService(t, connector)
	firstSeen := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return firstSeen }
	require.NoError(t, discover(service))
	listed, err := service.List(context.Background(), "unreviewed", "", 50)
	require.NoError(t, err)
	id := uuid.MustParse(listed.Albums[0].ID)

	connector.albums = nil
	service.now = func() time.Time { return firstSeen.Add(time.Hour) }
	require.NoError(t, discover(service))
	missing, err := service.Get(context.Background(), id)
	require.NoError(t, err)
	assert.True(t, missing.SourceMissing)
	assert.Equal(t, firstSeen, missing.LastSeenAt)
}

func TestDependencyFailuresFailClosedBeforePersistence(t *testing.T) {
	tests := []struct {
		name string
		fail func(*fakeConnector)
	}{
		{"version or permission validation", func(connector *fakeConnector) { connector.checkErr = errConnectorUnavailable }},
		{"owned album listing", func(connector *fakeConnector) { connector.listErr = errConnectorUnavailable }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			connector := &fakeConnector{albums: []immich.AlbumSummary{sourceAlbum(uuid.New(), "Existing", 2)}}
			service := newSourceService(t, connector)
			require.NoError(t, discover(service))
			before, err := service.List(context.Background(), "unreviewed", "", 10)
			require.NoError(t, err)
			require.Len(t, before.Albums, 1)

			test.fail(connector)
			connector.albums = nil
			_, err = service.Discover(context.Background())
			require.ErrorIs(t, err, ErrDependency)
			assert.NotContains(t, err.Error(), "private")

			after, err := service.Get(context.Background(), uuid.MustParse(before.Albums[0].ID))
			require.NoError(t, err)
			assert.Equal(t, before.Albums[0], after)
		})
	}
}

func TestAuthenticatedSourceRoutesWireDiscoveryInspectionAndTriage(t *testing.T) {
	connector := &fakeConnector{albums: []immich.AlbumSummary{sourceAlbum(uuid.New(), "Route album", 4)}}
	service := newSourceService(t, connector)
	e := sourceHTTP(service, &fakeAuthorizer{})

	discovered := sourceRequest(e, http.MethodPost, "/api/sources/discover", "session", "csrf")
	require.Equal(t, http.StatusOK, discovered.Code)
	assert.Equal(t, "no-store", discovered.Header().Get("Cache-Control"))

	listed := sourceRequest(e, http.MethodGet, "/api/sources?disposition=unreviewed", "session", "")
	require.Equal(t, http.StatusOK, listed.Code)
	var listPayload struct {
		Albums []map[string]any `json:"albums"`
	}
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &listPayload))
	require.Len(t, listPayload.Albums, 1)
	assert.ElementsMatch(t, []string{
		"id", "name", "description", "asset_count", "source_created_at", "source_updated_at",
		"start_at", "end_at", "disposition", "version", "first_seen_at", "last_seen_at", "source_missing",
	}, mapKeys(listPayload.Albums[0]))
	id := listPayload.Albums[0]["id"].(string)
	version := int64(listPayload.Albums[0]["version"].(float64))

	detail := sourceRequest(e, http.MethodGet, "/api/sources/"+id, "session", "")
	require.Equal(t, http.StatusOK, detail.Code)
	assert.NotContains(t, detail.Body.String(), "immich_album_id")

	ignored := sourceVersionedRequest(e, http.MethodPost, "/api/sources/"+id+"/ignore", "session", "csrf", fmt.Sprint(version))
	require.Equal(t, http.StatusOK, ignored.Code)
	var ignoredAlbum Album
	require.NoError(t, json.Unmarshal(ignored.Body.Bytes(), &ignoredAlbum))
	assert.Equal(t, "ignored", ignoredAlbum.Disposition)
	assert.Greater(t, ignoredAlbum.Version, version)

	restored := sourceVersionedRequest(e, http.MethodPost, "/api/sources/"+id+"/restore", "session", "csrf", fmt.Sprint(ignoredAlbum.Version))
	require.Equal(t, http.StatusOK, restored.Code)
	var restoredAlbum Album
	require.NoError(t, json.Unmarshal(restored.Body.Bytes(), &restoredAlbum))
	assert.Equal(t, "unreviewed", restoredAlbum.Disposition)
}

func mapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

type serializedConnector struct {
	mu            sync.Mutex
	listCalls     int
	firstStarted  chan struct{}
	releaseFirst  chan struct{}
	secondStarted chan struct{}
	snapshots     [][]immich.AlbumSummary
}

func (connector *serializedConnector) Check(context.Context) error { return nil }

func (connector *serializedConnector) OwnedAlbums(context.Context) ([]immich.AlbumSummary, error) {
	connector.mu.Lock()
	call := connector.listCalls
	snapshot := connector.snapshots[call]
	connector.listCalls++
	connector.mu.Unlock()
	if call == 0 {
		close(connector.firstStarted)
		<-connector.releaseFirst
	} else if call == 1 {
		close(connector.secondStarted)
	}
	return snapshot, nil
}

func TestConcurrentDiscoveriesSerializeDependencySnapshots(t *testing.T) {
	connector := &serializedConnector{
		firstStarted:  make(chan struct{}),
		releaseFirst:  make(chan struct{}),
		secondStarted: make(chan struct{}),
		snapshots: [][]immich.AlbumSummary{
			{sourceAlbum(uuid.New(), "Older snapshot", 1)},
			{sourceAlbum(uuid.New(), "Newer snapshot", 1)},
		},
	}
	service := newSourceService(t, connector)
	firstResult := make(chan error, 1)
	secondResult := make(chan error, 1)
	go func() { firstResult <- discover(service) }()
	<-connector.firstStarted
	go func() { secondResult <- discover(service) }()

	select {
	case <-connector.secondStarted:
		t.Fatal("second dependency snapshot began before the first discovery committed")
	case <-time.After(100 * time.Millisecond):
	}
	close(connector.releaseFirst)
	require.NoError(t, <-firstResult)
	require.NoError(t, <-secondResult)

	listed, err := service.List(context.Background(), "unreviewed", "", 10)
	require.NoError(t, err)
	require.Len(t, listed.Albums, 2)
	states := map[string]bool{}
	for _, album := range listed.Albums {
		states[album.Name] = album.SourceMissing
	}
	assert.True(t, states["Older snapshot"])
	assert.False(t, states["Newer snapshot"])
}

func TestSourceInventoryListPaginationAndNotFoundTransitions(t *testing.T) {
	connector := &fakeConnector{albums: []immich.AlbumSummary{
		sourceAlbum(uuid.New(), "One", 1), sourceAlbum(uuid.New(), "Two", 2), sourceAlbum(uuid.New(), "Three", 3),
	}}
	service := newSourceService(t, connector)
	require.NoError(t, discover(service))
	first, err := service.List(context.Background(), "unreviewed", "", 2)
	require.NoError(t, err)
	require.Len(t, first.Albums, 2)
	require.NotNil(t, first.NextCursor)
	assert.NotContains(t, *first.NextCursor, first.Albums[1].ID)
	second, err := service.List(context.Background(), "unreviewed", *first.NextCursor, 2)
	require.NoError(t, err)
	require.Len(t, second.Albums, 1)
	assert.Nil(t, second.NextCursor)
	allIDs := map[string]struct{}{}
	for _, album := range append(first.Albums, second.Albums...) {
		allIDs[album.ID] = struct{}{}
	}
	assert.Len(t, allIDs, 3, "cursor pages must neither duplicate nor omit Source albums")
	_, err = service.List(context.Background(), "private", "", 10)
	require.ErrorIs(t, err, ErrInvalidTransition)
	_, err = service.List(context.Background(), "unreviewed", "not-a-cursor", 10)
	require.ErrorIs(t, err, ErrInvalidCursor)
	_, err = service.Get(context.Background(), uuid.New())
	require.ErrorIs(t, err, ErrNotFound)
	_, err = service.Ignore(context.Background(), uuid.New(), 1)
	require.ErrorIs(t, err, ErrNotFound)
}

func discover(service *Service) error {
	_, err := service.Discover(context.Background())
	return err
}
