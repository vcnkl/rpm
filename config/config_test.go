package config

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vcnkl/rpm/models"
)

func TestRepoConfig_SetDefaults(t *testing.T) {
	tests := []struct {
		name     string
		initial  RepoConfig
		expected RepoConfig
	}{
		{
			name:    "all defaults",
			initial: RepoConfig{},
			expected: RepoConfig{
				Shell: "/bin/sh",
				Env:   map[string]string{},
				Docker: DockerConfig{
					Backend: "local",
				},
			},
		},
		{
			name: "preserves existing shell",
			initial: RepoConfig{
				Shell: "/bin/bash",
			},
			expected: RepoConfig{
				Shell: "/bin/bash",
				Env:   map[string]string{},
				Docker: DockerConfig{
					Backend: "local",
				},
			},
		},
		{
			name: "preserves existing docker config",
			initial: RepoConfig{
				Docker: DockerConfig{
					Backend: "remote",
					URL:     "tcp://localhost:2375",
				},
			},
			expected: RepoConfig{
				Shell: "/bin/sh",
				Env:   map[string]string{},
				Docker: DockerConfig{
					Backend: "remote",
					URL:     "tcp://localhost:2375",
				},
			},
		},
		{
			name: "preserves existing env",
			initial: RepoConfig{
				Env: map[string]string{"FOO": "bar"},
			},
			expected: RepoConfig{
				Shell: "/bin/sh",
				Env:   map[string]string{"FOO": "bar"},
				Docker: DockerConfig{
					Backend: "local",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.initial
			cfg.SetDefaults()

			assert.Equal(t, tt.expected.Shell, cfg.Shell)
			assert.Equal(t, tt.expected.Env, cfg.Env)
			assert.Equal(t, tt.expected.Docker.Backend, cfg.Docker.Backend)
			assert.Equal(t, tt.expected.Docker.URL, cfg.Docker.URL)
		})
	}
}

func TestBundleConfig_SetDefaults(t *testing.T) {
	tests := []struct {
		name         string
		initial      BundleConfig
		expectEnvNil bool
	}{
		{
			name:         "sets default env",
			initial:      BundleConfig{Name: "test"},
			expectEnvNil: false,
		},
		{
			name: "preserves existing env",
			initial: BundleConfig{
				Name: "test",
				Env:  map[string]string{"KEY": "value"},
			},
			expectEnvNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.initial
			cfg.SetDefaults()

			assert.NotNil(t, cfg.Env)
		})
	}
}

func TestTargetConfig_SetDefaults(t *testing.T) {
	tests := []struct {
		name     string
		initial  TargetConfig
		validate func(t *testing.T, cfg TargetConfig)
	}{
		{
			name:    "all defaults",
			initial: TargetConfig{Name: "app_build"},
			validate: func(t *testing.T, cfg TargetConfig) {
				assert.NotNil(t, cfg.Env)
				assert.NotNil(t, cfg.In)
				assert.NotNil(t, cfg.Out)
				assert.NotNil(t, cfg.Deps)
				assert.Equal(t, "local", cfg.Config.WorkingDir)
				assert.NotNil(t, cfg.Config.Dotenv.Enabled)
				assert.True(t, *cfg.Config.Dotenv.Enabled)
				assert.NotNil(t, cfg.Config.Reload)
				assert.True(t, *cfg.Config.Reload)
				assert.NotNil(t, cfg.Config.Ignore)
			},
		},
		{
			name: "preserves existing values",
			initial: TargetConfig{
				Name: "app_build",
				In:   []string{"src/*.go"},
				Out:  []string{"bin/app"},
				Deps: []string{":lib_build"},
				Env:  map[string]string{"GO": "1.21"},
				Config: TargetOptions{
					WorkingDir: "repo_root",
				},
			},
			validate: func(t *testing.T, cfg TargetConfig) {
				assert.Equal(t, []string{"src/*.go"}, cfg.In)
				assert.Equal(t, []string{"bin/app"}, cfg.Out)
				assert.Equal(t, []string{":lib_build"}, cfg.Deps)
				assert.Equal(t, map[string]string{"GO": "1.21"}, cfg.Env)
				assert.Equal(t, "repo_root", cfg.Config.WorkingDir)
			},
		},
		{
			name: "dotenv disabled preserves",
			initial: func() TargetConfig {
				enabled := false
				return TargetConfig{
					Name: "app_dev",
					Config: TargetOptions{
						Dotenv: DotenvConfig{Enabled: &enabled},
					},
				}
			}(),
			validate: func(t *testing.T, cfg TargetConfig) {
				assert.False(t, *cfg.Config.Dotenv.Enabled)
			},
		},
		{
			name: "reload disabled preserves",
			initial: func() TargetConfig {
				reload := false
				return TargetConfig{
					Name: "app_dev",
					Config: TargetOptions{
						Reload: &reload,
					},
				}
			}(),
			validate: func(t *testing.T, cfg TargetConfig) {
				assert.False(t, *cfg.Config.Reload)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.initial
			cfg.SetDefaults()
			tt.validate(t, cfg)
		})
	}
}

