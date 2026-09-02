package devtool

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type databaseCall struct {
	operation string
	source    string
	target    string
}

type fakeDatabase struct {
	exists        map[string]bool
	calls         []databaseCall
	cloneErr      error
	cloneReplaced bool
}

func (db *fakeDatabase) Start(context.Context, Environment) error {
	db.calls = append(db.calls, databaseCall{operation: "start"})
	return nil
}

func (db *fakeDatabase) RequireRunning(context.Context, Environment) error {
	db.calls = append(db.calls, databaseCall{operation: "require-running"})
	return nil
}

func (db *fakeDatabase) Exists(_ context.Context, _ Environment, name string) (bool, error) {
	db.calls = append(db.calls, databaseCall{operation: "exists", target: name})
	return db.exists[name], nil
}

func (db *fakeDatabase) Create(_ context.Context, _ Environment, name string) error {
	db.calls = append(db.calls, databaseCall{operation: "create", target: name})
	db.exists[name] = true
	return nil
}

func (db *fakeDatabase) Clone(_ context.Context, _ Environment, source, target string) (DatabaseCloneResult, error) {
	db.calls = append(db.calls, databaseCall{operation: "clone", source: source, target: target})
	if db.cloneErr != nil {
		return DatabaseCloneResult{Replaced: db.cloneReplaced}, db.cloneErr
	}
	db.exists[target] = true
	return DatabaseCloneResult{Replaced: true}, nil
}

func (db *fakeDatabase) Reset(_ context.Context, _ Environment, name string) error {
	db.calls = append(db.calls, databaseCall{operation: "reset", target: name})
	return nil
}

func (db *fakeDatabase) Migrate(_ context.Context, _ Environment, name string) error {
	db.calls = append(db.calls, databaseCall{operation: "migrate", target: name})
	return nil
}

func (db *fakeDatabase) Rollback(_ context.Context, _ Environment, name string) error {
	db.calls = append(db.calls, databaseCall{operation: "rollback", target: name})
	return nil
}

type fakeProcesses struct {
	apiPort int
	webPort int
	mode    string
}

func (p *fakeProcesses) Start(_ context.Context, _ Environment, mode string, apiPort, webPort int) error {
	p.mode = mode
	p.apiPort = apiPort
	p.webPort = webPort
	return nil
}

func (p *fakeProcesses) E2E(_ context.Context, _ Environment, project string, webPort int) error {
	p.mode = "e2e:" + project
	p.webPort = webPort
	return nil
}

type fakePorts struct {
	ports []int
}

func (p *fakePorts) Acquire(preferred int) (PortLease, error) {
	if len(p.ports) == 0 {
		return nil, errors.New("no port")
	}
	port := p.ports[0]
	p.ports = p.ports[1:]
	return fakeLease(port), nil
}

type fakeLease int

func (p fakeLease) Port() int  { return int(p) }
func (fakeLease) Close() error { return nil }

func TestSetupMainStartsPostgresAndPreservesExistingData(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	env := testEnvironment(root, root)
	db := &fakeDatabase{exists: map[string]bool{"memento": true}}
	app := testApp(env, db)

	require.NoError(t, app.Run(context.Background(), []string{"setup"}))
	assert.DirExists(t, filepath.Join(root, "tmp", "files"))
	assert.Equal(t, []databaseCall{
		{operation: "start"},
		{operation: "exists", target: "memento"},
		{operation: "migrate", target: "memento"},
	}, db.calls)
}

func TestSetupLinkedWorktreeClonesMissingDataFromMain(t *testing.T) {
	t.Parallel()

	mainRoot := t.TempDir()
	currentRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(mainRoot, "tmp", "files"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mainRoot, "tmp", "files", "state.txt"), []byte("source"), 0o644))
	env := testEnvironment(mainRoot, currentRoot)
	db := &fakeDatabase{exists: map[string]bool{"memento": true}}
	app := testApp(env, db)

	require.NoError(t, app.Run(context.Background(), []string{"setup"}))
	contents, err := os.ReadFile(filepath.Join(currentRoot, "tmp", "files", "state.txt"))
	require.NoError(t, err)
	assert.Equal(t, "source", string(contents))
	assert.Equal(t, []databaseCall{
		{operation: "require-running"},
		{operation: "exists", target: env.CurrentDatabase},
		{operation: "exists", target: "memento"},
		{operation: "clone", source: "memento", target: env.CurrentDatabase},
		{operation: "migrate", target: env.CurrentDatabase},
	}, db.calls)
}

