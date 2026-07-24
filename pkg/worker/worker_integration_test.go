//go:build integration

package worker

import (
	"context"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testConfig() config.WorkerConfig {
	return config.WorkerConfig{
		PollInterval:      5 * time.Millisecond,
		HeartbeatInterval: 5 * time.Millisecond,
		HeartbeatMaxAge:   time.Second,
		LeaseDuration:     time.Second,
		DrainTimeout:      time.Second,
	}
}

func TestShutdownStopsClaimsAndMakesLeaseReclaimable(t *testing.T) {
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(context.Background(), db))
	var id int64
	require.NoError(t, db.NewRaw(`INSERT INTO jobs (kind) VALUES ('blocking') RETURNING id`).Scan(context.Background(), &id))
	started := make(chan struct{})
	jobWorker, err := New(db, testConfig(), "shutdown-owner", map[string]Handler{
		"blocking": func(ctx context.Context, _ Job) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
	})
	require.NoError(t, err)
	jobWorker.Start(context.Background())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not claim the job")
	}
	assert.True(t, jobWorker.Healthy(time.Second))

	jobWorker.StopClaims()
	drainCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, jobWorker.Drain(drainCtx))
	assert.False(t, jobWorker.Healthy(time.Second))

	var status string
	var owner *string
	require.NoError(t, db.NewRaw(`SELECT status, lease_owner FROM jobs WHERE id = ?`, id).Scan(context.Background(), &status, &owner))
	assert.Equal(t, "pending", status)
	assert.Nil(t, owner)
}

func TestHeartbeatPreventsASecondWorkerFromReclaimingLiveWork(t *testing.T) {
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(context.Background(), db))
	var id int64
	require.NoError(t, db.NewRaw(`INSERT INTO jobs (kind) VALUES ('blocking') RETURNING id`).Scan(context.Background(), &id))

	cfg := testConfig()
	cfg.HeartbeatInterval = 50 * time.Millisecond
	cfg.LeaseDuration = 2 * time.Second
	started := make(chan struct{})
	release := make(chan struct{})
	first, err := New(db, cfg, "first-owner", map[string]Handler{
		"blocking": func(context.Context, Job) error {
			close(started)
			<-release
			return nil
		},
	})
	require.NoError(t, err)
	first.Start(context.Background())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first worker did not claim the job")
	}

	var observedExpiry time.Time
	require.NoError(t, db.NewRaw(`SELECT lease_expires_at FROM jobs WHERE id = ?`, id).Scan(context.Background(), &observedExpiry))
	require.Eventually(t, func() bool {
		var passed bool
		err := db.NewRaw(`SELECT now() >= ?`, observedExpiry).Scan(context.Background(), &passed)
		return err == nil && passed
	}, cfg.LeaseDuration+time.Second, 25*time.Millisecond)

	second, err := New(db, cfg, "second-owner", map[string]Handler{
		"blocking": func(context.Context, Job) error { return nil },
	})
	require.NoError(t, err)
	duplicate, err := second.claim(context.Background())
	require.NoError(t, err)
	assert.Nil(t, duplicate, "live lease was reclaimed after its originally observed expiry")

	var owner string
	var expiresInFuture bool
	require.NoError(t, db.NewRaw(`
		SELECT lease_owner, lease_expires_at > now()
		FROM jobs WHERE id = ?
	`, id).Scan(context.Background(), &owner, &expiresInFuture))
	assert.Equal(t, "first-owner", owner)
	assert.True(t, expiresInFuture)
	assert.True(t, first.Healthy(time.Second), "job execution must not stall the process heartbeat")

	close(release)
	first.StopClaims()
	drainCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, first.Drain(drainCtx))
}

func TestExpiredLeaseIsReclaimedAndCompleted(t *testing.T) {
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(context.Background(), db))
	var id int64
	require.NoError(t, db.NewRaw(`
		INSERT INTO jobs (kind, status, lease_owner, lease_expires_at)
		VALUES ('quick', 'running', 'dead-process', now() - interval '1 minute')
		RETURNING id
	`).Scan(context.Background(), &id))
	completed := make(chan struct{})
	jobWorker, err := New(db, testConfig(), "new-owner", map[string]Handler{
		"quick": func(context.Context, Job) error { close(completed); return nil },
	})
	require.NoError(t, err)
	jobWorker.Start(context.Background())
	defer func() {
		jobWorker.StopClaims()
		_ = jobWorker.Drain(context.Background())
	}()
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("worker did not reclaim expired lease")
	}

	require.Eventually(t, func() bool {
		var status string
		err := db.NewRaw(`SELECT status FROM jobs WHERE id = ?`, id).Scan(context.Background(), &status)
		return err == nil && status == "completed"
	}, time.Second, 10*time.Millisecond)
}

func TestCompletionRequiresLeaseOwnership(t *testing.T) {
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(context.Background(), db))
	var id int64
	require.NoError(t, db.NewRaw(`
		INSERT INTO jobs (kind, status, lease_owner, lease_expires_at)
		VALUES ('quick', 'running', 'another-owner', now() + interval '1 minute') RETURNING id
	`).Scan(context.Background(), &id))
	jobWorker, err := New(db, testConfig(), "this-owner", nil)
	require.NoError(t, err)
	assert.EqualError(t, jobWorker.complete(context.Background(), id), "complete job: lease ownership lost")
}
