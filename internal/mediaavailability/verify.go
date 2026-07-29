package mediaavailability

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
)

var errInvalidVerificationOptions = errors.New("invalid missing-media verification options")

// VerificationOptions bounds one aggregate dependency verification pass.
type VerificationOptions struct {
	Deadline     time.Duration
	MaxProbes    int
	Concurrency  int
	AfterAssetID uuid.UUID
}

// Verification contains only definitive per-asset evidence. Checked includes
// every backing whose probe succeeded, whether present or missing. Complete is
// false when the aggregate exceeded its work budget or any probe was uncertain.
// Cursor identifies the end of an incomplete deterministic work window.
type Verification struct {
	Checked  []Backing
	Missing  []Backing
	Complete bool
	Cursor   uuid.UUID
}

// VerifyMissing probes each distinct asset at most once with bounded aggregate
// time, concurrency, and work. A probe reports whether the asset still exists.
func VerifyMissing(
	ctx context.Context,
	backings []Backing,
	options VerificationOptions,
	probe func(context.Context, uuid.UUID) (bool, error),
) (Verification, error) {
	if len(backings) == 0 {
		return Verification{Complete: true}, nil
	}
	if options.MaxProbes <= 0 || options.Concurrency <= 0 || probe == nil {
		return Verification{}, errInvalidVerificationOptions
	}

	byAsset := make(map[uuid.UUID][]Backing, len(backings))
	assetIDs := make([]uuid.UUID, 0, len(backings))
	for _, backing := range backings {
		if existing, seen := byAsset[backing.AssetID]; seen {
			byAsset[backing.AssetID] = append(existing, backing)
			continue
		}
		byAsset[backing.AssetID] = []Backing{backing}
		assetIDs = append(assetIDs, backing.AssetID)
	}
	sort.Slice(assetIDs, func(left, right int) bool {
		return bytes.Compare(assetIDs[left][:], assetIDs[right][:]) < 0
	})
	budgetExceeded := len(assetIDs) > options.MaxProbes
	if budgetExceeded {
		start := sort.Search(len(assetIDs), func(index int) bool {
			return bytes.Compare(assetIDs[index][:], options.AfterAssetID[:]) > 0
		})
		selected := make([]uuid.UUID, 0, options.MaxProbes)
		for offset := range options.MaxProbes {
			selected = append(selected, assetIDs[(start+offset)%len(assetIDs)])
		}
		assetIDs = selected
	}

	verificationCtx := ctx
	cancel := func() {}
	if options.Deadline > 0 {
		verificationCtx, cancel = context.WithTimeout(ctx, options.Deadline)
	}
	defer cancel()

	type probeResult struct {
		exists bool
		err    error
	}
	results := make([]probeResult, len(assetIDs))
	jobs := make(chan int)
	workers := min(options.Concurrency, len(assetIDs))
	done := make(chan struct{}, workers)
	for range workers {
		go func() {
			defer func() { done <- struct{}{} }()
			for index := range jobs {
				if err := verificationCtx.Err(); err != nil {
					results[index].err = err
					continue
				}
				results[index].exists, results[index].err = probe(verificationCtx, assetIDs[index])
			}
		}()
	}
	for index := range assetIDs {
		jobs <- index
	}
	close(jobs)
	for range workers {
		<-done
	}

	result := Verification{
		Checked: make([]Backing, 0, len(assetIDs)), Missing: make([]Backing, 0),
		Complete: !budgetExceeded,
	}
	if budgetExceeded {
		result.Cursor = assetIDs[len(assetIDs)-1]
	}
	var firstErr error
	for index, checked := range results {
		if checked.err != nil {
			result.Complete = false
			if firstErr == nil {
				firstErr = checked.err
			}
			continue
		}
		result.Checked = append(result.Checked, byAsset[assetIDs[index]]...)
		if !checked.exists {
			result.Missing = append(result.Missing, byAsset[assetIDs[index]]...)
		}
	}
	return result, firstErr
}
