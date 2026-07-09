package generator_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rootconfig "github.com/vcnkl/rpm/config"
	envconfig "github.com/vcnkl/rpm/environments/config"
	"github.com/vcnkl/rpm/environments/generator"
	"github.com/vcnkl/rpm/environments/spec"
	"github.com/vcnkl/rpm/models"
)

func TestRenderDeterministicOutput(t *testing.T) {
	env := &spec.ResolvedEnvironment{
		Name: "local",
		Variables: []spec.EnvVar{
			{Name: "Z", Value: "last"},
			{Name: "A", Value: "first"},
		},
		Targets: []spec.Target{
			{Ref: "z:serve", ConfigPath: "z/rpm.yml", Command: "echo z", WorkingDir: "/repo/z", ReadinessCmd: "test -f /tmp/z-ready", ExplicitEnv: []spec.EnvVar{{Name: "Z", Value: "1"}}, OverrideEnv: []spec.EnvVar{{Name: "Z", Value: "override"}}, Reload: true, Watch: spec.Watch{Roots: []string{"/repo/z"}, Ignore: []string{"tmp/**"}, Reload: true, Enabled: true}},
			{Ref: "a:serve", ConfigPath: "a/rpm.yml", Command: "echo a", WorkingDir: "/repo/a", ExplicitEnv: []spec.EnvVar{{Name: "A", Value: "1"}}, Reload: true, Watch: spec.Watch{Roots: []string{"/repo/a"}, Ignore: []string{"tmp/**"}, Reload: true, Enabled: true}},
		},
		Dependencies: []spec.Dependency{
			{Ref: "postgres", ConfigPath: "repo.yml", Name: "postgres", Image: "postgres:16", Ports: []string{"MONGO_PORT=27017", "5433:5432", "5432:5432"}, Volumes: []string{"/z", "/a"}, ReadinessCmd: "pg_isready"},
			{Ref: "redis", ConfigPath: "repo.yml", Name: "redis", Image: "redis:7"},
		},
		BeforeTargets: []spec.BeforeTarget{
			{Ref: "z:before", ConfigPath: "z/rpm.yml", Command: "echo z", WorkingDir: "/repo/z", Env: []spec.EnvVar{{Name: "Z", Value: "1"}}},
			{Ref: "a:before", ConfigPath: "a/rpm.yml", Command: "echo a", WorkingDir: "/repo/a", Env: []spec.EnvVar{{Name: "A", Value: "1"}}},
		},
		RuntimeUnits: []spec.RuntimeUnit{
			{Id: "z:serve", Kind: "target"},
			{Id: "redis", Kind: "dependency"},
			{Id: "a:before", Kind: "before"},
			{Id: "z:before", Kind: "before"},
			{Id: "a:serve", Kind: "target"},
			{Id: "postgres", Kind: "dependency"},
		},
		ReloadPolicy: models.ReloadPolicy{Enabled: true, Debounce: "100ms"},
	}

	first, err := generator.Render(env)
	require.NoError(t, err)
	second, err := generator.Render(env)
	require.NoError(t, err)

	assert.Equal(t, string(first), string(second))
	assert.Less(t, strings.Index(string(first), `ref = "postgres"`), strings.Index(string(first), `ref = "redis"`))
	assert.Less(t, strings.Index(string(first), `ref = "z:before"`), strings.Index(string(first), `ref = "a:before"`))
	assert.Less(t, strings.Index(string(first), `ref = "a:before"`), strings.Index(string(first), `ref = "a:serve"`))
	assert.Less(t, strings.Index(string(first), `ref = "a:serve"`), strings.Index(string(first), `ref = "z:serve"`))
	assert.Contains(t, string(first), `config = "repo.yml"`)
	assert.Contains(t, string(first), `config = "a/rpm.yml"`)
	assert.Contains(t, string(first), `env = {"Z": "override"}`)
	assert.NotContains(t, string(first), `echo z`)
	assert.NotContains(t, string(first), `ports =`)
	assert.NotContains(t, string(first), `readiness_cmd =`)
	assert.NotContains(t, string(first), `test -f /tmp/z-ready`)
	assert.Contains(t, string(first), `order = ["z:before", "a:before", "postgres", "redis", "z:serve", "a:serve"]`)
}

func TestRenderGoldenLocalStack(t *testing.T) {
	repoRoot := filepath.Join("..", "..", "integration", "testdata", "sample-repo")
	repo, resolved := resolveLocalStack(t, repoRoot)
	data, err := generator.Render(resolved)
	require.NoError(t, err)

	actual := strings.ReplaceAll(string(data), repo.RepoRoot(), "<repo>")
	golden, err := os.ReadFile(filepath.Join("..", "..", "integration", "testdata", "golden", "local-stack.star"))
	require.NoError(t, err)
	assert.Equal(t, string(golden), actual)
}

func TestRenderDoesNotLeakHostEnv(t *testing.T) {
	t.Setenv("RPM_SHOULD_NOT_LEAK", "host_only_value")
	_, resolved := resolveLocalStack(t, filepath.Join("..", "..", "integration", "testdata", "sample-repo"))

	data, err := generator.Render(resolved)
	require.NoError(t, err)

	assert.NotContains(t, string(data), "RPM_SHOULD_NOT_LEAK")
	assert.NotContains(t, string(data), "host_only_value")
	assert.NotContains(t, string(data), "LOCAL_SECRET")
	assert.NotContains(t, string(data), "secret_from_dotenv")
	assert.Contains(t, string(data), "apps/go-app/rpm.yml")
}

func TestWriteUsesCachePath(t *testing.T) {
	repo := newGeneratorRepo(t)
	blueprint := &models.EnvironmentBlueprint{
		Name:         "local",
		Variables:    map[string]string{},
		ReloadPolicy: models.ReloadPolicy{Enabled: true, Debounce: "100ms"},
		Targets:      []models.EnvironmentTarget{{Ref: "api:serve", Env: map[string]string{}}},
	}
	resolved, err := spec.Resolve(repo, blueprint)
	require.NoError(t, err)

	path, err := generator.Write(repo, resolved, "")
	require.NoError(t, err)

	expected := filepath.Join(repo.RepoRoot(), ".rpm", "envs", "local", "runtime.gen.star")
	assert.Equal(t, expected, path)
	assert.FileExists(t, expected)
	data, err := os.ReadFile(expected)
	require.NoError(t, err)
	assert.Contains(t, string(data), `config = "api/rpm.yml"`)
	assert.NotContains(t, string(data), repo.RepoRoot())
}

func resolveLocalStack(t *testing.T, repoRoot string) (*rootconfig.Config, *spec.ResolvedEnvironment) {
	t.Helper()
	repo := rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
	blueprint, err := envconfig.LoadBlueprint(repo, "local-stack")
	require.NoError(t, err)
	resolved, err := spec.Resolve(repo, blueprint)
	require.NoError(t, err)
	return repo, resolved
}

func newGeneratorRepo(t *testing.T) *rootconfig.Config {
	t.Helper()
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte("project:\n  name: test-project\nshell: /bin/sh\n"), 0644))
	bundleRoot := filepath.Join(repoRoot, "api")
	require.NoError(t, os.MkdirAll(bundleRoot, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(bundleRoot, "rpm.yml"), []byte("name: api\ntargets:\n  - name: serve\n    cmd: echo serve\n"), 0644))
	return rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
}
