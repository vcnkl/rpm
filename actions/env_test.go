package actions

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rootconfig "github.com/vcnkl/rpm/config"
	"github.com/vcnkl/rpm/environments/generator"
)

func TestLoadPlanResolvesRequiredDependenciesFromConfigRefs(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte(`
project:
  name: test-project
shell: /bin/sh
env:
  deps:
    - name: postgres
      image: postgres:16
      ports:
        - POSTGRES_PORT=5432
`), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "api"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "api", "rpm.yml"), []byte(`
name: api
env:
  deps:
    - postgres
targets:
  - name: migrate
    cmd: echo "$POSTGRES_PORT"
  - name: serve
    cmd: echo serve
`), 0644))
	repo := rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
	require.NoError(t, os.MkdirAll(filepath.Dir(generator.CachePath(repo, "local")), 0755))
	require.NoError(t, os.WriteFile(generator.CachePath(repo, "local"), []byte(`
rpm_environment(name = "local", live_reload = {"enabled": True, "debounce": "100ms"}, variables = {})
rpm_before_target(ref = "api:migrate", config = "api/rpm.yml")
rpm_target(ref = "api:serve", config = "api/rpm.yml", env = {}, reload = True)
rpm_run(order = ["api:migrate", "api:serve"])
`), 0644))
	action := NewEnvAction(repo, nil, nil)

	plan, err := action.loadPlan(context.Background(), "local")

	require.NoError(t, err)
	require.Len(t, plan.Dependencies, 1)
	assert.Equal(t, "postgres", plan.Dependencies[0].Ref)
	assert.Equal(t, []string{"POSTGRES_PORT=5432"}, plan.Dependencies[0].Ports)
	assert.Equal(t, "echo \"$POSTGRES_PORT\"", plan.BeforeTargets[0].Command)
}

func TestLoadPlanResolvesExplicitDependencyConfigRefs(t *testing.T) {
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
    - name: rabbitmq
      image: rabbitmq:3-management
      ports:
        - RABBITMQ_PORT=5672
    - name: redis
      image: redis:7
      ports:
        - REDIS_PORT=6379
`), 0644))
	repo := rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
	require.NoError(t, os.MkdirAll(filepath.Dir(generator.CachePath(repo, "local")), 0755))
	require.NoError(t, os.WriteFile(generator.CachePath(repo, "local"), []byte(`
rpm_environment(name = "local", live_reload = {"enabled": True, "debounce": "100ms"}, variables = {})
rpm_dependency(ref = "mongodb", config = "repo.yml")
rpm_dependency(ref = "postgres", config = "repo.yml")
rpm_dependency(ref = "rabbitmq", config = "repo.yml")
rpm_dependency(ref = "redis", config = "repo.yml")
rpm_run(order = ["mongodb", "postgres", "rabbitmq", "redis"])
`), 0644))
	action := NewEnvAction(repo, nil, nil)

	plan, err := action.loadPlan(context.Background(), "local")

	require.NoError(t, err)
	require.Len(t, plan.Dependencies, 4)
	assert.Equal(t, "mongodb", plan.Dependencies[0].Ref)
	assert.Equal(t, "mongo:8.0.23-noble", plan.Dependencies[0].Image)
	assert.Equal(t, []string{"MONGODB_PORT=27017"}, plan.Dependencies[0].Ports)
	assert.Equal(t, "postgres", plan.Dependencies[1].Ref)
	assert.Equal(t, "postgres:16", plan.Dependencies[1].Image)
	assert.Equal(t, map[string]string{"POSTGRES_PASSWORD": "example"}, plan.Dependencies[1].Env)
	assert.Equal(t, []string{"POSTGRES_PORT=5432"}, plan.Dependencies[1].Ports)
	assert.Equal(t, "rabbitmq", plan.Dependencies[2].Ref)
	assert.Equal(t, "rabbitmq:3-management", plan.Dependencies[2].Image)
	assert.Equal(t, []string{"RABBITMQ_PORT=5672"}, plan.Dependencies[2].Ports)
	assert.Equal(t, "redis", plan.Dependencies[3].Ref)
	assert.Equal(t, "redis:7", plan.Dependencies[3].Image)
	assert.Equal(t, []string{"REDIS_PORT=6379"}, plan.Dependencies[3].Ports)
	assert.Equal(t, []string{"mongodb", "postgres", "rabbitmq", "redis"}, plan.RunOrder)
}
