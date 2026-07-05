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
	v := NewValidator("/repo", "/bin/sh", "test", store)
	assert.NotNil(t, v)
	assert.Equal(t, "/repo", v.repoRoot)
	assert.Equal(t, store, v.store)
}

func TestValidator_ResolveOutputPath(t *testing.T) {
	v := NewValidator("/repo", "/bin/sh", "test", NewStore(""))

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
			name:       "docker-like output resolves as ordinary relative path",
			out:        "@dock" + "er::myimage:latest",
			bundlePath: "internal/core",
			expected:   "/repo/internal/core/@dock" + "er::myimage:latest",
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
			name:       "docker-like output is missing filesystem output",
			setupFiles: []string{},
			outputs:    []string{"@dock" + "er::myimage:latest"},
			expected:   false,
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

			v := NewValidator(tmpDir, "/bin/sh", "test", NewStore(""))
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
				v := NewValidator(tmpDir, "/bin/sh", "test", store)
				shouldBuild, hash, _ := v.ShouldBuild(target)
				_ = shouldBuild
				store.Set(target.ID(), &Entry{InputHash: hash})
			} else if tt.cachedHash != "" {
				store.Set(target.ID(), &Entry{InputHash: tt.cachedHash})
			}

			v := NewValidator(tmpDir, "/bin/sh", "test", store)
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

func TestValidator_ShouldBuild_ConfigSensitivity(t *testing.T) {
	baseTarget := func() *models.Target {
		return &models.Target{
			Name:       "app_build",
			BundleName: "core",
			BundlePath: "internal/core",
			In:         []string{"src/*.go"},
			Env:        map[string]string{"MODE": "release"},
			Cmd:        "go build ./...",
			Config:     models.TargetConfig{WorkingDir: "internal/core"},
		}
	}

	tests := []struct {
		name        string
		shell       string
		toolVersion string
		mutate      func(*models.Target)
	}{
		{name: "changed command", shell: "/bin/sh", toolVersion: "test", mutate: func(t *models.Target) { t.Cmd = "go build -race ./..." }},
		{name: "changed env value", shell: "/bin/sh", toolVersion: "test", mutate: func(t *models.Target) { t.Env = map[string]string{"MODE": "debug"} }},
		{name: "changed working dir", shell: "/bin/sh", toolVersion: "test", mutate: func(t *models.Target) { t.Config.WorkingDir = "internal/other" }},
		{name: "changed deps", shell: "/bin/sh", toolVersion: "test", mutate: func(t *models.Target) { t.Deps = []string{"core:lib_build"} }},
		{name: "changed shell", shell: "/bin/bash", toolVersion: "test", mutate: func(t *models.Target) {}},
		{name: "changed tool version", shell: "/bin/sh", toolVersion: "next", mutate: func(t *models.Target) {}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			srcDir := filepath.Join(tmpDir, "internal/core/src")
			require.NoError(t, os.MkdirAll(srcDir, 0755))
			require.NoError(t, os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package main"), 0644))

			store := NewStore("")
			baseline := NewValidator(tmpDir, "/bin/sh", "test", store)
			target := baseTarget()

			_, hash, err := baseline.ShouldBuild(target)
			require.NoError(t, err)
			store.Set(target.ID(), &Entry{InputHash: hash})

			shouldBuild, _, err := baseline.ShouldBuild(target)
			require.NoError(t, err)
			require.False(t, shouldBuild, "baseline target must be cached before mutation")

			mutated := baseTarget()
			tt.mutate(mutated)
			changed := NewValidator(tmpDir, tt.shell, tt.toolVersion, store)

			shouldBuild, _, err = changed.ShouldBuild(mutated)
			require.NoError(t, err)
			assert.True(t, shouldBuild, "changing build configuration must invalidate the cache")
		})
	}
}
