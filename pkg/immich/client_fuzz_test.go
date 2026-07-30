package immich

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func FuzzMediaRequestHeaders(f *testing.F) {
	f.Add("bytes=0-99", "", `"v1"`, "")
	f.Add("bytes=-1", `"v1"`, "", "")
	f.Add("bytes=0-1,3-4", "", "", "")
	f.Add("", "", "", "Mon, 27 Jul 2026 12:00:00 GMT")
	f.Fuzz(func(t *testing.T, byteRange, ifRange, ifNoneMatch, ifModifiedSince string) {
		if len(byteRange)+len(ifRange)+len(ifNoneMatch)+len(ifModifiedSince) > 16<<10 {
			t.Skip()
		}
		request := MediaRequest{Range: byteRange, IfRange: ifRange, IfNoneMatch: ifNoneMatch, IfModifiedSince: ifModifiedSince}
		if !validMediaRequest(request) {
			return
		}
		if request.Range != "" {
			ranges, valid := parseRequestedMediaRanges(request.Range)
			require.True(t, valid)
			require.Len(t, ranges, 1)
			require.Equal(t, request.Range, canonicalMediaRange(ranges[0]))
		}
		header := make(http.Header)
		setMediaRequestHeaders(header, request)
		assert.Equal(t, request.Range, header.Get("Range"))
		assert.Equal(t, request.IfRange, header.Get("If-Range"))
	})
}

func FuzzMediaRedirectOrigin(f *testing.F) {
	f.Add("https://immich.example/api/assets/1")
	f.Add("https://evil.example/api/assets/1")
	f.Add("https://user:secret@immich.example/api/assets/1")
	f.Add("file:///private/media")
	f.Fuzz(func(t *testing.T, target string) {
		if len(target) > 16<<10 {
			t.Skip()
		}
		base, err := url.Parse("https://immich.example:443/api/")
		require.NoError(t, err)
		parsed, err := url.Parse(target)
		if err != nil || !sameOrigin(base, parsed) {
			return
		}
		assert.Nil(t, parsed.User)
		assert.True(t, strings.EqualFold(base.Scheme, parsed.Scheme))
		assert.True(t, strings.EqualFold(base.Hostname(), parsed.Hostname()))
		assert.Equal(t, effectivePort(base), effectivePort(parsed))
	})
}

func FuzzNormalizedMediaResponse(f *testing.F) {
	f.Add(200, "image/jpeg", "12", "", "bytes", []byte("image"), false, false)
	f.Add(206, "video/mp4", "5", "bytes 0-4/5", "bytes", []byte("video"), true, true)
	f.Add(302, "text/html", "", "", "", []byte("redirect"), false, false)
	f.Fuzz(func(t *testing.T, status int, contentType, contentLength, contentRange, acceptRanges string, body []byte, bounded, original bool) {
		if len(contentType)+len(contentLength)+len(contentRange)+len(acceptRanges)+len(body) > 64<<10 {
			t.Skip()
		}
		status = 100 + int(uint(status)%500)
		response := &http.Response{
			StatusCode: status,
			Header: http.Header{
				"Content-Type":   []string{contentType},
				"Content-Length": []string{contentLength},
				"Content-Range":  []string{contentRange},
				"Accept-Ranges":  []string{acceptRanges},
			},
			Body: io.NopCloser(bytes.NewReader(body)),
		}
		normalized, err := normalizeMediaResponse(response, MediaRequest{}, bounded, original)
		if normalized.Body != nil {
			require.NoError(t, normalized.Body.Close())
		}
		if err == nil {
			assert.NotContains(t, normalized.ContentType, "\n")
			assert.NotContains(t, normalized.ContentRange, "\n")
		} else {
			for _, privateValue := range []string{contentType, contentLength, contentRange, acceptRanges, string(body)} {
				if len(privateValue) > 32 {
					assert.NotContains(t, err.Error(), privateValue, "normalization errors must not echo upstream metadata")
				}
			}
		}
	})
}

func FuzzNormalizedImmichJSON(f *testing.F) {
	f.Add([]byte(`{"id":"one","nested":{"value":1}}`))
	f.Add([]byte(`{"id":"one","ID":"two"}`))
	f.Add([]byte(`[{"id":"one"},{"id":"two"}]`))
	f.Add([]byte(`{"id":`))
	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > 64<<10 {
			t.Skip()
		}
		if validateUniqueJSON(body) == nil {
			assert.True(t, json.Valid(body))
		}
	})
}
