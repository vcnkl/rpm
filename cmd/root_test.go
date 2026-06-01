package cmd_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcnkl/rpm/cmd"
)

func TestNewAppRegistersEnvCommand(t *testing.T) {
	app := cmd.NewApp()

	env := app.Command("env")
	require.NotNil(t, env)
	assert.Equal(t, "env", env.Name)
	assert.Nil(t, app.Command("dev"))
}
