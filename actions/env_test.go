package actions

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rootconfig "github.com/vcwx/rpm/config"
	"github.com/vcwx/rpm/environments/generator"
	envruntime "github.com/vcwx/rpm/environments/runtime"
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
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(generator.CachePath(repo, "local")), "config.yml"), []byte(`
version: 1
name: local
before:
  - api:migrate
targets:
  - ref: api:serve
dependencies:
  enabled: true
`), 0644))
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
	assert.Equal(t, []string{filepath.Join(repoRoot, "api", ".env")}, plan.BeforeTargets[0].DotenvFiles)
	assert.Equal(t, []string{filepath.Join(repoRoot, "api", ".env")}, plan.Targets[0].DotenvFiles)
}

func TestEnvUpRefreshesTargetRunOrderFromConfigRefs(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte("project:\n  name: test-project\nshell: /bin/sh\n"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "api"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "alpha-ready"), []byte{}, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "api", "rpm.yml"), []byte(`
name: api
targets:
  - name: migrate
    cmd: echo migrate
  - name: alpha
    cmd: echo alpha
    config:
      index: -1
      readiness-cmd: test -f ${REPO_ROOT}/alpha-ready
  - name: zed
    cmd: echo zed
    config:
      index: 10
`), 0644))
	repo := rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
	require.NoError(t, os.MkdirAll(filepath.Dir(generator.CachePath(repo, "local")), 0755))
	require.NoError(t, os.WriteFile(generator.CachePath(repo, "local"), []byte(`
rpm_environment(name = "local", live_reload = {"enabled": True, "debounce": "100ms"}, variables = {})
rpm_before_target(ref = "api:migrate", config = "api/rpm.yml")
rpm_target(ref = "api:alpha", config = "api/rpm.yml", env = {}, reload = True)
rpm_target(ref = "api:zed", config = "api/rpm.yml", env = {}, reload = True)
rpm_run(order = ["api:zed", "api:migrate", "api:alpha"])
`), 0644))
	var out bytes.Buffer
	var errOut bytes.Buffer
	action := NewEnvAction(repo, &out, &errOut)

	err := action.Up(context.Background(), EnvUpOptions{
		Blueprint:      "local",
		NoDeps:         true,
		NoReload:       true,
		NonInteractive: true,
	})

	require.NoError(t, err, errOut.String())
	assert.Equal(t, []string{"api:migrate", "api:alpha", "api:zed"}, processStartedRefs(t, out.String()))
}

func TestEnvUpRetainsInlineRunOrder(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte("project:\n  name: test-project\nshell: /bin/sh\n"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "api"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "zed-ready"), []byte{}, 0644))
	repo := rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
	require.NoError(t, os.MkdirAll(filepath.Dir(generator.CachePath(repo, "local")), 0755))
	require.NoError(t, os.WriteFile(generator.CachePath(repo, "local"), []byte(`
rpm_environment(name = "local", live_reload = {"enabled": True, "debounce": "100ms"}, variables = {})
rpm_target(ref = "api:alpha", command = "echo alpha", workdir = "${REPO_ROOT}/api", env = {}, reload = True)
rpm_target(ref = "api:zed", command = "echo zed", workdir = "${REPO_ROOT}/api", readiness_cmd = "test -f ${REPO_ROOT}/zed-ready", env = {}, reload = True)
rpm_run(order = ["api:zed", "api:alpha"])
`), 0644))
	var out bytes.Buffer
	var errOut bytes.Buffer
	action := NewEnvAction(repo, &out, &errOut)

	err := action.Up(context.Background(), EnvUpOptions{
		Blueprint:      "local",
		NoDeps:         true,
		NoReload:       true,
		NonInteractive: true,
	})

	require.NoError(t, err, errOut.String())
	assert.Equal(t, []string{"api:zed", "api:alpha"}, processStartedRefs(t, out.String()))
}

func TestLoadPlanResolvesDepTargetsFromConfigRefs(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte(`
project:
  name: test-project
shell: /bin/sh
`), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "api"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "api", "rpm.yml"), []byte(`
name: api
targets:
  - name: app_build
    cmd: echo build
  - name: app_serve
    cmd: echo serve
    deps:
      - :app_build
`), 0644))
	repo := rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
	require.NoError(t, os.MkdirAll(filepath.Dir(generator.CachePath(repo, "local")), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(generator.CachePath(repo, "local")), "config.yml"), []byte(`
version: 1
name: local
targets:
  - ref: api:app_serve
`), 0644))
	require.NoError(t, os.WriteFile(generator.CachePath(repo, "local"), []byte(`
rpm_environment(name = "local", live_reload = {"enabled": True, "debounce": "100ms"}, variables = {})
rpm_dep_target(ref = "api:app_build", config = "api/rpm.yml")
rpm_target(ref = "api:app_serve", config = "api/rpm.yml", env = {}, reload = True)
rpm_run(order = ["api:app_build", "api:app_serve"])
`), 0644))
	action := NewEnvAction(repo, nil, nil)

	plan, err := action.loadPlan(context.Background(), "local")

	require.NoError(t, err)
	require.Len(t, plan.DepTargets, 1)
	assert.Equal(t, "api:app_build", plan.DepTargets[0].Ref)
	assert.Equal(t, "echo build", plan.DepTargets[0].Command)
	assert.Equal(t, []string{filepath.Join(repoRoot, "api", ".env")}, plan.DepTargets[0].DotenvFiles)
	assert.Equal(t, []string{"api:app_build", "api:app_serve"}, plan.RunOrder)
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

func TestControlSenderStopInvokesRuntimeStop(t *testing.T) {
	actions := make(chan envruntime.ControlAction, 1)
	calls := 0
	sender := controlSender{
		actions: actions,
		stop: func() {
			calls++
		},
	}

	sender.Stop()

	assert.Equal(t, 1, calls)
	assert.Empty(t, actions)
}

func processStartedRefs(t *testing.T, output string) []string {
	t.Helper()
	refs := []string{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		var event envruntime.Event
		require.NoError(t, json.Unmarshal([]byte(line), &event))
		if event.Type == envruntime.EventProcessStarted {
			refs = append(refs, event.Ref)
		}
	}
	return refs
}
