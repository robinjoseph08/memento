package tests

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBrowserPreviewFailureCleansUpWithoutRawDiagnostics(t *testing.T) {
	root := filepath.Clean("..")
	bin := t.TempDir()
	invocations := filepath.Join(t.TempDir(), "docker.log")
	fakeDocker := filepath.Join(bin, "docker")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$MEMENTO_TEST_BROWSER_DOCKER_LOG"
if [ "$1" = "run" ]; then
  printf '%s\n' 'credential=must-not-be-logged' >&2
  exit 1
fi
exit 0
`
	if err := os.WriteFile(fakeDocker, []byte(script), 0o700); err != nil {
		t.Fatal("write fake Docker command")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "scripts/start-browser-preview.mjs")
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"MEMENTO_TEST_BROWSER_DOCKER_LOG="+invocations,
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("browser preview unexpectedly survived fixture startup failure")
	}
	if ctx.Err() != nil {
		t.Fatal("browser preview failure cleanup exceeded its deadline")
	}
	diagnostics := string(output)
	if !strings.Contains(diagnostics, "Browser application fixture failed during startup.") {
		t.Fatal("browser preview did not report its fixed startup stage")
	}
	if strings.Contains(diagnostics, "must-not-be-logged") || strings.Contains(diagnostics, "credential=") {
		t.Fatal("browser preview exposed raw dependency diagnostics")
	}

	logged, readErr := os.ReadFile(invocations)
	if readErr != nil {
		t.Fatal("fake Docker invocation log was unavailable")
	}
	calls := string(logged)
	if !strings.Contains(calls, "run ") || !strings.Contains(calls, "rm --force") {
		t.Fatal("browser preview failure did not remove its disposable container")
	}
}
