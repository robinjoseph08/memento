//go:build integration

package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/internal/testdb"
	commentsservice "github.com/robinjoseph08/memento/pkg/comments"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	favoritesservice "github.com/robinjoseph08/memento/pkg/favorites"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/mediaaccess"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/repairs"
	searchservice "github.com/robinjoseph08/memento/pkg/search"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type thumbnailStub struct {
	assets          []uuid.UUID
	representations []string
	requests        []immich.MediaRequest
	response        immich.MediaResponse
	err             error
}

func (stub *thumbnailStub) Thumbnail(_ context.Context, assetID uuid.UUID, request immich.MediaRequest) (immich.MediaResponse, error) {
	return stub.media(assetID, "thumbnail", request)
}

func (stub *thumbnailStub) Preview(_ context.Context, assetID uuid.UUID, request immich.MediaRequest) (immich.MediaResponse, error) {
	return stub.media(assetID, "preview", request)
}

func (stub *thumbnailStub) Video(_ context.Context, assetID uuid.UUID, request immich.MediaRequest) (immich.MediaResponse, error) {
	return stub.media(assetID, "video", request)
}

func (stub *thumbnailStub) Original(_ context.Context, assetID uuid.UUID, request immich.MediaRequest) (immich.MediaResponse, error) {
	return stub.media(assetID, "original", request)
}

func (stub *thumbnailStub) media(assetID uuid.UUID, representation string, request immich.MediaRequest) (immich.MediaResponse, error) {
	stub.assets = append(stub.assets, assetID)
	stub.representations = append(stub.representations, representation)
	stub.requests = append(stub.requests, request)
	if stub.err != nil {
		return immich.MediaResponse{}, stub.err
	}
	if stub.response.Body != nil {
		return stub.response, nil
	}
	return immich.MediaResponse{
		Body: io.NopCloser(bytes.NewBufferString(representation)), StatusCode: http.StatusOK,
		ContentType: "image/webp", ContentLength: int64(len(representation)),
	}, nil
}

type observedRepresentationBody struct {
	io.Reader
	closed chan struct{}
	once   sync.Once
}

func (body *observedRepresentationBody) Close() error {
	body.once.Do(func() { close(body.closed) })
	return nil
}

type blockingRepresentationSource struct {
	started chan struct{}
	release chan struct{}
	bodies  chan *observedRepresentationBody
}

func (source *blockingRepresentationSource) Thumbnail(ctx context.Context, _ uuid.UUID, _ immich.MediaRequest) (immich.MediaResponse, error) {
	return source.open(ctx, "thumbnail")
}

func (source *blockingRepresentationSource) Preview(ctx context.Context, _ uuid.UUID, _ immich.MediaRequest) (immich.MediaResponse, error) {
	return source.open(ctx, "preview")
}

func (source *blockingRepresentationSource) Video(ctx context.Context, _ uuid.UUID, _ immich.MediaRequest) (immich.MediaResponse, error) {
	return source.open(ctx, "video")
}

func (source *blockingRepresentationSource) Original(ctx context.Context, _ uuid.UUID, _ immich.MediaRequest) (immich.MediaResponse, error) {
	return source.open(ctx, "original")
}

func (source *blockingRepresentationSource) open(ctx context.Context, contents string) (immich.MediaResponse, error) {
	select {
	case source.started <- struct{}{}:
	case <-ctx.Done():
		return immich.MediaResponse{}, ctx.Err()
	}
	select {
	case <-source.release:
	case <-ctx.Done():
		return immich.MediaResponse{}, ctx.Err()
	}
	body := &observedRepresentationBody{Reader: strings.NewReader(contents), closed: make(chan struct{})}
	source.bodies <- body
	return immich.MediaResponse{Body: body, StatusCode: http.StatusOK, ContentType: "image/webp"}, nil
}

type lateMissingRepresentationSource struct {
	started chan struct{}
	release <-chan struct{}
}

func (source *lateMissingRepresentationSource) missing(ctx context.Context) (immich.MediaResponse, error) {
	select {
	case source.started <- struct{}{}:
	case <-ctx.Done():
		return immich.MediaResponse{}, ctx.Err()
	}
	select {
	case <-source.release:
		return immich.MediaResponse{}, immich.ErrNotFound
	case <-ctx.Done():
		return immich.MediaResponse{}, ctx.Err()
	}
}

func (source *lateMissingRepresentationSource) Thumbnail(ctx context.Context, _ uuid.UUID, _ immich.MediaRequest) (immich.MediaResponse, error) {
	return source.missing(ctx)
}
func (source *lateMissingRepresentationSource) Preview(ctx context.Context, _ uuid.UUID, _ immich.MediaRequest) (immich.MediaResponse, error) {
	return source.missing(ctx)
}
func (source *lateMissingRepresentationSource) Video(ctx context.Context, _ uuid.UUID, _ immich.MediaRequest) (immich.MediaResponse, error) {
	return source.missing(ctx)
}
func (source *lateMissingRepresentationSource) Original(ctx context.Context, _ uuid.UUID, _ immich.MediaRequest) (immich.MediaResponse, error) {
	return source.missing(ctx)
}

type relinkEvidenceSource struct{ asset immich.AssetSummary }

func (*relinkEvidenceSource) Check(context.Context) error { return nil }
func (*relinkEvidenceSource) People(context.Context) ([]immich.PersonSummary, error) {
	return nil, nil
}
func (*relinkEvidenceSource) Faces(context.Context, uuid.UUID) ([]immich.FaceSummary, error) {
	return nil, nil
}
func (source *relinkEvidenceSource) Asset(context.Context, uuid.UUID) (immich.AssetSummary, error) {
	return source.asset, nil
}
func (*relinkEvidenceSource) AssetDeliveryAvailable(context.Context, uuid.UUID, string) (bool, error) {
	return true, nil
}

type libraryFixture struct {
	db             *bun.DB
	service        *Service
	thumbnail      *thumbnailStub
	actor          setup.SessionActor
	curator        uuid.UUID
	events         []uuid.UUID
	publications   []uuid.UUID
	moments        []uuid.UUID
	draftMoments   []uuid.UUID
	media          []uuid.UUID
	assets         []uuid.UUID
	hiddenAccessID uuid.UUID
}

