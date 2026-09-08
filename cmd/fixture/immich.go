package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const fixtureAPIKey = "fixture-only-key"

type fixtureState struct {
	Available    bool `json:"available"`
	Unauthorized bool `json:"unauthorized"`
	Unsupported  bool `json:"unsupported"`
}

type immichFixture struct {
	mu          sync.RWMutex
	state       fixtureState
	restart     func(context.Context) error
	albums      []sourceAlbum
	assets      map[string]sourceAsset
	checkpoints map[string]*checkpoint
	requests    map[string]int
}

func newImmichFixture(offline bool) *immichFixture {
	albums, assets := fixtureLibrary()
	return &immichFixture{state: fixtureState{Available: !offline}, albums: albums, assets: assets,
		requests: map[string]int{}, checkpoints: map[string]*checkpoint{
			"asset-metadata": {Mode: "open", released: make(chan struct{})},
			"import-release": {Mode: "open", released: make(chan struct{})},
		}}
}

// ServeHTTP implements the read-only Immich 3.1 contract used by Memento.
// Control routes exist in this development command, never in the application.
func (f *immichFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/__fixture/checkpoints" || strings.HasPrefix(r.URL.Path, "/__fixture/checkpoints/") {
		f.checkpointControl(w, r)
		return
	}
	switch r.URL.Path {
	case "/__fixture/requests":
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		f.mu.RLock()
		defer f.mu.RUnlock()
		if err := json.NewEncoder(w).Encode(f.requests); err != nil {
			return
		}
		return
	case "/__fixture/state":
		if r.Method == http.MethodGet {
			f.mu.RLock()
			defer f.mu.RUnlock()
			if err := json.NewEncoder(w).Encode(f.state); err != nil {
				return
			}
			return
		}
		if !controlRequest(w, r) {
			return
		}
		var state struct {
			Available    *bool `json:"available"`
			Unauthorized bool  `json:"unauthorized"`
			Unsupported  bool  `json:"unsupported"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&state); err != nil || state.Available == nil {
			http.Error(w, "available boolean is required", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.state = fixtureState{Available: *state.Available, Unauthorized: state.Unauthorized, Unsupported: state.Unsupported}
		f.mu.Unlock()
		if err := json.NewEncoder(w).Encode(fixtureState{Available: *state.Available, Unauthorized: state.Unauthorized, Unsupported: state.Unsupported}); err != nil {
			return
		}
		return
	case "/__fixture/restart":
		if !controlRequest(w, r) {
			return
		}
		if f.restart == nil {
			http.Error(w, "API restart is not configured", http.StatusServiceUnavailable)
			return
		}
		if err := f.restart(r.Context()); err != nil {
			http.Error(w, "API restart failed; see supervisor stderr", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	f.mu.Lock()
	f.requests[r.Method+" "+r.URL.Path]++
	state := f.state
	f.mu.Unlock()
	if r.Method != http.MethodGet && (r.Method != http.MethodPost || r.URL.Path != "/api/search/metadata") {
		http.Error(w, "fixture supports read-only Immich requests", http.StatusMethodNotAllowed)
		return
	}
	if !state.Available {
		http.Error(w, "Immich fixture is offline", http.StatusServiceUnavailable)
		return
	}
	if r.URL.Path != "/api/server/version" && (state.Unauthorized || r.Header.Get("X-Api-Key") != fixtureAPIKey) {
		http.Error(w, "Invalid API key", http.StatusUnauthorized)
		return
	}
	switch {
	case r.URL.Path == "/api/server/version":
		minor := 1
		if state.Unsupported {
			minor = 2
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"major": 3, "minor": minor, "patch": 0, "prerelease": nil}); err != nil {
			return
		}
	case r.URL.Path == "/api/users/me":
		_, _ = w.Write([]byte(`{"id":"fixture-owner"}`))
	case r.URL.Path == "/api/albums":
		if err := json.NewEncoder(w).Encode(f.albums); err != nil {
			return
		}
	case strings.HasPrefix(r.URL.Path, "/api/albums/"):
		id := strings.TrimPrefix(r.URL.Path, "/api/albums/")
		for _, album := range f.albums {
			if album.ID == id {
				if err := json.NewEncoder(w).Encode(album); err != nil {
					return
				}
				return
			}
		}
		http.NotFound(w, r)
	case r.URL.Path == "/api/search/metadata" && r.Method == http.MethodPost:
		f.searchMembers(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/assets/"):
		path := strings.TrimPrefix(r.URL.Path, "/api/assets/")
		id, thumbnail := strings.CutSuffix(path, "/thumbnail")
		asset, ok := f.assets[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if thumbnail {
			if r.URL.Query().Get("size") != "thumbnail" && r.URL.Query().Get("size") != "preview" {
				http.Error(w, "only generated thumbnails are supported", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", asset.ContentType)
			w.Header().Set("Cache-Control", "private, max-age=3600")
			_, _ = w.Write(asset.Thumbnail)
			return
		}
		if id == "fixture-asset-02" && !f.reachCheckpoint(w, r, "asset-metadata") {
			return
		}
		if id == "fixture-asset-06" && !f.reachCheckpoint(w, r, "import-release") {
			return
		}
		if err := json.NewEncoder(w).Encode(asset); err != nil {
			return
		}
	default:
		http.NotFound(w, r)
	}
}

func (f *immichFixture) searchMembers(w http.ResponseWriter, r *http.Request) {
	var input struct {
		AlbumIDs    []string `json:"albumIds"`
		Page        int      `json:"page"`
		Size        int      `json:"size"`
		WithStacked bool     `json:"withStacked"`
		WithExif    bool     `json:"withExif"`
		Order       string   `json:"order"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || len(input.AlbumIDs) != 1 || input.Page < 1 || input.Size < 1 || input.Size > 1000 || !input.WithStacked || input.WithExif || input.Order != "asc" {
		http.Error(w, "expected paginated album metadata search with all stack members and no EXIF join", http.StatusBadRequest)
		return
	}
	for _, album := range f.albums {
		if album.ID != input.AlbumIDs[0] {
			continue
		}
		members := make([]sourceAsset, 0, len(album.Members))
		for _, id := range album.Members {
			members = append(members, f.assets[id])
		}
		sort.Slice(members, func(i, j int) bool {
			if members[i].FileCreatedAt == members[j].FileCreatedAt {
				return members[i].ID < members[j].ID
			}
			return members[i].FileCreatedAt < members[j].FileCreatedAt
		})
		start := len(members)
		if input.Page <= (len(members)+input.Size-1)/input.Size {
			start = (input.Page - 1) * input.Size
		}
		end := min(start+input.Size, len(members))
		var next *string
		if end < len(members) {
			value := strconv.Itoa(input.Page + 1)
			next = &value
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"albums": map[string]any{"items": []sourceAlbum{}},
			"assets": map[string]any{"items": members[start:end], "count": end - start, "total": end - start, "nextPage": next, "facets": []string{}},
		}); err != nil {
			return
		}
		return
	}
	http.NotFound(w, r)
}

func controlRequest(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return false
	}
	if r.Header.Get("Origin") != "" {
		http.Error(w, "fixture controls are not browser endpoints", http.StatusForbidden)
		return false
	}
	return true
}
