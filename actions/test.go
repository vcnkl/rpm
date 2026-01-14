package actions

import (
	"context"
	"time"

	"github.com/vcnkl/rpm/config"
	"github.com/vcnkl/rpm/dag"
	"github.com/vcnkl/rpm/exec"
	"github.com/vcnkl/rpm/logger"
	"github.com/vcnkl/rpm/models"
)

type TestAction struct {
	config   *config.Config
	graph    *dag.Graph
	log      logger.Logger
	parallel int
}

func NewTestAction(cfg *config.Config, graph *dag.Graph, log logger.Logger, parallel int) *TestAction {
	return &TestAction{
		config:   cfg,
		graph:    graph,
		log:      log,
		parallel: parallel,
	}
}

func (a *TestAction) Execute(ctx context.Context, targetIDs []string) (*models.Result, error) {
	start := time.Now()
	result := &models.Result{}

	subgraph := a.graph.SubgraphFor(targetIDs)

	sorted, err := subgraph.TopologicalSort()
	if err != nil {
		return nil, err
	}

	executor := exec.NewParallelExecutor(a.parallel)
	results := executor.Execute(ctx, sorted, func(ctx context.Context, node *dag.Node) error {
		return a.runTest(ctx, node)
	})

	for id, err := range results {
		if err != nil {
			result.Failed = append(result.Failed, models.FailedTarget{
				ID:    id,
				Error: err,
			})
		} else {
			result.Executed = append(result.Executed, id)
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}

func (a *TestAction) runTest(ctx context.Context, node *dag.Node) error {
	target := node.Target
	targetLog := a.log.WithPrefix(target.ID())

	targetLog.Info("testing...")

	bundle := a.config.Bundles()[target.BundleName]
	env := exec.ComposeEnv(a.config.RepoRoot(), a.config.Repo(), bundle, target)
	workDir := exec.ResolveWorkDir(a.config.RepoRoot(), target)

	err := exec.RunCommand(ctx, target.Cmd, &exec.ShellOptions{
		WorkDir: workDir,
		Env:     env,
		Shell:   a.config.Repo().Shell,
		Stdout:  targetLog.Writer(),
		Stderr:  targetLog.Writer(),
	})

	if err != nil {
		targetLog.Error("test failed", logger.Err(err))
		return err
	}

	targetLog.Info("passed")
	return nil
}
