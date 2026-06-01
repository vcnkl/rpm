package actions_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vcnkl/rpm/actions"
	rootconfig "github.com/vcnkl/rpm/config"
)

func TestEnvUpNonInteractiveBypassesNodeTUI(t *testing.T) {
	repo := rootconfig.NewConfigWithRepoFile(filepath.Join("..", "integration", "testdata", "sample-repo", "repo.yml"))
	action := actions.NewEnvAction(repo, nil, nil)

	err := action.Up(context.Background(), actions.EnvUpOptions{
		Blueprint:      "local-stack",
		NoDeps:         true,
		NoReload:       true,
		NonInteractive: true,
	})

	require.NoError(t, err)
}
