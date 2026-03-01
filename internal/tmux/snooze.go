package tmux

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SnoozeStore manages time-based snoozing of sessions. Snoozed sessions
// are excluded from the attention queue until their snooze expires.
type SnoozeStore struct {
	mu      sync.Mutex
	entries map[string]time.Time // session file path → expiry
	path    string
}

// LoadSnooze reads snooze.json from path and returns a SnoozeStore.
// Returns an empty store on any error.
func LoadSnooze(path string) *SnoozeStore {
	s := &SnoozeStore{
		entries: make(map[string]time.Time),
		path:    path,
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	// File stores string timestamps keyed by session file path
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return s
	}
	for k, v := range raw {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			continue
		}
		s.entries[k] = t
	}
	return s
}

// Save writes the snooze store to disk atomically (write temp, rename).
// Expired entries are cleaned up before saving. Safe to call without
// holding the lock.
func (s *SnoozeStore) Save() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveLocked()
}

// saveLocked writes to disk. Caller must hold s.mu.
func (s *SnoozeStore) saveLocked() {
	s.cleanup()
	raw := make(map[string]string, len(s.entries))
	for k, v := range s.entries {
		raw[k] = v.Format(time.RFC3339)
	}
	data, err := json.MarshalIndent(raw, "", "  ")
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
	os.Rename(tmp, s.path)
}

// Snooze marks a session as snoozed until the given time.
func (s *SnoozeStore) Snooze(sessionFile string, until time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[sessionFile] = until
	s.saveLocked()
}

// IsSnoozed returns true if the session is snoozed and the snooze has not expired.
func (s *SnoozeStore) IsSnoozed(sessionFile string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	expiry, ok := s.entries[sessionFile]
	if !ok {
		return false
	}
	return time.Now().Before(expiry)
}

// Unsnooze removes the snooze for a session.
func (s *SnoozeStore) Unsnooze(sessionFile string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, sessionFile)
	s.saveLocked()
}

// cleanup removes expired entries. Must be called with mu held or from Save.
func (s *SnoozeStore) cleanup() {
	now := time.Now()
	for k, v := range s.entries {
		if now.After(v) {
			delete(s.entries, k)
		}
	}
}
