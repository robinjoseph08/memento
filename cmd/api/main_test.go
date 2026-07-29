package main

import (
	"testing"

	"github.com/robinjoseph08/memento/pkg/archives"
	"github.com/robinjoseph08/memento/pkg/comments"
	"github.com/robinjoseph08/memento/pkg/emaildelivery"
	"github.com/robinjoseph08/memento/pkg/events"
	"github.com/robinjoseph08/memento/pkg/sources"
	"github.com/stretchr/testify/assert"
)

func TestJobHandlersAlwaysRegistersDomainHandlers(t *testing.T) {
	handlers := jobHandlers(&sources.Service{}, &events.Service{}, &archives.Service{}, &comments.Service{}, &emaildelivery.Service{}, false)
	assert.Contains(t, handlers, sources.ReconciliationJobKind)
	assert.Contains(t, handlers, events.PublicationJobKind)
	assert.Contains(t, handlers, archives.CleanupJobKind)
	assert.Contains(t, handlers, comments.CommentJobKind)
	assert.NotContains(t, handlers, emaildelivery.JobKind)
	assert.NotContains(t, handlers, emaildelivery.ImmediateJobKind)
	assert.NotContains(t, handlers, emaildelivery.WeeklyJobKind)

	handlers = jobHandlers(&sources.Service{}, &events.Service{}, &archives.Service{}, &comments.Service{}, &emaildelivery.Service{}, true)
	assert.Contains(t, handlers, sources.ReconciliationJobKind)
	assert.Contains(t, handlers, events.PublicationJobKind)
	assert.Contains(t, handlers, archives.CleanupJobKind)
	assert.Contains(t, handlers, comments.CommentJobKind)
	assert.Contains(t, handlers, emaildelivery.JobKind)
	assert.Contains(t, handlers, emaildelivery.ImmediateJobKind)
	assert.Contains(t, handlers, emaildelivery.WeeklyJobKind)
}
