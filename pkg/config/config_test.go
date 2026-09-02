package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mapProvider map[string]any

func (p mapProvider) ReadBytes() ([]byte, error) {
	return nil, errors.New("not supported")
}

func (p mapProvider) Read() (map[string]any, error) {
	return p, nil
}

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := load(filepath.Join(t.TempDir(), "missing.yaml"), false, mapProvider{}, func() (string, error) {
		return "example-host", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "postgres://postgres:postgres@localhost:5432/memento?sslmode=disable", cfg.DatabaseURL)
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

	cfg, err := load(path, false, mapProvider{
		"database_url": "postgres://env:secret@db:5432/env_db?sslmode=disable",
		"server_port":  "5000",
	}, func() (string, error) { return "host", nil })
	require.NoError(t, err)
	assert.Equal(t, "postgres://env:secret@db:5432/env_db?sslmode=disable", cfg.DatabaseURL)
	assert.Equal(t, 5000, cfg.ServerPort)
}

func TestLoadValidatesConfig(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "app.yaml")
	require.NoError(t, os.WriteFile(path, []byte("database_url: \"\"\n"), 0o600))

	_, err := load(path, false, mapProvider{}, func() (string, error) { return "host", nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DatabaseURL")
}

func TestLoadRequiresExplicitConfigFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing.yaml")
	_, err := load(path, true, mapProvider{}, func() (string, error) { return "host", nil })
	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestLoadReturnsHostnameError(t *testing.T) {
	t.Parallel()

	expected := errors.New("hostname unavailable")
	_, err := load(filepath.Join(t.TempDir(), "missing.yaml"), false, mapProvider{}, func() (string, error) {
		return "", expected
	})
	require.ErrorIs(t, err, expected)
}
