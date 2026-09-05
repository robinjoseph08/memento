package devtool

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDevelopmentPublicURL(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tmp"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tmp", "database.env"), []byte("POSTGRES_PORT=5544\n"), 0o600))
	env := Environment{MainRoot: root, CurrentRoot: root, CurrentDatabase: "memento_worktree"}
	values, err := developmentEnvironment(env, 3581, 5177)
	require.NoError(t, err)
	assert.True(t, slices.Contains(values, "PUBLIC_URL=http://localhost:5177"), "match Vite's advertised localhost origin at the selected port")
	values, err = developmentEnvironment(env, 3581, 0)
	require.NoError(t, err)
	assert.True(t, slices.Contains(values, "PUBLIC_URL=http://127.0.0.1:3581"), "use the API origin without Vite")
}

func TestE2EDatabaseEnvironment(t *testing.T) {
	root := t.TempDir()
	// Observe the child process environment through a substitute pnpm executable.
	script := "#!/bin/sh\nprintf '%s' \"$TEST_DATABASE_URL\" > \"$FILES_PATH/database-url\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "pnpm"), []byte(script), 0o755))
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TEST_DATABASE_URL", "")
	t.Setenv("CI", "")
	env := Environment{CurrentRoot: root, CurrentDatabase: "memento_worktree", PostgresPort: 5544}
	require.NoError(t, os.MkdirAll(env.CurrentFilesPath(), 0o755))
	processes := &OSProcesses{}
	require.NoError(t, processes.E2E(context.Background(), env, "chromium", 5177))
	actual, err := os.ReadFile(filepath.Join(env.CurrentFilesPath(), "database-url"))
	require.NoError(t, err)
	assert.Equal(t, "postgres://postgres:postgres@127.0.0.1:5544/memento_worktree?sslmode=disable", string(actual))
	t.Setenv("TEST_DATABASE_URL", "postgres://qa:password@localhost:5545/memento_test")
	require.NoError(t, processes.E2E(context.Background(), env, "webkit", 5177))
	actual, err = os.ReadFile(filepath.Join(env.CurrentFilesPath(), "database-url"))
	require.NoError(t, err)
	assert.Equal(t, "postgres://qa:password@localhost:5545/memento_test", string(actual))
	t.Setenv("TEST_DATABASE_URL", "")
	t.Setenv("CI", "true")
	assert.EqualError(t, processes.E2E(context.Background(), env, "chromium", 5177), "TEST_DATABASE_URL is required in CI")
}