func TestSetupLinkedWorktreePreservesFilesWhenDatabaseIsMissing(t *testing.T) {
	t.Parallel()

	mainRoot := t.TempDir()
	currentRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(mainRoot, "tmp", "files"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(currentRoot, "tmp", "files"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mainRoot, "tmp", "files", "state.txt"), []byte("main"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(currentRoot, "tmp", "files", "state.txt"), []byte("local"), 0o644))
	env := testEnvironment(mainRoot, currentRoot)
	db := &fakeDatabase{exists: map[string]bool{"memento": true}}
	app := testApp(env, db)
	app.Stdin = bytes.NewBufferString("n\n")

	require.NoError(t, app.Run(context.Background(), []string{"setup"}))
	contents, err := os.ReadFile(filepath.Join(currentRoot, "tmp", "files", "state.txt"))
	require.NoError(t, err)
	assert.Equal(t, "local", string(contents))
	for _, call := range db.calls {
		assert.NotEqual(t, "clone", call.operation)
	}
}

func TestSetupLinkedWorktreeDoesNotReplaceExistingData(t *testing.T) {
	t.Parallel()

	mainRoot := t.TempDir()
	currentRoot := t.TempDir()
	env := testEnvironment(mainRoot, currentRoot)
	db := &fakeDatabase{exists: map[string]bool{env.CurrentDatabase: true}}
	app := testApp(env, db)

	require.NoError(t, app.Run(context.Background(), []string{"setup"}))
	assert.Equal(t, []databaseCall{
		{operation: "require-running"},
		{operation: "exists", target: env.CurrentDatabase},
		{operation: "migrate", target: env.CurrentDatabase},
	}, db.calls)
}

func TestCloneFilesDoesNotRequirePostgres(t *testing.T) {
	t.Parallel()

	mainRoot := t.TempDir()
	currentRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(mainRoot, "tmp", "files"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mainRoot, "tmp", "files", "state.txt"), []byte("source"), 0o644))
	env := testEnvironment(mainRoot, currentRoot)
	db := &fakeDatabase{exists: map[string]bool{}}
	app := testApp(env, db)

	require.NoError(t, app.Run(context.Background(), []string{"clone", "files"}))
	contents, err := os.ReadFile(filepath.Join(currentRoot, "tmp", "files", "state.txt"))
	require.NoError(t, err)
	assert.Equal(t, "source", string(contents))
	assert.Empty(t, db.calls)
}

func TestCloneAllRestoresFilesWhenDatabaseCloneFails(t *testing.T) {
	t.Parallel()

	mainRoot := t.TempDir()
	currentRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(mainRoot, "tmp", "files"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(currentRoot, "tmp", "files"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mainRoot, "tmp", "files", "state.txt"), []byte("new"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(currentRoot, "tmp", "files", "state.txt"), []byte("old"), 0o644))
	env := testEnvironment(mainRoot, currentRoot)
	db := &fakeDatabase{
		exists:   map[string]bool{"memento": true, env.CurrentDatabase: true},
		cloneErr: errors.New("clone failed"),
	}
	app := testApp(env, db)
	app.Stdin = bytes.NewBufferString("y\n")

	err := app.Run(context.Background(), []string{"clone"})
	require.ErrorContains(t, err, "clone failed")
	contents, readErr := os.ReadFile(filepath.Join(currentRoot, "tmp", "files", "state.txt"))
	require.NoError(t, readErr)
	assert.Equal(t, "old", string(contents))
}

func TestCloneAllKeepsInstalledFilesWhenDatabaseCleanupFails(t *testing.T) {
	t.Parallel()

	mainRoot := t.TempDir()
	currentRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(mainRoot, "tmp", "files"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(currentRoot, "tmp", "files"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mainRoot, "tmp", "files", "state.txt"), []byte("new"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(currentRoot, "tmp", "files", "state.txt"), []byte("old"), 0o644))
	env := testEnvironment(mainRoot, currentRoot)
	db := &fakeDatabase{
		exists:        map[string]bool{"memento": true, env.CurrentDatabase: true},
		cloneErr:      errors.New("remove database backup"),
		cloneReplaced: true,
	}
	app := testApp(env, db)
	app.Stdin = bytes.NewBufferString("y\n")

	err := app.Run(context.Background(), []string{"clone"})
	require.ErrorContains(t, err, "remove database backup")
	contents, readErr := os.ReadFile(filepath.Join(currentRoot, "tmp", "files", "state.txt"))
	require.NoError(t, readErr)
	assert.Equal(t, "new", string(contents))
}

