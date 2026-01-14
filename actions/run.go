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

	node, ok := a.graph.Nodes[targetID]
	if !ok {
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

	_ = node
	result.Duration = time.Since(start)
	return result, nil
}

func (a *RunAction) runTarget(ctx context.Context, node *dag.Node) error {
	target := node.Target
	targetLog := a.log.WithPrefix(target.ID())

	targetLog.Info("running...")

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
		targetLog.Error("failed", logger.Err(err))
		return err
	}

	targetLog.Info("completed")

	return nil
}
