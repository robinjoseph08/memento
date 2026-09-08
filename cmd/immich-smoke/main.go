// immich-smoke exercises the production adapter and import against disposable services.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/cmd/immich-smoke/fixture"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/media"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/uptrace/bun"
)

func parseRelease(args []string) (string, error) {
	flags := flag.NewFlagSet("immich-smoke", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	release := flags.String("version", fixture.Release, "exact stable Immich release tag")
	if err := flags.Parse(args); err != nil {
		return "", err
	}
	if flags.NArg() != 0 || !regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`).MatchString(*release) {
		return "", fmt.Errorf("version must be an exact stable tag such as v3.1.0")
	}
	return *release, nil
}

func main() {
	release, err := parseRelease(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	if err := run(ctx, release); err != nil {
		fmt.Fprintln(os.Stderr, "Immich smoke failed:", err)
		os.Exit(1)
	}
	fmt.Printf("PASS Immich %s: EXIF local dates, midnight, ties, shared media, generated thumbnails through production HTTP media routes, and unchanged source albums\n", release)
}

func run(ctx context.Context, release string) (returnErr error) {
	baseURL, databaseURL := os.Getenv("MEMENTO_SMOKE_IMMICH_URL"), os.Getenv("MEMENTO_SMOKE_DATABASE_URL")
	if os.Getenv("MEMENTO_SMOKE_DISPOSABLE") != "1" || baseURL == "" || databaseURL == "" {
		return fmt.Errorf("run mise test:immich to provision disposable services")
	}
	library, err := fixture.Setup(ctx, baseURL, release)
	if err != nil {
		return err
	}
	source := library.Source()
	if err := source.CheckImport(ctx); err != nil {
		return err
	}
	fmt.Printf("Immich %s: non-admin fixture key grants only album.read, asset.read, asset.view\n", release)
	readyCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if err := waitForMedia(readyCtx, source, library.Assets); err != nil {
		return err
	}
	before, err := snapshot(ctx, source, library.Albums)
	if err != nil {
		return err
	}

	schema, err := testdb.NewSchema(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		returnErr = errors.Join(returnErr, schema.Close(cleanupCtx))
	}()
	db, err := testdb.Open(schema.URL)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, db.Close()) }()
	if _, err := migrations.BringUpToDate(ctx, db); err != nil {
		return err
	}
	module := publishing.New(db, source, func(context.Context, bun.Tx, string) error { return nil })
	// This in-process HTTP check bypasses sign-in, not the production media module.
	// It has no listening socket and can only reach this invocation's temporary schema.
	handler := echo.New()
	handler.HTTPErrorHandler = errcodes.NewHandler().Handle
	media.RegisterRoutes(handler, media.New(db, source), func(next echo.HandlerFunc) echo.HandlerFunc { return next })
	mediaIDs := map[string]string{}
	entryIDs := map[string]bool{}
	for _, album := range library.Albums {
		pending, err := module.StartImport(ctx, album.ID)
		if err != nil {
			return err
		}
		if pending.Status != "queued" {
			return fmt.Errorf("new import was not queued")
		}
		if err := module.ExecuteImport(ctx, pending.ID); err != nil {
			return err
		}
		detail, err := module.GetAlbum(ctx, pending.ID)
		if err != nil {
			return err
		}
		if err := verifyAlbum(detail, album, library.Assets); err != nil {
			return err
		}
		for _, moment := range detail.Moments {
			for _, entry := range moment.Entries {
				if err := checkMediaEndpoint(ctx, handler, entry.ThumbnailURL); err != nil {
					return err
				}
				if entryIDs[entry.ID] {
					return fmt.Errorf("albums reused an Album Entry identity")
				}
				entryIDs[entry.ID] = true
				if previous, ok := mediaIDs[entry.Filename]; ok && previous != entry.MediaID {
					return fmt.Errorf("overlapping albums did not reuse the Media Item")
				}
				mediaIDs[entry.Filename] = entry.MediaID
			}
		}
		reopened, err := module.StartImport(ctx, album.ID)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(detail, reopened) {
			return fmt.Errorf("reopening an import changed its reviewed Album")
		}
		fmt.Printf("Imported %q: %d Moments, %d entries, unpublished\n", detail.Title, len(detail.Moments), detail.Processed)
	}
	if len(mediaIDs) != len(library.Assets) || len(entryIDs) != 6 {
		return fmt.Errorf("shared media or album-entry counts differ")
	}
	after, err := snapshot(ctx, source, library.Albums)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(before, after) {
		return fmt.Errorf("source Immich title, description, or membership changed")
	}
	return nil
}

// Poll only the real external service. Ordinary unit tests do not wait for jobs.
func waitForMedia(ctx context.Context, source *immich.Client, assets []fixture.Asset) error {
	for _, expected := range assets {
		var lastErr error
		var previous *immich.Asset
		for {
			actual, err := source.GetAsset(ctx, expected.ID)
			if err == nil {
				local, parseErr := time.Parse(time.RFC3339Nano, actual.LocalDateTime)
				expectedTime, _ := time.Parse(time.RFC3339, expected.CapturedAt)
				instant, instantErr := time.Parse(time.RFC3339Nano, actual.FileCreatedAt)
				if parseErr != nil || instantErr != nil || local.Format("2006-01-02T15:04:05") != expectedTime.Format("2006-01-02T15:04:05") || !instant.Equal(expectedTime) {
					err = fmt.Errorf("EXIF metadata not ready for %s: local=%s instant=%s", expected.Filename, actual.LocalDateTime, actual.FileCreatedAt)
				} else if actual.Thumbhash == nil || *actual.Thumbhash == "" {
					err = fmt.Errorf("generated thumbhash not ready for %s", expected.Filename)
				} else {
					err = checkThumbnail(ctx, source, expected.ID)
				}
			}
			if err == nil {
				if previous != nil && reflect.DeepEqual(actual, *previous) {
					break
				}
				previous = &actual
				err = fmt.Errorf("waiting for stable metadata and thumbhash for %s", expected.Filename)
			} else {
				previous = nil
			}
			lastErr = err
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return fmt.Errorf("wait for generated media: %w; last result: %w", ctx.Err(), lastErr)
			case <-timer.C:
			}
		}
	}
	return nil
}

func checkThumbnail(ctx context.Context, source *immich.Client, id string) error {
	thumbnail, err := source.Thumbnail(ctx, id)
	if err != nil {
		return err
	}
	defer func() { _ = thumbnail.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(thumbnail.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("read generated thumbnail")
	}
	if len(data) == 0 || len(data) == 4<<20 || !strings.HasPrefix(thumbnail.ContentType, "image/") || !strings.HasPrefix(http.DetectContentType(data), "image/") {
		return fmt.Errorf("generated thumbnail did not contain an image")
	}
	return nil
}

type albumSnapshot struct {
	ID          string
	Name        string
	Description string
	Members     []string
}

func snapshot(ctx context.Context, source *immich.Client, expected []fixture.Album) ([]albumSnapshot, error) {
	albums, err := source.ListAlbums(ctx)
	if err != nil {
		return nil, err
	}
	if len(albums) != len(expected) {
		return nil, fmt.Errorf("fixture source listing has unexpected album count")
	}
	snapshots := []albumSnapshot{}
	for _, want := range expected {
		album, err := source.GetAlbum(ctx, want.ID)
		if err != nil {
			return nil, err
		}
		got := albumSnapshot{ID: album.ID, Name: album.Name, Description: album.Description}
		for page := 1; page != 0; {
			members, next, err := source.ListMembers(ctx, want.ID, page)
			if err != nil {
				return nil, err
			}
			if next != 0 && next <= page {
				return nil, fmt.Errorf("membership paging did not advance")
			}
			for _, member := range members {
				got.Members = append(got.Members, member.ID)
			}
			page = next
		}
		slices.Sort(got.Members)
		ids := slices.Clone(want.AssetIDs)
		slices.Sort(ids)
		if got.Name != want.Name || got.Description != want.Description || album.Count != len(ids) || !slices.Equal(got.Members, ids) {
			return nil, fmt.Errorf("source fixture album title, description, or membership differs")
		}
		snapshots = append(snapshots, got)
	}
	return snapshots, nil
}

func verifyAlbum(detail publishing.AlbumDetail, album fixture.Album, assets []fixture.Asset) error {
	if detail.Status != "complete" || detail.Published || detail.Title != album.Name || detail.Description != album.Description || detail.Processed != len(album.AssetIDs) || detail.Total != len(album.AssetIDs) {
		return fmt.Errorf("imported Album metadata or completion differs")
	}
	expected := []fixture.Asset{}
	for _, asset := range assets {
		if slices.Contains(album.AssetIDs, asset.ID) {
			expected = append(expected, asset)
		}
	}
	slices.SortFunc(expected, func(a, b fixture.Asset) int {
		// All fixtures use the same offset; compare wall-clock components first.
		if order := strings.Compare(a.CapturedAt[:19], b.CapturedAt[:19]); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	count := 0
	lastDate := ""
	for _, moment := range detail.Moments {
		if moment.Date <= lastDate || len(moment.Entries) == 0 {
			return fmt.Errorf("imported Moment dates are unordered or empty")
		}
		lastDate = moment.Date
		if moment.CoverEntryID != moment.Entries[0].ID {
			return fmt.Errorf("imported Moment cover is not its earliest entry")
		}
		for _, entry := range moment.Entries {
			if count >= len(expected) {
				return fmt.Errorf("import has extra entries")
			}
			want := expected[count]
			if entry.Filename != want.Filename || moment.Date != want.CapturedAt[:10] || entry.CapturedAt != want.CapturedAt[:19] || !entry.Available || entry.Kind != "IMAGE" || !strings.HasPrefix(entry.ThumbnailURL, "/api/media/") {
				return fmt.Errorf("capture-local Moment grouping or deterministic entry order differs for %s", want.Filename)
			}
			count++
		}
	}
	if count != len(expected) {
		return fmt.Errorf("import lost entries")
	}
	return nil
}
