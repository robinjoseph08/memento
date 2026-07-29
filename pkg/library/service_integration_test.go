//go:build integration

package library

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/migrations"
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "bytes=not-a-range", r.Header.Get("Range"))
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
