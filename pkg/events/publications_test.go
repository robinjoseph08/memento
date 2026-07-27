package events

import (
	"context"
	"testing"

	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/stretchr/testify/require"
)

func TestPublicationJobsRejectInvalidPayloadPermanently(t *testing.T) {
	err := new(Service).HandlePublicationJob(context.Background(), worker.Job{Payload: []byte(`{"event_id":"not-an-id"}`)})
	require.EqualError(t, err, "invalid_publication_payload")
}
