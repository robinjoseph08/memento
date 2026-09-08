package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	pkgerrors "github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mapProvider map[string]any

type errorProvider struct {
	err error
}

func (p errorProvider) ReadBytes() ([]byte, error) {
	return nil, p.err
}

func (p errorProvider) Read() (map[string]any, error) {
	return nil, p.err
}

func (p mapProvider) ReadBytes() ([]byte, error) {
	return nil, errors.New("not supported")
}

func (p mapProvider) Read() (map[string]any, error) {
	return p, nil
}

func requiredConfig() mapProvider {
	return mapProvider{
		"database_url":   "postgres://test:test@localhost:5432/memento_test?sslmode=disable",
		"public_url":     "http://localhost:3579",
		"immich_url":     "http://localhost:2283",
		"immich_api_key": "test-api-key",
		"auth_mode":      "fake",
		"app_env":        "test",
	}
}

func TestLoadRequiresExplicitDatabaseURL(t *testing.T) {
	t.Parallel()
	values := requiredConfig()
	delete(values, "database_url")
	_, err := load(filepath.Join(t.TempDir(), "missing.yaml"), false, values, func() (string, error) { return "host", nil })
	require.ErrorContains(t, err, "database_url: required")
}

func TestLoadRequiresInstallationSettings(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"database_url", "public_url", "immich_url", "immich_api_key", "auth_mode"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			for _, missing := range []bool{true, false} {
				values := requiredConfig()
				if missing {
					delete(values, field)
				} else {
					values[field] = "  "
				}
				_, err := load(filepath.Join(t.TempDir(), "missing.yaml"), false, values, func() (string, error) { return "host", nil })
				require.ErrorContains(t, err, field+": required")
			}
		})
	}
}

func TestLoadRestrictsAuthentication(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ mode, environment, want string }{
		{"fake", "development", ""},
		{"fake", "test", ""},
		{"fake", "production", "auth_mode: fake requires app_env development or test"},
		{"fake", "", "auth_mode: fake requires app_env development or test"},
		{"google", "production", "google_client_id: required"},
		{"google", "development", "google_client_id: required"},
		{"other", "test", "auth_mode: must be google or fake"},
		{"fake", "staging", "app_env: must be production, development, or test"},
	} {
		t.Run(tc.mode+"/"+tc.environment, func(t *testing.T) {
			t.Parallel()
			values := requiredConfig()
			values["auth_mode"] = tc.mode
			if tc.environment == "" {
				delete(values, "app_env")
			} else {
				values["app_env"] = tc.environment
			}
			_, err := load(filepath.Join(t.TempDir(), "missing.yaml"), false, values, func() (string, error) { return "host", nil })
			if tc.want == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.want)
			}
		})
	}
}

func TestLoadValidatesHTTPURLs(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"public_url", "immich_url"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			for _, value := range []string{
				"localhost:3579", "ftp://example.com", "https:///missing", "https://user:secret@example.com",
				"https://example.com?key=secret", "https://example.com?", "https://example.com#secret", "https://example.com#",
				"https://example.com:bad", "https://example.com:65536", "https://example.com:",
				"https://example.com:0", "https://example.com:123:456", "https://[broken]", "https://example.com/%zzsecret",
			} {
				values := requiredConfig()
				values[field] = value
				_, err := load(filepath.Join(t.TempDir(), "missing.yaml"), false, values, func() (string, error) { return "host", nil })
				require.ErrorContains(t, err, field+":", value)
				assert.NotContains(t, err.Error(), "secret")
			}
		})
	}
	for _, tc := range []struct{ field, value, want string }{
		{"public_url", "https://example.com/memento", "public_url: must be an origin without a path"},
		{"immich_url", "https://example.com/api", "immich_url: use the instance base URL without /api"},
		{"immich_url", "https://example.com/photos/api/", "immich_url: use the instance base URL without /api"},
		{"immich_url", "https://example.com/photos/%61pi", "immich_url: use the instance base URL without /api"},
	} {
		values := requiredConfig()
		values[tc.field] = tc.value
		_, err := load(filepath.Join(t.TempDir(), "missing.yaml"), false, values, func() (string, error) { return "host", nil })
		require.ErrorContains(t, err, tc.want)
	}
	values := requiredConfig()
	values["public_url"] = "https://[::1]:3579/"
	values["immich_url"] = "https://example.com/photos/"
	cfg, err := load(filepath.Join(t.TempDir(), "missing.yaml"), false, values, func() (string, error) { return "host", nil })
	require.NoError(t, err)
	assert.Equal(t, "https://[::1]:3579", cfg.PublicURL)
	assert.Equal(t, "https://example.com/photos", cfg.ImmichURL)
}

