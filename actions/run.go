package actions

import (
	"context"
	"time"

	"github.com/vcwx/rpm/config"
	"github.com/vcwx/rpm/dag"
	"github.com/vcwx/rpm/logger"
	"github.com/vcwx/rpm/models"
)

type RunAction struct {
	config *config.Config
	graph  *dag.Graph
	log    logger.Logger
}

func NewRunAction(cfg *config.Config, graph *dag.Graph, log logger.Logger) *RunAction {
	return &RunAction{
		config: cfg,
		graph:  graph,
		log:    log,
	}
}

func (a *RunAction) Execute(ctx context.Context, targetID string) (*models.Result, error) {
	start := time.Now()
	result := &models.Result{}

	if _, ok := a.graph.Nodes[targetID]; !ok {
		return nil, &dag.TargetNotFoundError{ID: targetID}
	}

	subgraph := a.graph.SubgraphFor([]string{targetID})
	sorted, err := subgraph.TopologicalSort()
	if err != nil {
		return nil, err
	}

	for _, n := range sorted {
		if err = a.runTarget(ctx, n); err != nil {
			result.Failed = append(result.Failed, models.FailedTarget{
				ID:    n.ID,
				Error: err,
			})
			result.Duration = time.Since(start)
			return result, err
		}
		result.Executed = append(result.Executed, n.ID)
	}

	result.Duration = time.Since(start)
	return result, nil
}

func (a *RunAction) runTarget(ctx context.Context, node *dag.Node) error {
	target := node.Target
	targetLog := a.log.WithPrefix(target.ID())

	targetLog.Info("running...")

	err := runTargetCommand(ctx, a.config, target, targetLog.Writer())

	if err != nil {
		targetLog.Error("failed", logger.Err(err))
		return err
	}

	targetLog.Info("completed")

	return nil
}
