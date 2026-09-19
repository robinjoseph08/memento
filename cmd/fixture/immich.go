package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/robinjoseph08/memento/pkg/notifications/smtptest"
)

const fixtureAPIKey = "fixture-only-key"

type fixtureState struct {
	Available    bool `json:"available"`
	Unauthorized bool `json:"unauthorized"`
	Unsupported  bool `json:"unsupported"`
}

type immichFixture struct {
	mu      sync.RWMutex
	state   fixtureState
	restart func(context.Context) error
	// library is immutable once published; control edits install a new one.
	library          sourceLibrary
	personThumbnails map[string]sourceAsset
	checkpoints      map[string]*checkpoint
	requests         map[string]int
	// smtp is nil when the fixture runs without email.
	smtp *smtptest.Server
}

type sourceFace struct {
	ID            string       `json:"id"`
	ImageHeight   int          `json:"imageHeight"`
	ImageWidth    int          `json:"imageWidth"`
	BoundingBoxX1 int          `json:"boundingBoxX1"`
	BoundingBoxX2 int          `json:"boundingBoxX2"`
	BoundingBoxY1 int          `json:"boundingBoxY1"`
	BoundingBoxY2 int          `json:"boundingBoxY2"`
	SourceType    string       `json:"sourceType"`
	Person        sourcePerson `json:"person"`
}

type sourcePerson struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	BirthDate     *string `json:"birthDate"`
	ThumbnailPath string  `json:"thumbnailPath"`
	Hidden        bool    `json:"isHidden"`
	UpdatedAt     string  `json:"updatedAt"`
}

// sourceLibrary is the fake Immich content: albums, assets, and faces.
type sourceLibrary struct {
	albums []sourceAlbum
	assets map[string]sourceAsset
	faces  map[string][]sourceFace
}

func newImmichFixture(offline bool) *immichFixture {
	albums, assets := fixtureLibrary()
	faces, thumbnails := fixtureFaces()
	return &immichFixture{state: fixtureState{Available: !offline}, library: sourceLibrary{albums: albums, assets: assets, faces: faces},
		personThumbnails: thumbnails, requests: map[string]int{}, checkpoints: map[string]*checkpoint{
			"asset-metadata": {Mode: "open", released: make(chan struct{})},
			"import-release": {Mode: "open", released: make(chan struct{})},
			"chapter-probe":  {Mode: "open", released: make(chan struct{})},
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
	if r.URL.Path == "/__fixture/smtp" {
		f.smtpControl(w, r)
		return
	}
	if r.URL.Path == "/__fixture/library" {
		f.libraryControl(w, r)
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
	if r.Header.Get("Range") != "" {
		// Ranged reads are counted apart so playback seeking and ffprobe's
		// header probing can be told from whole-file downloads.
		f.requests[r.Method+" "+r.URL.Path+" (range)"]++
	}
	state := f.state
	// The library is replaced whole by control edits, so one snapshot serves
	// this request consistently without holding the lock.
	library := f.library
	f.mu.Unlock()
	// Immich answers HEAD wherever it answers GET, which Memento's media
	// routes rely on to describe a stream without opening it.
	if r.Method != http.MethodGet && r.Method != http.MethodHead && (r.Method != http.MethodPost || r.URL.Path != "/api/search/metadata") {
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
			minor = 3
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"major": 3, "minor": minor, "patch": 0, "prerelease": nil}); err != nil {
			return
		}
	case r.URL.Path == "/api/users/me":
		_, _ = w.Write([]byte(`{"id":"fixture-owner"}`))
	case r.URL.Path == "/api/albums":
		if err := json.NewEncoder(w).Encode(library.albums); err != nil {
			return
		}
	case strings.HasPrefix(r.URL.Path, "/api/albums/"):
		id := strings.TrimPrefix(r.URL.Path, "/api/albums/")
		for _, album := range library.albums {
			if album.ID == id {
				if err := json.NewEncoder(w).Encode(album); err != nil {
					return
				}
				return
			}
		}
		http.NotFound(w, r)
	case r.URL.Path == "/api/search/metadata" && r.Method == http.MethodPost:
		library.searchMembers(w, r)
	case r.URL.Path == "/api/faces":
		faces := library.faces[r.URL.Query().Get("id")]
		if faces == nil {
			faces = []sourceFace{}
		}
		if err := json.NewEncoder(w).Encode(faces); err != nil {
			return
		}
	case strings.HasPrefix(r.URL.Path, "/api/people/") && strings.HasSuffix(r.URL.Path, "/thumbnail"):
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/people/"), "/thumbnail")
		thumbnail, ok := f.personThumbnails[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", thumbnail.ContentType)
		_, _ = w.Write(thumbnail.Thumbnail)
	case strings.HasPrefix(r.URL.Path, "/api/assets/"):
		path := strings.TrimPrefix(r.URL.Path, "/api/assets/")
		id, thumbnail := strings.CutSuffix(path, "/thumbnail")
		id, original := strings.CutSuffix(id, "/original")
		id, playback := strings.CutSuffix(id, "/video/playback")
		asset, ok := library.assets[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if original || playback {
			if id == "workbench-video-broken" {
				http.Error(w, "fixture original is permanently unreadable", http.StatusInternalServerError)
				return
			}
			if id == "workbench-video-retry" && !f.reachCheckpoint(w, r, "chapter-probe") {
				return
			}
			if playback && asset.Kind != "VIDEO" {
				http.NotFound(w, r)
				return
			}
			// Immich serves uploaded files and playback with byte ranges. The
			// original arrives as an attachment; playback is the same bytes here.
			content, contentType := asset.Original, asset.OriginalType
			if content == nil {
				content, contentType = asset.Thumbnail, asset.ContentType
			}
			w.Header().Set("Content-Type", contentType)
			if original {
				w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(asset.Filename))
			}
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(content))
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

func (f sourceLibrary) searchMembers(w http.ResponseWriter, r *http.Request) {
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
			// Immich's metadata search omits trashed assets; they still answer
			// on their own asset route.
			if asset := f.assets[id]; !asset.Trashed {
				members = append(members, asset)
			}
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

// smtpControl exposes accepted messages and switches how the next
// end-of-data reply behaves. Changing the mode also releases held sessions.
func (f *immichFixture) smtpControl(w http.ResponseWriter, r *http.Request) {
	if f.smtp == nil {
		http.Error(w, "SMTP is not configured for this fixture", http.StatusNotFound)
		return
	}
	if r.Method == http.MethodGet {
		if err := json.NewEncoder(w).Encode(f.smtp.State()); err != nil {
			return
		}
		return
	}
	if !controlRequest(w, r) {
		return
	}
	var input struct {
		Mode smtptest.Mode `json:"mode"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || (input.Mode != smtptest.ModeAccept && input.Mode != smtptest.ModeTransient && input.Mode != smtptest.ModePermanent && input.Mode != smtptest.ModeHold) {
		http.Error(w, "mode must be accept, transient, permanent, or hold", http.StatusBadRequest)
		return
	}
	f.smtp.SetMode(input.Mode)
	if err := json.NewEncoder(w).Encode(f.smtp.State()); err != nil {
		return
	}
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
