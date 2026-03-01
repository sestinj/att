package tmux

import (
	"path/filepath"
	"testing"
)

func TestPriorityStoreDefaultValue(t *testing.T) {
	store := &PriorityStore{
		entries: make(map[string]int),
	}
	if got := store.Get("nonexistent.jsonl"); got != DefaultPriority {
		t.Errorf("expected default priority %d, got %d", DefaultPriority, got)
	}
}

func TestPriorityStorePersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "priority.json")

	// Create and save
	store := LoadPriority(path)
	store.Set("/tmp/session.jsonl", 0)
	store.Set("/tmp/session2.jsonl", 2)

	// Reload and verify
	store2 := LoadPriority(path)
	if got := store2.Get("/tmp/session.jsonl"); got != 0 {
		t.Errorf("expected priority 0 after reload, got %d", got)
	}
	if got := store2.Get("/tmp/session2.jsonl"); got != 2 {
		t.Errorf("expected priority 2 after reload, got %d", got)
	}
}

func TestPriorityStoreRemoveResetsToDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "priority.json")

	store := LoadPriority(path)
	store.Set("/tmp/session.jsonl", 1)

	if got := store.Get("/tmp/session.jsonl"); got != 1 {
		t.Errorf("expected priority 1, got %d", got)
	}

	store.Remove("/tmp/session.jsonl")

	if got := store.Get("/tmp/session.jsonl"); got != DefaultPriority {
		t.Errorf("expected default priority %d after remove, got %d", DefaultPriority, got)
	}

	// Reload and verify removal persisted
	store2 := LoadPriority(path)
	if got := store2.Get("/tmp/session.jsonl"); got != DefaultPriority {
		t.Errorf("expected default priority %d after reload, got %d", DefaultPriority, got)
	}
}

func TestPriorityStoreSetDefaultRemovesEntry(t *testing.T) {
	store := &PriorityStore{
		entries: make(map[string]int),
		path:    filepath.Join(t.TempDir(), "priority.json"),
	}

	store.Set("/tmp/session.jsonl", 0)
	if got := store.Get("/tmp/session.jsonl"); got != 0 {
		t.Errorf("expected priority 0, got %d", got)
	}

	// Setting to default should remove the entry
	store.Set("/tmp/session.jsonl", DefaultPriority)
	if got := store.Get("/tmp/session.jsonl"); got != DefaultPriority {
		t.Errorf("expected default priority after setting to default, got %d", got)
	}
}