func TestSetupLinkedWorktreeRestoresFilesWhenDatabaseCloneFails(t *testing.T) {
	t.Parallel()

	mainRoot := t.TempDir()
	currentRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(mainRoot, "tmp", "files"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mainRoot, "tmp", "files", "state.txt"), []byte("new"), 0o644))
	env := testEnvironment(mainRoot, currentRoot)
	db := &fakeDatabase{
		exists:   map[string]bool{"memento": true},
		cloneErr: errors.New("clone failed"),
	}
	app := testApp(env, db)

	err := app.Run(context.Background(), []string{"setup"})
	require.ErrorContains(t, err, "clone failed")
	entries, readErr := os.ReadDir(filepath.Join(currentRoot, "tmp", "files"))
	require.NoError(t, readErr)
	assert.Empty(t, entries)
}

func TestCloneToMainAlwaysRequiresConfirmation(t *testing.T) {
	t.Parallel()

	mainRoot := t.TempDir()
	currentRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(mainRoot, "tmp", "files"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(currentRoot, "tmp", "files"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(currentRoot, "tmp", "files", "state.txt"), []byte("source"), 0o644))
	env := testEnvironment(mainRoot, currentRoot)
	app := testApp(env, &fakeDatabase{exists: map[string]bool{}})
	app.Stdin = bytes.NewBufferString("n\n")

	require.NoError(t, app.Run(context.Background(), []string{"clone", "files", "--to-main"}))
	_, err := os.Stat(filepath.Join(mainRoot, "tmp", "files", "state.txt"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestCloneDatabaseToMainReversesDirection(t *testing.T) {
	t.Parallel()

	mainRoot := t.TempDir()
	currentRoot := t.TempDir()
	env := testEnvironment(mainRoot, currentRoot)
	db := &fakeDatabase{exists: map[string]bool{"memento": true, env.CurrentDatabase: true}}
	app := testApp(env, db)
	app.Stdin = bytes.NewBufferString("y\n")

	require.NoError(t, app.Run(context.Background(), []string{"clone", "db", "--to-main"}))
	assert.Contains(t, db.calls, databaseCall{operation: "clone", source: env.CurrentDatabase, target: "memento"})
}

func TestCloneDeclinedLeavesDestinationUntouched(t *testing.T) {
	t.Parallel()

	mainRoot := t.TempDir()
	currentRoot := t.TempDir()
	env := testEnvironment(mainRoot, currentRoot)
	db := &fakeDatabase{exists: map[string]bool{"memento": true, env.CurrentDatabase: true}}
	app := testApp(env, db)
	app.Stdin = bytes.NewBufferString("n\n")

	require.NoError(t, app.Run(context.Background(), []string{"clone", "db"}))
	for _, call := range db.calls {
		assert.NotEqual(t, "clone", call.operation)
	}
}

func TestResetRecreatesCurrentDatabaseAfterConfirmation(t *testing.T) {
	t.Parallel()

	mainRoot := t.TempDir()
	currentRoot := t.TempDir()
	env := testEnvironment(mainRoot, currentRoot)
	db := &fakeDatabase{exists: map[string]bool{env.CurrentDatabase: true}}
	app := testApp(env, db)
	app.Stdin = bytes.NewBufferString("y\n")

	require.NoError(t, app.Run(context.Background(), []string{"db", "reset"}))
	assert.Equal(t, []databaseCall{
		{operation: "require-running"},
		{operation: "exists", target: env.CurrentDatabase},
		{operation: "reset", target: env.CurrentDatabase},
		{operation: "migrate", target: env.CurrentDatabase},
	}, db.calls)
}

func TestStartUsesPortsSelectedAtRuntime(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	env := testEnvironment(root, root)
	db := &fakeDatabase{exists: map[string]bool{"memento": true}}
	processes := &fakeProcesses{}
	app := testApp(env, db)
	app.Ports = &fakePorts{ports: []int{3580, 5174}}
	app.Processes = processes

	require.NoError(t, app.Run(context.Background(), []string{"start"}))
	assert.Equal(t, "all", processes.mode)
	assert.Equal(t, 3580, processes.apiPort)
	assert.Equal(t, 5174, processes.webPort)
}

func TestPortAllocatorSkipsAnOccupiedPort(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, listener.Close()) })
	_, portText, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	occupiedPort, err := strconv.Atoi(portText)
	require.NoError(t, err)

	allocator := &FilePortAllocator{LockRoot: t.TempDir()}
	lease, err := allocator.Acquire(occupiedPort)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lease.Close()) })
	assert.Greater(t, lease.Port(), occupiedPort)
}

