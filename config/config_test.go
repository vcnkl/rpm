package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
				Shell:        "/bin/sh",
				Env:          map[string]string{},
				Dependencies: []EnvironmentDependency{},
				Logger: LoggerConfig{
					DateTime: LoggerDateTimeConfig{
						Format: "2006-01-02T15:04:05Z07:00",
					},
				},
			},
		},
		{
			name: "preserves existing shell",
			initial: RepoConfig{
				Shell: "/bin/bash",
			},
			expected: RepoConfig{
				Shell:        "/bin/bash",
				Env:          map[string]string{},
				Dependencies: []EnvironmentDependency{},
				Logger: LoggerConfig{
					DateTime: LoggerDateTimeConfig{
						Format: "2006-01-02T15:04:05Z07:00",
					},
				},
			},
		},
		{
			name: "preserves existing env",
			initial: RepoConfig{
				Env: map[string]string{"FOO": "bar"},
			},
			expected: RepoConfig{
				Shell:        "/bin/sh",
				Env:          map[string]string{"FOO": "bar"},
				Dependencies: []EnvironmentDependency{},
				Logger: LoggerConfig{
					DateTime: LoggerDateTimeConfig{
						Format: "2006-01-02T15:04:05Z07:00",
					},
				},
			},
		},
		{
			name: "preserves existing logger datetime format",
			initial: RepoConfig{
				Logger: LoggerConfig{
					DateTime: LoggerDateTimeConfig{
						Format: "2006-01-02 15:04:05",
					},
				},
			},
			expected: RepoConfig{
				Shell:        "/bin/sh",
				Env:          map[string]string{},
				Dependencies: []EnvironmentDependency{},
				Logger: LoggerConfig{
					DateTime: LoggerDateTimeConfig{
						Format: "2006-01-02 15:04:05",
					},
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
			assert.Equal(t, tt.expected.Dependencies, cfg.Dependencies)
			assert.Equal(t, tt.expected.Logger.DateTime.Format, cfg.Logger.DateTime.Format)
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
				Env: BundleEnvConfig{
					Variables: map[string]string{"KEY": "value"},
				},
			},
			expectEnvNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.initial
			cfg.SetDefaults()

			assert.NotNil(t, cfg.Env.Variables)
			assert.NotNil(t, cfg.Env.Deps)
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
					Name: "serve",
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
					Name: "serve",
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
						{Name: "serve", BundleName: "core"},
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

func TestConfig_AllTargetsSorted(t *testing.T) {
	cfg := &Config{
		bundles: map[string]*models.Bundle{
			"z": {
				Name: "z",
				Targets: []*models.Target{
					{Name: "b", BundleName: "z"},
					{Name: "a", BundleName: "z"},
				},
			},
			"a": {
				Name: "a",
				Targets: []*models.Target{
					{Name: "b", BundleName: "a"},
				},
			},
		},
	}

	targets := cfg.AllTargets()

	assert.Equal(t, []string{"a:b", "z:a", "z:b"}, []string{targets[0].ID(), targets[1].ID(), targets[2].ID()})
}

func TestConfig_QueryTargets(t *testing.T) {
	cfg := &Config{
		bundles: map[string]*models.Bundle{
			"api": {
				Name: "api",
				Targets: []*models.Target{
					{Name: "app_build", BundleName: "api"},
					{Name: "app_test", BundleName: "api"},
					{Name: "build", BundleName: "api"},
					{Name: "http_dev", BundleName: "api"},
					{Name: "migrate", BundleName: "api"},
					{Name: "test", BundleName: "api"},
				},
			},
			"worker": {
				Name: "worker",
				Targets: []*models.Target{
					{Name: "jobs_serve", BundleName: "worker"},
				},
			},
		},
	}

	targets := cfg.QueryTargets(func(target *models.Target) bool {
		return strings.HasSuffix(target.Name, "_dev") || strings.HasSuffix(target.Name, "_serve")
	})

	assert.Equal(t, []string{"api:http_dev", "worker:jobs_serve"}, targetIds(targets))

	targets = cfg.QueryTargets(func(target *models.Target) bool {
		return target.Name != "build" &&
			target.Name != "test" &&
			!strings.HasSuffix(target.Name, "_build") &&
			!strings.HasSuffix(target.Name, "_test")
	})

	assert.Equal(t, []string{"api:http_dev", "api:migrate", "worker:jobs_serve"}, targetIds(targets))
}

func TestNewConfigWithRepoFile(t *testing.T) {
	repoRoot := t.TempDir()
	requireNoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte("project:\n  name: test-project\nshell: /bin/bash\n"), 0644))
	requireNoError(t, os.MkdirAll(filepath.Join(repoRoot, "services", "api"), 0755))
	requireNoError(t, os.WriteFile(filepath.Join(repoRoot, "services", "api", "rpm.yml"), []byte(`
name: api
targets:
  - name: serve
    cmd: echo serve
`), 0644))

	cfg := NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))

	assert.Equal(t, repoRoot, cfg.RepoRoot())
	assert.Equal(t, "/bin/bash", cfg.Repo().Shell)
	assert.NotNil(t, cfg.Bundles()["api"])
}

