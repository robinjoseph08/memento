package archives

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

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
