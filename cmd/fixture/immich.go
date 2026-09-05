package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
)

const fixtureAPIKey = "fixture-only-key"

type fixtureState struct {
	Available    bool `json:"available"`
	Unauthorized bool `json:"unauthorized"`
}

type immichFixture struct {
	mu      sync.RWMutex
	state   fixtureState
	restart func(context.Context) error
}

func newImmichFixture(offline bool) *immichFixture {
	return &immichFixture{state: fixtureState{Available: !offline}}
}

// ServeHTTP implements only the read-only Immich calls needed for setup.
// Control routes exist in this development command, never in the application.
func (f *immichFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/__fixture/state":
		if r.Method == http.MethodGet {
			f.mu.RLock()
			defer f.mu.RUnlock()
			_ = json.NewEncoder(w).Encode(f.state)
			return
		}
		if !controlRequest(w, r) {
			return
		}
		var state struct {
			Available    *bool `json:"available"`
			Unauthorized bool  `json:"unauthorized"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&state); err != nil || state.Available == nil {
			http.Error(w, "available boolean is required", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.state = fixtureState{Available: *state.Available, Unauthorized: state.Unauthorized}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(fixtureState{Available: *state.Available, Unauthorized: state.Unauthorized})
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
	if r.Method != http.MethodGet {
		http.Error(w, "fixture supports read-only Immich requests", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path != "/api/server/version" && r.URL.Path != "/api/users/me" {
		http.NotFound(w, r)
		return
	}
	f.mu.RLock()
	state := f.state
	f.mu.RUnlock()
	if !state.Available {
		http.Error(w, "Immich fixture is offline", http.StatusServiceUnavailable)
		return
	}
	if r.URL.Path == "/api/users/me" && (state.Unauthorized || r.Header.Get("x-api-key") != fixtureAPIKey) {
		http.Error(w, "Invalid API key", http.StatusUnauthorized)
		return
	}
	if r.URL.Path == "/api/server/version" {
		_, _ = w.Write([]byte(`{"major":2,"minor":7,"patch":0}`))
	} else {
		_, _ = w.Write([]byte(`{"id":"fixture-owner"}`))
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