func TestDiscoverBundlesSortedByRepoRelativePath(t *testing.T) {
	repoRoot := t.TempDir()
	requireNoError(t, os.MkdirAll(filepath.Join(repoRoot, "z"), 0755))
	requireNoError(t, os.MkdirAll(filepath.Join(repoRoot, "a"), 0755))
	requireNoError(t, os.WriteFile(filepath.Join(repoRoot, "z", "rpm.yml"), []byte("name: z\ntargets:\n  - name: serve\n    cmd: echo z\n"), 0644))
	requireNoError(t, os.WriteFile(filepath.Join(repoRoot, "a", "rpm.yml"), []byte("name: a\ntargets:\n  - name: serve\n    cmd: echo a\n"), 0644))

	bundles := discoverBundles(repoRoot, nil, map[string]bool{})

	assert.Equal(t, []string{"a", "z"}, []string{bundles[0].Name, bundles[1].Name})
}

func TestValidateDependencyImage(t *testing.T) {
	tests := []struct {
		image string
		valid bool
	}{
		{image: "postgres:16", valid: true},
		{image: "library/postgres:16", valid: true},
		{image: "ghcr.io/org/service:2026.05.30", valid: true},
		{image: "postgres", valid: false},
		{image: "library/postgres", valid: false},
		{image: "ghcr.io/org/service", valid: false},
	}

	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			err := ValidateDependencyImage(tt.image)
			if tt.valid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestNewConfigValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		message string
	}{
		{
			name: "duplicate bundle names",
			files: map[string]string{
				"a/rpm.yml": "name: api\ntargets:\n  - name: serve\n    cmd: echo serve\n",
				"b/rpm.yml": "name: api\ntargets:\n  - name: serve\n    cmd: echo serve\n",
			},
			message: "duplicate bundle name",
		},
		{
			name: "duplicate target names",
			files: map[string]string{
				"api/rpm.yml": "name: api\ntargets:\n  - name: serve\n    cmd: echo one\n  - name: serve\n    cmd: echo two\n",
			},
			message: "duplicate target name",
		},
		{
			name: "missing target command",
			files: map[string]string{
				"api/rpm.yml": "name: api\ntargets:\n  - name: serve\n",
			},
			message: "missing target command",
		},
		{
			name: "missing bundle name",
			files: map[string]string{
				"api/rpm.yml": "targets:\n  - name: serve\n    cmd: echo serve\n",
			},
			message: "missing bundle name",
		},
		{
			name: "missing target name",
			files: map[string]string{
				"api/rpm.yml": "name: api\ntargets:\n  - cmd: echo serve\n",
			},
			message: "missing target name",
		},
		{
			name: "invalid target dependency ref",
			files: map[string]string{
				"api/rpm.yml": "name: api\ntargets:\n  - name: serve\n    deps:\n      - missing\n    cmd: echo serve\n",
			},
			message: "invalid target ref",
		},
		{
			name: "legacy bundle dependencies unsupported",
			files: map[string]string{
				"api/rpm.yml": "name: api\nenv:\n  dependencies:\n    - name: postgres\n      image: postgres:16\ntargets:\n  - name: serve\n    cmd: echo serve\n",
			},
			message: "env.dependencies is not supported",
		},
		{
			name:    "missing project name",
			files:   map[string]string{"repo.yml": "shell: /bin/sh\n", "api/rpm.yml": "name: api\ntargets:\n  - name: serve\n    cmd: echo serve\n"},
			message: "project.name is required",
		},
		{
			name: "duplicate repo dependency names",
			files: map[string]string{
				"repo.yml":    "project:\n  name: test-project\ndependencies:\n  - name: postgres\n    image: postgres:16\n  - name: postgres\n    image: postgres:16\n",
				"api/rpm.yml": "name: api\ntargets:\n  - name: serve\n    cmd: echo serve\n",
			},
			message: "duplicate dependency name",
		},
		{
			name: "missing repo dependency image",
			files: map[string]string{
				"repo.yml":    "project:\n  name: test-project\ndependencies:\n  - name: postgres\n",
				"api/rpm.yml": "name: api\ntargets:\n  - name: serve\n    cmd: echo serve\n",
			},
			message: "invalid dependency image",
		},
		{
			name: "repo dependency volume bind rejected",
			files: map[string]string{
				"repo.yml":    "project:\n  name: test-project\ndependencies:\n  - name: postgres\n    image: postgres:16\n    volumes:\n      - postgres-data:/var/lib/postgresql/data\n",
				"api/rpm.yml": "name: api\ntargets:\n  - name: serve\n    cmd: echo serve\n",
			},
			message: "container path only",
		},
		{
			name: "unknown env dependency ref",
			files: map[string]string{
				"api/rpm.yml": "name: api\nenv:\n  deps:\n    - postgres\ntargets:\n  - name: serve\n    cmd: echo serve\n",
			},
			message: "unknown dependency",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoRoot := t.TempDir()
			if _, ok := tt.files["repo.yml"]; !ok {
				requireNoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte("project:\n  name: test-project\nshell: /bin/sh\n"), 0644))
			}
			for path, content := range tt.files {
				fullPath := filepath.Join(repoRoot, path)
				requireNoError(t, os.MkdirAll(filepath.Dir(fullPath), 0755))
				requireNoError(t, os.WriteFile(fullPath, []byte(content), 0644))
			}

			assertPanicsContains(t, tt.message, func() {
				NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
			})
		})
	}
}