func newLibraryFixture(t *testing.T) libraryFixture {
	t.Helper()
	ctx := context.Background()
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(ctx, db))
	fixture := libraryFixture{
		db: db, curator: uuid.New(), events: []uuid.UUID{uuid.New(), uuid.New(), uuid.New()},
		publications: []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}, moments: []uuid.UUID{uuid.New(), uuid.New(), uuid.New()},
		draftMoments: []uuid.UUID{uuid.New(), uuid.New(), uuid.New()},
		media:        []uuid.UUID{uuid.New(), uuid.New(), uuid.New()},
		assets:       []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}, thumbnail: &thumbnailStub{},
	}
	personID, accessID, sessionID := uuid.New(), uuid.New(), uuid.New()
	fixture.actor = setup.SessionActor{PersonID: personID, AccessID: accessID, SessionID: sessionID}
	hiddenPersonID := uuid.New()
	fixture.hiddenAccessID = uuid.New()
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
		FROM system_settings WHERE id = 1
	`, fixture.curator, personID, hiddenPersonID,
		fixture.curator, personID, hiddenPersonID,
		accessID, personID, now, fixture.hiddenAccessID, hiddenPersonID, now,
		sessionID, personID, accessID, now).Exec(ctx)
	require.NoError(t, err)

	for index := range fixture.events {
		committed := now.Add(time.Duration(index) * time.Hour)
		snapshotID := uuid.New()
		_, err = db.NewRaw(`
			INSERT INTO events (id, lifecycle, title, description, grouping_timezone, version, created_at, updated_at)
			VALUES (?, 'published', ?, '', 'UTC', 1, ?, ?);
			INSERT INTO publications
				(id, event_id, revision, editable_version, published_by_person_id, notify_recipients, committed_at)
			VALUES (?, ?, 1, 1, ?, true, ?);
			UPDATE events SET current_publication_id = ? WHERE id = ?;
			INSERT INTO current_published_events
				(event_id, publication_id, title, description, grouping_timezone, committed_at)
			VALUES (?, ?, ?, '', 'UTC', ?);
			INSERT INTO audience_snapshots
				(id, target_kind, target_id, approved_by_person_id, approved_at, label)
			VALUES (?, 'moment', ?, ?, ?, 'Shared');
			INSERT INTO published_moments
				(id, publication_id, draft_moment_id, audience_snapshot_id, position, title, proposed_day)
			VALUES (?, ?, ?, ?, 0, '', '2026-07-27')
		`, fixture.events[index], "Event "+string(rune('A'+index)), committed, committed,
			fixture.publications[index], fixture.events[index], fixture.curator, committed,
			fixture.publications[index], fixture.events[index],
			fixture.events[index], fixture.publications[index], "Event "+string(rune('A'+index)), committed,
			snapshotID, fixture.draftMoments[index], fixture.curator, committed,
			fixture.moments[index], fixture.publications[index], fixture.draftMoments[index], snapshotID).Exec(ctx)
		require.NoError(t, err)
	}
	for index := range fixture.media {
		_, err = db.NewRaw(`
			INSERT INTO media_items
				(id, immich_asset_id, media_type, width, height, local_date_time, availability, first_seen_at, last_seen_at)
			VALUES (?, ?, 'image', ?, ?, ?, 'current', ?, ?);
			INSERT INTO media_backings (id, media_item_id, immich_asset_id, linked_at)
			VALUES (gen_random_uuid(), ?, ?, ?)
		`, fixture.media[index], fixture.assets[index], 1600-index*200, 900+index*100,
			now.Add(time.Duration(index)*time.Hour).Format(time.RFC3339), now, now,
			fixture.media[index], fixture.assets[index], now).Exec(ctx)
		require.NoError(t, err)
	}
	fixture.place(t, 0, fixture.media[0], accessID, 0)
	fixture.place(t, 0, fixture.media[1], accessID, 1)
	fixture.place(t, 0, fixture.media[2], fixture.hiddenAccessID, 2)
	fixture.place(t, 1, fixture.media[0], accessID, 0)
	fixture.place(t, 2, fixture.media[2], fixture.hiddenAccessID, 0)
	_, err = db.NewRaw(`
		INSERT INTO new_for_you_entries (recipient_access_generation_id, publication_id)
		VALUES (?, ?), (?, ?), (?, ?), (?, ?)
	`, accessID, fixture.publications[0], accessID, fixture.publications[1],
		fixture.hiddenAccessID, fixture.publications[0], fixture.hiddenAccessID, fixture.publications[2]).Exec(ctx)
	require.NoError(t, err)
	fixture.service = New(db, fixture.thumbnail)
	return fixture
}

func (fixture libraryFixture) place(t *testing.T, eventIndex int, mediaID, accessID uuid.UUID, position int) {
	t.Helper()
	_, err := fixture.db.NewRaw(`
		INSERT INTO published_media_placements
			(published_moment_id, media_item_id, position, media_type, width, height, local_date_time)
		SELECT ?, id, ?, media_type, width, height, local_date_time FROM media_items WHERE id = ?;
		INSERT INTO current_published_placements
			(event_id, publication_id, published_moment_id, media_item_id, position)
		VALUES (?, ?, ?, ?, ?);
		INSERT INTO audience_entries (
			published_moment_id, recipient_person_id, recipient_access_generation_id
		) SELECT ?, person_id, ? FROM recipient_access_generations WHERE id = ?
		ON CONFLICT DO NOTHING;
		INSERT INTO current_audience_entitlements
			(event_id, publication_id, recipient_person_id, recipient_access_generation_id, media_item_id)
		SELECT ?, ?, person_id, ?, ? FROM recipient_access_generations WHERE id = ?
	`, fixture.moments[eventIndex], position, mediaID,
		fixture.events[eventIndex], fixture.publications[eventIndex], fixture.moments[eventIndex], mediaID, position,
		fixture.moments[eventIndex], accessID, accessID,
		fixture.events[eventIndex], fixture.publications[eventIndex], accessID, mediaID, accessID).Exec(context.Background())
	require.NoError(t, err)
}

func TestRecipientLibraryPaginatesOnlyCurrentAuthorizedUnion(t *testing.T) {
	fixture := newLibraryFixture(t)
	ctx := context.Background()
	_, err := fixture.db.NewRaw(`
		UPDATE published_moments SET cover_media_item_id = ? WHERE id = ?;
		UPDATE media_items SET media_type = 'video' WHERE id = ?;
		UPDATE published_media_placements SET media_type = 'video' WHERE media_item_id = ?
	`, fixture.media[1], fixture.moments[0], fixture.media[1], fixture.media[1]).Exec(ctx)
	require.NoError(t, err)
	first, err := fixture.service.Photos(ctx, fixture.actor, "1", "", false)
	require.NoError(t, err)
	require.Len(t, first.Media, 1)
	assert.Equal(t, fixture.media[1].String(), first.Media[0].ID)
	assert.Equal(t, "/api/me/media/"+fixture.media[1].String()+"/preview", first.Media[0].PreviewURL)
	assert.Equal(t, "/api/me/media/"+fixture.media[1].String()+"/original", first.Media[0].OriginalURL)
	assert.Equal(t, "/api/me/media/"+fixture.media[1].String()+"/video", first.Media[0].VideoURL)
	require.NotNil(t, first.NextCursor)
	second, err := fixture.service.Photos(ctx, fixture.actor, "1", *first.NextCursor, false)
	require.NoError(t, err)
	require.Len(t, second.Media, 1)
	assert.Equal(t, fixture.media[0].String(), second.Media[0].ID)
	assert.Nil(t, second.NextCursor)
	assert.NotContains(t, []string{first.Media[0].ID, second.Media[0].ID}, fixture.media[2].String())

	events, err := fixture.service.Events(ctx, fixture.actor, "10", "")
	require.NoError(t, err)
	require.Len(t, events.Events, 2)
	assert.Equal(t, 1, events.Events[0].MediaCount)
	assert.Equal(t, 2, events.Events[1].MediaCount)
	assert.Equal(t, fixture.media[1].String(), events.Events[1].CoverMediaID)
	require.NotNil(t, events.Events[1].CoverWidth)
	require.NotNil(t, events.Events[1].CoverHeight)
	assert.Equal(t, 1400, *events.Events[1].CoverWidth)
	assert.Equal(t, 1000, *events.Events[1].CoverHeight)
	detail, err := fixture.service.Event(ctx, fixture.actor, fixture.events[0], "10", "")
	require.NoError(t, err)
	assert.Equal(t, 2, detail.MediaCount)
	assert.Equal(t, fixture.media[1].String(), detail.CoverMediaID)
	assert.Len(t, detail.Media, 2)

	newForYou, err := fixture.service.NewForYou(ctx, fixture.actor)
	require.NoError(t, err)
	require.Len(t, newForYou.Events, 2)
	require.NotNil(t, newForYou.Events[0].CoverWidth)
	require.NotNil(t, newForYou.Events[0].CoverHeight)
	assert.Equal(t, 1600, *newForYou.Events[0].CoverWidth)
	assert.Equal(t, 900, *newForYou.Events[0].CoverHeight)
	_, err = fixture.service.Photos(ctx, fixture.actor, "10", "guessed", false)
	assert.ErrorIs(t, err, ErrInvalidCursor)
}

func TestLibraryCursorsAreScopedAndEventPaginationDoesNotExposeHiddenPositions(t *testing.T) {
	fixture := newLibraryFixture(t)
	ctx := context.Background()

	photos, err := fixture.service.Photos(ctx, fixture.actor, "1", "", false)
	require.NoError(t, err)
	require.NotNil(t, photos.NextCursor)
	_, err = fixture.service.Photos(ctx, fixture.actor, "1", *photos.NextCursor, true)
	assert.ErrorIs(t, err, ErrInvalidCursor, "Photos cursors cannot paginate Favorites")

	events, err := fixture.service.Events(ctx, fixture.actor, "1", "")
	require.NoError(t, err)
	require.NotNil(t, events.NextCursor)
	staleEventCursor := encodeCursor(cursor{
		Kind: cursorKindEvents, Sort: events.Events[0].CommittedAt.Format(time.RFC3339Nano),
		ID: events.Events[0].ID, PublicationID: uuid.NewString(),
	})
	_, err = fixture.service.Events(ctx, fixture.actor, "1", *staleEventCursor)
	assert.ErrorIs(t, err, ErrInvalidCursor, "Events cursors are bound to the current Publication")

	_, err = fixture.db.NewRaw(`
		UPDATE current_published_placements SET position = position + 10 WHERE event_id = ?;
		UPDATE current_published_placements SET position = 0 WHERE event_id = ? AND media_item_id = ?;
		UPDATE current_published_placements SET position = 1 WHERE event_id = ? AND media_item_id = ?;
		UPDATE current_published_placements SET position = 2 WHERE event_id = ? AND media_item_id = ?
	`, fixture.events[0], fixture.events[0], fixture.media[0], fixture.events[0], fixture.media[2], fixture.events[0], fixture.media[1]).Exec(ctx)
	require.NoError(t, err)
	first, err := fixture.service.Event(ctx, fixture.actor, fixture.events[0], "1", "")
	require.NoError(t, err)
	require.Len(t, first.Media, 1)
	assert.Equal(t, fixture.media[0].String(), first.Media[0].ID)
	require.NotNil(t, first.NextCursor)
	position, err := decodeCursor(*first.NextCursor, cursorKindEventMedia)
	require.NoError(t, err)
	assert.Empty(t, position.Sort, "the hidden source ordinal between authorized Media is not disclosed")
	second, err := fixture.service.Event(ctx, fixture.actor, fixture.events[0], "1", *first.NextCursor)
	require.NoError(t, err)
	require.Len(t, second.Media, 1)
	assert.Equal(t, fixture.media[1].String(), second.Media[0].ID)
	assert.Nil(t, second.NextCursor)

	_, err = fixture.service.Event(ctx, fixture.actor, fixture.events[1], "1", *first.NextCursor)
	assert.ErrorIs(t, err, ErrInvalidCursor, "Event cursors cannot cross resource boundaries")
	staleDetailCursor := encodeCursor(cursor{
		Kind: cursorKindEventMedia, ID: first.Media[0].ID,
		ResourceID: fixture.events[0].String(), PublicationID: uuid.NewString(),
	})
	_, err = fixture.service.Event(ctx, fixture.actor, fixture.events[0], "1", *staleDetailCursor)
	assert.ErrorIs(t, err, ErrInvalidCursor, "Event cursors are bound to the current Publication")
}

func TestEventsAndEventDetailHaveStableDuplicateFreePageBoundaries(t *testing.T) {
	fixture := newLibraryFixture(t)
	ctx := context.Background()
	equalCommittedAt := time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)
	_, err := fixture.db.NewRaw(`
		UPDATE current_published_events SET committed_at = ? WHERE event_id IN (?, ?)
	`, equalCommittedAt, fixture.events[0], fixture.events[1]).Exec(ctx)
	require.NoError(t, err)

	firstEvents, err := fixture.service.Events(ctx, fixture.actor, "1", "")
	require.NoError(t, err)
	require.Len(t, firstEvents.Events, 1)
	require.NotNil(t, firstEvents.NextCursor)
	secondEvents, err := fixture.service.Events(ctx, fixture.actor, "1", *firstEvents.NextCursor)
	require.NoError(t, err)
	require.Len(t, secondEvents.Events, 1)
	assert.Nil(t, secondEvents.NextCursor)
	eventIDs := []string{firstEvents.Events[0].ID, secondEvents.Events[0].ID}
	expectedEventIDs := []string{fixture.events[0].String(), fixture.events[1].String()}
	slices.Sort(expectedEventIDs)
	slices.Reverse(expectedEventIDs)
	assert.Equal(t, expectedEventIDs, eventIDs, "equal commit times use Event ID as a stable descending tie breaker")
	assert.NotEqual(t, eventIDs[0], eventIDs[1], "Events do not repeat across page boundaries")

	firstMedia, err := fixture.service.Event(ctx, fixture.actor, fixture.events[0], "1", "")
	require.NoError(t, err)
	require.Len(t, firstMedia.Media, 1)
	require.NotNil(t, firstMedia.NextCursor)
	secondMedia, err := fixture.service.Event(ctx, fixture.actor, fixture.events[0], "1", *firstMedia.NextCursor)
	require.NoError(t, err)
	require.Len(t, secondMedia.Media, 1)
	assert.Nil(t, secondMedia.NextCursor)
	mediaIDs := []string{firstMedia.Media[0].ID, secondMedia.Media[0].ID}
	expectedMediaIDs := []string{fixture.media[0].String(), fixture.media[1].String()}
	assert.Equal(t, expectedMediaIDs, mediaIDs, "Event Media retain their current placement order across pages")
	assert.NotEqual(t, mediaIDs[0], mediaIDs[1], "Event Media do not repeat across page boundaries")
}

func TestEventDetailRetainsCuratorOrderAcrossMomentsAndHiddenPageBoundaries(t *testing.T) {
	fixture := newLibraryFixture(t)
	ctx := context.Background()
	secondMoment, secondDraft, secondSnapshot := uuid.New(), uuid.New(), uuid.New()
	_, err := fixture.db.NewRaw(`
		INSERT INTO audience_snapshots
			(id, target_kind, target_id, approved_by_person_id, approved_at, label)
		VALUES (?, 'moment', ?, ?, now(), 'Shared');
		INSERT INTO published_moments
			(id, publication_id, draft_moment_id, audience_snapshot_id, position, title, proposed_day)
		VALUES (?, ?, ?, ?, 1, 'Second Moment', '2026-07-28');
		INSERT INTO published_media_placements
			(published_moment_id, media_item_id, position, media_type, width, height, local_date_time)
		SELECT ?, id, 0, media_type, width, height, local_date_time FROM media_items WHERE id = ?;
		UPDATE current_published_placements SET position = position + 10 WHERE event_id = ?;
		UPDATE current_published_placements SET position = 0 WHERE event_id = ? AND media_item_id = ?;
		UPDATE current_published_placements SET position = 1 WHERE event_id = ? AND media_item_id = ?;
		UPDATE current_published_placements
		SET published_moment_id = ?, position = 2 WHERE event_id = ? AND media_item_id = ?
	`, secondSnapshot, secondDraft, fixture.curator,
		secondMoment, fixture.publications[0], secondDraft, secondSnapshot,
		secondMoment, fixture.media[1], fixture.events[0],
		fixture.events[0], fixture.media[0], fixture.events[0], fixture.media[2],
		secondMoment, fixture.events[0], fixture.media[1]).Exec(ctx)
	require.NoError(t, err)

	var actual []string
	cursor := ""
	for {
		page, pageErr := fixture.service.Event(ctx, fixture.actor, fixture.events[0], "1", cursor)
		require.NoError(t, pageErr)
		require.Len(t, page.Media, 1)
		actual = append(actual, page.Media[0].ID)
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}

	assert.Equal(t, []string{fixture.media[0].String(), fixture.media[1].String()}, actual,
		"authorized Media retain exact Curator order across Moments while the hidden placement stays omitted")
}

func TestFavoritesAndNewForYouRemainRecipientScopedAndDurable(t *testing.T) {
	fixture := newLibraryFixture(t)
	ctx := context.Background()
	_, err := fixture.db.NewRaw(`INSERT INTO favorites (recipient_person_id, media_item_id) VALUES (?, ?), ((SELECT person_id FROM recipient_access_generations WHERE id = ?), ?)`,
		fixture.actor.PersonID, fixture.media[0], fixture.hiddenAccessID, fixture.media[2]).Exec(ctx)
	require.NoError(t, err)
	favorites, err := fixture.service.Photos(ctx, fixture.actor, "10", "", true)
	require.NoError(t, err)
	require.Len(t, favorites.Media, 1)
	assert.Equal(t, fixture.media[0].String(), favorites.Media[0].ID)

	err = fixture.service.MarkSeen(ctx, fixture.actor, fixture.publications[0])
	require.NoError(t, err)
	newForYou, err := fixture.service.NewForYou(ctx, fixture.actor)
	require.NoError(t, err)
	require.Len(t, newForYou.Events, 1)
	assert.Equal(t, fixture.publications[1].String(), newForYou.Events[0].PublicationID)
	err = fixture.service.MarkSeen(ctx, fixture.actor, fixture.publications[0])
	assert.ErrorIs(t, err, ErrNotFound, "an already-seen Publication cannot be enumerated through mutation")
}

type libraryErrorSignature struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	StatusCode int    `json:"status_code"`
}

func requestLibraryError(t *testing.T, fixture libraryFixture, method, path string) (int, string, libraryErrorSignature) {
	t.Helper()
	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	RegisterRoutes(e, NewHandler(fixture.service, &routeAuthorizer{actor: fixture.actor}))
	request := httptest.NewRequestWithContext(context.Background(), method, path, nil)
	request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "opaque"})
	request.Header.Set(setup.CSRFHeader, "valid")
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	var envelope struct {
		Error libraryErrorSignature `json:"error"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	return response.Code, response.Header().Get(echo.HeaderCacheControl), envelope.Error
}

