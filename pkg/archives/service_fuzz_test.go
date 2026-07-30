package archives

import (
	"encoding/base64"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func FuzzArchiveSelection(f *testing.F) {
	validEvent := uuid.MustParse("11111111-1111-4111-8111-111111111111").String()
	validMedia := uuid.MustParse("22222222-2222-4222-8222-222222222222").String()
	f.Add("event", validEvent, validMedia, false)
	f.Add("subset", validEvent, validMedia, true)
	f.Add("subset", "", validMedia, false)
	f.Add("subset", "", "not-an-id", false)
	f.Fuzz(func(t *testing.T, scope, eventID, mediaID string, duplicate bool) {
		if len(scope)+len(eventID)+len(mediaID) > 16<<10 {
			t.Skip()
		}
		request := PlanRequest{Scope: scope}
		if eventID != "" {
			request.EventID = &eventID
		}
		if mediaID != "" {
			request.MediaIDs = []string{mediaID}
			if duplicate {
				request.MediaIDs = append(request.MediaIDs, mediaID)
			}
		}
		selection, err := parsePlanSelection(request)
		if err != nil {
			assert.ErrorIs(t, err, ErrInvalidSelection)
			return
		}
		switch scope {
		case "event":
			require.NotEqual(t, uuid.Nil, selection.eventID)
			require.Empty(t, selection.media)
			require.Empty(t, request.MediaIDs)
		case "subset":
			require.Nil(t, request.EventID)
			require.NotEmpty(t, selection.media)
			require.Len(t, selection.media, len(request.MediaIDs))
		default:
			t.Fatalf("unknown scope %q was accepted", scope)
		}
	})
}

func FuzzArchiveToken(f *testing.F) {
	valid := make([]byte, 32)
	for index := range valid {
		valid[index] = byte(index)
	}
	f.Add(base64.RawURLEncoding.EncodeToString(valid))
	f.Add("")
	f.Add("token=secret")
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 16<<10 {
			t.Skip()
		}
		decoded, err := decodeToken(raw)
		if err != nil {
			require.ErrorIs(t, err, ErrNotFound)
			if len(raw) > 16 {
				assert.NotContains(t, err.Error(), raw)
			}
			return
		}
		require.Len(t, decoded, 32)
		require.Equal(t, raw, base64.RawURLEncoding.EncodeToString(decoded[:]))
	})
}
