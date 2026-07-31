package actions

import (
	"context"
	"time"

	"github.com/vcwx/rpm/config"
	"github.com/vcwx/rpm/dag"
	"github.com/vcwx/rpm/exec"
	"github.com/vcwx/rpm/logger"
	"github.com/vcwx/rpm/models"
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

	err := runTargetCommand(ctx, a.config, target, targetLog.Writer())

	if err != nil {
		targetLog.Error("test failed", logger.Err(err))
		return err
	}

	targetLog.Info("passed")
	return nil
}
