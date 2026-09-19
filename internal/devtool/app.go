package devtool

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
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

func (e Environment) DatabaseURL(name string) string {
	return fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%d/%s?sslmode=disable", e.PostgresPort, name)
}

// DatabaseManager owns the shared PostgreSQL process and its worktree databases.
type DatabaseManager interface {
	Start(context.Context, Environment) error
	RequireRunning(context.Context, Environment) error
	Exists(context.Context, Environment, string) (bool, error)
	Create(context.Context, Environment, string) error
	Clone(context.Context, Environment, string, string) error
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

// ProcessManager runs the development servers, the Mobile App's bundler, and
// browser tests.
type ProcessManager interface {
	Start(context.Context, Environment, string, int, int) error
	// Mobile starts Expo advertising host, with the app's address field
	// prefilled with installationURL.
	Mobile(ctx context.Context, env Environment, host, installationURL string) error
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
	// LANAddress is this machine's address as a phone on the same network
	// sees it.
	LANAddress func(context.Context) (string, error)
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

		LANAddress: lanAddress,
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
		if err := a.Database.Clone(ctx, env, env.MainDatabase, env.CurrentDatabase); err != nil {
			return fmt.Errorf("clone main database: %w", err)
		}
	}
	if err := a.Database.Migrate(ctx, env, env.CurrentDatabase); err != nil {
		return err
	}
	return a.printf("Worktree is ready with database %s.\n", env.CurrentDatabase)
}

func (a *App) clone(ctx context.Context, env Environment, args []string) error {
	toMain := false
	for _, arg := range args {
		if arg != "--to-main" {
			return fmt.Errorf("unknown clone argument %q", arg)
		}
		toMain = true
	}
	if env.IsMain() {
		return errors.New("clone must be run from a linked worktree")
	}
	if err := a.Database.RequireRunning(ctx, env); err != nil {
		return err
	}

	source, target := env.MainDatabase, env.CurrentDatabase
	if toMain {
		source, target = target, source
	}
	sourceExists, err := a.Database.Exists(ctx, env, source)
	if err != nil {
		return err
	}
	if !sourceExists {
		return fmt.Errorf("source database %s does not exist", source)
	}
	targetExists, err := a.Database.Exists(ctx, env, target)
	if err != nil {
		return err
	}
	if toMain || targetExists {
		message := fmt.Sprintf("Replace %s data with data from %s?", targetLabel(env, toMain), sourceLabel(env, toMain))
		confirmed, err := a.confirm(message)
		if err != nil {
			return err
		}
		if !confirmed {
			return a.printf("Clone cancelled.\n")
		}
	}
	if err := a.Database.Clone(ctx, env, source, target); err != nil {
		return err
	}
	return a.printf("Cloned database from %s to %s.\n", sourceLabel(env, toMain), targetLabel(env, toMain))
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
	if mode == "mobile" {
		return a.startMobile(ctx, env)
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
		if err := a.printf("Web: http://localhost:%d\n", webPort); err != nil {
			return err
		}
		forget, err := recordWebPort(env, webPort)
		if err != nil {
			return err
		}
		defer forget()
	}
	return a.Processes.Start(ctx, env, mode, apiPort, webPort)
}

// startMobile runs beside an already running web server. The phone reaches
// Memento the way a browser on another machine would: through Vite, which
// proxies the API, at this machine's address on the network.
func (a *App) startMobile(ctx context.Context, env Environment) error {
	webPort, err := recordedWebPort(env)
	if err != nil {
		return err
	}
	host, err := a.LANAddress(ctx)
	if err != nil {
		return err
	}
	installationURL := "http://" + net.JoinHostPort(host, strconv.Itoa(webPort))
	if err := a.printf("Memento for the phone: %s\n", installationURL); err != nil {
		return err
	}
	return a.Processes.Mobile(ctx, env, host, installationURL)
}

func webPortPath(env Environment) string {
	return filepath.Join(env.CurrentRoot, "tmp", "web-port")
}

// recordWebPort leaves this worktree's web port where the mobile task can find
// it. Ports are chosen at start, so there is nowhere else to look it up.
func recordWebPort(env Environment, port int) (func(), error) {
	path := webPortPath(env)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(port)), 0o600); err != nil {
		return nil, fmt.Errorf("record web port: %w", err)
	}
	return func() { _ = os.Remove(path) }, nil
}

func recordedWebPort(env Environment) (int, error) {
	contents, err := os.ReadFile(webPortPath(env))
	if errors.Is(err, os.ErrNotExist) {
		return 0, errors.New("the web server is not running in this worktree; run mise start first, then run this in a second terminal")
	}
	if err != nil {
		return 0, fmt.Errorf("read web port: %w", err)
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(contents)))
	if err != nil {
		return 0, fmt.Errorf("read web port: %w", err)
	}
	return port, nil
}

// lanAddress asks the routing table which local address reaches the outside
// world. Connecting a UDP socket sends nothing.
func lanAddress(ctx context.Context) (string, error) {
	connection, err := (&net.Dialer{}).DialContext(ctx, "udp", "192.0.2.1:9")
	if err != nil {
		return "", fmt.Errorf("find this machine's network address (is it on Wi-Fi or Ethernet?): %w", err)
	}
	defer func() { _ = connection.Close() }()
	address, ok := connection.LocalAddr().(*net.UDPAddr)
	if !ok || address.IP.IsLoopback() {
		return "", errors.New("find this machine's network address: not connected to a network")
	}
	return address.IP.String(), nil
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
