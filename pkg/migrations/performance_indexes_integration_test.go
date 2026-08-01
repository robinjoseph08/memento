//go:build integration

package migrations

import (
	"context"
	"strings"
	"testing"

	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTargetScaleProjectionIndexesRetainAuthorizationPaths(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, db))

	type indexRow struct {
		Name       string `bun:"indexname"`
		Definition string `bun:"indexdef"`
	}
	var indexes []indexRow
	require.NoError(t, db.NewRaw(`SELECT indexname,indexdef FROM pg_indexes
		WHERE schemaname=current_schema() AND tablename='current_audience_entitlements'
		ORDER BY indexname`).Scan(ctx, &indexes))

	definitions := make(map[string]string, len(indexes))
	for _, index := range indexes {
		definitions[index.Name] = index.Definition
	}
	assert.Contains(t, definitions, "current_audience_entitlements_pkey")
	assert.Contains(t, definitions, "current_entitlements_media_idx")
	assert.Contains(t, definitions, "current_entitlements_event_idx")
	assert.NotContains(t, definitions, "current_entitlements_recipient_idx", "the access-leading primary key replaces the duplicate index")
	assert.Contains(t, strings.ReplaceAll(definitions["current_audience_entitlements_pkey"], " ", ""), "(recipient_access_generation_id,event_id,media_item_id)")
	assert.Contains(t, strings.ReplaceAll(definitions["current_entitlements_media_idx"], " ", ""), "(media_item_id,recipient_access_generation_id)")
	assert.Contains(t, strings.ReplaceAll(definitions["current_entitlements_event_idx"], " ", ""), "(event_id,recipient_access_generation_id,media_item_id)")

	var chronologyIndex string
	require.NoError(t, db.NewRaw(`SELECT indexdef FROM pg_indexes
		WHERE schemaname=current_schema() AND tablename='current_published_placements'
		  AND indexname='current_placements_chronology_idx'`).Scan(ctx, &chronologyIndex))
	compactChronologyIndex := strings.ReplaceAll(chronologyIndex, " ", "")
	assert.Contains(t, compactChronologyIndex, "COALESCE(local_date_time,''::text)")
	assert.Contains(t, compactChronologyIndex, "media_item_idDESC")
	assert.Contains(t, compactChronologyIndex, "capture_date")
}

func TestCompactedSearchDocumentsKeepStableConstraintNames(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, db))

	var constraints []string
	require.NoError(t, db.NewRaw(`SELECT conname FROM pg_constraint
		WHERE conrelid='published_search_documents'::regclass ORDER BY conname`).Scan(ctx, &constraints))
	assert.Equal(t, []string{
		"published_search_documents_event_id_fkey",
		"published_search_documents_media_item_id_fkey",
		"published_search_documents_pkey",
		"published_search_documents_publication_id_fkey",
	}, constraints)
}
