//go:build integration && performance

package performance

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/activity"
	"github.com/robinjoseph08/memento/pkg/audiences"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/emaildelivery"
	"github.com/robinjoseph08/memento/pkg/events"
	"github.com/robinjoseph08/memento/pkg/favorites"
	"github.com/robinjoseph08/memento/pkg/health"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/library"
	"github.com/robinjoseph08/memento/pkg/mediaaccess"
	"github.com/robinjoseph08/memento/pkg/people"
	"github.com/robinjoseph08/memento/pkg/search"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/sources"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

func TestTargetScalePerformance(t *testing.T) {
	fixture := newScaleFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()

	actor := fixture.recipientActor(1)
	cheapSamples := configuredSamples("MEMENTO_PERFORMANCE_CHEAP_SAMPLES", 100)
	operationSamples := configuredSamples("MEMENTO_PERFORMANCE_OPERATION_SAMPLES", 20)
	metrics := make([]Metric, 0, len(Baselines))
	addDuration := func(name string, samples []time.Duration, concurrency int, immichLatency time.Duration) {
		t.Helper()
		baseline, found := BaselineByName(name)
		require.True(t, found)
		metric, err := NewDurationMetric(baseline, samples, "warm", "steady", "none", concurrency, immichLatency)
		require.NoError(t, err)
		metrics = append(metrics, metric)
	}

	healthService := health.New(fixture.db, healthyChecker{}, healthyWorker{}, time.Second, time.Minute)
	healthRouter := echo.New()
	health.RegisterRoutes(healthRouter, healthService)
	addDuration("liveness", measureHTTP(t, healthRouter, "/api/health/live", cheapSamples), 1, 0)
	addDuration("readiness", measureHTTP(t, healthRouter, "/api/health/ready", operationSamples), 1, 0)

	addDuration("session_authorization", measure(t, operationSamples, func() error {
		current, err := setup.CurrentRecipientSession(ctx, fixture.db, actor)
		if err != nil {
			return err
		}
		if !current {
			return fmt.Errorf("seeded Session is not current")
		}
		return mediaaccess.Require(ctx, fixture.db, actor, deterministicUUID("media", 1))
	}), 1, 0)

	libraryService := library.New(fixture.db, nil)
	addDuration("recipient_list", measure(t, operationSamples, func() error {
		page, err := libraryService.Photos(ctx, actor, "100", "", false)
		if err == nil && len(page.Media) != 100 {
			return fmt.Errorf("Recipient timeline returned %d items, expected 100", len(page.Media))
		}
		return err
	}), 1, 0)

	peopleService := people.New(fixture.db)
	addDuration("curator_list", measure(t, operationSamples, func() error {
		_, err := peopleService.List(ctx, "", false)
		return err
	}), 1, 0)

	searchService := search.New(fixture.db)
	addDuration("search", measure(t, operationSamples, func() error {
		response, err := searchService.Search(ctx, actor, search.Request{Query: "01"})
		if err == nil && len(response.Events) == 0 {
			return fmt.Errorf("authorized search returned no Events")
		}
		return err
	}), 1, 0)

	favoriteService := favorites.New(fixture.db, activity.New(fixture.db))
	mediaID := deterministicUUID("media", 1)
	favorite := false
	addDuration("mutation", measure(t, operationSamples, func() error {
		favorite = !favorite
		_, err := favoriteService.Set(ctx, actor, mediaID, favorite)
		return err
	}), 1, 0)

	publicationService := events.New(fixture.db)
	if os.Getenv("MEMENTO_PERFORMANCE_TRACE_QUERIES") != "" {
		fixture.db.AddQueryHook(slowQueryTrace{})
	}
	publicationSamples := configuredSamples("MEMENTO_PERFORMANCE_PUBLICATION_SAMPLES", 20)
	publicationDurations := make([]time.Duration, 0, publicationSamples)
	for sample := -1; sample < publicationSamples; sample++ {
		var version int64
		require.NoError(t, fixture.db.NewRaw(`UPDATE events SET title = ?, version = version + 1, final_review_complete = true WHERE id = ? RETURNING version`, fmt.Sprintf("Target-scale revision %d", sample), fixture.publicationEvent).Scan(ctx, &version))
		started := time.Now()
		_, err := publicationService.PublishEvent(ctx, setup.CuratorSession{PersonID: fixture.curatorID, SessionID: fixture.curatorSession}, fixture.publicationEvent, events.PublishEventRequest{Version: version})
		require.NoError(t, err)
		if sample >= 0 {
			publicationDurations = append(publicationDurations, time.Since(started))
		}
	}
	addDuration("publication", publicationDurations, 1, 0)

	audienceService := audiences.New(fixture.db, nil)
	var reviewVersion int64
	require.NoError(t, fixture.db.NewRaw(`SELECT review_version FROM draft_moments WHERE id = ?`, fixture.proposalMoment).Scan(ctx, &reviewVersion))
	audienceDurations := make([]time.Duration, 0, operationSamples)
	for sample := -1; sample < operationSamples; sample++ {
		started := time.Now()
		review, err := audienceService.Recalculate(ctx, setup.CuratorSession{PersonID: fixture.curatorID, SessionID: fixture.curatorSession}, "moment", fixture.proposalMoment, reviewVersion)
		require.NoError(t, err)
		require.Len(t, review.Proposal, 50)
		if sample >= 0 {
			audienceDurations = append(audienceDurations, time.Since(started))
		}
		reviewVersion++
	}
	addDuration("audience_proposal", audienceDurations, 1, 0)

	jobDurations := measureWorkerStarts(t, fixture.db, "performance_job_start", operationSamples, 2*time.Millisecond, 0)
	addDuration("job_start", jobDurations, 1, 0)
	notificationDurations := measureWorkerStarts(t, fixture.db, emaildelivery.ImmediateJobKind, operationSamples, 2*time.Millisecond, 10*time.Millisecond)
	addDuration("notification_dispatch", notificationDurations, 1, 0)

	reconciliationConnector := newScaleConnector(0, nil)
	reconciliationService := sources.New(fixture.db, reconciliationConnector, 10*time.Minute)
	require.NoError(t, reconciliationService.Reconcile(ctx, fixture.sourceAlbum))
	started := time.Now()
	require.NoError(t, reconciliationService.Reconcile(ctx, fixture.sourceAlbum))
	require.Equal(t, int64(200), reconciliationConnector.pageCalls.Load(), "warm and measured reconciliation must each consume 100 pages")
	require.Equal(t, int64(1), reconciliationConnector.maximumActive.Load(), "connector pagination remains bounded and serial")
	addDuration("reconciliation", []time.Duration{time.Since(started)}, 1, 0)

	proxySource := &streamSource{delay: 5 * time.Millisecond, size: 1}
	proxyRouter := libraryRouter(actor, fixture.db, proxySource)
	proxySamples, blocked := measureProxyHTTP(t, proxyRouter, proxySource, mediaID, operationSamples)
	proxyMetric, err := NewDurationMetric(mustBaseline(t, "proxy_overhead"), proxySamples, "warm", "steady", "none", 1, proxySource.delay)
	require.NoError(t, err)
	proxyMetric.ImmichBlocked = blocked
	metrics = append(metrics, proxyMetric)

	streamConcurrency := configuredSamples("MEMENTO_PERFORMANCE_STREAM_CONCURRENCY", 32)
	streamSource := &streamSource{size: 16 << 20, hold: true, streamStarted: make(chan struct{}, streamConcurrency), release: make(chan struct{})}
	streamRouter := libraryRouter(actor, fixture.db, streamSource)
	streamBuffers := measureConcurrentStreams(t, streamRouter, streamSource, mediaID, streamConcurrency)
	streamMetric, err := NewByteMetric(mustBaseline(t, "stream_buffer"), streamBuffers, "warm", "steady", streamConcurrency)
	require.NoError(t, err)
	streamMetric.MaximumUpstreamReads = int(streamSource.maximumRead.Load())
	metrics = append(metrics, streamMetric)

	comparisons := measureCompetingWork(t, ctx, fixture, actor, libraryService, searchService, operationSamples)
	plans := capturePlans(t, ctx, fixture.db, actor)
	qualifying := cheapSamples >= 100 && operationSamples >= 20 && publicationSamples >= 20 && streamConcurrency >= 32
	report := Report{SchemaVersion: 1, Qualifying: qualifying, GeneratedAt: time.Now().UTC(), CacheState: "warm", Fixture: fixture.shape, Environment: readEnvironment(t, ctx, fixture.db), Metrics: metrics, Comparisons: comparisons, Plans: plans}

	reportDirectory := os.Getenv("MEMENTO_PERFORMANCE_REPORT_DIR")
	if reportDirectory == "" {
		reportDirectory = filepath.Join("..", "..", "tmp", "performance")
	}
	require.NoError(t, os.MkdirAll(reportDirectory, 0o700))
	require.NoError(t, report.Write(filepath.Join(reportDirectory, "report.json"), filepath.Join(reportDirectory, "report.md")))
	for _, metric := range append(append([]Metric(nil), metrics...), comparisons...) {
		require.Truef(t, metric.Passed, "%s p95=%s target=%s scenario=%s competitor=%s", metric.Name, time.Duration(metric.P95), metric.Target, metric.Scenario, metric.CompetingWork)
	}
}

