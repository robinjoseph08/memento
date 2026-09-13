// Package worker commits import and chapter work with application transactions
// and runs it through the shared PostgreSQL connection pool.
package worker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivertype"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

type finalAttemptKey struct{}

// FinalAttempt tells the import use case whether a failure exhausts automatic
// retries. Direct execution outside the runtime is a single, final attempt.
func FinalAttempt(ctx context.Context) bool {
	final, ok := ctx.Value(finalAttemptKey{}).(bool)
	return !ok || final
}

type importArgs struct {
	AlbumID string `json:"album_id" river:"unique"`
}

func (importArgs) Kind() string { return "import_immich_album" }

type chapterArgs struct {
	MediaItemID string `json:"media_item_id" river:"unique"`
	Checksum    string `json:"checksum" river:"unique"`
}

func (chapterArgs) Kind() string { return "extract_video_chapters" }

// Runtime shares Bun's pool and never closes it. Stop it before closing Bun.
type Runtime struct {
	client   *river.Client[*sql.Tx]
	chapters bool
}

// Option extends the runtime with another task on its own queue.
type Option func(*settings)

type settings struct {
	chapters     func(context.Context, string, string) error
	ffprobeSlots int
}

// Chapters adds the chapter extraction task on the bounded ffprobe queue.
// concurrency is how many probes may run at once; one is the production default.
func Chapters(execute func(ctx context.Context, mediaItemID, checksum string) error, concurrency int) Option {
	return func(s *settings) {
		s.chapters = execute
		s.ffprobeSlots = concurrency
	}
}

// New binds the use cases without executing work or starting goroutines.
func New(db *bun.DB, execute func(context.Context, string) error, options ...Option) (*Runtime, error) {
	if db == nil || execute == nil {
		return nil, fmt.Errorf("worker requires a database and import executor")
	}
	var config settings
	for _, option := range options {
		option(&config)
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &importWorker{execute: execute})
	queues := map[string]river.QueueConfig{"imports": {MaxWorkers: 2}}
	if config.chapters != nil {
		if config.ffprobeSlots < 1 {
			return nil, fmt.Errorf("worker requires a positive ffprobe concurrency")
		}
		river.AddWorker(workers, &chapterWorker{execute: config.chapters})
		queues["ffprobe"] = river.QueueConfig{MaxWorkers: config.ffprobeSlots}
	}
	client, err := river.NewClient(riverdatabasesql.New(db.DB), &river.Config{
		Workers:              workers,
		Queues:               queues,
		PollOnly:             true,
		FetchPollInterval:    time.Second,
		MaxAttempts:          3,
		JobTimeout:           15 * time.Minute,
		RescueStuckJobsAfter: 16 * time.Minute,
		SoftStopTimeout:      15 * time.Second,
	})
	if err != nil {
		return nil, runtimeError(context.Background(), "configure import worker", err)
	}
	return &Runtime{client: client, chapters: config.chapters != nil}, nil
}

// Start begins polling. Use the application lifetime, not an HTTP request context.
func (r *Runtime) Start(ctx context.Context) error {
	return runtimeError(ctx, "start import worker", r.client.Start(ctx))
}

// Stop drains work, cancelling remaining imports after fifteen seconds. A context
// error means shutdown is not confirmed; call Stop again before closing Bun.
func (r *Runtime) Stop(ctx context.Context) error {
	return runtimeError(ctx, "stop import worker", r.client.Stop(ctx))
}

