// immich-smoke exercises the production adapter and import against disposable services.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
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
	"github.com/robinjoseph08/memento/pkg/ffprobe"
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
	fmt.Printf("PASS Immich %s: manual faces, local dates, scoped access, preview and viewer thumbnail bytes, original downloads, ranged video playback, original-file range probing with bundled ffprobe chapters, publish/unpublish, shared Album deletion, and unchanged source albums\n", release)
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
	source.Probe = ffprobe.Command{}
	if err := source.CheckImport(ctx); err != nil {
		return err
	}
	fmt.Printf("Immich %s: non-admin fixture key grants only album.read, asset.download, asset.read, asset.view, face.read, person.read\n", release)
	mediaCtx, cancelMedia := context.WithTimeout(ctx, 3*time.Minute)
	if err := waitForMedia(mediaCtx, source, library.Assets); err != nil {
		cancelMedia()
		return err
	}
	cancelMedia()
	personCtx, cancelPerson := context.WithTimeout(ctx, 3*time.Minute)
	defer cancelPerson()
	if err := waitForPerson(personCtx, source, library.Person, library.Assets); err != nil {
		return err
	}
	videoCtx, cancelVideo := context.WithTimeout(ctx, 3*time.Minute)
	defer cancelVideo()
	if err := waitForVideo(videoCtx, source, library.Video); err != nil {
		return err
	}
	// Chapter extraction needs byte ranges on the original-file endpoint. Ask
	// for them directly so a release that streams whole files is caught here.
	status, contentRange, rangeBody, err := library.OriginalRange(ctx, library.Video.ID)
	if err != nil {
		return err
	}
	if status != http.StatusPartialContent || contentRange != fmt.Sprintf("bytes 0-1/%d", len(library.Video.Bytes)) || !bytes.Equal(rangeBody, library.Video.Bytes[:2]) {
		return fmt.Errorf("the Immich %s original-file endpoint did not honor a byte range: HTTP %d %q", release, status, contentRange)
	}
	fmt.Printf("Immich %s: original-file endpoint answers Range: bytes=0-1 with 206 %s, so ffprobe can read headers without a whole-original fallback\n", release, contentRange)
	sourceAlbums := append(append([]fixture.Album{}, library.Albums...), library.VideoAlbum)
	before, err := snapshot(ctx, source, sourceAlbums)
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
	enqueued := 0
	module := publishing.New(db, source, func(context.Context, bun.Tx, string) error {
		enqueued++
		return nil
	})
	delivery := media.New(db, source)
	var chapterTasks []string
	delivery.EnqueueChapters = func(_ context.Context, tx bun.Tx, mediaItemID, checksum string) error {
		if tx.Tx == nil {
			return fmt.Errorf("chapter extraction was requested outside the import transaction")
		}
		chapterTasks = append(chapterTasks, mediaItemID+":"+checksum)
		return nil
	}
	module.Chapters = delivery
	var imported []publishing.AlbumDetail
	// This in-process HTTP check bypasses sign-in, not the production media module.
	// It has no listening socket and can only reach this invocation's temporary schema.
	handler := echo.New()
	handler.HTTPErrorHandler = errcodes.NewHandler().Handle
	passthrough := func(next echo.HandlerFunc) echo.HandlerFunc { return next }
	media.RegisterRoutes(handler, media.New(db, source), passthrough, passthrough)
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
		summaries, err := module.ListAlbums(ctx, album.Name)
		if err != nil {
			return err
		}
		index := slices.IndexFunc(summaries, func(summary publishing.Album) bool { return summary.ID == detail.ID })
		if index < 0 || summaries[index] != detail.Album {
			return fmt.Errorf("album listing summary differs from imported detail header")
		}
		if err := checkMediaEndpoint(ctx, handler, detail.CoverURL); err != nil {
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
		imported = append(imported, detail)
		fmt.Printf("Imported %q: %d Moments, %d entries, unpublished\n", detail.Title, len(detail.Moments), detail.Processed)
	}
	if len(mediaIDs) != len(library.Assets) || len(entryIDs) != 6 {
		return fmt.Errorf("shared media or album-entry counts differ")
	}
	if enqueued != 2 {
		return fmt.Errorf("two imports did not enqueue exactly two tasks")
	}
	if len(chapterTasks) != 0 {
		return fmt.Errorf("photo imports requested chapter extraction")
	}
	videoAlbum, err := importVideoAlbum(ctx, module, delivery, library, &chapterTasks)
	if err != nil {
		return fmt.Errorf("video import and chapter extraction through production Immich adapter: %w", err)
	}
	if enqueued != 3 {
		return fmt.Errorf("three imports did not enqueue exactly three import tasks")
	}
	if err := verifyPublishing(ctx, db, module, delivery, imported, library.Assets); err != nil {
		return fmt.Errorf("publishing through production Immich adapter: %w", err)
	}
	if err := verifyVideoPublishing(ctx, db, module, delivery, videoAlbum, library.Video); err != nil {
		return fmt.Errorf("video playback through production Immich adapter: %w", err)
	}
	if enqueued != 3 || len(chapterTasks) != 1 {
		return fmt.Errorf("publication, unpublication, or deletion enqueued unexpected durable work")
	}
	if err := verifySynchronization(ctx, module, library, imported[1], &chapterTasks); err != nil {
		return fmt.Errorf("synchronization through production Immich adapter: %w", err)
	}
	if enqueued != 3 || len(chapterTasks) != 1 {
		return fmt.Errorf("synchronization enqueued unexpected durable work")
	}
	after, err := snapshot(ctx, source, sourceAlbums)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(before, after) {
		return fmt.Errorf("source Immich title, description, cover, or membership changed")
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

// waitForVideo waits for Immich to finish reading the upload: a thumbhash
// proves the poster exists, and two identical reads prove metadata settled so
// the content version imported below still matches when media is served.
func waitForVideo(ctx context.Context, source *immich.Client, video fixture.Video) error {
	var lastErr error
	var previous *immich.Asset
	for {
		actual, err := source.GetAsset(ctx, video.ID)
		if err == nil {
			switch {
			case actual.Kind != "VIDEO":
				err = fmt.Errorf("upload %s was not recognized as a video", video.Filename)
			case actual.Thumbhash == nil || *actual.Thumbhash == "":
				err = fmt.Errorf("generated poster not ready for %s", video.Filename)
			case previous != nil && reflect.DeepEqual(actual, *previous):
				return nil
			default:
				previous = &actual
				err = fmt.Errorf("waiting for stable video metadata for %s", video.Filename)
			}
		}
		lastErr = err
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait for video metadata: %w; last result: %w", ctx.Err(), lastErr)
		case <-timer.C:
		}
	}
}

