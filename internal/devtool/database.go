package devtool

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/database"
	"github.com/robinjoseph08/memento/pkg/migrations"
)

// DockerDatabase manages PostgreSQL through the main worktree's Compose service.
type DockerDatabase struct {
	// Stdout receives progress messages, like when the container is started.
	Stdout io.Writer
}

// Ensure starts the shared PostgreSQL container unless it is already running.
// Any worktree may call it, since the Compose project is always the main
// worktree's. A file lock keeps worktrees from starting it at the same time.
func (d *DockerDatabase) Ensure(ctx context.Context, env Environment) error {
	unlock, err := lockFile(ctx, filepath.Join(env.MainRoot, "tmp", "database.lock"), func() {
		d.printf("Waiting for another worktree to finish starting PostgreSQL...\n")
	})
	if err != nil {
		return err
	}
	defer unlock()

	// A running container is the source of truth for its port, so a lost or
	// stale tmp/database.env can't move it while it starts up or point
	// connections at some other PostgreSQL.
	published, running, err := d.publishedPort(ctx, env)
	if err != nil {
		return err
	}
	if running {
		recorded, err := readPostgresPort(env.MainRoot)
		if err != nil || recorded != published {
			if err := writePostgresPort(env.MainRoot, published); err != nil {
				return err
			}
		}
		if d.postgresReady(ctx, env) {
			return nil
		}
	} else {
		if err := os.MkdirAll(filepath.Join(env.MainRoot, "tmp", "ports"), 0o755); err != nil {
			return fmt.Errorf("create port lock directory: %w", err)
		}
		if err := preparePostgresEnvironment(env); err != nil {
			return err
		}
	}

	for attempt := range 10 {
		port, err := readPostgresPort(env.MainRoot)
		if err != nil {
			return err
		}
		d.printf("Starting PostgreSQL on port %d...\n", port)
		command, err := d.composeCommand(ctx, env, "up", "-d", "--wait", "postgres")
		if err != nil {
			return err
		}
		output, startErr := command.CombinedOutput()
		if startErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !postgresPortConflict(output) || attempt == 9 {
			return fmt.Errorf("start PostgreSQL: %w: %s", startErr, strings.TrimSpace(string(output)))
		}
		nextPort, err := firstAvailablePort(port + 1)
		if err != nil {
			return errors.Join(fmt.Errorf("start PostgreSQL: %w: %s", startErr, strings.TrimSpace(string(output))), err)
		}
		if err := writePostgresPort(env.MainRoot, nextPort); err != nil {
			return err
		}
	}
	return errors.New("start PostgreSQL: exhausted port retries")
}

func postgresPortConflict(output []byte) bool {
	message := strings.ToLower(string(output))
	return strings.Contains(message, "port is already allocated") || strings.Contains(message, "address already in use")
}

