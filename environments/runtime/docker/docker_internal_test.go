package docker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
