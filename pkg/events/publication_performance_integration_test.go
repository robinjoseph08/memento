//go:build integration

package events

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicationStoresOneSearchDocumentPerPlacementAcrossRecipients(t *testing.T) {
	fixture := newPublicationFixture(t)
	_, err := fixture.service.PublishEvent(context.Background(), fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)

	var entitlements, documents int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM current_audience_entitlements WHERE event_id=? AND media_item_id=?`, fixture.event, fixture.media[0]).Scan(context.Background(), &entitlements))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM published_search_documents WHERE event_id=? AND media_item_id=?`, fixture.event, fixture.media[0]).Scan(context.Background(), &documents))
	assert.Equal(t, 2, entitlements, "two Recipient generations may share one placement")
	assert.Equal(t, 1, documents, "authorization stays in entitlements instead of duplicating identical search text")
}
