package templateinit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitializeReplacesTemplateIdentity(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runGit(t, root, "init", "--quiet")
	templateModule := "github.com/robinjoseph08/" + "web-app-template"
	templateSlug := "web-app" + "-template"
	templateDisplayName := "Web App" + " Template"
	templateDatabase := "web_app_" + "template"
	templateOnlyStart := "<!-- web-app-" + "template:template-only:start -->"
	templateOnlyEnd := "<!-- web-app-" + "template:template-only:end -->"
	writeFile(t, root, "go.mod", "module "+templateModule+"\n")
	writeFile(t, root, "package.json", "{\"name\":\""+templateSlug+"\",\"description\":\"A full-stack web application "+"template\"}\n")
	writeFile(t, root, "README.md", "# "+templateDisplayName+"\n\nA full-stack web application "+"template.\n\n"+templateOnlyStart+"\nTemplate instructions\n"+templateOnlyEnd+"\n\nDatabase: "+templateDatabase+"\n")
	runGit(t, root, "add", ".")

	result, err := Initialize(root, "github.com/example/order-console")
	require.NoError(t, err)
	assert.Equal(t, "Order Console", result.DisplayName)
	assert.Contains(t, mustRead(t, root, "README.md"), "Database: order_console")
	assert.Equal(t, 3, result.FilesChanged)
	assert.Contains(t, mustRead(t, root, "go.mod"), "github.com/example/order-console")
	assert.Contains(t, mustRead(t, root, "package.json"), `"order-console"`)
	assert.Contains(t, mustRead(t, root, "package.json"), "Order Console is a full-stack web application")
	assert.Contains(t, mustRead(t, root, "README.md"), "# Order Console")
	assert.Contains(t, mustRead(t, root, "README.md"), "Order Console is a full-stack web application.")
	assert.NotContains(t, mustRead(t, root, "README.md"), "Template instructions")
}

func TestInitializeRemovesTemplateOnlyFilesAndTask(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runGit(t, root, "init", "--quiet")
	writeFile(t, root, "go.mod", "module github.com/robinjoseph08/"+"web-app-template\n")
	writeFile(t, root, "cmd/template-init/main.go", "package main\n")
	writeFile(t, root, "internal/templateinit/initialize.go", "package templateinit\n")
	writeFile(t, root, "internal/templateinit/initialize_test.go", "package templateinit\n")
	writeFile(t, root, "docs/research/github-template-repositories.md", "template research\n")
	start := "# web-app-" + "template:template-only:start"
	end := "# web-app-" + "template:template-only:end"
	writeFile(t, root, "mise.toml", "before = true\n"+start+"\n[tasks.init]\nrun = 'init'\n"+end+"\nafter = true\n")
	runGit(t, root, "add", ".")

	_, err := Initialize(root, "github.com/example/app")
	require.NoError(t, err)
	assert.NoFileExists(t, filepath.Join(root, "cmd", "template-init", "main.go"))
	assert.NoFileExists(t, filepath.Join(root, "internal", "templateinit", "initialize.go"))
	assert.NoFileExists(t, filepath.Join(root, "docs", "research", "github-template-repositories.md"))
	assert.NotContains(t, mustRead(t, root, "mise.toml"), "tasks.init")
}

func TestInitializeValidatesAllFilesBeforeWriting(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runGit(t, root, "init", "--quiet")
	original := "module github.com/robinjoseph08/" + "web-app-template\n"
	writeFile(t, root, "go.mod", original)
	writeFile(t, root, "README.md", "<!-- web-app-"+"template:template-only:start -->\nmissing end\n")
	runGit(t, root, "add", ".")

	_, err := Initialize(root, "github.com/example/app")
	require.ErrorContains(t, err, "missing its end marker")
	assert.Equal(t, original, mustRead(t, root, "go.mod"))
}

func TestInitializeRejectsSecondRun(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runGit(t, root, "init", "--quiet")
	writeFile(t, root, "README.md", "# Already Initialized\n")
	runGit(t, root, "add", ".")

	_, err := Initialize(root, "github.com/example/app")
	require.ErrorContains(t, err, "already be initialized")
}

func TestInitializeRejectsInvalidModule(t *testing.T) {
	t.Parallel()

	_, err := Initialize(t.TempDir(), "not a module")
	require.ErrorContains(t, err, "invalid Go module path")
}

func TestInitializeRejectsRepositoryNameThatCannotBeAnImageName(t *testing.T) {
	t.Parallel()

	longName := strings.Repeat("a", 129)
	_, err := Initialize(t.TempDir(), "github.com/example/"+longName)
	require.ErrorContains(t, err, "repository name")
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	require.NoError(t, command.Run())
}

func writeFile(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
}

func mustRead(t *testing.T, root, name string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(root, name))
	require.NoError(t, err)
	return string(contents)
}
