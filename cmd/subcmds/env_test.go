package subcmds_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"

	"github.com/vcnkl/rpm/cmd"
	"github.com/vcnkl/rpm/cmd/subcmds"
	rootconfig "github.com/vcnkl/rpm/config"
	envconfig "github.com/vcnkl/rpm/environments/config"
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
	assert.Contains(t, output, "prune")
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
}

func TestEnvCreateNonInteractiveCommand(t *testing.T) {
	repo := newCommandTestRepo(t)
	app := cmd.NewApp()

	err := app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "env", "create", "--non-interactive", "local-stack", "--target", "go-app:echo-123", "--target", "ts-app:web"})

	require.NoError(t, err)
	blueprint, err := envconfig.LoadBlueprint(repo, "local-stack")
	require.NoError(t, err)
	assert.Equal(t, []string{"go-app:echo-123", "ts-app:web"}, []string{blueprint.Targets[0].Ref, blueprint.Targets[1].Ref})
	assert.FileExists(t, filepath.Join(repo.RepoRoot(), ".rpm", "envs", "local-stack", "runtime.gen.star"))
}

func TestEnvEditNonInteractiveCommand(t *testing.T) {
	repo := newCommandTestRepo(t)
	app := cmd.NewApp()
	require.NoError(t, app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "env", "create", "--non-interactive", "--target", "go-app:echo-123", "local-stack"}))

	err := app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "env", "edit", "--non-interactive", "local-stack", "--add-target", "python-app:echo-456"})

	require.NoError(t, err)
	blueprint, err := envconfig.LoadBlueprint(repo, "local-stack")
	require.NoError(t, err)
	assert.Equal(t, []string{"go-app:echo-123", "python-app:echo-456"}, []string{blueprint.Targets[0].Ref, blueprint.Targets[1].Ref})
}

func TestEnvCreateNonInteractiveRejectsInvalidTargetReload(t *testing.T) {
	repo := newCommandTestRepo(t)
	app := cmd.NewApp()
	defer captureCliExit(t)()

	err := app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "env", "create", "--non-interactive", "local-stack", "--target", "go-app:echo-123", "--target-reload", "go-app:echo-123=maybe"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid boolean value")
}

func TestEnvCreateNonInteractiveRejectsUnknownTargetReloadRef(t *testing.T) {
	repo := newCommandTestRepo(t)
	app := cmd.NewApp()
	defer captureCliExit(t)()

	err := app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "env", "create", "--non-interactive", "local-stack", "--target", "go-app:echo-123", "--target-reload", "missing:echo-123=false"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown blueprint target ref")
}

func TestEnvRenderWritesCachePathAndPrintsPath(t *testing.T) {
	repo := newCommandTestRepo(t)
	app := cmd.NewApp()
	out := new(bytes.Buffer)
	app.Writer = out
	require.NoError(t, app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "env", "create", "--non-interactive", "local-stack", "--target", "go-app:echo-123"}))
	out.Reset()

	err := app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "env", "render", "local-stack"})

	require.NoError(t, err)
	expected := filepath.Join(repo.RepoRoot(), ".rpm", "envs", "local-stack", "runtime.gen.star")
	assert.Equal(t, expected, strings.TrimSpace(out.String()))
	assert.FileExists(t, expected)
}

func TestEnvRenderHonorsOutAfterBlueprint(t *testing.T) {
	repo := newCommandTestRepo(t)
	app := cmd.NewApp()
	out := new(bytes.Buffer)
	app.Writer = out
	require.NoError(t, app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "env", "create", "--non-interactive", "local-stack", "--target", "go-app:echo-123"}))
	out.Reset()
	renderedPath := filepath.Join(t.TempDir(), "local-stack.star")

	err := app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "env", "render", "local-stack", "--out", renderedPath})

	require.NoError(t, err)
	assert.Equal(t, renderedPath, strings.TrimSpace(out.String()))
	assert.FileExists(t, renderedPath)
}

