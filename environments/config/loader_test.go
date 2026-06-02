package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rootconfig "github.com/vcnkl/rpm/config"
	envconfig "github.com/vcnkl/rpm/environments/config"
	"github.com/vcnkl/rpm/models"
)

func TestLoadBlueprint(t *testing.T) {
	repo := newTestRepoWithTargets(t, "serve", "migrate")
	writeBlueprint(t, repo.RepoRoot(), "local", `
version: 1
name: local
live_reload:
  enabled: false
targets:
  - ref: api:serve
    reload: true
    env:
      PORT: "8080"
variables:
  LOG_LEVEL: debug
before:
  - api:migrate
`)

	blueprint, err := envconfig.LoadBlueprint(repo, "local")

	require.NoError(t, err)
	assert.Equal(t, "local", blueprint.Name)
	assert.False(t, blueprint.ReloadPolicy.Enabled)
	assert.Equal(t, "100ms", blueprint.ReloadPolicy.Debounce)
	require.Len(t, blueprint.Targets, 1)
	assert.Equal(t, "api:serve", blueprint.Targets[0].Ref)
	assert.True(t, *blueprint.Targets[0].Reload)
	assert.Equal(t, "8080", blueprint.Targets[0].Env["PORT"])
	assert.Equal(t, []string{"api:migrate"}, blueprint.Before)
}

func TestLoadBlueprintUnknownFile(t *testing.T) {
	repo := newTestRepo(t)

	_, err := envconfig.LoadBlueprint(repo, "missing")

	require.Error(t, err)
	assert.True(t, errors.Is(err, envconfig.ErrUnknownBlueprint))
	assert.Contains(t, err.Error(), ".rpm/envs/missing.yml")
}

func TestBlueprintPathUsesRpmEnvs(t *testing.T) {
	repo := newTestRepo(t)

	path, err := envconfig.BlueprintPath(repo, "local")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(repo.RepoRoot(), ".rpm", "envs", "local.yml"), path)
}

func TestBlueprintPathRejectsTraversal(t *testing.T) {
	repo := newTestRepo(t)

	for _, name := range []string{"", ".", "..", "../repo", "../../repo", "nested/local", `nested\local`} {
		t.Run(name, func(t *testing.T) {
			_, err := envconfig.BlueprintPath(repo, name)
			require.Error(t, err)
			assert.True(t, errors.Is(err, envconfig.ErrInvalidBlueprintName))
		})
	}
}

func TestWriteBlueprintRejectsTraversalName(t *testing.T) {
	repo := newTestRepo(t)

	err := envconfig.WriteBlueprint(repo, &models.EnvironmentBlueprint{
		Version: 1,
		Name:    "../../repo",
		Targets: []models.EnvironmentTarget{
			{Ref: "api:serve"},
		},
		Variables: map[string]string{},
	})

	require.Error(t, err)
	assert.True(t, errors.Is(err, envconfig.ErrInvalidBlueprintName))
}

func TestLoadBlueprintDuplicateTargetRefs(t *testing.T) {
	repo := newTestRepo(t)
	writeBlueprint(t, repo.RepoRoot(), "dupes", `
name: dupes
targets:
  - ref: api:serve
  - ref: api:serve
`)

	_, err := envconfig.LoadBlueprint(repo, "dupes")

	require.Error(t, err)
	assert.True(t, errors.Is(err, envconfig.ErrDuplicateBlueprintRef))
}

func TestLoadBlueprintInvalidAndUnknownTargetRefs(t *testing.T) {
	tests := []struct {
		name    string
		content string
		message string
	}{
		{
			name: "invalid",
			content: `
name: invalid
targets:
  - ref: serve
`,
			message: "invalid blueprint target ref",
		},
		{
			name: "unknown",
			content: `
name: unknown
targets:
  - ref: api:missing
`,
			message: "unknown blueprint target ref",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newTestRepo(t)
			writeBlueprint(t, repo.RepoRoot(), tt.name, tt.content)

			_, err := envconfig.LoadBlueprint(repo, tt.name)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.message)
		})
	}
}

func TestLoadBlueprintRejectsLegacyPre(t *testing.T) {
	repo := newTestRepo(t)
	writeBlueprint(t, repo.RepoRoot(), "legacy", `
name: legacy
targets:
  - ref: api:serve
pre:
  - api:serve
`)

	_, err := envconfig.LoadBlueprint(repo, "legacy")

	require.Error(t, err)
	assert.True(t, errors.Is(err, envconfig.ErrUnsupportedPre))
	assert.Contains(t, err.Error(), "pre is no longer supported; use before with existing target refs")
}