func TestConfig_Accessors(t *testing.T) {
	cfg := &Config{
		repoRoot:   "/repo",
		buildsPath: "/repo/.rpm/cache/builds.json",
		dagPath:    "/repo/.rpm/cache/dag.json",
		repo:       &RepoConfig{Shell: "/bin/bash"},
		bundles: map[string]*models.Bundle{
			"core": {Name: "core"},
		},
	}

	assert.Equal(t, "/repo", cfg.RepoRoot())
	assert.Equal(t, "/repo/.rpm/cache/builds.json", cfg.BuildsPath())
	assert.Equal(t, "/repo/.rpm/cache/dag.json", cfg.DagPath())
	assert.Equal(t, "/bin/bash", cfg.Repo().Shell)
	assert.Len(t, cfg.Bundles(), 1)
	assert.Equal(t, "core", cfg.Bundles()["core"].Name)
}

func targetIds(targets []*models.Target) []string {
	ids := make([]string, 0, len(targets))
	for _, target := range targets {
		ids = append(ids, target.ID())
	}
	return ids
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func assertPanicsContains(t *testing.T, message string, fn func()) {
	t.Helper()
	defer func() {
		value := recover()
		if value == nil {
			t.Fatalf("expected panic containing %q", message)
		}
		assert.Contains(t, fmt.Sprint(value), message)
	}()
	fn()
}

func TestConfig_InitPathsUsesCacheDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &Config{repoRoot: tmpDir}

	cfg.initPaths()

	assert.Equal(t, filepath.Join(tmpDir, ".rpm"), cfg.rpmDir)
	assert.Equal(t, filepath.Join(tmpDir, ".rpm", "cache"), cfg.cacheDir)
	assert.Equal(t, filepath.Join(tmpDir, ".rpm", "cache", "builds.json"), cfg.BuildsPath())
	assert.Equal(t, filepath.Join(tmpDir, ".rpm", "cache", "dag.json"), cfg.DagPath())
	assert.FileExists(t, cfg.BuildsPath())
	assert.DirExists(t, filepath.Join(tmpDir, ".rpm", "cache"))
}
