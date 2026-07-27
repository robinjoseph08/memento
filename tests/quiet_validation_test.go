package tests

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuietValidationSuppressesSuccessfulOutputAndRunsOnce(t *testing.T) {
	output, exitCode, calls := runQuietValidation(t, 0, "successful command noise\n")

	assert.Equal(t, 0, exitCode)
	assert.Equal(t, "check\n", calls)
	assert.Contains(t, output, "check:quiet PASSED")
	assert.NotContains(t, output, "successful command noise")
}

func TestQuietValidationReportsTheOriginalFailureWithoutRerunning(t *testing.T) {
	output, exitCode, calls := runQuietValidation(t, 7, "[test] useful failure detail\n[test] ERROR task failed\n")

	assert.Equal(t, 7, exitCode)
	assert.Equal(t, "check\n", calls)
	assert.Contains(t, output, "FAILED TASKS:\n  test")
	assert.Contains(t, output, "[test] useful failure detail")
	assert.Contains(t, output, "check:quiet FAILED")
}

func TestQuietValidationForwardsSignalsAndRemovesItsCaptureDirectory(t *testing.T) {
	tests := []struct {
		name     string
		signal   syscall.Signal
		exitCode int
	}{
		{name: "interrupt", signal: syscall.SIGINT, exitCode: 130},
		{name: "termination", signal: syscall.SIGTERM, exitCode: 143},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			temporary := t.TempDir()
			captureRoot := filepath.Join(temporary, "captures")
			require.NoError(t, os.Mkdir(captureRoot, 0o755))
			readyPath := filepath.Join(temporary, "ready")
			terminatedPath := filepath.Join(temporary, "terminated")
			fakeMise := filepath.Join(temporary, "mise")
			script := `#!/bin/sh
trap 'printf terminated > "$QUIET_TEST_TERMINATED"; exit 143' TERM
printf ready > "$QUIET_TEST_READY"
while :; do sleep 1; done
`
			require.NoError(t, os.WriteFile(fakeMise, []byte(script), 0o755))

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "sh", "../scripts/run-mise-quiet.sh", "check")
			command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			command.Env = append(os.Environ(),
				"MISE_BIN="+fakeMise,
				"QUIET_TEST_READY="+readyPath,
				"QUIET_TEST_TERMINATED="+terminatedPath,
				"TMPDIR="+captureRoot,
			)
			require.NoError(t, command.Start())
			finished := make(chan error, 1)
			go func() { finished <- command.Wait() }()
			waited := false
			t.Cleanup(func() {
				if waited {
					return
				}
				_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
				select {
				case <-finished:
				case <-time.After(3 * time.Second):
				}
			})

			require.Eventually(t, func() bool {
				_, err := os.Stat(readyPath)
				return err == nil
			}, 3*time.Second, 10*time.Millisecond)
			require.NoError(t, command.Process.Signal(test.signal))

			select {
			case err := <-finished:
				waited = true
				var exitError *exec.ExitError
				require.ErrorAs(t, err, &exitError)
				assert.Equal(t, test.exitCode, exitError.ExitCode())
			case <-time.After(5 * time.Second):
				require.FailNow(t, "quiet validation did not stop after signal", "signal: %v", test.signal)
			}

			terminated, err := os.ReadFile(terminatedPath)
			require.NoError(t, err)
			assert.Equal(t, "terminated", string(terminated))
			captures, err := os.ReadDir(captureRoot)
			require.NoError(t, err)
			assert.Empty(t, captures)
		})
	}
}

func runQuietValidation(t *testing.T, fakeExit int, fakeOutput string) (string, int, string) {
	t.Helper()
	temporary := t.TempDir()
	callsPath := filepath.Join(temporary, "calls")
	fakeMise := filepath.Join(temporary, "mise")
	script := `#!/bin/sh
printf '%s\n' "$2" >> "$QUIET_TEST_CALLS"
printf '%s' "$QUIET_TEST_OUTPUT"
exit "$QUIET_TEST_EXIT"
`
	require.NoError(t, os.WriteFile(fakeMise, []byte(script), 0o755))

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "sh", "../scripts/run-mise-quiet.sh", "check")
	command.Env = append(os.Environ(),
		"MISE_BIN="+fakeMise,
		"QUIET_TEST_CALLS="+callsPath,
		"QUIET_TEST_OUTPUT="+fakeOutput,
		"QUIET_TEST_EXIT="+strconv.Itoa(fakeExit),
	)
	combined, err := command.CombinedOutput()
	exitCode := 0
	if err != nil {
		var exitError *exec.ExitError
		require.ErrorAs(t, err, &exitError, "quiet validation returned a non-exit error: %v", err)
		exitCode = exitError.ExitCode()
	}
	calls, readErr := os.ReadFile(callsPath)
	require.NoError(t, readErr)
	return string(combined), exitCode, string(calls)
}