func TestLoadBlueprintInvalidBeforeTarget(t *testing.T) {
	tests := []struct {
		name    string
		content string
		message string
	}{
		{
			name: "empty",
			content: `
name: empty
targets:
  - ref: api:serve
before:
  - ""
`,
			message: "invalid blueprint before target ref",
		},
		{
			name: "unqualified",
			content: `
name: unqualified
targets:
  - ref: api:serve
before:
  - scripts/bootstrap.sh
`,
			message: "invalid blueprint before target ref",
		},
		{
			name: "unknown-target",
			content: `
name: unknown-target
targets:
  - ref: api:serve
before:
  - api:missing
`,
			message: "unknown blueprint target ref",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newTestRepo(t)
			writeBlueprint(t, repo.RepoRoot(), tt.name, tt.content)

			_, err := envconfig.LoadBlueprint(repo, tt.name)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.message)
		})
	}
}

func TestLoadBlueprintRejectsBeforeDuplicateAndOverlap(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name: "duplicate",
			content: `
name: duplicate
targets:
  - ref: api:serve
before:
  - api:migrate
  - api:migrate
`,
		},
		{
			name: "overlap",
			content: `
name: overlap
targets:
  - ref: api:serve
before:
  - api:serve
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newTestRepoWithTargets(t, "serve", "migrate")
			writeBlueprint(t, repo.RepoRoot(), tt.name, tt.content)

			_, err := envconfig.LoadBlueprint(repo, tt.name)

			require.Error(t, err)
			assert.True(t, errors.Is(err, envconfig.ErrDuplicateBlueprintRef))
		})
	}
}

func TestLoadBlueprintTargetsSorted(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte("shell: /bin/sh\n"), 0644))
	writeBundle(t, repoRoot, "api", "api", "serve")
	writeBundle(t, repoRoot, "worker", "worker", "run")
	repo := rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
	writeBlueprint(t, repo.RepoRoot(), "local", `
name: local
targets:
  - ref: worker:run
  - ref: api:serve
`)

	blueprint, err := envconfig.LoadBlueprint(repo, "local")

	require.NoError(t, err)
	assert.Equal(t, []string{"api:serve", "worker:run"}, []string{blueprint.Targets[0].Ref, blueprint.Targets[1].Ref})
}

func TestLoadBlueprintDefaultsLiveReload(t *testing.T) {
	repo := newTestRepo(t)
	writeBlueprint(t, repo.RepoRoot(), "local", `
name: local
targets:
  - ref: api:serve
`)

	blueprint, err := envconfig.LoadBlueprint(repo, "local")

	require.NoError(t, err)
	assert.True(t, blueprint.ReloadPolicy.Enabled)
	assert.Equal(t, "100ms", blueprint.ReloadPolicy.Debounce)
}

func TestLoadBlueprintDependencyPolicy(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte("shell: /bin/sh\n"), 0644))
	writeBundleContent(t, repoRoot, "api", `
name: api
env:
  dependencies:
    - name: postgres
      image: postgres:16
targets:
  - name: serve
    cmd: echo serve
`)
	repo := rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
	writeBlueprint(t, repo.RepoRoot(), "local", `
name: local
targets:
  - ref: api:serve
dependencies:
  enabled: true
  include:
    - api:postgres
  exclude: []
`)

	blueprint, err := envconfig.LoadBlueprint(repo, "local")

	require.NoError(t, err)
	assert.True(t, blueprint.DependencyPolicy.Enabled)
	assert.Equal(t, []string{"api:postgres"}, blueprint.DependencyPolicy.Include)
	assert.Empty(t, blueprint.DependencyPolicy.Exclude)
}

func TestLoadBlueprintUnknownDependencyRef(t *testing.T) {
	repo := newTestRepo(t)
	writeBlueprint(t, repo.RepoRoot(), "local", `
name: local
targets:
  - ref: api:serve
dependencies:
  enabled: true
  include:
    - api:postgres
`)

	_, err := envconfig.LoadBlueprint(repo, "local")

	require.Error(t, err)
	assert.True(t, errors.Is(err, envconfig.ErrUnknownDependencyRef))
}

func TestMarshalBlueprintDeterministicYAML(t *testing.T) {
	reloadFalse := false
	reloadTrue := true
	blueprint := &models.EnvironmentBlueprint{
		Version: 1,
		Name:    "local-stack",
		ReloadPolicy: models.ReloadPolicy{
			Enabled:  true,
			Debounce: "100ms",
		},
		Targets: []models.EnvironmentTarget{
			{Ref: "ts-app:web", Reload: &reloadTrue},
			{Ref: "go-app:serve", Reload: &reloadFalse, Env: map[string]string{"APP_PORT": "8080", "LOG_LEVEL": "debug"}},
		},
		Before: []string{"go-app:migrate"},
		DependencyPolicy: models.DependencyPolicy{
			Enabled: true,
			Include: []string{"ts-app:mailhog", "go-app:postgres"},
			Exclude: []string{"python-app:redis"},
		},
		Variables: map[string]string{"ZED": "last", "LOG_LEVEL": "debug"},
	}

	data, err := envconfig.MarshalBlueprint(blueprint)

	require.NoError(t, err)
	assert.Equal(t, `version: 1
name: local-stack
live_reload:
    enabled: true
    debounce: 100ms
before:
    - go-app:migrate
targets:
    - ref: go-app:serve
      reload: false
      env:
        APP_PORT: "8080"
        LOG_LEVEL: debug
    - ref: ts-app:web
      reload: true
dependencies:
    enabled: true
    include:
        - go-app:postgres
        - ts-app:mailhog
    exclude:
        - python-app:redis
variables:
    LOG_LEVEL: debug
    ZED: last
`, string(data))
}

func TestBundleDependencyDefaultsAndValidation(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte("shell: /bin/sh\n"), 0644))
	writeBundleContent(t, repoRoot, "api", `
name: api
env:
  dependencies:
    - name: postgres
      image: postgres:16
targets:
  - name: serve
    cmd: echo serve
`)

	repo := rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))

	require.Len(t, repo.Bundles()["api"].Dependencies, 1)
	assert.Equal(t, "shared", string(repo.Bundles()["api"].Dependencies[0].Mode))
}

func TestBundleDependencyImageValidation(t *testing.T) {
	valid := []string{"postgres:16", "library/postgres:16", "ghcr.io/org/service:2026.05.30"}
	invalid := []string{"postgres", "library/postgres", "ghcr.io/org/service"}

	for _, image := range valid {
		t.Run("valid "+image, func(t *testing.T) {
			assert.NoError(t, rootconfig.ValidateDependencyImage(image))
		})
	}
	for _, image := range invalid {
		t.Run("invalid "+image, func(t *testing.T) {
			assert.Error(t, rootconfig.ValidateDependencyImage(image))
		})
	}
}

func newTestRepo(t *testing.T) *rootconfig.Config {
	t.Helper()
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte("shell: /bin/sh\n"), 0644))
	writeBundle(t, repoRoot, "api", "api", "serve")
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, ".rpm", "envs"), 0755))
	return rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
}

func newTestRepoWithTargets(t *testing.T, targets ...string) *rootconfig.Config {
	t.Helper()
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte("shell: /bin/sh\n"), 0644))
	content := "name: api\ntargets:\n"
	for _, target := range targets {
		content += "  - name: " + target + "\n    cmd: echo " + target + "\n"
	}
	writeBundleContent(t, repoRoot, "api", content)
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, ".rpm", "envs"), 0755))
	return rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
}

func writeBundle(t *testing.T, repoRoot string, dir string, name string, target string) {
	t.Helper()
	writeBundleContent(t, repoRoot, dir, "name: "+name+"\ntargets:\n  - name: "+target+"\n    cmd: echo "+target+"\n")
}

func writeBundleContent(t *testing.T, repoRoot string, dir string, content string) {
	t.Helper()
	bundleRoot := filepath.Join(repoRoot, dir)
	require.NoError(t, os.MkdirAll(bundleRoot, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(bundleRoot, "rpm.yml"), []byte(content), 0644))
}

func writeBlueprint(t *testing.T, repoRoot string, name string, content string) {
	t.Helper()
	blueprintRoot := filepath.Join(repoRoot, ".rpm", "envs")
	require.NoError(t, os.MkdirAll(blueprintRoot, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(blueprintRoot, name+".yml"), []byte(content), 0644))
}
