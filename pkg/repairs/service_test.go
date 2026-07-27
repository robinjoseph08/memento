package repairs

import (
	"testing"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersonRepairContractForMergesReassignmentAndAnchorLoss(t *testing.T) {
	current, replacement := uuid.New(), uuid.New()
	firstFace, secondFace := uuid.New(), uuid.New()
	anchors := []anchorRow{{FaceID: firstFace}, {FaceID: secondFace}}
	tests := []struct {
		name          string
		present       map[uuid.UUID]immich.PersonSummary
		faces         map[uuid.UUID]immich.FaceSummary
		wantMatched   bool
		wantCandidate *uuid.UUID
		wantConflict  string
	}{
		{
			name:    "healthy link remains suggestion-capable",
			present: map[uuid.UUID]immich.PersonSummary{current: {SourceID: current}},
			faces: map[uuid.UUID]immich.FaceSummary{
				firstFace:  {SourceID: firstFace, PersonID: &current},
				secondFace: {SourceID: secondFace, PersonID: &current},
			},
			wantMatched: true,
		},
		{
			name:    "Immich merge proposes the surviving destination",
			present: map[uuid.UUID]immich.PersonSummary{replacement: {SourceID: replacement}},
			faces: map[uuid.UUID]immich.FaceSummary{
				firstFace:  {SourceID: firstFace, PersonID: &replacement},
				secondFace: {SourceID: secondFace, PersonID: &replacement},
			},
			wantCandidate: &replacement,
			wantConflict:  "immich_person_missing",
		},
		{
			name:    "face reassignment split is ambiguous",
			present: map[uuid.UUID]immich.PersonSummary{current: {SourceID: current}, replacement: {SourceID: replacement}},
			faces: map[uuid.UUID]immich.FaceSummary{
				firstFace:  {SourceID: firstFace, PersonID: &current},
				secondFace: {SourceID: secondFace, PersonID: &replacement},
			},
			wantConflict: "anchors_split_across_people",
		},
		{
			name:         "forced face recreation leaves anchors missing",
			present:      map[uuid.UUID]immich.PersonSummary{current: {SourceID: current}},
			faces:        map[uuid.UUID]immich.FaceSummary{},
			wantConflict: "face_anchor_missing",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate, conflicts, matched := evaluatePersonLink(current, test.present, anchors, test.faces)
			assert.Equal(t, test.wantMatched, matched)
			if test.wantCandidate == nil {
				assert.Nil(t, candidate)
			} else {
				require.NotNil(t, candidate)
				assert.Equal(t, *test.wantCandidate, *candidate)
			}
			if test.wantConflict != "" {
				assert.Contains(t, conflicts, test.wantConflict)
			}
		})
	}
}

func TestMissingPersonWithoutAnchorsRequiresReviewWithoutGuessingByName(t *testing.T) {
	current := uuid.New()
	candidate, conflicts, matched := evaluatePersonLink(current, map[uuid.UUID]immich.PersonSummary{}, nil, nil)
	assert.False(t, matched)
	assert.Nil(t, candidate)
	assert.Equal(t, []string{"immich_person_missing", "no_surviving_face_anchors"}, conflicts)
}
