//go:build integration && performance

package performance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/emaildelivery"
	"github.com/robinjoseph08/memento/pkg/events"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/library"
	"github.com/robinjoseph08/memento/pkg/outbox"
	"github.com/robinjoseph08/memento/pkg/search"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/smtp"
	"github.com/robinjoseph08/memento/pkg/sources"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type scaleConnector struct {
	latency       time.Duration
	started       chan struct{}
	once          sync.Once
	pageCalls     atomic.Int64
	active        atomic.Int64
	maximumActive atomic.Int64
}

func newScaleConnector(latency time.Duration, started chan struct{}) *scaleConnector {
	return &scaleConnector{latency: latency, started: started}
}

func (connector *scaleConnector) wait(ctx context.Context) error {
	if connector.latency == 0 {
		return nil
	}
	timer := time.NewTimer(connector.latency)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func (connector *scaleConnector) Check(context.Context) error { return nil }
func (connector *scaleConnector) OwnedAlbums(context.Context) ([]immich.AlbumSummary, error) {
	return []immich.AlbumSummary{connector.summary()}, nil
}
func (connector *scaleConnector) Album(ctx context.Context, _ uuid.UUID) (immich.AlbumSummary, error) {
	if err := connector.wait(ctx); err != nil {
		return immich.AlbumSummary{}, err
	}
	return connector.summary(), nil
}
func (connector *scaleConnector) AlbumAssetsPage(ctx context.Context, _ uuid.UUID, page int) (immich.AssetPage, error) {
	connector.pageCalls.Add(1)
	active := connector.active.Add(1)
	defer connector.active.Add(-1)
	for {
		maximum := connector.maximumActive.Load()
		if active <= maximum || connector.maximumActive.CompareAndSwap(maximum, active) {
			break
		}
	}
	if connector.started != nil {
		connector.once.Do(func() { close(connector.started) })
	}
	if err := connector.wait(ctx); err != nil {
		return immich.AssetPage{}, err
	}
	if page < 1 || page > 100 {
		return immich.AssetPage{}, nil
	}
	width, height := 1600, 1200
	items := make([]immich.AssetSummary, 1000)
	for index := range items {
		mediaIndex := (page-1)*1000 + index + 1
		localDateTime := time.Date(2020, 1, 1, 12, 0, 0, 0, time.UTC).AddDate(0, 0, mediaIndex%2000).Format(time.RFC3339)
		items[index] = immich.AssetSummary{SourceID: deterministicUUID("asset", mediaIndex), MediaType: map[bool]string{true: "video", false: "image"}[mediaIndex%20 == 0], Width: &width, Height: &height, LocalDateTime: &localDateTime, Filename: fmt.Sprintf("media-%d.jpg", mediaIndex), OriginalPath: fmt.Sprintf("/fixture/media-%d.jpg", mediaIndex)}
	}
	var next *int
	if page < 100 {
		value := page + 1
		next = &value
	}
	return immich.AssetPage{Items: items, NextPage: next}, nil
}
func (connector *scaleConnector) AssetExists(context.Context, uuid.UUID) (bool, error) {
	return true, nil
}
func (connector *scaleConnector) summary() immich.AlbumSummary {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	return immich.AlbumSummary{SourceID: uuid.MustParse("00000000-0000-4000-8000-000000000005"), Name: "Target-scale source", AssetCount: 100000, CreatedAt: now, UpdatedAt: now}
}

type blockingEmailSender struct {
	started chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (sender *blockingEmailSender) Send(ctx context.Context, _ smtp.Message) error {
	sender.once.Do(func() { close(sender.started) })
	select {
	case <-sender.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type publicationStartHook struct {
	started chan struct{}
	once    sync.Once
}

func (hook *publicationStartHook) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context {
	return ctx
}
func (hook *publicationStartHook) AfterQuery(_ context.Context, event *bun.QueryEvent) {
	if event.Err == nil && strings.Contains(event.Query, "events:staged-access-summary-inputs") && strings.Contains(event.Query, "pg_advisory_xact_lock(") {
		hook.once.Do(func() { close(hook.started) })
	}
}

func measureCompetingWork(t *testing.T, ctx context.Context, fixture scaleFixture, actor setup.SessionActor, libraryService *library.Service, searchService *search.Service, samples int) []Metric {
	t.Helper()
	measureTimeline := func() error {
		page, err := libraryService.Photos(ctx, actor, "100", "", false)
		if err == nil && len(page.Media) != 100 {
			return fmt.Errorf("Recipient timeline returned %d items, expected 100", len(page.Media))
		}
		return err
	}

	started := make(chan struct{})
	reconciler := sources.New(fixture.db, newScaleConnector(time.Millisecond, started), 10*time.Minute)
	reconcileResult := make(chan error, 1)
	go func() { reconcileResult <- reconciler.Reconcile(ctx, fixture.sourceAlbum) }()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("competing reconciliation did not start")
	}
	recipientSamples := measure(t, samples, measureTimeline)
	require.NoError(t, <-reconcileResult)
	recipientMetric, err := NewDurationMetric(mustBaseline(t, "recipient_list"), recipientSamples, "warm", "competing", "reconciliation", 2, time.Millisecond)
	require.NoError(t, err)

	var publicationVersion int64
	require.NoError(t, fixture.db.NewRaw(`UPDATE events SET title='Family Event 01 Competing Publication',version=version+1,final_review_complete=true WHERE id=? RETURNING version`, fixture.publicationEvent).Scan(ctx, &publicationVersion))
	publicationStarted := make(chan struct{})
	fixture.db.AddQueryHook(&publicationStartHook{started: publicationStarted})
	publicationResult := make(chan error, 1)
	go func() {
		_, publishErr := events.New(fixture.db).PublishEvent(ctx, setup.CuratorSession{PersonID: fixture.curatorID, SessionID: fixture.curatorSession}, fixture.publicationEvent, events.PublishEventRequest{Version: publicationVersion})
		publicationResult <- publishErr
	}()
	select {
	case <-publicationStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("competing Publication did not enter its transaction")
	}
	publicationSamples := measure(t, samples, measureTimeline)
	require.NoError(t, <-publicationResult)
	publicationMetric, err := NewDurationMetric(mustBaseline(t, "recipient_list"), publicationSamples, "warm", "competing", "publication", 2, 0)
	require.NoError(t, err)

	notificationStarted := make(chan struct{})
	releaseNotification := make(chan struct{})
	sender := &blockingEmailSender{started: notificationStarted, release: releaseNotification}
	emailService := performanceEmailService(fixture.db, sender)
	notificationWorker, err := worker.New(fixture.db, config.WorkerConfig{PollInterval: 2 * time.Millisecond, HeartbeatInterval: 50 * time.Millisecond, LeaseDuration: time.Minute, RetryBase: time.Millisecond, RetryMax: time.Second}, "performance-notification-competitor", map[string]worker.Handler{
		emaildelivery.ImmediateJobKind: emailService.HandleImmediate,
	}, worker.WithDispatcher(outbox.New(fixture.db)))
	require.NoError(t, err)
	workerCtx, stopWorker := context.WithCancel(ctx)
	notificationWorker.Start(workerCtx)
	queuePerformanceComment(t, fixture, emailService, "competitor", 1, time.Now().Add(10*time.Millisecond))
	select {
	case <-notificationStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("notification dispatch competitor did not start")
	}
	searchSamples := measure(t, samples, func() error {
		response, searchErr := searchService.Search(ctx, actor, search.Request{Query: "01"})
		if searchErr == nil && len(response.Events) == 0 {
			return fmt.Errorf("authorized search returned no Events")
		}
		return searchErr
	})
	close(releaseNotification)
	notificationWorker.StopClaims()
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 5*time.Second)
	require.NoError(t, notificationWorker.Drain(drainCtx))
	drainCancel()
	stopWorker()
	searchMetric, err := NewDurationMetric(mustBaseline(t, "search"), searchSamples, "warm", "competing", "notification dispatch", 2, 0)
	require.NoError(t, err)
	return []Metric{recipientMetric, publicationMetric, searchMetric}
}

type queryCaptureHook struct {
	match string
	mu    sync.Mutex
	query string
}

func (hook *queryCaptureHook) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context {
	return ctx
}
func (hook *queryCaptureHook) AfterQuery(_ context.Context, event *bun.QueryEvent) {
	if event.Err != nil || !strings.Contains(event.QueryTemplate, hook.match) {
		return
	}
	hook.mu.Lock()
	defer hook.mu.Unlock()
	if hook.query == "" {
		hook.query = event.Query
	}
}
func (hook *queryCaptureHook) captured() string {
	hook.mu.Lock()
	defer hook.mu.Unlock()
	return hook.query
}

func capturePlans(t *testing.T, ctx context.Context, db *bun.DB, actor setup.SessionActor) []PlanEvidence {
	t.Helper()
	searchCapture := &queryCaptureHook{match: "WITH authorized AS"}
	db.AddQueryHook(searchCapture)
	searchResponse, err := search.New(db).Search(ctx, actor, search.Request{Query: "01"})
	require.NoError(t, err)
	require.NotEmpty(t, searchResponse.Events)
	require.NotEmpty(t, searchCapture.captured())

	libraryService := library.New(db, nil)
	chronologyCapture := &queryCaptureHook{match: "GROUP BY capture_date"}
	db.AddQueryHook(chronologyCapture)
	chronology, err := libraryService.Chronology(ctx, actor, false)
	require.NoError(t, err)
	require.NotEmpty(t, chronology.Dates)
	require.NotEmpty(t, chronologyCapture.captured())

	galleryCapture := &queryCaptureHook{match: "SELECT media_item_id AS id"}
	db.AddQueryHook(galleryCapture)
	gallery, err := libraryService.Photos(ctx, actor, "100", "", false)
	require.NoError(t, err)
	require.Len(t, gallery.Media, 100)
	require.NotEmpty(t, galleryCapture.captured())

	plans := []struct {
		name, role, query string
		args              []any
	}{
		{name: "authorization", role: "Session authorization", query: `SELECT EXISTS (SELECT 1 FROM sessions session JOIN people person ON person.id=session.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL JOIN recipient_access_generations access ON access.id=session.recipient_access_generation_id AND access.person_id=session.person_id AND access.is_current AND access.state='completed' JOIN system_settings settings ON settings.id=1 AND settings.setup_complete AND NOT settings.recovery_hold AND settings.security_epoch=session.security_epoch WHERE session.id=? AND session.person_id=? AND session.recipient_access_generation_id=? AND session.revoked_at IS NULL)`, args: []any{actor.SessionID, actor.PersonID, actor.AccessID}},
		{name: "media_authorization", role: "Simple Media authorization", query: `SELECT EXISTS (SELECT 1 FROM current_audience_entitlements entitlement JOIN current_published_placements placement ON placement.event_id=entitlement.event_id AND placement.publication_id=entitlement.publication_id AND placement.media_item_id=entitlement.media_item_id JOIN published_moments moment ON moment.id=placement.published_moment_id WHERE entitlement.recipient_access_generation_id=? AND entitlement.media_item_id=? AND NOT content_is_withdrawn(placement.event_id,moment.draft_moment_id,placement.media_item_id))`, args: []any{actor.AccessID, deterministicUUID("media", 1)}},
		{name: "gallery", role: "Recipient timeline", query: galleryCapture.captured()},
		{name: "chronology", role: "Complete authorized Recipient chronology", query: chronologyCapture.captured()},
		{name: "search", role: "Authorized search", query: searchCapture.captured()},
		{name: "curator", role: "Curator People list", query: `SELECT id,display_name,sort_name FROM people WHERE archived_at IS NULL ORDER BY memento_normalize_person_name(sort_name),id LIMIT 200`},
	}
	result := make([]PlanEvidence, 0, len(plans))
	for _, evidence := range plans {
		var raw string
		query := "EXPLAIN (ANALYZE, BUFFERS, SETTINGS, FORMAT JSON) " + evidence.query
		require.NoError(t, db.NewRaw(query, evidence.args...).Scan(ctx, &raw))
		require.True(t, json.Valid([]byte(raw)), "%s plan is not JSON", evidence.name)
		result = append(result, PlanEvidence{Name: evidence.name, CacheState: "warm", SQLRole: evidence.role, Plan: json.RawMessage(raw)})
	}
	return result
}

func readEnvironment(t *testing.T, ctx context.Context, db *bun.DB) Environment {
	t.Helper()
	environment := Environment{OS: runtime.GOOS, Architecture: runtime.GOARCH, LogicalCPUs: runtime.NumCPU(), GoVersion: runtime.Version(), DatabasePoolSize: db.Stats().MaxOpenConnections}
	environment.GitRevision = commandOutput("git", "rev-parse", "HEAD")
	if environment.GitRevision == "" {
		environment.GitRevision = "unknown"
	}
	environment.GitDirty = commandOutput("git", "status", "--porcelain", "--untracked-files=normal") != ""
	environment.CPU = commandOutput("sysctl", "-n", "machdep.cpu.brand_string")
	if environment.CPU == "" {
		environment.CPU = commandOutput("sh", "-c", `grep -m1 'model name' /proc/cpuinfo | cut -d: -f2-`)
	}
	if environment.CPU == "" {
		environment.CPU = "unknown"
	}
	environment.MemoryBytes = physicalMemory()
	require.NoError(t, db.NewRaw(`SELECT version(),pg_database_size(current_database())`).Scan(ctx, &environment.PostgreSQLVersion, &environment.DatabaseSizeBytes))
	return environment
}

func commandOutput(name string, arguments ...string) string {
	output, err := exec.Command(name, arguments...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func physicalMemory() uint64 {
	if value := commandOutput("sysctl", "-n", "hw.memsize"); value != "" {
		parsed, _ := strconv.ParseUint(value, 10, 64)
		return parsed
	}
	contents, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(contents))
	if len(fields) >= 2 {
		parsed, _ := strconv.ParseUint(fields[1], 10, 64)
		return parsed * 1024
	}
	return 0
}
