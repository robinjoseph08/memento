package database

import (
	"context"
	"testing"

	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenRejectsInvalidDriverOptionsWithoutPanicOrSecret(t *testing.T) {
	secret := "never-print-this"
	cfg := config.DatabaseConfig{
		URL:          "postgresql://memento:" + secret + "@db:5432/memento?sslmode=bogus",
		Name:         "memento",
		MaxOpenConns: 2,
	}

	_, err := Open(context.Background(), cfg)

	require.EqualError(t, err, "parse Memento database URL")
	assert.NotContains(t, err.Error(), secret)
}
