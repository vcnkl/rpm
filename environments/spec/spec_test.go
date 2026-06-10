package spec_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rootconfig "github.com/vcnkl/rpm/config"
	"github.com/vcnkl/rpm/environments/spec"
	"github.com/vcnkl/rpm/models"
)

func TestResolveWorkingDirCompatibility(t *testing.T) {
	repoRoot := "/repo"
	tests := []struct {
		name     string
		workDir  string
		expected string
	}{
		{name: "local", workDir: "local", expected: filepath.Join(repoRoot, "apps", "api")},
		{name: "empty", workDir: "", expected: filepath.Join(repoRoot, "apps", "api")},
		{name: "repo root", workDir: "repo_root", expected: repoRoot},
		{name: "absolute", workDir: "/tmp/api", expected: "/tmp/api"},
		{name: "relative", workDir: "cmd/server", expected: filepath.Join(repoRoot, "apps", "api", "cmd", "server")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := &models.Target{
				BundlePath: "apps/api",
				Config: models.TargetConfig{
					WorkingDir: tt.workDir,
				},
			}

			assert.Equal(t, tt.expected, spec.ResolveWorkingDir(repoRoot, target))
		})
	}
}

func TestResolveTargetEnvAndDotenvCompatibility(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte(`
project:
  name: test-project
env:
  vars:
    FROM_REPO: repo
`), 0644))
	bundleRoot := filepath.Join(repoRoot, "apps", "api")
	require.NoError(t, os.MkdirAll(bundleRoot, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(bundleRoot, ".env"), []byte("FROM_DOTENV=base\nOVERRIDE=dotenv\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(bundleRoot, ".env.local"), []byte("FROM_LOCAL_DOTENV=local\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(bundleRoot, "rpm.yml"), []byte(`
name: api
env:
  variables:
    FROM_BUNDLE: bundle
targets:
  - name: serve
    cmd: echo serve
    env:
      FROM_TARGET: target
      OVERRIDE: target
    config:
      dotenv:
        enabled: true
        files:
          - ".env.local"
`), 0644))
	repo := rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
	bundle := repo.Bundles()["api"]
	target := bundle.Targets[0]
	blueprint := &models.EnvironmentBlueprint{
		Name:      "local",
		Variables: map[string]string{"FROM_BLUEPRINT": "blueprint", "OVERRIDE": "blueprint"},
		Targets: []models.EnvironmentTarget{
			{Ref: "api:serve", Env: map[string]string{"FROM_BLUEPRINT_TARGET": "bp-target", "OVERRIDE": "bp-target"}},
		},
	}

	env := spec.ResolveTargetEnv(repo, bundle, target, blueprint, blueprint.Targets[0])
	values := envMap(env)

	assert.Equal(t, "repo", values["FROM_REPO"])
	assert.Equal(t, "bundle", values["FROM_BUNDLE"])
	assert.Equal(t, "target", values["FROM_TARGET"])
	assert.Equal(t, "blueprint", values["FROM_BLUEPRINT"])
	assert.Equal(t, "bp-target", values["FROM_BLUEPRINT_TARGET"])
	assert.Equal(t, "base", values["FROM_DOTENV"])
	assert.Equal(t, "local", values["FROM_LOCAL_DOTENV"])
	assert.Equal(t, "dotenv", values["OVERRIDE"])
	assert.Equal(t, repoRoot, values["REPO_ROOT"])
	assert.Equal(t, bundleRoot, values["BUNDLE_ROOT"])
	assert.Equal(t, []string{filepath.Join(bundleRoot, ".env"), filepath.Join(bundleRoot, ".env.local")}, spec.ResolveDotenvFiles(repoRoot, bundle, target))
}

