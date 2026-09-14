package publishing_test

import (
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVisibleEntriesUseViewerEligibilityWithoutCuratorBypass(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	now := time.Now().UTC()
	curator := models.Person{ID: models.NewUUIDv7(), DisplayName: "Curator", IsCurator: true, CreatedAt: now}
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: now}
	_, err := db.NewInsert().Model(&[]models.Person{curator, person}).Exec(t.Context())
	require.NoError(t, err)
	visible := func(id string) []notifications.VisibleEntry {
		entries, err := module.VisibleEntries(t.Context(), db, id)
		require.NoError(t, err)
		return entries
	}
	assert.Empty(t, visible(curator.ID.String()), "an unpublished Album is invisible even to a Curator")
	assert.Empty(t, visible(person.ID.String()))
	_, err = module.SaveAlbumAccess(t.Context(), album.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: person.ID.String(), Allowed: true}}})
	require.NoError(t, err)
	assert.Empty(t, visible(person.ID.String()), "access alone is not visibility before publication")
	review, err := module.ReviewPublication(t.Context(), album.ID)
	require.NoError(t, err)
	_, err = module.PublishAlbum(t.Context(), album.ID, publishing.PublishRequest{ReviewToken: review.ReviewToken})
	require.NoError(t, err)
	entries := visible(person.ID.String())
	total := 0
	for _, moment := range album.Moments {
		total += len(moment.Entries)
	}
	require.Len(t, entries, total)
	for _, entry := range entries {
		assert.Equal(t, album.ID, entry.AlbumID)
		assert.Equal(t, album.Title, entry.AlbumTitle)
		assert.Equal(t, "IMAGE", entry.Kind)
	}
	assert.Empty(t, visible(curator.ID.String()), "publication does not make bypass count as viewer visibility")
	_, err = module.SaveEntryRules(t.Context(), album.ID, album.Moments[0].Entries[0].ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: person.ID.String(), Decision: publishing.DecisionDeny}}})
	require.NoError(t, err)
	assert.Len(t, visible(person.ID.String()), total-1)
	_, err = module.UnpublishAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	assert.Empty(t, visible(person.ID.String()))
	_, err = module.VisibleEntries(t.Context(), db, "not-a-person")
	require.Error(t, err)
}
