package exec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcnkl/rpm/config"
	"github.com/vcnkl/rpm/models"
)

func TestLoadDotenv(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		expected    map[string]string
		expectError bool
	}{
		{
			name:    "simple key-value pairs",
			content: "FOO=bar\nBAZ=qux",
			expected: map[string]string{
				"FOO": "bar",
				"BAZ": "qux",
			},
			expectError: false,
		},
		{
			name:    "with comments and empty lines",
			content: "# This is a comment\nFOO=bar\n\n# Another comment\nBAZ=qux",
			expected: map[string]string{
				"FOO": "bar",
				"BAZ": "qux",
			},
			expectError: false,
		},
		{
			name:    "double quoted values",
			content: `FOO="bar baz"`,
			expected: map[string]string{
				"FOO": "bar baz",
			},
			expectError: false,
		},
		{
			name:    "single quoted values",
			content: `FOO='bar baz'`,
			expected: map[string]string{
				"FOO": "bar baz",
			},
			expectError: false,
		},
		{
			name:    "values with equals sign",
			content: "DATABASE_URL=postgres://user:pass@host:5432/db?sslmode=disable",
			expected: map[string]string{
				"DATABASE_URL": "postgres://user:pass@host:5432/db?sslmode=disable",
			},
			expectError: false,
		},
		{
			name:    "whitespace handling",
			content: "  FOO  =  bar  \n  BAZ=qux",
			expected: map[string]string{
				"FOO": "bar",
				"BAZ": "qux",
			},
			expectError: false,
		},
		{
			name:        "empty file",
			content:     "",
			expected:    map[string]string{},
			expectError: false,
		},
		{
			name:    "line without equals ignored",
			content: "FOO=bar\nINVALID_LINE\nBAZ=qux",
			expected: map[string]string{
				"FOO": "bar",
				"BAZ": "qux",
			},
			expectError: false,
		},
		{
			name:    "empty value",
			content: "EMPTY=",
			expected: map[string]string{
				"EMPTY": "",
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			envPath := filepath.Join(tmpDir, ".env")
			err := os.WriteFile(envPath, []byte(tt.content), 0644)
			require.NoError(t, err)

			result, err := LoadDotenv(envPath)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestLoadDotenv_FileNotFound(t *testing.T) {
	_, err := LoadDotenv("/nonexistent/path/.env")
	require.Error(t, err)
}

func TestMergeEnv(t *testing.T) {
	tests := []struct {
		name     string
		base     []string
		override []string
		expected map[string]string
	}{
		{
			name:     "simple merge",
			base:     []string{"FOO=bar", "BAZ=qux"},
			override: []string{"NEW=value"},
			expected: map[string]string{
				"FOO": "bar",
				"BAZ": "qux",
				"NEW": "value",
			},
		},
		{
			name:     "override existing",
			base:     []string{"FOO=bar", "BAZ=qux"},
			override: []string{"FOO=overridden"},
			expected: map[string]string{
				"FOO": "overridden",
				"BAZ": "qux",
			},
		},
		{
			name:     "empty base",
			base:     []string{},
			override: []string{"FOO=bar"},
			expected: map[string]string{
				"FOO": "bar",
			},
		},
		{
			name:     "empty override",
			base:     []string{"FOO=bar"},
			override: []string{},
			expected: map[string]string{
				"FOO": "bar",
			},
		},
		{
			name:     "both empty",
			base:     []string{},
			override: []string{},
			expected: map[string]string{},
		},
		{
			name:     "values with equals",
			base:     []string{"URL=http://host?a=1"},
			override: []string{"URL=http://host?a=2&b=3"},
			expected: map[string]string{
				"URL": "http://host?a=2&b=3",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MergeEnv(tt.base, tt.override)

			resultMap := make(map[string]string)
			for _, e := range result {
				idx := indexOf(e, "=")
				if idx != -1 {
					resultMap[e[:idx]] = e[idx+1:]
				}
			}

			assert.Equal(t, tt.expected, resultMap)
		})
	}
}

func indexOf(s string, sep string) int {
	for i := 0; i < len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return i
		}
	}
	return -1
}

func TestComposeEnv(t *testing.T) {
	tests := []struct {
		name           string
		repoRoot       string
		repo           *config.RepoConfig
		bundle         *models.Bundle
		target         *models.Target
		dotenvContent  string
		expectedEnvs   map[string]string
		checkDotenvVar bool
	}{
		{
			name:     "basic env composition",
			repoRoot: "/repo",
			repo: &config.RepoConfig{
				Env: map[string]string{"REPO_VAR": "repo_value"},
			},
			bundle: &models.Bundle{
				Name: "core",
				Path: "internal/core",
				Env:  map[string]string{"BUNDLE_VAR": "bundle_value"},
			},
			target: &models.Target{
				Name:       "app_build",
				BundleName: "core",
				BundlePath: "internal/core",
				Env:        map[string]string{"TARGET_VAR": "target_value"},
				Config:     models.TargetConfig{Dotenv: models.DotenvConfig{Enabled: false}},
			},
			expectedEnvs: map[string]string{
				"REPO_ROOT":   "/repo",
				"BUNDLE_ROOT": "/repo/internal/core",
				"REPO_VAR":    "repo_value",
				"BUNDLE_VAR":  "bundle_value",
				"TARGET_VAR":  "target_value",
			},
		},
		{
			name:     "dotenv enabled and loaded",
			repoRoot: "/repo",
			repo:     &config.RepoConfig{},
			bundle: &models.Bundle{
				Name: "core",
				Path: "internal/core",
			},
			target: &models.Target{
				Name:       "app_build",
				BundleName: "core",
				BundlePath: "internal/core",
				Config:     models.TargetConfig{Dotenv: models.DotenvConfig{Enabled: true}},
			},
			dotenvContent:  "DOTENV_VAR=dotenv_value",
			checkDotenvVar: true,
			expectedEnvs: map[string]string{
				"REPO_ROOT":   "/repo",
				"BUNDLE_ROOT": "/repo/internal/core",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoRoot := tt.repoRoot
			if tt.dotenvContent != "" {
				tmpDir := t.TempDir()
				repoRoot = tmpDir
				bundlePath := filepath.Join(tmpDir, tt.bundle.Path)
				require.NoError(t, os.MkdirAll(bundlePath, 0755))
				require.NoError(t, os.WriteFile(filepath.Join(bundlePath, ".env"), []byte(tt.dotenvContent), 0644))

				tt.expectedEnvs["REPO_ROOT"] = tmpDir
				tt.expectedEnvs["BUNDLE_ROOT"] = bundlePath
			}

			result := ComposeEnv(repoRoot, tt.repo, tt.bundle, tt.target)

			resultMap := make(map[string]string)
			for _, e := range result {
				idx := indexOf(e, "=")
				if idx != -1 {
					resultMap[e[:idx]] = e[idx+1:]
				}
			}

			for key, expectedVal := range tt.expectedEnvs {
				assert.Equal(t, expectedVal, resultMap[key], "env var %s mismatch", key)
			}

			if tt.checkDotenvVar {
				assert.Equal(t, "dotenv_value", resultMap["DOTENV_VAR"])
			}
		})
	}
}
