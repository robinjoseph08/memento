package devtool

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/database"
	"github.com/robinjoseph08/memento/pkg/migrations"
)

// DockerDatabase manages PostgreSQL through the main worktree's Compose service.
type DockerDatabase struct{}

func (d *DockerDatabase) Start(ctx context.Context, env Environment) error {
	if !env.IsMain() {
		return errors.New("the shared PostgreSQL container may only be started from the main worktree")
	}
	if err := os.MkdirAll(filepath.Join(env.MainRoot, "tmp", "postgres"), 0o755); err != nil {
		return fmt.Errorf("create PostgreSQL data directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(env.MainRoot, "tmp", "ports"), 0o755); err != nil {
		return fmt.Errorf("create port lock directory: %w", err)
	}
	if err := d.preparePostgresEnvironment(ctx, env); err != nil {
		return err
	}

	for attempt := 0; attempt < 10; attempt++ {
		command, err := d.composeCommand(ctx, env, "up", "-d", "--wait", "postgres")
		if err != nil {
			return err
		}
		output, startErr := command.CombinedOutput()
		if startErr == nil {
			return nil
		}
		port, err := readPostgresPort(env.MainRoot)
		if err != nil {
			return err
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

func (d *DockerDatabase) RequireRunning(ctx context.Context, env Environment) error {
	if _, err := os.Stat(postgresEnvironmentPath(env.MainRoot)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("PostgreSQL is not configured; run mise setup in the main worktree at %s", env.MainRoot)
		}
		return fmt.Errorf("read PostgreSQL configuration: %w", err)
	}
	command, err := d.composeCommand(ctx, env, "exec", "-T", "postgres", "pg_isready", "-U", "postgres")
	if err != nil {
		return err
	}
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("PostgreSQL is not running; run mise setup in the main worktree at %s: %w: %s", env.MainRoot, err, strings.TrimSpace(string(output)))
	}
	return nil
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

func (d *DockerDatabase) Clone(ctx context.Context, env Environment, source, target string) (result DatabaseCloneResult, returnErr error) {
	if source == target {
		return result, errors.New("source and target databases are the same")
	}
	stage, err := stagingDatabaseName()
	if err != nil {
		return result, err
	}
	if err := d.Create(ctx, env, stage); err != nil {
		return result, fmt.Errorf("create staging database: %w", err)
	}
	stagePresent := true
	defer func() {
		if stagePresent {
			_ = d.drop(context.WithoutCancel(ctx), env, stage)
		}
	}()

	if err := d.dumpAndRestore(ctx, env, source, stage); err != nil {
		return result, err
	}
	targetExists, err := d.Exists(ctx, env, target)
	if err != nil {
		return result, err
	}
	backup := ""
	swapCtx := context.WithoutCancel(ctx)
	if targetExists {
		backup, err = stagingDatabaseName()
		if err != nil {
			return result, err
		}
		if err := d.terminateConnections(ctx, env, target); err != nil {
			return result, fmt.Errorf("disconnect database %s: %w", target, err)
		}
		if err := d.rename(swapCtx, env, target, backup); err != nil {
			recoveryErr := d.restoreAmbiguousBackup(swapCtx, env, target, backup)
			return result, errors.Join(fmt.Errorf("back up database %s: %w", target, err), recoveryErr)
		}
	}

	if err := d.rename(swapCtx, env, stage, target); err != nil {
		installed, verifyErr := d.renameCompleted(swapCtx, env, stage, target)
		if installed {
			result.Replaced = true
			stagePresent = false
			if backup != "" {
				if cleanupErr := d.drop(swapCtx, env, backup); cleanupErr != nil {
					return result, fmt.Errorf("remove replaced database: %w", cleanupErr)
				}
			}
			return result, verifyErr
		}
		if backup != "" {
			restoreErr := d.rename(swapCtx, env, backup, target)
			if restoreErr != nil {
				return result, errors.Join(err, verifyErr, fmt.Errorf("restore original database: %w", restoreErr))
			}
		}
		return result, errors.Join(fmt.Errorf("install cloned database: %w", err), verifyErr)
	}
	result.Replaced = true
	stagePresent = false
	if backup != "" {
		if err := d.drop(swapCtx, env, backup); err != nil {
			return result, fmt.Errorf("remove replaced database: %w", err)
		}
	}
	return result, nil
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
	db, err := database.New(cfg)
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
	db, err := database.New(cfg)
	if err != nil {
		return fmt.Errorf("open database %s: %w", name, err)
	}
	defer func() { _ = db.Close() }()
	if _, err := migrations.Rollback(ctx, db); err != nil {
		return fmt.Errorf("roll back database %s: %w", name, err)
	}
	return nil
}

func (d *DockerDatabase) dumpAndRestore(ctx context.Context, env Environment, source, target string) error {
	dump, err := d.composeCommand(ctx, env, "exec", "-T", "postgres", "pg_dump", "-U", "postgres", "--format=custom", "--no-owner", "--no-privileges", "--dbname="+source)
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
	command.Env = append(os.Environ(),
		"POSTGRES_PORT="+strconv.Itoa(port),
		"POSTGRES_DATA_DIR="+filepath.Join(env.MainRoot, "tmp", "postgres"),
	)
	return command, nil
}

func (d *DockerDatabase) preparePostgresEnvironment(ctx context.Context, env Environment) error {
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
	running, err := d.postgresRunning(ctx, env)
	if err != nil {
		return err
	}
	if running || portAvailable(port) {
		return nil
	}
	nextPort, err := firstAvailablePort(5432)
	if err != nil {
		return fmt.Errorf("select replacement PostgreSQL port: %w", err)
	}
	return writePostgresPort(env.MainRoot, nextPort)
}

func (d *DockerDatabase) postgresRunning(ctx context.Context, env Environment) (bool, error) {
	command, err := d.composeCommand(ctx, env, "exec", "-T", "postgres", "pg_isready", "-U", "postgres")
	if err != nil {
		return false, err
	}
	if err := command.Run(); err != nil {
		return false, nil
	}
	return true, nil
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
		FilesPath:                 env.CurrentFilesPath(),
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