func TestTargetConfig_GetCmd(t *testing.T) {
	tests := []struct {
		name     string
		cmd      interface{}
		expected string
	}{
		{
			name:     "string command",
			cmd:      "go build ./...",
			expected: "go build ./...",
		},
		{
			name:     "slice of interface strings",
			cmd:      []interface{}{"echo hello", "echo world"},
			expected: "echo hello\necho world",
		},
		{
			name:     "slice of strings",
			cmd:      []string{"go fmt ./...", "go build ./..."},
			expected: "go fmt ./...\ngo build ./...",
		},
		{
			name:     "nil command",
			cmd:      nil,
			expected: "",
		},
		{
			name:     "empty string",
			cmd:      "",
			expected: "",
		},
		{
			name:     "int command returns empty",
			cmd:      42,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := TargetConfig{Cmd: tt.cmd}
			result := cfg.GetCmd()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConfig_ResolveTarget(t *testing.T) {
	tests := []struct {
		name        string
		bundles     map[string]*models.Bundle
		ref         string
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid target reference",
			bundles: map[string]*models.Bundle{
				"core": {
					Name: "core",
					Targets: []*models.Target{
						{Name: "app_build", BundleName: "core"},
					},
				},
			},
			ref:         "core:app_build",
			expectError: false,
		},
		{
			name:        "invalid format - no colon",
			bundles:     map[string]*models.Bundle{},
			ref:         "core",
			expectError: true,
			errorMsg:    "invalid target reference",
		},
		{
			name: "bundle not found",
			bundles: map[string]*models.Bundle{
				"api": {Name: "api"},
			},
			ref:         "core:app_build",
			expectError: true,
			errorMsg:    "bundle not found",
		},
		{
			name: "target not found",
			bundles: map[string]*models.Bundle{
				"core": {
					Name: "core",
					Targets: []*models.Target{
						{Name: "app_build", BundleName: "core"},
					},
				},
			},
			ref:         "core:lib_build",
			expectError: true,
			errorMsg:    "target not found",
		},
		{
			name:        "empty reference",
			bundles:     map[string]*models.Bundle{},
			ref:         "",
			expectError: true,
			errorMsg:    "invalid target reference",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				bundles: tt.bundles,
			}

			target, err := cfg.ResolveTarget(tt.ref)

			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
				assert.Nil(t, target)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, target)
			}
		})
	}
}

func TestConfig_AllTargets(t *testing.T) {
	tests := []struct {
		name          string
		bundles       map[string]*models.Bundle
		expectedCount int
	}{
		{
			name:          "no bundles",
			bundles:       map[string]*models.Bundle{},
			expectedCount: 0,
		},
		{
			name: "single bundle single target",
			bundles: map[string]*models.Bundle{
				"core": {
					Name: "core",
					Targets: []*models.Target{
						{Name: "app_build", BundleName: "core"},
					},
				},
			},
			expectedCount: 1,
		},
		{
			name: "multiple bundles multiple targets",
			bundles: map[string]*models.Bundle{
				"core": {
					Name: "core",
					Targets: []*models.Target{
						{Name: "app_build", BundleName: "core"},
						{Name: "app_dev", BundleName: "core"},
					},
				},
				"api": {
					Name: "api",
					Targets: []*models.Target{
						{Name: "server_build", BundleName: "api"},
					},
				},
			},
			expectedCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{bundles: tt.bundles}
			targets := cfg.AllTargets()
			assert.Len(t, targets, tt.expectedCount)
		})
	}
}

func TestConfig_TargetsByType(t *testing.T) {
	tests := []struct {
		name          string
		bundles       map[string]*models.Bundle
		suffix        string
		expectedCount int
	}{
		{
			name: "filter build targets",
			bundles: map[string]*models.Bundle{
				"core": {
					Name: "core",
					Targets: []*models.Target{
						{Name: "app_build", BundleName: "core"},
						{Name: "app_dev", BundleName: "core"},
						{Name: "lib_build", BundleName: "core"},
					},
				},
			},
			suffix:        "_build",
			expectedCount: 2,
		},
		{
			name: "filter dev targets",
			bundles: map[string]*models.Bundle{
				"core": {
					Name: "core",
					Targets: []*models.Target{
						{Name: "app_build", BundleName: "core"},
						{Name: "app_dev", BundleName: "core"},
					},
				},
				"api": {
					Name: "api",
					Targets: []*models.Target{
						{Name: "server_dev", BundleName: "api"},
					},
				},
			},
			suffix:        "_dev",
			expectedCount: 2,
		},
		{
			name: "no matching targets",
			bundles: map[string]*models.Bundle{
				"core": {
					Name: "core",
					Targets: []*models.Target{
						{Name: "app_build", BundleName: "core"},
					},
				},
			},
			suffix:        "_test",
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{bundles: tt.bundles}
			targets := cfg.TargetsByType(tt.suffix)
			assert.Len(t, targets, tt.expectedCount)
		})
	}
}

func TestConfig_Accessors(t *testing.T) {
	cfg := &Config{
		repoRoot:   "/repo",
		buildsPath: "/repo/.rpm/builds.json",
		dagPath:    "/repo/.rpm/dag.json",
		repo:       &RepoConfig{Shell: "/bin/bash"},
		bundles: map[string]*models.Bundle{
			"core": {Name: "core"},
		},
	}

	assert.Equal(t, "/repo", cfg.RepoRoot())
	assert.Equal(t, "/repo/.rpm/builds.json", cfg.BuildsPath())
	assert.Equal(t, "/repo/.rpm/dag.json", cfg.DagPath())
	assert.Equal(t, "/bin/bash", cfg.Repo().Shell)
	assert.Len(t, cfg.Bundles(), 1)
	assert.Equal(t, "core", cfg.Bundles()["core"].Name)
}
