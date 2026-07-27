package audiences

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAudienceTargetAndAttendanceInputValidation(t *testing.T) {
	id := uuid.New()
	_, err := validTarget("publication", id)
	require.ErrorIs(t, err, ErrInvalid)
	_, err = validTarget(targetMoment, uuid.Nil)
	require.ErrorIs(t, err, ErrInvalid)

	_, err = parseIDs([]string{id.String(), id.String()})
	require.ErrorIs(t, err, ErrInvalid)
	_, err = parseIDs([]string{"not-an-id"})
	require.ErrorIs(t, err, ErrInvalid)
	ids, err := parseIDs(nil)
	require.NoError(t, err)
	assert.Empty(t, ids)
}
