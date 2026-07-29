package mediaavailability

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyMissingRejectsInvalidOptions(t *testing.T) {
	backing := Backing{MediaID: uuid.New(), BackingID: uuid.New(), AssetID: uuid.New()}
	validProbe := func(context.Context, uuid.UUID) (bool, error) { return true, nil }
	tests := []struct {
		name    string
		options VerificationOptions
		probe   func(context.Context, uuid.UUID) (bool, error)
	}{
		{name: "work budget", options: VerificationOptions{MaxProbes: 0, Concurrency: 1}, probe: validProbe},
		{name: "concurrency", options: VerificationOptions{MaxProbes: 1, Concurrency: 0}, probe: validProbe},
		{name: "probe", options: VerificationOptions{MaxProbes: 1, Concurrency: 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verification, err := VerifyMissing(context.Background(), []Backing{backing}, test.options, test.probe)
			require.ErrorIs(t, err, errInvalidVerificationOptions)
			assert.False(t, verification.Complete)
			assert.Empty(t, verification.Missing)
		})
	}
}

func TestVerifyMissingBoundsAggregateWorkAndConcurrency(t *testing.T) {
	const (
		workBudget  = 12
		concurrency = 4
	)
	backings := make([]Backing, 100)
	for index := range backings {
		backings[index] = Backing{MediaID: uuid.New(), BackingID: uuid.New(), AssetID: uuid.New()}
	}

	started := make(chan struct{}, concurrency)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var mu sync.Mutex
	calls, active, maximumActive := 0, 0, 0
	probe := func(ctx context.Context, _ uuid.UUID) (bool, error) {
		deadline, bounded := ctx.Deadline()
		if !bounded || time.Until(deadline) <= 0 {
			return false, errors.New("probe context has no live deadline")
		}
		mu.Lock()
		calls++
		callNumber := calls
		active++
		maximumActive = max(maximumActive, active)
		mu.Unlock()
		if callNumber <= concurrency {
			started <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return false, ctx.Err()
			}
		}
		mu.Lock()
		active--
		mu.Unlock()
		return true, nil
	}

	type outcome struct {
		verification Verification
		err          error
	}
	finished := make(chan outcome, 1)
	go func() {
		verification, err := VerifyMissing(context.Background(), backings, VerificationOptions{
			Deadline: time.Minute, MaxProbes: workBudget, Concurrency: concurrency,
		}, probe)
		finished <- outcome{verification: verification, err: err}
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for startedCount := 0; startedCount < concurrency; startedCount++ {
		select {
		case <-started:
		case <-deadline.C:
			mu.Lock()
			observedCalls, observedActive, observedMaximum := calls, active, maximumActive
			mu.Unlock()
			t.Fatalf("timed out waiting for probes to start: started=%d/%d calls=%d active=%d maximum_active=%d", startedCount, concurrency, observedCalls, observedActive, observedMaximum)
		}
	}
	releaseOnce.Do(func() { close(release) })
	var completed outcome
	select {
	case completed = <-finished:
	case <-deadline.C:
		mu.Lock()
		observedCalls, observedActive, observedMaximum := calls, active, maximumActive
		mu.Unlock()
		t.Fatalf("timed out waiting for verification: calls=%d/%d active=%d maximum_active=%d", observedCalls, workBudget, observedActive, observedMaximum)
	}
	require.NoError(t, completed.err)
	assert.False(t, completed.verification.Complete)
	assert.Empty(t, completed.verification.Missing)
	assert.Equal(t, workBudget, calls)
	assert.Equal(t, concurrency, maximumActive)
}

func TestVerifyMissingRetainsOnlyPreciseEvidence(t *testing.T) {
	missingAsset, currentAsset, uncertainAsset := uuid.New(), uuid.New(), uuid.New()
	firstMissing := Backing{MediaID: uuid.New(), BackingID: uuid.New(), AssetID: missingAsset}
	secondMissing := Backing{MediaID: uuid.New(), BackingID: uuid.New(), AssetID: missingAsset}
	uncertainErr := errors.New("malformed dependency response")
	calls := make(map[uuid.UUID]int)

	verification, err := VerifyMissing(context.Background(), []Backing{
		firstMissing,
		{MediaID: uuid.New(), BackingID: uuid.New(), AssetID: currentAsset},
		secondMissing,
		{MediaID: uuid.New(), BackingID: uuid.New(), AssetID: uncertainAsset},
	}, VerificationOptions{Deadline: time.Minute, MaxProbes: 10, Concurrency: 1}, func(_ context.Context, assetID uuid.UUID) (bool, error) {
		calls[assetID]++
		switch assetID {
		case missingAsset:
			return false, nil
		case currentAsset:
			return true, nil
		default:
			return false, uncertainErr
		}
	})

	require.ErrorIs(t, err, uncertainErr)
	assert.False(t, verification.Complete)
	assert.Equal(t, []Backing{firstMissing, secondMissing}, verification.Missing)
	assert.Equal(t, map[uuid.UUID]int{missingAsset: 1, currentAsset: 1, uncertainAsset: 1}, calls)
}