// importVideoAlbum imports the video album, then runs the committed chapter
// task through the production adapter and the bundled ffprobe.
func importVideoAlbum(ctx context.Context, module *publishing.Module, delivery *media.Module, library *fixture.Library, chapterTasks *[]string) (publishing.AlbumDetail, error) {
	pending, err := module.StartImport(ctx, library.VideoAlbum.ID)
	if err != nil {
		return pending, err
	}
	if err := module.ExecuteImport(ctx, pending.ID); err != nil {
		return pending, err
	}
	detail, err := module.GetAlbum(ctx, pending.ID)
	if err != nil {
		return detail, err
	}
	if detail.Status != "complete" || detail.VideoCount != 1 || detail.PhotoCount != 0 || len(detail.Moments) != 1 || len(detail.Moments[0].Entries) != 1 {
		return detail, fmt.Errorf("video album import differs from its single-video source")
	}
	entry := detail.Moments[0].Entries[0]
	if entry.Kind != "VIDEO" || entry.Filename != library.Video.Filename || !entry.Available || entry.ChapterStatus != "pending" || entry.Title != "" {
		return detail, fmt.Errorf("imported video entry differs: %+v", entry)
	}
	if err := checkMediaEndpoint(ctx, curatorMediaHandler(delivery), entry.ThumbnailURL); err != nil {
		return detail, fmt.Errorf("video poster: %w", err)
	}
	if len(*chapterTasks) != 1 || !strings.HasPrefix((*chapterTasks)[0], entry.MediaID+":") {
		return detail, fmt.Errorf("import did not commit exactly one chapter task for the video")
	}
	checksum := strings.TrimPrefix((*chapterTasks)[0], entry.MediaID+":")
	if err := delivery.ExtractChapters(ctx, entry.MediaID, checksum, true); err != nil {
		return detail, fmt.Errorf("ffprobe against the original-file endpoint: %w", err)
	}
	detail, err = module.GetAlbum(ctx, pending.ID)
	if err != nil {
		return detail, err
	}
	entry = detail.Moments[0].Entries[0]
	titles := []string{}
	for _, chapter := range entry.Chapters {
		titles = append(titles, chapter.Title)
	}
	if entry.ChapterStatus != "complete" || !slices.Equal(titles, library.Video.Chapters) || entry.Chapters[0].Start != 0 || entry.Chapters[2].Start != 4 {
		return detail, fmt.Errorf("chapter extraction result differs: status %q chapters %+v", entry.ChapterStatus, entry.Chapters)
	}
	fmt.Printf("Imported %q: bundled ffprobe read %d chapters from the authenticated original through HTTP ranges\n", detail.Title, len(entry.Chapters))
	return detail, nil
}

