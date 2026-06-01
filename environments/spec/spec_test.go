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
env:
  FROM_REPO: repo
`), 0644))
	bundleRoot := filepath.Join(repoRoot, "apps", "api")
	require.NoError(t, os.MkdirAll(bundleRoot, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(bundleRoot, ".env"), []byte("FROM_DOTENV=base\nOVERRIDE=dotenv\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(bundleRoot, ".env.local"), []byte("FROM_LOCAL_DOTENV=local\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(bundleRoot, "rpm.yml"), []byte(`
name: api
env:
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

func TestResolvePreScriptsReuseTargetAndBundleContext(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte(`
shell: /bin/sh
env:
  GLOBAL_VAR: global
`), 0644))
	bundleRoot := filepath.Join(repoRoot, "apps", "api")
	require.NoError(t, os.MkdirAll(filepath.Join(bundleRoot, "scripts"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(bundleRoot, ".env"), []byte("FROM_DOTENV=dotenv\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(bundleRoot, "rpm.yml"), []byte(`
name: api
env:
  BUNDLE_VAR: bundle
targets:
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
		Pre: []models.EnvironmentPreScript{
			{Ref: "api:migrate"},
			{Ref: "api:scripts/bootstrap.sh"},
			{Ref: "/scripts/repo.sh"},
			{Ref: "echo inline\n"},
		},
	}

	resolved, err := spec.Resolve(repo, blueprint)

	require.NoError(t, err)
	require.Len(t, resolved.PreScripts, 4)
	assert.Equal(t, "api:migrate", resolved.PreScripts[0].Ref)
	assert.Equal(t, "echo migrate", resolved.PreScripts[0].Command)
	assert.Equal(t, filepath.Join(bundleRoot, "cmd"), resolved.PreScripts[0].WorkingDir)
	targetEnv := envMap(resolved.PreScripts[0].Env)
	assert.Equal(t, "global", targetEnv["GLOBAL_VAR"])
	assert.Equal(t, repoRoot, targetEnv["REPO_ROOT"])
	assert.Equal(t, bundleRoot, targetEnv["BUNDLE_ROOT"])
	assert.Equal(t, "bundle", targetEnv["BUNDLE_VAR"])
	assert.Equal(t, "target", targetEnv["TARGET_VAR"])
	assert.Equal(t, "stack", targetEnv["STACK_VAR"])
	assert.Equal(t, "dotenv", targetEnv["FROM_DOTENV"])

	assert.Equal(t, "api:scripts/bootstrap.sh", resolved.PreScripts[1].Ref)
	assert.Equal(t, bundleRoot, resolved.PreScripts[1].WorkingDir)
	assert.Contains(t, resolved.PreScripts[1].Command, filepath.Join(bundleRoot, "scripts", "bootstrap.sh"))
	bundleEnv := envMap(resolved.PreScripts[1].Env)
	assert.Equal(t, "bundle", bundleEnv["BUNDLE_VAR"])
	assert.Equal(t, "stack", bundleEnv["STACK_VAR"])

	assert.Equal(t, "/scripts/repo.sh", resolved.PreScripts[2].Ref)
	assert.Equal(t, repoRoot, resolved.PreScripts[2].WorkingDir)
	assert.Contains(t, resolved.PreScripts[2].Command, filepath.Join(repoRoot, "scripts", "repo.sh"))

	assert.Equal(t, "pre:inline:4", resolved.PreScripts[3].Ref)
	assert.Equal(t, "echo inline\n", resolved.PreScripts[3].Command)
	assert.Equal(t, repoRoot, resolved.PreScripts[3].WorkingDir)
}

func TestResolveSortsEnvironmentTargets(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte("shell: /bin/sh\n"), 0644))
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

func TestResolveUsesTargetReloadOverride(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte("shell: /bin/sh\n"), 0644))
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
	assert.True(t, resolved.Targets[0].Reload)
}

func writeBundle(t *testing.T, repoRoot string, name string, envValue string) {
	t.Helper()
	bundleRoot := filepath.Join(repoRoot, name)
	require.NoError(t, os.MkdirAll(bundleRoot, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(bundleRoot, "rpm.yml"), []byte("name: "+name+"\nenv:\n  NAME: "+envValue+"\ntargets:\n  - name: serve\n    cmd: echo serve\n"), 0644))
}

func envMap(env []spec.EnvVar) map[string]string {
	result := make(map[string]string)
	for _, item := range env {
		result[item.Name] = item.Value
	}
	return result
}