func configuredSamples(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err == nil && value > 0 {
		return value
	}
	return fallback
}

func measure(t *testing.T, samples int, operation func() error) []time.Duration {
	t.Helper()
	result := make([]time.Duration, 0, samples)
	require.NoError(t, operation(), "warm-up operation failed")
	for sample := range samples {
		started := time.Now()
		require.NoErrorf(t, operation(), "measurement sample %d/%d failed", sample+1, samples)
		result = append(result, time.Since(started))
	}
	return result
}

func measureHTTP(t *testing.T, router http.Handler, path string, samples int) []time.Duration {
	t.Helper()
	return measure(t, samples, func() error {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			return fmt.Errorf("%s returned %d: %s", path, response.Code, response.Body.String())
		}
		return nil
	})
}

func mustBaseline(t *testing.T, name string) Baseline {
	t.Helper()
	baseline, found := BaselineByName(name)
	require.True(t, found)
	return baseline
}

type slowQueryTrace struct{}

func (slowQueryTrace) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context { return ctx }
func (slowQueryTrace) AfterQuery(_ context.Context, event *bun.QueryEvent) {
	elapsed := time.Since(event.StartTime)
	if elapsed >= 100*time.Millisecond {
		query := strings.Join(strings.Fields(event.QueryTemplate), " ")
		if len(query) > 180 {
			query = query[:180]
		}
		fmt.Printf("PERFORMANCE QUERY %s %s\n", elapsed, query)
	}
}

