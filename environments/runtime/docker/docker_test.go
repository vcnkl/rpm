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
			Ports: []string{"MONGO_PORT=27017"},
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
	assert.Equal(t, map[string]string{"MONGO_PORT": "49152"}, startup.Env)
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
	runner := docker.NewCLI(docker.Options{Backend: backend})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:   "redis",
			Name:  "redis",
			Image: "redis:7",
		}},
		Targets: []envstarlark.TargetProcess{
			{Ref: "api:serve"},
			{Ref: "api:worker"},
			{Ref: "web:serve"},
		},
	}

	startup, err := runner.Up(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []docker.ContainerSpec{
		{Name: "rpm-local-stack-redis", Image: "redis:7", Network: "rpm-local-stack"},
	}, backend.containers)
	assert.Empty(t, startup.Env)
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
