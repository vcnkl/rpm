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
