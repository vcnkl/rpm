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
			{Ref: "z:serve", Command: "echo z", WorkingDir: "/repo/z", ExplicitEnv: []spec.EnvVar{{Name: "Z", Value: "1"}}, Reload: true, Watch: spec.Watch{Roots: []string{"/repo/z"}, Ignore: []string{"tmp/**"}, Reload: true, Enabled: true}},
			{Ref: "a:serve", Command: "echo a", WorkingDir: "/repo/a", ExplicitEnv: []spec.EnvVar{{Name: "A", Value: "1"}}, Reload: true, Watch: spec.Watch{Roots: []string{"/repo/a"}, Ignore: []string{"tmp/**"}, Reload: true, Enabled: true}},
		},
		Dependencies: []spec.Dependency{
			{Ref: "z:postgres", Name: "postgres", Image: "postgres:16", Mode: models.DependencyInstanceModeShared, Ports: []string{"5433:5432", "5432:5432"}, Volumes: []string{"z:/z", "a:/a"}},
			{Ref: "a:redis", Name: "redis", Image: "redis:7", Mode: models.DependencyInstanceModeDedicated},
		},
		RuntimeUnits: []spec.RuntimeUnit{
			{Id: "z:serve", Kind: "target"},
			{Id: "a:redis", Kind: "dependency"},
			{Id: "a:serve", Kind: "target"},
			{Id: "z:postgres", Kind: "dependency"},
		},
		ReloadPolicy: models.ReloadPolicy{Enabled: true, Debounce: "100ms"},
	}

	first, err := generator.Render(env)
	require.NoError(t, err)
	second, err := generator.Render(env)
	require.NoError(t, err)

	assert.Equal(t, string(first), string(second))
	assert.Less(t, strings.Index(string(first), `ref = "a:redis"`), strings.Index(string(first), `ref = "z:postgres"`))
	assert.Less(t, strings.Index(string(first), `ref = "a:serve"`), strings.Index(string(first), `ref = "z:serve"`))
	assert.Contains(t, string(first), `ports = ["5432:5432", "5433:5432"]`)
	assert.Contains(t, string(first), `volumes = ["a:/a", "z:/z"]`)
	assert.Contains(t, string(first), `order = ["a:redis", "z:postgres", "a:serve", "z:serve"]`)
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
	assert.Contains(t, string(data), "LOCAL_SECRET")
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

	expected := filepath.Join(repo.RepoRoot(), ".rpm", "cache", "starlark", "local", "env.star")
	assert.Equal(t, expected, path)
	assert.FileExists(t, expected)
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
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte("shell: /bin/sh\n"), 0644))
	bundleRoot := filepath.Join(repoRoot, "api")
	require.NoError(t, os.MkdirAll(bundleRoot, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(bundleRoot, "rpm.yml"), []byte("name: api\ntargets:\n  - name: serve\n    cmd: echo serve\n"), 0644))
	return rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
}
