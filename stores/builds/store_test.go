package builds

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStore(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{
			name: "creates store with path",
			path: "/tmp/builds.json",
		},
		{
			name: "empty path",
			path: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(tt.path)
			assert.NotNil(t, store)
			assert.Equal(t, tt.path, store.path)
			assert.NotNil(t, store.entries)
			assert.Empty(t, store.entries)
		})
	}
}

func TestStore_Load(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		expectError bool
		expected    map[string]*Entry
	}{
		{
			name:        "load valid JSON",
			content:     `{"core:app_build":{"input_hash":"sha256:abc123","timestamp":"2024-01-01T00:00:00Z","duration_ms":1000}}`,
			expectError: false,
			expected: map[string]*Entry{
				"core:app_build": {
					InputHash:  "sha256:abc123",
					Timestamp:  time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
					DurationMs: 1000,
				},
			},
		},
		{
			name:        "load empty JSON",
			content:     `{}`,
			expectError: false,
			expected:    map[string]*Entry{},
		},
		{
			name:        "invalid JSON",
			content:     `{invalid}`,
			expectError: true,
			expected:    nil,
		},
		{
			name:        "file does not exist",
			content:     "",
			expectError: false,
			expected:    map[string]*Entry{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			path := filepath.Join(tmpDir, "builds.json")

			if tt.content != "" {
				err := os.WriteFile(path, []byte(tt.content), 0644)
				require.NoError(t, err)
			} else if tt.name != "file does not exist" {
				err := os.WriteFile(path, []byte(tt.content), 0644)
				require.NoError(t, err)
			}

			store := NewStore(path)
			err := store.Load()

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				if tt.expected != nil {
					for id, expectedEntry := range tt.expected {
						actualEntry, ok := store.entries[id]
						assert.True(t, ok, "expected entry %s not found", id)
						if ok {
							assert.Equal(t, expectedEntry.InputHash, actualEntry.InputHash)
							assert.Equal(t, expectedEntry.DurationMs, actualEntry.DurationMs)
						}
					}
				}
			}
		})
	}
}

func TestStore_GetSet(t *testing.T) {
	tests := []struct {
		name     string
		setData  map[string]*Entry
		getKey   string
		expected *Entry
		found    bool
	}{
		{
			name: "get existing entry",
			setData: map[string]*Entry{
				"core:app_build": {InputHash: "sha256:abc", DurationMs: 100},
			},
			getKey:   "core:app_build",
			expected: &Entry{InputHash: "sha256:abc", DurationMs: 100},
			found:    true,
		},
		{
			name:     "get non-existing entry",
			setData:  map[string]*Entry{},
			getKey:   "core:nonexistent",
			expected: nil,
			found:    false,
		},
		{
			name: "overwrite entry",
			setData: map[string]*Entry{
				"core:app_build": {InputHash: "sha256:new", DurationMs: 200},
			},
			getKey:   "core:app_build",
			expected: &Entry{InputHash: "sha256:new", DurationMs: 200},
			found:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore("")

			for id, entry := range tt.setData {
				store.Set(id, entry)
			}

			result, found := store.Get(tt.getKey)
			assert.Equal(t, tt.found, found)
			if tt.found {
				assert.Equal(t, tt.expected.InputHash, result.InputHash)
				assert.Equal(t, tt.expected.DurationMs, result.DurationMs)
			}
		})
	}
}

func TestStore_Save(t *testing.T) {
	tests := []struct {
		name        string
		entries     map[string]*Entry
		expectError bool
	}{
		{
			name: "save single entry",
			entries: map[string]*Entry{
				"core:app_build": {
					InputHash:  "sha256:abc123",
					Timestamp:  time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
					DurationMs: 1000,
				},
			},
			expectError: false,
		},
		{
			name:        "save empty entries",
			entries:     map[string]*Entry{},
			expectError: false,
		},
		{
			name: "save multiple entries",
			entries: map[string]*Entry{
				"core:app_build":   {InputHash: "sha256:abc", DurationMs: 100},
				"api:server_build": {InputHash: "sha256:def", DurationMs: 200},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			path := filepath.Join(tmpDir, "subdir", "builds.json")

			store := NewStore(path)
			for id, entry := range tt.entries {
				store.Set(id, entry)
			}

			err := store.Save()
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)

				loadedStore := NewStore(path)
				err = loadedStore.Load()
				require.NoError(t, err)

				for id, expectedEntry := range tt.entries {
					actualEntry, found := loadedStore.Get(id)
					assert.True(t, found)
					assert.Equal(t, expectedEntry.InputHash, actualEntry.InputHash)
					assert.Equal(t, expectedEntry.DurationMs, actualEntry.DurationMs)
				}
			}
		})
	}
}

func TestStore_Concurrency(t *testing.T) {
	store := NewStore("")
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(2)

		go func(id int) {
			defer wg.Done()
			store.Set("target:"+string(rune(id)), &Entry{InputHash: "hash"})
		}(i)

		go func(id int) {
			defer wg.Done()
			store.Get("target:" + string(rune(id)))
		}(i)
	}

	wg.Wait()
}
