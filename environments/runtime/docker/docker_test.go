package docker_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcnkl/rpm/environments/runtime/docker"
	envstarlark "github.com/vcnkl/rpm/environments/starlark"
)

func TestUpBuildsDockerCommands(t *testing.T) {
	commands := &recordingCommands{}
	runner := docker.NewCLI(docker.Options{CommandRunner: commands})
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
		"docker network create rpm-local-stack",
		"docker volume create postgres-data",
		"docker run --detach --name rpm-local-stack-api-postgres --network rpm-local-stack --env POSTGRES_PASSWORD=example --publish 5432:5432 --volume postgres-data:/var/lib/postgresql/data postgres:16",
	}, commands.lines)
}

func TestUpAllocatesDynamicHostPortForSingleContainerPort(t *testing.T) {
	commands := &recordingCommands{}
	runner := docker.NewCLI(docker.Options{
		CommandRunner: commands,
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

	assert.Equal(t, []string{
		"docker network create rpm-local-stack",
		"docker run --detach --name rpm-local-stack-api-postgres --network rpm-local-stack --publish 49152:5432 postgres:16",
	}, commands.lines)
}

func TestUpPreservesMultipleBarePorts(t *testing.T) {
	commands := &recordingCommands{}
	runner := docker.NewCLI(docker.Options{
		CommandRunner: commands,
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

	assert.Equal(t, []string{
		"docker network create rpm-local-stack",
		"docker run --detach --name rpm-local-stack-api-mailhog --network rpm-local-stack --publish 1025 --publish 8025 mailhog/mailhog:v1.0.1",
	}, commands.lines)
}

func TestUpMixesExplicitAndDynamicDependencyPorts(t *testing.T) {
	commands := &recordingCommands{}
	runner := docker.NewCLI(docker.Options{
		CommandRunner: commands,
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

	assert.Equal(t, []string{
		"docker network create rpm-local-stack",
		"docker run --detach --name rpm-local-stack-python-app-redis-python-app-echo-456 --network rpm-local-stack --publish 6379:6379 redis:7",
		"docker run --detach --name rpm-local-stack-ts-app-mailhog --network rpm-local-stack --publish 49153:1025 mailhog/mailhog:v1.0.1",
	}, commands.lines)
}

func TestUpBuildsDedicatedDockerCommandsPerTarget(t *testing.T) {
	commands := &recordingCommands{}
	runner := docker.NewCLI(docker.Options{CommandRunner: commands})
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

	assert.Equal(t, []string{
		"docker network create rpm-local-stack",
		"docker run --detach --name rpm-local-stack-api-redis-api-serve --network rpm-local-stack redis:7",
		"docker run --detach --name rpm-local-stack-api-redis-api-worker --network rpm-local-stack redis:7",
	}, commands.lines)
}

func TestDownRemovesDependencyContainersAndNetwork(t *testing.T) {
	commands := &recordingCommands{}
	runner := docker.NewCLI(docker.Options{CommandRunner: commands})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{Ref: "api:postgres", Image: "postgres:16", Mode: "shared"}},
	}

	err := runner.Down(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"docker rm -f rpm-local-stack-api-postgres",
		"docker network rm rpm-local-stack",
	}, commands.lines)
}

type recordingCommands struct {
	lines []string
}

func (r *recordingCommands) Run(_ context.Context, name string, args ...string) error {
	r.lines = append(r.lines, strings.Join(append([]string{name}, args...), " "))
	return nil
}

type fixedPortAllocator struct {
	port int
}

func (a fixedPortAllocator) Allocate(context.Context) (int, error) {
	return a.port, nil
}
