package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Stub external commands, not the script: selection must reach Compose and Go
// without inheriting an operator's image tag or bind-mounted data paths.
func TestSmokeRedaction(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs("../..")
	require.NoError(t, err)
	cmd := exec.CommandContext(t.Context(), "python3", filepath.Join(root, "scripts", "smoke-redact.py"))
	cmd.Env = append(os.Environ(), "INHERITED_API_KEY=inherited-key-value")
	cmd.Stdin = strings.NewReader(`Authorization: Bearer access-token-value
{"apiKey":"generated-key-value"}
Set-Cookie: session=cookie-value
postgres://user:database-password@localhost:5432/smoke
inherited-key-value
opaque: aabbccddeeff00112233445566778899aabbccddeeff0011
sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
missing permission: asset.read
`)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err)
	for _, secret := range []string{"access-token-value", "generated-key-value", "cookie-value", "database-password", "inherited-key-value", "aabbccddeeff00112233445566778899aabbccddeeff0011"} {
		require.NotContains(t, string(output), secret)
	}
	require.Contains(t, string(output), "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	require.Contains(t, string(output), "missing permission: asset.read")
}

func TestSmokeScriptSelection(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, release, failure string
		args                   []string
		badChecksum            bool
		goFailure              bool
	}{
		{name: "default", release: "v3.2.1"},
		{name: "previous minor", release: "v3.1.0", args: []string{"--version", "v3.1.0"}},
		{name: "retained minor", release: "v3.0.3", args: []string{"--version", "v3.0.3"}},
		{name: "unpinned patch", release: "v3.1.1", args: []string{"--version", "v3.1.1"}},
		{name: "floating", args: []string{"--version", "release"}, failure: "exact stable tag"},
		{name: "prerelease", args: []string{"--version", "v3.1.0-rc.1"}, failure: "exact stable tag"},
		{name: "leading zero", args: []string{"--version", "v3.01.0"}, failure: "exact stable tag"},
		{name: "exploratory", release: "v2.7.5", args: []string{"--version", "v2.7.5", "--probe"}},
		{name: "ML", release: "v3.2.1", args: []string{"--ml"}},
		{name: "permission diagnostic", release: "v3.2.1", args: []string{"--permission-failure", "asset.read"}},
		{name: "failed smoke retains evidence", release: "v3.2.1", goFailure: true, failure: "fixture failed"},
		{name: "missing", args: []string{"--version"}, failure: "Missing value"},
		{name: "unknown", args: []string{"--other"}, failure: "Unknown argument"},
		{name: "checksum", release: "v3.0.3", args: []string{"--version", "v3.0.3"}, badChecksum: true, failure: "checksum mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root, err := filepath.Abs("../..")
			require.NoError(t, err)
			bin := t.TempDir()
			log := filepath.Join(bin, "calls")
			stub := `#!/bin/bash
set -eu
printf '%s|%s|%s|%s|%s\n' "${0##*/}" "$*" "${IMMICH_VERSION:-}" "${UPLOAD_LOCATION:-}" "${SMOKE_WORK_DIR:-}" >> "$SMOKE_TEST_LOG"
case "${0##*/}" in
  curl)
    while (( $# )); do
      if [[ $1 == --output ]]; then printf 'services: {}\n' > "$2"; break; fi
      shift
    done
    ;;
  shasum)
    if [[ $SMOKE_TEST_BAD_CHECKSUM == true ]]; then printf 'wrong\n'
    elif [[ $IMMICH_VERSION == v3.0.3 ]]; then printf 'da6f0ca9156c1716e69b3067cb8775b735a4a69fb478a8719e56cf7ea3e3d246  compose.yaml\n'
    elif [[ $IMMICH_VERSION == v3.2.1 ]]; then printf 'fa98c3eb0884b0b88aaee11a9c9acb8cfb17a0f495d12c21358deb6d3e8a546b  compose.yaml\n'
    elif [[ $IMMICH_VERSION == v2.7.5 ]]; then printf '69da59c813f1382400a8fe9fce9f51ac00a4d05d7d1571a0d49dcd517ae41ad5  compose.yaml\n'
    else printf 'c651a8211c9ab152bf7060ba1f370737ad2b4872da788a6c8a9e7fa7d4217357  compose.yaml\n'; fi
    ;;
  docker)
    if [[ "$*" == *'up --detach'* && "$*" == *'immich-machine-learning'* ]]; then touch "$SMOKE_TEST_LOG.ml"; fi
    if [[ "$*" == *'down --timeout'* && "$*" == *'--profile ml'* ]]; then rm -f "$SMOKE_TEST_LOG.ml"; fi
    if [[ "$*" == *'port immich-server 2283'* ]]; then printf '127.0.0.1:12283\n'
    elif [[ "$*" == *'port memento-db 5432'* ]]; then printf '127.0.0.1:15432\n'
    elif [[ "$*" == *'ps --all --quiet'* ]]; then printf 'container-id\n'
    elif [[ "$*" == *'inspect --format'* ]]; then printf 'sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n'
    elif [[ "$*" == *'logs --no-color'* ]]; then printf 'x-api-key: fixture-secret-key\n'; fi
    ;;
  go)
    [[ $MEMENTO_SMOKE_DISPOSABLE == 1 ]]
    [[ $MEMENTO_SMOKE_IMMICH_URL == http://127.0.0.1:12283 ]]
    [[ $MEMENTO_SMOKE_DATABASE_URL == 'postgres://smoke:smoke@127.0.0.1:15432/smoke?sslmode=disable' ]]
    [[ $(< "$SMOKE_WORK_DIR/.env") == "IMMICH_VERSION=$IMMICH_VERSION"$'\nUPLOAD_LOCATION=smoke-upload\nDB_DATA_LOCATION=smoke-db\nDB_USERNAME=postgres\nDB_PASSWORD=smoke\nDB_DATABASE_NAME=immich' ]]
    ml=false
    if [[ " $* " == *' --ml '* ]]; then ml=true; fi
    [[ $(< "$SMOKE_WORK_DIR/immich-config.json") == "{\"machineLearning\":{\"enabled\":$ml},\"metadata\":{\"faces\":{\"import\":true}},\"reverseGeocoding\":{\"enabled\":false}}" ]]
    printf 'Authorization: Bearer fixture-secret-token\n'
    if [[ $SMOKE_TEST_GO_FAILURE == true ]]; then printf 'fixture failed\n'; exit 1; fi
    ;;
esac
`
			for _, tool := range []string{"docker", "curl", "go", "shasum"} {
				require.NoError(t, os.WriteFile(filepath.Join(bin, tool), []byte(stub), 0o700))
			}
			args := append([]string{filepath.Join(root, "scripts", "immich-smoke.sh")}, test.args...)
			cmd := exec.CommandContext(t.Context(), "bash", args...)
			badChecksum := "false"
			if test.badChecksum {
				badChecksum = "true"
			}
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "SMOKE_TEST_LOG="+log,
				"SMOKE_TEST_BAD_CHECKSUM="+badChecksum, "SMOKE_TEST_GO_FAILURE="+strconv.FormatBool(test.goFailure),
				"IMMICH_VERSION=operator-version", "UPLOAD_LOCATION=/operator/photos")
			output, err := cmd.CombinedOutput()
			if test.failure != "" {
				require.Error(t, err, string(output))
				require.Contains(t, string(output), test.failure)
			} else {
				require.NoError(t, err, string(output))
				require.Contains(t, string(output), "with Immich "+test.release)
			}
			calls, readErr := os.ReadFile(log)
			if test.release == "" {
				require.ErrorIs(t, readErr, os.ErrNotExist, "invalid selection must fail before external commands")
				return
			}
			require.NoError(t, readErr)
			text := string(calls)
			require.Contains(t, text, "https://github.com/immich-app/immich/releases/download/"+test.release+"/docker-compose.yml")
			if test.badChecksum {
				require.NotContains(t, text, "up --detach")
				require.NotContains(t, text, "go|run")
			} else {
				require.Contains(t, text, "go|run ./cmd/immich-smoke --version "+test.release+" --openapi ")
				require.Contains(t, text, "https://raw.githubusercontent.com/immich-app/immich/"+test.release+"/open-api/immich-openapi-specs.json")
				if test.name == "exploratory" {
					require.Contains(t, text, " --probe|")
				}
				if test.name == "ML" {
					require.Contains(t, text, " --ml|")
					require.Contains(t, text, "immich-server memento-db immich-machine-learning|")
					_, err := os.Stat(log + ".ml")
					require.ErrorIs(t, err, os.ErrNotExist, "cleanup must remove the profiled ML service")
				}
				if test.name == "permission diagnostic" {
					require.Contains(t, text, " --permission-failure asset.read|")
				}
				require.Contains(t, text, "down --timeout 15 --volumes")
			}
			for line := range strings.SplitSeq(text, "\n") {
				fields := strings.Split(line, "|")
				if len(fields) == 5 && fields[4] != "" {
					work := fields[4]
					t.Cleanup(func() { _ = os.RemoveAll(work) })
					for _, private := range []string{".env", "immich-config.json", "compose.yaml", "openapi.json"} {
						_, err := os.Stat(filepath.Join(work, private))
						require.ErrorIs(t, err, os.ErrNotExist, "private config must not remain after cleanup")
					}
					request, err := os.ReadFile(filepath.Join(work, "artifacts", "request.txt"))
					require.NoError(t, err)
					require.Contains(t, string(request), "requested_version="+test.release)
					if !test.badChecksum {
						for _, name := range []string{"run.log", "compose.log"} {
							log, err := os.ReadFile(filepath.Join(work, "artifacts", name))
							require.NoError(t, err)
							require.NotContains(t, string(log), "fixture-secret")
							require.Contains(t, string(log), "[REDACTED]")
						}
						images, err := os.ReadFile(filepath.Join(work, "artifacts", "images.txt"))
						require.NoError(t, err)
						require.Contains(t, string(images), "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
					}
					result, err := os.ReadFile(filepath.Join(work, "artifacts", "result.txt"))
					require.NoError(t, err)
					if test.failure == "" {
						require.Contains(t, string(result), "exit_code=0")
					} else {
						require.Contains(t, string(result), "exit_code=1")
					}
				}
			}
		})
	}
}