type healthyChecker struct{}

func (healthyChecker) Check(context.Context) error { return nil }

type healthyWorker struct{}

func (healthyWorker) Healthy(time.Duration) bool { return true }

func (fixture scaleFixture) recipientActor(index int) setup.SessionActor {
	return setup.SessionActor{PersonID: deterministicUUID("person", index), AccessID: deterministicUUID("access", index), SessionID: deterministicUUID("session", index)}
}

func measureWorkerStarts(t *testing.T, db *bun.DB, kind string, samples int, poll, eligibilityDelay time.Duration) []time.Duration {
	t.Helper()
	starts := make(chan time.Time, samples)
	jobWorker, err := worker.New(db, config.WorkerConfig{PollInterval: poll, HeartbeatInterval: 50 * time.Millisecond, LeaseDuration: time.Minute, RetryBase: time.Millisecond, RetryMax: time.Second}, kind, map[string]worker.Handler{kind: func(context.Context, worker.Job) error { starts <- time.Now(); return nil }})
	require.NoError(t, err)
	workerCtx, cancel := context.WithCancel(context.Background())
	jobWorker.Start(workerCtx)
	t.Cleanup(func() {
		jobWorker.StopClaims()
		drainCtx, drainCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer drainCancel()
		_ = jobWorker.Drain(drainCtx)
		cancel()
	})
	result := make([]time.Duration, 0, samples)
	for sample := 0; sample < samples; sample++ {
		availableAt := time.Now().Add(eligibilityDelay)
		if kind == emaildelivery.ImmediateJobKind {
			windowStartedAt := availableAt.Add(-15 * time.Minute)
			_, err := db.NewRaw(`INSERT INTO notification_batches (public_id,recipient_access_generation_id,channel,window_started_at,closes_at,status) VALUES (?,?,'email',?,?,'pending')`, deterministicUUID("measurement-batch", sample+1), deterministicUUID("access", 1), windowStartedAt, availableAt).Exec(context.Background())
			require.NoError(t, err)
		}
		_, err := db.NewRaw(`INSERT INTO jobs (kind,payload,available_at,created_at,updated_at) VALUES (?, '{}'::jsonb, ?, ?, ?)`, kind, availableAt, time.Now(), time.Now()).Exec(context.Background())
		require.NoError(t, err)
		select {
		case handlerStarted := <-starts:
			result = append(result, handlerStarted.Sub(availableAt))
		case <-time.After(5 * time.Second):
			t.Fatalf("%s did not start within deadline", kind)
		}
	}
	jobWorker.StopClaims()
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer drainCancel()
	require.NoError(t, jobWorker.Drain(drainCtx))
	cancel()
	return result
}

type fixedAuthorizer struct{ actor setup.SessionActor }

func (authorizer fixedAuthorizer) AuthorizeSession(context.Context, string, string, bool) (setup.SessionActor, error) {
	return authorizer.actor, nil
}

type streamSource struct {
	delay         time.Duration
	size          int64
	hold          bool
	streamStarted chan struct{}
	release       chan struct{}
	blocked       atomic.Int64
	maximumRead   atomic.Int64
}