func (d *DockerDatabase) Exists(ctx context.Context, env Environment, name string) (bool, error) {
	query := "SELECT 1 FROM pg_database WHERE datname = '" + sqlLiteral(name) + "'"
	command, err := d.composeCommand(ctx, env, "exec", "-T", "postgres", "psql", "-U", "postgres", "-d", "postgres", "-tAc", query)
	if err != nil {
		return false, err
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("check database %s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)) == "1", nil
}

func (d *DockerDatabase) Create(ctx context.Context, env Environment, name string) error {
	return d.runCompose(ctx, env, "create database "+name, "exec", "-T", "postgres", "createdb", "-U", "postgres", "--template=template0", name)
}

func (d *DockerDatabase) Clone(ctx context.Context, env Environment, source, target string) (returnErr error) {
	if source == target {
		return errors.New("source and target databases are the same")
	}
	stage, err := stagingDatabaseName()
	if err != nil {
		return err
	}
	if err := d.Create(ctx, env, stage); err != nil {
		return fmt.Errorf("create staging database: %w", err)
	}
	stagePresent := true
	defer func() {
		if stagePresent {
			_ = d.drop(context.WithoutCancel(ctx), env, stage)
		}
	}()

	if err := d.dumpAndRestore(ctx, env, source, stage); err != nil {
		return err
	}
	targetExists, err := d.Exists(ctx, env, target)
	if err != nil {
		return err
	}
	backup := ""
	swapCtx := context.WithoutCancel(ctx)
	if targetExists {
		backup, err = stagingDatabaseName()
		if err != nil {
			return err
		}
		if err := d.terminateConnections(ctx, env, target); err != nil {
			return fmt.Errorf("disconnect database %s: %w", target, err)
		}
		if err := d.rename(swapCtx, env, target, backup); err != nil {
			recoveryErr := d.restoreAmbiguousBackup(swapCtx, env, target, backup)
			return errors.Join(fmt.Errorf("back up database %s: %w", target, err), recoveryErr)
		}
	}

	if err := d.rename(swapCtx, env, stage, target); err != nil {
		installed, verifyErr := d.renameCompleted(swapCtx, env, stage, target)
		if installed {
			stagePresent = false
			if backup != "" {
				if cleanupErr := d.drop(swapCtx, env, backup); cleanupErr != nil {
					return fmt.Errorf("remove replaced database: %w", cleanupErr)
				}
			}
			return verifyErr
		}
		if backup != "" {
			restoreErr := d.rename(swapCtx, env, backup, target)
			if restoreErr != nil {
				return errors.Join(err, verifyErr, fmt.Errorf("restore original database: %w", restoreErr))
			}
		}
		return errors.Join(fmt.Errorf("install cloned database: %w", err), verifyErr)
	}
	stagePresent = false
	if backup != "" {
		if err := d.drop(swapCtx, env, backup); err != nil {
			return fmt.Errorf("remove replaced database: %w", err)
		}
	}
	return nil
}

func (d *DockerDatabase) restoreAmbiguousBackup(ctx context.Context, env Environment, target, backup string) error {
	targetExists, targetErr := d.Exists(ctx, env, target)
	backupExists, backupErr := d.Exists(ctx, env, backup)
	if targetErr != nil || backupErr != nil {
		return errors.Join(targetErr, backupErr)
	}
	if !targetExists && backupExists {
		if err := d.rename(ctx, env, backup, target); err != nil {
			return fmt.Errorf("restore original database: %w", err)
		}
	}
	return nil
}

func (d *DockerDatabase) renameCompleted(ctx context.Context, env Environment, source, target string) (bool, error) {
	targetExists, targetErr := d.Exists(ctx, env, target)
	sourceExists, sourceErr := d.Exists(ctx, env, source)
	if targetErr != nil || sourceErr != nil {
		return false, errors.Join(targetErr, sourceErr)
	}
	return targetExists && !sourceExists, nil
}

func (d *DockerDatabase) rename(ctx context.Context, env Environment, source, target string) error {
	query := fmt.Sprintf("ALTER DATABASE %s RENAME TO %s", quoteIdentifier(source), quoteIdentifier(target))
	return d.runCompose(ctx, env, "rename database", "exec", "-T", "postgres", "psql", "-v", "ON_ERROR_STOP=1", "-U", "postgres", "-d", "postgres", "-c", query)
}

func (d *DockerDatabase) terminateConnections(ctx context.Context, env Environment, name string) error {
	query := "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '" + sqlLiteral(name) + "'"
	return d.runCompose(ctx, env, "terminate database connections", "exec", "-T", "postgres", "psql", "-v", "ON_ERROR_STOP=1", "-U", "postgres", "-d", "postgres", "-c", query)
}

func (d *DockerDatabase) Reset(ctx context.Context, env Environment, name string) error {
	if err := d.drop(ctx, env, name); err != nil {
		return err
	}
	return d.Create(ctx, env, name)
}

func (d *DockerDatabase) Migrate(ctx context.Context, env Environment, name string) error {
	cfg, err := databaseConfig(env, name)
	if err != nil {
		return err
	}
	db, err := database.New(ctx, cfg)
	if err != nil {
		return fmt.Errorf("open database %s: %w", name, err)
	}
	defer func() { _ = db.Close() }()
	if _, err := migrations.BringUpToDate(ctx, db); err != nil {
		return fmt.Errorf("migrate database %s: %w", name, err)
	}
	return nil
}

func (d *DockerDatabase) Rollback(ctx context.Context, env Environment, name string) error {
	cfg, err := databaseConfig(env, name)
	if err != nil {
		return err
	}
	db, err := database.New(ctx, cfg)
	if err != nil {
		return fmt.Errorf("open database %s: %w", name, err)
	}
	defer func() { _ = db.Close() }()
	if _, err := migrations.Rollback(ctx, db); err != nil {
		return fmt.Errorf("roll back database %s: %w", name, err)
	}
	return nil
}

// dumpAndRestore copies one database into another. Unlogged tables travel
// without their rows: River's leader election lives in one, its rows belong
// to the source's own worker, and PostgreSQL empties such tables after any
// unclean stop, which can leave them unreadable by pg_dump.
func (d *DockerDatabase) dumpAndRestore(ctx context.Context, env Environment, source, target string) error {
	dump, err := d.composeCommand(ctx, env, "exec", "-T", "postgres", "pg_dump", "-U", "postgres", "--format=custom", "--no-owner", "--no-privileges", "--no-unlogged-table-data", "--dbname="+source)
	if err != nil {
		return err
	}
	restore, err := d.composeCommand(ctx, env, "exec", "-T", "postgres", "pg_restore", "-U", "postgres", "--exit-on-error", "--no-owner", "--no-privileges", "--dbname="+target)
	if err != nil {
		return err
	}

	pipe, err := dump.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create database dump pipe: %w", err)
	}
	var dumpErrors, restoreErrors bytes.Buffer
	dump.Stderr = &dumpErrors
	restore.Stdin = pipe
	restore.Stderr = &restoreErrors

	if err := restore.Start(); err != nil {
		return fmt.Errorf("start database restore: %w", err)
	}
	if err := dump.Start(); err != nil {
		_ = restore.Process.Kill()
		_ = restore.Wait()
		return fmt.Errorf("start database dump: %w", err)
	}
	dumpErr := dump.Wait()
	restoreErr := restore.Wait()
	if dumpErr != nil {
		return fmt.Errorf("dump database %s: %w: %s", source, dumpErr, strings.TrimSpace(dumpErrors.String()))
	}
	if restoreErr != nil {
		return fmt.Errorf("restore database %s: %w: %s", target, restoreErr, strings.TrimSpace(restoreErrors.String()))
	}
	return nil
}

func (d *DockerDatabase) drop(ctx context.Context, env Environment, name string) error {
	return d.runCompose(ctx, env, "drop database "+name, "exec", "-T", "postgres", "dropdb", "-U", "postgres", "--force", "--if-exists", name)
}

func (d *DockerDatabase) runCompose(ctx context.Context, env Environment, action string, args ...string) error {
	command, err := d.composeCommand(ctx, env, args...)
	if err != nil {
		return err
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", action, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (d *DockerDatabase) composeCommand(ctx context.Context, env Environment, args ...string) (*exec.Cmd, error) {
	port, err := readPostgresPort(env.MainRoot)
	if err != nil {
		return nil, err
	}
	composeFile := filepath.Join(env.MainRoot, "compose.yaml")
	commandArgs := []string{"compose", "--project-name", env.ProjectName, "--project-directory", env.MainRoot, "--file", composeFile}
	commandArgs = append(commandArgs, args...)
	command := exec.CommandContext(ctx, "docker", commandArgs...)
	command.Dir = env.MainRoot
	command.Env = append(os.Environ(), "POSTGRES_PORT="+strconv.Itoa(port))
	return command, nil
}

// preparePostgresEnvironment picks the port for a container that is about to
// start: the recorded one while it is free, otherwise the first free one from
// 5432.
func preparePostgresEnvironment(env Environment) error {
	path := postgresEnvironmentPath(env.MainRoot)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		port, portErr := firstAvailablePort(5432)
		if portErr != nil {
			return fmt.Errorf("select PostgreSQL port: %w", portErr)
		}
		return writePostgresPort(env.MainRoot, port)
	} else if err != nil {
		return fmt.Errorf("stat PostgreSQL environment: %w", err)
	}

	port, err := readPostgresPort(env.MainRoot)
	if err != nil {
		return err
	}
	if portAvailable(port) {
		return nil
	}
	nextPort, err := firstAvailablePort(5432)
	if err != nil {
		return fmt.Errorf("select replacement PostgreSQL port: %w", err)
	}
	return writePostgresPort(env.MainRoot, nextPort)
}

// publishedPort reports the host port of the PostgreSQL container, and
// whether the container is running at all.
func (d *DockerDatabase) publishedPort(ctx context.Context, env Environment) (int, bool, error) {
	command, err := d.composeCommand(ctx, env, "ps", "--status", "running", "--quiet", "postgres")
	if err != nil {
		return 0, false, err
	}
	output, err := command.Output()
	if err != nil {
		return 0, false, fmt.Errorf("check PostgreSQL container: %w", err)
	}
	if strings.TrimSpace(string(output)) == "" {
		return 0, false, nil
	}
	command, err = d.composeCommand(ctx, env, "port", "postgres", "5432")
	if err != nil {
		return 0, false, err
	}
	output, err = command.Output()
	if err != nil {
		return 0, false, fmt.Errorf("find PostgreSQL port: %w", err)
	}
	port, err := parsePublishedPort(string(output))
	if err != nil {
		return 0, false, err
	}
	return port, true, nil
}

// parsePublishedPort reads the port from `docker compose port` output, like
// "127.0.0.1:5433". The first line is enough, and IPv6 forms like
// "[::]:5433" work too.
func parsePublishedPort(output string) (int, error) {
	line, _, _ := strings.Cut(strings.TrimSpace(output), "\n")
	index := strings.LastIndex(line, ":")
	if index < 0 {
		return 0, fmt.Errorf("unexpected docker compose port output %q", output)
	}
	port, err := strconv.Atoi(strings.TrimSpace(line[index+1:]))
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("unexpected docker compose port output %q", output)
	}
	return port, nil
}

