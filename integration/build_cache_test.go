package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcwx/rpm/actions"
	"github.com/vcwx/rpm/config"
	"github.com/vcwx/rpm/dag"
	"github.com/vcwx/rpm/logger"
	"github.com/vcwx/rpm/models"
	"github.com/vcwx/rpm/stores/builds"
)

func TestIntegration_BuildCacheReportsSkippedOnSecondRun(t *testing.T) {
	shouldSkip(t)

	repoRoot := t.TempDir()
	bundleDir := filepath.Join(repoRoot, "app")
	srcDir := filepath.Join(bundleDir, "src")
	require.NoError(t, os.MkdirAll(srcDir, 0755))

	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte(
		"project:\n  name: cache-repo\nshell: /bin/sh\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(bundleDir, "rpm.yml"), []byte(
		"name: app\ntargets:\n  - name: artifact_build\n    in:\n      - \"src/*.txt\"\n    out:\n      - out.bin\n    cmd: cat src/*.txt > out.bin\n    config:\n      working_dir: local\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "main.txt"), []byte("source"), 0644))

	targetID := "app:artifact_build"

	firstResult := runBuild(t, repoRoot, targetID)
	assert.Equal(t, []string{targetID}, firstResult.Executed, "first run must execute the target")
	assert.Empty(t, firstResult.Skipped)
	assert.Empty(t, firstResult.Failed)

	secondResult := runBuild(t, repoRoot, targetID)
	assert.Equal(t, []string{targetID}, secondResult.Skipped, "second run must report the cached target as skipped")
	assert.Empty(t, secondResult.Executed)
	assert.Empty(t, secondResult.Failed)
}

func runBuild(t *testing.T, repoRoot, targetID string) *models.Result {
	t.Helper()

	cfg := config.NewConfigWithRepoFile(filepath.Join(repoRoot, "repo.yml"))

	graph := dag.NewGraph()
	for _, bundle := range cfg.Bundles() {
		for _, target := range bundle.Targets {
			graph.AddTarget(target)
		}
	}
	require.NoError(t, graph.Resolve(cfg.Bundles()))

	store := builds.NewStore(cfg.BuildsPath())
	require.NoError(t, store.Load())

	log := logger.New(logger.ErrorLevel)
	action := actions.NewBuildAction(cfg, graph, store, log, 1, false)

	result, err := action.Execute(context.Background(), []string{targetID})
	require.NoError(t, err)
	return result
}
