package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Resolve the real override through Compose: the official file uses short-form
// depends_on, which starts containers without waiting for PostgreSQL to listen.
func TestSmokeComposeWaitsForTCPDatabase(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("Docker Compose is required")
	}
	root, err := filepath.Abs("../..")
	require.NoError(t, err)
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yaml")
	require.NoError(t, os.WriteFile(base, []byte(`services:
  immich-server:
    image: fixture-server
    depends_on: [redis, database]
  immich-machine-learning:
    image: fixture-ml
  redis:
    image: fixture-redis
  database:
    image: fixture-database
`), 0600))
	cmd := exec.CommandContext(t.Context(), "docker", "compose", "--file", base, "--file", filepath.Join(root, "scripts/immich-smoke.override.yaml"), "config", "--format", "json")
	cmd.Env = append(os.Environ(), "COMPOSE_PROJECT_NAME=memento-readiness-test", "SMOKE_WORK_DIR="+dir, "DB_USERNAME=postgres", "DB_DATABASE_NAME=immich")
	data, err := cmd.Output()
	require.NoError(t, err)
	var config struct {
		Services map[string]struct {
			DependsOn map[string]struct {
				Condition string `json:"condition"`
			} `json:"depends_on"`
			Healthcheck struct {
				Test []string `json:"test"`
			} `json:"healthcheck"`
		} `json:"services"`
	}
	require.NoError(t, json.Unmarshal(data, &config))
	require.Equal(t, "service_healthy", config.Services["immich-server"].DependsOn["database"].Condition)
	require.Equal(t, "service_healthy", config.Services["immich-server"].DependsOn["redis"].Condition)
	require.Equal(t, []string{"CMD", "pg_isready", "-h", "127.0.0.1", "-U", "postgres", "-d", "immich"}, config.Services["database"].Healthcheck.Test)
}
