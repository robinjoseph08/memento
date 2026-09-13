package immich_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/ffprobe"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlaybackForwardsOneRangeAndKeepsPartialSemantics(t *testing.T) {
	t.Parallel()
	content := "0123456789abcdef"
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/assets/clip%2F1/video/playback", r.RequestURI)
		assert.Equal(t, "read-key", r.Header.Get("X-Api-Key"))
		assert.Empty(t, r.Header.Get("If-Range"), "Memento owns validators; upstream never sees them")
		assert.Empty(t, r.Header.Get("If-None-Match"))
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Accept-Ranges", "bytes")
		switch r.Header.Get("Range") {
		case "":
			w.Header().Set("Content-Length", fmt.Sprint(len(content)))
			w.WriteHeader(http.StatusOK)
			if r.Method == http.MethodGet {
				_, _ = io.WriteString(w, content)
			}
		case "bytes=4-7":
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 4-7/%d", len(content)))
			w.Header().Set("Content-Length", "4")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = io.WriteString(w, content[4:8])
		case "bytes=99-":
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", len(content)))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		default:
			t.Errorf("unexpected range %q", r.Header.Get("Range"))
		}
	}))
	t.Cleanup(fixture.Close)
	client := immich.New(fixture.URL, "read-key")
	full, err := client.Playback(t.Context(), "clip/1", immich.PlaybackRequest{})
	require.NoError(t, err)
	body, _ := io.ReadAll(full.Body)
	_ = full.Body.Close()
	assert.Equal(t, http.StatusOK, full.StatusCode)
	assert.Equal(t, content, string(body))
	assert.Equal(t, "video/mp4", full.ContentType)
	assert.Equal(t, int64(len(content)), full.ContentLength)
	assert.Empty(t, full.ContentRange)
	assert.True(t, full.AcceptRanges)

	partial, err := client.Playback(t.Context(), "clip/1", immich.PlaybackRequest{Range: "bytes=4-7"})
	require.NoError(t, err)
	body, _ = io.ReadAll(partial.Body)
	_ = partial.Body.Close()
	assert.Equal(t, http.StatusPartialContent, partial.StatusCode)
	assert.Equal(t, "4567", string(body))
	assert.Equal(t, "bytes 4-7/16", partial.ContentRange)
	assert.Equal(t, int64(4), partial.ContentLength)

	unsatisfiable, err := client.Playback(t.Context(), "clip/1", immich.PlaybackRequest{Range: "bytes=99-"})
	require.NoError(t, err)
	_ = unsatisfiable.Body.Close()
	assert.Equal(t, http.StatusRequestedRangeNotSatisfiable, unsatisfiable.StatusCode)
	assert.Equal(t, "bytes */16", unsatisfiable.ContentRange)

	head, err := client.Playback(t.Context(), "clip/1", immich.PlaybackRequest{Head: true})
	require.NoError(t, err)
	body, _ = io.ReadAll(head.Body)
	_ = head.Body.Close()
	assert.Equal(t, http.StatusOK, head.StatusCode)
	assert.Empty(t, body)
	assert.Equal(t, int64(len(content)), head.ContentLength)
}

func TestPlaybackContentTypesAndUpstreamFailuresStaySafe(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		status      int
		contentType string
		want        string
		wantCode    int
	}{
		"webm":      {http.StatusOK, "video/webm; codecs=vp8", "video/webm", 0},
		"quicktime": {http.StatusOK, "video/quicktime", "video/quicktime", 0},
		"html":      {http.StatusOK, "text/html", "application/octet-stream", 0},
		"missing":   {http.StatusNotFound, "", "", http.StatusNotFound},
		"denied":    {http.StatusForbidden, "", "", http.StatusForbidden},
		"broken":    {http.StatusInternalServerError, "", "", http.StatusBadGateway},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.contentType != "" {
					w.Header().Set("Content-Type", tc.contentType)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, "private-key in body")
			}))
			t.Cleanup(fixture.Close)
			playback, err := immich.New(fixture.URL, "private-key").Playback(t.Context(), "clip", immich.PlaybackRequest{})
			if tc.wantCode != 0 {
				require.Error(t, err)
				var coded *errcodes.Error
				require.ErrorAs(t, err, &coded)
				assert.Equal(t, tc.wantCode, coded.HTTPCode)
				assert.NotContains(t, fmt.Sprintf("%+v", err), "private-key")
				return
			}
			require.NoError(t, err)
			_ = playback.Body.Close()
			assert.Equal(t, tc.want, playback.ContentType)
		})
	}
}

func TestPlaybackRejectsMissingIDAndCancellation(t *testing.T) {
	t.Parallel()
	_, err := immich.New("http://127.0.0.1:9", "key").Playback(t.Context(), "", immich.PlaybackRequest{})
	require.Error(t, err)
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(fixture.Close)
	ctx, cancel := context.WithCancel(t.Context())
	playback, err := immich.New(fixture.URL, "key").Playback(ctx, "clip", immich.PlaybackRequest{})
	require.NoError(t, err)
	cancel()
	_, err = io.ReadAll(playback.Body)
	require.ErrorIs(t, err, context.Canceled)
	_ = playback.Body.Close()
}

type recordingProbe struct {
	url     string
	headers map[string]string
	result  []ffprobe.Chapter
	err     error
}

func (p *recordingProbe) Chapters(_ context.Context, url string, headers map[string]string) ([]ffprobe.Chapter, error) {
	p.url, p.headers = url, headers
	return p.result, p.err
}

func TestChaptersProbeTheAuthenticatedOriginalURL(t *testing.T) {
	t.Parallel()
	probe := &recordingProbe{result: []ffprobe.Chapter{{Title: "Cake", Start: 2, End: 4}}}
	client := immich.New("https://immich.example.test/photos/", "read-key")
	client.Probe = probe
	chapters, err := client.Chapters(t.Context(), "clip/1 ?")
	require.NoError(t, err)
	assert.Equal(t, probe.result, chapters)
	assert.Equal(t, "https://immich.example.test/photos/api/assets/clip%2F1%20%3F/original", probe.url)
	assert.Equal(t, map[string]string{"X-Api-Key": "read-key"}, probe.headers)
	_, err = client.Chapters(t.Context(), "")
	require.Error(t, err)
	unconfigured := immich.New("https://immich.example.test", "read-key")
	_, err = unconfigured.Chapters(t.Context(), "clip")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "read-key")
	_, err = immich.New("not a url", "read-key").Chapters(t.Context(), "clip")
	require.Error(t, err, "invalid configuration never reaches the probe")
}

func TestOriginalAcceptsVideoContentTypes(t *testing.T) {
	t.Parallel()
	for contentType, want := range map[string]string{"video/webm": "video/webm", "video/mp4; codecs=avc1": "video/mp4", "image/jpeg": "image/jpeg", "text/html": "application/octet-stream"} {
		t.Run(contentType, func(t *testing.T) {
			t.Parallel()
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", contentType)
				_, _ = io.WriteString(w, "bytes")
			}))
			t.Cleanup(fixture.Close)
			original, err := immich.New(fixture.URL, "key").Original(t.Context(), "clip")
			require.NoError(t, err)
			_ = original.Body.Close()
			assert.Equal(t, want, original.ContentType)
		})
	}
}
