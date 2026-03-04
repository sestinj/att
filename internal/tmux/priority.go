package tmux

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// DefaultPriority is the default priority level for new windows.
const DefaultPriority = 2

// MaxPriority is the highest priority number (lowest urgency).
const MaxPriority = 4

// PriorityStore manages priority levels (P0-P4) for windows. Lower numbers
// are higher priority. Only non-default entries are persisted.
// Keyed by tmux window_id.
type PriorityStore struct {
	mu      sync.Mutex
	entries map[string]int // window_id → priority 0-4
	path    string
}

// LoadPriority reads priority.json from path and returns a PriorityStore.
// Returns an empty store on any error.
func LoadPriority(path string) *PriorityStore {
	s := &PriorityStore{
		entries: make(map[string]int),
		path:    path,
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var raw map[string]int
	if err := json.Unmarshal(data, &raw); err != nil {
		return s
	}
	for k, v := range raw {
		if v >= 0 && v <= MaxPriority && v != DefaultPriority {
			s.entries[k] = v
		}
	}
	return s
}

// Get returns the priority for a window. Returns DefaultPriority if not set.
func (s *PriorityStore) Get(windowID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.entries[windowID]; ok {
		return p
	}
	return DefaultPriority
}

// Set sets the priority for a window and persists to disk.
// Setting to DefaultPriority removes the entry.
func (s *PriorityStore) Set(windowID string, priority int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if priority == DefaultPriority {
		delete(s.entries, windowID)
	} else {
		s.entries[windowID] = priority
	}
	s.saveLocked()
}

// Remove removes the priority entry for a window and persists to disk.
func (s *PriorityStore) Remove(windowID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, windowID)
	s.saveLocked()
}

// saveLocked writes to disk. Caller must hold s.mu.
func (s *PriorityStore) saveLocked() {
	data, err := json.MarshalIndent(s.entries, "", "  ")
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
		log.Printf("att: priority save rename failed: %v", err)
	}
}
