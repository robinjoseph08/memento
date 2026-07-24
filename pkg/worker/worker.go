// Package worker runs bounded PostgreSQL-backed jobs inside the application process.
package worker

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/uptrace/bun"
)

var (
	errDatabaseRequired   = errors.New("worker database is required")
	errLeaseOwnerRequired = errors.New("worker lease owner is required")
	errLeaseOwnershipLost = errors.New("lease ownership lost")
)

// Job is a leased unit of work.
type Job struct {
	ID         int64
	Kind       string
	Payload    json.RawMessage
	Attempts   int
	LeaseOwner string
}

// Handler processes one job. It must honor context cancellation.
type Handler func(context.Context, Job) error

// Dispatcher durably hands one leased outbox event to the jobs table.
type Dispatcher interface {
	Dispatch(ctx context.Context, owner string, lease time.Duration) (bool, error)
}

type handlerFailure struct {
	diagnostic string
	retryAfter time.Duration
	permanent  bool
}

func (e *handlerFailure) Error() string { return e.diagnostic }

// RetryAfter returns a secret-safe retry result for a handler.
func RetryAfter(delay time.Duration, diagnostic string) error {
	return &handlerFailure{diagnostic: diagnostic, retryAfter: delay}
}

// Permanent returns a secret-safe terminal result for a handler.
func Permanent(diagnostic string) error {
	return &handlerFailure{diagnostic: diagnostic, permanent: true}
}

// Option configures optional worker infrastructure.
type Option func(*Worker)

// WithDispatcher enables transactional outbox dispatch before ordinary claims.
func WithDispatcher(dispatcher Dispatcher) Option {
	return func(w *Worker) { w.dispatcher = dispatcher }
}

// Worker polls PostgreSQL, heartbeats, and owns at most one active job at a time.
type Worker struct {
	db         *bun.DB
	cfg        config.WorkerConfig
	owner      string
	handlers   map[string]Handler
	dispatcher Dispatcher

	claimsOpen   atomic.Bool
	heartbeat    atomic.Int64
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	mu           sync.Mutex
	activeID     int64
	activeCancel context.CancelFunc
}

// New constructs a worker with a process-unique lease owner.
func New(db *bun.DB, cfg config.WorkerConfig, owner string, handlers map[string]Handler, options ...Option) (*Worker, error) {
	if db == nil {
		return nil, errDatabaseRequired
	}
	if owner == "" {
		return nil, errLeaseOwnerRequired
	}
	if handlers == nil {
		handlers = map[string]Handler{}
	}
	w := &Worker{db: db, cfg: cfg, owner: owner, handlers: handlers}
	for _, option := range options {
		option(w)
	}
	return w, nil
}

// Start begins polling and records a heartbeat immediately.
func (w *Worker) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	w.cancel = cancel
	w.claimsOpen.Store(true)
	w.heartbeat.Store(time.Now().UnixNano())
	w.wg.Add(1)
	go w.run(ctx)
}

func (w *Worker) run(ctx context.Context) {
	defer w.wg.Done()
	poll := time.NewTicker(w.cfg.PollInterval)
	heartbeat := time.NewTicker(w.cfg.HeartbeatInterval)
	defer poll.Stop()
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-heartbeat.C:
			w.heartbeat.Store(now.UnixNano())
			if err := w.heartbeatLease(ctx); err != nil {
				w.cancelActive()
			}
		case <-poll.C:
			if !w.claimsOpen.Load() || w.hasActiveJob() {
				continue
			}
			if w.dispatcher != nil {
				if _, err := w.dispatcher.Dispatch(ctx, w.owner, w.cfg.LeaseDuration); err != nil {
					continue
				}
			}
			if len(w.handlers) != 0 {
				w.claimAndRun(ctx)
			}
		}
	}
}

func (w *Worker) claimAndRun(ctx context.Context) {
	job, err := w.claim(ctx)
	if err != nil || job == nil {
		return
	}
	jobCtx, cancel := context.WithCancel(ctx)
	w.mu.Lock()
	w.activeID = job.ID
	w.activeCancel = cancel
	w.mu.Unlock()

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer cancel()
		handlerErr := w.handlers[job.Kind](jobCtx, *job)
		finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(jobCtx), 5*time.Second)
		defer finalizeCancel()
		if handlerErr == nil {
			if err := w.complete(finalizeCtx, job.ID); err != nil {
				_ = w.retry(finalizeCtx, job.ID, 0, "completion_interrupted")
			}
		} else {
			var failure *handlerFailure
			if errors.As(handlerErr, &failure) && failure.permanent {
				_ = w.fail(finalizeCtx, job.ID, failure.diagnostic)
			} else {
				delay := w.retryDelay(job.Attempts)
				diagnostic := "handler_unavailable"
				if errors.As(handlerErr, &failure) {
					delay = failure.retryAfter
					diagnostic = failure.diagnostic
				}
				if errors.Is(handlerErr, context.Canceled) {
					delay = 0
				}
				_ = w.retry(finalizeCtx, job.ID, delay, diagnostic)
			}
		}
		w.mu.Lock()
		if w.activeID == job.ID {
			w.activeID = 0
			w.activeCancel = nil
		}
		w.mu.Unlock()
	}()
}

func (w *Worker) hasActiveJob() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.activeID != 0
}

func (w *Worker) cancelActive() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.activeCancel != nil {
		w.activeCancel()
	}
}

