package contractscheck

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckGoContractsReportsStableEchoTransportDiagnostics(t *testing.T) {
	diagnostics, err := CheckGo(filepath.Join("testdata", "go"), "./reject")
	require.NoError(t, err)
	assert.Equal(t, []string{
		"reject/reject.go:17:21: request JSON contract must be a named exported struct; got anonymous struct",
		"reject/reject.go:22:16: request JSON contract must be a named exported struct; got map",
		"reject/reject.go:27:21: response JSON contract must be a named exported struct; got unexported struct privateResponse",
		"reject/reject.go:31:21: response JSON contract must be a named exported struct; got map",
		"reject/reject.go:35:27: response JSON contract must be a named exported struct; got anonymous struct",
	}, diagnostics)
}

func TestCheckGoContractsReportsStableImmichDependencyDiagnostics(t *testing.T) {
	diagnostics, err := CheckGo(filepath.Join("testdata", "go"), "./pkg/immich")
	require.NoError(t, err)
	assert.Equal(t, []string{
		"pkg/immich/reject.go:10:25: Immich request JSON contract must not use an anonymous struct or map; got anonymous struct",
		"pkg/immich/reject.go:17:25: Immich request JSON contract must not use an anonymous struct or map; got map",
		"pkg/immich/reject.go:25:41: Immich response JSON contract must not use an anonymous struct or map; got anonymous struct",
		"pkg/immich/reject.go:30:47: Immich response JSON contract must not use an anonymous struct or map; got map",
		"pkg/immich/reject.go:34:54: Immich response JSON contract must not use an anonymous struct or map; got anonymous struct",
		"pkg/immich/reject.go:38:60: Immich response JSON contract must not use an anonymous struct or map; got map",
		"pkg/immich/reject.go:6:8: Immich provider DTO rawProviderResponse.Extra must not contain json.RawMessage fields",
	}, diagnostics)
}

func TestCheckGoContractsAcceptsNamedContractsAndNonTransportMaps(t *testing.T) {
	diagnostics, err := CheckGo(filepath.Join("testdata", "go"), "./allow")
	require.NoError(t, err)
	assert.Empty(t, diagnostics)
}
