package create_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rootconfig "github.com/vcnkl/rpm/config"
	envconfig "github.com/vcnkl/rpm/environments/config"
	envcreate "github.com/vcnkl/rpm/environments/create"
)

func TestRunCreateNonInteractiveWritesSortedBlueprint(t *testing.T) {
	repo := newTestRepo(t)

	err := envcreate.RunCreate(repo, envcreate.CreateOptions{
		Name:           "local-stack",
		Targets:        []string{"ts-app:web", "go-app:echo-123"},
		Dependencies:   true,
		ReloadEnabled:  true,
		NonInteractive: true,
	})

	require.NoError(t, err)
	blueprint, err := envconfig.LoadBlueprint(repo, "local-stack")
	require.NoError(t, err)
	assert.Equal(t, []string{"go-app:echo-123", "ts-app:web"}, []string{blueprint.Targets[0].Ref, blueprint.Targets[1].Ref})
	assert.Equal(t, []string{"go-app:postgres", "ts-app:mailhog"}, blueprint.DependencyPolicy.Include)
}

func TestRunCreateNonInteractiveRequiresNameAndTargets(t *testing.T) {
	repo := newTestRepo(t)

	assert.ErrorIs(t, envcreate.RunCreate(repo, envcreate.CreateOptions{NonInteractive: true}), envcreate.ErrMissingBlueprintName)
	assert.ErrorIs(t, envcreate.RunCreate(repo, envcreate.CreateOptions{Name: "local", NonInteractive: true}), envcreate.ErrMissingTargets)
}

func TestRunCreateNonInteractiveRejectsUnknownTargetReloadRef(t *testing.T) {
	repo := newTestRepo(t)

	err := envcreate.RunCreate(repo, envcreate.CreateOptions{
		Name:           "local-stack",
		Targets:        []string{"go-app:echo-123"},
		ReloadEnabled:  true,
		TargetReload:   map[string]bool{"missing:echo-123": false},
		NonInteractive: true,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown blueprint target ref")
}

func TestRunEditNonInteractiveAddsTarget(t *testing.T) {
	repo := newTestRepo(t)
	require.NoError(t, envcreate.RunCreate(repo, envcreate.CreateOptions{
		Name:           "local-stack",
		Targets:        []string{"go-app:echo-123"},
		ReloadEnabled:  true,
		NonInteractive: true,
	}))

	err := envcreate.RunEdit(repo, envcreate.EditOptions{
		Name:           "local-stack",
		AddTargets:     []string{"python-app:echo-456"},
		NonInteractive: true,
	})

	require.NoError(t, err)
	blueprint, err := envconfig.LoadBlueprint(repo, "local-stack")
	require.NoError(t, err)
	assert.Equal(t, []string{"go-app:echo-123", "python-app:echo-456"}, []string{blueprint.Targets[0].Ref, blueprint.Targets[1].Ref})
}

func TestRunCreateInteractiveWritesBlueprint(t *testing.T) {
	repo := newTestRepo(t)

	err := envcreate.RunCreate(repo, envcreate.CreateOptions{
		Name: "local-stack",
		In:   strings.NewReader("go-app:echo-123,ts-app:web\n\n\n\nn\n"),
	})

	require.NoError(t, err)
	blueprint, err := envconfig.LoadBlueprint(repo, "local-stack")
	require.NoError(t, err)
	assert.Equal(t, []string{"go-app:echo-123", "ts-app:web"}, []string{blueprint.Targets[0].Ref, blueprint.Targets[1].Ref})
	assert.True(t, blueprint.ReloadPolicy.Enabled)
	assert.False(t, blueprint.DependencyPolicy.Enabled)
}

func TestRunEditInteractiveRewritesBlueprint(t *testing.T) {
	repo := newTestRepo(t)
	require.NoError(t, envcreate.RunCreate(repo, envcreate.CreateOptions{
		Name:           "local-stack",
		Targets:        []string{"go-app:echo-123"},
		ReloadEnabled:  true,
		NonInteractive: true,
	}))

	err := envcreate.RunEdit(repo, envcreate.EditOptions{
		Name: "local-stack",
		In:   strings.NewReader("go-app:echo-123,python-app:echo-456\n\n\n\n\n\n\n"),
	})

	require.NoError(t, err)
	blueprint, err := envconfig.LoadBlueprint(repo, "local-stack")
	require.NoError(t, err)
	assert.Equal(t, []string{"go-app:echo-123", "python-app:echo-456"}, []string{blueprint.Targets[0].Ref, blueprint.Targets[1].Ref})
}

func newTestRepo(t *testing.T) *rootconfig.Config {
	t.Helper()
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte("shell: /bin/sh\n"), 0644))
	writeBundle(t, repoRoot, "go-app", "postgres")
	writeBundle(t, repoRoot, "ts-app", "mailhog")
	writeBundle(t, repoRoot, "python-app", "redis")
	return rootconfig.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))
}

func writeBundle(t *testing.T, repoRoot string, name string, dep string) {
	t.Helper()
	bundleRoot := filepath.Join(repoRoot, "apps", name)
	require.NoError(t, os.MkdirAll(bundleRoot, 0755))
	echoTarget := "echo-123"
	if name == "python-app" {
		echoTarget = "echo-456"
	}
	require.NoError(t, os.WriteFile(filepath.Join(bundleRoot, "rpm.yml"), []byte(`
name: `+name+`
env:
  dependencies:
    - name: `+dep+`
      image: postgres:16
targets:
  - name: `+echoTarget+`
    cmd: echo `+echoTarget+`
  - name: web
    cmd: echo web
`), 0644))
}
