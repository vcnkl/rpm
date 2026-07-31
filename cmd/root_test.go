package cmd_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcwx/rpm/cmd"
	"github.com/vcwx/rpm/version"
)

func TestNewAppRegistersEnvCommand(t *testing.T) {
	app := cmd.NewApp()

	env := app.Command("env")
	require.NotNil(t, env)
	assert.Equal(t, "env", env.Name)
	assert.NotNil(t, app.Command("dev"))
}

func TestNewAppUsesInjectedVersion(t *testing.T) {
	app := cmd.NewApp()

	assert.Equal(t, version.Version, app.Version)
}