func TestPublicURLMatchesBrowserOrigin(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ input, want string }{
		{"https://PHOTOS.example.test:443/", "https://photos.example.test"},
		{"http://LOCALHOST:80/", "http://localhost"},
		{"https://[::1]:443/", "https://[::1]"},
	} {
		values := requiredConfig()
		values["public_url"] = tc.input
		cfg, err := load(filepath.Join(t.TempDir(), "missing.yaml"), false, values, func() (string, error) { return "host", nil })
		require.NoError(t, err)
		assert.Equal(t, tc.want, cfg.PublicURL)
	}
}

func TestLoadValidatesDatabaseURL(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"mysql://role:secret@db/memento", "postgres:///memento", "postgres://db/memento",
		"postgres://:secret@db/memento", "postgres://role:secret@db", "postgres://role:secret@db/",
		"postgres://role:secret@db/one/two", "postgres://role:secret@db:abc/memento",
		"postgres://role:secret@db:65536/memento", "postgres://role:secret@db:/memento",
		"postgres://role:secret@db/memento#fragment", "postgres://role:%zzsecret@db/memento",
		"postgres://role:secret@db/memento?search_path=%zz", "postgres://role:secret@db/%20",
	} {
		values := requiredConfig()
		values["database_url"] = value
		_, err := load(filepath.Join(t.TempDir(), "missing.yaml"), false, values, func() (string, error) { return "host", nil })
		require.ErrorContains(t, err, "database_url:", value)
		assert.NotContains(t, err.Error(), "secret")
	}
	for _, value := range []string{
		"postgres://role:secret@db/memento?sslmode=disable&search_path=isolated_test",
		"postgresql://role@localhost:5432/memento",
	} {
		values := requiredConfig()
		values["database_url"] = value
		cfg, err := load(filepath.Join(t.TempDir(), "missing.yaml"), false, values, func() (string, error) { return "host", nil })
		require.NoError(t, err)
		assert.Equal(t, value, cfg.DatabaseURL)
	}
}

