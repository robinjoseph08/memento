package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

type checkpoint struct {
	Mode     string `json:"mode"`
	Hits     int    `json:"hits"`
	Waiting  int    `json:"waiting"`
	released chan struct{}
}

// checkpointControl changes only this fixture's upstream HTTP responses. Opening
// a checkpoint releases all current requests; canceled requests never leak waiters.
func (f *immichFixture) checkpointControl(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/__fixture/checkpoints" && r.Method == http.MethodGet {
		f.mu.RLock()
		defer f.mu.RUnlock()
		if err := json.NewEncoder(w).Encode(f.checkpoints); err != nil {
			return
		}
		return
	}
	if !controlRequest(w, r) {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/__fixture/checkpoints/")
	var input struct {
		Mode string `json:"mode"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || (input.Mode != "open" && input.Mode != "pause" && input.Mode != "fail") {
		http.Error(w, "mode must be open, pause, or fail", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	point, ok := f.checkpoints[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if point.Mode != input.Mode {
		close(point.released)
		point.released = make(chan struct{})
		point.Mode = input.Mode
	}
	if err := json.NewEncoder(w).Encode(point); err != nil {
		return
	}
}

func (f *immichFixture) reachCheckpoint(w http.ResponseWriter, r *http.Request, name string) bool {
	f.mu.Lock()
	point := f.checkpoints[name]
	point.Hits++
	for point.Mode == "pause" {
		released := point.released
		point.Waiting++
		f.mu.Unlock()
		select {
		case <-r.Context().Done():
			f.mu.Lock()
			point.Waiting--
			f.mu.Unlock()
			return false
		case <-released:
		}
		f.mu.Lock()
		point.Waiting--
	}
	failed := point.Mode == "fail"
	f.mu.Unlock()
	if failed {
		http.Error(w, "controlled "+name+" failure", http.StatusServiceUnavailable)
	}
	return !failed
}
