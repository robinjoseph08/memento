package version

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultVersion(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "dev", Version)
}