func TestPostgresPortConflictsAreRecognized(t *testing.T) {
	t.Parallel()

	assert.True(t, postgresPortConflict([]byte("Bind for 127.0.0.1:5432 failed: port is already allocated")))
	assert.True(t, postgresPortConflict([]byte("listen tcp 127.0.0.1:5432: bind: address already in use")))
	assert.False(t, postgresPortConflict([]byte("database files are incompatible with server")))
}

func TestDevelopmentEnvironmentUsesCurrentWorktreeResources(t *testing.T) {
	t.Parallel()

	mainRoot := t.TempDir()
	currentRoot := t.TempDir()
	require.NoError(t, writePostgresPort(mainRoot, 5544))
	env := testEnvironment(mainRoot, currentRoot)

	values, err := developmentEnvironment(env, 3580, 5174)
	require.NoError(t, err)
	assert.Equal(t, "postgres://postgres:postgres@127.0.0.1:5544/memento_feature_12345678?sslmode=disable", lastEnvironmentValue(values, "DATABASE_URL"))
	assert.Equal(t, filepath.Join(currentRoot, "tmp", "files"), lastEnvironmentValue(values, "FILES_PATH"))
	assert.Equal(t, filepath.Join(currentRoot, "app.dev.yaml"), lastEnvironmentValue(values, "CONFIG_FILE"))
	assert.Equal(t, "3580", lastEnvironmentValue(values, "SERVER_PORT"))
	assert.Equal(t, "5174", lastEnvironmentValue(values, "WEB_PORT"))
}

func TestComposeCommandsUseTheMainWorktree(t *testing.T) {
	t.Parallel()

	mainRoot := t.TempDir()
	currentRoot := t.TempDir()
	require.NoError(t, writePostgresPort(mainRoot, 5544))
	env := testEnvironment(mainRoot, currentRoot)

	command, err := (&DockerDatabase{}).composeCommand(context.Background(), env, "ps")
	require.NoError(t, err)
	assert.Equal(t, mainRoot, command.Dir)
	assert.Equal(t, []string{
		"docker", "compose",
		"--project-name", env.ProjectName,
		"--project-directory", mainRoot,
		"--file", filepath.Join(mainRoot, "compose.yaml"),
		"ps",
	}, command.Args)
	assert.Equal(t, "5544", lastEnvironmentValue(command.Env, "POSTGRES_PORT"))
	assert.Equal(t, filepath.Join(mainRoot, "tmp", "postgres"), lastEnvironmentValue(command.Env, "POSTGRES_DATA_DIR"))
}

func TestRepositoryIdentityComesFromMainWorktreeDirectory(t *testing.T) {
	t.Parallel()

	mainRoot := filepath.Join("/tmp", "Order Console")
	currentRoot := filepath.Join("/tmp", "feature-worktree")

	assert.Equal(t, "order_console", worktreeDatabaseName(mainRoot, mainRoot))
	assert.Regexp(t, `^order_console_feature_worktree_[a-f0-9]{8}$`, worktreeDatabaseName(mainRoot, currentRoot))
	assert.Regexp(t, `^order_console_[a-f0-9]{8}$`, projectName(mainRoot))
}

func TestWorktreeDatabaseNamesAreStablePostgresIdentifiers(t *testing.T) {
	t.Parallel()

	currentRoot := filepath.Join("/tmp", "A feature worktree with a name that is much longer than a PostgreSQL identifier can be")
	name := worktreeDatabaseName("/tmp/main", currentRoot)
	assert.Equal(t, name, worktreeDatabaseName("/tmp/main", currentRoot))
	assert.Regexp(t, `^[a-z0-9_]+$`, name)
	assert.LessOrEqual(t, len(name), 63)
}

func lastEnvironmentValue(values []string, key string) string {
	prefix := key + "="
	for index := len(values) - 1; index >= 0; index-- {
		if len(values[index]) >= len(prefix) && values[index][:len(prefix)] == prefix {
			return values[index][len(prefix):]
		}
	}
	return ""
}

func testEnvironment(mainRoot, currentRoot string) Environment {
	return Environment{
		MainRoot:        mainRoot,
		CurrentRoot:     currentRoot,
		MainDatabase:    "memento",
		CurrentDatabase: "memento_feature_12345678",
		PostgresPort:    5432,
		ProjectName:     "memento_test",
	}
}

func testApp(env Environment, db DatabaseManager) *App {
	return &App{
		Stdin:     bytes.NewBuffer(nil),
		Stdout:    &bytes.Buffer{},
		Stderr:    &bytes.Buffer{},
		Resolve:   func(context.Context) (Environment, error) { return env, nil },
		Database:  db,
		Ports:     &fakePorts{ports: []int{3579, 5173}},
		Processes: &fakeProcesses{},
	}
}
