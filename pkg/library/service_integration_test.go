//go:build integration

package library

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type thumbnailStub struct {
	assets []uuid.UUID
}

func (stub *thumbnailStub) Thumbnail(_ context.Context, assetID uuid.UUID) (immich.MediaResponse, error) {
	stub.assets = append(stub.assets, assetID)
	return immich.MediaResponse{
		Body: io.NopCloser(bytes.NewBufferString("thumbnail")), ContentType: "image/webp", ContentLength: 9,
	}, nil
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
		INSERT INTO current_audience_entitlements
			(event_id, publication_id, recipient_person_id, recipient_access_generation_id, media_item_id)
		SELECT ?, ?, person_id, ?, ? FROM recipient_access_generations WHERE id = ?
	`, fixture.moments[eventIndex], position, mediaID,
		fixture.events[eventIndex], fixture.publications[eventIndex], fixture.moments[eventIndex], mediaID, position,
		fixture.events[eventIndex], fixture.publications[eventIndex], accessID, mediaID, accessID).Exec(context.Background())
	require.NoError(t, err)
}

func TestRecipientLibraryPaginatesOnlyCurrentAuthorizedUnion(t *testing.T) {
	fixture := newLibraryFixture(t)
	ctx := context.Background()
	_, err := fixture.db.NewRaw(`UPDATE published_moments SET cover_media_item_id = ? WHERE id = ?`, fixture.media[1], fixture.moments[0]).Exec(ctx)
	require.NoError(t, err)
	first, err := fixture.service.Photos(ctx, fixture.actor, "1", "", false)
	require.NoError(t, err)
	require.Len(t, first.Media, 1)
	assert.Equal(t, fixture.media[1].String(), first.Media[0].ID)
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
			name: "Media", method: http.MethodGet,
			unauthorizedPath: "/api/me/media/" + fixture.media[2].String() + "/thumbnail",
			missingPath:      "/api/me/media/" + missingID + "/thumbnail",
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

func TestThumbnailRevalidatesEveryAuthorizationBoundaryBeforeImmich(t *testing.T) {
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
			reason: "a revoked Session cannot authorize a thumbnail",
		},
		{
			name: "expired Session",
			deny: func(ctx context.Context, fixture libraryFixture) error {
				_, err := fixture.db.NewRaw(`UPDATE sessions SET idle_expires_at = now() - interval '1 second' WHERE id = ?`, fixture.actor.SessionID).Exec(ctx)
				return err
			},
			reason: "an expired Session cannot authorize a thumbnail",
		},
		{
			name: "stale security epoch",
			deny: func(ctx context.Context, fixture libraryFixture) error {
				_, err := fixture.db.NewRaw(`UPDATE system_settings SET security_epoch = decode(repeat('99', 32), 'hex') WHERE id = 1`).Exec(ctx)
				return err
			},
			reason: "a Session from a stale security epoch cannot authorize a thumbnail",
		},
		{
			name: "non-current access generation",
			deny: func(ctx context.Context, fixture libraryFixture) error {
				_, err := fixture.db.NewRaw(`UPDATE recipient_access_generations SET is_current = false WHERE id = ?`, fixture.actor.AccessID).Exec(ctx)
				return err
			},
			reason: "a non-current access generation cannot authorize a thumbnail",
		},
		{
			name: "suspended access generation",
			deny: func(ctx context.Context, fixture libraryFixture) error {
				_, err := fixture.db.NewRaw(`UPDATE recipient_access_generations SET state = 'suspended' WHERE id = ?`, fixture.actor.AccessID).Exec(ctx)
				return err
			},
			reason: "a suspended access generation cannot authorize a thumbnail",
		},
		{
			name: "missing Audience entitlement",
			deny: func(ctx context.Context, fixture libraryFixture) error {
				_, err := fixture.db.NewRaw(`DELETE FROM current_audience_entitlements
					WHERE recipient_access_generation_id = ? AND media_item_id = ?`, fixture.actor.AccessID, fixture.media[0]).Exec(ctx)
				return err
			},
			reason: "Media without a current Audience entitlement cannot authorize a thumbnail",
		},
		{
			name: "withdrawn Media",
			deny: func(ctx context.Context, fixture libraryFixture) error {
				_, err := fixture.db.NewRaw(`INSERT INTO content_withdrawals
					(id, target_kind, target_id, withdrawn_by_person_id, withdrawn_at)
					VALUES (gen_random_uuid(), 'media', ?, ?, now())`, fixture.media[0], fixture.curator).Exec(ctx)
				return err
			},
			reason: "withdrawn Media cannot authorize a thumbnail",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLibraryFixture(t)
			ctx := context.Background()
			require.NoError(t, test.deny(ctx, fixture))

			_, err := fixture.service.Thumbnail(ctx, fixture.actor, fixture.media[0])
			assert.ErrorIs(t, err, ErrNotFound, test.reason)
			assert.Empty(t, fixture.thumbnail.assets, "a denied thumbnail request never reaches Immich")
		})
	}
}

func TestRecipientAuthorizationMatrixRevalidatesReuseWithdrawalAndAvailability(t *testing.T) {
	fixture := newLibraryFixture(t)
	ctx := context.Background()
	_, err := fixture.db.NewRaw(`UPDATE media_items SET availability = 'source_missing' WHERE id = ?`, fixture.media[1]).Exec(ctx)
	require.NoError(t, err)
	photos, err := fixture.service.Photos(ctx, fixture.actor, "10", "", false)
	require.NoError(t, err)
	require.Len(t, photos.Media, 2, "source-missing authorized placements remain visible as unavailable")
	assert.False(t, photos.Media[0].Available)
	assert.Empty(t, photos.Media[0].ThumbnailURL)
	_, err = fixture.service.Thumbnail(ctx, fixture.actor, fixture.media[1])
	assert.ErrorIs(t, err, ErrNotFound, "source-missing Media cannot reach Immich")
	assert.Empty(t, fixture.thumbnail.assets)

	_, err = fixture.db.NewRaw(`INSERT INTO content_withdrawals
		(id, target_kind, target_id, withdrawn_by_person_id, withdrawn_at)
		VALUES (gen_random_uuid(), 'event', ?, ?, now())`, fixture.events[0], fixture.curator).Exec(ctx)
	require.NoError(t, err)
	photos, err = fixture.service.Photos(ctx, fixture.actor, "10", "", false)
	require.NoError(t, err)
	require.Len(t, photos.Media, 1, "Media reuse stays visible through another valid placement")

	thumbnail, err := fixture.service.Thumbnail(ctx, fixture.actor, fixture.media[0])
	require.NoError(t, err)
	require.NoError(t, thumbnail.Body.Close())
	assert.Equal(t, []uuid.UUID{fixture.assets[0]}, fixture.thumbnail.assets)
	_, err = fixture.service.Thumbnail(ctx, fixture.actor, uuid.New())
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
