package devtool

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultAPIPort = 3579
	defaultWebPort = 5173
)

// Environment describes the repository and worktree resources used by a command.
type Environment struct {
	MainRoot        string
	CurrentRoot     string
	MainDatabase    string
	CurrentDatabase string
	PostgresPort    int
	ProjectName     string
}

func (e Environment) IsMain() bool {
	return samePath(e.MainRoot, e.CurrentRoot)
}

func (e Environment) MainFilesPath() string {
	return filepath.Join(e.MainRoot, "tmp", "files")
}

func (e Environment) CurrentFilesPath() string {
	return filepath.Join(e.CurrentRoot, "tmp", "files")
}

func (e Environment) DatabaseURL(name string) string {
	return fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%d/%s?sslmode=disable", e.PostgresPort, name)
}

// DatabaseCloneResult reports whether a clone installed the replacement before
// returning an error during cleanup.
type DatabaseCloneResult struct {
	Replaced bool
}

// DatabaseManager owns the shared PostgreSQL process and its worktree databases.
type DatabaseManager interface {
	Start(context.Context, Environment) error
	RequireRunning(context.Context, Environment) error
	Exists(context.Context, Environment, string) (bool, error)
	Create(context.Context, Environment, string) error
	Clone(context.Context, Environment, string, string) (DatabaseCloneResult, error)
	Reset(context.Context, Environment, string) error
	Migrate(context.Context, Environment, string) error
	Rollback(context.Context, Environment, string) error
}

// PortLease reserves a runtime port until Close is called.
type PortLease interface {
	Port() int
	Close() error
}

// PortAllocator reserves the first available port at or above a preferred port.
type PortAllocator interface {
	Acquire(preferred int) (PortLease, error)
}

// ProcessManager runs the development servers and browser tests.
type ProcessManager interface {
	Start(context.Context, Environment, string, int, int) error
	E2E(context.Context, Environment, string, int) error
}

// App implements the command interface used by mise tasks.
type App struct {
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	Resolve   func(context.Context) (Environment, error)
	Database  DatabaseManager
	Ports     PortAllocator
	Processes ProcessManager
}

func NewApp() *App {
	return &App{
		Stdin:     os.Stdin,
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		Resolve:   ResolveEnvironment,
		Database:  &DockerDatabase{},
		Ports:     &FilePortAllocator{},
		Processes: &OSProcesses{},
	}
}

