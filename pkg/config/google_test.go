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
	values["smtp_url"] = "smtp://mail.example.test"
	values["smtp_from"] = "memento@example.test"
	values["app_env"] = "production"
	values["public_url"] = "https://photos.example.com"
	values["google_client_id"] = "google-client"
	values["google_client_secret"] = "google-secret"
	cfg, err := load(filepath.Join(t.TempDir(), "missing.yaml"), false, values, func() (string, error) { return "host", nil })
	require.NoError(t, err)
	require.True(t, cfg.GoogleConfigured())
	require.Equal(t, "google-client", cfg.GoogleClientID)
	require.Equal(t, "google-secret", cfg.GoogleClientSecret)
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NotContains(t, string(data), "google-secret")
}

func TestGoogleConfigurationRestrictions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, publicURL, environment, missing, want string }{
		{name: "production HTTPS", publicURL: "https://photos.example.com", environment: "production"},
		{name: "manual localhost", publicURL: "http://localhost:3579", environment: "development"},
		{name: "test localhost", publicURL: "http://localhost:3579", environment: "test"},
		{name: "production HTTP", publicURL: "http://photos.example.com", environment: "production", want: "public_url:"},
		{name: "production localhost", publicURL: "http://localhost:3579", environment: "production", want: "public_url:"},
		{name: "dev remote HTTP", publicURL: "http://photos.example.com", environment: "development", want: "public_url:"},
		{name: "loopback IP", publicURL: "http://127.0.0.1:3579", environment: "development", want: "public_url:"},
		{name: "localhost suffix", publicURL: "http://localhost.example.com:3579", environment: "development", want: "public_url:"},
		{name: "client ID required", publicURL: "https://photos.example.com", environment: "production", missing: "google_client_id", want: "google_client_id and google_client_secret: must be set together"},
		{name: "client secret required", publicURL: "https://photos.example.com", environment: "production", missing: "google_client_secret", want: "google_client_id and google_client_secret: must be set together"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			values := requiredConfig()
			values["smtp_url"] = "smtp://mail.example.test"
			values["smtp_from"] = "memento@example.test"
			values["app_env"] = tc.environment
			values["public_url"] = tc.publicURL
			values["google_client_id"] = "client"
			values["google_client_secret"] = "secret"
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

func TestGoogleIsOptionalInEveryEnvironment(t *testing.T) {
	t.Parallel()
	for _, environment := range []string{"production", "development", "test"} {
		t.Run(environment, func(t *testing.T) {
			t.Parallel()
			values := requiredConfig()
			values["app_env"] = environment
			values["smtp_url"] = "smtp://mail.example.test"
			values["smtp_from"] = "memento@example.test"
			cfg, err := load(filepath.Join(t.TempDir(), "missing.yaml"), false, values, func() (string, error) { return "host", nil })
			require.NoError(t, err)
			require.False(t, cfg.GoogleConfigured())
		})
	}
}

func TestGoogleCredentialsMustBePairedInEveryEnvironment(t *testing.T) {
	t.Parallel()
	for _, environment := range []string{"production", "development", "test"} {
		t.Run(environment, func(t *testing.T) {
			t.Parallel()
			for _, credential := range []string{"google_client_id", "google_client_secret"} {
				values := requiredConfig()
				values["app_env"] = environment
				values["smtp_url"] = "smtp://mail.example.test"
				values["smtp_from"] = "memento@example.test"
				values[credential] = "secret"
				_, err := load(filepath.Join(t.TempDir(), "missing.yaml"), false, values, func() (string, error) { return "host", nil })
				require.ErrorContains(t, err, "google_client_id and google_client_secret: must be set together")
				require.NotContains(t, err.Error(), ": secret")
			}
		})
	}
}
