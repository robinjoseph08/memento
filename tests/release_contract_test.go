package tests

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var pinnedDigest = regexp.MustCompile(`@sha256:[0-9a-f]{64}(?:\s|$|["'])`)

func TestExecutableContainerImagesArePinnedByDigest(t *testing.T) {
	dockerfile := readRepositoryFile(t, "Dockerfile")
	stages := make(map[string]bool)
	for _, line := range strings.Split(dockerfile, "\n") {
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)
		switch {
		case strings.HasPrefix(strings.ToLower(trimmed), "# syntax="):
			assert.Regexp(t, pinnedDigest, trimmed, "Dockerfile frontend must be immutable: %s", trimmed)
		case strings.HasPrefix(upper, "FROM "):
			fields := strings.Fields(trimmed)
			reference := fields[1]
			assertDockerReferencePinned(t, stages, reference, trimmed)
			if len(fields) == 4 && strings.EqualFold(fields[2], "AS") {
				stages[fields[3]] = true
			}
		case strings.HasPrefix(upper, "COPY "), strings.HasPrefix(upper, "RUN "):
			for _, match := range regexp.MustCompile(`(?i)(?:--from=|\bfrom=)([^,\s]+)`).FindAllStringSubmatch(trimmed, -1) {
				assertDockerReferencePinned(t, stages, match[1], trimmed)
			}
		}
	}

	for _, path := range []string{
		"compose.yaml",
		"tests/compose.yaml",
		"tests/fixtures/immich-contract.compose.yaml",
	} {
		content := readRepositoryFile(t, path)
		for _, line := range strings.Split(content, "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "image:") || strings.Contains(trimmed, "MEMENTO_TEST_IMAGE_TAG") {
				continue
			}
			assert.Regexp(t, pinnedDigest, trimmed, "%s contains a mutable image: %s", path, trimmed)
		}
	}

	for _, path := range []string{"mise.toml", "tests/test-integration.sh", "tests/test-performance.sh"} {
		content := readRepositoryFile(t, path)
		for _, match := range regexp.MustCompile(`(?:caddy|postgres):[0-9][^\s"']+`).FindAllString(content, -1) {
			assert.Regexp(t, pinnedDigest, match, "%s contains a mutable image: %s", path, match)
		}
	}
}

func assertDockerReferencePinned(t *testing.T, stages map[string]bool, reference, instruction string) {
	t.Helper()
	if stages[reference] || reference == "scratch" || regexp.MustCompile(`^[0-9]+$`).MatchString(reference) {
		return
	}
	assert.Regexp(t, pinnedDigest, reference, "Dockerfile external source must be immutable: %s", instruction)
}

func TestWorkflowActionsArePinnedByCommit(t *testing.T) {
	workflows, err := filepath.Glob("../.github/workflows/*.yml")
	require.NoError(t, err)
	yamlWorkflows, err := filepath.Glob("../.github/workflows/*.yaml")
	require.NoError(t, err)
	workflows = append(workflows, yamlWorkflows...)
	require.NotEmpty(t, workflows)
	for _, path := range workflows {
		content, err := os.ReadFile(path)
		require.NoError(t, err)
		for _, match := range regexp.MustCompile(`(?m)^\s*uses:\s*([^\s]+)`).FindAllStringSubmatch(string(content), -1) {
			if strings.HasPrefix(match[1], "./") {
				continue
			}
			assert.Regexp(t, `^[^@]+@[0-9a-f]{40}$`, match[1], "%s contains an unpinned action", path)
		}
		for _, match := range regexp.MustCompile(`(?m)^\s*(?:image:\s*|driver-opts:\s*image=)([^\s]+)`).FindAllStringSubmatch(string(content), -1) {
			assert.Regexp(t, pinnedDigest, match[1], "%s contains a floating action helper image: %s", path, match[1])
		}
	}
}

func TestProductionComposeRequiresImmutableSingleImage(t *testing.T) {
	compose := readRepositoryFile(t, "deploy/compose.production.yaml")
	assert.Contains(t, compose, `image: "ghcr.io/robinjoseph08/memento@${MEMENTO_IMAGE_DIGEST:?`)
	assert.NotContains(t, compose, "${MEMENTO_IMAGE:")
	assert.NotContains(t, compose, "build:")
	assert.Equal(t, 1, strings.Count(compose, "  memento:\n"))
	assert.Contains(t, compose, "read_only: true")
	assert.Contains(t, compose, "no-new-privileges:true")
	assert.Contains(t, compose, "stop_signal: SIGTERM")
	assert.Contains(t, compose, "stop_grace_period: 15s")
	assert.Contains(t, compose, ":/etc/memento/memento.yaml:ro")
	assert.Contains(t, compose, ":/run/secrets:ro")
	assert.Contains(t, compose, "external: true")
}

func TestReleaseWorkflowPublishesExactTagAndRecordsDigest(t *testing.T) {
	workflow := readRepositoryFile(t, ".github/workflows/release.yml")
	assert.Contains(t, workflow, "tags: [\"v*\"]")
	assert.Contains(t, workflow, "uses: ./.github/workflows/ci.yml")
	assert.Contains(t, workflow, "platforms: linux/amd64,linux/arm64")
	assert.Contains(t, workflow, "provenance: mode=max")
	assert.Contains(t, workflow, "sbom: true")
	assert.Contains(t, workflow, "ghcr.io/${{ github.repository }}:${{ github.ref_name }}")
	assert.Contains(t, workflow, "${{ steps.build.outputs.digest }}")
	assert.Contains(t, workflow, "environment: release")
	assert.Contains(t, workflow, "needs: validation")
	assert.Contains(t, workflow, "MEMENTO_VERSION=${{ steps.source.outputs.version }}")
	assert.Contains(t, workflow, "MEMENTO_REVISION=${{ github.sha }}")
	assert.Contains(t, workflow, "MEMENTO_CREATED=${{ steps.source.outputs.created }}")
	assert.Contains(t, workflow, "memento-deployment-${RELEASE_VERSION}.tar.gz")
	assert.Contains(t, workflow, "cp LICENSE README.md memento-image.txt")
	assert.Contains(t, workflow, "release_state=--draft")
	assert.NotContains(t, workflow, ":latest")
}

func TestReleaseCheckAcceptsPackageVersionAndRejectsOtherTags(t *testing.T) {
	output, err := runReleaseCheck("v0.1.0")
	require.NoError(t, err, string(output))
	assert.Contains(t, string(output), "version=0.1.0")

	for _, tag := range []string{"0.1.0", "v0.1", "v01.1.0", "v0.1.1", "v0.1.0+metadata", "latest"} {
		output, err := runReleaseCheck(tag)
		assert.Error(t, err, "%s unexpectedly passed: %s", tag, output)
	}
}

func runReleaseCheck(tag string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "../scripts/check-release.sh", tag)
	command.Dir = "../tests"
	return command.CombinedOutput()
}
