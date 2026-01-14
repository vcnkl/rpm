package builds

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcnkl/rpm/models"
)

func TestNewValidator(t *testing.T) {
	store := NewStore("")
	v := NewValidator("/repo", store)
	assert.NotNil(t, v)
	assert.Equal(t, "/repo", v.repoRoot)
	assert.Equal(t, store, v.store)
}

func TestValidator_ResolveOutputPath(t *testing.T) {
	v := NewValidator("/repo", NewStore(""))

	tests := []struct {
		name       string
		out        string
		bundlePath string
		expected   string
	}{
		{
			name:       "repo root relative path",
			out:        "//bin/output",
			bundlePath: "internal/core",
			expected:   "/repo/bin/output",
		},
		{
			name:       "docker output returns empty",
			out:        "@docker::myimage:latest",
			bundlePath: "internal/core",
			expected:   "",
		},
		{
			name:       "explicit relative path",
			out:        "./dist/output",
			bundlePath: "internal/core",
			expected:   "/repo/internal/core/dist/output",
		},
		{
			name:       "implicit relative path",
			out:        "dist/output",
			bundlePath: "internal/core",
			expected:   "/repo/internal/core/dist/output",
		},
		{
			name:       "simple filename",
			out:        "output.bin",
			bundlePath: "internal/core",
			expected:   "/repo/internal/core/output.bin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := v.resolveOutputPath(tt.out, tt.bundlePath)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestValidator_OutputsExist(t *testing.T) {
	tests := []struct {
		name       string
		setupFiles []string
		outputs    []string
		expected   bool
	}{
		{
			name:       "no outputs defined",
			setupFiles: []string{},
			outputs:    []string{},
			expected:   true,
		},
		{
			name:       "single file exists",
			setupFiles: []string{"dist/output.bin"},
			outputs:    []string{"dist/output.bin"},
			expected:   true,
		},
		{
			name:       "single file missing",
			setupFiles: []string{},
			outputs:    []string{"dist/output.bin"},
			expected:   false,
		},
		{
			name:       "docker output always passes",
			setupFiles: []string{},
			outputs:    []string{"@docker::myimage:latest"},
			expected:   true,
		},
		{
			name:       "multiple files all exist",
			setupFiles: []string{"dist/a.bin", "dist/b.bin"},
			outputs:    []string{"dist/a.bin", "dist/b.bin"},
			expected:   true,
		},
		{
			name:       "multiple files one missing",
			setupFiles: []string{"dist/a.bin"},
			outputs:    []string{"dist/a.bin", "dist/b.bin"},
			expected:   false,
		},
		{
			name:       "glob pattern matches",
			setupFiles: []string{"dist/a.go", "dist/b.go"},
			outputs:    []string{"dist/*.go"},
			expected:   true,
		},
		{
			name:       "glob pattern no match",
			setupFiles: []string{},
			outputs:    []string{"dist/*.go"},
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			bundlePath := "internal/core"
			bundleRoot := filepath.Join(tmpDir, bundlePath)
			require.NoError(t, os.MkdirAll(bundleRoot, 0755))

			for _, file := range tt.setupFiles {
				fullPath := filepath.Join(bundleRoot, file)
				require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0755))
				require.NoError(t, os.WriteFile(fullPath, []byte("content"), 0644))
			}

			v := NewValidator(tmpDir, NewStore(""))
			target := &models.Target{
				BundlePath: bundlePath,
				Out:        tt.outputs,
			}

			result := v.outputsExist(target)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestValidator_ShouldBuild(t *testing.T) {
	tests := []struct {
		name            string
		setupInputs     []string
		inputPatterns   []string
		outputs         []string
		setupOutputs    []string
		cachedHash      string
		expectedBuild   bool
		expectHashError bool
	}{
		{
			name:          "no cache entry - should build",
			setupInputs:   []string{"src/main.go"},
			inputPatterns: []string{"src/*.go"},
			outputs:       []string{},
			cachedHash:    "",
			expectedBuild: true,
		},
		{
			name:          "hash matches and outputs exist - skip build",
			setupInputs:   []string{"src/main.go"},
			inputPatterns: []string{"src/*.go"},
			outputs:       []string{"dist/output"},
			setupOutputs:  []string{"dist/output"},
			cachedHash:    "MATCH",
			expectedBuild: false,
		},
		{
			name:          "hash matches but outputs missing - should build",
			setupInputs:   []string{"src/main.go"},
			inputPatterns: []string{"src/*.go"},
			outputs:       []string{"dist/output"},
			setupOutputs:  []string{},
			cachedHash:    "MATCH",
			expectedBuild: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			bundlePath := "internal/core"
			bundleRoot := filepath.Join(tmpDir, bundlePath)
			require.NoError(t, os.MkdirAll(bundleRoot, 0755))

			for _, file := range tt.setupInputs {
				fullPath := filepath.Join(bundleRoot, file)
				require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0755))
				require.NoError(t, os.WriteFile(fullPath, []byte("content"), 0644))
			}

			for _, file := range tt.setupOutputs {
				fullPath := filepath.Join(bundleRoot, file)
				require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0755))
				require.NoError(t, os.WriteFile(fullPath, []byte("output"), 0644))
			}

			store := NewStore("")
			target := &models.Target{
				Name:       "app_build",
				BundleName: "core",
				BundlePath: bundlePath,
				In:         tt.inputPatterns,
				Out:        tt.outputs,
			}

			if tt.cachedHash == "MATCH" {
				v := NewValidator(tmpDir, store)
				shouldBuild, hash, _ := v.ShouldBuild(target)
				_ = shouldBuild
				store.Set(target.ID(), &Entry{InputHash: hash})
			} else if tt.cachedHash != "" {
				store.Set(target.ID(), &Entry{InputHash: tt.cachedHash})
			}

			v := NewValidator(tmpDir, store)
			shouldBuild, _, err := v.ShouldBuild(target)

			if tt.expectHashError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedBuild, shouldBuild)
			}
		})
	}
}
