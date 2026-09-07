// Command fixture runs a disposable, production-shaped Memento installation.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/robinjoseph08/memento/internal/devtool"
	"github.com/robinjoseph08/memento/pkg/testdb"
)

func main() {
	offline := flag.Bool("offline", os.Getenv("FIXTURE_OFFLINE") == "true", "start with Immich unavailable")
	binary := flag.String("api", "build/api/api", "compiled API binary")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *binary, *offline); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, binary string, offline bool) error {
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		if os.Getenv("CI") != "" {
			return errors.New("TEST_DATABASE_URL is required in CI")
		}
		environment, err := devtool.ResolveEnvironment(ctx)
		if err != nil {
			return fmt.Errorf("set TEST_DATABASE_URL or run mise setup: %w", err)
		}
		baseURL = environment.DatabaseURL(environment.MainDatabase)
	}
	setupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	schema, err := testdb.NewSchema(setupCtx, baseURL)
	if err != nil {
		return fmt.Errorf("allocate fixture schema: %w", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := schema.Close(cleanupCtx); err != nil {
			fmt.Fprintln(os.Stderr, "fixture schema cleanup failed:", err)
		}
	}()
	files, err := os.MkdirTemp("", "memento-fixture-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(files) }()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	fixtureURL := "http://" + listener.Addr().String()
	// Reserve an ephemeral API port until immediately before spawning the binary.
	apiListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = listener.Close()
		return err
	}
	apiPort := apiListener.Addr().(*net.TCPAddr).Port
	apiURL := "http://" + apiListener.Addr().String()
	configFile, err := filepath.Abs("app.dev.yaml")
	if err != nil {
		_ = listener.Close()
		_ = apiListener.Close()
		return err
	}
	environment := append(os.Environ(),
		"DATABASE_URL="+schema.URL,
		"APP_ENV=test", "AUTH_MODE=fake",
		"PUBLIC_URL="+apiURL,
		"IMMICH_URL="+fixtureURL, "IMMICH_API_KEY="+fixtureAPIKey,
		"SERVER_HOST=127.0.0.1", "SERVER_PORT="+strconv.Itoa(apiPort),
		"CONFIG_FILE="+configFile, "FILES_PATH="+files,
		"DATABASE_MAX_OPEN_CONNS=3", "DATABASE_MAX_IDLE_CONNS=1", "DATABASE_DEBUG=false",
	)
	api := &apiSupervisor{binary: binary, environment: environment, url: apiURL, failed: make(chan error, 1)}
	defer api.close()
	fixture := newImmichFixture(offline)
	fixture.restart = func(requestCtx context.Context) error {
		err := api.restart(requestCtx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "restart API:", err)
		}
		return err
	}
	server := &http.Server{Handler: fixture, ReadHeaderTimeout: 5 * time.Second}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(listener) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		_ = server.Close()
	}()
	_ = apiListener.Close()
	if err := api.restart(ctx); err != nil {
		return err
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"apiURL": apiURL, "fixtureURL": fixtureURL}); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Open %s\nReset: stop this command and start it again. Ctrl-C removes only its temporary schema.\n", apiURL)
	fmt.Fprintf(os.Stderr, "Status:  curl %s/__fixture/state\nOffline: curl -X POST -H 'Content-Type: application/json' -d '{\"available\":false}' %s/__fixture/state\nOnline:  curl -X POST -H 'Content-Type: application/json' -d '{\"available\":true}' %s/__fixture/state\nRestart: curl -X POST %s/__fixture/restart\n", fixtureURL, fixtureURL, fixtureURL, fixtureURL)
	select {
	case <-ctx.Done():
		return nil
	case err := <-api.failed:
		return err
	case err := <-serverDone:
		return fmt.Errorf("fixture server exited: %w", err)
	}
}

type apiProcess struct {
	command  *exec.Cmd
	done     chan struct{}
	expected atomic.Bool
	err      error
}

type apiSupervisor struct {
	mu          sync.Mutex
	binary      string
	environment []string
	url         string
	process     *apiProcess
	failed      chan error
}

// restart preserves the schema, URL, and browser sessions while replacing the API.
func (a *apiSupervisor) restart(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stop()
	command := exec.Command(a.binary)
	command.Env = a.environment
	// Keep stdout exclusively for the machine-readable readiness record.
	command.Stdout = os.Stderr
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return fmt.Errorf("start API; run mise build first: %w", err)
	}
	process := &apiProcess{command: command, done: make(chan struct{})}
	a.process = process
	go func() {
		process.err = command.Wait()
		close(process.done)
		if !process.expected.Load() {
			select {
			case a.failed <- fmt.Errorf("API exited unexpectedly: %v", process.err):
			default:
			}
		}
	}()
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	client := &http.Client{Timeout: time.Second}
	for {
		request, err := http.NewRequestWithContext(readyCtx, http.MethodGet, a.url+"/health", nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-readyCtx.Done():
			return fmt.Errorf("API readiness: %w", readyCtx.Err())
		case <-process.done:
			return fmt.Errorf("API exited before readiness: %v", process.err)
		case <-ticker.C:
		}
	}
}

func (a *apiSupervisor) close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stop()
}

func (a *apiSupervisor) stop() {
	if a.process == nil {
		return
	}
	process := a.process
	process.expected.Store(true)
	_ = process.command.Process.Signal(syscall.SIGTERM)
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-process.done:
	case <-timer.C:
		_ = process.command.Process.Kill()
		<-process.done
	}
	a.process = nil
}
