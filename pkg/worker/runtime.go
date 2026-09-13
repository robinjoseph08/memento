// Package worker commits import and mail work with application transactions
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
	"github.com/robinjoseph08/golib/logger"
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

type mailArgs struct {
	DeliveryID string `json:"delivery_id" river:"unique"`
}

func (mailArgs) Kind() string { return "deliver_mail" }

const (
	mailQueue          = "mail"
	mailMaxAttempts    = 5
	DefaultMailWorkers = 5
)

// Runtime shares Bun's pool and never closes it. Stop it before closing Bun.
type Runtime struct {
	client *river.Client[*sql.Tx]
	mail   bool
}

// Option configures optional work kinds. The import executor is always required.
type Option func(*options)

type options struct {
	mail        func(context.Context, string) error
	mailWorkers int
}

// WithMail binds the mail delivery executor to its own queue so slow SMTP
// sessions never occupy import workers. Concurrency below one uses the default.
func WithMail(execute func(context.Context, string) error, concurrency int) Option {
	return func(o *options) {
		o.mail = execute
		o.mailWorkers = concurrency
	}
}

// New binds the use cases without executing work or starting goroutines.
func New(db *bun.DB, execute func(context.Context, string) error, opts ...Option) (*Runtime, error) {
	if db == nil || execute == nil {
		return nil, fmt.Errorf("worker requires a database and import executor")
	}
	var configured options
	for _, opt := range opts {
		opt(&configured)
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &importWorker{execute: execute})
	queues := map[string]river.QueueConfig{"imports": {MaxWorkers: 2}}
	if configured.mail != nil {
		river.AddWorker(workers, &mailWorker{execute: configured.mail})
		if configured.mailWorkers < 1 {
			configured.mailWorkers = DefaultMailWorkers
		}
		queues[mailQueue] = river.QueueConfig{MaxWorkers: configured.mailWorkers}
	}
	client, err := river.NewClient(riverdatabasesql.New(db.DB), &river.Config{
		Workers:              workers,
		Queues:               queues,
		ErrorHandler:         &errorLogger{log: logger.New()},
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
	return &Runtime{client: client, mail: configured.mail != nil}, nil
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

// EnqueueMail records one delivery attempt in the caller's transaction. Mail
// jobs are deliberately not unique in River: the delivery record decides
// whether work remains, and River's required "running" uniqueness would let a
// job orphaned by a crash block a Curator's deliberate retry until the rescuer
// runs. A rescued job finds the record already settled and does nothing.
func (r *Runtime) EnqueueMail(ctx context.Context, tx bun.Tx, deliveryID string) error {
	if tx.Tx == nil || deliveryID == "" {
		return fmt.Errorf("enqueue mail requires a transaction and delivery ID")
	}
	if !r.mail {
		return fmt.Errorf("enqueue mail requires a mail executor")
	}
	_, err := r.client.InsertTx(ctx, tx.Tx, mailArgs{DeliveryID: deliveryID}, &river.InsertOpts{Queue: mailQueue, MaxAttempts: mailMaxAttempts})
	return runtimeError(ctx, "enqueue mail", err)
}

type mailWorker struct {
	river.WorkerDefaults[mailArgs]
	execute func(context.Context, string) error
}

// Timeout bounds one SMTP session well below the import limit.
func (w *mailWorker) Timeout(*river.Job[mailArgs]) time.Duration { return 2 * time.Minute }

func (w *mailWorker) Work(ctx context.Context, job *river.Job[mailArgs]) error {
	return work(ctx, job.Attempt, job.MaxAttempts, "deliver mail", func(ctx context.Context) error {
		return w.execute(ctx, job.Args.DeliveryID)
	})
}

// work runs one attempt with panic recovery, the final-attempt flag, and
// sanitized errors, shared by every job kind.
func work(ctx context.Context, attempt, maxAttempts int, operation string, fn func(context.Context) error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = runtimeError(ctx, operation, fmt.Errorf("%s executor panicked: %v", operation, recovered))
		}
	}()
	ctx = context.WithValue(ctx, finalAttemptKey{}, attempt >= maxAttempts)
	return runtimeError(ctx, operation, fn(ctx))
}

// errorLogger writes job failures to the application log with their captured
// cause, since River persists only the sanitized message.
type errorLogger struct{ log logger.Logger }

func (l *errorLogger) HandleError(_ context.Context, job *rivertype.JobRow, err error) *river.ErrorHandlerResult {
	l.log.Err(err).Error("job attempt failed", logger.Data{"kind": job.Kind, "queue": job.Queue, "attempt": job.Attempt, "max_attempts": job.MaxAttempts})
	return nil
}

func (l *errorLogger) HandlePanic(_ context.Context, job *rivertype.JobRow, panicVal any, trace string) *river.ErrorHandlerResult {
	l.log.Error("job attempt panicked", logger.Data{"kind": job.Kind, "queue": job.Queue, "attempt": job.Attempt, "panic": fmt.Sprint(panicVal), "trace": trace})
	return nil
}

type importWorker struct {
	river.WorkerDefaults[importArgs]
	execute func(context.Context, string) error
}

func (w *importWorker) Work(ctx context.Context, job *river.Job[importArgs]) error {
	return work(ctx, job.Attempt, job.MaxAttempts, "execute import", func(ctx context.Context) error {
		return w.execute(ctx, job.Args.AlbumID)
	})
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
