package worker

import (
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

func TestNewValidatesRequiredDependencies(t *testing.T) {
	_, err := New(nil, config.WorkerConfig{}, "owner", nil)
	require.EqualError(t, err, "worker database is required")
	_, err = New(new(bun.DB), config.WorkerConfig{}, "", nil)
	require.EqualError(t, err, "worker lease owner is required")
}

func TestHealthyIsFalseBeforeStart(t *testing.T) {
	worker := &Worker{}
	assert.False(t, worker.Healthy(time.Minute))
}
