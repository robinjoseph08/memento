package archives

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/internal/mediaavailability"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type blockedArchiveBody struct {
	contents *bytes.Reader
	started  chan struct{}
	release  <-chan struct{}
	once     sync.Once
	closed   bool
}

func (body *blockedArchiveBody) Read(contents []byte) (int, error) {
	body.once.Do(func() {
		close(body.started)
		<-body.release
	})
	return body.contents.Read(contents)
}

func (body *blockedArchiveBody) Close() error {
	body.closed = true
	return nil
}

func TestRewriteArchiveHonorsCancellationAndClosesUpstream(t *testing.T) {
	var source bytes.Buffer
	writer := zip.NewWriter(&source)
	entry, err := writer.Create("private-source.jpg")
	require.NoError(t, err)
	_, err = io.WriteString(entry, "contents")
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	started := make(chan struct{})
	release := make(chan struct{})
	body := &blockedArchiveBody{
		contents: bytes.NewReader(source.Bytes()), started: started, release: release,
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, _, rewriteErr := rewriteArchive(ctx, body, int64(len("contents")), []plannedItem{{EntryName: "Event/0001-media"}})
		result <- rewriteErr
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("archive rewrite did not start before the deadline")
	}
	cancel()
	close(release)

	var rewriteErr error
	select {
	case rewriteErr = <-result:
	case <-time.After(5 * time.Second):
		t.Fatal("canceled archive rewrite did not return before the deadline")
	}
	require.ErrorIs(t, rewriteErr, context.Canceled, "cancellation should stop archive rewriting")
	assert.True(t, body.closed, "cancellation should close the Immich body")
}

type budgetArchiveSource struct {
	mu      sync.Mutex
	calls   int
	bounded bool
}

func (*budgetArchiveSource) ArchiveInfo(context.Context, []uuid.UUID) ([]immich.ArchivePart, error) {
	return nil, nil
}

func (*budgetArchiveSource) Archive(context.Context, []uuid.UUID) (immich.ArchiveResponse, error) {
	return immich.ArchiveResponse{}, nil
}

func (source *budgetArchiveSource) Original(ctx context.Context, _ uuid.UUID, _ immich.MediaRequest) (immich.MediaResponse, error) {
	_, bounded := ctx.Deadline()
	source.mu.Lock()
	source.calls++
	source.bounded = source.bounded || bounded
	source.mu.Unlock()
	return immich.MediaResponse{Body: io.NopCloser(bytes.NewReader([]byte{'x'}))}, nil
}

func TestAggregateArchiveMissingVerificationHasWorkBudget(t *testing.T) {
	backings := make([]mediaavailability.Backing, maximumSelection)
	for index := range backings {
		backings[index] = mediaavailability.Backing{
			MediaID: uuid.New(), BackingID: uuid.New(), AssetID: uuid.New(),
		}
	}
	source := &budgetArchiveSource{}
	err := New(nil, source).markVerifiedSourceMissing(context.Background(), backings)
	require.ErrorIs(t, err, ErrUnavailable)
	source.mu.Lock()
	defer source.mu.Unlock()
	assert.Equal(t, missingVerificationWorkBudget, source.calls)
	assert.True(t, source.bounded, "all archive probes share an aggregate deadline")
}
