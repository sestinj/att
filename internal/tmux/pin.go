package tmux

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// PinStore manages a set of pinned windows. Pinned windows remain visible
// in the filtered bar regardless of their attention state. Keyed by tmux
// window_id (e.g. "@123") which is globally unique per window.
type PinStore struct {
	mu      sync.Mutex
	entries map[string]bool // window_id → pinned
	path    string
}

// LoadPin reads pin.json from path and returns a PinStore.
// Returns an empty store on any error.
func LoadPin(path string) *PinStore {
	s := &PinStore{
		entries: make(map[string]bool),
		path:    path,
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var items []string
	if err := json.Unmarshal(data, &items); err != nil {
		return s
	}
	for _, item := range items {
		s.entries[item] = true
	}
	return s
}

// IsPinned returns whether a window is pinned.
func (s *PinStore) IsPinned(windowID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entries[windowID]
}

// Toggle toggles the pin state for a window and persists to disk.
// Returns the new pin state.
func (s *PinStore) Toggle(windowID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries[windowID] {
		delete(s.entries, windowID)
		s.saveLocked()
		return false
	}
	s.entries[windowID] = true
	s.saveLocked()
	return true
}

// Remove removes the pin for a window and persists to disk.
func (s *PinStore) Remove(windowID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, windowID)
	s.saveLocked()
}

// saveLocked writes to disk. Caller must hold s.mu.
func (s *PinStore) saveLocked() {
	var items []string
	for k := range s.entries {
		items = append(items, k)
	}
	sort.Strings(items)
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		log.Printf("att: pin save rename failed: %v", err)
	}
}
