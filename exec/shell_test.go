package exec

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vcnkl/rpm/models"
)

func TestResolveWorkDir(t *testing.T) {
	tests := []struct {
		name     string
		repoRoot string
		target   *models.Target
		expected string
	}{
		{
			name:     "empty working dir defaults to bundle path",
			repoRoot: "/repo",
			target: &models.Target{
				BundlePath: "internal/core",
				Config:     models.TargetConfig{WorkingDir: ""},
			},
			expected: "/repo/internal/core",
		},
		{
			name:     "local working dir uses bundle path",
			repoRoot: "/repo",
			target: &models.Target{
				BundlePath: "internal/api",
				Config:     models.TargetConfig{WorkingDir: "local"},
			},
			expected: "/repo/internal/api",
		},
		{
			name:     "repo_root working dir",
			repoRoot: "/repo",
			target: &models.Target{
				BundlePath: "internal/core",
				Config:     models.TargetConfig{WorkingDir: "repo_root"},
			},
			expected: "/repo",
		},
		{
			name:     "absolute path",
			repoRoot: "/repo",
			target: &models.Target{
				BundlePath: "internal/core",
				Config:     models.TargetConfig{WorkingDir: "/absolute/path"},
			},
			expected: "/absolute/path",
		},
		{
			name:     "relative path within bundle",
			repoRoot: "/repo",
			target: &models.Target{
				BundlePath: "internal/core",
				Config:     models.TargetConfig{WorkingDir: "subdir"},
			},
			expected: "/repo/internal/core/subdir",
		},
		{
			name:     "nested relative path",
			repoRoot: "/repo",
			target: &models.Target{
				BundlePath: "internal/core",
				Config:     models.TargetConfig{WorkingDir: "sub/nested"},
			},
			expected: "/repo/internal/core/sub/nested",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ResolveWorkDir(tt.repoRoot, tt.target)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple string",
			input:    "hello world",
			expected: "'hello world'",
		},
		{
			name:     "string with single quote",
			input:    "it's working",
			expected: "'it'\"'\"'s working'",
		},
		{
			name:     "string with multiple single quotes",
			input:    "it's Bob's code",
			expected: "'it'\"'\"'s Bob'\"'\"'s code'",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "''",
		},
		{
			name:     "string with special chars",
			input:    "echo $HOME && rm -rf /",
			expected: "'echo $HOME && rm -rf /'",
		},
		{
			name:     "string with newlines",
			input:    "line1\nline2",
			expected: "'line1\nline2'",
		},
		{
			name:     "string with double quotes",
			input:    `say "hello"`,
			expected: `'say "hello"'`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shellQuote(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDependencyFailedError(t *testing.T) {
	tests := []struct {
		name     string
		targetID string
		expected string
	}{
		{
			name:     "simple target ID",
			targetID: "core:app_build",
			expected: "dependency failed for target: core:app_build",
		},
		{
			name:     "empty target ID",
			targetID: "",
			expected: "dependency failed for target: ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &DependencyFailedError{TargetID: tt.targetID}
			assert.Equal(t, tt.expected, err.Error())
		})
	}
}