func (a *App) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("command is required")
	}

	env, err := a.Resolve(ctx)
	if err != nil {
		return fmt.Errorf("resolve development environment: %w", err)
	}

	switch args[0] {
	case "setup":
		return a.setup(ctx, env)
	case "clone":
		return a.clone(ctx, env, args[1:])
	case "db":
		return a.database(ctx, env, args[1:])
	case "start":
		return a.start(ctx, env, args[1:])
	case "e2e":
		return a.e2e(ctx, env, args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func (a *App) setup(ctx context.Context, env Environment) error {
	if err := os.MkdirAll(env.CurrentFilesPath(), 0o755); err != nil {
		return fmt.Errorf("create files directory: %w", err)
	}

	if env.IsMain() {
		if err := a.Database.Start(ctx, env); err != nil {
			return err
		}
		exists, err := a.Database.Exists(ctx, env, env.MainDatabase)
		if err != nil {
			return err
		}
		if !exists {
			if err := a.Database.Create(ctx, env, env.MainDatabase); err != nil {
				return err
			}
		}
		if err := a.Database.Migrate(ctx, env, env.MainDatabase); err != nil {
			return err
		}
		return a.printf("Main worktree is ready with database %s.\n", env.MainDatabase)
	}

	if err := a.Database.RequireRunning(ctx, env); err != nil {
		return err
	}
	exists, err := a.Database.Exists(ctx, env, env.CurrentDatabase)
	if err != nil {
		return err
	}
	if !exists {
		mainExists, err := a.Database.Exists(ctx, env, env.MainDatabase)
		if err != nil {
			return err
		}
		if !mainExists {
			return fmt.Errorf("main database %s does not exist; run mise setup in %s", env.MainDatabase, env.MainRoot)
		}
		nonempty, err := directoryNonempty(env.CurrentFilesPath())
		if err != nil {
			return err
		}
		if nonempty {
			confirmed, err := a.confirm(fmt.Sprintf("Database %s is missing. Replace this worktree's existing files from main?", env.CurrentDatabase))
			if err != nil {
				return err
			}
			if !confirmed {
				return a.printf("Setup cancelled.\n")
			}
		}
		stagedFiles, err := stageDirectory(env.MainFilesPath(), env.CurrentFilesPath())
		if err != nil {
			return fmt.Errorf("stage main files: %w", err)
		}
		defer stagedFiles.Cleanup()
		if err := stagedFiles.Install(); err != nil {
			return fmt.Errorf("install main files: %w", err)
		}
		cloneResult, cloneErr := a.Database.Clone(ctx, env, env.MainDatabase, env.CurrentDatabase)
		if cloneErr != nil {
			if cloneResult.Replaced {
				return errors.Join(fmt.Errorf("clone main database: %w", cloneErr), stagedFiles.Finalize())
			}
			return errors.Join(fmt.Errorf("clone main database: %w", cloneErr), stagedFiles.Rollback())
		}
		if err := stagedFiles.Finalize(); err != nil {
			return fmt.Errorf("finalize main files: %w", err)
		}
	}
	if err := a.Database.Migrate(ctx, env, env.CurrentDatabase); err != nil {
		return err
	}
	return a.printf("Worktree is ready with database %s.\n", env.CurrentDatabase)
}

func (a *App) clone(ctx context.Context, env Environment, args []string) error {
	kind := "all"
	toMain := false
	for _, arg := range args {
		switch arg {
		case "db", "files":
			if kind != "all" {
				return errors.New("only one clone resource may be specified")
			}
			kind = arg
		case "--to-main":
			toMain = true
		default:
			return fmt.Errorf("unknown clone argument %q", arg)
		}
	}
	if env.IsMain() {
		return errors.New("clone commands must be run from a linked worktree")
	}
	if kind != "files" {
		if err := a.Database.RequireRunning(ctx, env); err != nil {
			return err
		}
	}

	sourceDatabase, targetDatabase := env.MainDatabase, env.CurrentDatabase
	sourceFiles, targetFiles := env.MainFilesPath(), env.CurrentFilesPath()
	if toMain {
		sourceDatabase, targetDatabase = targetDatabase, sourceDatabase
		sourceFiles, targetFiles = targetFiles, sourceFiles
	}

	replaceDatabase := kind == "all" || kind == "db"
	replaceFiles := kind == "all" || kind == "files"
	needsConfirmation := toMain
	if replaceDatabase {
		sourceExists, err := a.Database.Exists(ctx, env, sourceDatabase)
		if err != nil {
			return err
		}
		if !sourceExists {
			return fmt.Errorf("source database %s does not exist", sourceDatabase)
		}
		targetExists, err := a.Database.Exists(ctx, env, targetDatabase)
		if err != nil {
			return err
		}
		needsConfirmation = targetExists
	}
	if replaceFiles {
		if _, err := os.Stat(sourceFiles); err != nil {
			return fmt.Errorf("read source files %s: %w", sourceFiles, err)
		}
		nonempty, err := directoryNonempty(targetFiles)
		if err != nil {
			return err
		}
		needsConfirmation = needsConfirmation || nonempty
	}

	if needsConfirmation {
		message := fmt.Sprintf("Replace %s data with data from %s?", targetLabel(env, toMain), sourceLabel(env, toMain))
		confirmed, err := a.confirm(message)
		if err != nil {
			return err
		}
		if !confirmed {
			return a.printf("Clone cancelled.\n")
		}
	}

	var stagedFiles *stagedDirectory
	if replaceFiles {
		var err error
		stagedFiles, err = stageDirectory(sourceFiles, targetFiles)
		if err != nil {
			return err
		}
		defer stagedFiles.Cleanup()
	}
	if stagedFiles != nil {
		if err := stagedFiles.Install(); err != nil {
			return err
		}
	}
	if replaceDatabase {
		cloneResult, cloneErr := a.Database.Clone(ctx, env, sourceDatabase, targetDatabase)
		if cloneErr != nil {
			if stagedFiles == nil {
				return cloneErr
			}
			if cloneResult.Replaced {
				return errors.Join(cloneErr, stagedFiles.Finalize())
			}
			return errors.Join(cloneErr, stagedFiles.Rollback())
		}
	}
	if stagedFiles != nil {
		if err := stagedFiles.Finalize(); err != nil {
			return err
		}
	}
	return a.printf("Cloned %s from %s to %s.\n", kind, sourceLabel(env, toMain), targetLabel(env, toMain))
}

func (a *App) database(ctx context.Context, env Environment, args []string) error {
	if len(args) != 1 {
		return errors.New("database command must be migrate, reset, or rollback")
	}
	if err := a.Database.RequireRunning(ctx, env); err != nil {
		return err
	}

	switch args[0] {
	case "migrate":
		return a.Database.Migrate(ctx, env, env.CurrentDatabase)
	case "rollback":
		return a.Database.Rollback(ctx, env, env.CurrentDatabase)
	case "reset":
		exists, err := a.Database.Exists(ctx, env, env.CurrentDatabase)
		if err != nil {
			return err
		}
		if exists {
			confirmed, err := a.confirm(fmt.Sprintf("Reset database %s and delete all of its data?", env.CurrentDatabase))
			if err != nil {
				return err
			}
			if !confirmed {
				return a.printf("Reset cancelled.\n")
			}
		}
		if err := a.Database.Reset(ctx, env, env.CurrentDatabase); err != nil {
			return err
		}
		return a.Database.Migrate(ctx, env, env.CurrentDatabase)
	default:
		return fmt.Errorf("unknown database command %q", args[0])
	}
}

func (a *App) start(ctx context.Context, env Environment, args []string) error {
	mode := "all"
	if len(args) > 1 {
		return errors.New("start accepts at most one mode")
	}
	if len(args) == 1 {
		mode = args[0]
	}
	if mode != "all" && mode != "air" && mode != "api" && mode != "web" {
		return fmt.Errorf("unknown start mode %q", mode)
	}
	if mode != "web" {
		if err := a.Database.RequireRunning(ctx, env); err != nil {
			return err
		}
	}

	var apiLease, webLease PortLease
	var err error
	if mode != "web" {
		apiLease, err = a.Ports.Acquire(defaultAPIPort)
		if err != nil {
			return fmt.Errorf("reserve API port: %w", err)
		}
		defer func() { _ = apiLease.Close() }()
	}
	if mode == "all" || mode == "web" {
		webLease, err = a.Ports.Acquire(defaultWebPort)
		if err != nil {
			return fmt.Errorf("reserve web port: %w", err)
		}
		defer func() { _ = webLease.Close() }()
	}

	apiPort, webPort := 0, 0
	if apiLease != nil {
		apiPort = apiLease.Port()
	}
	if webLease != nil {
		webPort = webLease.Port()
	}
	if err := a.printf("Database: %s\n", env.CurrentDatabase); err != nil {
		return err
	}
	if apiPort != 0 {
		if err := a.printf("API: http://127.0.0.1:%d\n", apiPort); err != nil {
			return err
		}
	}
	if webPort != 0 {
		if err := a.printf("Web: http://127.0.0.1:%d\n", webPort); err != nil {
			return err
		}
	}
	return a.Processes.Start(ctx, env, mode, apiPort, webPort)
}

func (a *App) e2e(ctx context.Context, env Environment, args []string) error {
	project := ""
	if len(args) > 1 {
		return errors.New("e2e accepts at most one project")
	}
	if len(args) == 1 {
		project = args[0]
	}
	lease, err := a.Ports.Acquire(defaultWebPort)
	if err != nil {
		return fmt.Errorf("reserve web port: %w", err)
	}
	defer func() { _ = lease.Close() }()
	return a.Processes.E2E(ctx, env, project, lease.Port())
}

func (a *App) confirm(message string) (bool, error) {
	if err := a.printf("%s [y/N] ", message); err != nil {
		return false, err
	}
	line, err := bufio.NewReader(a.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func (a *App) printf(format string, values ...any) error {
	if _, err := fmt.Fprintf(a.Stdout, format, values...); err != nil {
		return fmt.Errorf("write command output: %w", err)
	}
	return nil
}

func sourceLabel(env Environment, toMain bool) string {
	if toMain {
		return filepath.Base(env.CurrentRoot)
	}
	return "main"
}

func targetLabel(env Environment, toMain bool) string {
	if toMain {
		return "main"
	}
	return filepath.Base(env.CurrentRoot)
}

func samePath(left, right string) bool {
	leftPath, leftErr := filepath.Abs(left)
	rightPath, rightErr := filepath.Abs(right)
	return leftErr == nil && rightErr == nil && leftPath == rightPath
}
