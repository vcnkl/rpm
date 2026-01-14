package builds

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Entry struct {
	InputHash  string    `json:"input_hash"`
	OutputHash string    `json:"output_hash,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
	DurationMs int64     `json:"duration_ms"`
}

type Store struct {
	path    string
	entries map[string]*Entry
	mu      sync.RWMutex
}

func NewStore(path string) *Store {
	return &Store{
		path:    path,
		entries: make(map[string]*Entry),
	}
}

func (s *Store) Load() error {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read builds file %s: %w", s.path, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err = json.Unmarshal(data, &s.entries); err != nil {
		return fmt.Errorf("failed to parse builds file %s: %w", s.path, err)
	}

	return nil
}

func (s *Store) Get(targetID string) (*Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.entries[targetID]
	return entry, ok
}

func (s *Store) Set(targetID string, entry *Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries[targetID] = entry
}

func (s *Store) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal builds: %w", err)
	}

	tmpPath := s.path + ".tmp"
	if err = os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write builds file %s: %w", tmpPath, err)
	}

	if err = os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("failed to rename builds file: %w", err)
	}

	return nil
}
