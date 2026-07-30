package binder

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

type fuzzJSONPayload struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func FuzzStrictJSONBinding(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`{"name":"Alex","count":1}`),
		[]byte(`{"name":"Alex","unknown":true}`),
		[]byte(`{"name":"Alex"}{"count":1}`),
		[]byte(`null`),
		[]byte(`{"name":`),
		{0xff, 0x00, '{', '}'},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > 64<<10 {
			t.Skip()
		}
		request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/fuzz", bytes.NewReader(body))
		request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		e := echo.New()
		context := e.NewContext(request, httptest.NewRecorder())
		binder, err := New()
		require.NoError(t, err)
		var got fuzzJSONPayload
		err = binder.Bind(&got, context)
		if err != nil {
			if len(body) > 0 {
				require.NotContains(t, err.Error(), string(body), "binding errors must not echo raw request bodies")
			}
			return
		}

		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		var want fuzzJSONPayload
		require.NoError(t, decoder.Decode(&want))
		var trailing json.RawMessage
		require.ErrorIs(t, decoder.Decode(&trailing), io.EOF)
		require.Equal(t, want, got)
	})
}
