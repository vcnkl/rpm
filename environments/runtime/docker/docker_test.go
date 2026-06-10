package docker_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	envruntime "github.com/vcnkl/rpm/environments/runtime"
	"github.com/vcnkl/rpm/environments/runtime/docker"
	envstarlark "github.com/vcnkl/rpm/environments/starlark"
)

func TestUpStartsSharedDependencyWithNetworkVolumeEnvPortsAndBinds(t *testing.T) {
	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{Backend: backend, VolumeNamer: fixedVolumeNamer{
		names: map[string]string{"local-stack|postgres|/var/lib/postgresql/data": "sample-repo-postgres-local-stack-123456"},
	}})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:     "postgres",
			Name:    "postgres",
			Image:   "postgres:16",
			Env:     map[string]string{"POSTGRES_PASSWORD": "example"},
			Ports:   []string{"5432:5432"},
			Volumes: []string{"/var/lib/postgresql/data"},
		}},
	}

	startup, err := runner.Up(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"network rpm-local-stack",
		"volume sample-repo-postgres-local-stack-123456",
		"run rpm-local-stack-postgres",
	}, backend.calls)
	assert.Equal(t, []docker.ContainerSpec{{
		Name:    "rpm-local-stack-postgres",
		Image:   "postgres:16",
		Network: "rpm-local-stack",
		Env:     map[string]string{"POSTGRES_PASSWORD": "example"},
		Ports:   []string{"5432:5432"},
		Volumes: []string{"sample-repo-postgres-local-stack-123456:/var/lib/postgresql/data"},
	}}, backend.containers)
	assert.Equal(t, map[string]string{"POSTGRES_PORT": "5432"}, startup.Env)
}

func TestUpReusesExistingDockerNetwork(t *testing.T) {
	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{Backend: backend})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:   "postgres",
			Name:  "postgres",
			Image: "postgres:16",
		}},
	}

	startup, err := runner.Up(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"network rpm-local-stack",
		"run rpm-local-stack-postgres",
	}, backend.calls)
	assert.Equal(t, []docker.ContainerSpec{{
		Name:    "rpm-local-stack-postgres",
		Image:   "postgres:16",
		Network: "rpm-local-stack",
	}}, backend.containers)
	assert.Empty(t, startup.Env)
}

func TestUpAllocatesDynamicHostPortForSingleContainerPort(t *testing.T) {
	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{
		Backend: backend,
		PortAllocator: &fixedPortAllocator{
			port: 49152,
		},
	})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:   "postgres",
			Name:  "postgres",
			Image: "postgres:16",
			Ports: []string{"5432"},
		}},
	}

	startup, err := runner.Up(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []docker.ContainerSpec{{
		Name:    "rpm-local-stack-postgres",
		Image:   "postgres:16",
		Network: "rpm-local-stack",
		Ports:   []string{"49152:5432"},
	}}, backend.containers)
	assert.Equal(t, map[string]string{"POSTGRES_PORT": "49152"}, startup.Env)
}

func TestUpAllocatesDynamicHostPortsForMultipleBarePorts(t *testing.T) {
	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{
		Backend: backend,
		PortAllocator: &fixedPortAllocator{
			port: 49152,
		},
	})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:   "mailhog",
			Name:  "mailhog",
			Image: "mailhog/mailhog:v1.0.1",
			Ports: []string{"1025", "8025"},
		}},
	}

	startup, err := runner.Up(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []docker.ContainerSpec{{
		Name:    "rpm-local-stack-mailhog",
		Image:   "mailhog/mailhog:v1.0.1",
		Network: "rpm-local-stack",
		Ports:   []string{"49152:1025", "49153:8025"},
	}}, backend.containers)
	assert.Equal(t, map[string]string{
		"MAILHOG_PORT_1025": "49152",
		"MAILHOG_PORT_8025": "49153",
	}, startup.Env)
}

