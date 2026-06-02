package starlark_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rootconfig "github.com/vcnkl/rpm/config"
	"github.com/vcnkl/rpm/environments/generator"
	"github.com/vcnkl/rpm/environments/spec"
	envstarlark "github.com/vcnkl/rpm/environments/starlark"
	"github.com/vcnkl/rpm/models"
)

func TestInterpretGeneratedPlan(t *testing.T) {
	src := []byte(`
rpm_environment(name = "local", live_reload = {"enabled": True, "debounce": "100ms"}, variables = {"LOG_LEVEL": "debug"})
rpm_dependency(ref = "postgres", name = "postgres", image = "postgres:16", env = {"POSTGRES_PASSWORD": "example"}, ports = ["5432:5432"], volumes = ["/data"])
rpm_before_target(ref = "api:migrate", command = "go run migrations", workdir = "/repo/api", env = {"A": "b"})
rpm_target(ref = "api:serve", command = "go run .", workdir = "/repo/api", env = {"A": "b"}, reload = True)
rpm_watch(target = "api:serve", roots = ["/repo/api"], ignore = ["bin/**"], reload = True, enabled = True)
rpm_run(order = ["postgres", "api:serve"])
`)

	plan, err := envstarlark.InterpretSource(context.Background(), "local", "env.star", src)

	require.NoError(t, err)
	assert.Equal(t, "local", plan.Environment.Name)
	assert.Equal(t, "debug", plan.Environment.Variables["LOG_LEVEL"])
	assert.Equal(t, "postgres", plan.Dependencies[0].Ref)
	assert.Equal(t, []string{"5432:5432"}, plan.Dependencies[0].Ports)
	assert.Equal(t, "api:migrate", plan.BeforeTargets[0].Ref)
	assert.Equal(t, "go run migrations", plan.BeforeTargets[0].Command)
	assert.Equal(t, "/repo/api", plan.BeforeTargets[0].WorkingDir)
	assert.Equal(t, "api:serve", plan.Targets[0].Ref)
	assert.Equal(t, "/repo/api", plan.Targets[0].WorkingDir)
	assert.True(t, plan.Targets[0].Reload)
	assert.Equal(t, []string{"/repo/api"}, plan.Watches[0].Roots)
	assert.Equal(t, []string{"postgres", "api:serve"}, plan.RunOrder)
}

func TestInterpretGeneratorOutput(t *testing.T) {
	repo := newStarlarkRepo(t)
	blueprint := &models.EnvironmentBlueprint{
		Name: "local",
		Variables: map[string]string{
			"LOG_LEVEL": "debug",
		},
		Targets: []models.EnvironmentTarget{
			{Ref: "api:serve", Env: map[string]string{"APP_PORT": "8080"}},
		},
		Before:           []string{"api:migrate"},
		DependencyPolicy: models.DependencyPolicy{Enabled: true},
		ReloadPolicy:     models.ReloadPolicy{Enabled: true, Debounce: "100ms"},
	}
	resolved, err := spec.Resolve(repo, blueprint)
	require.NoError(t, err)
	src, err := generator.Render(resolved)
	require.NoError(t, err)

	plan, err := envstarlark.InterpretSource(context.Background(), "local", "env.star", src)

	require.NoError(t, err)
	assert.Equal(t, "local", plan.Environment.Name)
	assert.Equal(t, "postgres", plan.Dependencies[0].Ref)
	assert.Equal(t, []string{"api:migrate"}, []string{plan.BeforeTargets[0].Ref})
	assert.Equal(t, "api:serve", plan.Targets[0].Ref)
	assert.Equal(t, "8080", plan.Targets[0].Env["APP_PORT"])
	assert.Equal(t, filepath.Join(repo.RepoRoot(), "api"), plan.Targets[0].WorkingDir)
	assert.Equal(t, []string{filepath.Join(repo.RepoRoot(), "api", "*.go")}, plan.Watches[0].Roots)
	assert.Equal(t, []string{"tmp/**"}, plan.Watches[0].Ignore)
}

func TestInterpretRejectsUnsupportedValuesWithBacktrace(t *testing.T) {
	src := []byte(`
def broken():
    rpm_target(ref = "api:serve", command = "go run .", workdir = "/repo/api", env = {"A": 1}, reload = True)
broken()
`)

	_, err := envstarlark.InterpretSource(context.Background(), "local", "env.star", src)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected string, got int")
	assert.Contains(t, err.Error(), "Traceback")
	assert.Contains(t, err.Error(), "broken")
}

func TestInterpretFailsClearlyForLoad(t *testing.T) {
	src := []byte(`load("other.star", "value")`)

	_, err := envstarlark.InterpretSource(context.Background(), "local", "env.star", src)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "load")
}

func TestInterpretEnforcesStepBudget(t *testing.T) {
	src := []byte(`
def spin():
    for _ in range(1000000000):
        pass
spin()
`)

	_, err := envstarlark.InterpretSource(context.Background(), "local", "env.star", src)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "too many steps")
}

func TestInterpretHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	src := []byte(`
def spin():
    for _ in range(1000000000):
        pass
spin()
`)

	_, err := envstarlark.InterpretSource(ctx, "local", "env.star", src)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Starlark computation cancelled")
}

func newStarlarkRepo(t *testing.T) *rootconfig.Config {
	t.Helper()
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte(`
project:
  name: test-project
shell: /bin/sh
env:
  GLOBAL_VAR: global
dependencies:
  - name: postgres
    image: postgres:16
    ports:
      - "5432:5432"
`), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "api"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "api", "rpm.yml"), []byte(`
name: api
env:
  variables:
    BUNDLE_VAR: bundle
  deps:
    - postgres
targets:
  - name: migrate
    cmd: go run migrations
  - name: serve
    in:
      - "*.go"
    cmd: go run .
    env:
      TARGET_VAR: target
    config:
      ignore:
        - tmp/**
`), 0644))
	return rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
}
