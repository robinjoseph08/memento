package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPublicPageMetadata(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		path        string
		title       string
		description string
	}{
		{path: "/", description: "View photos and videos shared with you through Memento."},
		{path: "/setup", title: "Setup", description: "Set up Memento to start sharing photos and videos from Immich."},
		{path: "/sign-in", title: "Sign in", description: "Sign in to view photos and videos shared with you."},
		{path: "/access-denied", title: "Access denied", description: "This account does not have access to the requested Memento page."},
	} {
		t.Run(test.path, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			metadata, ok := publicPageMetadata(request)
			assert.True(t, ok)
			assert.Equal(t, test.title, metadata.Title)
			assert.Equal(t, test.description, metadata.Description)
		})
	}
}

func TestPublicPageMetadataDoesNotDescribeAuthenticatedRoute(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodGet, "/curator/people/person-id", nil)
	_, ok := publicPageMetadata(request)
	assert.False(t, ok)
}
