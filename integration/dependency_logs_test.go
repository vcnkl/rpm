package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcwx/rpm/actions"
	rootconfig "github.com/vcwx/rpm/config"
	"github.com/vcwx/rpm/environments/generator"
	envruntime "github.com/vcwx/rpm/environments/runtime"
	"github.com/vcwx/rpm/environments/runtime/docker"
	envstarlark "github.com/vcwx/rpm/environments/starlark"
)

func TestIntegration_DependencyContainerLogs(t *testing.T) {
	t.Parallel()
	shouldSkip(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	blueprint := fmt.Sprintf("dependency-logs-%d", time.Now().UTC().UnixNano())
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte("project:\n  name: dependency-logs\nshell: /bin/sh\n"), 0644))
	repo := rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
	postgresReady := `timeout 90 sh -c 'until docker exec "$DOCKER_CONTAINER_NAME" pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done'`
	nginxReady := `timeout 90 sh -c 'until docker exec "$DOCKER_CONTAINER_NAME" wget -q -O /dev/null http://127.0.0.1/; do sleep 1; done; i=0; while [ "$i" -lt 25 ]; do docker exec "$DOCKER_CONTAINER_NAME" wget -q -O /dev/null http://127.0.0.1/; i=$((i+1)); done'`
	source := fmt.Sprintf(`
rpm_environment(name = %s, live_reload = {"enabled": False, "debounce": "100ms"}, variables = {})
rpm_dependency(ref = "postgres", name = "postgres", image = "postgres:17", env = {"POSTGRES_PASSWORD": "rpm-test"}, readiness_cmd = %s)
rpm_dependency(ref = "nginx", name = "nginx", image = "nginx:1.27-alpine", readiness_cmd = %s)
rpm_run(order = ["postgres", "nginx"])
`, strconv.Quote(blueprint), strconv.Quote(postgresReady), strconv.Quote(nginxReady))
	require.NoError(t, os.MkdirAll(filepath.Dir(generator.CachePath(repo, blueprint)), 0755))
	require.NoError(t, os.WriteFile(generator.CachePath(repo, blueprint), []byte(source), 0644))
	var output bytes.Buffer
	action := actions.NewEnvAction(repo, &output, &output)

	err := action.Up(ctx, actions.EnvUpOptions{Blueprint: blueprint, NoReload: true, NonInteractive: true})
	require.NoError(t, err, output.String())

	events := runtimeEvents(t, output.String())
	expected := map[string]bool{
		"postgres-stderr": false,
		"nginx-stdout":    false,
		"nginx-stderr":    false,
	}
	stoppedAt := -1
	for index, event := range events {
		if event.Type == envruntime.EventEnvironmentStopped {
			stoppedAt = index
		}
		if event.Type != envruntime.EventProcessOutput {
			continue
		}
		switch {
		case event.Ref == "postgres" && event.Stream == "stderr" && strings.Contains(event.Line, "database system is ready to accept connections"):
			expected["postgres-stderr"] = true
		case event.Ref == "nginx" && event.Stream == "stdout" && strings.Contains(event.Line, `"GET / HTTP/1.1" 200`):
			expected["nginx-stdout"] = true
		case event.Ref == "nginx" && event.Stream == "stderr" && strings.Contains(event.Line, "start worker process"):
			expected["nginx-stderr"] = true
		}
	}
	assert.True(t, allDependencyLogsSeen(expected), "%v", expected)
	require.NotEqual(t, -1, stoppedAt)
	assert.Equal(t, len(events)-1, stoppedAt)
}