func TestEnvRenderRejectsOutWithoutValueAfterBlueprint(t *testing.T) {
	repo := newCommandTestRepo(t)
	app := cmd.NewApp()
	defer captureCliExit(t)()

	err := app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "env", "render", "local-stack", "--out"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--out requires a value")
}

func TestEnvUpReadsGeneratedStarlarkOnly(t *testing.T) {
	repo := newCommandTestRepo(t)
	app := cmd.NewApp()
	out := new(bytes.Buffer)
	app.Writer = out
	require.NoError(t, app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "env", "create", "--non-interactive", "local-stack", "--target", "go-app:echo-123"}))
	configPath := filepath.Join(repo.RepoRoot(), ".rpm", "envs", "local-stack", "config.yml")
	config, err := os.ReadFile(configPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, append(config, []byte("    - ref: ts-app:web\n")...), 0644))
	out.Reset()

	err = app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "env", "up", "local-stack", "--non-interactive", "--no-deps", "--no-reload"})

	require.NoError(t, err)
	output := out.String()
	assert.Contains(t, output, `"ref":"go-app:echo-123"`)
	assert.NotContains(t, output, `"ref":"ts-app:web"`)
}

func TestEnvUpResolvesCurrentTargetConfigFromGeneratedRefs(t *testing.T) {
	repo := newCommandTestRepo(t)
	app := cmd.NewApp()
	out := new(bytes.Buffer)
	app.Writer = out
	require.NoError(t, app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "env", "create", "--non-interactive", "local-stack", "--target", "go-app:echo-123"}))
	out.Reset()
	require.NoError(t, os.WriteFile(filepath.Join(repo.RepoRoot(), "apps", "go-app", ".env.override"), []byte("UPDATED_VALUE=from-dotenv\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(repo.RepoRoot(), "apps", "go-app", "rpm.yml"), []byte("name: go-app\ntargets:\n  - name: echo-123\n    cmd: echo updated-command $UPDATED_VALUE\n    config:\n      dotenv:\n        files:\n          - .env.override\n"), 0644))

	err := app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "env", "up", "local-stack", "--non-interactive", "--no-deps", "--no-reload"})

	require.NoError(t, err)
	assert.Contains(t, out.String(), "updated-command")
	assert.Contains(t, out.String(), "from-dotenv")
}

func TestEnvUpFailsWhenGeneratedStarlarkMissing(t *testing.T) {
	repo := newCommandTestRepo(t)
	app := cmd.NewApp()
	defer captureCliExit(t)()
	require.NoError(t, app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "env", "create", "--non-interactive", "local-stack", "--target", "go-app:echo-123"}))
	require.NoError(t, os.Remove(filepath.Join(repo.RepoRoot(), ".rpm", "envs", "local-stack", "runtime.gen.star")))

	err := app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "env", "up", "local-stack", "--non-interactive", "--no-deps", "--no-reload"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "generated Starlark not found")
	assert.Contains(t, err.Error(), "rpm env render local-stack")
}

func TestEnvironmentCommandsRequireBlueprint(t *testing.T) {
	repo := newCommandTestRepo(t)
	configPath := filepath.Join(repo.RepoRoot(), "repo.yml")
	tests := []string{"edit", "validate", "render", "up", "down", "prune"}
	defer captureCliExit(t)()

	for _, command := range tests {
		t.Run(command, func(t *testing.T) {
			app := cmd.NewApp()
			err := app.Run([]string{"rpm", "--config", configPath, "env", command})

			require.Error(t, err)
			assert.Equal(t, "blueprint argument required", strings.TrimPrefix(err.Error(), "error: "))
		})
	}
}

