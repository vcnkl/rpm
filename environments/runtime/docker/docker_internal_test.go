package docker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	sdkclient "github.com/docker/go-sdk/client"
	"github.com/moby/moby/api/types/container"
	mobynetwork "github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSDKBackendClientDoesNotInspectDefaultDockerContext(t *testing.T) {
	configDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.json"), []byte(`{"currentContext":"default"}`), 0o644))
	t.Setenv("DOCKER_CONFIG", configDir)
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_AUTH_CONFIG", "")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cli, err := sdkBackend{}.client(ctx)
	if cli != nil {
		require.NoError(t, cli.Close())
	}
	require.Error(t, err)
	assert.Contains(t, err.Error(), "health check")
	assert.NotContains(t, err.Error(), "docker context not found")
}

func TestPublishedPortsPrefersLiveBindingsOverHostConfig(t *testing.T) {
	inspect := container.InspectResponse{
		HostConfig: &container.HostConfig{
			PortBindings: mobynetwork.PortMap{
				mobynetwork.MustParsePort("27017/tcp"): {{HostPort: "49152"}},
				mobynetwork.MustParsePort("28017/tcp"): {{HostPort: "49153"}},
			},
		},
		NetworkSettings: &container.NetworkSettings{
			Ports: mobynetwork.PortMap{
				mobynetwork.MustParsePort("27017/tcp"): {{HostPort: "49154"}},
				mobynetwork.MustParsePort("29017/tcp"): {},
			},
		},
	}

	assert.Equal(t, map[string]string{
		"27017/tcp": "49154",
		"28017/tcp": "49153",
	}, publishedPorts(inspect))
}

func TestReusablePortsMatchesDeclaredMappings(t *testing.T) {
	actual := map[string]string{"27017/tcp": "49152", "5432/tcp": "5432"}

	assert.True(t, reusablePorts(nil, nil))
	assert.True(t, reusablePorts([]portMapping{
		{Container: "27017", HostPort: "49807", Allocated: true},
		{Container: "5432", HostPort: "5432"},
	}, actual))
	assert.False(t, reusablePorts([]portMapping{
		{Container: "6379", HostPort: "49807", Allocated: true},
	}, actual), "missing container port")
	assert.False(t, reusablePorts([]portMapping{
		{Container: "5432", HostPort: "5433"},
	}, actual), "pinned host port changed")
}

func TestPortKeyNormalizesProtocol(t *testing.T) {
	assert.Equal(t, "27017/tcp", portKey("27017"))
	assert.Equal(t, "27017/tcp", portKey(" 27017/TCP "))
	assert.Equal(t, "53/udp", portKey("53/udp"))
}

type fakeSDKClient struct {
	sdkclient.SDKClient
	startErr error
	calls    []string
}

func (c *fakeSDKClient) ContainerInspect(_ context.Context, containerID string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	c.calls = append(c.calls, "inspect "+containerID)
	return client.ContainerInspectResult{Container: container.InspectResponse{
		HostConfig: &container.HostConfig{
			PortBindings: mobynetwork.PortMap{
				mobynetwork.MustParsePort("6379/tcp"): {{HostPort: "6379"}},
			},
		},
	}}, nil
}

func (c *fakeSDKClient) ContainerStart(_ context.Context, containerID string, _ client.ContainerStartOptions) (client.ContainerStartResult, error) {
	c.calls = append(c.calls, "start "+containerID)
	return client.ContainerStartResult{}, c.startErr
}

func (c *fakeSDKClient) ContainerRemove(_ context.Context, containerID string, _ client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
	c.calls = append(c.calls, "remove "+containerID)
	return client.ContainerRemoveResult{}, nil
}

func TestReuseExistingContainerStartsStoppedContainer(t *testing.T) {
	cli := &fakeSDKClient{}
	found := &container.Summary{ID: "abc123", State: "exited"}

	state, reused, err := reuseExistingContainer(context.Background(), cli, found, ContainerSpec{Name: "rpm-dev-redis"})

	require.NoError(t, err)
	assert.True(t, reused)
	assert.Equal(t, map[string]string{"6379/tcp": "6379"}, state.Ports)
	assert.Equal(t, []string{"inspect abc123", "start abc123"}, cli.calls)
}

func TestReuseExistingContainerRemovesContainerThatCannotStart(t *testing.T) {
	cli := &fakeSDKClient{startErr: errors.New("failed to bind host port: address already in use")}
	found := &container.Summary{ID: "abc123", State: "created"}

	state, reused, err := reuseExistingContainer(context.Background(), cli, found, ContainerSpec{Name: "rpm-dev-redis"})

	require.NoError(t, err)
	assert.False(t, reused)
	assert.Empty(t, state.Ports)
	assert.Equal(t, []string{"inspect abc123", "start abc123", "remove abc123"}, cli.calls)
}

func TestReuseExistingContainerReportsRunningContainerWithoutStart(t *testing.T) {
	cli := &fakeSDKClient{}
	found := &container.Summary{ID: "abc123", State: "running"}

	state, reused, err := reuseExistingContainer(context.Background(), cli, found, ContainerSpec{Name: "rpm-dev-redis"})

	require.NoError(t, err)
	assert.True(t, reused)
	assert.Equal(t, map[string]string{"6379/tcp": "6379"}, state.Ports)
	assert.Equal(t, []string{"inspect abc123"}, cli.calls)
}
