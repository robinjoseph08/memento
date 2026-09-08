package server

import (
	"net/http"
	"path"

	"github.com/robinjoseph08/memento/internal/webapp"
)

func publicPageMetadata(request *http.Request) (webapp.PageMetadata, bool) {
	switch path.Clean(request.URL.Path) {
	case "/":
		return webapp.PageMetadata{
			Description: "View photos and videos shared with you through Memento.",
		}, true
	case "/access-denied":
		return webapp.PageMetadata{
			Title:       "Access denied",
			Description: "This account does not have access to the requested Memento page.",
		}, true
	case "/setup":
		return webapp.PageMetadata{
			Title:       "Setup",
			Description: "Set up Memento to start sharing photos and videos from Immich.",
		}, true
	case "/sign-in":
		return webapp.PageMetadata{
			Title:       "Sign in",
			Description: "Sign in to view photos and videos shared with you.",
		}, true
	default:
		return webapp.PageMetadata{}, false
	}
}
