package models

import (
	"encoding/json"
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewUUIDv7(t *testing.T) {
	t.Parallel()

	id := NewUUIDv7()
	parsed, err := uuid.Parse(id.String())
	require.NoError(t, err)
	assert.Equal(t, byte(7), parsed[6]>>4)
}

func TestUUIDDatabaseValueAndScan(t *testing.T) {
	t.Parallel()

	want := NewUUIDv7()
	value, err := want.Value()
	require.NoError(t, err)
	assert.Equal(t, want.String(), value)

	for name, source := range map[string]any{
		"string":       want.String(),
		"text bytes":   []byte(want.String()),
		"binary bytes": append([]byte(nil), want[:]...),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var got UUID
			require.NoError(t, got.Scan(source))
			assert.Equal(t, want, got)
		})
	}

	cleared := want
	require.NoError(t, cleared.Scan(nil))
	assert.Equal(t, UUID{}, cleared)
	require.Error(t, cleared.Scan("invalid"))
	require.Error(t, cleared.Scan(42))
}

func TestUUIDJSONRoundTrip(t *testing.T) {
	t.Parallel()

	want := NewUUIDv7()
	encoded, err := json.Marshal(want)
	require.NoError(t, err)
	assert.JSONEq(t, `"`+want.String()+`"`, string(encoded))

	var got UUID
	require.NoError(t, json.Unmarshal(encoded, &got))
	assert.Equal(t, want, got)
}