// EnqueueImport records work in the caller's transaction. It never executes the
// import; the caller must commit before workers can see it.
func (r *Runtime) EnqueueImport(ctx context.Context, tx bun.Tx, albumID string) error {
	if tx.Tx == nil || albumID == "" {
		return fmt.Errorf("enqueue import requires a transaction and album ID")
	}
	result, err := r.client.InsertTx(ctx, tx.Tx, importArgs{AlbumID: albumID}, &river.InsertOpts{
		Queue: "imports", MaxAttempts: 3,
		UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: []rivertype.JobState{
			rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning,
			rivertype.JobStateRetryable, rivertype.JobStateScheduled,
		}},
	})
	if err != nil {
		return runtimeError(ctx, "enqueue import", err)
	}
	if result.UniqueSkippedAsDuplicate && result.Job.State == rivertype.JobStateRunning {
		return &errcodes.Error{HTTPCode: 409, Code: "import_attempt_running", Message: "The previous import attempt is still finishing. Try again in a moment."}
	}
	if result.UniqueSkippedAsDuplicate && result.Job.State == rivertype.JobStateRetryable {
		_, err = r.client.JobRetryTx(ctx, tx.Tx, result.Job.ID)
	}
	return runtimeError(ctx, "enqueue import", err)
}

// EnqueueChapters records chapter extraction for one Media Item at one
// checksum in the caller's transaction. An active task for the same pair is
// reused, and a task waiting for an automatic retry runs at once instead.
func (r *Runtime) EnqueueChapters(ctx context.Context, tx bun.Tx, mediaItemID, checksum string) error {
	if !r.chapters {
		return fmt.Errorf("chapter extraction is not configured on this worker")
	}
	if tx.Tx == nil || mediaItemID == "" || checksum == "" {
		return fmt.Errorf("enqueue chapters requires a transaction, media item ID, and checksum")
	}
	result, err := r.client.InsertTx(ctx, tx.Tx, chapterArgs{MediaItemID: mediaItemID, Checksum: checksum}, &river.InsertOpts{
		Queue: "ffprobe", MaxAttempts: 3,
		UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: []rivertype.JobState{
			rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning,
			rivertype.JobStateRetryable, rivertype.JobStateScheduled,
		}},
	})
	if err != nil {
		return runtimeError(ctx, "enqueue chapters", err)
	}
	if result.UniqueSkippedAsDuplicate && result.Job.State == rivertype.JobStateRetryable {
		_, err = r.client.JobRetryTx(ctx, tx.Tx, result.Job.ID)
	}
	return runtimeError(ctx, "enqueue chapters", err)
}

type chapterWorker struct {
	river.WorkerDefaults[chapterArgs]
	execute func(context.Context, string, string) error
}

// Timeout bounds one probe well under the import budget; ffprobe reads only
// container headers, so minutes mean a stalled connection, not a large file.
func (*chapterWorker) Timeout(*river.Job[chapterArgs]) time.Duration { return 3 * time.Minute }

func (w *chapterWorker) Work(ctx context.Context, job *river.Job[chapterArgs]) error {
	return work(ctx, "extract chapters", job.Attempt >= job.MaxAttempts, func(ctx context.Context) error {
		return w.execute(ctx, job.Args.MediaItemID, job.Args.Checksum)
	})
}

type importWorker struct {
	river.WorkerDefaults[importArgs]
	execute func(context.Context, string) error
}

func (w *importWorker) Work(ctx context.Context, job *river.Job[importArgs]) error {
	return work(ctx, "execute import", job.Attempt >= job.MaxAttempts, func(ctx context.Context) error {
		return w.execute(ctx, job.Args.AlbumID)
	})
}

// work runs one task body with the final-attempt flag, converting a panic into
// a redacted failure so River never persists upstream detail.
func work(ctx context.Context, operation string, finalAttempt bool, execute func(context.Context) error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = runtimeError(ctx, operation, fmt.Errorf("executor panicked: %v", recovered))
		}
	}()
	ctx = context.WithValue(ctx, finalAttemptKey{}, finalAttempt)
	return runtimeError(ctx, operation, execute(ctx))
}

// Keep adapter and upstream details out of River's persisted error strings while
// preserving the cause and creation-site stack for error inspection.
type operationError struct {
	operation string
	cause     error
}

func (e *operationError) Error() string { return e.operation + " failed" }
func (e *operationError) Unwrap() error { return e.cause }
func runtimeError(ctx context.Context, operation string, err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*errcodes.Error](err); !ok {
		err = errorstack.CaptureContext(ctx, err)
	}
	return &operationError{operation: operation, cause: err}
}
