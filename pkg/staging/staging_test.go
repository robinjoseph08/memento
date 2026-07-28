package staging

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChangeKindRejectsUnknownPersistedValue(t *testing.T) {
	var changes []Change
	err := json.Unmarshal([]byte(`[{"kind":"replacement"}]`), &changes)
	require.Error(t, err)
	assert.ErrorContains(t, err, `unknown Staged change kind "replacement"`)
}

func TestMeaningfulMovesIgnorePositionCompactionAndAdditions(t *testing.T) {
	momentID := uuid.New()
	removedID, firstID, secondID, addedID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	published := []stagedPlacement{
		{MediaItemID: removedID, MomentID: momentID, Position: 3},
		{MediaItemID: firstID, MomentID: momentID, Position: 7},
		{MediaItemID: secondID, MomentID: momentID, Position: 11},
	}
	draft := []stagedPlacement{
		{MediaItemID: addedID, MomentID: momentID, Position: 0},
		{MediaItemID: firstID, MomentID: momentID, Position: 1},
		{MediaItemID: secondID, MomentID: momentID, Position: 2},
	}

	assert.Empty(t, meaningfulMoves(draft, published))
}

func TestMeaningfulMovesReportMomentChangesAndRetainedReordering(t *testing.T) {
	firstMomentID, secondMomentID := uuid.New(), uuid.New()
	movedMomentID, firstID, secondID := uuid.New(), uuid.New(), uuid.New()
	published := []stagedPlacement{
		{MediaItemID: movedMomentID, MomentID: firstMomentID, Position: 0},
		{MediaItemID: firstID, MomentID: secondMomentID, Position: 1},
		{MediaItemID: secondID, MomentID: secondMomentID, Position: 2},
	}
	draft := []stagedPlacement{
		{MediaItemID: secondID, MomentID: secondMomentID, Position: 0},
		{MediaItemID: firstID, MomentID: secondMomentID, Position: 1},
		{MediaItemID: movedMomentID, MomentID: secondMomentID, Position: 2},
	}

	assert.Equal(t, []string{secondID.String(), firstID.String(), movedMomentID.String()}, meaningfulMoves(draft, published))
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
