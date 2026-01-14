package actions

import (
	"context"
	"sync"
	"time"

	"github.com/vcnkl/rpm/config"
	"github.com/vcnkl/rpm/dag"
	"github.com/vcnkl/rpm/exec"
	"github.com/vcnkl/rpm/logger"
	"github.com/vcnkl/rpm/models"
	"github.com/vcnkl/rpm/stores/builds"
)

type BuildAction struct {
	config    *config.Config
	graph     *dag.Graph
	store     *builds.Store
	validator *builds.Validator
	log       logger.Logger
	parallel  int
	force     bool
	rebuilt   map[string]bool
	rebuiltMu sync.RWMutex
}

func NewBuildAction(cfg *config.Config, graph *dag.Graph, store *builds.Store, log logger.Logger, parallel int, force bool) *BuildAction {
	return &BuildAction{
		config:    cfg,
		graph:     graph,
		store:     store,
		validator: builds.NewValidator(cfg.RepoRoot(), store),
		log:       log,
		parallel:  parallel,
		force:     force,
		rebuilt:   make(map[string]bool),
	}
}

func (a *BuildAction) Execute(ctx context.Context, targetIDs []string) (*models.Result, error) {
	start := time.Now()
	result := &models.Result{}

	subgraph := a.graph.SubgraphFor(targetIDs)

	sorted, err := subgraph.TopologicalSort()
	if err != nil {
		return nil, err
	}

	executor := exec.NewParallelExecutor(a.parallel)
	results := executor.Execute(ctx, sorted, func(ctx context.Context, node *dag.Node) error {
		return a.buildTarget(ctx, node)
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

func (a *BuildAction) buildTarget(ctx context.Context, node *dag.Node) error {
	target := node.Target
	targetLog := a.log.WithPrefix(target.ID())

	targetLog.Debug("checking cache", logger.String("input_hash_in_progress", "calculating"))

	shouldBuild, inputHash, err := a.validator.ShouldBuild(target)
	if err != nil {
		targetLog.Warn("cache check failed", logger.Err(err))
		shouldBuild = true
	}

	if !shouldBuild {
		a.rebuiltMu.RLock()
		for _, depID := range target.Deps {
			if a.rebuilt[depID] {
				shouldBuild = true
				targetLog.Debug("dependency was rebuilt", logger.String("dep", depID))
				break
			}
		}
		a.rebuiltMu.RUnlock()
	}

	targetLog.Debug("cache check complete",
		logger.String("input_hash", inputHash),
		logger.Bool("should_build", shouldBuild),
		logger.Bool("force", a.force))

	if !shouldBuild && !a.force {
		targetLog.Info("skipped (cached)")
		return nil
	}

	targetLog.Info("building...")
	buildStart := time.Now()

	bundle := a.config.Bundles()[target.BundleName]
	env := exec.ComposeEnv(a.config.RepoRoot(), a.config.Repo(), bundle, target)
	workDir := exec.ResolveWorkDir(a.config.RepoRoot(), target)

	err = exec.RunCommand(ctx, target.Cmd, &exec.ShellOptions{
		WorkDir: workDir,
		Env:     env,
		Shell:   a.config.Repo().Shell,
		Stdout:  targetLog.Writer(),
		Stderr:  targetLog.Writer(),
	})

	if err != nil {
		targetLog.Error("build failed", logger.Err(err))
		return err
	}

	duration := time.Since(buildStart)

	a.rebuiltMu.Lock()
	a.rebuilt[target.ID()] = true
	a.rebuiltMu.Unlock()

	a.store.Set(target.ID(), &builds.Entry{
		InputHash:  inputHash,
		Timestamp:  time.Now(),
		DurationMs: duration.Milliseconds(),
	})

	if err = a.store.Save(); err != nil {
		targetLog.Warn("failed to save cache", logger.Err(err))
	}

	targetLog.Info("completed", logger.Duration("duration", duration))

	return nil
}

func (a *BuildAction) DryRun(targetIDs []string) {
	subgraph := a.graph.SubgraphFor(targetIDs)

	sorted, err := subgraph.TopologicalSort()
	if err != nil {
		a.log.Error("failed to sort targets", logger.Err(err))
		return
	}

	for _, node := range sorted {
		target := node.Target
		bundle := a.config.Bundles()[target.BundleName]
		env := exec.ComposeEnv(a.config.RepoRoot(), a.config.Repo(), bundle, target)
		workDir := exec.ResolveWorkDir(a.config.RepoRoot(), target)

		a.log.Info("target", logger.String("id", target.ID()))
		a.log.Info("workdir", logger.String("path", workDir))
		a.log.Info("command", logger.String("cmd", target.Cmd))
		for _, e := range env {
			a.log.Info("env", logger.String("var", e))
		}
	}
}