func (w *Worker) heartbeatLease(ctx context.Context) error {
	w.mu.Lock()
	id := w.activeID
	w.mu.Unlock()
	if id == 0 {
		return nil
	}
	result, err := w.db.NewRaw(`
		UPDATE jobs SET lease_expires_at = now() + (? * interval '1 microsecond'), updated_at = now()
		WHERE id = ? AND status = 'running' AND lease_owner = ? AND lease_expires_at > now()
	`, w.cfg.LeaseDuration.Microseconds(), id, w.owner).Exec(ctx)
	if err != nil {
		return fmt.Errorf("heartbeat job: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return fmt.Errorf("heartbeat job: %w", errLeaseOwnershipLost)
	}
	return nil
}

func (w *Worker) claim(ctx context.Context) (*Job, error) {
	kinds := make([]string, 0, len(w.handlers))
	for kind := range w.handlers {
		kinds = append(kinds, kind)
	}
	if len(kinds) == 0 {
		return nil, nil
	}
	var job Job
	err := w.db.NewRaw(`
		WITH candidate AS (
			SELECT id FROM jobs
			WHERE kind IN (?)
			  AND available_at <= now()
			  AND (status = 'pending' OR (status = 'running' AND lease_expires_at <= now()))
			ORDER BY available_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE jobs AS job
		SET status = 'running', lease_owner = ?, lease_expires_at = now() + (? * interval '1 microsecond'), updated_at = now()
		FROM candidate
		WHERE job.id = candidate.id
		RETURNING job.id, job.kind, job.payload, job.attempts
	`, bun.List(kinds), w.owner, w.cfg.LeaseDuration.Microseconds()).Scan(ctx, &job.ID, &job.Kind, &job.Payload, &job.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim job: %w", err)
	}
	job.LeaseOwner = w.owner
	return &job, nil
}

func (w *Worker) complete(ctx context.Context, id int64) error {
	result, err := w.db.NewRaw(`
		UPDATE jobs SET status = 'completed', lease_owner = NULL, lease_expires_at = NULL, updated_at = now()
		WHERE id = ? AND status = 'running' AND lease_owner = ? AND lease_expires_at > now()
	`, id, w.owner).Exec(ctx)
	if err != nil {
		return fmt.Errorf("complete job: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return fmt.Errorf("complete job: %w", errLeaseOwnershipLost)
	}
	return nil
}

func (w *Worker) retry(ctx context.Context, id int64, delay time.Duration, diagnostic string) error {
	result, err := w.db.NewRaw(`
		UPDATE jobs
		SET status = 'pending', attempts = attempts + 1,
			available_at = now() + (? * interval '1 microsecond'), last_safe_error = ?,
			lease_owner = NULL, lease_expires_at = NULL, updated_at = now()
		WHERE id = ? AND status = 'running' AND lease_owner = ? AND lease_expires_at > now()
	`, delay.Microseconds(), diagnostic, id, w.owner).Exec(ctx)
	if err != nil {
		return fmt.Errorf("retry job: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return fmt.Errorf("retry job: %w", errLeaseOwnershipLost)
	}
	return nil
}

func (w *Worker) fail(ctx context.Context, id int64, diagnostic string) error {
	result, err := w.db.NewRaw(`
		UPDATE jobs
		SET status = 'failed', attempts = attempts + 1, last_safe_error = ?,
			lease_owner = NULL, lease_expires_at = NULL, updated_at = now()
		WHERE id = ? AND status = 'running' AND lease_owner = ? AND lease_expires_at > now()
	`, diagnostic, id, w.owner).Exec(ctx)
	if err != nil {
		return fmt.Errorf("fail job: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return fmt.Errorf("fail job: %w", errLeaseOwnershipLost)
	}
	return nil
}

func (w *Worker) retryDelay(attempts int) time.Duration {
	delay := w.cfg.RetryBase
	for range attempts {
		if delay >= w.cfg.RetryMax/2 {
			delay = w.cfg.RetryMax
			break
		}
		delay *= 2
	}
	if delay > w.cfg.RetryMax {
		delay = w.cfg.RetryMax
	}
	return jitter(delay, w.cfg.RetryMax)
}

func jitter(delay, maximum time.Duration) time.Duration {
	spread := delay / 5
	if spread <= 0 {
		return delay
	}
	value, err := rand.Int(rand.Reader, big.NewInt(int64(2*spread)+1))
	if err != nil {
		return delay
	}
	result := delay - spread + time.Duration(value.Int64())
	if result > maximum {
		return maximum
	}
	return result
}

// StopClaims prevents any subsequent job claim and cancels active dependency work.
func (w *Worker) StopClaims() {
	w.claimsOpen.Store(false)
	w.cancelActive()
	if w.cancel != nil {
		w.cancel()
	}
}

// Drain waits for active work, then releases any remaining owned lease.
func (w *Worker) Drain(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		// The bounded lease will expire even if a handler ignored cancellation.
		return fmt.Errorf("drain worker: %w", ctx.Err())
	}
	_, err := w.db.NewRaw(`
		UPDATE jobs
		SET status = 'pending', attempts = attempts + 1, available_at = now(), lease_owner = NULL, lease_expires_at = NULL, updated_at = now()
		WHERE status = 'running' AND lease_owner = ?
	`, w.owner).Exec(ctx)
	if err != nil {
		return fmt.Errorf("release worker leases: %w", err)
	}
	if ctx.Err() != nil {
		return fmt.Errorf("drain worker: %w", ctx.Err())
	}
	return nil
}

// Healthy reports whether claims are open and the in-process heartbeat is fresh.
func (w *Worker) Healthy(maxAge time.Duration) bool {
	if !w.claimsOpen.Load() {
		return false
	}
	last := time.Unix(0, w.heartbeat.Load())
	return !last.IsZero() && time.Since(last) <= maxAge
}
