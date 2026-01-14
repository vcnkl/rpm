package actions

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vcnkl/rpm/config"
	"github.com/vcnkl/rpm/dag"
	rpmexec "github.com/vcnkl/rpm/exec"
	"github.com/vcnkl/rpm/logger"
	"github.com/vcnkl/rpm/models"
	"github.com/vcnkl/rpm/watcher"
)

type DevAction struct {
	config *config.Config
	graph  *dag.Graph
	log    logger.Logger
}

func NewDevAction(cfg *config.Config, graph *dag.Graph, log logger.Logger) *DevAction {
	return &DevAction{
		config: cfg,
		graph:  graph,
		log:    log,
	}
}

func (a *DevAction) Execute(ctx context.Context, targetIDs []string) (*models.Result, error) {
	start := time.Now()
	result := &models.Result{}

	var wg sync.WaitGroup
	errCh := make(chan error, len(targetIDs))

	for _, id := range targetIDs {
		node, ok := a.graph.Nodes[id]
		if !ok {
			return nil, &dag.TargetNotFoundError{ID: id}
		}

		wg.Add(1)
		go func(n *dag.Node) {
			defer wg.Done()
			if err := a.runDevTarget(ctx, n); err != nil {
				errCh <- err
			}
		}(node)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		wg.Wait()
	case err := <-errCh:
		result.Failed = append(result.Failed, models.FailedTarget{Error: err})
	case <-done:
	}

	result.Duration = time.Since(start)
	return result, nil
}

func (a *DevAction) runDevTarget(ctx context.Context, node *dag.Node) error {
	target := node.Target
	targetLog := a.log.WithPrefix(target.ID())

	if err := a.RunDependencies(ctx, node); err != nil {
		return err
	}

	bundle := a.config.Bundles()[target.BundleName]
	env := rpmexec.ComposeEnv(a.config.RepoRoot(), a.config.Repo(), bundle, target)
	workDir := rpmexec.ResolveWorkDir(a.config.RepoRoot(), target)

	if !target.Config.Reload {
		targetLog.Info("starting (reload disabled)...")
		return rpmexec.RunCommand(ctx, target.Cmd, &rpmexec.ShellOptions{
			WorkDir: workDir,
			Env:     env,
			Shell:   a.config.Repo().Shell,
			Stdout:  targetLog.Writer(),
			Stderr:  targetLog.Writer(),
		})
	}

	if err := a.runBundleBuildTargets(ctx, target.BundleName, targetLog); err != nil {
		targetLog.Error("build failed, cannot start", logger.Err(err))
	}

	bundleRoot := filepath.Join(a.config.RepoRoot(), target.BundlePath)
	w, err := watcher.NewWatcher([]string{bundleRoot}, target.Config.Ignore)
	if err != nil {
		return err
	}
	defer w.Stop()

	var cmd *exec.Cmd
	var cmdMu sync.Mutex

	startCmd := func() {
		cmdMu.Lock()
		defer cmdMu.Unlock()

		if cmd != nil && cmd.Process != nil {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			time.Sleep(100 * time.Millisecond)
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			cmd.Wait()
		}

		targetLog.Info("starting...")
		shellParts := strings.Fields(a.config.Repo().Shell)
		shellArgs := append(shellParts[1:], "-c", target.Cmd)
		cmd = exec.Command(shellParts[0], shellArgs...)
		cmd.Dir = workDir
		cmd.Env = env
		cmd.Stdout = targetLog.Writer()
		cmd.Stderr = targetLog.Writer()
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

		if err = cmd.Start(); err != nil {
			targetLog.Error("failed to start", logger.Err(err))
			return
		}

		go func() {
			cmd.Wait()
		}()
	}

	w.OnChange(func(path string) {
		targetLog.Info("file changed, rebuilding...", logger.String("path", path))
		if err = a.runBundleBuildTargets(ctx, target.BundleName, targetLog); err != nil {
			targetLog.Error("build failed, not restarting", logger.Err(err))
			return
		}
		targetLog.Info("restarting...")
		startCmd()
	})

	startCmd()

	go func() {
		w.Start(ctx)
	}()

	<-ctx.Done()

	cmdMu.Lock()
	if cmd != nil && cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		time.Sleep(100 * time.Millisecond)
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmdMu.Unlock()

	return nil
}

func (a *DevAction) DryRun(targetIDs []string) {
	for _, id := range targetIDs {
		node, ok := a.graph.Nodes[id]
		if !ok {
			a.log.Error("target not found", logger.String("id", id))
			continue
		}

		target := node.Target
		bundle := a.config.Bundles()[target.BundleName]
		env := rpmexec.ComposeEnv(a.config.RepoRoot(), a.config.Repo(), bundle, target)
		workDir := rpmexec.ResolveWorkDir(a.config.RepoRoot(), target)

		a.log.Info("target", logger.String("id", target.ID()))
		a.log.Info("workdir", logger.String("path", workDir))
		a.log.Info("command", logger.String("cmd", target.Cmd))
		for _, e := range env {
			a.log.Info("env", logger.String("var", e))
		}
	}
}

func (a *DevAction) RunDependencies(ctx context.Context, node *dag.Node) error {
	deps := a.graph.Ancestors(node.ID)

	for _, dep := range deps {
		if dep.Target.HasSuffix("_dev") {
			continue
		}

		targetLog := a.log.WithPrefix(dep.ID)
		targetLog.Info("running dependency...")

		bundle := a.config.Bundles()[dep.Target.BundleName]
		env := rpmexec.ComposeEnv(a.config.RepoRoot(), a.config.Repo(), bundle, dep.Target)
		workDir := rpmexec.ResolveWorkDir(a.config.RepoRoot(), dep.Target)

		err := rpmexec.RunCommand(ctx, dep.Target.Cmd, &rpmexec.ShellOptions{
			WorkDir: workDir,
			Env:     env,
			Shell:   a.config.Repo().Shell,
			Stdout:  os.Stdout,
			Stderr:  os.Stderr,
		})

		if err != nil {
			return err
		}
	}

	return nil
}

func (a *DevAction) runBundleBuildTargets(ctx context.Context, bundleName string, targetLog logger.Logger) error {
	bundle := a.config.Bundles()[bundleName]
	if bundle == nil {
		return nil
	}

	buildTargets := bundle.TargetsByType("_build")
	if len(buildTargets) == 0 {
		return nil
	}

	var targetIds []string
	for _, t := range buildTargets {
		targetIds = append(targetIds, t.ID())
	}

	subgraph := a.graph.SubgraphFor(targetIds)
	sorted, err := subgraph.TopologicalSort()
	if err != nil {
		return err
	}

	for _, node := range sorted {
		target := node.Target
		buildLog := a.log.WithPrefix(target.ID())
		buildLog.Info("building...")

		b := a.config.Bundles()[target.BundleName]
		env := rpmexec.ComposeEnv(a.config.RepoRoot(), a.config.Repo(), b, target)
		workDir := rpmexec.ResolveWorkDir(a.config.RepoRoot(), target)

		err = rpmexec.RunCommand(ctx, target.Cmd, &rpmexec.ShellOptions{
			WorkDir: workDir,
			Env:     env,
			Shell:   a.config.Repo().Shell,
			Stdout:  buildLog.Writer(),
			Stderr:  buildLog.Writer(),
		})

		if err != nil {
			buildLog.Error("build failed", logger.Err(err))
			return err
		}
	}

	return nil
}
