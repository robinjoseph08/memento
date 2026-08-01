package tests

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowMatrixMatchesCompleteValidationSuite(t *testing.T) {
	mise := readRepositoryFile(t, "mise.toml")
	workflow := readRepositoryFile(t, ".github/workflows/ci.yml")
	tasks := parseTaskDefinitions(t, mise)

	expected := executableTaskClosure(t, tasks, []string{"ci"})
	matrixTasks := regexpMatches(workflow, `(?m)^\s+task: ([^\s]+)\s*$`)
	actual := executableTaskClosure(t, tasks, matrixTasks)
	slices.Sort(expected)
	slices.Sort(actual)
	assert.Equal(t, expected, actual, "the CI matrix must run the same validation tasks as mise ci")
}

func TestProductionTopologyAvoidsComposeBuildDelegation(t *testing.T) {
	script := readRepositoryFile(t, "tests/test-production.sh")
	for _, line := range strings.Split(script, "\n") {
		line = strings.TrimSpace(line)
		assert.False(t, strings.HasPrefix(line, "compose build"), "production topology must use direct docker build: %s", line)
		assert.False(t, strings.HasPrefix(line, "compose up") && strings.Contains(line, "--build"), "production topology must not ask Compose to build: %s", line)
	}
}

func TestImmichBootstrapHasSingleAttempt(t *testing.T) {
	script := readRepositoryFile(t, "tests/test-immich-contract.sh")
	starts := strings.Count(script, "compose up ")
	assert.Equal(t, 1, starts, "Immich bootstrap must start the fixture exactly once")
	if strings.Contains(script, "project_base") {
		t.Fatal("Immich bootstrap must not create per-attempt fixture projects")
	}
	if strings.Contains(strings.ToLower(script), "retrying") {
		t.Fatal("Immich bootstrap must expose its first failure instead of retrying")
	}
	if strings.Contains(script, "compose logs") {
		t.Fatal("Immich failures must expose status without raw service logs")
	}
	if !strings.Contains(script, "down --volumes --remove-orphans") {
		t.Fatal("Immich cleanup must remove disposable volumes and orphaned containers")
	}
}

func TestImmichExternalFixtureMountIsNarrowAndReadOnly(t *testing.T) {
	compose := readRepositoryFile(t, "tests/fixtures/immich-contract.compose.yaml")
	if !strings.Contains(compose, "../../pkg/immich/testdata/external:/external:ro") {
		t.Fatal("Immich must mount the external fixture read-only")
	}
	if strings.Contains(compose, "../../pkg/immich/testdata:/external") {
		t.Fatal("Immich must not mount unrelated adapter fixtures")
	}
}

func TestImmichBootstrapSanitizesFailureAndRunsCleanup(t *testing.T) {
	temporaryDirectory := t.TempDir()
	dockerLog := filepath.Join(temporaryDirectory, "docker.log")
	fakeDocker := filepath.Join(temporaryDirectory, "docker")
	require.NoError(t, os.WriteFile(fakeDocker, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$MEMENTO_FAKE_DOCKER_LOG"
printf '%s\n' 'unsafe /private/fixture-source/path memento-contract@example.com testpassword' >&2
case " $* " in
  *" up "*) exit 1 ;;
  *" down "*) exit 0 ;;
  *) exit 1 ;;
esac
`), 0o700))

	commandContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(commandContext, "sh", "test-immich-contract.sh")
	command.Dir = "."
	command.Env = append(os.Environ(),
		"PATH="+temporaryDirectory+":"+os.Getenv("PATH"),
		"MEMENTO_FAKE_DOCKER_LOG="+dockerLog,
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("the injected Compose startup failure must fail the harness")
	}
	diagnostics := string(output)
	if !strings.Contains(diagnostics, "Pinned Immich fixture failed to start") ||
		!strings.Contains(diagnostics, "server_state=unavailable") {
		t.Fatal("Immich failure diagnostics omitted their safe classification")
	}
	if strings.Contains(diagnostics, "/private/fixture-source/path") ||
		strings.Contains(diagnostics, "memento-contract@example.com") ||
		strings.Contains(diagnostics, "testpassword") {
		t.Fatal("Immich failure diagnostics exposed unsafe fixture data")
	}

	invocations, readErr := os.ReadFile(dockerLog)
	if readErr != nil {
		t.Fatal("fake Docker invocation log was unavailable")
	}
	invocationLog := string(invocations)
	if !strings.Contains(invocationLog, " up ") || !strings.Contains(invocationLog, " down ") {
		t.Fatal("Immich failure cleanup did not run after fixture startup failed")
	}
}

type taskDefinition struct {
	dependencies []string
	executable   bool
}

func parseTaskDefinitions(t *testing.T, mise string) map[string]taskDefinition {
	t.Helper()
	headerPattern := regexp.MustCompile(`(?m)^\[tasks\.(?:"([^"]+)"|([^]\n]+))\]\n`)
	headers := headerPattern.FindAllStringSubmatchIndex(mise, -1)
	require.NotEmpty(t, headers)
	definitions := make(map[string]taskDefinition, len(headers))
	dependsPattern := regexp.MustCompile(`(?ms)^depends = \[(.*?)\]`)
	for index, header := range headers {
		var name string
		if header[2] >= 0 {
			name = mise[header[2]:header[3]]
		} else {
			name = mise[header[4]:header[5]]
		}
		bodyEnd := len(mise)
		if index+1 < len(headers) {
			bodyEnd = headers[index+1][0]
		}
		body := mise[header[1]:bodyEnd]
		var dependencies []string
		if depends := dependsPattern.FindStringSubmatch(body); len(depends) == 2 {
			dependencies = regexpMatches(depends[1], `"([^"]+)"`)
		}
		definitions[name] = taskDefinition{
			dependencies: dependencies,
			executable:   regexp.MustCompile(`(?m)^run =`).MatchString(body),
		}
	}
	return definitions
}

func executableTaskClosure(t *testing.T, tasks map[string]taskDefinition, roots []string) []string {
	t.Helper()
	visited := make(map[string]bool)
	executable := make(map[string]bool)
	var visit func(string)
	visit = func(name string) {
		if visited[name] {
			return
		}
		visited[name] = true
		definition, ok := tasks[name]
		require.True(t, ok, "mise task %q must exist", name)
		if definition.executable {
			executable[name] = true
		}
		for _, dependency := range definition.dependencies {
			visit(dependency)
		}
	}
	for _, root := range roots {
		visit(root)
	}
	result := make([]string, 0, len(executable))
	for name := range executable {
		result = append(result, name)
	}
	return result
}

func regexpMatches(content, pattern string) []string {
	matches := regexp.MustCompile(pattern).FindAllStringSubmatch(content, -1)
	values := make([]string, 0, len(matches))
	for _, match := range matches {
		values = append(values, match[1])
	}
	return values
}

func readRepositoryFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile("../" + path)
	require.NoError(t, err)
	return string(content)
}
