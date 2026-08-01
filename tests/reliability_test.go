package tests

import (
	"context"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestReleaseGateWiringRunsCompleteImmichContract(t *testing.T) {
	mise := readRepositoryFile(t, "mise.toml")
	ciWorkflow := parseWorkflowDefinition(t, readRepositoryFile(t, ".github/workflows/ci.yml"))
	releaseWorkflow := parseWorkflowDefinition(t, readRepositoryFile(t, ".github/workflows/release.yml"))
	tasks := parseTaskDefinitions(t, mise)

	ciTask, ok := tasks["ci"]
	require.True(t, ok, "mise task %q must exist", "ci")
	assert.Contains(t, ciTask.dependencies, "test:immich-contract", "mise ci must directly include the Immich contract gate")

	expected := executableTaskClosure(t, tasks, []string{"ci"})
	ciSuite, ok := ciWorkflow.Jobs["suite"]
	require.True(t, ok, "CI workflow must define the suite job")
	matrixTasks := make([]string, 0, len(ciSuite.Strategy.Matrix.Include))
	for _, entry := range ciSuite.Strategy.Matrix.Include {
		require.NotEmpty(t, entry.Task, "every CI matrix entry must name an executable mise task")
		matrixTasks = append(matrixTasks, entry.Task)
	}
	require.NotEmpty(t, matrixTasks, "CI suite must define an executable task matrix")
	actual := executableTaskClosure(t, tasks, matrixTasks)
	slices.Sort(expected)
	slices.Sort(actual)
	assert.Equal(t, expected, actual, "the CI matrix executable task closure must match mise ci")

	validation, ok := releaseWorkflow.Jobs["validation"]
	require.True(t, ok, "release workflow must define the validation job")
	assert.Equal(t, "./.github/workflows/ci.yml", validation.Uses, "release validation must invoke the complete CI workflow")
	publish, ok := releaseWorkflow.Jobs["publish"]
	require.True(t, ok, "release workflow must define the publish job")
	assert.Contains(t, workflowNeeds(t, publish.Needs), "validation", "release publication must wait for validation")

	contractTask, ok := tasks["test:immich-contract"]
	require.True(t, ok, "mise task %q must exist", "test:immich-contract")
	assert.Equal(t, "./tests/test-immich-contract.sh", contractTask.command, "the Immich contract task must run its fixture harness")

	contractScript := readRepositoryFile(t, "tests/test-immich-contract.sh")
	contractCommandPattern := regexp.MustCompile(`(?m)^[ \t]*(go[ \t]+test[ \t]+-count=1[ \t]+-tags=immichcontract[ \t]+\./pkg/immich)[ \t]*(?:;[ \t]*then)?[ \t]*$`)
	contractCommands := contractCommandPattern.FindAllStringSubmatch(contractScript, -1)
	require.Len(t, contractCommands, 1, "Immich harness must execute exactly one live contract command: go test -count=1 -tags=immichcontract ./pkg/immich")
	assert.Equal(t, "go test -count=1 -tags=immichcontract ./pkg/immich", strings.Join(strings.Fields(contractCommands[0][1]), " "))

	requireTaggedGoTest(t, "pkg/immich", "immichcontract", "TestImmichV303LiveContract")
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
	command      string
	executable   bool
}

type workflowDefinition struct {
	Jobs map[string]workflowJobDefinition `yaml:"jobs"`
}

type workflowJobDefinition struct {
	Needs    yaml.Node `yaml:"needs"`
	Uses     string    `yaml:"uses"`
	Strategy struct {
		Matrix struct {
			Include []struct {
				Task string `yaml:"task"`
			} `yaml:"include"`
		} `yaml:"matrix"`
	} `yaml:"strategy"`
}

func parseTaskDefinitions(t *testing.T, mise string) map[string]taskDefinition {
	t.Helper()
	headerPattern := regexp.MustCompile(`(?m)^\[tasks\.(?:"([^"]+)"|([^]\n]+))\]\n`)
	headers := headerPattern.FindAllStringSubmatchIndex(mise, -1)
	require.NotEmpty(t, headers)
	definitions := make(map[string]taskDefinition, len(headers))
	dependsPattern := regexp.MustCompile(`(?ms)^depends = \[(.*?)\]`)
	commandPattern := regexp.MustCompile(`(?m)^run = ("(?:[^"\\]|\\.)*")$`)
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
		var command string
		if run := commandPattern.FindStringSubmatch(body); len(run) == 2 {
			var err error
			command, err = strconv.Unquote(run[1])
			require.NoError(t, err, "mise task %q has an invalid quoted run command", name)
		}
		definitions[name] = taskDefinition{
			dependencies: dependencies,
			command:      command,
			executable:   regexp.MustCompile(`(?m)^run =`).MatchString(body),
		}
	}
	return definitions
}

func parseWorkflowDefinition(t *testing.T, content string) workflowDefinition {
	t.Helper()
	var workflow workflowDefinition
	require.NoError(t, yaml.Unmarshal([]byte(content), &workflow), "workflow must be valid YAML")
	require.NotEmpty(t, workflow.Jobs, "workflow must define jobs")
	return workflow
}

func workflowNeeds(t *testing.T, node yaml.Node) []string {
	t.Helper()
	switch node.Kind {
	case yaml.ScalarNode:
		require.NotEmpty(t, node.Value, "workflow job needs must not be empty")
		return []string{node.Value}
	case yaml.SequenceNode:
		needs := make([]string, 0, len(node.Content))
		for _, dependency := range node.Content {
			require.Equal(t, yaml.ScalarNode, dependency.Kind, "workflow job needs entries must be job names")
			require.NotEmpty(t, dependency.Value, "workflow job needs entries must not be empty")
			needs = append(needs, dependency.Value)
		}
		return needs
	case yaml.DocumentNode, yaml.MappingNode, yaml.AliasNode:
		require.Failf(t, "workflow job needs must be a job name or list", "unexpected YAML node kind %d", node.Kind)
		return nil
	default:
		require.Failf(t, "workflow job needs must be present", "unexpected YAML node kind %d", node.Kind)
		return nil
	}
}

func requireTaggedGoTest(t *testing.T, directory, tag, testName string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", directory, "*_test.go"))
	require.NoError(t, err)
	require.NotEmpty(t, paths, "%s must contain Go test files", directory)

	var matchingFiles []string
	for _, path := range paths {
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		require.NoError(t, parseErr, "parse %s", path)
		containsTest := false
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok && function.Recv == nil && function.Name.Name == testName {
				containsTest = true
				break
			}
		}
		if !containsTest {
			continue
		}

		matchingFiles = append(matchingFiles, path)
		var buildExpression constraint.Expr
		for _, commentGroup := range parsed.Comments {
			if commentGroup.Pos() > parsed.Package {
				break
			}
			for _, comment := range commentGroup.List {
				if !strings.HasPrefix(comment.Text, "//go:build ") {
					continue
				}
				buildExpression, err = constraint.Parse(comment.Text)
				require.NoError(t, err, "%s has an invalid Go build constraint", path)
			}
		}
		require.NotNil(t, buildExpression, "%s must place %s in a tagged suite", path, testName)
		assert.Equal(t, tag, buildExpression.String(), "%s must place %s in the %s suite", path, testName, tag)
	}
	require.Len(t, matchingFiles, 1, "%s must contain exactly one declaration of %s; matches: %v", directory, testName, matchingFiles)
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
