package hashing

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashFile(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		expectError bool
	}{
		{
			name:        "hash simple file",
			content:     "hello world",
			expectError: false,
		},
		{
			name:        "hash empty file",
			content:     "",
			expectError: false,
		},
		{
			name:        "hash binary content",
			content:     "\x00\x01\x02\x03",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			filePath := filepath.Join(tmpDir, "test.txt")
			err := os.WriteFile(filePath, []byte(tt.content), 0644)
			require.NoError(t, err)

			hash, err := HashFile(filePath)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.NotEmpty(t, hash)
				assert.Len(t, hash, 64)

				hash2, err := HashFile(filePath)
				require.NoError(t, err)
				assert.Equal(t, hash, hash2)
			}
		})
	}
}

func TestHashFile_NonExistent(t *testing.T) {
	_, err := HashFile("/nonexistent/path/file.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to open file")
}

func TestHashFile_Deterministic(t *testing.T) {
	tmpDir := t.TempDir()
	content := "deterministic content"

	file1 := filepath.Join(tmpDir, "file1.txt")
	file2 := filepath.Join(tmpDir, "file2.txt")

	require.NoError(t, os.WriteFile(file1, []byte(content), 0644))
	require.NoError(t, os.WriteFile(file2, []byte(content), 0644))

	hash1, err := HashFile(file1)
	require.NoError(t, err)

	hash2, err := HashFile(file2)
	require.NoError(t, err)

	assert.Equal(t, hash1, hash2)
}

func TestHashFile_DifferentContent(t *testing.T) {
	tmpDir := t.TempDir()

	file1 := filepath.Join(tmpDir, "file1.txt")
	file2 := filepath.Join(tmpDir, "file2.txt")

	require.NoError(t, os.WriteFile(file1, []byte("content1"), 0644))
	require.NoError(t, os.WriteFile(file2, []byte("content2"), 0644))

	hash1, err := HashFile(file1)
	require.NoError(t, err)

	hash2, err := HashFile(file2)
	require.NoError(t, err)

	assert.NotEqual(t, hash1, hash2)
}

func TestStartsWithRepoRoot(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		expected bool
	}{
		{
			name:     "starts with //",
			pattern:  "//bin/output",
			expected: true,
		},
		{
			name:     "does not start with //",
			pattern:  "src/main.go",
			expected: false,
		},
		{
			name:     "single slash",
			pattern:  "/absolute/path",
			expected: false,
		},
		{
			name:     "empty string",
			pattern:  "",
			expected: false,
		},
		{
			name:     "just //",
			pattern:  "//",
			expected: true,
		},
		{
			name:     "single char",
			pattern:  "/",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := startsWithRepoRoot(tt.pattern)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHashInputs(t *testing.T) {
	tests := []struct {
		name        string
		setupFiles  map[string]string
		patterns    []string
		expectError bool
		expectHash  bool
	}{
		{
			name: "single file pattern",
			setupFiles: map[string]string{
				"src/main.go": "package main",
			},
			patterns:    []string{"src/main.go"},
			expectError: false,
			expectHash:  true,
		},
		{
			name: "glob pattern",
			setupFiles: map[string]string{
				"src/main.go": "package main",
				"src/util.go": "package main",
			},
			patterns:    []string{"src/*.go"},
			expectError: false,
			expectHash:  true,
		},
		{
			name: "multiple patterns",
			setupFiles: map[string]string{
				"src/main.go":   "package main",
				"config/app.go": "package config",
			},
			patterns:    []string{"src/*.go", "config/*.go"},
			expectError: false,
			expectHash:  true,
		},
		{
			name:        "empty patterns",
			setupFiles:  map[string]string{},
			patterns:    []string{},
			expectError: false,
			expectHash:  true,
		},
		{
			name: "no matching files",
			setupFiles: map[string]string{
				"src/main.go": "package main",
			},
			patterns:    []string{"nonexistent/*.go"},
			expectError: false,
			expectHash:  true,
		},
		{
			name: "double star glob",
			setupFiles: map[string]string{
				"src/pkg/main.go":  "package pkg",
				"src/util/util.go": "package util",
			},
			patterns:    []string{"src/**/*.go"},
			expectError: false,
			expectHash:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			for path, content := range tt.setupFiles {
				fullPath := filepath.Join(tmpDir, path)
				require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0755))
				require.NoError(t, os.WriteFile(fullPath, []byte(content), 0644))
			}

			hash, err := HashInputs(tmpDir, tt.patterns)

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				if tt.expectHash {
					assert.True(t, len(hash) > 0)
					assert.Contains(t, hash, "sha256:")
				}
			}
		})
	}
}

func TestHashInputs_Deterministic(t *testing.T) {
	tmpDir := t.TempDir()

	files := map[string]string{
		"src/main.go": "package main",
		"src/util.go": "package util",
	}

	for path, content := range files {
		fullPath := filepath.Join(tmpDir, path)
		require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0755))
		require.NoError(t, os.WriteFile(fullPath, []byte(content), 0644))
	}

	hash1, err := HashInputs(tmpDir, []string{"src/*.go"})
	require.NoError(t, err)

	hash2, err := HashInputs(tmpDir, []string{"src/*.go"})
	require.NoError(t, err)

	assert.Equal(t, hash1, hash2)
}

func TestHashInputs_ChangesOnFileModification(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "src/main.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(filePath), 0755))
	require.NoError(t, os.WriteFile(filePath, []byte("version1"), 0644))

	hash1, err := HashInputs(tmpDir, []string{"src/main.go"})
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filePath, []byte("version2"), 0644))

	hash2, err := HashInputs(tmpDir, []string{"src/main.go"})
	require.NoError(t, err)

	assert.NotEqual(t, hash1, hash2)
}

func TestExpandGlob(t *testing.T) {
	tests := []struct {
		name          string
		setupFiles    []string
		pattern       string
		expectedCount int
	}{
		{
			name:          "simple glob",
			setupFiles:    []string{"a.go", "b.go", "c.txt"},
			pattern:       "*.go",
			expectedCount: 2,
		},
		{
			name:          "no double star",
			setupFiles:    []string{"a.go"},
			pattern:       "*.go",
			expectedCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			for _, file := range tt.setupFiles {
				fullPath := filepath.Join(tmpDir, file)
				require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0755))
				require.NoError(t, os.WriteFile(fullPath, []byte("content"), 0644))
			}

			pattern := filepath.Join(tmpDir, tt.pattern)
			matches, err := expandGlob(pattern)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedCount, len(matches))
		})
	}
}

func TestExpandDoubleStarGlob(t *testing.T) {
	tests := []struct {
		name          string
		setupFiles    []string
		pattern       string
		expectedCount int
	}{
		{
			name:          "match all go files recursively",
			setupFiles:    []string{"a.go", "pkg/b.go", "pkg/sub/c.go"},
			pattern:       "**/*.go",
			expectedCount: 3,
		},
		{
			name:          "match all files",
			setupFiles:    []string{"a.go", "b.txt", "pkg/c.go"},
			pattern:       "**",
			expectedCount: 3,
		},
		{
			name:          "no matches",
			setupFiles:    []string{"a.go", "b.go"},
			pattern:       "**/*.txt",
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			for _, file := range tt.setupFiles {
				fullPath := filepath.Join(tmpDir, file)
				require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0755))
				require.NoError(t, os.WriteFile(fullPath, []byte("content"), 0644))
			}

			pattern := filepath.Join(tmpDir, tt.pattern)
			matches, err := expandDoubleStarGlob(pattern)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedCount, len(matches))
		})
	}
}