func TestValidUnauthorizedIdentifiersAreIndistinguishableFromMissingContent(t *testing.T) {
	fixture := newLibraryFixture(t)
	missingID := uuid.NewString()
	for _, test := range []struct {
		name             string
		method           string
		unauthorizedPath string
		missingPath      string
	}{
		{
			name: "Media thumbnail", method: http.MethodGet,
			unauthorizedPath: "/api/me/media/" + fixture.media[2].String() + "/thumbnail",
			missingPath:      "/api/me/media/" + missingID + "/thumbnail",
		},
		{
			name: "Media preview", method: http.MethodGet,
			unauthorizedPath: "/api/me/media/" + fixture.media[2].String() + "/preview",
			missingPath:      "/api/me/media/" + missingID + "/preview",
		},
		{
			name: "Media video", method: http.MethodGet,
			unauthorizedPath: "/api/me/media/" + fixture.media[2].String() + "/video",
			missingPath:      "/api/me/media/" + missingID + "/video",
		},
		{
			name: "Media original", method: http.MethodGet,
			unauthorizedPath: "/api/me/media/" + fixture.media[2].String() + "/original",
			missingPath:      "/api/me/media/" + missingID + "/original",
		},
		{
			name: "Event", method: http.MethodGet,
			unauthorizedPath: "/api/me/events/" + fixture.events[2].String(),
			missingPath:      "/api/me/events/" + missingID,
		},
		{
			name: "Publication", method: http.MethodPost,
			unauthorizedPath: "/api/me/new-for-you/" + fixture.publications[2].String() + "/seen",
			missingPath:      "/api/me/new-for-you/" + missingID + "/seen",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			unauthorizedStatus, unauthorizedCache, unauthorizedError := requestLibraryError(
				t, fixture, test.method, test.unauthorizedPath,
			)
			missingStatus, missingCache, missingError := requestLibraryError(
				t, fixture, test.method, test.missingPath,
			)
			assert.Equal(t, http.StatusNotFound, unauthorizedStatus)
			assert.Equal(t, missingStatus, unauthorizedStatus)
			assert.Equal(t, missingError, unauthorizedError)
			assert.Equal(t, "private, no-store", unauthorizedCache)
			assert.Equal(t, missingCache, unauthorizedCache)
		})
	}
	assert.Empty(t, fixture.thumbnail.assets, "unauthorized and missing Media identifiers never reach Immich")
}

type boundedStreamBody struct {
	remaining int
	maxRead   int
	closed    bool
}

func (body *boundedStreamBody) Read(contents []byte) (int, error) {
	if len(contents) > body.maxRead {
		body.maxRead = len(contents)
	}
	if body.remaining == 0 {
		return 0, io.EOF
	}
	count := min(len(contents), body.remaining)
	for index := range count {
		contents[index] = byte(index)
	}
	body.remaining -= count
	return count, nil
}

func (body *boundedStreamBody) Close() error {
	body.closed = true
	return nil
}

type failingThumbnailBody struct{}

func (failingThumbnailBody) Read(contents []byte) (int, error) {
	return copy(contents, "partial"), errors.New("read https://immich.internal/private?key=secret")
}

func (failingThumbnailBody) Close() error { return nil }

func TestThumbnailRouteKeepsUpstreamFailuresSafeAndPrivate(t *testing.T) {
	for _, test := range []struct {
		name       string
		configure  func(*thumbnailStub)
		wantStatus int
		wantBody   string
	}{
		{
			name: "not found",
			configure: func(stub *thumbnailStub) {
				stub.err = immich.ErrNotFound
			},
			wantStatus: http.StatusNotFound,
			wantBody:   `{"error":{"code":"not_found","message":"Content not found.","status_code":404}}`,
		},
		{
			name: "upstream failure",
			configure: func(stub *thumbnailStub) {
				stub.err = errors.New("request https://immich.internal/private?key=secret")
			},
			wantStatus: http.StatusInternalServerError,
			wantBody:   `{"error":{"code":"internal_server_error","message":"Internal Server Error","status_code":500}}`,
		},
		{
			name: "streaming read failure",
			configure: func(stub *thumbnailStub) {
				stub.response = immich.MediaResponse{
					Body: failingThumbnailBody{}, ContentType: "image/png", ContentLength: -1,
				}
			},
			wantStatus: http.StatusOK,
			wantBody:   "partial",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLibraryFixture(t)
			test.configure(fixture.thumbnail)
			e := echo.New()
			e.HTTPErrorHandler = errcodes.NewHandler().Handle
			RegisterRoutes(e, NewHandler(fixture.service, &routeAuthorizer{actor: fixture.actor}))
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
				"/api/me/media/"+fixture.media[0].String()+"/thumbnail", nil)
			request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "opaque"})
			response := httptest.NewRecorder()

			e.ServeHTTP(response, request)

			assert.Equal(t, test.wantStatus, response.Code)
			wantCache := "private, no-store"
			if test.wantStatus == http.StatusOK {
				wantCache = "private, no-cache"
			}
			assert.Equal(t, wantCache, response.Header().Get(echo.HeaderCacheControl))
			if test.wantStatus == http.StatusOK {
				assert.Equal(t, test.wantBody, response.Body.String())
			} else {
				assert.JSONEq(t, test.wantBody, response.Body.String())
			}
			assert.NotContains(t, response.Body.String(), "immich.internal")
			assert.NotContains(t, response.Body.String(), "secret")
		})
	}
}

func TestThumbnailAndPreviewRoutesForwardValidatorsAndDispatch(t *testing.T) {
	t.Run("thumbnail revalidation", func(t *testing.T) {
		fixture := newLibraryFixture(t)
		fixture.thumbnail.response = immich.MediaResponse{
			Body: io.NopCloser(strings.NewReader("private upstream response")), StatusCode: http.StatusNotModified,
			ContentLength: -1, ETag: `"thumbnail-v1"`,
		}
		e := echo.New()
		e.HTTPErrorHandler = errcodes.NewHandler().Handle
		RegisterRoutes(e, NewHandler(fixture.service, &routeAuthorizer{actor: fixture.actor}))
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
			"/api/me/media/"+fixture.media[0].String()+"/thumbnail", nil)
		request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "opaque"})
		request.Header.Set("If-None-Match", `"thumbnail-v1"`)
		response := httptest.NewRecorder()

		e.ServeHTTP(response, request)

		assert.Equal(t, http.StatusNotModified, response.Code)
		assert.Empty(t, response.Body.String())
		assert.Equal(t, "private, no-cache", response.Header().Get(echo.HeaderCacheControl))
		assert.Equal(t, `"thumbnail-v1"`, response.Header().Get("ETag"))
		assert.Equal(t, []string{"thumbnail"}, fixture.thumbnail.representations)
		assert.Equal(t, immich.MediaRequest{IfNoneMatch: `"thumbnail-v1"`}, fixture.thumbnail.requests[0])
		var engagementCount int
		require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM engagement_events`).Scan(context.Background(), &engagementCount))
		assert.Equal(t, 1, engagementCount, "thumbnail traffic adds nothing beyond Session creation")
	})

	t.Run("successful preview with If-Modified-Since", func(t *testing.T) {
		fixture := newLibraryFixture(t)
		lastModified := "Mon, 27 Jul 2026 12:00:00 GMT"
		ifModifiedSince := "Sun, 26 Jul 2026 12:00:00 GMT"
		fixture.thumbnail.response = immich.MediaResponse{
			Body: io.NopCloser(strings.NewReader("preview")), StatusCode: http.StatusOK,
			ContentType: "image/jpeg", ContentLength: int64(len("preview")), LastModified: lastModified,
		}
		e := echo.New()
		e.HTTPErrorHandler = errcodes.NewHandler().Handle
		RegisterRoutes(e, NewHandler(fixture.service, &routeAuthorizer{actor: fixture.actor}))
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
			"/api/me/media/"+fixture.media[0].String()+"/preview", nil)
		request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "opaque"})
		request.Header.Set("If-Modified-Since", ifModifiedSince)
		response := httptest.NewRecorder()

		e.ServeHTTP(response, request)

		assert.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, "preview", response.Body.String())
		assert.Equal(t, strconv.Itoa(len("preview")), response.Header().Get(echo.HeaderContentLength))
		assert.Equal(t, lastModified, response.Header().Get("Last-Modified"))
		assert.Equal(t, []string{"preview"}, fixture.thumbnail.representations)
		assert.Equal(t, immich.MediaRequest{IfModifiedSince: ifModifiedSince}, fixture.thumbnail.requests[0])
		var engagementCount int
		require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM engagement_events`).Scan(context.Background(), &engagementCount))
		assert.Equal(t, 1, engagementCount, "Preview as Recipient adds nothing beyond Session creation")
	})
}

func TestVideoAndOriginalRoutesStreamSafeHeadersWithBoundedMemory(t *testing.T) {
	t.Run("partial video", func(t *testing.T) {
		fixture := newLibraryFixture(t)
		_, err := fixture.db.NewRaw(`
			UPDATE media_items SET media_type = 'video' WHERE id = ?;
			UPDATE published_media_placements SET media_type = 'video' WHERE media_item_id = ?
		`, fixture.media[0], fixture.media[0]).Exec(context.Background())
		require.NoError(t, err)
		body := &boundedStreamBody{remaining: 256 << 10}
		fixture.thumbnail.response = immich.MediaResponse{
			Body: body, StatusCode: http.StatusPartialContent, ContentType: "video/mp4",
			ContentLength: 256 << 10, ContentRange: "bytes 0-262143/1048576",
			AcceptRanges: "bytes", ETag: `"video"`, LastModified: "Mon, 27 Jul 2026 12:00:00 GMT",
		}
		e := echo.New()
		e.HTTPErrorHandler = errcodes.NewHandler().Handle
		RegisterRoutes(e, NewHandler(fixture.service, &routeAuthorizer{actor: fixture.actor}))
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
			"/api/me/media/"+fixture.media[0].String()+"/video", nil)
		request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "opaque"})
		request.Header.Set("Range", "bytes=0-262143")
		request.Header.Set("If-Range", `"video"`)
		response := httptest.NewRecorder()

		e.ServeHTTP(response, request)

		assert.Equal(t, http.StatusPartialContent, response.Code)
		assert.Equal(t, "private, no-cache", response.Header().Get(echo.HeaderCacheControl))
		assert.Equal(t, "bytes 0-262143/1048576", response.Header().Get("Content-Range"))
		assert.Equal(t, "bytes", response.Header().Get("Accept-Ranges"))
		assert.Equal(t, `"video"`, response.Header().Get("ETag"))
		assert.Equal(t, strconv.Itoa(256<<10), response.Header().Get(echo.HeaderContentLength))
		assert.Equal(t, "Mon, 27 Jul 2026 12:00:00 GMT", response.Header().Get("Last-Modified"))
		assert.Empty(t, response.Header().Get(echo.HeaderContentDisposition))
		assert.Equal(t, 256<<10, response.Body.Len())
		assert.LessOrEqual(t, body.maxRead, 32<<10, "stream memory is bounded by the fixed copy buffer")
		assert.True(t, body.closed)
		assert.Equal(t, []string{"video"}, fixture.thumbnail.representations)
		assert.Equal(t, immich.MediaRequest{Range: "bytes=0-262143", IfRange: `"video"`}, fixture.thumbnail.requests[0])
	})

	t.Run("If-Range mismatch returns full video", func(t *testing.T) {
		fixture := newLibraryFixture(t)
		_, err := fixture.db.NewRaw(`
			UPDATE media_items SET media_type = 'video' WHERE id = ?;
			UPDATE published_media_placements SET media_type = 'video' WHERE media_item_id = ?
		`, fixture.media[0], fixture.media[0]).Exec(context.Background())
		require.NoError(t, err)
		fixture.thumbnail.response = immich.MediaResponse{
			Body: io.NopCloser(strings.NewReader("complete-video")), StatusCode: http.StatusOK,
			ContentType: "video/mp4", ContentLength: int64(len("complete-video")), AcceptRanges: "bytes", ETag: `"current"`,
		}
		e := echo.New()
		e.HTTPErrorHandler = errcodes.NewHandler().Handle
		RegisterRoutes(e, NewHandler(fixture.service, &routeAuthorizer{actor: fixture.actor}))
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
			"/api/me/media/"+fixture.media[0].String()+"/video", nil)
		request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "opaque"})
		request.Header.Set("Range", "bytes=0-3")
		request.Header.Set("If-Range", `"stale"`)
		response := httptest.NewRecorder()

		e.ServeHTTP(response, request)

		assert.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, "complete-video", response.Body.String())
		assert.Empty(t, response.Header().Get("Content-Range"))
		assert.Equal(t, immich.MediaRequest{Range: "bytes=0-3", IfRange: `"stale"`}, fixture.thumbnail.requests[0])
	})

	t.Run("unchanged original", func(t *testing.T) {
		fixture := newLibraryFixture(t)
		original := []byte{0, 1, 2, 0xff, 'E', 'X', 'I', 'F'}
		fixture.thumbnail.response = immich.MediaResponse{
			Body: io.NopCloser(bytes.NewReader(original)), StatusCode: http.StatusOK,
			ContentType: "image/jpeg", ContentLength: int64(len(original)), ETag: `"original"`,
			LastModified: "Mon, 27 Jul 2026 12:00:00 GMT",
		}
		e := echo.New()
		e.HTTPErrorHandler = errcodes.NewHandler().Handle
		RegisterRoutes(e, NewHandler(fixture.service, &routeAuthorizer{actor: fixture.actor}))
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
			"/api/me/media/"+fixture.media[0].String()+"/original", nil)
		request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "opaque"})
		request.Header.Set("If-None-Match", `"original"`)
		response := httptest.NewRecorder()

		e.ServeHTTP(response, request)

		assert.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, original, response.Body.Bytes())
		assert.Equal(t, "private, no-cache", response.Header().Get(echo.HeaderCacheControl))
		assert.Equal(t, strconv.Itoa(len(original)), response.Header().Get(echo.HeaderContentLength))
		assert.Equal(t, "Mon, 27 Jul 2026 12:00:00 GMT", response.Header().Get("Last-Modified"))
		assert.Equal(t, "attachment; filename=memento-"+fixture.media[0].String()+".jpg",
			response.Header().Get(echo.HeaderContentDisposition))
		assert.Equal(t, immich.MediaRequest{IfNoneMatch: `"original"`}, fixture.thumbnail.requests[0])
	})
}

