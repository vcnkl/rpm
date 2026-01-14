package actions

import (
	"context"
	"os/exec"
	"time"

	"github.com/vcnkl/rpm/config"
	"github.com/vcnkl/rpm/dag"
	rpmexec "github.com/vcnkl/rpm/exec"
	"github.com/vcnkl/rpm/logger"
	"github.com/vcnkl/rpm/models"
	"github.com/vcnkl/rpm/stores/dags"
)

type InitAction struct {
	config   *config.Config
	graph    *dag.Graph
	dagStore *dags.Store
	log      logger.Logger
	force    bool
}

func NewInitAction(cfg *config.Config, graph *dag.Graph, log logger.Logger, force bool) *InitAction {
	return &InitAction{
		config:   cfg,
		graph:    graph,
		dagStore: dags.NewStore(cfg.DagPath()),
		log:      log,
		force:    force,
	}
}

func (a *InitAction) Execute(ctx context.Context) (*models.Result, error) {
	start := time.Now()
	result := &models.Result{}

	if err := a.dagStore.Save(a.graph); err != nil {
		a.log.Warn("failed to write dag.json", logger.Err(err))
	}

	for _, dep := range a.config.Repo().Deps {
		depLog := a.log.WithPrefix(dep.Label)

		checkPassed := false
		if !a.force {
			cmd := exec.CommandContext(ctx, "sh", "-c", dep.CheckCmd)
			if err := cmd.Run(); err == nil {
				checkPassed = true
				depLog.Info("check passed")
			}
		}

		if !checkPassed || a.force {
			depLog.Info("installing...")
			cmd := exec.CommandContext(ctx, "sh", "-c", dep.InstallCmd)
			cmd.Stdout = depLog.Writer()
			cmd.Stderr = depLog.Writer()

			if err := cmd.Run(); err != nil {
				depLog.Error("install failed", logger.Err(err))
				result.Failed = append(result.Failed, models.FailedTarget{
					ID:    dep.Label,
					Error: err,
				})
				continue
			}
			depLog.Info("installed")
		}

		result.Executed = append(result.Executed, dep.Label)
	}

	selector := dag.NewSelector(a.graph, a.config.RepoRoot())
	initTargets := selector.SelectBySuffix("_init")

	subgraph := a.graph.SubgraphFor(getIDs(initTargets))
	sorted, err := subgraph.TopologicalSort()
	if err != nil {
		return nil, err
	}

	for _, node := range sorted {
		target := node.Target
		targetLog := a.log.WithPrefix(target.ID())

		targetLog.Info("running...")

		bundle := a.config.Bundles()[target.BundleName]
		env := rpmexec.ComposeEnv(a.config.RepoRoot(), a.config.Repo(), bundle, target)
		workDir := rpmexec.ResolveWorkDir(a.config.RepoRoot(), target)

		err = rpmexec.RunCommand(ctx, target.Cmd, &rpmexec.ShellOptions{
			WorkDir: workDir,
			Env:     env,
			Shell:   a.config.Repo().Shell,
			Stdout:  targetLog.Writer(),
			Stderr:  targetLog.Writer(),
		})

		if err != nil {
			targetLog.Error("failed", logger.Err(err))
			result.Failed = append(result.Failed, models.FailedTarget{
				ID:    target.ID(),
				Error: err,
			})
			continue
		}

		result.Executed = append(result.Executed, target.ID())
	}

	result.Duration = time.Since(start)

	return result, nil
}

func getIDs(nodes []*dag.Node) []string {
	ids := make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	return ids
}
