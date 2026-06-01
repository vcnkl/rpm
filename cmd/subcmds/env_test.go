package subcmds_test

import (
	"bytes"
	"os"
	"path/filepath"
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

func TestEnvCreateNonInteractiveCommand(t *testing.T) {
	repo := newCommandTestRepo(t)
	app := cmd.NewApp()

	err := app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "env", "create", "--non-interactive", "local-stack", "--target", "go-app:serve", "--target", "ts-app:web"})

	require.NoError(t, err)
	blueprint, err := envconfig.LoadBlueprint(repo, "local-stack")
	require.NoError(t, err)
	assert.Equal(t, []string{"go-app:serve", "ts-app:web"}, []string{blueprint.Targets[0].Ref, blueprint.Targets[1].Ref})
}

func TestEnvEditNonInteractiveCommand(t *testing.T) {
	repo := newCommandTestRepo(t)
	app := cmd.NewApp()
	require.NoError(t, app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "env", "create", "--non-interactive", "--target", "go-app:serve", "local-stack"}))

	err := app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "env", "edit", "--non-interactive", "local-stack", "--add-target", "python-app:serve"})

	require.NoError(t, err)
	blueprint, err := envconfig.LoadBlueprint(repo, "local-stack")
	require.NoError(t, err)
	assert.Equal(t, []string{"go-app:serve", "python-app:serve"}, []string{blueprint.Targets[0].Ref, blueprint.Targets[1].Ref})
}

func TestEnvCreateNonInteractiveRejectsInvalidTargetReload(t *testing.T) {
	repo := newCommandTestRepo(t)
	app := cmd.NewApp()
	defer captureCliExit(t)()

	err := app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "env", "create", "--non-interactive", "local-stack", "--target", "go-app:serve", "--target-reload", "go-app:serve=maybe"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid boolean value")
}

func TestEnvCreateNonInteractiveRejectsUnknownTargetReloadRef(t *testing.T) {
	repo := newCommandTestRepo(t)
	app := cmd.NewApp()
	defer captureCliExit(t)()

	err := app.Run([]string{"rpm", "--config", filepath.Join(repo.RepoRoot(), "repo.yml"), "env", "create", "--non-interactive", "local-stack", "--target", "go-app:serve", "--target-reload", "missing:serve=false"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown blueprint target ref")
}

func newCommandTestRepo(t *testing.T) *rootconfig.Config {
	t.Helper()
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte("shell: /bin/sh\n"), 0644))
	writeCommandBundle(t, repoRoot, "go-app", []string{"serve"})
	writeCommandBundle(t, repoRoot, "ts-app", []string{"web"})
	writeCommandBundle(t, repoRoot, "python-app", []string{"serve"})
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