func (source *streamSource) response(ctx context.Context, contentType string) (immich.MediaResponse, error) {
	if source.delay > 0 {
		started := time.Now()
		timer := time.NewTimer(source.delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return immich.MediaResponse{}, ctx.Err()
		case <-timer.C:
		}
		source.blocked.Add(time.Since(started).Nanoseconds())
	}
	body := &measuredBody{remaining: source.size, maximumRead: &source.maximumRead}
	if source.hold {
		body.started = source.streamStarted
		body.release = source.release
	}
	return immich.MediaResponse{Body: body, StatusCode: http.StatusOK, ContentType: contentType, ContentLength: source.size}, nil
}
func (source *streamSource) Thumbnail(ctx context.Context, _ uuid.UUID, _ immich.MediaRequest) (immich.MediaResponse, error) {
	return source.response(ctx, "image/webp")
}
func (source *streamSource) Preview(ctx context.Context, _ uuid.UUID, _ immich.MediaRequest) (immich.MediaResponse, error) {
	return source.response(ctx, "image/jpeg")
}
func (source *streamSource) Video(ctx context.Context, _ uuid.UUID, _ immich.MediaRequest) (immich.MediaResponse, error) {
	return source.response(ctx, "video/mp4")
}
func (source *streamSource) Original(ctx context.Context, _ uuid.UUID, _ immich.MediaRequest) (immich.MediaResponse, error) {
	return source.response(ctx, "application/octet-stream")
}

type measuredBody struct {
	remaining   int64
	maximumRead *atomic.Int64
	started     chan<- struct{}
	release     <-chan struct{}
	once        sync.Once
}

func (body *measuredBody) Read(buffer []byte) (int, error) {
	body.once.Do(func() {
		if body.started != nil {
			body.started <- struct{}{}
			<-body.release
		}
	})
	for {
		current := body.maximumRead.Load()
		if int64(len(buffer)) <= current || body.maximumRead.CompareAndSwap(current, int64(len(buffer))) {
			break
		}
	}
	if body.remaining == 0 {
		return 0, io.EOF
	}
	read := int64(len(buffer))
	if read > body.remaining {
		read = body.remaining
	}
	for index := int64(0); index < read; index++ {
		buffer[index] = byte(index)
	}
	body.remaining -= read
	return int(read), nil
}
func (*measuredBody) Close() error { return nil }

type discardWriter struct {
	header http.Header
	status int
}

func (writer *discardWriter) Header() http.Header                { return writer.header }
func (writer *discardWriter) WriteHeader(status int)             { writer.status = status }
func (writer *discardWriter) Write(contents []byte) (int, error) { return len(contents), nil }

func libraryRouter(actor setup.SessionActor, db *bun.DB, source *streamSource) *echo.Echo {
	router := echo.New()
	library.RegisterRoutes(router, library.NewHandler(library.New(db, source), fixedAuthorizer{actor: actor}))
	return router
}

func streamRequest(router http.Handler, mediaID uuid.UUID) error {
	request := httptest.NewRequest(http.MethodGet, "/api/me/media/"+mediaID.String()+"/original", nil)
	request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "performance"})
	response := &discardWriter{header: make(http.Header)}
	router.ServeHTTP(response, request)
	if response.status != http.StatusOK {
		return fmt.Errorf("stream returned %d", response.status)
	}
	return nil
}

func measureProxyHTTP(t *testing.T, router http.Handler, source *streamSource, mediaID uuid.UUID, samples int) ([]time.Duration, time.Duration) {
	t.Helper()
	durations := make([]time.Duration, 0, samples)
	var blocked time.Duration
	for range samples {
		before := source.blocked.Load()
		started := time.Now()
		require.NoError(t, streamRequest(router, mediaID))
		elapsed := time.Since(started)
		upstream := time.Duration(source.blocked.Load() - before)
		require.GreaterOrEqual(t, elapsed, upstream)
		durations = append(durations, elapsed-upstream)
		blocked += upstream
	}
	return durations, blocked
}

func measureConcurrentStreams(t *testing.T, router http.Handler, source *streamSource, mediaID uuid.UUID, concurrency int) []int64 {
	t.Helper()
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	var group sync.WaitGroup
	errors := make(chan error, concurrency)
	for range concurrency {
		group.Add(1)
		go func() { defer group.Done(); errors <- streamRequest(router, mediaID) }()
	}
	for range concurrency {
		select {
		case <-source.streamStarted:
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent Media streams did not reach the streaming boundary")
		}
	}
	var active runtime.MemStats
	runtime.ReadMemStats(&active)
	close(source.release)
	group.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	growth := int64(0)
	if active.HeapAlloc > before.HeapAlloc {
		growth = int64(active.HeapAlloc-before.HeapAlloc) / int64(concurrency)
	}
	if maximumRead := source.maximumRead.Load(); maximumRead > growth {
		growth = maximumRead
	}
	result := make([]int64, concurrency)
	for index := range result {
		result[index] = growth
	}
	return result
}