func curatorMediaHandler(delivery *media.Module) http.Handler {
	handler := echo.New()
	handler.HTTPErrorHandler = errcodes.NewHandler().Handle
	passthrough := func(next echo.HandlerFunc) echo.HandlerFunc { return next }
	media.RegisterRoutes(handler, delivery, passthrough, passthrough)
	return handler
}

func waitForPerson(ctx context.Context, source *immich.Client, expected fixture.Person, assets []fixture.Asset) error {
	var lastErr error
	for {
		faces, err := source.ListFaces(ctx, expected.AssetID)
		if err == nil {
			err = verifyManualFace(faces, expected)
		}
		if err == nil {
			err = checkPersonThumbnail(ctx, source, expected.ID)
		}
		if err == nil {
			for _, asset := range assets {
				if asset.ID == expected.AssetID {
					continue
				}
				otherFaces, listErr := source.ListFaces(ctx, asset.ID)
				if listErr != nil {
					err = listErr
					break
				}
				if len(otherFaces) != 0 {
					err = fmt.Errorf("manual face appeared on another fixture asset")
					break
				}
			}
		}
		if err == nil {
			return nil
		}
		lastErr = err
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait for manual face and person thumbnail: %w; last result: %w", ctx.Err(), lastErr)
		case <-timer.C:
		}
	}
}

func verifyManualFace(faces []immich.Face, expected fixture.Person) error {
	if len(faces) != 1 {
		return fmt.Errorf("fixture asset does not have exactly one face")
	}
	face := faces[0]
	if face.FaceID == "" {
		return fmt.Errorf("manual face has no face identity")
	}
	if face.ImageWidth != expected.ImageWidth || face.ImageHeight != expected.ImageHeight || face.BoundingBoxX1 != expected.X || face.BoundingBoxY1 != expected.Y || face.BoundingBoxX2 != expected.X+expected.Width || face.BoundingBoxY2 != expected.Y+expected.Height {
		return fmt.Errorf("manual face geometry differs")
	}
	if face.SourceType != "manual" {
		return fmt.Errorf("fixture face is not manual")
	}
	if face.ID != expected.ID || face.Name != expected.Name || face.Hidden {
		return fmt.Errorf("manual face person differs")
	}
	if face.ThumbnailPath == "" {
		return fmt.Errorf("person thumbnail is not ready")
	}
	return nil
}

