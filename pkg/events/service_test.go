package events

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRestoredPlacementInsertAtUsesPublishedSuccessorWithoutReorderingEditableMedia(t *testing.T) {
	targetMoment, nextMoment := uuid.New(), uuid.New()
	addition, publishedSuccessor, laterMedia := uuid.New(), uuid.New(), uuid.New()
	targetPosition, nextPosition, successorPosition := 1, 2, 2
	placements := []editablePlacementOrder{
		{MediaItemID: addition, DraftMomentID: &targetMoment, MomentPosition: &targetPosition},
		{MediaItemID: publishedSuccessor, DraftMomentID: &targetMoment, PublishedPosition: &successorPosition, MomentPosition: &targetPosition},
		{MediaItemID: laterMedia, DraftMomentID: &nextMoment, MomentPosition: &nextPosition},
	}

	assert.Equal(t, 1, restoredPlacementInsertAt(placements, targetMoment, targetPosition, 1), "restoration stays after an editable addition and before its published successor")
	assert.Equal(t, 2, restoredPlacementInsertAt(placements, targetMoment, targetPosition, 3), "restoration without a successor stays after the target Moment's editable order")
	assert.Equal(t, 0, restoredPlacementInsertAt(placements[2:], targetMoment, targetPosition, 0), "an empty restored Moment precedes the next editable Moment")
}

func TestEventDateRangeRequiresCanonicalCompleteOrderedDates(t *testing.T) {
	start, end := "2026-08-01", "2026-08-03"
	normalizedStart, normalizedEnd, valid := normalizeEventDateRange(&start, &end)
	require.True(t, valid)
	assert.Equal(t, start, *normalizedStart)
	assert.Equal(t, end, *normalizedEnd)

	_, _, valid = normalizeEventDateRange(nil, nil)
	assert.True(t, valid)
	_, _, valid = normalizeEventDateRange(&start, nil)
	assert.False(t, valid)
	reversed := "2026-07-31"
	_, _, valid = normalizeEventDateRange(&start, &reversed)
	assert.False(t, valid)
	noncanonical := "2026-8-1"
	_, _, valid = normalizeEventDateRange(&noncanonical, &end)
	assert.False(t, valid)
}

func TestCreateEventRejectsTooManySourcesBeforeDatabaseAccess(t *testing.T) {
	sourceIDs := make([]string, maxDraftSourceAlbums+1)
	for index := range sourceIDs {
		sourceIDs[index] = uuid.NewString()
	}
	_, err := new(Service).CreateEvent(t.Context(), setup.CuratorSession{}, CreateEventRequest{
		SourceAlbumIDs: sourceIDs,
		Timezone:       "UTC",
	})
	require.ErrorIs(t, err, ErrInvalid)
}

func TestParseUniqueIDsRejectsMalformedAndDuplicatePortalIdentities(t *testing.T) {
	_, err := parseUniqueIDs([]string{"not-an-id"})
	require.ErrorIs(t, err, ErrInvalid)
	id := "11111111-1111-4111-8111-111111111111"
	_, err = parseUniqueIDs([]string{id, id})
	require.ErrorIs(t, err, ErrInvalid)
	ids, err := parseUniqueIDs(nil)
	require.NoError(t, err)
	assert.Empty(t, ids)
}

func TestPlaceLabelLimitsUseRuneLengthAndMapToAccurateGuidance(t *testing.T) {
	labels := make([]string, 21)
	for index := range labels {
		labels[index] = uuid.NewString()
	}
	_, valid := normalizePlaceLabels(labels)
	assert.False(t, valid)

	_, valid = normalizePlaceLabels([]string{strings.Repeat("é", 121)})
	assert.False(t, valid)
	accepted, valid := normalizePlaceLabels([]string{strings.Repeat("é", 120)})
	assert.True(t, valid)
	assert.Equal(t, []string{strings.Repeat("é", 120)}, accepted)

	accepted, valid = normalizePlaceLabels([]string{" Garden ", "garden", "Café"})
	assert.True(t, valid)
	assert.Equal(t, []string{"Garden", "Café"}, accepted,
		"Moment merges can preserve the ordered union without duplicate labels")

	mapped := draftError(ErrPlaceLabelsInvalid, "Event")
	var coded *errcodes.Error
	require.ErrorAs(t, mapped, &coded)
	assert.Equal(t, "validation_error", coded.Code)
	assert.Equal(t, "Use no more than 20 Place labels, with 1 to 120 characters in each label.", coded.Message)
}