func TestIntegration_DependencyLogFollowerTeardownPrecedesEnvironmentStop(t *testing.T) {
	t.Parallel()
	sink := newDependencyLogSink()
	backend := &controlledLogBackend{
		follow: func(ctx context.Context, _ io.Writer, stderr io.Writer) error {
			<-ctx.Done()
			_, err := io.WriteString(stderr, "during cancellation\n")
			return err
		},
	}
	cli := docker.NewCLI(docker.Options{Backend: backend, EventSink: sink})
	plan := dependencyLogPlan("teardown-order")
	runner := envruntime.NewRunner(envruntime.Options{DependencyRunner: cli, EventSink: sink, NoReload: true})

	require.NoError(t, runner.Up(context.Background(), plan))

	events := sink.snapshot()
	outputAt := eventPosition(events, func(event envruntime.Event) bool {
		return event.Type == envruntime.EventProcessOutput && event.Line == "during cancellation"
	})
	stoppedAt := eventPosition(events, func(event envruntime.Event) bool {
		return event.Type == envruntime.EventEnvironmentStopped
	})
	require.NotEqual(t, -1, outputAt)
	require.NotEqual(t, -1, stoppedAt)
	assert.Less(t, outputAt, stoppedAt)
}

func TestIntegration_DependencyLogFollowerReportsFailure(t *testing.T) {
	t.Parallel()
	sink := newDependencyLogSink()
	backend := &controlledLogBackend{
		follow: func(context.Context, io.Writer, io.Writer) error {
			return errors.New("attach failed")
		},
	}
	cli := docker.NewCLI(docker.Options{Backend: backend, EventSink: sink})
	plan := dependencyLogPlan("follow-failure")
	runner := envruntime.NewRunner(envruntime.Options{DependencyRunner: cli, EventSink: sink, NoReload: true})

	require.NoError(t, runner.Up(context.Background(), plan))

	events := sink.snapshot()
	diagnosticAt := eventPosition(events, func(event envruntime.Event) bool {
		return event.Type == envruntime.EventProcessOutput &&
			event.Ref == "dependency" &&
			event.Stream == "stderr" &&
			event.Line == "dependency log stream failed: attach failed"
	})
	stoppedAt := eventPosition(events, func(event envruntime.Event) bool {
		return event.Type == envruntime.EventEnvironmentStopped
	})
	require.NotEqual(t, -1, diagnosticAt)
	require.NotEqual(t, -1, stoppedAt)
	assert.Less(t, diagnosticAt, stoppedAt)
}

type dependencyLogSink struct {
	all []envruntime.Event
	mu  sync.Mutex
}

func newDependencyLogSink() *dependencyLogSink {
	return &dependencyLogSink{}
}

func (s *dependencyLogSink) Emit(event envruntime.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.all = append(s.all, event)
}

func (s *dependencyLogSink) snapshot() []envruntime.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]envruntime.Event{}, s.all...)
}

type controlledLogBackend struct {
	follow func(context.Context, io.Writer, io.Writer) error
}

func (b *controlledLogBackend) EnsureNetwork(context.Context, string) (bool, error) {
	return true, nil
}

func (b *controlledLogBackend) EnsureVolume(context.Context, string) (bool, error) {
	return true, nil
}

func (b *controlledLogBackend) EnsureContainer(context.Context, docker.ContainerSpec) (docker.ContainerState, error) {
	return docker.ContainerState{Created: true}, nil
}

func (b *controlledLogBackend) FollowContainerLogs(ctx context.Context, _ string, _ time.Time, stdout io.Writer, stderr io.Writer) error {
	return b.follow(ctx, stdout, stderr)
}

func (b *controlledLogBackend) RemoveContainer(context.Context, string) error {
	return nil
}

func (b *controlledLogBackend) RemoveNetwork(context.Context, string) error {
	return nil
}

func (b *controlledLogBackend) RemoveVolume(context.Context, string) error {
	return nil
}

func dependencyLogPlan(name string) *envstarlark.RuntimePlan {
	return &envstarlark.RuntimePlan{
		Environment:  envstarlark.Environment{Name: name},
		Dependencies: []envstarlark.Dependency{{Ref: "dependency", Name: "dependency", Image: "example:1"}},
	}
}

func eventPosition(events []envruntime.Event, matches func(envruntime.Event) bool) int {
	for index, event := range events {
		if matches(event) {
			return index
		}
	}
	return -1
}

func allDependencyLogsSeen(values map[string]bool) bool {
	for _, value := range values {
		if !value {
			return false
		}
	}
	return true
}