func TestEnvironmentCommandsLoadBlueprintDuringValidation(t *testing.T) {
	repo := newCommandTestRepo(t)
	configPath := filepath.Join(repo.RepoRoot(), "repo.yml")
	tests := []struct {
		name string
		args []string
	}{
		{name: "edit", args: []string{"edit", "missing", "--non-interactive", "--reload"}},
		{name: "validate", args: []string{"validate", "missing"}},
		{name: "render", args: []string{"render", "missing"}},
	}
	defer captureCliExit(t)()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := cmd.NewApp()
			out := new(bytes.Buffer)
			errOut := new(bytes.Buffer)
			app.Writer = out
			app.ErrWriter = errOut
			args := append([]string{"rpm", "--config", configPath, "env"}, tt.args...)

			err := app.Run(args)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "unknown blueprint file")
			assert.Empty(t, out.String())
			assert.Empty(t, errOut.String())
		})
	}
}

func TestEnvDownValidatesGeneratedEnvironmentBeforeAction(t *testing.T) {
	repo := newCommandTestRepo(t)
	app := cmd.NewApp()
	out := new(bytes.Buffer)
	errOut := new(bytes.Buffer)
	app.Writer = out
	app.ErrWriter = errOut
	defer captureCliExit(t)()

	err := app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "env", "down", "local-stack"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "generated Starlark not found")
	assert.Empty(t, out.String())
	assert.Empty(t, errOut.String())
}

func TestEnvPruneDoesNotRequireGeneratedEnvironment(t *testing.T) {
	repo := newCommandTestRepo(t)
	app := cmd.NewApp()

	err := app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "env", "prune", "local-stack"})

	require.NoError(t, err)
}

func TestEnvCreateCollectsValidationErrorsWithoutOutput(t *testing.T) {
	app := cmd.NewApp()
	out := new(bytes.Buffer)
	errOut := new(bytes.Buffer)
	app.Writer = out
	app.ErrWriter = errOut
	defer captureCliExit(t)()
	missingConfig := filepath.Join(t.TempDir(), "missing.yml")

	err := app.Run([]string{"rpm", "--config", missingConfig, "env", "create", "--non-interactive", "--target-reload", "go-app:echo-123=maybe", "local-stack"})

	require.Error(t, err)
	configError := strings.Index(err.Error(), "failed to read repo.yml")
	assignmentError := strings.Index(err.Error(), "invalid boolean value")
	assert.GreaterOrEqual(t, configError, 0)
	assert.Greater(t, assignmentError, configError)
	assert.Empty(t, out.String())
	assert.Empty(t, errOut.String())
}

func TestEnvCreateCollectsMalformedFlagAndBooleanAssignments(t *testing.T) {
	repo := newCommandTestRepo(t)
	app := cmd.NewApp()
	defer captureCliExit(t)()

	err := app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "env", "create", "local-stack", "--target-reload", "missing-assignment", "--target-reload", "go-app:echo-123=maybe", "--target"})

	require.Error(t, err)
	assert.Equal(t, strings.Join([]string{
		"error: --target requires a value",
		`error: invalid boolean assignment "missing-assignment" (expected ref=true|false)`,
		`error: invalid boolean value "maybe" for go-app:echo-123`,
	}, "\n"), err.Error())
}

func newCommandTestRepo(t *testing.T) *rootconfig.Config {
	t.Helper()
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte("project:\n  name: test-project\nshell: /bin/sh\n"), 0644))
	writeCommandBundle(t, repoRoot, "go-app", []string{"echo-123"})
	writeCommandBundle(t, repoRoot, "ts-app", []string{"web"})
	writeCommandBundle(t, repoRoot, "python-app", []string{"echo-456"})
	return rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
}

func writeCommandBundle(t *testing.T, repoRoot string, name string, targets []string) {
	t.Helper()
	bundleRoot := filepath.Join(repoRoot, "apps", name)
	require.NoError(t, os.MkdirAll(bundleRoot, 0755))
	content := "name: " + name + "\ntargets:\n"
	for _, target := range targets {
		content += "  - name: " + target + "\n    cmd: echo " + target + "\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(bundleRoot, "rpm.yml"), []byte(content), 0644))
}

func captureCliExit(t *testing.T) func() {
	t.Helper()
	previous := cli.OsExiter
	cli.OsExiter = func(int) {}
	return func() {
		cli.OsExiter = previous
	}
}
