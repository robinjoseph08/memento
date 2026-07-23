package immich

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func clientConfig(serverURL string) config.ImmichConfig {
	return config.ImmichConfig{URL: serverURL, APIKey: "secret-key", ExpectedVersion: "3.0.3", HealthTimeout: 100 * time.Millisecond}
}

func TestCheckValidatesVersionAndAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/server/version", r.URL.Path)
		assert.Equal(t, "secret-key", r.Header.Get("x-api-key"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"major":3,"minor":0,"patch":3}`))
	}))
	defer server.Close()
	client, err := New(clientConfig(server.URL), server.Client())
	require.NoError(t, err)
	require.NoError(t, client.Check(context.Background()))
}

func TestCheckReturnsSafeErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"status", http.StatusUnauthorized, "private dependency response", "Immich version check failed"},
		{"malformed", http.StatusOK, `{`, "Immich returned an invalid version"},
		{"unknown fields", http.StatusOK, `{"major":3,"minor":0,"patch":3,"url":"private"}`, "Immich returned an invalid version"},
		{"unsupported", http.StatusOK, `{"major":3,"minor":0,"patch":4}`, "Immich version is unsupported"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := New(clientConfig(server.URL), server.Client())
			require.NoError(t, err)
			err = client.Check(context.Background())
			require.EqualError(t, err, test.want)
			assert.NotContains(t, err.Error(), test.body)
			assert.NotContains(t, err.Error(), "secret-key")
		})
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial http://private.internal?key=secret")
}

func TestCheckRedactsTransportErrors(t *testing.T) {
	client, err := New(clientConfig("https://immich.internal"), &http.Client{Transport: failingTransport{}})
	require.NoError(t, err)
	err = client.Check(context.Background())
	require.EqualError(t, err, "Immich is unreachable")
}

func TestCheckTimesOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte(`{"major":3,"minor":0,"patch":3}`))
	}))
	defer server.Close()
	cfg := clientConfig(server.URL)
	cfg.HealthTimeout = time.Millisecond
	client, err := New(cfg, server.Client())
	require.NoError(t, err)
	require.EqualError(t, client.Check(context.Background()), "Immich is unreachable")
}

func TestNewRejectsInvalidURLWithoutEchoingIt(t *testing.T) {
	cfg := clientConfig("https://%zz-secret")
	_, err := New(cfg, nil)
	require.EqualError(t, err, "parse Immich URL")
	assert.False(t, strings.Contains(err.Error(), "secret"))
}
