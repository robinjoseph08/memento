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
		"pkg/immich/reject.go:104:33: Immich response JSON contract must use a named provider DTO; got map",
		"pkg/immich/reject.go:109:52: Immich response JSON contract must use a named provider DTO; got map",
		"pkg/immich/reject.go:115:40: Immich request JSON contract must be a named struct; got slice",
		"pkg/immich/reject.go:15:31: Immich request JSON contract must be a named struct; got anonymous struct",
		"pkg/immich/reject.go:22:31: Immich request JSON contract must be a named struct; got map",
		"pkg/immich/reject.go:27:31: Immich request JSON contract must be a named struct; got map",
		"pkg/immich/reject.go:32:31: Immich request JSON contract must be a named struct; got slice",
		"pkg/immich/reject.go:38:31: Immich request JSON contract must be a named struct; got interface",
		"pkg/immich/reject.go:44:20: Immich request JSON contract must be a named struct; got slice",
		"pkg/immich/reject.go:54:24: Immich request JSON contract must be a named struct; got map",
		"pkg/immich/reject.go:62:31: Immich request JSON contract must be a named struct; got external struct Buffer",
		"pkg/immich/reject.go:68:49: Immich request JSON contract must be a named struct; got slice",
		"pkg/immich/reject.go:75:41: Immich response JSON contract must use a named provider DTO; got anonymous struct",
		"pkg/immich/reject.go:80:47: Immich response JSON contract must use a named provider DTO; got map",
		"pkg/immich/reject.go:85:41: Immich response JSON contract must use a named provider DTO; got interface",
		"pkg/immich/reject.go:89:41: Immich response JSON contract must use a named provider DTO; got unknown type",
		"pkg/immich/reject.go:95:30: Immich response JSON contract must use a named provider DTO; got map",
		"pkg/immich/reject.go:9:8: Immich provider DTO rawProviderResponse.Extra must not contain json.RawMessage fields",
	}, diagnostics)
}

func TestCheckGoContractsAcceptsNamedContractsAndNonTransportMaps(t *testing.T) {
	diagnostics, err := CheckGo(filepath.Join("testdata", "go"), "./allow")
	require.NoError(t, err)
	assert.Empty(t, diagnostics)
}
