package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// TodoBinding identifies the workspace a todo belongs to.
type TodoBinding struct {
	Host           string `json:"host"`
	WorkspaceID    string `json:"workspace_id"`
	WorkspaceLabel string `json:"workspace_label,omitempty"`
}

// Todo is a persisted workspace note.
type Todo struct {
	ID        string       `json:"id"`
	Body      string       `json:"body"`
	Binding   *TodoBinding `json:"binding,omitempty"`
	CreatedAt string       `json:"created_at"`
	UpdatedAt string       `json:"updated_at"`
}

// Store is a JSON file-backed todo store.
type Store struct {
	mu       sync.RWMutex
	filePath string
	data     map[string]Todo
}

// New creates or loads a Store from the given file path.
func New(filePath string) (*Store, error) {
	s := &Store{
		filePath: filePath,
		data:     make(map[string]Todo),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	b, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // fresh store
		}
		return err
	}
	return json.Unmarshal(b, &s.data)
}

func (s *Store) save() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Atomic write: temp file in same dir, then rename
	tmp, err := os.CreateTemp(dir, ".tmp-todo-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op if rename succeeded
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.filePath)
}

// Get returns a todo by ID.
func (s *Store) Get(id string) (Todo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	todo, ok := s.data[id]
	return todo, ok
}

// All returns a copy of all todos.
func (s *Store) All() []Todo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Todo, 0, len(s.data))
	for _, todo := range s.data {
		out = append(out, todo)
	}
	return out
}

// Set creates or replaces a todo.
func (s *Store) Set(todo Todo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[todo.ID] = todo
	return s.save()
}

// Delete removes a todo.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, id)
	return s.save()
}