func TestConditionalAndFailedMediaStreamsRemainPrivateAndSafe(t *testing.T) {
	for _, test := range []struct {
		name       string
		request    immich.MediaRequest
		status     int
		rangeValue string
	}{
		{name: "not modified", request: immich.MediaRequest{IfNoneMatch: `"current"`}, status: http.StatusNotModified},
		{name: "unsatisfied range", request: immich.MediaRequest{Range: "bytes=99-100"}, status: http.StatusRequestedRangeNotSatisfiable, rangeValue: "bytes */12"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLibraryFixture(t)
			fixture.thumbnail.response = immich.MediaResponse{
				Body:       io.NopCloser(strings.NewReader("private upstream response")),
				StatusCode: test.status, ContentLength: -1, ContentRange: test.rangeValue, ETag: `"current"`,
			}
			e := echo.New()
			e.HTTPErrorHandler = errcodes.NewHandler().Handle
			RegisterRoutes(e, NewHandler(fixture.service, &routeAuthorizer{actor: fixture.actor}))
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
				"/api/me/media/"+fixture.media[0].String()+"/original", nil)
			request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "opaque"})
			request.Header.Set("Range", test.request.Range)
			request.Header.Set("If-None-Match", test.request.IfNoneMatch)
			response := httptest.NewRecorder()

			e.ServeHTTP(response, request)

			assert.Equal(t, test.status, response.Code)
			assert.Empty(t, response.Body.String())
			assert.Equal(t, "private, no-cache", response.Header().Get(echo.HeaderCacheControl))
			assert.Equal(t, test.rangeValue, response.Header().Get("Content-Range"))
			assert.Equal(t, test.request, fixture.thumbnail.requests[0])
		})
	}

	t.Run("interrupted original is observable without leaking the upstream error", func(t *testing.T) {
		fixture := newLibraryFixture(t)
		fixture.thumbnail.response = immich.MediaResponse{
			Body: failingThumbnailBody{}, StatusCode: http.StatusOK,
			ContentType: "application/octet-stream", ContentLength: 64,
		}
		e := echo.New()
		e.HTTPErrorHandler = errcodes.NewHandler().Handle
		RegisterRoutes(e, NewHandler(fixture.service, &routeAuthorizer{actor: fixture.actor}))
		server := httptest.NewServer(e)
		defer server.Close()
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
			server.URL+"/api/me/media/"+fixture.media[0].String()+"/original", nil)
		require.NoError(t, err)
		request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "opaque"})

		response, err := server.Client().Do(request)
		require.NoError(t, err)
		contents, readErr := io.ReadAll(response.Body)
		require.NoError(t, response.Body.Close())

		assert.Equal(t, http.StatusOK, response.StatusCode)
		assert.Equal(t, "private, no-cache", response.Header.Get(echo.HeaderCacheControl))
		assert.Equal(t, "partial", string(contents))
		require.Error(t, readErr, "the route-level HTTP response exposes the interrupted stream")
		assert.NotContains(t, readErr.Error(), "immich.internal")
		assert.NotContains(t, string(contents), "secret")
	})
}

func TestMediaRepresentationSelectsOnlyActiveBackingAndFailsClosedForVideo(t *testing.T) {
	t.Run("active backing", func(t *testing.T) {
		fixture := newLibraryFixture(t)
		activeAssetID := uuid.New()
		_, err := fixture.db.NewRaw(`
			UPDATE media_backings SET active = false, ended_at = now()
			WHERE media_item_id = ? AND active;
			INSERT INTO media_backings (id, media_item_id, immich_asset_id, linked_at)
			VALUES (gen_random_uuid(), ?, ?, now())
		`, fixture.media[0], fixture.media[0], activeAssetID).Exec(context.Background())
		require.NoError(t, err)

		response, err := fixture.service.Original(context.Background(), fixture.actor, fixture.media[0], immich.MediaRequest{})
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		assert.Equal(t, []uuid.UUID{activeAssetID}, fixture.thumbnail.assets,
			"historical and denormalized asset identifiers cannot reach Immich")
	})

	t.Run("video representation for image", func(t *testing.T) {
		fixture := newLibraryFixture(t)

		_, err := fixture.service.Video(context.Background(), fixture.actor, fixture.media[0], immich.MediaRequest{})

		assert.ErrorIs(t, err, ErrNotFound)
		assert.Empty(t, fixture.thumbnail.assets, "an image cannot reach Immich video playback")
	})
}

func TestMediaRepresentationsRevalidateEveryAuthorizationBoundaryBeforeImmich(t *testing.T) {
	for _, test := range []struct {
		name   string
		deny   func(context.Context, libraryFixture) error
		reason string
	}{
		{
			name: "revoked Session",
			deny: func(ctx context.Context, fixture libraryFixture) error {
				_, err := fixture.db.NewRaw(`UPDATE sessions SET revoked_at = now() WHERE id = ?`, fixture.actor.SessionID).Exec(ctx)
				return err
			},
			reason: "a revoked Session cannot authorize Media",
		},
		{
			name: "expired Session",
			deny: func(ctx context.Context, fixture libraryFixture) error {
				_, err := fixture.db.NewRaw(`UPDATE sessions SET idle_expires_at = now() - interval '1 second' WHERE id = ?`, fixture.actor.SessionID).Exec(ctx)
				return err
			},
			reason: "an expired Session cannot authorize Media",
		},
		{
			name: "stale security epoch",
			deny: func(ctx context.Context, fixture libraryFixture) error {
				_, err := fixture.db.NewRaw(`UPDATE system_settings SET security_epoch = decode(repeat('99', 32), 'hex') WHERE id = 1`).Exec(ctx)
				return err
			},
			reason: "a Session from a stale security epoch cannot authorize Media",
		},
		{
			name: "non-current access generation",
			deny: func(ctx context.Context, fixture libraryFixture) error {
				_, err := fixture.db.NewRaw(`UPDATE recipient_access_generations SET is_current = false WHERE id = ?`, fixture.actor.AccessID).Exec(ctx)
				return err
			},
			reason: "a non-current access generation cannot authorize Media",
		},
		{
			name: "suspended access generation",
			deny: func(ctx context.Context, fixture libraryFixture) error {
				_, err := fixture.db.NewRaw(`UPDATE recipient_access_generations SET state = 'suspended' WHERE id = ?`, fixture.actor.AccessID).Exec(ctx)
				return err
			},
			reason: "a suspended access generation cannot authorize Media",
		},
		{
			name: "missing Audience entitlement",
			deny: func(ctx context.Context, fixture libraryFixture) error {
				_, err := fixture.db.NewRaw(`DELETE FROM current_audience_entitlements
					WHERE recipient_access_generation_id = ? AND media_item_id = ?`, fixture.actor.AccessID, fixture.media[0]).Exec(ctx)
				return err
			},
			reason: "Media without a current Audience entitlement cannot be streamed",
		},
		{
			name: "source unavailable",
			deny: func(ctx context.Context, fixture libraryFixture) error {
				_, err := fixture.db.NewRaw(`UPDATE media_items SET availability = 'source_missing', missing_since = now() WHERE id = ?`, fixture.media[0]).Exec(ctx)
				return err
			},
			reason: "source-unavailable Media cannot be streamed",
		},
		{
			name: "withdrawn Media",
			deny: func(ctx context.Context, fixture libraryFixture) error {
				_, err := fixture.db.NewRaw(`INSERT INTO content_withdrawals
					(id, target_kind, target_id, withdrawn_by_person_id, withdrawn_at)
					VALUES (gen_random_uuid(), 'media', ?, ?, now())`, fixture.media[0], fixture.curator).Exec(ctx)
				return err
			},
			reason: "withdrawn Media cannot be streamed",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLibraryFixture(t)
			ctx := context.Background()
			_, err := fixture.db.NewRaw(`
				UPDATE media_items SET media_type = 'video' WHERE id = ?;
				UPDATE published_media_placements SET media_type = 'video' WHERE media_item_id = ?
			`, fixture.media[0], fixture.media[0]).Exec(ctx)
			require.NoError(t, err)
			require.NoError(t, test.deny(ctx, fixture))

			for _, representation := range []struct {
				name string
				load func() (immich.MediaResponse, error)
			}{
				{name: "thumbnail", load: func() (immich.MediaResponse, error) {
					return fixture.service.Thumbnail(ctx, fixture.actor, fixture.media[0], immich.MediaRequest{})
				}},
				{name: "preview", load: func() (immich.MediaResponse, error) {
					return fixture.service.Preview(ctx, fixture.actor, fixture.media[0], immich.MediaRequest{})
				}},
				{name: "video", load: func() (immich.MediaResponse, error) {
					return fixture.service.Video(ctx, fixture.actor, fixture.media[0], immich.MediaRequest{})
				}},
				{name: "original", load: func() (immich.MediaResponse, error) {
					return fixture.service.Original(ctx, fixture.actor, fixture.media[0], immich.MediaRequest{})
				}},
			} {
				_, err := representation.load()
				assert.ErrorIs(t, err, ErrNotFound, representation.name+": "+test.reason)
			}
			assert.Empty(t, fixture.thumbnail.assets, "a denied representation request never reaches Immich")
		})
	}
}

func TestRepresentationClosesOpenedBodyWhenActorInvalidatesBeforeHandoff(t *testing.T) {
	tests := []struct {
		name           string
		representation string
		invalidate     string
	}{
		{name: "Session revocation", representation: "thumbnail", invalidate: `UPDATE sessions SET revoked_at = now() WHERE id = ?`},
		{name: "Person archive", representation: "preview", invalidate: `UPDATE people SET archived_at = now() WHERE id = ?`},
		{name: "access suspension", representation: "video", invalidate: `UPDATE recipient_access_generations SET state = 'suspended' WHERE id = ?`},
		{name: "access revocation", representation: "original", invalidate: `UPDATE recipient_access_generations SET state = 'revoked', is_current = false, ended_at = now() WHERE id = ?`},
		{name: "generation invalidation", representation: "thumbnail", invalidate: `UPDATE recipient_access_generations SET is_current = false WHERE id = ?`},
		{name: "security epoch change", representation: "preview", invalidate: `UPDATE system_settings SET security_epoch = decode(repeat('99', 32), 'hex') WHERE id = ?`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLibraryFixture(t)
			ctx := context.Background()
			if test.representation == "video" {
				_, err := fixture.db.NewRaw(`
					UPDATE media_items SET media_type = 'video' WHERE id = ?;
					UPDATE published_media_placements SET media_type = 'video' WHERE media_item_id = ?
				`, fixture.media[0], fixture.media[0]).Exec(ctx)
				require.NoError(t, err)
			}
			source := &blockingRepresentationSource{
				started: make(chan struct{}, 1), release: make(chan struct{}),
				bodies: make(chan *observedRepresentationBody, 1),
			}
			service := New(fixture.db, source)
			result := make(chan error, 1)
			go func() {
				var response immich.MediaResponse
				var err error
				switch test.representation {
				case "thumbnail":
					response, err = service.Thumbnail(ctx, fixture.actor, fixture.media[0], immich.MediaRequest{})
				case "preview":
					response, err = service.Preview(ctx, fixture.actor, fixture.media[0], immich.MediaRequest{})
				case "video":
					response, err = service.Video(ctx, fixture.actor, fixture.media[0], immich.MediaRequest{})
				case "original":
					response, err = service.Original(ctx, fixture.actor, fixture.media[0], immich.MediaRequest{})
				}
				if response.Body != nil {
					_ = response.Body.Close()
				}
				result <- err
			}()
			select {
			case <-source.started:
			case <-time.After(5 * time.Second):
				t.Fatal("Immich response did not begin opening")
			}
			argument := any(fixture.actor.AccessID)
			switch test.name {
			case "Session revocation":
				argument = fixture.actor.SessionID
			case "Person archive":
				argument = fixture.actor.PersonID
			case "security epoch change":
				argument = 1
			}
			_, err := fixture.db.NewRaw(test.invalidate, argument).Exec(ctx)
			require.NoError(t, err, "invalidation must commit while Immich is slow")
			close(source.release)
			var body *observedRepresentationBody
			select {
			case body = <-source.bodies:
			case <-time.After(5 * time.Second):
				t.Fatal("Immich did not return its opened body")
			}
			select {
			case err := <-result:
				assert.ErrorIs(t, err, ErrNotFound)
			case <-time.After(5 * time.Second):
				t.Fatal("representation did not finish final authorization")
			}
			select {
			case <-body.closed:
			case <-time.After(5 * time.Second):
				t.Fatal("denied opened body was not closed")
			}
		})
	}
}