func checkPersonThumbnail(ctx context.Context, source *immich.Client, id string) error {
	thumbnail, err := source.PersonThumbnail(ctx, id)
	if err != nil {
		return err
	}
	defer func() { _ = thumbnail.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(thumbnail.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("read person thumbnail")
	}
	if len(data) == 0 || len(data) == 4<<20 || !strings.HasPrefix(thumbnail.ContentType, "image/") || !strings.HasPrefix(http.DetectContentType(data), "image/") {
		return fmt.Errorf("person thumbnail did not contain an image")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width != 26 || config.Height != 26 {
		return fmt.Errorf("person thumbnail dimensions differ")
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
	ID           string
	Name         string
	Description  string
	Members      []string
	CoverAssetID string
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
		if album.ThumbnailID != nil {
			got.CoverAssetID = *album.ThumbnailID
		}
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
		if got.Name != want.Name || got.Description != want.Description || got.CoverAssetID != want.CoverAssetID || album.Count != len(ids) || !slices.Equal(got.Members, ids) {
			return nil, fmt.Errorf("source fixture album title, description, cover, or membership differs")
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
	summaryCover := ""
	for momentIndex, moment := range detail.Moments {
		if moment.Date <= lastDate || len(moment.Entries) == 0 {
			return fmt.Errorf("imported Moment dates are unordered or empty")
		}
		lastDate = moment.Date
		cover := moment.Entries[0]
		for _, entry := range moment.Entries {
			if count >= len(expected) {
				return fmt.Errorf("import has extra entries")
			}
			want := expected[count]
			if entry.Filename != want.Filename || moment.Date != want.CapturedAt[:10] || entry.CapturedAt != want.CapturedAt[:19] || !entry.Available || entry.Kind != "IMAGE" || !strings.HasPrefix(entry.ThumbnailURL, "/api/media/") {
				return fmt.Errorf("capture-local Moment grouping or deterministic entry order differs for %s", want.Filename)
			}
			if want.ID == album.CoverAssetID {
				cover = entry
			}
			count++
		}
		if moment.CoverEntryID != cover.ID {
			return fmt.Errorf("imported Moment cover differs from configured seed")
		}
		if momentIndex == 0 {
			summaryCover = cover.ThumbnailURL
		}
	}
	if count != len(expected) {
		return fmt.Errorf("import lost entries")
	}
	if detail.PhotoCount != len(expected) || detail.VideoCount != 0 {
		return fmt.Errorf("album summary photo or video count differs from fixture")
	}
	if len(expected) > 0 && (detail.StartDate != expected[0].CapturedAt[:10] || detail.EndDate != expected[len(expected)-1].CapturedAt[:10] || detail.CoverURL != summaryCover) {
		return fmt.Errorf("album summary capture-local dates or configured Moment cover differs")
	}
	return nil
}

// verifySynchronization edits the second source album the way a photographer
// would, then proves a check reports exactly those edits, an apply commits
// them atomically, and Immich itself is left as the smoke found it once the
// edits are reverted.
func verifySynchronization(ctx context.Context, module *publishing.Module, library *fixture.Library, detail publishing.AlbumDetail, chapterTasks *[]string) error {
	album := library.Albums[1]
	unchanged, err := module.CheckSync(ctx, detail.ID, publishing.SyncRequest{})
	if err != nil {
		return err
	}
	if !unchanged.UpToDate || unchanged.Ready || len(unchanged.Additions)+len(unchanged.Removals)+len(unchanged.Changes) != 0 || unchanged.Description != nil {
		return fmt.Errorf("an unchanged source album was not reported as up to date")
	}
	// Remove the pair member that is not the cover, add the later-day photo,
	// and edit the description.
	removed := min(library.Assets[1].ID, library.Assets[2].ID)
	added := library.Assets[3]
	if err := library.RemoveAssets(ctx, album.ID, []string{removed}); err != nil {
		return err
	}
	if err := library.AddAssets(ctx, album.ID, []string{added.ID}); err != nil {
		return err
	}
	if err := library.SetDescription(ctx, album.ID, "Edited by the smoke"); err != nil {
		return err
	}
	revert := func() error {
		if err := library.RemoveAssets(ctx, album.ID, []string{added.ID}); err != nil {
			return err
		}
		if err := library.AddAssets(ctx, album.ID, []string{removed}); err != nil {
			return err
		}
		return library.SetDescription(ctx, album.ID, album.Description)
	}
	review, err := module.CheckSync(ctx, detail.ID, publishing.SyncRequest{})
	if err != nil {
		return errors.Join(err, revert())
	}
	if !review.Ready || review.UpToDate || len(review.Additions) != 1 || len(review.Removals) != 1 || len(review.Changes) != 0 || review.Description == nil || review.Description.After != "Edited by the smoke" {
		return errors.Join(fmt.Errorf("check did not report one addition, one removal, and the description edit"), revert())
	}
	if review.Additions[0].SourceID != added.ID || review.Additions[0].Filename != added.Filename || !strings.HasPrefix(review.Additions[0].SuggestedMomentID, "new:") {
		return errors.Join(fmt.Errorf("the added later-day photo was not suggested a new Moment"), revert())
	}
	if review.Removals[0].Cover || review.Removals[0].Deleted || len(review.CoverChoices) != 0 || len(review.RemovedMoments) != 0 {
		return errors.Join(fmt.Errorf("removing the non-cover pair member should need no cover choice"), revert())
	}
	// The first Memento Album was permanently deleted by the publishing check,
	// so the photo it once shared must not be reported as shared any more.
	if len(review.Additions[0].OtherAlbums) != 0 {
		return errors.Join(fmt.Errorf("a deleted Album was reported as sharing the added photo"), revert())
	}
	// A check changes nothing.
	still, err := module.GetAlbum(ctx, detail.ID)
	if err != nil {
		return errors.Join(err, revert())
	}
	if still.Description != detail.Description || still.PhotoCount != detail.PhotoCount || len(still.Moments) != len(detail.Moments) {
		return errors.Join(fmt.Errorf("a check changed the Album before apply"), revert())
	}
	applied, err := module.ApplySync(ctx, detail.ID, publishing.SyncRequest{ReviewToken: review.ReviewToken})
	if err != nil {
		return errors.Join(err, revert())
	}
	if applied.Description != "Edited by the smoke" || applied.PhotoCount != 2 || len(applied.Moments) != 2 || len(*chapterTasks) != 1 {
		return errors.Join(fmt.Errorf("apply did not commit the reviewed membership and description"), revert())
	}
	filenames := []string{}
	for _, moment := range applied.Moments {
		for _, entry := range moment.Entries {
			filenames = append(filenames, entry.Filename)
		}
	}
	if slices.Contains(filenames, library.Assets[slices.IndexFunc(library.Assets, func(a fixture.Asset) bool { return a.ID == removed })].Filename) || !slices.Contains(filenames, added.Filename) {
		return errors.Join(fmt.Errorf("apply did not swap the reviewed members"), revert())
	}
	first, err := module.GetAlbum(ctx, detail.ID)
	if err != nil {
		return errors.Join(err, revert())
	}
	if !reflect.DeepEqual(applied, first) {
		return errors.Join(fmt.Errorf("apply result differs from a fresh read"), revert())
	}
	if err := revert(); err != nil {
		return err
	}
	// The reverted source reads as the reverse diff, and the removed entry
	// returns with its identity.
	reversed, err := module.CheckSync(ctx, detail.ID, publishing.SyncRequest{})
	if err != nil {
		return err
	}
	if !reversed.Ready || len(reversed.Additions) != 1 || !reversed.Additions[0].Returning || len(reversed.Removals) != 1 || reversed.Description == nil || reversed.Description.After != album.Description {
		return fmt.Errorf("reverting the source edits was not reported as the reverse diff with a returning entry")
	}
	fmt.Printf("Synchronized %q: one addition, one removal, and a description edit applied atomically\n", detail.Title)
	return nil
}
