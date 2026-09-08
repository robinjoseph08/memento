package config

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadGoogle(t *testing.T) {
	t.Parallel()
	values := requiredConfig()
	values["auth_mode"] = "google"
	values["app_env"] = "production"
	values["public_url"] = "https://photos.example.com"
	values["google_client_id"] = "google-client"
	values["google_client_secret"] = "google-secret"
	values["google_callback_url"] = "https://photos.example.com/api/identity/google/callback"
	cfg, err := load(filepath.Join(t.TempDir(), "missing.yaml"), false, values, func() (string, error) { return "host", nil })
	require.NoError(t, err)
	require.Equal(t, "google", cfg.AuthMode)
	require.Equal(t, "google-client", cfg.GoogleClientID)
	require.Equal(t, "google-secret", cfg.GoogleClientSecret)
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NotContains(t, string(data), "google-secret")
}

func TestGoogleConfigurationRestrictions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, publicURL, callback, environment, missing, want string }{
		{name: "production HTTPS", publicURL: "https://photos.example.com", environment: "production"},
		{name: "manual localhost", publicURL: "http://localhost:3579", environment: "development"},
		{name: "test localhost", publicURL: "http://localhost:3579", environment: "test"},
		{name: "production HTTP", publicURL: "http://photos.example.com", environment: "production", want: "public_url:"},
		{name: "production localhost", publicURL: "http://localhost:3579", environment: "production", want: "public_url:"},
		{name: "dev remote HTTP", publicURL: "http://photos.example.com", environment: "development", want: "public_url:"},
		{name: "loopback IP", publicURL: "http://127.0.0.1:3579", environment: "development", want: "public_url:"},
		{name: "localhost suffix", publicURL: "http://localhost.example.com:3579", environment: "development", want: "public_url:"},
		{name: "client ID required", publicURL: "https://photos.example.com", environment: "production", missing: "google_client_id", want: "google_client_id: required"},
		{name: "client secret required", publicURL: "https://photos.example.com", environment: "production", missing: "google_client_secret", want: "google_client_secret: required"},
		{name: "callback required", publicURL: "https://photos.example.com", environment: "production", missing: "google_callback_url", want: "google_callback_url: required"},
		{name: "callback origin", publicURL: "https://photos.example.com", environment: "production", callback: "https://evil.example.com/api/identity/google/callback", want: "google_callback_url:"},
		{name: "callback port", publicURL: "http://localhost:3579", environment: "development", callback: "http://localhost:3000/api/identity/google/callback", want: "google_callback_url:"},
		{name: "callback query", publicURL: "https://photos.example.com", environment: "production", callback: "https://photos.example.com/api/identity/google/callback?secret", want: "google_callback_url:"},
		{name: "callback path", publicURL: "https://photos.example.com", environment: "production", callback: "https://photos.example.com/api/identity/google/callback/", want: "google_callback_url:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			values := requiredConfig()
			values["auth_mode"] = "google"
			values["app_env"] = tc.environment
			values["public_url"] = tc.publicURL
			values["google_client_id"] = "client"
			values["google_client_secret"] = "secret"
			values["google_callback_url"] = tc.publicURL + "/api/identity/google/callback"
			if tc.callback != "" {
				values["google_callback_url"] = tc.callback
			}
			if tc.missing != "" {
				values[tc.missing] = " "
			}
			_, err := load(filepath.Join(t.TempDir(), "missing.yaml"), false, values, func() (string, error) { return "host", nil })
			if tc.want == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.want)
				require.NotContains(t, err.Error(), "?secret")
			}
		})
	}
}
