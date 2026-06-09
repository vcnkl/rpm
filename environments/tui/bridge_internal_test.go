package envtui

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	envruntime "github.com/vcnkl/rpm/environments/runtime"
)

func TestBridgeLetsChildExitAfterEventStreamCloses(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for bridge child shutdown test")
	}
	marker := filepath.Join(t.TempDir(), "closed")
	t.Setenv("RPM_ENV_TUI_TEST_MARKER", marker)
	events := make(chan envruntime.Event)
	close(events)
	bridge := &Bridge{
		nodePath: nodePath,
		script: []byte(`
import fs from 'node:fs'

const stream = fs.createReadStream('/dev/fd/3', { encoding: 'utf8' })
stream.on('data', () => {})
stream.on('end', () => {
	fs.writeFileSync(process.env.RPM_ENV_TUI_TEST_MARKER, 'closed')
})
stream.on('error', (error) => {
	fs.writeFileSync(process.env.RPM_ENV_TUI_TEST_MARKER, error.message)
	process.exitCode = 1
})
`),
		stdout: io.Discard,
		stderr: io.Discard,
	}

	err = bridge.Run(context.Background(), events, noopController{})

	require.NoError(t, err)
	data, err := os.ReadFile(marker)
	require.NoError(t, err)
	assert.Equal(t, "closed", string(data))
}

type noopController struct{}

func (noopController) Restart(context.Context, string) error {
	return nil
}

func (noopController) RestartAll(context.Context) error {
	return nil
}

func (noopController) Stop() {}
