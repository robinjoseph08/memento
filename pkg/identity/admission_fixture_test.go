package identity_test

import (
	"context"
	"sync"
	"testing"

	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// library is a three-photo Immich stand-in so Onboarding tests can publish
// an Album and grant per-entry access.
type library struct{}

func (library) CheckImport(context.Context) error { return nil }
func (library) ListAlbums(context.Context) ([]immich.Album, error) {
	return []immich.Album{{ID: "source", Name: "Summer", Count: 3}}, nil
}
func (library) GetAlbum(context.Context, string) (immich.Album, error) {
	return immich.Album{ID: "source", Name: "Summer", Count: 3}, nil
}
func (library) ListMembers(context.Context, string, int) ([]immich.Asset, int, error) {
	return fixtureAssets, 0, nil
}
func (library) ListFaces(context.Context, string) ([]immich.Face, error) { return nil, nil }
func (library) GetAsset(_ context.Context, id string) (immich.Asset, error) {
	for _, asset := range fixtureAssets {
		if asset.ID == id {
			return asset, nil
		}
	}
	panic("unknown fixture asset")
}

var fixtureAssets = []immich.Asset{
	{ID: "a", Checksum: "YQ==", Filename: "a.jpg", Kind: "IMAGE", LocalDateTime: "2026-07-04T10:00:00Z", FileCreatedAt: "2026-07-04T10:00:00Z", UpdatedAt: "2026-07-06T00:00:00Z"},
	{ID: "b", Checksum: "Yg==", Filename: "b.jpg", Kind: "IMAGE", LocalDateTime: "2026-07-04T11:00:00Z", FileCreatedAt: "2026-07-04T11:00:00Z", UpdatedAt: "2026-07-06T00:00:00Z"},
	{ID: "c", Checksum: "Yw==", Filename: "c.jpg", Kind: "IMAGE", LocalDateTime: "2026-07-05T10:00:00Z", FileCreatedAt: "2026-07-05T10:00:00Z", UpdatedAt: "2026-07-06T00:00:00Z"},
}

// mailQueue stands in for the worker: it records delivery IDs so tests run
// deliveries deliberately instead of through River.
type mailQueue struct {
	mu  sync.Mutex
	ids []string
}

func (q *mailQueue) enqueue(_ context.Context, tx bun.Tx, id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if tx.Tx == nil {
		panic("enqueue outside a transaction")
	}
	q.ids = append(q.ids, id)
	return nil
}

func (q *mailQueue) count() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.ids)
}

type admission struct {
	db        *bun.DB
	identity  *identity.Module
	mail      *notifications.Module
	recorder  *notifications.Recorder
	queue     *mailQueue
	albums    *publishing.Module
	configure bool
}

// newAdmission wires Identity, Notifications, and Publishing the way main does.
// Passing false leaves SMTP unconfigured.
func newAdmission(t *testing.T, configured bool) *admission {
	t.Helper()
	db := testdb.New(t)
	albums := publishing.New(db, library{}, func(context.Context, bun.Tx, string) error { return nil })
	recorder := &notifications.Recorder{}
	queue := &mailQueue{}
	var mailer notifications.Mailer
	if configured {
		mailer = recorder
	}
	mail := notifications.New(db, mailer, queue.enqueue, albums, nil)
	module := identity.New(db, nil)
	module.PublicURL = "https://memento.example.test"
	module.Mail = mail
	module.Announcements = mail
	return &admission{db: db, identity: module, mail: mail, recorder: recorder, queue: queue, albums: albums, configure: configured}
}

// deliverAll runs every queued delivery once, as the worker would.
func (a *admission) deliverAll(t *testing.T) {
	t.Helper()
	a.queue.mu.Lock()
	ids := append([]string(nil), a.queue.ids...)
	a.queue.ids = nil
	a.queue.mu.Unlock()
	for _, id := range ids {
		require.NoError(t, a.mail.Execute(t.Context(), id, true))
	}
}

// publishedAlbum imports and publishes the fixture Album, returning its detail.
func (a *admission) publishedAlbum(t *testing.T) publishing.AlbumDetail {
	t.Helper()
	album, err := a.albums.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, a.albums.ExecuteImport(t.Context(), album.ID))
	review, err := a.albums.ReviewPublication(t.Context(), album.ID)
	require.NoError(t, err)
	require.Empty(t, review.Blockers)
	detail, err := a.albums.PublishAlbum(t.Context(), album.ID, publishing.PublishRequest{ReviewToken: review.ReviewToken})
	require.NoError(t, err)
	return detail
}

func entryIDs(detail publishing.AlbumDetail) []string {
	ids := []string{}
	for _, moment := range detail.Moments {
		for _, entry := range moment.Entries {
			ids = append(ids, entry.ID)
		}
	}
	return ids
}