// postgresReady reports whether a running container accepts connections. Any
// failure counts as not ready, and compose up then waits for it.
func (d *DockerDatabase) postgresReady(ctx context.Context, env Environment) bool {
	command, err := d.composeCommand(ctx, env, "exec", "-T", "postgres", "pg_isready", "-U", "postgres")
	if err != nil {
		return false
	}
	return command.Run() == nil
}

func (d *DockerDatabase) printf(format string, values ...any) {
	if d.Stdout != nil {
		_, _ = fmt.Fprintf(d.Stdout, format, values...)
	}
}

// lockFile holds an exclusive lock on path until the returned function is
// called, calling onWait once if another process holds it first.
func lockFile(ctx context.Context, path string, onWait func()) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", path, err)
	}
	waited := false
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = file.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		if !waited {
			onWait()
			waited = true
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

func writePostgresPort(mainRoot string, port int) error {
	path := postgresEnvironmentPath(mainRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create PostgreSQL environment directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".database-env-")
	if err != nil {
		return fmt.Errorf("create PostgreSQL environment: %w", err)
	}
	temporaryPath := file.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("set PostgreSQL environment permissions: %w", err)
	}
	if _, err := fmt.Fprintf(file, "POSTGRES_PORT=%d\n", port); err != nil {
		_ = file.Close()
		return fmt.Errorf("write PostgreSQL environment: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close PostgreSQL environment: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("install PostgreSQL environment: %w", err)
	}
	return nil
}

func databaseConfig(env Environment, name string) (*config.Config, error) {
	port, err := readPostgresPort(env.MainRoot)
	if err != nil {
		return nil, err
	}
	return &config.Config{
		DatabaseURL:               fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%d/%s?sslmode=disable", port, name),
		DatabaseConnectRetryCount: 5,
		DatabaseConnectRetryDelay: 500 * time.Millisecond,
		ServerHost:                "127.0.0.1",
		ServerPort:                0,
		Hostname:                  "development",
	}, nil
}

func stagingDatabaseName() (string, error) {
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate staging database name: %w", err)
	}
	return "app_stage_" + hex.EncodeToString(random[:]), nil
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func sqlLiteral(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}