func TestRepresentationLocksResolvedMediaAndBackingThroughFinalHandoff(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate string
	}{
		{name: "availability mutation", mutate: `UPDATE media_items SET availability = 'source_missing', missing_since = now() WHERE id = ?`},
		{name: "active backing mutation", mutate: `UPDATE media_backings SET active = false, ended_at = now() WHERE media_item_id = ? AND active`},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLibraryFixture(t)
			ctx := context.Background()
			handoffLocked := make(chan struct{})
			releaseHandoff := make(chan struct{})
			service := New(fixture.db, fixture.thumbnail)
			service.representationHandoffLocked = func() {
				close(handoffLocked)
				<-releaseHandoff
			}
			type representationResult struct {
				response immich.MediaResponse
				err      error
			}
			opened := make(chan representationResult, 1)
			go func() {
				response, err := service.Thumbnail(ctx, fixture.actor, fixture.media[0], immich.MediaRequest{})
				opened <- representationResult{response: response, err: err}
			}()
			select {
			case <-handoffLocked:
			case <-time.After(5 * time.Second):
				t.Fatal("representation did not lock its resolved Media handoff")
			}

			writerPID := make(chan int, 1)
			mutated := make(chan error, 1)
			go func() {
				tx, err := fixture.db.BeginTx(ctx, nil)
				if err != nil {
					mutated <- err
					return
				}
				defer func() { _ = tx.Rollback() }()
				var pid int
				if err := tx.NewRaw(`SELECT pg_backend_pid()`).Scan(ctx, &pid); err != nil {
					mutated <- err
					return
				}
				writerPID <- pid
				if _, err := tx.NewRaw(test.mutate, fixture.media[0]).Exec(ctx); err != nil {
					mutated <- err
					return
				}
				mutated <- tx.Commit()
			}()
			var pid int
			select {
			case pid = <-writerPID:
			case err := <-mutated:
				require.NoError(t, err)
				t.Fatal("Media lifecycle mutation completed before the final handoff released its row locks")
			case <-time.After(5 * time.Second):
				t.Fatal("Media lifecycle writer did not start")
			}
			waitForLibraryLockWait(t, fixture.db, pid)
			close(releaseHandoff)

			select {
			case result := <-opened:
				require.NoError(t, result.err)
				require.NotNil(t, result.response.Body)
				require.NoError(t, result.response.Body.Close())
			case <-time.After(5 * time.Second):
				t.Fatal("representation did not complete its final handoff")
			}
			select {
			case err := <-mutated:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("Media lifecycle mutation did not resume after the representation handoff")
			}
			assert.Equal(t, []uuid.UUID{fixture.assets[0]}, fixture.thumbnail.assets)
		})
	}
}

func waitForLibraryLockWait(t *testing.T, db *bun.DB, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	lastWait := ""
	for time.Now().Before(deadline) {
		if err := db.NewRaw(`SELECT COALESCE(wait_event_type, '') || ':' || COALESCE(wait_event, '')
			FROM pg_stat_activity WHERE pid = ?`, pid).Scan(context.Background(), &lastWait); err != nil {
			t.Fatalf("inspect Media lifecycle writer wait: %v", err)
		}
		if strings.HasPrefix(lastWait, "Lock:") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Media lifecycle writer did not wait on the representation row locks; last wait was %q", lastWait)
}

func waitForLibraryBlockedQuery(t *testing.T, db *bun.DB, blockerPID int, pattern string) int {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	lastState := "no matching backend"
	for {
		type waitState struct {
			PID      int
			WaitType string
			Blockers string
			Query    string
		}
		var states []waitState
		err := db.NewRaw(`
			SELECT pid, COALESCE(wait_event_type, '') AS wait_type,
				array_to_string(pg_blocking_pids(pid), ',') AS blockers, query
			FROM pg_stat_activity
			WHERE datname = current_database() AND pid <> ? AND query LIKE ?
			ORDER BY pid
		`, blockerPID, pattern).Scan(context.Background(), &states)
		require.NoError(t, err)
		for _, state := range states {
			lastState = fmt.Sprintf("pid=%d wait_type=%q blockers=%v query=%q", state.PID, state.WaitType, state.Blockers, state.Query)
			if state.WaitType == "Lock" && slices.Contains(strings.Split(state.Blockers, ","), strconv.Itoa(blockerPID)) {
				return state.PID
			}
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("query did not reach the controlled lock: pattern=%q blocker_pid=%d last_state=%s", pattern, blockerPID, lastState)
		}
	}
}

func TestSlowRepresentationOpeningDoesNotExhaustMinimumConnectionPool(t *testing.T) {
	fixture := newLibraryFixture(t)
	fixture.db.SetMaxOpenConns(2)
	fixture.db.SetMaxIdleConns(2)
	source := &blockingRepresentationSource{
		started: make(chan struct{}, 2), release: make(chan struct{}),
		bodies: make(chan *observedRepresentationBody, 2),
	}
	service := New(fixture.db, source)
	type result struct {
		response immich.MediaResponse
		err      error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			response, err := service.Thumbnail(context.Background(), fixture.actor, fixture.media[0], immich.MediaRequest{})
			results <- result{response: response, err: err}
		}()
	}
	for range 2 {
		select {
		case <-source.started:
		case <-time.After(5 * time.Second):
			t.Fatal("slow Immich opening did not start")
		}
	}

	queryCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var one int
	require.NoError(t, fixture.db.NewRaw(`SELECT 1`).Scan(queryCtx, &one),
		"slow upstream headers must not retain either pooled connection")
	assert.Equal(t, 1, one)
	close(source.release)
	for range 2 {
		select {
		case opened := <-results:
			require.NoError(t, opened.err)
			require.NoError(t, opened.response.Body.Close())
		case <-time.After(5 * time.Second):
			t.Fatal("representation did not finish after upstream release")
		}
	}
}

type exactLibraryAudience struct {
	PublishedEntries    string
	CurrentEntitlements string
}

func loadExactLibraryAudience(t *testing.T, fixture libraryFixture, mediaID uuid.UUID) exactLibraryAudience {
	t.Helper()
	ctx := context.Background()
	var state exactLibraryAudience
	require.NoError(t, fixture.db.NewRaw(`
		SELECT COALESCE(jsonb_agg(to_jsonb(audience) ORDER BY audience.published_moment_id, audience.recipient_person_id, audience.recipient_access_generation_id), '[]'::jsonb)::text
		FROM (
			SELECT DISTINCT entry.published_moment_id, entry.recipient_person_id, entry.recipient_access_generation_id
			FROM audience_entries AS entry
			JOIN current_published_placements AS placement ON placement.published_moment_id = entry.published_moment_id
			WHERE placement.media_item_id = ?
		) AS audience
	`, mediaID).Scan(ctx, &state.PublishedEntries))
	require.NoError(t, fixture.db.NewRaw(`
		SELECT COALESCE(jsonb_agg(to_jsonb(entitlement) ORDER BY entitlement.event_id, entitlement.publication_id, entitlement.recipient_person_id, entitlement.recipient_access_generation_id, entitlement.media_item_id), '[]'::jsonb)::text
		FROM (
			SELECT event_id, publication_id, recipient_person_id, recipient_access_generation_id, media_item_id
			FROM current_audience_entitlements WHERE media_item_id = ?
		) AS entitlement
	`, mediaID).Scan(ctx, &state.CurrentEntitlements))
	require.NotEqual(t, "[]", state.PublishedEntries)
	require.NotEqual(t, "[]", state.CurrentEntitlements)
	return state
}

