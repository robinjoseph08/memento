//go:build integration

package sources

import (
	"context"
	"errors"
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

func newSourceService(t *testing.T, connector *fakeConnector) *Service {
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
		name      string
		connector *fakeConnector
	}{
		{"version or permission validation", &fakeConnector{checkErr: errConnectorUnavailable}},
		{"owned album listing", &fakeConnector{listErr: errConnectorUnavailable}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newSourceService(t, test.connector)
			_, err := service.Discover(context.Background())
			require.ErrorIs(t, err, ErrDependency)
			assert.NotContains(t, err.Error(), "private")
			var count int
			require.NoError(t, service.db.NewRaw(`SELECT count(*) FROM source_albums`).Scan(context.Background(), &count))
			assert.Zero(t, count)
		})
	}
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
	assert.Len(t, second.Albums, 1)
	assert.Nil(t, second.NextCursor)
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
