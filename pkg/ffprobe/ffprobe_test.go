package ffprobe_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pkgerrors "github.com/pkg/errors"
	"github.com/robinjoseph08/memento/internal/testmedia"
	"github.com/robinjoseph08/memento/pkg/ffprobe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireFFprobe fails in CI, where the binary is installed, and skips on a
// developer machine that has not installed ffmpeg yet.
func requireFFprobe(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffprobe"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("ffprobe is required in CI")
		}
		t.Skip("ffprobe is not installed")
	}
}

type video struct {
	bytes       []byte
	status      int
	ranges      atomic.Int32
	keys        atomic.Int32
	rangeHeader atomic.Value
}

func (v *video) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Api-Key") == "private-key" {
		v.keys.Add(1)
	}
	if r.Header.Get("Range") != "" {
		v.ranges.Add(1)
		v.rangeHeader.Store(r.Header.Get("Range"))
	}
	if v.status != 0 {
		http.Error(w, "upstream detail with private-key", v.status)
		return
	}
	w.Header().Set("Content-Type", testmedia.ContentType)
	http.ServeContent(w, r, "", time.Time{}, strings.NewReader(string(v.bytes)))
}

func TestChaptersReadEmbeddedMetadataThroughRanges(t *testing.T) {
	t.Parallel()
	requireFFprobe(t)
	upstream := &video{bytes: testmedia.Chaptered}
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	chapters, err := ffprobe.Command{}.Chapters(t.Context(), server.URL+"/api/assets/clip/original", map[string]string{"X-Api-Key": "private-key"})
	require.NoError(t, err)
	assert.Equal(t, []ffprobe.Chapter{{Title: "Arrival", Start: 0, End: 2}, {Title: "Cake", Start: 2, End: 4}, {Title: "Goodbyes", Start: 4, End: 6}}, chapters)
	assert.Positive(t, upstream.keys.Load(), "the authenticated header reaches Immich")
	assert.Positive(t, upstream.ranges.Load(), "ffprobe asks for byte ranges instead of a plain download")
	assert.True(t, strings.HasPrefix(fmt.Sprint(upstream.rangeHeader.Load()), "bytes="))
}

func TestChaptersReportNoChaptersAsAnEmptySuccess(t *testing.T) {
	t.Parallel()
	requireFFprobe(t)
	server := httptest.NewServer(&video{bytes: testmedia.Plain})
	t.Cleanup(server.Close)
	chapters, err := ffprobe.Command{}.Chapters(t.Context(), server.URL+"/clip", nil)
	require.NoError(t, err)
	assert.Equal(t, []ffprobe.Chapter{}, chapters)
}

func TestChaptersFailuresAreRedactedAndStacked(t *testing.T) {
	t.Parallel()
	requireFFprobe(t)
	server := httptest.NewServer(&video{status: http.StatusForbidden})
	t.Cleanup(server.Close)
	_, err := ffprobe.Command{}.Chapters(t.Context(), server.URL+"/clip?private-key", map[string]string{"X-Api-Key": "private-key"})
	require.Error(t, err)
	rendered := fmt.Sprintf("%+v", err)
	assert.NotContains(t, rendered, "private-key")
	assert.NotContains(t, rendered, server.URL)
	assert.Contains(t, err.Error(), "ffprobe")
	var tracer interface{ StackTrace() pkgerrors.StackTrace }
	require.ErrorAs(t, err, &tracer)
	var failure *ffprobe.Error
	require.ErrorAs(t, err, &failure)
	assert.NotEmpty(t, failure.Detail)
}

func TestChaptersHonorContextDeadline(t *testing.T) {
	t.Parallel()
	requireFFprobe(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	t.Cleanup(cancel)
	started := time.Now()
	_, err := ffprobe.Command{}.Chapters(ctx, server.URL+"/clip", nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(started), 5*time.Second)
}

func TestMissingBinaryIsAnActionableError(t *testing.T) {
	t.Parallel()
	_, err := ffprobe.Command{Path: "/nonexistent/ffprobe"}.Chapters(t.Context(), "http://127.0.0.1:9/clip", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ffprobe")
	assert.NotContains(t, err.Error(), "127.0.0.1")
}

func TestCheckReportsTheInstalledVersion(t *testing.T) {
	t.Parallel()
	requireFFprobe(t)
	status := ffprobe.Command{}.Check(t.Context())
	assert.True(t, status.Usable, status.Message)
	assert.Regexp(t, `^\d+\.\d+`, status.Version)
	assert.Equal(t, "ffprobe is ready to read video chapters.", status.Message)
}

func TestCheckReportsAMissingBinaryWithoutThePath(t *testing.T) {
	t.Parallel()
	status := ffprobe.Command{Path: "/nonexistent/ffprobe"}.Check(t.Context())
	assert.False(t, status.Usable)
	assert.Empty(t, status.Version)
	assert.Contains(t, status.Message, "not found")
	assert.NotContains(t, status.Message, "/nonexistent")
}

func TestParseRejectsMalformedOutput(t *testing.T) {
	t.Parallel()
	for name, output := range map[string]string{
		"not json":       "{",
		"negative start": `{"chapters":[{"start_time":"-1","end_time":"2"}]}`,
		"non numeric":    `{"chapters":[{"start_time":"x","end_time":"2"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := ffprobe.Parse([]byte(output))
			require.Error(t, err)
		})
	}
	chapters, err := ffprobe.Parse([]byte(`{"chapters":[{"start_time":"4.5","end_time":"6","tags":{"title":"Late"}},{"start_time":"0","end_time":"4.5"}]}`))
	require.NoError(t, err)
	assert.Equal(t, []ffprobe.Chapter{{Title: "", Start: 0, End: 4.5}, {Title: "Late", Start: 4.5, End: 6}}, chapters, "chapters are ordered by start and untitled ones keep an empty title")
}
