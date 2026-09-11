package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Stub external commands, not the script: selection must reach Compose and Go
// without inheriting an operator's image tag or bind-mounted data paths.
func TestSmokeScriptSelection(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, release, failure string
		args                   []string
		badChecksum            bool
	}{
		{name: "default", release: "v3.1.0"},
		{name: "previous minor", release: "v3.0.3", args: []string{"--version", "v3.0.3"}},
		{name: "unpinned patch", release: "v3.1.1", args: []string{"--version", "v3.1.1"}},
		{name: "floating", args: []string{"--version", "release"}, failure: "exact stable tag"},
		{name: "prerelease", args: []string{"--version", "v3.1.0-rc.1"}, failure: "exact stable tag"},
		{name: "leading zero", args: []string{"--version", "v3.01.0"}, failure: "exact stable tag"},
		{name: "unsupported", args: []string{"--version", "v3.2.0"}, failure: "Unsupported Immich release"},
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
    else printf 'c651a8211c9ab152bf7060ba1f370737ad2b4872da788a6c8a9e7fa7d4217357  compose.yaml\n'; fi
    ;;
  docker)
    if [[ "$*" == *'port immich-server 2283'* ]]; then printf '127.0.0.1:12283\n'
    elif [[ "$*" == *'port memento-db 5432'* ]]; then printf '127.0.0.1:15432\n'; fi
    ;;
  go)
    [[ $MEMENTO_SMOKE_DISPOSABLE == 1 ]]
    [[ $MEMENTO_SMOKE_IMMICH_URL == http://127.0.0.1:12283 ]]
    [[ $MEMENTO_SMOKE_DATABASE_URL == 'postgres://smoke:smoke@127.0.0.1:15432/smoke?sslmode=disable' ]]
    [[ $(< "$SMOKE_WORK_DIR/.env") == "IMMICH_VERSION=$IMMICH_VERSION"$'\nUPLOAD_LOCATION=smoke-upload\nDB_DATA_LOCATION=smoke-db\nDB_USERNAME=postgres\nDB_PASSWORD=smoke\nDB_DATABASE_NAME=immich' ]]
    [[ $(< "$SMOKE_WORK_DIR/immich-config.json") == '{"machineLearning":{"enabled":false},"metadata":{"faces":{"import":true}},"reverseGeocoding":{"enabled":false}}' ]]
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
				"SMOKE_TEST_BAD_CHECKSUM="+badChecksum, "IMMICH_VERSION=operator-version", "UPLOAD_LOCATION=/operator/photos")
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
				require.Contains(t, text, "go|run ./cmd/immich-smoke --version "+test.release+"|"+test.release+"|smoke-upload|")
				require.Contains(t, text, "down --timeout 15 --volumes")
			}
			for line := range strings.SplitSeq(text, "\n") {
				fields := strings.Split(line, "|")
				if len(fields) == 5 && fields[4] != "" {
					_, err := os.Stat(fields[4])
					require.ErrorIs(t, err, os.ErrNotExist, "disposable Compose directory must be removed")
				}
			}
		})
	}
}