func TestUpMixesExplicitAndDynamicDependencyPorts(t *testing.T) {
	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{
		Backend: backend,
		PortAllocator: &fixedPortAllocator{
			port: 49153,
		},
	})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{
			{
				Ref:   "redis",
				Name:  "redis",
				Image: "redis:7",
				Ports: []string{"6379:6379"},
			},
			{
				Ref:   "mailhog",
				Name:  "mailhog",
				Image: "mailhog/mailhog:v1.0.1",
				Ports: []string{"1025"},
			},
		},
		Targets: []envstarlark.TargetProcess{
			{Ref: "python-app:echo-456"},
			{Ref: "ts-app:web"},
		},
	}

	startup, err := runner.Up(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []docker.ContainerSpec{
		{
			Name:    "rpm-local-stack-redis",
			Image:   "redis:7",
			Network: "rpm-local-stack",
			Ports:   []string{"6379:6379"},
		},
		{
			Name:    "rpm-local-stack-mailhog",
			Image:   "mailhog/mailhog:v1.0.1",
			Network: "rpm-local-stack",
			Ports:   []string{"49153:1025"},
		},
	}, backend.containers)
	assert.Equal(t, map[string]string{
		"REDIS_PORT":   "6379",
		"MAILHOG_PORT": "49153",
	}, startup.Env)
}

func TestUpUsesHostPortFromExplicitHostBinding(t *testing.T) {
	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{Backend: backend})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:   "postgres",
			Name:  "postgres",
			Image: "postgres:16",
			Ports: []string{"127.0.0.1:5433:5432"},
		}},
	}

	startup, err := runner.Up(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []docker.ContainerSpec{{
		Name:    "rpm-local-stack-postgres",
		Image:   "postgres:16",
		Network: "rpm-local-stack",
		Ports:   []string{"127.0.0.1:5433:5432"},
	}}, backend.containers)
	assert.Equal(t, map[string]string{"POSTGRES_PORT": "5433"}, startup.Env)
}

func TestUpUsesCustomPortEnvName(t *testing.T) {
	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{
		Backend: backend,
		PortAllocator: &fixedPortAllocator{
			port: 49152,
		},
	})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:   "mongodb",
			Name:  "mongodb",
			Image: "mongo:8.0.23-noble",
			Ports: []string{"MONGODB_PORT=27017"},
		}},
	}

	startup, err := runner.Up(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []docker.ContainerSpec{{
		Name:    "rpm-local-stack-mongodb",
		Image:   "mongo:8.0.23-noble",
		Network: "rpm-local-stack",
		Ports:   []string{"49152:27017"},
	}}, backend.containers)
	assert.Equal(t, map[string]string{"MONGODB_PORT": "49152"}, startup.Env)
}

func TestUpRunsDependencyReadinessWithContainerNameAndResolvedPorts(t *testing.T) {
	backend := &recordingBackend{}
	readiness := &recordingReadinessRunner{}
	runner := docker.NewCLI(docker.Options{
		Backend:         backend,
		ReadinessRunner: readiness,
		Shell:           "/bin/bash",
		PortAllocator: &fixedPortAllocator{
			port: 49152,
		},
	})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:          "postgres",
			Name:         "postgres",
			Image:        "postgres:16",
			Ports:        []string{"POSTGRES_PORT=5432"},
			ReadinessCmd: `docker exec ${DOCKER_CONTAINER_NAME} pg_isready`,
		}},
	}

	startup, err := runner.Up(context.Background(), "local-stack", plan)

	require.NoError(t, err)
	assert.Equal(t, map[string]string{"POSTGRES_PORT": "49152"}, startup.Env)
	require.Len(t, readiness.calls, 1)
	assert.Equal(t, "/bin/bash", readiness.calls[0].shell)
	assert.Equal(t, `docker exec ${DOCKER_CONTAINER_NAME} pg_isready`, readiness.calls[0].command)
	assert.Equal(t, "rpm-local-stack-postgres", readiness.calls[0].env["DOCKER_CONTAINER_NAME"])
	assert.Equal(t, "49152", readiness.calls[0].env["POSTGRES_PORT"])
	assert.Equal(t, []string{
		"network rpm-local-stack",
		"run rpm-local-stack-postgres",
	}, backend.calls)
}

