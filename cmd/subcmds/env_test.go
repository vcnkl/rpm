package subcmds_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"

	"github.com/vcnkl/rpm/cmd/subcmds"
)

func TestEnvHelpListsSubcommands(t *testing.T) {
	app := cli.NewApp()
	app.Writer = new(bytes.Buffer)
	app.Commands = []*cli.Command{subcmds.EnvCmd()}

	err := app.Run([]string{"rpm", "env", "--help"})
	require.NoError(t, err)

	output := app.Writer.(*bytes.Buffer).String()
	assert.Contains(t, output, "create")
	assert.Contains(t, output, "edit")
	assert.Contains(t, output, "validate")
	assert.Contains(t, output, "render")
	assert.Contains(t, output, "up")
	assert.Contains(t, output, "down")
}

func TestEnvUpHelpListsFlags(t *testing.T) {
	app := cli.NewApp()
	app.Writer = new(bytes.Buffer)
	app.Commands = []*cli.Command{subcmds.EnvCmd()}

	err := app.Run([]string{"rpm", "env", "up", "--help"})
	require.NoError(t, err)

	output := app.Writer.(*bytes.Buffer).String()
	assert.Contains(t, output, "--non-interactive")
	assert.Contains(t, output, "--no-reload")
	assert.Contains(t, output, "--no-deps")
	assert.Contains(t, output, "--render-only")
}
