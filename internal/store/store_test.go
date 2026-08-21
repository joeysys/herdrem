package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreSetGetDelete(t *testing.T) {
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "todo.json"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := s.Get("missing"); ok {
		t.Error("Get missing unexpectedly found a todo")
	}
	todo := Todo{ID: "k1", Body: "v1", Binding: &TodoBinding{Host: "local", WorkspaceID: "ws1"}}
	if err := s.Set(todo); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, ok := s.Get("k1"); !ok || got.Body != "v1" || got.Binding.WorkspaceID != "ws1" {
		t.Errorf("Get k1 = %#v, %v", got, ok)
	}
	if err := s.Delete("k1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := s.Get("k1"); ok {
		t.Error("Get after delete unexpectedly found a todo")
	}
}

func TestStoreRestartPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "todo.json")

	s1, err := New(path)
	if err != nil {
		t.Fatalf("New s1: %v", err)
	}
	if err := s1.Set(Todo{ID: "persisted", Body: "data"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Re-open store from same file
	s2, err := New(path)
	if err != nil {
		t.Fatalf("New s2: %v", err)
	}
	if got, ok := s2.Get("persisted"); !ok || got.Body != "data" {
		t.Errorf("after restart Get = %#v, %v", got, ok)
	}
}

func TestStoreAtomicWriteNoCorruption(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "todo.json")

	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Set(Todo{ID: "k", Body: "v"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Verify no temp files left behind
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "todo.json" {
			t.Errorf("unexpected file left behind: %s", e.Name())
		}
	}
}
