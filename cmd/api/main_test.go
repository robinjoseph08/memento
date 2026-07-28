package main

import (
	"testing"

	"github.com/robinjoseph08/memento/pkg/archives"
	"github.com/robinjoseph08/memento/pkg/emaildelivery"
	"github.com/robinjoseph08/memento/pkg/events"
	"github.com/robinjoseph08/memento/pkg/sources"
	"github.com/stretchr/testify/assert"
)

func TestJobHandlersAlwaysRegistersDomainHandlers(t *testing.T) {
	handlers := jobHandlers(&sources.Service{}, &events.Service{}, &archives.Service{}, &emaildelivery.Service{}, false)
	assert.Contains(t, handlers, sources.ReconciliationJobKind)
	assert.Contains(t, handlers, events.PublicationJobKind)
	assert.Contains(t, handlers, archives.CleanupJobKind)
	assert.NotContains(t, handlers, emaildelivery.JobKind)

	handlers = jobHandlers(&sources.Service{}, &events.Service{}, &archives.Service{}, &emaildelivery.Service{}, true)
	assert.Contains(t, handlers, sources.ReconciliationJobKind)
	assert.Contains(t, handlers, events.PublicationJobKind)
	assert.Contains(t, handlers, archives.CleanupJobKind)
	assert.Contains(t, handlers, emaildelivery.JobKind)
}
