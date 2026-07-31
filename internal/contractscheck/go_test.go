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
		"reject/reject.go:45:18: protected JSON transport function Bind must be called directly; function values must not be passed, stored, or returned",
		"reject/reject.go:49:9: protected JSON transport function JSON must be called directly; function values must not be passed, stored, or returned",
		"reject/reject.go:53:9: protected JSON transport function JSONPretty must be called directly; function values must not be passed, stored, or returned",
		"reject/reject.go:57:9: protected JSON transport function Bind must be called directly; function values must not be passed, stored, or returned",
		"reject/reject.go:61:9: protected JSON transport function bindJSON must be called directly; function values must not be passed, stored, or returned",
	}, diagnostics)
}

func TestCheckGoContractsReportsStableImmichDependencyDiagnostics(t *testing.T) {
	diagnostics, err := CheckGo(filepath.Join("testdata", "go"), "./pkg/immich")
	require.NoError(t, err)
	assert.Equal(t, []string{
		"pkg/immich/reject.go:102:41: Immich response JSON contract must use a named provider DTO; got interface",
		"pkg/immich/reject.go:106:41: Immich response JSON contract must use a named provider DTO; got unknown type",
		"pkg/immich/reject.go:10:8: Immich provider DTO rawProviderResponse.Extra must not contain json.RawMessage fields",
		"pkg/immich/reject.go:110:9: protected JSON transport function getJSON must be called directly; function values must not be passed, stored, or returned",
		"pkg/immich/reject.go:121:33: Immich response JSON contract must use a named provider DTO; got map",
		"pkg/immich/reject.go:126:52: Immich response JSON contract must use a named provider DTO; got map",
		"pkg/immich/reject.go:130:10: protected JSON transport function doJSON must be called directly; function values must not be passed, stored, or returned",
		"pkg/immich/reject.go:142:37: Immich response JSON contract must use a named provider DTO; got map",
		"pkg/immich/reject.go:147:37: Immich response JSON contract must use a named provider DTO; got interface",
		"pkg/immich/reject.go:152:37: Immich response JSON contract must use a named provider DTO; got scalar",
		"pkg/immich/reject.go:157:37: Immich response JSON contract must use a named provider DTO; got anonymous struct",
		"pkg/immich/reject.go:162:37: Immich response JSON contract must use a named provider DTO; got scalar",
		"pkg/immich/reject.go:167:37: Immich response JSON contract must use a named provider DTO; got external struct Time",
		"pkg/immich/reject.go:172:37: Immich response JSON contract must use a named provider DTO; got exported provider struct ExportedProviderResponse",
		"pkg/immich/reject.go:177:37: Immich response JSON contract must use a named provider DTO; got raw bytes",
		"pkg/immich/reject.go:181:37: Immich response JSON contract must use a named provider DTO; got unresolved type parameter",
		"pkg/immich/reject.go:190:17: protected JSON transport function getJSON must be called directly; function values must not be passed, stored, or returned",
		"pkg/immich/reject.go:194:9: protected JSON transport function getJSON must be called directly; function values must not be passed, stored, or returned",
		"pkg/immich/reject.go:198:9: protected JSON transport function getJSON must be called directly; function values must not be passed, stored, or returned",
		"pkg/immich/reject.go:202:9: protected JSON transport function getJSONQuery must be called directly; function values must not be passed, stored, or returned",
		"pkg/immich/reject.go:206:9: protected JSON transport function doJSONStatus must be called directly; function values must not be passed, stored, or returned",
		"pkg/immich/reject.go:215:21: protected JSON transport function marshalJSONRequest must be called directly; function values must not be passed, stored, or returned",
		"pkg/immich/reject.go:219:9: protected JSON transport function marshalJSONRequest must be called directly; function values must not be passed, stored, or returned",
		"pkg/immich/reject.go:223:9: protected JSON transport function forwardResponse must be called directly; function values must not be passed, stored, or returned",
		"pkg/immich/reject.go:32:31: Immich request JSON contract must be a named struct; got anonymous struct",
		"pkg/immich/reject.go:39:31: Immich request JSON contract must be a named struct; got map",
		"pkg/immich/reject.go:44:31: Immich request JSON contract must be a named struct; got map",
		"pkg/immich/reject.go:49:31: Immich request JSON contract must be a named struct; got slice",
		"pkg/immich/reject.go:55:31: Immich request JSON contract must be a named struct; got interface",
		"pkg/immich/reject.go:60:13: protected JSON transport function marshalJSONRequest must be called directly; function values must not be passed, stored, or returned",
		"pkg/immich/reject.go:71:24: Immich request JSON contract must be a named struct; got map",
		"pkg/immich/reject.go:79:31: Immich request JSON contract must be a named struct; got external struct Buffer",
		"pkg/immich/reject.go:85:49: Immich request JSON contract must be a named struct; got slice",
		"pkg/immich/reject.go:92:41: Immich response JSON contract must use a named provider DTO; got anonymous struct",
		"pkg/immich/reject.go:97:47: Immich response JSON contract must use a named provider DTO; got map",
	}, diagnostics)
}

func TestCheckGoContractsAcceptsNamedContractsAndNonTransportSerialization(t *testing.T) {
	diagnostics, err := CheckGo(filepath.Join("testdata", "go"), "./allow")
	require.NoError(t, err)
	assert.Empty(t, diagnostics)
}