func TestUpReturnsDependencyReadinessFailure(t *testing.T) {
	backend := &recordingBackend{}
	readiness := &recordingReadinessRunner{err: assert.AnError}
	runner := docker.NewCLI(docker.Options{Backend: backend, ReadinessRunner: readiness})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:          "postgres",
			Name:         "postgres",
			Image:        "postgres:16",
			ReadinessCmd: "false",
		}},
	}

	_, err := runner.Up(context.Background(), "local-stack", plan)

	require.ErrorIs(t, err, assert.AnError)
	var depErr envruntime.DependencyError
	require.True(t, errors.As(err, &depErr))
	assert.Equal(t, "postgres", depErr.Ref)
	assert.Contains(t, err.Error(), "postgres readiness check failed")
	require.Len(t, readiness.calls, 1)
	assert.Equal(t, "rpm-local-stack-postgres", readiness.calls[0].env["DOCKER_CONTAINER_NAME"])
	assert.Equal(t, []string{
		"network rpm-local-stack",
		"run rpm-local-stack-postgres",
		"remove-container rpm-local-stack-postgres",
	}, backend.calls)
}

func TestUpRejectsInvalidCustomPortEnvName(t *testing.T) {
	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{Backend: backend})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:   "mongodb",
			Name:  "mongodb",
			Image: "mongo:8.0.23-noble",
			Ports: []string{"MONGO-PORT=27017"},
		}},
	}

	_, err := runner.Up(context.Background(), "local-stack", plan)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid env name "MONGO-PORT"`)
}

func TestUpBuildsOneDockerContainerPerDependency(t *testing.T) {
	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{
		Backend: backend,
		PortAllocator: &fixedPortAllocator{
			port: 49152,
		},
		VolumeNamer: fixedVolumeNamer{
			names: map[string]string{"dev|postgres|/var/lib/postgresql/data": "sample-repo-postgres-dev-123456"},
		},
	})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{
			{
				Ref:   "mongodb",
				Name:  "mongodb",
				Image: "mongo:8.0.23-noble",
				Ports: []string{"MONGODB_PORT=27017"},
			},
			{
				Ref:     "postgres",
				Name:    "postgres",
				Image:   "postgis/postgis:17-3.5",
				Env:     map[string]string{"POSTGRES_PASSWORD": "example"},
				Ports:   []string{"POSTGRES_PORT=5432"},
				Volumes: []string{"/var/lib/postgresql/data"},
			},
			{
				Ref:   "rabbitmq",
				Name:  "rabbitmq",
				Image: "rabbitmq:4.1.3",
				Ports: []string{"RABBITMQ_PORT=5672"},
			},
			{
				Ref:   "redis",
				Name:  "redis",
				Image: "redis:7",
				Ports: []string{"REDIS_PORT=6379"},
			},
		},
	}

	startup, err := runner.Up(context.Background(), "dev", plan)
	require.NoError(t, err)

	assert.Equal(t, []docker.ContainerSpec{
		{
			Name:    "rpm-dev-mongodb",
			Image:   "mongo:8.0.23-noble",
			Network: "rpm-dev",
			Ports:   []string{"49152:27017"},
		},
		{
			Name:    "rpm-dev-postgres",
			Image:   "postgis/postgis:17-3.5",
			Network: "rpm-dev",
			Env:     map[string]string{"POSTGRES_PASSWORD": "example"},
			Ports:   []string{"49153:5432"},
			Volumes: []string{"sample-repo-postgres-dev-123456:/var/lib/postgresql/data"},
		},
		{
			Name:    "rpm-dev-rabbitmq",
			Image:   "rabbitmq:4.1.3",
			Network: "rpm-dev",
			Ports:   []string{"49154:5672"},
		},
		{
			Name:    "rpm-dev-redis",
			Image:   "redis:7",
			Network: "rpm-dev",
			Ports:   []string{"49155:6379"},
		},
	}, backend.containers)
	assert.Equal(t, map[string]string{
		"MONGODB_PORT":  "49152",
		"POSTGRES_PORT": "49153",
		"RABBITMQ_PORT": "49154",
		"REDIS_PORT":    "49155",
	}, startup.Env)
}

func TestUpCollapsesIdenticalDuplicateDependencies(t *testing.T) {
	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{Backend: backend})
	dependency := envstarlark.Dependency{
		Ref:          "rabbitmq",
		Name:         "rabbitmq",
		Image:        "rabbitmq:4.1.3",
		Ports:        []string{"5672:5672"},
		ReadinessCmd: "true",
	}
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{dependency, dependency},
	}

	startup, err := runner.Up(context.Background(), "dev", plan)

	require.NoError(t, err)
	assert.Equal(t, []string{
		"network rpm-dev",
		"run rpm-dev-rabbitmq",
	}, backend.calls)
	assert.Equal(t, []docker.ContainerSpec{{
		Name:    "rpm-dev-rabbitmq",
		Image:   "rabbitmq:4.1.3",
		Network: "rpm-dev",
		Ports:   []string{"5672:5672"},
	}}, backend.containers)
	assert.Equal(t, map[string]string{"RABBITMQ_PORT": "5672"}, startup.Env)
}

func TestUpRejectsConflictingDuplicateDependencyContainersBeforeStartup(t *testing.T) {
	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{Backend: backend})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{
			{Ref: "rabbitmq", Name: "rabbitmq", Image: "rabbitmq:4.1.3", Ports: []string{"5672:5672"}},
			{Ref: "rabbitmq-alt", Name: "rabbitmq", Image: "rabbitmq:3-management", Ports: []string{"5673:5672"}},
		},
	}

	_, err := runner.Up(context.Background(), "dev", plan)

	require.Error(t, err)
	var depErr envruntime.DependencyError
	require.True(t, errors.As(err, &depErr))
	assert.Equal(t, "rabbitmq-alt", depErr.Ref)
	assert.Contains(t, err.Error(), `duplicate dependency container "rpm-dev-rabbitmq"`)
	assert.Empty(t, backend.calls)
	assert.Empty(t, backend.containers)
}

func TestUpReturnsScopedDependencyFailureAndCleansStartedContainers(t *testing.T) {
	backend := &recordingBackend{
		runErrs: map[string]error{"rpm-dev-rabbitmq": assert.AnError},
	}
	runner := docker.NewCLI(docker.Options{Backend: backend})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{
			{Ref: "mongodb", Name: "mongodb", Image: "mongo:8.0.23-noble"},
			{Ref: "postgres", Name: "postgres", Image: "postgis/postgis:17-3.5"},
			{Ref: "rabbitmq", Name: "rabbitmq", Image: "rabbitmq:4.1.3"},
			{Ref: "redis", Name: "redis", Image: "redis:7"},
		},
	}

	_, err := runner.Up(context.Background(), "dev", plan)

	require.ErrorIs(t, err, assert.AnError)
	var depErr envruntime.DependencyError
	require.True(t, errors.As(err, &depErr))
	assert.Equal(t, "rabbitmq", depErr.Ref)
	assert.Equal(t, []string{
		"network rpm-dev",
		"run rpm-dev-mongodb",
		"run rpm-dev-postgres",
		"run rpm-dev-rabbitmq",
		"remove-container rpm-dev-postgres",
		"remove-container rpm-dev-mongodb",
	}, backend.calls)
}

func TestUpDoesNotRemovePreexistingConflictContainer(t *testing.T) {
	backend := &recordingBackend{
		runErrs: map[string]error{"rpm-dev-mongodb": assert.AnError},
	}
	runner := docker.NewCLI(docker.Options{Backend: backend})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{
			{Ref: "mongodb", Name: "mongodb", Image: "mongo:8.0.23-noble"},
			{Ref: "postgres", Name: "postgres", Image: "postgis/postgis:17-3.5"},
		},
	}

	_, err := runner.Up(context.Background(), "dev", plan)

	require.ErrorIs(t, err, assert.AnError)
	var depErr envruntime.DependencyError
	require.True(t, errors.As(err, &depErr))
	assert.Equal(t, "mongodb", depErr.Ref)
	assert.Equal(t, []string{
		"network rpm-dev",
		"run rpm-dev-mongodb",
	}, backend.calls)
}

func TestDownRemovesDependencyContainersAndNetwork(t *testing.T) {
	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{Backend: backend})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{Ref: "postgres", Name: "postgres", Image: "postgres:16"}},
	}

	err := runner.Down(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"remove-container rpm-local-stack-postgres",
		"remove-network rpm-local-stack",
	}, backend.calls)
}

func TestDownIgnoresMissingDependencyContainersAndNetwork(t *testing.T) {
	backend := &recordingBackend{missingContainers: map[string]bool{
		"rpm-local-stack-postgres": true,
	}, missingNetworks: map[string]bool{
		"rpm-local-stack": true,
	}}
	runner := docker.NewCLI(docker.Options{Backend: backend})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{Ref: "postgres", Name: "postgres", Image: "postgres:16"}},
	}

	err := runner.Down(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"remove-container rpm-local-stack-postgres",
		"remove-network rpm-local-stack",
	}, backend.calls)
}

func TestFileVolumeNamerPersistsAndPrunesBlueprintEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env-volumes.json")
	namer := docker.NewFileVolumeNamer(path, "sample-repo")

	first, err := namer.Name(context.Background(), "local-stack", "postgres", "/var/lib/postgresql/data")
	require.NoError(t, err)
	second, err := namer.Name(context.Background(), "local-stack", "postgres", "/var/lib/postgresql/data")
	require.NoError(t, err)

	assert.Equal(t, first, second)
	assert.Regexp(t, regexp.MustCompile(`^sample-repo-postgres-local-stack-[0-9]{6}$`), first)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var cache map[string]map[string]map[string]string
	require.NoError(t, json.Unmarshal(data, &cache))
	assert.Equal(t, first, cache["local-stack"]["postgres"]["/var/lib/postgresql/data"])

	require.NoError(t, docker.PruneVolumeCache(path, "local-stack"))
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	cache = nil
	require.NoError(t, json.Unmarshal(data, &cache))
	assert.NotContains(t, cache, "local-stack")
}

type recordingBackend struct {
	calls             []string
	containers        []docker.ContainerSpec
	missingContainers map[string]bool
	missingNetworks   map[string]bool
	runErrs           map[string]error
}

type readinessCall struct {
	shell   string
	command string
	env     map[string]string
}

type recordingReadinessRunner struct {
	calls []readinessCall
	err   error
}

func (r *recordingReadinessRunner) Run(_ context.Context, shell string, command string, env map[string]string) error {
	values := make(map[string]string, len(env))
	for key, value := range env {
		values[key] = value
	}
	r.calls = append(r.calls, readinessCall{shell: shell, command: command, env: values})
	return r.err
}

func (b *recordingBackend) EnsureNetwork(_ context.Context, name string) error {
	b.calls = append(b.calls, "network "+name)
	return nil
}

func (b *recordingBackend) EnsureVolume(_ context.Context, name string) error {
	b.calls = append(b.calls, "volume "+name)
	return nil
}

func (b *recordingBackend) RunContainer(_ context.Context, spec docker.ContainerSpec) error {
	b.calls = append(b.calls, "run "+spec.Name)
	b.containers = append(b.containers, spec)
	if b.runErrs[spec.Name] != nil {
		return b.runErrs[spec.Name]
	}
	return nil
}

func (b *recordingBackend) RemoveContainer(_ context.Context, name string) error {
	b.calls = append(b.calls, "remove-container "+name)
	if b.missingContainers[name] {
		return errors.New("no such container")
	}
	return nil
}

func (b *recordingBackend) RemoveNetwork(_ context.Context, name string) error {
	b.calls = append(b.calls, "remove-network "+name)
	if b.missingNetworks[name] {
		return errors.New("no such network")
	}
	return nil
}

type fixedPortAllocator struct {
	port  int
	calls int
}

func (a *fixedPortAllocator) Allocate(context.Context) (int, error) {
	port := a.port + a.calls
	a.calls++
	return port, nil
}

type fixedVolumeNamer struct {
	names map[string]string
}

func (n fixedVolumeNamer) Name(_ context.Context, blueprint string, dependency string, path string) (string, error) {
	key := blueprint + "|" + dependency + "|" + path
	if name := n.names[key]; name != "" {
		return name, nil
	}
	return "sample-repo-" + dependency + "-" + blueprint + "-123456", nil
}