func TestResolveBeforeTargetsReuseTargetContext(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte(`
project:
  name: test-project
shell: /bin/sh
env:
  vars:
    GLOBAL_VAR: global
`), 0644))
	bundleRoot := filepath.Join(repoRoot, "apps", "api")
	require.NoError(t, os.MkdirAll(filepath.Join(bundleRoot, "scripts"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(bundleRoot, ".env"), []byte("FROM_DOTENV=dotenv\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(bundleRoot, "rpm.yml"), []byte(`
name: api
env:
  variables:
    BUNDLE_VAR: bundle
targets:
  - name: serve
    cmd: echo serve
  - name: migrate
    env:
      TARGET_VAR: target
    cmd: echo migrate
    config:
      working_dir: cmd
      dotenv:
        enabled: true
`), 0644))
	repo := rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
	blueprint := &models.EnvironmentBlueprint{
		Name:      "local",
		Variables: map[string]string{"STACK_VAR": "stack"},
		Before:    []string{"api:migrate"},
		Targets:   []models.EnvironmentTarget{{Ref: "api:serve", Env: map[string]string{}}},
	}

	resolved, err := spec.Resolve(repo, blueprint)

	require.NoError(t, err)
	require.Len(t, resolved.BeforeTargets, 1)
	assert.Equal(t, "api:migrate", resolved.BeforeTargets[0].Ref)
	assert.Equal(t, "echo migrate", resolved.BeforeTargets[0].Command)
	assert.Equal(t, filepath.Join(bundleRoot, "cmd"), resolved.BeforeTargets[0].WorkingDir)
	targetEnv := envMap(resolved.BeforeTargets[0].Env)
	assert.Equal(t, "global", targetEnv["GLOBAL_VAR"])
	assert.Equal(t, repoRoot, targetEnv["REPO_ROOT"])
	assert.Equal(t, bundleRoot, targetEnv["BUNDLE_ROOT"])
	assert.Equal(t, "bundle", targetEnv["BUNDLE_VAR"])
	assert.Equal(t, "target", targetEnv["TARGET_VAR"])
	assert.Equal(t, "stack", targetEnv["STACK_VAR"])
	assert.Equal(t, "dotenv", targetEnv["FROM_DOTENV"])
	assert.Equal(t, []spec.RuntimeUnit{
		{Id: "api:migrate", Kind: "before"},
		{Id: "api:serve", Kind: "target"},
	}, resolved.RuntimeUnits)
}

func TestResolveOrdersBeforeTargetsByConfigIndex(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte("project:\n  name: test-project\nshell: /bin/sh\n"), 0644))
	bundleRoot := filepath.Join(repoRoot, "api")
	require.NoError(t, os.MkdirAll(bundleRoot, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(bundleRoot, "rpm.yml"), []byte(`
name: api
targets:
  - name: serve
    cmd: echo serve
  - name: seed
    cmd: echo seed
  - name: init-db
    cmd: echo init
    config:
      index: 1
  - name: migrate
    cmd: echo migrate
    config:
      index: 2
  - name: cleanup
    cmd: echo cleanup
`), 0644))
	repo := rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
	blueprint := &models.EnvironmentBlueprint{
		Name:    "local",
		Before:  []string{"api:seed", "api:init-db", "api:migrate", "api:cleanup"},
		Targets: []models.EnvironmentTarget{{Ref: "api:serve", Env: map[string]string{}}},
	}

	resolved, err := spec.Resolve(repo, blueprint)

	require.NoError(t, err)
	assert.Equal(t, []string{"api:init-db", "api:migrate", "api:cleanup", "api:seed"}, beforeRefs(resolved.BeforeTargets))
	assert.Equal(t, []spec.RuntimeUnit{
		{Id: "api:init-db", Kind: "before"},
		{Id: "api:migrate", Kind: "before"},
		{Id: "api:cleanup", Kind: "before"},
		{Id: "api:seed", Kind: "before"},
		{Id: "api:serve", Kind: "target"},
	}, resolved.RuntimeUnits)
}

func TestResolveOrdersBeforeTargetIndexTiesByRef(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte("project:\n  name: test-project\nshell: /bin/sh\n"), 0644))
	bundleRoot := filepath.Join(repoRoot, "api")
	require.NoError(t, os.MkdirAll(bundleRoot, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(bundleRoot, "rpm.yml"), []byte(`
name: api
targets:
  - name: serve
    cmd: echo serve
  - name: zed
    cmd: echo zed
    config:
      index: 1
  - name: alpha
    cmd: echo alpha
    config:
      index: 1
`), 0644))
	repo := rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
	blueprint := &models.EnvironmentBlueprint{
		Name:    "local",
		Before:  []string{"api:zed", "api:alpha"},
		Targets: []models.EnvironmentTarget{{Ref: "api:serve", Env: map[string]string{}}},
	}

	resolved, err := spec.Resolve(repo, blueprint)

	require.NoError(t, err)
	assert.Equal(t, []string{"api:alpha", "api:zed"}, beforeRefs(resolved.BeforeTargets))
}

func TestResolveRejectsInvalidBeforeTargets(t *testing.T) {
	tests := []struct {
		name    string
		before  []string
		targets []models.EnvironmentTarget
		message string
	}{
		{
			name:    "unknown",
			before:  []string{"api:missing"},
			targets: []models.EnvironmentTarget{{Ref: "api:serve", Env: map[string]string{}}},
			message: "unknown before target ref",
		},
		{
			name:    "duplicate",
			before:  []string{"api:migrate", "api:migrate"},
			targets: []models.EnvironmentTarget{{Ref: "api:serve", Env: map[string]string{}}},
			message: "duplicate before target ref",
		},
		{
			name:    "overlap",
			before:  []string{"api:serve"},
			targets: []models.EnvironmentTarget{{Ref: "api:serve", Env: map[string]string{}}},
			message: "is also listed in targets",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoRoot := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte("project:\n  name: test-project\nshell: /bin/sh\n"), 0644))
			writeBundleWithTargets(t, repoRoot, "api", "serve", "migrate")
			repo := rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
			blueprint := &models.EnvironmentBlueprint{Name: "local", Before: tt.before, Targets: tt.targets}

			_, err := spec.Resolve(repo, blueprint)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.message)
		})
	}
}

func TestResolveSortsEnvironmentTargets(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte("project:\n  name: test-project\nshell: /bin/sh\n"), 0644))
	writeBundle(t, repoRoot, "z", "z")
	writeBundle(t, repoRoot, "a", "a")
	repo := rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
	blueprint := &models.EnvironmentBlueprint{
		Name: "local",
		ReloadPolicy: models.ReloadPolicy{
			Enabled: true,
		},
		Targets: []models.EnvironmentTarget{
			{Ref: "z:serve", Env: map[string]string{}},
			{Ref: "a:serve", Env: map[string]string{}},
		},
	}

	resolved, err := spec.Resolve(repo, blueprint)

	require.NoError(t, err)
	assert.Equal(t, []string{"a:serve", "z:serve"}, []string{resolved.Targets[0].Ref, resolved.Targets[1].Ref})
}

func TestResolveIncludesBundleDependenciesWhenPolicyDisablesThem(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte(`
project:
  name: test-project
shell: /bin/sh
env:
  deps:
    - name: postgres
      image: postgres:16
    - name: redis
      image: redis:7
`), 0644))
	writeBundleWithDependencies(t, repoRoot, "api", []string{"postgres"}, "serve", "migrate")
	writeBundleWithDependencies(t, repoRoot, "worker", []string{"redis"}, "serve")
	repo := rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
	blueprint := &models.EnvironmentBlueprint{
		Name:   "local",
		Before: []string{"api:migrate"},
		Targets: []models.EnvironmentTarget{
			{Ref: "api:serve", Env: map[string]string{}},
			{Ref: "worker:serve", Env: map[string]string{}},
		},
		DependencyPolicy: models.DependencyPolicy{
			Enabled: false,
			Include: []string{},
			Exclude: []string{"postgres", "redis"},
		},
	}

	resolved, err := spec.Resolve(repo, blueprint)

	require.NoError(t, err)
	assert.Equal(t, []string{"postgres", "redis"}, dependencyRefs(resolved.Dependencies))
}

func TestResolveIncludesExplicitDependencyPolicyRefs(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte(`
project:
  name: test-project
shell: /bin/sh
env:
  deps:
    - name: mongodb
      image: mongo:8.0.23-noble
      ports:
        - MONGODB_PORT=27017
    - name: postgres
      image: postgres:16
      env:
        POSTGRES_PASSWORD: example
      ports:
        - POSTGRES_PORT=5432
      volumes:
        - /var/lib/postgresql/data
      readiness-cmd: pg_isready
    - name: rabbitmq
      image: rabbitmq:3-management
      ports:
        - RABBITMQ_PORT=5672
    - name: redis
      image: redis:7
      ports:
        - REDIS_PORT=6379
`), 0644))
	writeBundle(t, repoRoot, "api", "api")
	repo := rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
	blueprint := &models.EnvironmentBlueprint{
		Name: "local",
		Targets: []models.EnvironmentTarget{
			{Ref: "api:serve", Env: map[string]string{}},
		},
		DependencyPolicy: models.DependencyPolicy{
			Enabled: true,
			Include: []string{"mongodb", "postgres", "rabbitmq", "redis"},
			Exclude: []string{},
		},
	}

	resolved, err := spec.Resolve(repo, blueprint)

	require.NoError(t, err)
	require.Len(t, resolved.Dependencies, 4)
	assert.Equal(t, []string{"mongodb", "postgres", "rabbitmq", "redis"}, dependencyRefs(resolved.Dependencies))
	assert.Equal(t, "mongo:8.0.23-noble", resolved.Dependencies[0].Image)
	assert.Equal(t, []string{"MONGODB_PORT=27017"}, resolved.Dependencies[0].Ports)
	assert.Equal(t, "postgres:16", resolved.Dependencies[1].Image)
	assert.Equal(t, map[string]string{"POSTGRES_PASSWORD": "example"}, envMap(resolved.Dependencies[1].Env))
	assert.Equal(t, []string{"POSTGRES_PORT=5432"}, resolved.Dependencies[1].Ports)
	assert.Equal(t, []string{"/var/lib/postgresql/data"}, resolved.Dependencies[1].Volumes)
	assert.Equal(t, "pg_isready", resolved.Dependencies[1].ReadinessCmd)
	assert.Equal(t, "rabbitmq:3-management", resolved.Dependencies[2].Image)
	assert.Equal(t, []string{"RABBITMQ_PORT=5672"}, resolved.Dependencies[2].Ports)
	assert.Equal(t, "redis:7", resolved.Dependencies[3].Image)
	assert.Equal(t, []string{"REDIS_PORT=6379"}, resolved.Dependencies[3].Ports)
}

func TestResolveUsesBundleTargetReloadConfig(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte("project:\n  name: test-project\nshell: /bin/sh\n"), 0644))
	writeBundleWithReload(t, repoRoot, "api", false)
	repo := rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
	blueprint := &models.EnvironmentBlueprint{
		Name: "local",
		ReloadPolicy: models.ReloadPolicy{
			Enabled: true,
		},
		Targets: []models.EnvironmentTarget{
			{Ref: "api:serve", Env: map[string]string{}},
		},
	}

	resolved, err := spec.Resolve(repo, blueprint)

	require.NoError(t, err)
	assert.False(t, resolved.Targets[0].Reload)
}

func TestResolveTargetReloadOverrideIsGatedByGlobalPolicy(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte("project:\n  name: test-project\nshell: /bin/sh\n"), 0644))
	writeBundle(t, repoRoot, "api", "api")
	repo := rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
	reload := true
	blueprint := &models.EnvironmentBlueprint{
		Name: "local",
		ReloadPolicy: models.ReloadPolicy{
			Enabled: false,
		},
		Targets: []models.EnvironmentTarget{
			{Ref: "api:serve", Reload: &reload, Env: map[string]string{}},
		},
	}

	resolved, err := spec.Resolve(repo, blueprint)

	require.NoError(t, err)
	assert.False(t, resolved.Targets[0].Reload)
}

func writeBundle(t *testing.T, repoRoot string, name string, envValue string) {
	t.Helper()
	bundleRoot := filepath.Join(repoRoot, name)
	require.NoError(t, os.MkdirAll(bundleRoot, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(bundleRoot, "rpm.yml"), []byte("name: "+name+"\nenv:\n  NAME: "+envValue+"\ntargets:\n  - name: serve\n    cmd: echo serve\n"), 0644))
}

func writeBundleWithTargets(t *testing.T, repoRoot string, name string, targets ...string) {
	t.Helper()
	bundleRoot := filepath.Join(repoRoot, name)
	require.NoError(t, os.MkdirAll(bundleRoot, 0755))
	content := "name: " + name + "\ntargets:\n"
	for _, target := range targets {
		content += "  - name: " + target + "\n    cmd: echo " + target + "\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(bundleRoot, "rpm.yml"), []byte(content), 0644))
}

func writeBundleWithDependencies(t *testing.T, repoRoot string, name string, deps []string, targets ...string) {
	t.Helper()
	bundleRoot := filepath.Join(repoRoot, name)
	require.NoError(t, os.MkdirAll(bundleRoot, 0755))
	content := "name: " + name + "\nenv:\n  deps:\n"
	for _, dep := range deps {
		content += "    - " + dep + "\n"
	}
	content += "targets:\n"
	for _, target := range targets {
		content += "  - name: " + target + "\n    cmd: echo " + target + "\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(bundleRoot, "rpm.yml"), []byte(content), 0644))
}

func writeBundleWithReload(t *testing.T, repoRoot string, name string, reload bool) {
	t.Helper()
	bundleRoot := filepath.Join(repoRoot, name)
	require.NoError(t, os.MkdirAll(bundleRoot, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(bundleRoot, "rpm.yml"), []byte("name: "+name+"\ntargets:\n  - name: serve\n    cmd: echo serve\n    config:\n      reload: "+boolString(reload)+"\n"), 0644))
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func envMap(env []spec.EnvVar) map[string]string {
	result := make(map[string]string)
	for _, item := range env {
		result[item.Name] = item.Value
	}
	return result
}

func beforeRefs(targets []spec.BeforeTarget) []string {
	refs := make([]string, 0, len(targets))
	for _, target := range targets {
		refs = append(refs, target.Ref)
	}
	return refs
}

func dependencyRefs(dependencies []spec.Dependency) []string {
	refs := make([]string, 0, len(dependencies))
	for _, dep := range dependencies {
		refs = append(refs, dep.Ref)
	}
	return refs
}
