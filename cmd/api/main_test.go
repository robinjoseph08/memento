package main

import (
	"testing"

	"github.com/robinjoseph08/memento/pkg/emaildelivery"
	"github.com/robinjoseph08/memento/pkg/sources"
	"github.com/stretchr/testify/assert"
)

func TestJobHandlersAlwaysRegistersSourceReconciliation(t *testing.T) {
	handlers := jobHandlers(&sources.Service{}, &emaildelivery.Service{}, false)
	assert.Contains(t, handlers, sources.ReconciliationJobKind)
	assert.NotContains(t, handlers, emaildelivery.JobKind)

	handlers = jobHandlers(&sources.Service{}, &emaildelivery.Service{}, true)
	assert.Contains(t, handlers, sources.ReconciliationJobKind)
	assert.Contains(t, handlers, emaildelivery.JobKind)
}