func TestUpstreamMissingResponseFailsEveryRepresentationClosedWithoutRemovingHistory(t *testing.T) {
	fixture := newLibraryFixture(t)
	ctx := context.Background()
	audienceBefore := loadExactLibraryAudience(t, fixture, fixture.media[0])
	fixture.thumbnail.err = immich.ErrNotFound

	_, err := fixture.service.Thumbnail(ctx, fixture.actor, fixture.media[0], immich.MediaRequest{})
	assert.ErrorIs(t, err, ErrNotFound)
	fixture.thumbnail.err = nil

	photos, err := fixture.service.Photos(ctx, fixture.actor, "10", "", false)
	require.NoError(t, err)
	var missing *Media
	for index := range photos.Media {
		if photos.Media[index].ID == fixture.media[0].String() {
			missing = &photos.Media[index]
			break
		}
	}
	require.NotNil(t, missing, "the published listing remains in Recipient history")
	assert.False(t, missing.Available)
	assert.Empty(t, missing.ThumbnailURL)
	assert.Empty(t, missing.PreviewURL)
	assert.Empty(t, missing.VideoURL)
	assert.Empty(t, missing.OriginalURL)

	for _, open := range []func() (immich.MediaResponse, error){
		func() (immich.MediaResponse, error) {
			return fixture.service.Thumbnail(ctx, fixture.actor, fixture.media[0], immich.MediaRequest{})
		},
		func() (immich.MediaResponse, error) {
			return fixture.service.Preview(ctx, fixture.actor, fixture.media[0], immich.MediaRequest{})
		},
		func() (immich.MediaResponse, error) {
			return fixture.service.Original(ctx, fixture.actor, fixture.media[0], immich.MediaRequest{})
		},
	} {
		_, err := open()
		assert.ErrorIs(t, err, ErrNotFound)
	}
	assert.Len(t, fixture.thumbnail.assets, 1, "once missing is observed no derivative or original can reach Immich")
	var placements int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM current_published_placements WHERE media_item_id = ?`, fixture.media[0]).Scan(ctx, &placements))
	assert.Positive(t, placements)
	assert.Equal(t, audienceBefore, loadExactLibraryAudience(t, fixture, fixture.media[0]), "Source missing must preserve every relevant Audience and entitlement row")
}

func TestVideoPlaybackNotFoundBlocksEveryRepresentationAndPreservesHistory(t *testing.T) {
	fixture := newLibraryFixture(t)
	ctx := context.Background()
	_, err := fixture.db.NewRaw(`
		UPDATE media_items SET media_type = 'video' WHERE id = ?;
		UPDATE published_media_placements SET media_type = 'video' WHERE media_item_id = ?
	`, fixture.media[0], fixture.media[0]).Exec(ctx)
	require.NoError(t, err)
	audienceBefore := loadExactLibraryAudience(t, fixture, fixture.media[0])
	fixture.thumbnail.err = immich.ErrNotFound
	_, err = fixture.service.Video(ctx, fixture.actor, fixture.media[0], immich.MediaRequest{})
	assert.ErrorIs(t, err, ErrNotFound)
	fixture.thumbnail.err = nil
	for _, open := range []func() (immich.MediaResponse, error){
		func() (immich.MediaResponse, error) {
			return fixture.service.Thumbnail(ctx, fixture.actor, fixture.media[0], immich.MediaRequest{})
		},
		func() (immich.MediaResponse, error) {
			return fixture.service.Preview(ctx, fixture.actor, fixture.media[0], immich.MediaRequest{})
		},
		func() (immich.MediaResponse, error) {
			return fixture.service.Video(ctx, fixture.actor, fixture.media[0], immich.MediaRequest{})
		},
		func() (immich.MediaResponse, error) {
			return fixture.service.Original(ctx, fixture.actor, fixture.media[0], immich.MediaRequest{})
		},
	} {
		_, err := open()
		assert.ErrorIs(t, err, ErrNotFound)
	}
	assert.Equal(t, []string{"video"}, fixture.thumbnail.representations)
	assert.Equal(t, audienceBefore, loadExactLibraryAudience(t, fixture, fixture.media[0]))
}

func TestCuratorNotFoundUsesSharedMissingTransitionAndFailsClosed(t *testing.T) {
	fixture := newLibraryFixture(t)
	ctx := context.Background()
	_, err := fixture.db.NewRaw(`
		DELETE FROM person_roles WHERE person_id = ? AND role = 'curator';
		INSERT INTO person_roles (person_id, role) VALUES (?, 'curator')
	`, fixture.curator, fixture.actor.PersonID).Exec(ctx)
	require.NoError(t, err)
	curator := fixture.actor
	curator.Curator = true
	fixture.thumbnail.err = immich.ErrNotFound
	_, err = fixture.service.CuratorRepresentation(ctx, curator, fixture.media[0], representationPreview, immich.MediaRequest{})
	assert.ErrorIs(t, err, ErrNotFound)
	fixture.thumbnail.err = nil
	_, err = fixture.service.CuratorRepresentation(ctx, curator, fixture.media[0], representationThumbnail, immich.MediaRequest{})
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = fixture.service.Thumbnail(ctx, fixture.actor, fixture.media[0], immich.MediaRequest{})
	assert.ErrorIs(t, err, ErrNotFound)
	assert.Equal(t, []string{"preview"}, fixture.thumbnail.representations)
	var availability string
	require.NoError(t, fixture.db.NewRaw(`SELECT availability FROM media_items WHERE id = ?`, fixture.media[0]).Scan(ctx, &availability))
	assert.Equal(t, "source_missing", availability)
}

func TestLateOldBackingNotFoundCannotUndoConfirmedRelink(t *testing.T) {
	fixture := newLibraryFixture(t)
	ctx := context.Background()
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	lateSource := &lateMissingRepresentationSource{started: started, release: release}
	lateService := New(fixture.db, lateSource)
	delivery := make(chan error, 1)
	go func() {
		_, err := lateService.Thumbnail(ctx, fixture.actor, fixture.media[0], immich.MediaRequest{})
		delivery <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("old backing delivery did not begin")
	}

	stableMediaID, oldAssetID := fixture.media[0], fixture.assets[0]
	candidateMediaID, candidateAssetID, candidateBackingID := uuid.New(), uuid.New(), uuid.New()
	sourceAlbumID, candidateID := uuid.New(), uuid.New()
	digest := sha256.Sum256([]byte("late old backing relink"))
	checksum := fmt.Sprintf("%x", digest[:20])
	_, err := fixture.db.NewRaw(`
		UPDATE media_items SET availability = 'source_missing', missing_since = now() WHERE id = ?;
		UPDATE media_backings SET checksum = ? WHERE media_item_id = ? AND active;
		INSERT INTO source_albums (
			id, immich_album_id, name, asset_count, source_created_at, source_updated_at,
			first_seen_at, last_seen_at, source_fingerprint, next_reconciliation_at
		) VALUES (?, gen_random_uuid(), 'Relink race', 1, now(), now(), now(), now(), decode(repeat('12', 32), 'hex'), now());
		INSERT INTO media_items (
			id, immich_asset_id, media_type, availability, first_seen_at, last_seen_at
		) VALUES (?, ?, 'image', 'current', now(), now());
		INSERT INTO media_backings (
			id, media_item_id, immich_asset_id, checksum, filename, original_path, linked_at
		) VALUES (?, ?, ?, ?, 'replacement.jpg', '/library/replacement.jpg', now());
		INSERT INTO source_album_memberships (
			source_album_id, immich_asset_id, media_item_id, first_seen_at, last_seen_at, source_fingerprint
		) VALUES (?, ?, ?, now(), now(), decode(repeat('13', 32), 'hex'));
		INSERT INTO media_repair_candidates (
			id, media_item_id, candidate_media_item_id, previous_immich_asset_id,
			candidate_immich_asset_id, previous_evidence, candidate_evidence, created_at
		) VALUES (?, ?, ?, ?, ?,
			jsonb_build_object('checksum', ?, 'capture', NULL, 'filename', '', 'path', ''),
			jsonb_build_object('checksum', ?, 'capture', NULL, 'filename', 'replacement.jpg', 'path', '/library/replacement.jpg'), now())
	`, stableMediaID, checksum, stableMediaID, sourceAlbumID,
		candidateMediaID, candidateAssetID, candidateBackingID, candidateMediaID,
		candidateAssetID, checksum, sourceAlbumID, candidateAssetID, candidateMediaID,
		candidateID, stableMediaID, candidateMediaID, oldAssetID, candidateAssetID, checksum, checksum).Exec(ctx)
	require.NoError(t, err)
	evidence := &relinkEvidenceSource{asset: immich.AssetSummary{
		SourceID: candidateAssetID, MediaType: "image", Checksum: checksum,
		Filename: "renamed-without-identity-change.jpg", OriginalPath: "/library/replacement.jpg",
	}}
	repairService := repairs.New(fixture.db, evidence)
	listed, err := repairService.List(ctx)
	require.NoError(t, err)
	var reviewToken string
	for _, candidate := range listed.MediaCandidates {
		if candidate.ID == candidateID.String() {
			reviewToken = candidate.ReviewToken
		}
	}
	require.NotEmpty(t, reviewToken)
	_, err = repairService.ConfirmMedia(ctx, setup.CuratorSession{
		PersonID: fixture.actor.PersonID, SessionID: fixture.actor.SessionID,
	}, candidateID, reviewToken)
	require.NoError(t, err)
	close(release)
	select {
	case err := <-delivery:
		assert.ErrorIs(t, err, ErrNotFound)
	case <-time.After(5 * time.Second):
		t.Fatal("late old backing response did not finish")
	}
	var activeAssetID uuid.UUID
	var availability string
	require.NoError(t, fixture.db.NewRaw(`
		SELECT media.immich_asset_id, media.availability FROM media_items AS media WHERE media.id = ?
	`, stableMediaID).Scan(ctx, &activeAssetID, &availability))
	assert.Equal(t, candidateAssetID, activeAssetID)
	assert.Equal(t, "current", availability)
}

func TestCandidateNotFoundAndConfirmationSerializeForBothLockOrders(t *testing.T) {
	type raceFixture struct {
		library          libraryFixture
		stableMediaID    uuid.UUID
		stableAssetID    uuid.UUID
		candidateMediaID uuid.UUID
		candidateAssetID uuid.UUID
		candidateID      uuid.UUID
		candidateBacking uuid.UUID
		reviewToken      string
		repairService    *repairs.Service
		deliveryService  *Service
		source           *lateMissingRepresentationSource
		release          chan struct{}
		curator          setup.SessionActor
	}
	newRace := func(t *testing.T) raceFixture {
		t.Helper()
		fixture := newLibraryFixture(t)
		result := raceFixture{library: fixture, stableMediaID: fixture.media[0], stableAssetID: fixture.assets[0],
			candidateMediaID: uuid.New(), candidateAssetID: uuid.New(), candidateID: uuid.New(), candidateBacking: uuid.New()}
		sourceAlbumID := uuid.New()
		digest := sha256.Sum256([]byte("candidate missing confirmation race"))
		checksum := fmt.Sprintf("%x", digest[:20])
		_, err := fixture.db.NewRaw(`
			DELETE FROM person_roles WHERE person_id = ? AND role = 'curator';
			INSERT INTO person_roles (person_id, role) VALUES (?, 'curator');
			UPDATE media_items SET availability = 'source_missing', missing_since = now() WHERE id = ?;
			UPDATE media_backings SET checksum = ? WHERE media_item_id = ? AND active;
			INSERT INTO source_albums (
				id, immich_album_id, name, asset_count, source_created_at, source_updated_at,
				first_seen_at, last_seen_at, source_fingerprint, next_reconciliation_at
			) VALUES (?, gen_random_uuid(), 'Candidate missing race', 1, now(), now(), now(), now(), decode(repeat('14', 32), 'hex'), now());
			INSERT INTO media_items (
				id, immich_asset_id, media_type, availability, first_seen_at, last_seen_at
			) VALUES (?, ?, 'image', 'current', now(), now());
			INSERT INTO media_backings (
				id, media_item_id, immich_asset_id, checksum, filename, original_path, linked_at
			) VALUES (?, ?, ?, ?, 'candidate.jpg', '/library/candidate.jpg', now());
			INSERT INTO source_album_memberships (
				source_album_id, immich_asset_id, media_item_id, first_seen_at, last_seen_at, source_fingerprint
			) VALUES (?, ?, ?, now(), now(), decode(repeat('15', 32), 'hex'));
			INSERT INTO media_repair_candidates (
				id, media_item_id, candidate_media_item_id, previous_immich_asset_id,
				candidate_immich_asset_id, previous_evidence, candidate_evidence, created_at
			) VALUES (?, ?, ?, ?, ?,
				jsonb_build_object('checksum', ?, 'capture', NULL, 'filename', '', 'path', ''),
				jsonb_build_object('checksum', ?, 'capture', NULL, 'filename', 'candidate.jpg', 'path', '/library/candidate.jpg'), now())
		`, fixture.curator, fixture.actor.PersonID, result.stableMediaID, checksum, result.stableMediaID, sourceAlbumID,
			result.candidateMediaID, result.candidateAssetID, result.candidateBacking,
			result.candidateMediaID, result.candidateAssetID, checksum,
			sourceAlbumID, result.candidateAssetID, result.candidateMediaID,
			result.candidateID, result.stableMediaID, result.candidateMediaID,
			result.stableAssetID, result.candidateAssetID, checksum, checksum).Exec(context.Background())
		require.NoError(t, err)
		evidence := &relinkEvidenceSource{asset: immich.AssetSummary{
			SourceID: result.candidateAssetID, MediaType: "image", Checksum: checksum,
			Filename: "candidate.jpg", OriginalPath: "/library/candidate.jpg",
		}}
		result.repairService = repairs.New(fixture.db, evidence)
		listed, err := result.repairService.List(context.Background())
		require.NoError(t, err)
		for _, candidate := range listed.MediaCandidates {
			if candidate.ID == result.candidateID.String() {
				result.reviewToken = candidate.ReviewToken
			}
		}
		require.NotEmpty(t, result.reviewToken)
		started, release := make(chan struct{}, 1), make(chan struct{})
		result.release = release
		result.source = &lateMissingRepresentationSource{started: started, release: release}
		result.deliveryService = New(fixture.db, result.source)
		result.curator = fixture.actor
		result.curator.Curator = true
		return result
	}
	startDelivery := func(t *testing.T, race raceFixture) chan error {
		t.Helper()
		result := make(chan error, 1)
		go func() {
			_, err := race.deliveryService.CuratorRepresentation(context.Background(), race.curator,
				race.candidateMediaID, representationThumbnail, immich.MediaRequest{})
			result <- err
		}()
		select {
		case <-race.source.started:
		case <-time.After(5 * time.Second):
			t.Fatal("candidate delivery did not reach Immich")
		}
		return result
	}
	confirm := func(race raceFixture) error {
		_, err := race.repairService.ConfirmMedia(context.Background(), setup.CuratorSession{
			PersonID: race.library.actor.PersonID, SessionID: race.library.actor.SessionID,
		}, race.candidateID, race.reviewToken)
		return err
	}

	t.Run("confirmation commits before missing transition", func(t *testing.T) {
		race := newRace(t)
		delivery := startDelivery(t, race)
		require.NoError(t, confirm(race))
		close(race.release)
		select {
		case err := <-delivery:
			assert.ErrorIs(t, err, ErrNotFound)
		case <-time.After(5 * time.Second):
			t.Fatal("candidate missing transition did not follow the confirmed backing move")
		}
		var assetID uuid.UUID
		var availability string
		require.NoError(t, race.library.db.NewRaw(`SELECT immich_asset_id, availability FROM media_items WHERE id = ?`, race.stableMediaID).Scan(context.Background(), &assetID, &availability))
		assert.Equal(t, race.candidateAssetID, assetID)
		assert.Equal(t, "source_missing", availability, "late missing evidence follows the exact moved backing")
	})

	t.Run("missing transition locks before confirmation", func(t *testing.T) {
		race := newRace(t)
		delivery := startDelivery(t, race)
		blocker, err := race.library.db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		_, err = blocker.NewRaw(`SELECT id FROM media_items WHERE id = ? FOR UPDATE`, race.candidateMediaID).Exec(context.Background())
		require.NoError(t, err)
		var blockerPID int
		require.NoError(t, blocker.NewRaw(`SELECT pg_backend_pid()`).Scan(context.Background(), &blockerPID))
		close(race.release)
		missingPID := waitForLibraryBlockedQuery(t, race.library.db, blockerPID, `%SELECT id FROM media_items WHERE id =%FOR UPDATE%`)
		confirmation := make(chan error, 1)
		go func() { confirmation <- confirm(race) }()
		waitForLibraryBlockedQuery(t, race.library.db, missingPID, `%SELECT id FROM media_items WHERE id IN%FOR UPDATE%`)
		require.NoError(t, blocker.Commit())
		select {
		case err := <-delivery:
			assert.ErrorIs(t, err, ErrNotFound)
		case <-time.After(5 * time.Second):
			t.Fatal("candidate missing transition did not finish")
		}
		select {
		case err := <-confirmation:
			assert.ErrorIs(t, err, repairs.ErrConflict)
		case <-time.After(5 * time.Second):
			t.Fatal("confirmation did not observe the missing candidate")
		}
		var stableAvailability, candidateAvailability string
		require.NoError(t, race.library.db.NewRaw(`SELECT availability FROM media_items WHERE id = ?`, race.stableMediaID).Scan(context.Background(), &stableAvailability))
		require.NoError(t, race.library.db.NewRaw(`SELECT availability FROM media_items WHERE id = ?`, race.candidateMediaID).Scan(context.Background(), &candidateAvailability))
		assert.Equal(t, "source_missing", stableAvailability)
		assert.Equal(t, "source_missing", candidateAvailability)
	})
}

func TestMalformedOrUnauthorizedUpstreamFailureDoesNotInventSourceMissing(t *testing.T) {
	fixture := newLibraryFixture(t)
	fixture.thumbnail.err = errors.New("Immich returned an invalid response")

	_, err := fixture.service.Thumbnail(context.Background(), fixture.actor, fixture.media[0], immich.MediaRequest{})
	require.Error(t, err)
	var availability string
	require.NoError(t, fixture.db.NewRaw(`SELECT availability FROM media_items WHERE id = ?`, fixture.media[0]).Scan(context.Background(), &availability))
	assert.Equal(t, "current", availability, "only a confirmed not-found response is Source missing evidence")
}

func TestRealImmichAuthorizationFailuresPreserveMediaAvailability(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			fixture := newLibraryFixture(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, fixture.assets[0].String(), strings.Split(r.URL.Path, "/")[3])
				w.WriteHeader(status)
			}))
			defer server.Close()
			client, err := immich.New(config.ImmichConfig{
				URL: server.URL, APIKey: "rejected-key", HealthTimeout: time.Second,
			}, server.Client())
			require.NoError(t, err)
			service := New(fixture.db, client)

			_, err = service.Thumbnail(context.Background(), fixture.actor, fixture.media[0], immich.MediaRequest{})
			require.EqualError(t, err, "Immich API key is invalid")
			var availability string
			var missingSince *time.Time
			require.NoError(t, fixture.db.NewRaw(`SELECT availability, missing_since FROM media_items WHERE id = ?`, fixture.media[0]).Scan(context.Background(), &availability, &missingSince))
			assert.Equal(t, "current", availability)
			assert.Nil(t, missingSince, "upstream authorization failure is not missing-source evidence")
		})
	}
}

func TestRecipientMalformedRangeUpstreamBadRequestDoesNotMarkSourceMissing(t *testing.T) {
	fixture := newLibraryFixture(t)
	var upstreamCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	client, err := immich.New(config.ImmichConfig{
		URL: server.URL, APIKey: "test-key", HealthTimeout: time.Second,
	}, server.Client())
	require.NoError(t, err)
	service := New(fixture.db, client)

	_, err = service.Thumbnail(context.Background(), fixture.actor, fixture.media[0], immich.MediaRequest{Range: "bytes=not-a-range"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, immich.ErrNotFound)
	assert.NotErrorIs(t, err, ErrNotFound)
	var availability string
	require.NoError(t, fixture.db.NewRaw(`SELECT availability FROM media_items WHERE id = ?`, fixture.media[0]).Scan(context.Background(), &availability))
	assert.Equal(t, "current", availability, "a recipient-induced 400 is request failure, not global missing evidence")
	assert.Zero(t, upstreamCalls, "malformed recipient headers are rejected before Immich")
}

func TestDeletedImmichBadRequestWithBrowserValidatorsMarksExactBackingMissing(t *testing.T) {
	for _, request := range []immich.MediaRequest{
		{Range: "bytes=0-1023"},
		{IfNoneMatch: `"thumbnail-v1"`},
		{IfModifiedSince: "Mon, 27 Jul 2026 12:00:00 GMT"},
	} {
		fixture := newLibraryFixture(t)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		}))
		client, err := immich.New(config.ImmichConfig{
			URL: server.URL, APIKey: "test-key", HealthTimeout: time.Second,
		}, server.Client())
		require.NoError(t, err)
		service := New(fixture.db, client)

		_, err = service.Thumbnail(context.Background(), fixture.actor, fixture.media[0], request)
		assert.ErrorIs(t, err, ErrNotFound)
		var availability string
		var missingSince *time.Time
		require.NoError(t, fixture.db.NewRaw(`SELECT availability, missing_since FROM media_items WHERE id = ?`, fixture.media[0]).Scan(context.Background(), &availability, &missingSince))
		assert.Equal(t, "source_missing", availability)
		assert.NotNil(t, missingSince)
		server.Close()
	}
}

func TestRecipientAuthorizationMatrixRevalidatesReuseWithdrawalAndAvailability(t *testing.T) {
	fixture := newLibraryFixture(t)
	ctx := context.Background()
	_, err := fixture.db.NewRaw(`UPDATE media_items SET availability = 'source_missing', missing_since = now() WHERE id = ?`, fixture.media[1]).Exec(ctx)
	require.NoError(t, err)
	photos, err := fixture.service.Photos(ctx, fixture.actor, "10", "", false)
	require.NoError(t, err)
	require.Len(t, photos.Media, 2, "source-missing authorized placements remain visible as unavailable")
	assert.False(t, photos.Media[0].Available)
	assert.Empty(t, photos.Media[0].ThumbnailURL)
	assert.Empty(t, photos.Media[0].PreviewURL)
	assert.Empty(t, photos.Media[0].VideoURL)
	assert.Empty(t, photos.Media[0].OriginalURL)
	_, err = fixture.service.Thumbnail(ctx, fixture.actor, fixture.media[1], immich.MediaRequest{})
	assert.ErrorIs(t, err, ErrNotFound, "source-missing Media cannot reach Immich")
	assert.Empty(t, fixture.thumbnail.assets)

	_, err = fixture.db.NewRaw(`INSERT INTO content_withdrawals
		(id, target_kind, target_id, withdrawn_by_person_id, withdrawn_at)
		VALUES (gen_random_uuid(), 'event', ?, ?, now())`, fixture.events[0], fixture.curator).Exec(ctx)
	require.NoError(t, err)
	photos, err = fixture.service.Photos(ctx, fixture.actor, "10", "", false)
	require.NoError(t, err)
	require.Len(t, photos.Media, 1, "Media reuse stays visible through another valid placement")

	thumbnail, err := fixture.service.Thumbnail(ctx, fixture.actor, fixture.media[0], immich.MediaRequest{})
	require.NoError(t, err)
	require.NoError(t, thumbnail.Body.Close())
	assert.Equal(t, []uuid.UUID{fixture.assets[0]}, fixture.thumbnail.assets)
	_, err = fixture.service.Thumbnail(ctx, fixture.actor, uuid.New(), immich.MediaRequest{})
	assert.ErrorIs(t, err, ErrNotFound)
	assert.Len(t, fixture.thumbnail.assets, 1, "guessed identifiers never reach Immich")

	_, err = fixture.db.NewRaw(`INSERT INTO content_withdrawals
		(id, target_kind, target_id, withdrawn_by_person_id, withdrawn_at)
		VALUES (gen_random_uuid(), 'event', ?, ?, now())`, fixture.events[1], fixture.curator).Exec(ctx)
	require.NoError(t, err)
	photos, err = fixture.service.Photos(ctx, fixture.actor, "10", "", false)
	require.NoError(t, err)
	assert.Empty(t, photos.Media)
	newForYou, err := fixture.service.NewForYou(ctx, fixture.actor)
	require.NoError(t, err)
	assert.Empty(t, newForYou.Events, "stale unseen rows cannot reveal withdrawn Events")

	var securityEpoch []byte
	require.NoError(t, fixture.db.NewRaw(`SELECT security_epoch FROM sessions WHERE id = ?`, fixture.actor.SessionID).Scan(ctx, &securityEpoch))
	_, err = fixture.db.NewRaw(`UPDATE system_settings SET security_epoch = decode(repeat('99', 32), 'hex') WHERE id = 1`).Exec(ctx)
	require.NoError(t, err)
	_, err = fixture.service.Events(ctx, fixture.actor, "10", "")
	assert.ErrorIs(t, err, ErrNotFound, "the Session security epoch is revalidated inside the content read")
	_, err = fixture.db.NewRaw(`UPDATE system_settings SET security_epoch = ? WHERE id = 1; UPDATE recipient_access_generations SET state = 'suspended' WHERE id = ?`, securityEpoch, fixture.actor.AccessID).Exec(ctx)
	require.NoError(t, err)
	_, err = fixture.service.Events(ctx, fixture.actor, "10", "")
	assert.ErrorIs(t, err, ErrNotFound, "the current access generation state is revalidated inside the content read")
}

func TestMediaAccessFailsClosedOnRecoveryHoldWithoutEpochRotation(t *testing.T) {
	fixture := newLibraryFixture(t)
	ctx := context.Background()

	require.NoError(t, mediaaccess.Require(ctx, fixture.db, fixture.actor, fixture.media[0]))
	allowed, err := mediaaccess.GenerationCanAccess(ctx, fixture.db, fixture.actor.AccessID, fixture.media[0])
	require.NoError(t, err)
	require.True(t, allowed)

	_, err = fixture.db.NewRaw(`UPDATE system_settings SET recovery_hold = true,
		recovery_nonce_hash = decode(repeat('ab', 32), 'hex'), recovery_started_at = now() WHERE id = 1`).Exec(ctx)
	require.NoError(t, err)
	assert.ErrorIs(t, mediaaccess.Require(ctx, fixture.db, fixture.actor, fixture.media[0]), mediaaccess.ErrNotFound)
	allowed, err = mediaaccess.GenerationCanAccess(ctx, fixture.db, fixture.actor.AccessID, fixture.media[0])
	require.NoError(t, err)
	assert.False(t, allowed)

	tx, err := fixture.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	assert.ErrorIs(t, mediaaccess.RequireForMutation(ctx, tx, fixture.actor, fixture.media[0]), mediaaccess.ErrNotFound)
}

type productionMatrixSessionType string
type productionMatrixValidity string
type productionMatrixEpoch string
type productionMatrixRecipientState string
type productionMatrixGeneration string
type productionMatrixEntitlement string
type productionMatrixPlacement string
type productionMatrixWithdrawal string
type productionMatrixSource string

const (
	matrixTrustedSession     productionMatrixSessionType    = "trusted"
	matrixPublicSession      productionMatrixSessionType    = "public"
	matrixValidSession       productionMatrixValidity       = "valid"
	matrixExpiredSession     productionMatrixValidity       = "expired"
	matrixRevokedSession     productionMatrixValidity       = "revoked"
	matrixCurrentEpoch       productionMatrixEpoch          = "current"
	matrixStaleEpoch         productionMatrixEpoch          = "stale"
	matrixPendingAccess      productionMatrixRecipientState = "pending"
	matrixCompletedAccess    productionMatrixRecipientState = "completed"
	matrixSuspendedAccess    productionMatrixRecipientState = "suspended"
	matrixRevokedAccess      productionMatrixRecipientState = "revoked"
	matrixCurrentGeneration  productionMatrixGeneration     = "current"
	matrixStaleGeneration    productionMatrixGeneration     = "stale"
	matrixEntitled           productionMatrixEntitlement    = "entitled"
	matrixUnentitled         productionMatrixEntitlement    = "not_entitled"
	matrixSinglePlacement    productionMatrixPlacement      = "single"
	matrixReusedPlacement    productionMatrixPlacement      = "reused"
	matrixNoWithdrawal       productionMatrixWithdrawal     = "none"
	matrixPrimaryWithdrawal  productionMatrixWithdrawal     = "primary"
	matrixCompleteWithdrawal productionMatrixWithdrawal     = "all"
	matrixCurrentSource      productionMatrixSource         = "current"
	matrixMissingSource      productionMatrixSource         = "source_missing"
)

func TestRecipientAuthorizationCartesianMatrixUsesProductionQueries(t *testing.T) {
	fixture := newLibraryFixture(t)
	ctx := context.Background()
	commentService := commentsservice.New(fixture.db)
	favoriteService := favoritesservice.New(fixture.db, nil)
	searchService := searchservice.New(fixture.db)
	_, err := fixture.db.NewRaw(`UPDATE media_items SET media_type = 'video' WHERE id = ?;
		UPDATE published_media_placements SET media_type = 'video' WHERE media_item_id = ?;
		DELETE FROM current_audience_entitlements WHERE recipient_access_generation_id = ? AND media_item_id <> ?`,
		fixture.media[0], fixture.media[0], fixture.actor.AccessID, fixture.media[0]).Exec(ctx)
	require.NoError(t, err)
	representations := []struct {
		name string
		open func(context.Context, setup.SessionActor, uuid.UUID, immich.MediaRequest) (immich.MediaResponse, error)
	}{
		{"thumbnail", fixture.service.Thumbnail},
		{"preview", fixture.service.Preview},
		{"video", fixture.service.Video},
		{"original", fixture.service.Original},
	}
	type authorizationState struct {
		sessionType    productionMatrixSessionType
		validity       productionMatrixValidity
		epoch          productionMatrixEpoch
		recipientState productionMatrixRecipientState
		generation     productionMatrixGeneration
		entitled       productionMatrixEntitlement
		placement      productionMatrixPlacement
		withdrawal     productionMatrixWithdrawal
		source         productionMatrixSource
	}
	caseNumber := 0
	for _, sessionType := range []productionMatrixSessionType{matrixTrustedSession, matrixPublicSession} {
		for _, validity := range []productionMatrixValidity{matrixValidSession, matrixExpiredSession, matrixRevokedSession} {
			for _, epoch := range []productionMatrixEpoch{matrixCurrentEpoch, matrixStaleEpoch} {
				for _, recipientState := range []productionMatrixRecipientState{matrixPendingAccess, matrixCompletedAccess, matrixSuspendedAccess, matrixRevokedAccess} {
					for _, generation := range []productionMatrixGeneration{matrixCurrentGeneration, matrixStaleGeneration} {
						for _, entitled := range []productionMatrixEntitlement{matrixEntitled, matrixUnentitled} {
							for _, placement := range []productionMatrixPlacement{matrixSinglePlacement, matrixReusedPlacement} {
								for _, withdrawal := range []productionMatrixWithdrawal{matrixNoWithdrawal, matrixPrimaryWithdrawal, matrixCompleteWithdrawal} {
									for _, source := range []productionMatrixSource{matrixCurrentSource, matrixMissingSource} {
										state := authorizationState{sessionType, validity, epoch, recipientState, generation, entitled, placement, withdrawal, source}
										caseNumber++
										isCompleted := recipientState == matrixCompletedAccess
										hasCompletedOnboarding := isCompleted || recipientState == matrixSuspendedAccess || recipientState == matrixRevokedAccess
										_, err := fixture.db.NewRaw(`
											UPDATE system_settings SET security_epoch = decode(repeat('42', 32), 'hex'), recovery_hold = false WHERE id = 1;
											UPDATE recipient_access_generations SET state = ?, is_current = ?,
												onboarding_completed_at = CASE WHEN ? THEN now() ELSE NULL END,
												ended_at = CASE WHEN ? = 'revoked' THEN now() ELSE NULL END
											WHERE id = ?;
											UPDATE sessions SET session_type = ?, revoked_at = CASE WHEN ? = 'revoked' THEN now() ELSE NULL END,
												security_epoch = decode(repeat(CASE WHEN ? = 'current' THEN '42' ELSE '43' END, 32), 'hex'),
												idle_expires_at = CASE WHEN ? = 'trusted' THEN now() + CASE WHEN ? = 'expired' THEN interval '-1 hour' ELSE interval '1 hour' END ELSE NULL END,
												absolute_expires_at = CASE WHEN ? = 'public' THEN now() + CASE WHEN ? = 'expired' THEN interval '-1 hour' ELSE interval '1 hour' END ELSE NULL END
											WHERE id = ?;
											DELETE FROM current_audience_entitlements WHERE recipient_access_generation_id = ? AND media_item_id = ?;
											INSERT INTO current_audience_entitlements
												(event_id, publication_id, recipient_person_id, recipient_access_generation_id, media_item_id)
											SELECT ?, ?, person_id, ?, ? FROM recipient_access_generations WHERE id = ? AND ? = 'entitled';
											INSERT INTO current_audience_entitlements
												(event_id, publication_id, recipient_person_id, recipient_access_generation_id, media_item_id)
											SELECT ?, ?, person_id, ?, ? FROM recipient_access_generations
											WHERE id = ? AND ? = 'entitled' AND ? = 'reused';
											DELETE FROM content_withdrawals WHERE target_id IN (?, ?, ?);
											INSERT INTO content_withdrawals (id, target_kind, target_id, withdrawn_by_person_id, withdrawn_at)
											SELECT gen_random_uuid(), 'event', ?, ?, now() WHERE ? = 'primary';
											INSERT INTO content_withdrawals (id, target_kind, target_id, withdrawn_by_person_id, withdrawn_at)
											SELECT gen_random_uuid(), 'media', ?, ?, now() WHERE ? = 'all';
											UPDATE media_items SET availability = ?, missing_since = CASE WHEN ? = 'source_missing' THEN now() ELSE NULL END WHERE id = ?
										`, recipientState, generation == matrixCurrentGeneration, hasCompletedOnboarding, recipientState, fixture.actor.AccessID,
											sessionType, validity, epoch, sessionType, validity, sessionType, validity, fixture.actor.SessionID,
											fixture.actor.AccessID, fixture.media[0],
											fixture.events[0], fixture.publications[0], fixture.actor.AccessID, fixture.media[0], fixture.actor.AccessID, entitled,
											fixture.events[1], fixture.publications[1], fixture.actor.AccessID, fixture.media[0], fixture.actor.AccessID, entitled, placement,
											fixture.events[0], fixture.events[1], fixture.media[0],
											fixture.events[0], fixture.curator, withdrawal,
											fixture.media[0], fixture.curator, withdrawal,
											source, source, fixture.media[0]).Exec(ctx)
										require.NoErrorf(t, err, "case %d: %+v", caseNumber, state)

										sessionUsable := validity == matrixValidSession && epoch == matrixCurrentEpoch
										generationUsable := isCompleted && generation == matrixCurrentGeneration
										livePlacement := entitled == matrixEntitled && withdrawal != matrixCompleteWithdrawal && (withdrawal == matrixNoWithdrawal || placement == matrixReusedPlacement)
										metadataVisible := sessionUsable && generationUsable && livePlacement
										accessErr := mediaaccess.Require(ctx, fixture.db, fixture.actor, fixture.media[0])
										if metadataVisible {
											require.NoErrorf(t, accessErr, "case %d: %+v", caseNumber, state)
										} else {
											require.ErrorIsf(t, accessErr, mediaaccess.ErrNotFound, "case %d: %+v", caseNumber, state)
										}

										photos, photosErr := fixture.service.Photos(ctx, fixture.actor, "10", "", false)
										events, eventsErr := fixture.service.Events(ctx, fixture.actor, "10", "")
										newForYou, newForYouErr := fixture.service.NewForYou(ctx, fixture.actor)
										targetEventIndex := 0
										if placement == matrixReusedPlacement {
											targetEventIndex = 1
										}
										event, eventErr := fixture.service.Event(ctx, fixture.actor, fixture.events[targetEventIndex], "10", "")
										_, searchErr := searchService.Search(ctx, fixture.actor, searchservice.Request{Query: "Event"})
										if sessionUsable && generationUsable {
											require.NoErrorf(t, photosErr, "case %d: %+v", caseNumber, state)
											require.NoErrorf(t, eventsErr, "case %d: %+v", caseNumber, state)
											require.NoErrorf(t, newForYouErr, "case %d: %+v", caseNumber, state)
											require.NoErrorf(t, searchErr, "case %d: %+v", caseNumber, state)
											photoVisible := slices.ContainsFunc(photos.Media, func(item Media) bool { return item.ID == fixture.media[0].String() })
											eventVisible := slices.ContainsFunc(events.Events, func(item EventSummary) bool { return item.ID == fixture.events[targetEventIndex].String() })
											newForYouVisible := slices.ContainsFunc(newForYou.Events, func(item EventSummary) bool { return item.ID == fixture.events[targetEventIndex].String() })
											assert.Equalf(t, metadataVisible, photoVisible, "case %d: %+v", caseNumber, state)
											assert.Equalf(t, metadataVisible, eventVisible, "case %d: %+v", caseNumber, state)
											assert.Equalf(t, metadataVisible, newForYouVisible, "case %d: %+v", caseNumber, state)
											if metadataVisible {
												require.NoErrorf(t, eventErr, "case %d: %+v", caseNumber, state)
												assert.Equal(t, fixture.events[targetEventIndex].String(), event.ID)
											} else {
												require.ErrorIsf(t, eventErr, ErrNotFound, "case %d: %+v", caseNumber, state)
											}
										} else {
											require.ErrorIsf(t, photosErr, ErrNotFound, "case %d: %+v", caseNumber, state)
											require.ErrorIsf(t, eventsErr, ErrNotFound, "case %d: %+v", caseNumber, state)
											require.ErrorIsf(t, newForYouErr, ErrNotFound, "case %d: %+v", caseNumber, state)
											require.ErrorIsf(t, eventErr, ErrNotFound, "case %d: %+v", caseNumber, state)
											require.ErrorIsf(t, searchErr, searchservice.ErrNotFound, "case %d: %+v", caseNumber, state)
										}

										_, commentErr := commentService.List(ctx, fixture.actor, fixture.media[0])
										_, favoriteErr := favoriteService.Get(ctx, fixture.actor, fixture.media[0])
										if metadataVisible {
											require.NoErrorf(t, commentErr, "case %d: %+v", caseNumber, state)
											require.NoErrorf(t, favoriteErr, "case %d: %+v", caseNumber, state)
										} else {
											require.ErrorIsf(t, commentErr, commentsservice.ErrNotFound, "case %d: %+v", caseNumber, state)
											require.ErrorIsf(t, favoriteErr, favoritesservice.ErrNotFound, "case %d: %+v", caseNumber, state)
										}

										for _, representation := range representations {
											fixture.thumbnail.assets = nil
											stream, streamErr := representation.open(ctx, fixture.actor, fixture.media[0], immich.MediaRequest{})
											if metadataVisible && source == matrixCurrentSource {
												require.NoErrorf(t, streamErr, "%s case %d: %+v", representation.name, caseNumber, state)
												require.NoError(t, stream.Body.Close())
												require.Len(t, fixture.thumbnail.assets, 1)
											} else {
												require.ErrorIsf(t, streamErr, ErrNotFound, "%s case %d: %+v", representation.name, caseNumber, state)
												assert.Empty(t, fixture.thumbnail.assets, "denied cases must not reach Immich")
											}
										}
										for _, deniedID := range []uuid.UUID{fixture.media[2], uuid.New()} {
											fixture.thumbnail.assets = nil
											_, deniedErr := fixture.service.Thumbnail(ctx, fixture.actor, deniedID, immich.MediaRequest{})
											require.ErrorIsf(t, deniedErr, ErrNotFound, "cross-Recipient and guessed identifiers must not enumerate case %d: %+v", caseNumber, state)
											assert.Empty(t, fixture.thumbnail.assets, "denied identifiers must not reach Immich")
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
	require.Equal(t, 2304, caseNumber)
}

func FuzzProductionAuthorizationTransitions(f *testing.F) {
	f.Add([]byte{0, 2, 4, 6, 8, 10, 12})
	f.Add([]byte{12, 13, 8, 9, 6, 7, 4, 5, 2, 3, 0, 1})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, transitions []byte) {
		if len(transitions) > 128 {
			t.Skip()
		}
		fixture := newLibraryFixture(t)
		ctx := context.Background()
		mediaID := fixture.media[1]
		notRevoked, notExpired, epochCurrent := true, true, true
		accessCompleted, entitled, withdrawn, sourceCurrent := true, true, false, true

		for index, rawTransition := range transitions {
			transition := rawTransition % 14
			var query string
			var args []any
			switch transition {
			case 0:
				query = `UPDATE sessions SET revoked_at = now() WHERE id = ?`
				args = []any{fixture.actor.SessionID}
				notRevoked = false
			case 1:
				query = `UPDATE sessions SET revoked_at = NULL WHERE id = ?`
				args = []any{fixture.actor.SessionID}
				notRevoked = true
			case 2:
				query = `UPDATE sessions SET security_epoch = decode(repeat('43', 32), 'hex') WHERE id = ?`
				args = []any{fixture.actor.SessionID}
				epochCurrent = false
			case 3:
				query = `UPDATE sessions SET security_epoch = settings.security_epoch FROM system_settings AS settings WHERE sessions.id = ? AND settings.id = 1`
				args = []any{fixture.actor.SessionID}
				epochCurrent = true
			case 4:
				query = `UPDATE recipient_access_generations SET state = 'suspended' WHERE id = ?`
				args = []any{fixture.actor.AccessID}
				accessCompleted = false
			case 5:
				query = `UPDATE recipient_access_generations SET state = 'completed', is_current = true, onboarding_completed_at = now(), ended_at = NULL WHERE id = ?`
				args = []any{fixture.actor.AccessID}
				accessCompleted = true
			case 6:
				query = `DELETE FROM current_audience_entitlements WHERE recipient_access_generation_id = ? AND media_item_id = ?`
				args = []any{fixture.actor.AccessID, mediaID}
				entitled = false
			case 7:
				query = `INSERT INTO current_audience_entitlements
					(event_id, publication_id, recipient_person_id, recipient_access_generation_id, media_item_id)
					SELECT ?, ?, person_id, ?, ? FROM recipient_access_generations WHERE id = ? ON CONFLICT DO NOTHING`
				args = []any{fixture.events[0], fixture.publications[0], fixture.actor.AccessID, mediaID, fixture.actor.AccessID}
				entitled = true
			case 8:
				query = `INSERT INTO content_withdrawals (id, target_kind, target_id, withdrawn_by_person_id, withdrawn_at)
					VALUES (gen_random_uuid(), 'media', ?, ?, now()) ON CONFLICT DO NOTHING`
				args = []any{mediaID, fixture.curator}
				withdrawn = true
			case 9:
				query = `DELETE FROM content_withdrawals WHERE target_kind = 'media' AND target_id = ?`
				args = []any{mediaID}
				withdrawn = false
			case 10:
				query = `UPDATE media_items SET availability = 'source_missing', missing_since = now() WHERE id = ?`
				args = []any{mediaID}
				sourceCurrent = false
			case 11:
				query = `UPDATE media_items SET availability = 'current', missing_since = NULL WHERE id = ?`
				args = []any{mediaID}
				sourceCurrent = true
			case 12:
				query = `UPDATE sessions SET idle_expires_at = now() - interval '1 hour' WHERE id = ?`
				args = []any{fixture.actor.SessionID}
				notExpired = false
			case 13:
				query = `UPDATE sessions SET idle_expires_at = now() + interval '1 hour' WHERE id = ?`
				args = []any{fixture.actor.SessionID}
				notExpired = true
			}

			updateDone := make(chan error, 1)
			go func() {
				_, err := fixture.db.NewRaw(query, args...).Exec(ctx)
				updateDone <- err
			}()
			concurrentErr := mediaaccess.Require(ctx, fixture.db, fixture.actor, mediaID)
			require.Truef(t, concurrentErr == nil || errors.Is(concurrentErr, mediaaccess.ErrNotFound),
				"transition %d returned an unsafe intermediate error: %v", index, concurrentErr)
			require.NoErrorf(t, <-updateDone, "transition %d", index)

			metadataVisible := notRevoked && notExpired && epochCurrent && accessCompleted && entitled && !withdrawn
			finalErr := mediaaccess.Require(ctx, fixture.db, fixture.actor, mediaID)
			if metadataVisible {
				require.NoErrorf(t, finalErr, "transition %d", index)
			} else {
				require.ErrorIsf(t, finalErr, mediaaccess.ErrNotFound, "transition %d", index)
			}
			fixture.thumbnail.assets = nil
			stream, streamErr := fixture.service.Thumbnail(ctx, fixture.actor, mediaID, immich.MediaRequest{})
			if metadataVisible && sourceCurrent {
				require.NoErrorf(t, streamErr, "transition %d", index)
				require.NoError(t, stream.Body.Close())
				require.Len(t, fixture.thumbnail.assets, 1)
			} else {
				require.ErrorIsf(t, streamErr, ErrNotFound, "transition %d", index)
				assert.Empty(t, fixture.thumbnail.assets, "denied transitions must not reach Immich")
			}
		}
	})
}
