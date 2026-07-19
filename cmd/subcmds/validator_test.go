package subcmds_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"

	"github.com/vcnkl/rpm/cmd"
)

func TestSingleTargetCommandsIgnoreAdditionalArguments(t *testing.T) {
	repo := newCommandTestRepo(t)
	repoFile := filepath.Join(repo.RepoRoot(), "repo.yml")
	tests := []struct {
		name string
		args []string
	}{
		{name: "run", args: []string{"run", "go-app:echo-123", "invalid-name:missing"}},
		{name: "graph", args: []string{"graph", "--format", "json", "go-app:echo-123", "invalid-name:missing"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := cmd.NewApp()
			args := append([]string{"rpm", "--config", repoFile}, tt.args...)

			require.NoError(t, app.Run(args))
		})
	}
}

func TestCommandsRejectUnknownTargets(t *testing.T) {
	repo := newCommandTestRepo(t)
	repoFile := filepath.Join(repo.RepoRoot(), "repo.yml")
	tests := []struct {
		name string
		args []string
	}{
		{name: "build", args: []string{"build", "go-app:missing"}},
		{name: "test", args: []string{"test", "go-app:missing"}},
		{name: "dev", args: []string{"dev", "go-app:missing"}},
		{name: "run", args: []string{"run", "go-app:missing"}},
		{name: "graph", args: []string{"graph", "go-app:missing"}},
	}
	defer captureCliExit(t)()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := cmd.NewApp()
			args := append([]string{"rpm", "--config", repoFile}, tt.args...)

			err := app.Run(args)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "error: target not found: go-app:missing")
			assertExitCodeOne(t, err)
		})
	}
}

func TestGraphRejectsInvalidFormat(t *testing.T) {
	repo := newCommandTestRepo(t)
	app := cmd.NewApp()
	defer captureCliExit(t)()

	err := app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "graph", "--format", "yaml"})

	require.Error(t, err)
	assert.Equal(t, `error: invalid output format "yaml" (expected text, json, dot)`, err.Error())
	assertExitCodeOne(t, err)
}

func TestCommandValidationOrdersBundlesBeforeTargets(t *testing.T) {
	repo := newCommandTestRepo(t)
	app := cmd.NewApp()
	defer captureCliExit(t)()

	err := app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "build", "missing:build", "go-app:missing"})

	require.Error(t, err)
	assert.Equal(t, "error: bundle \"missing\" not found in test-project\nerror: target not found: go-app:missing", err.Error())
	assertExitCodeOne(t, err)
}

func TestCommandValidationRecoversConfigPanicWithoutOutput(t *testing.T) {
	app := cmd.NewApp()
	out := new(bytes.Buffer)
	errOut := new(bytes.Buffer)
	app.Writer = out
	app.ErrWriter = errOut
	defer captureCliExit(t)()

	err := app.Run([]string{"rpm", "--config", filepath.Join(t.TempDir(), "missing.yml"), "run"})

	require.Error(t, err)
	lines := strings.Split(err.Error(), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, "error: target argument required", lines[0])
	assert.Contains(t, lines[1], "error: failed to read repo.yml")
	assert.NotContains(t, err.Error(), "goroutine")
	assert.NotContains(t, err.Error(), "runtime/debug.Stack")
	assert.Empty(t, out.String())
	assert.Empty(t, errOut.String())
	assertExitCodeOne(t, err)
}

func assertExitCodeOne(t *testing.T, err error) {
	t.Helper()
	exitErr, ok := err.(cli.ExitCoder)
	require.True(t, ok)
	assert.Equal(t, 1, exitErr.ExitCode())
}
