package main

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// testdata/openapi-3.2.1.json is an excerpt of Immich's official v3.2.1
// open-api/immich-openapi-specs.json, with prose and unrelated operations omitted.
// It is test input only; production adapter types are never generated from it.
func TestOpenAPIPreflight(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, from, to, failure string }{
		{name: "official contract"},
		{name: "response replaced", from: `"$ref": "#/components/schemas/ServerVersionResponseDto"`, to: `"$ref": "#/components/schemas/AlbumResponseDto"`, failure: "/server/version"},
		{name: "endpoint removed", from: `"/faces":`, to: `"/faces-removed":`, failure: "/faces"},
		{name: "field removed", from: `"localDateTime": {`, to: `"renamedLocalDateTime": {`, failure: "localDateTime"},
		{name: "incompatible type", from: `"duration": {\n            "nullable": true,\n            "type": "integer"`, to: `"duration": {\n            "nullable": true,\n            "type": "string"`, failure: "duration"},
		{name: "new required search input", from: `"MetadataSearchDto": {`, to: `"MetadataSearchDto": {"required":["newRequiredInput"],`, failure: "newRequiredInput"},
		{name: "wrong release", from: `"version": "3.2.1"`, to: `"version": "3.1.0"`, failure: "version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile("testdata/openapi-3.2.1.json")
			require.NoError(t, err)
			if tc.from != "" {
				from := bytes.ReplaceAll([]byte(tc.from), []byte(`\n`), []byte("\n"))
				to := bytes.ReplaceAll([]byte(tc.to), []byte(`\n`), []byte("\n"))
				require.Contains(t, string(data), string(from))
				data = bytes.Replace(data, from, to, 1)
			}
			require.True(t, json.Valid(data))
			err = CheckOpenAPI(bytes.NewReader(data), "v3.2.1")
			if tc.failure == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.failure)
			}
		})
	}
}