func TestLoadRedactsMalformedValues(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		field string
		value any
	}{
		{"server_port", "secret"},
		{"database_connect_retry_delay", "secret"},
		{"database_debug", "secret"},
		{"database_url", map[string]any{"secret": "secret"}},
		{"immich_api_key", []string{"secret"}},
		{"immich_api_key", 123},
	} {
		t.Run(tc.field, func(t *testing.T) {
			t.Parallel()
			values := requiredConfig()
			values[tc.field] = tc.value
			_, err := load(filepath.Join(t.TempDir(), "missing.yaml"), false, values, func() (string, error) { return "host", nil })
			require.ErrorContains(t, err, tc.field+":")
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}

func TestLoadRedactsYAMLParseErrors(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "app.yaml")
	require.NoError(t, os.WriteFile(path, []byte("immich_api_key: !!int secret\n"), 0o600))
	_, err := load(path, true, nil, func() (string, error) { return "host", nil })
	require.ErrorContains(t, err, "config_file:")
	assert.NotContains(t, err.Error(), "secret")
}

func TestLoadRedactsEnvironmentProviderError(t *testing.T) {
	t.Parallel()

	cause := errors.New("secret environment provider failure")
	_, err := load(filepath.Join(t.TempDir(), "missing.yaml"), false, errorProvider{err: cause}, func() (string, error) {
		return "host", nil
	})

	require.ErrorIs(t, err, cause)
	require.EqualError(t, err, "environment: could not load configuration")
	assert.NotContains(t, err.Error(), "secret")
	var tracer configStackTracer
	require.ErrorAs(t, err, &tracer)
	stack := fmt.Sprintf("%+v", tracer.StackTrace())
	assert.Contains(t, stack, "config.load")
}

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := load(filepath.Join(t.TempDir(), "missing.yaml"), false, requiredConfig(), func() (string, error) {
		return "example-host", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "postgres://test:test@localhost:5432/memento_test?sslmode=disable", cfg.DatabaseURL)
	assert.Equal(t, 5, cfg.DatabaseConnectRetryCount)
	assert.Equal(t, 2*time.Second, cfg.DatabaseConnectRetryDelay)
	assert.Equal(t, "./tmp/files", cfg.FilesPath)
	assert.Equal(t, "0.0.0.0", cfg.ServerHost)
	assert.Equal(t, 3579, cfg.ServerPort)
	assert.Equal(t, "example-host", cfg.Hostname)
}

func TestLoadYAMLFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "app.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
database_url: postgres://yaml:secret@db:5432/yaml_db?sslmode=disable
public_url: http://localhost:3579
immich_url: http://localhost:2283
immich_api_key: yaml-test-key
auth_mode: fake
app_env: development
database_debug: true
database_connect_retry_count: 2
database_connect_retry_delay: 25ms
files_path: /var/lib/app/files
server_host: 127.0.0.1
server_port: 4000
`), 0o600))

	cfg, err := load(path, false, mapProvider{}, func() (string, error) { return "host", nil })
	require.NoError(t, err)
	assert.Equal(t, "postgres://yaml:secret@db:5432/yaml_db?sslmode=disable", cfg.DatabaseURL)
	assert.Equal(t, "http://localhost:3579", cfg.PublicURL)
	assert.Equal(t, "http://localhost:2283", cfg.ImmichURL)
	assert.Equal(t, "yaml-test-key", cfg.ImmichAPIKey)
	assert.Equal(t, "fake", cfg.AuthMode)
	assert.Equal(t, "development", cfg.AppEnv)
	assert.True(t, cfg.DatabaseDebug)
	assert.Equal(t, 2, cfg.DatabaseConnectRetryCount)
	assert.Equal(t, 25*time.Millisecond, cfg.DatabaseConnectRetryDelay)
	assert.Equal(t, "/var/lib/app/files", cfg.FilesPath)
	assert.Equal(t, "127.0.0.1", cfg.ServerHost)
	assert.Equal(t, 4000, cfg.ServerPort)
}

func TestEnvironmentOverridesFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "app.yaml")
	require.NoError(t, os.WriteFile(path, []byte("server_port: 4000\n"), 0o600))

	values := requiredConfig()
	values["database_url"] = "postgres://env:secret@db:5432/env_db?sslmode=disable"
	values["server_port"] = "5000"
	values["public_url"] = "https://env.example.com"
	values["immich_url"] = "https://immich.example.com/photos"
	values["immich_api_key"] = "environment-test-key"
	cfg, err := load(path, false, values, func() (string, error) { return "host", nil })
	require.NoError(t, err)
	assert.Equal(t, "postgres://env:secret@db:5432/env_db?sslmode=disable", cfg.DatabaseURL)
	assert.Equal(t, 5000, cfg.ServerPort)
	assert.Equal(t, "https://env.example.com", cfg.PublicURL)
	assert.Equal(t, "https://immich.example.com/photos", cfg.ImmichURL)
	assert.Equal(t, "environment-test-key", cfg.ImmichAPIKey)
}

func TestLoadValidatesConfig(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "app.yaml")
	require.NoError(t, os.WriteFile(path, []byte("database_url: \"\"\n"), 0o600))

	_, err := load(path, false, mapProvider{}, func() (string, error) { return "host", nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database_url")
}

type configStackTracer interface {
	StackTrace() pkgerrors.StackTrace
}

func TestLoadRequiresExplicitConfigFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing.yaml")
	_, err := load(path, true, mapProvider{}, func() (string, error) { return "host", nil })
	require.Error(t, err)
	require.ErrorIs(t, err, os.ErrNotExist)
	var tracer configStackTracer
	assert.NotErrorAs(t, err, &tracer)
}

func TestNewForTest(t *testing.T) {
	t.Parallel()
	cfg := NewForTest()
	assert.Equal(t, "test", cfg.AppEnv)
	assert.Equal(t, "fake", cfg.AuthMode)
	assert.Equal(t, "http://localhost:3579", cfg.PublicURL)
	assert.Equal(t, "http://localhost:2283", cfg.ImmichURL)
	assert.Equal(t, "test-api-key", cfg.ImmichAPIKey)
	assert.Equal(t, "postgres://test:test@localhost:5432/memento_test?sslmode=disable", cfg.DatabaseURL)
	assert.Equal(t, 1, cfg.DatabaseConnectRetryCount)
	assert.Zero(t, cfg.DatabaseConnectRetryDelay)
}

func TestLoadReturnsHostnameError(t *testing.T) {
	t.Parallel()

	expected := errors.New("hostname unavailable")
	_, err := load(filepath.Join(t.TempDir(), "missing.yaml"), false, mapProvider{}, func() (string, error) {
		return "", expected
	})
	require.ErrorIs(t, err, expected)
	var tracer configStackTracer
	require.ErrorAs(t, err, &tracer)
	stack := fmt.Sprintf("%+v", tracer.StackTrace())
	assert.Contains(t, stack, "config.load")
	assert.Contains(t, stack, "config.go")
}
