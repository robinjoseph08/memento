package lifecycle

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recorder struct {
	mu    sync.Mutex
	steps []string
}

func (r *recorder) add(step string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps = append(r.steps, step)
}

type fakeGate struct{ r *recorder }

func (f fakeGate) SetDraining() { f.r.add("unready") }

type fakeServer struct {
	r     *recorder
	err   error
	block bool
}

func (f fakeServer) Shutdown(ctx context.Context) error {
	f.r.add("http")
	if f.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return f.err
}

type fakeWorker struct {
	r     *recorder
	err   error
	block bool
}

func (f fakeWorker) StopClaims() { f.r.add("stop_claims") }
func (f fakeWorker) Drain(ctx context.Context) error {
	f.r.add("worker")
	if f.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return f.err
}

type fakeDatabase struct {
	r   *recorder
	err error
}

func (f fakeDatabase) Close() error { f.r.add("database"); return f.err }

func TestShutdownOrdersReadinessClaimsDrainAndClose(t *testing.T) {
	r := &recorder{}
	err := Shutdown(context.Background(), time.Second, fakeGate{r}, fakeServer{r: r}, fakeWorker{r: r}, fakeDatabase{r: r})
	require.NoError(t, err)
	assert.Equal(t, []string{"unready", "stop_claims", "http", "worker", "database"}, r.steps)
}

func TestShutdownClosesDatabaseAndJoinsEveryFailure(t *testing.T) {
	r := &recorder{}
	err := Shutdown(
		context.Background(), time.Second, fakeGate{r},
		fakeServer{r: r, err: errors.New("HTTP failure")},
		fakeWorker{r: r, err: errors.New("worker failure")},
		fakeDatabase{r: r, err: errors.New("database failure")},
	)
	require.Error(t, err)
	require.ErrorContains(t, err, "HTTP failure")
	require.ErrorContains(t, err, "worker failure")
	require.ErrorContains(t, err, "database failure")
	assert.Equal(t, "database", r.steps[len(r.steps)-1])
}

func TestShutdownUsesWorkerDrainTimeout(t *testing.T) {
	r := &recorder{}
	started := time.Now()
	err := Shutdown(context.Background(), 5*time.Millisecond, fakeGate{r}, fakeServer{r: r}, fakeWorker{r: r, block: true}, fakeDatabase{r: r})
	require.ErrorContains(t, err, "context deadline exceeded")
	assert.Less(t, time.Since(started), 100*time.Millisecond)
	assert.Equal(t, "database", r.steps[len(r.steps)-1])
}

func TestShutdownUsesCallerDeadline(t *testing.T) {
	r := &recorder{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := Shutdown(ctx, time.Second, fakeGate{r}, fakeServer{r: r, block: true}, fakeWorker{r: r}, fakeDatabase{r: r})
	require.ErrorContains(t, err, "context deadline exceeded")
	assert.Less(t, time.Since(started), 100*time.Millisecond)
	assert.Equal(t, "database", r.steps[len(r.steps)-1])
}
