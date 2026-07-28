package staging

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChangeKindRejectsUnknownPersistedValue(t *testing.T) {
	var changes []Change
	err := json.Unmarshal([]byte(`[{"kind":"replacement"}]`), &changes)
	require.Error(t, err)
	assert.ErrorContains(t, err, `unknown Staged change kind "replacement"`)
}

func TestChangeKindAcceptsEverySupportedValue(t *testing.T) {
	for _, kind := range []ChangeKind{
		ChangeKindAddition,
		ChangeKindRemoval,
		ChangeKindMove,
		ChangeKindMetadata,
		ChangeKindMomentStructure,
		ChangeKindAccess,
	} {
		t.Run(string(kind), func(t *testing.T) {
			var decoded ChangeKind
			require.NoError(t, json.Unmarshal([]byte(`"`+string(kind)+`"`), &decoded))
			assert.Equal(t, kind, decoded)
		})
	}
}
