package tests

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

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
	starts := 0
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "compose up ") {
			starts++
		}
	}
	assert.Equal(t, 1, starts, "Immich bootstrap must start the fixture exactly once")
	assert.NotContains(t, script, "project_base", "Immich bootstrap must not create per-attempt fixture projects")
	assert.NotContains(t, strings.ToLower(script), "retrying", "Immich bootstrap must expose its first failure instead of retrying")
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
