package docker_test

import (
	"context"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcnkl/rpm/environments/runtime/docker"
	envstarlark "github.com/vcnkl/rpm/environments/starlark"
)

func TestUpStartsSharedDependencyWithNetworkVolumeEnvPortsAndBinds(t *testing.T) {
	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{Backend: backend})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:     "api:postgres",
			Name:    "postgres",
			Image:   "postgres:16",
			Mode:    "shared",
			Env:     map[string]string{"POSTGRES_PASSWORD": "example"},
			Ports:   []string{"5432:5432"},
			Volumes: []string{"postgres-data:/var/lib/postgresql/data"},
		}},
	}

	err := runner.Up(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"network rpm-local-stack",
		"volume postgres-data",
		"run rpm-local-stack-api-postgres",
	}, backend.calls)
	assert.Equal(t, []docker.ContainerSpec{{
		Name:    "rpm-local-stack-api-postgres",
		Image:   "postgres:16",
		Network: "rpm-local-stack",
		Env:     map[string]string{"POSTGRES_PASSWORD": "example"},
		Ports:   []string{"5432:5432"},
		Volumes: []string{"postgres-data:/var/lib/postgresql/data"},
	}}, backend.containers)
}

func TestUpReusesExistingDockerNetwork(t *testing.T) {
	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{Backend: backend})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:   "api:postgres",
			Image: "postgres:16",
			Mode:  "shared",
		}},
	}

	err := runner.Up(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"network rpm-local-stack",
		"run rpm-local-stack-api-postgres",
	}, backend.calls)
	assert.Equal(t, []docker.ContainerSpec{{
		Name:    "rpm-local-stack-api-postgres",
		Image:   "postgres:16",
		Network: "rpm-local-stack",
	}}, backend.containers)
}

func TestUpAllocatesDynamicHostPortForSingleContainerPort(t *testing.T) {
	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{
		Backend: backend,
		PortAllocator: fixedPortAllocator{
			port: 49152,
		},
	})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:   "api:postgres",
			Name:  "postgres",
			Image: "postgres:16",
			Mode:  "shared",
			Ports: []string{"5432"},
		}},
	}

	err := runner.Up(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []docker.ContainerSpec{{
		Name:    "rpm-local-stack-api-postgres",
		Image:   "postgres:16",
		Network: "rpm-local-stack",
		Ports:   []string{"49152:5432"},
	}}, backend.containers)
}

func TestUpPreservesMultipleBarePorts(t *testing.T) {
	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{
		Backend: backend,
		PortAllocator: fixedPortAllocator{
			port: 49152,
		},
	})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:   "api:mailhog",
			Name:  "mailhog",
			Image: "mailhog/mailhog:v1.0.1",
			Mode:  "shared",
			Ports: []string{"1025", "8025"},
		}},
	}

	err := runner.Up(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []docker.ContainerSpec{{
		Name:    "rpm-local-stack-api-mailhog",
		Image:   "mailhog/mailhog:v1.0.1",
		Network: "rpm-local-stack",
		Ports:   []string{"1025", "8025"},
	}}, backend.containers)
}

func TestUpMixesExplicitAndDynamicDependencyPorts(t *testing.T) {
	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{
		Backend: backend,
		PortAllocator: fixedPortAllocator{
			port: 49153,
		},
	})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{
			{
				Ref:   "python-app:redis",
				Name:  "redis",
				Image: "redis:7",
				Mode:  "dedicated",
				Ports: []string{"6379:6379"},
			},
			{
				Ref:   "ts-app:mailhog",
				Name:  "mailhog",
				Image: "mailhog/mailhog:v1.0.1",
				Mode:  "shared",
				Ports: []string{"1025"},
			},
		},
		Targets: []envstarlark.TargetProcess{
			{Ref: "python-app:echo-456"},
			{Ref: "ts-app:web"},
		},
	}

	err := runner.Up(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []docker.ContainerSpec{
		{
			Name:    "rpm-local-stack-python-app-redis-python-app-echo-456",
			Image:   "redis:7",
			Network: "rpm-local-stack",
			Ports:   []string{"6379:6379"},
		},
		{
			Name:    "rpm-local-stack-ts-app-mailhog",
			Image:   "mailhog/mailhog:v1.0.1",
			Network: "rpm-local-stack",
			Ports:   []string{"49153:1025"},
		},
	}, backend.containers)
}

func TestUpBuildsDedicatedDockerContainersPerTarget(t *testing.T) {
	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{Backend: backend})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:   "api:redis",
			Name:  "redis",
			Image: "redis:7",
			Mode:  "dedicated",
		}},
		Targets: []envstarlark.TargetProcess{
			{Ref: "api:serve"},
			{Ref: "api:worker"},
			{Ref: "web:serve"},
		},
	}

	err := runner.Up(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []docker.ContainerSpec{
		{Name: "rpm-local-stack-api-redis-api-serve", Image: "redis:7", Network: "rpm-local-stack"},
		{Name: "rpm-local-stack-api-redis-api-worker", Image: "redis:7", Network: "rpm-local-stack"},
	}, backend.containers)
}

func TestDownRemovesDependencyContainersAndNetwork(t *testing.T) {
	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{Backend: backend})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{Ref: "api:postgres", Image: "postgres:16", Mode: "shared"}},
	}

	err := runner.Down(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"remove-container rpm-local-stack-api-postgres",
		"remove-network rpm-local-stack",
	}, backend.calls)
}

func TestDownIgnoresMissingDependencyContainersAndNetwork(t *testing.T) {
	backend := &recordingBackend{missingContainers: map[string]bool{
		"rpm-local-stack-api-postgres": true,
	}, missingNetworks: map[string]bool{
		"rpm-local-stack": true,
	}}
	runner := docker.NewCLI(docker.Options{Backend: backend})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{Ref: "api:postgres", Image: "postgres:16", Mode: "shared"}},
	}

	err := runner.Down(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"remove-container rpm-local-stack-api-postgres",
		"remove-network rpm-local-stack",
	}, backend.calls)
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
	port int
}

func (a fixedPortAllocator) Allocate(context.Context) (int, error) {
	return a.port, nil
}
