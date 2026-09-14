package main

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"time"
)

// libraryPatch edits the fake library the way a photographer edits Immich:
// album membership and description, asset facts, and outright deletion.
// Members replaces the album's membership; every listed ID must exist.
type libraryPatch struct {
	Album       string                `json:"album"`
	Members     *[]string             `json:"members"`
	Description *string               `json:"description"`
	Assets      map[string]assetPatch `json:"assets"`
	Delete      []string              `json:"delete"`
}

// assetPatch changes the source facts Memento synchronizes. Any change bumps
// updatedAt to now unless the patch sets it, as Immich does.
type assetPatch struct {
	Checksum      *string `json:"checksum"`
	Filename      *string `json:"originalFileName"`
	LocalDateTime *string `json:"localDateTime"`
	FileCreatedAt *string `json:"fileCreatedAt"`
	UpdatedAt     *string `json:"updatedAt"`
	Trashed       *bool   `json:"isTrashed"`
	Offline       *bool   `json:"isOffline"`
}

// libraryControl applies one patch and answers with the album as Immich
// would list it. Deleted assets answer 404 everywhere afterwards.
func (f *immichFixture) libraryControl(w http.ResponseWriter, r *http.Request) {
	if !controlRequest(w, r) {
		return
	}
	var patch libraryPatch
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&patch); err != nil || patch.Album == "" {
		http.Error(w, "album is required", http.StatusBadRequest)
		return
	}
	// Hold the write lock for the whole edit so two control posts cannot
	// lose each other's changes; readers keep the snapshot they took.
	f.mu.Lock()
	defer f.mu.Unlock()
	current := f.library
	index := slices.IndexFunc(current.albums, func(album sourceAlbum) bool { return album.ID == patch.Album })
	if index < 0 {
		http.NotFound(w, r)
		return
	}
	for id := range patch.Assets {
		if _, ok := current.assets[id]; !ok {
			http.Error(w, "unknown asset "+id, http.StatusBadRequest)
			return
		}
	}
	if patch.Members != nil {
		for _, id := range *patch.Members {
			if _, ok := current.assets[id]; !ok {
				http.Error(w, "unknown asset "+id, http.StatusBadRequest)
				return
			}
		}
	}
	// Edit a copy, then publish it whole, so in-flight upstream requests keep
	// reading a consistent library.
	next := sourceLibrary{albums: slices.Clone(current.albums), assets: maps.Clone(current.assets), faces: maps.Clone(current.faces)}
	for i := range next.albums {
		next.albums[i].Members = slices.Clone(next.albums[i].Members)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for id, edit := range patch.Assets {
		asset := next.assets[id]
		if edit.Checksum != nil {
			asset.Checksum = *edit.Checksum
		}
		if edit.Filename != nil {
			asset.Filename = *edit.Filename
		}
		if edit.LocalDateTime != nil {
			asset.LocalDateTime = *edit.LocalDateTime
		}
		if edit.FileCreatedAt != nil {
			asset.FileCreatedAt = *edit.FileCreatedAt
		}
		if edit.Trashed != nil {
			asset.Trashed = *edit.Trashed
		}
		if edit.Offline != nil {
			asset.Offline = *edit.Offline
		}
		asset.UpdatedAt = now
		if edit.UpdatedAt != nil {
			asset.UpdatedAt = *edit.UpdatedAt
		}
		next.assets[id] = asset
	}
	for _, id := range patch.Delete {
		delete(next.assets, id)
		delete(next.faces, id)
	}
	album := &next.albums[index]
	if patch.Members != nil {
		album.Members = slices.Clone(*patch.Members)
	}
	if patch.Description != nil {
		album.Description = *patch.Description
	}
	album.UpdatedAt = now
	// Deleted assets leave every album's membership, as they do in Immich.
	for i := range next.albums {
		next.albums[i].Members = slices.DeleteFunc(next.albums[i].Members, func(id string) bool { _, ok := next.assets[id]; return !ok })
		next.albums[i].Count = len(next.albums[i].Members)
		next.albums[i].StartDate, next.albums[i].EndDate = "", ""
		for _, id := range next.albums[i].Members {
			asset := next.assets[id]
			if asset.Trashed {
				continue
			}
			if next.albums[i].StartDate == "" || asset.FileCreatedAt < next.albums[i].StartDate {
				next.albums[i].StartDate = asset.FileCreatedAt
			}
			if next.albums[i].EndDate == "" || asset.FileCreatedAt > next.albums[i].EndDate {
				next.albums[i].EndDate = asset.FileCreatedAt
			}
		}
	}
	f.library = next
	if err := json.NewEncoder(w).Encode(album); err != nil {
		return
	}
}
