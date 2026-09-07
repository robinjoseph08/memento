package devtool

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

const serverStartupTimeout = 30 * time.Second

// OSProcesses runs development commands as child process groups.
type OSProcesses struct{}

func (p *OSProcesses) Start(ctx context.Context, env Environment, mode string, apiPort, webPort int) error {
	childEnv := webEnvironment(env, webPort)
	if mode != "web" {
		var err error
		childEnv, err = developmentEnvironment(env, apiPort, webPort)
		if err != nil {
			return err
		}
	}

	switch mode {
	case "api":
		return runSingleProcess(ctx, env.CurrentRoot, childEnv, "go", "run", "./cmd/api")
	case "air":
		return runSingleProcess(ctx, env.CurrentRoot, childEnv, "air")
	case "web":
		return runSingleProcess(ctx, env.CurrentRoot, childEnv, "pnpm", "start", "--port", strconv.Itoa(webPort))
	case "all":
		return runDevelopmentProcesses(ctx, env.CurrentRoot, childEnv, apiPort, webPort)
	default:
		return fmt.Errorf("unknown process mode %q", mode)
	}
}

func (p *OSProcesses) E2E(ctx context.Context, env Environment, project string, webPort int) error {
	childEnv := webEnvironment(env, webPort)
	if os.Getenv("TEST_DATABASE_URL") == "" {
		if os.Getenv("CI") == "true" {
			return errors.New("TEST_DATABASE_URL is required in CI")
		}
		childEnv = append(childEnv, "TEST_DATABASE_URL="+env.DatabaseURL(env.CurrentDatabase))
	}
	args := []string{"exec", "playwright", "test"}
	if project != "" {
		args = append(args, "--project="+project)
	}
	return runSingleProcess(ctx, env.CurrentRoot, childEnv, "pnpm", args...)
}

func webEnvironment(env Environment, webPort int) []string {
	values := append([]string{}, os.Environ()...)
	values = append(values, "FILES_PATH="+env.CurrentFilesPath())
	if webPort != 0 {
		values = append(values, "WEB_PORT="+strconv.Itoa(webPort))
	}
	return values
}

func developmentEnvironment(env Environment, apiPort, webPort int) ([]string, error) {
	port, err := readPostgresPort(env.MainRoot)
	if err != nil {
		return nil, err
	}
	values := append([]string{}, os.Environ()...)
	values = append(values,
		"DATABASE_URL="+fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%d/%s?sslmode=disable", port, env.CurrentDatabase),
		"FILES_PATH="+env.CurrentFilesPath(),
		"CONFIG_FILE="+filepath.Join(env.CurrentRoot, "app.dev.yaml"),
	)
	if apiPort != 0 {
		values = append(values,
			"SERVER_PORT="+strconv.Itoa(apiPort),
			"VITE_API_URL="+fmt.Sprintf("http://127.0.0.1:%d", apiPort),
		)
	}
	publicPort := apiPort
	publicHost := "127.0.0.1"
	if webPort != 0 {
		values = append(values, "WEB_PORT="+strconv.Itoa(webPort))
		publicPort = webPort
		publicHost = "localhost"
	}
	if publicPort != 0 {
		values = append(values, "PUBLIC_URL=http://"+net.JoinHostPort(publicHost, strconv.Itoa(publicPort)))
	}
	return values, nil
}

func runDevelopmentProcesses(ctx context.Context, root string, environment []string, apiPort, webPort int) error {
	api := newProcess(root, environment, "air")
	if err := api.Start(); err != nil {
		return fmt.Errorf("start API: %w", err)
	}
	apiDone := waitForProcess(api)
	apiExited, err := waitForAPI(ctx, apiPort, apiDone)
	if err != nil {
		if !apiExited {
			terminateProcess(api)
			awaitTermination(api, apiDone)
		}
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}

	web := newProcess(root, environment, "pnpm", "start", "--port", strconv.Itoa(webPort))
	if err := web.Start(); err != nil {
		terminateProcess(api)
		awaitTermination(api, apiDone)
		return fmt.Errorf("start web server: %w", err)
	}
	webDone := waitForProcess(web)

	select {
	case err := <-apiDone:
		terminateProcess(web)
		awaitTermination(web, webDone)
		return processExitError("API", err)
	case err := <-webDone:
		terminateProcess(api)
		awaitTermination(api, apiDone)
		return processExitError("web server", err)
	case <-ctx.Done():
		terminateProcess(api)
		terminateProcess(web)
		awaitTermination(api, apiDone)
		awaitTermination(web, webDone)
		return nil
	}
}

func runSingleProcess(ctx context.Context, root string, environment []string, name string, args ...string) error {
	command := newProcess(root, environment, name, args...)
	if err := command.Start(); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}
	done := waitForProcess(command)
	select {
	case err := <-done:
		return processExitError(name, err)
	case <-ctx.Done():
		terminateProcess(command)
		awaitTermination(command, done)
		return nil
	}
}

func newProcess(root string, environment []string, name string, args ...string) *exec.Cmd {
	command := exec.Command(name, args...)
	command.Dir = root
	command.Env = environment
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return command
}

func waitForProcess(command *exec.Cmd) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- command.Wait()
	}()
	return done
}

func terminateProcess(command *exec.Cmd) {
	if command != nil && command.Process != nil {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
	}
}

func awaitTermination(command *exec.Cmd, done <-chan error) {
	select {
	case <-done:
		return
	case <-time.After(3 * time.Second):
		if command != nil && command.Process != nil {
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		}
		<-done
	}
}

func waitForAPI(ctx context.Context, port int, exited <-chan error) (bool, error) {
	deadline := time.NewTimer(serverStartupTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	client := &http.Client{Timeout: 500 * time.Millisecond}
	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)

	for {
		select {
		case err := <-exited:
			if err == nil {
				return true, errors.New("API exited before becoming ready")
			}
			return true, processExitError("API", err)
		case <-ctx.Done():
			return false, ctx.Err()
		case <-deadline.C:
			return false, fmt.Errorf("API did not become ready at %s within %s", url, serverStartupTimeout)
		case <-ticker.C:
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return false, fmt.Errorf("create API health request: %w", err)
			}
			response, err := client.Do(request)
			if err == nil {
				_ = response.Body.Close()
				if response.StatusCode >= 200 && response.StatusCode < 300 {
					return false, nil
				}
			}
		}
	}
}

func processExitError(name string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return fmt.Errorf("%s exited: %w", name, err)
}
