package actions

import (
	"context"
	"os/exec"
	"path/filepath"
	"sort"
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

type buildRunner func(ctx context.Context, bundleName string, targetIDs []string) error

type buildStatus int

const (
	buildStatusNoop buildStatus = iota
	buildStatusRan
	buildStatusShared
)

type buildResult struct {
	done chan struct{}
	err  error
}

type recentBuild struct {
	at  time.Time
	err error
}

type bundleBuildCoordinator struct {
	targetsByBundle map[string][]string
	run             buildRunner
	inflight        map[string]*buildResult
	recent          map[string]*recentBuild
	window          time.Duration
	mu              sync.Mutex
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

	targetNodes := make([]*dag.Node, 0, len(targetIDs))
	for _, id := range targetIDs {
		node, ok := a.graph.Nodes[id]
		if !ok {
			return nil, &dag.TargetNotFoundError{ID: id}
		}
		targetNodes = append(targetNodes, node)
	}

	if err := a.runDependencies(ctx, targetIDs); err != nil {
		return nil, err
	}

	coordinator := newBundleBuildCoordinator(buildTargetsByBundle(a.graph, targetIDs), a.runBundleBuildTargets)

	var wg sync.WaitGroup
	errCh := make(chan error, len(targetNodes))

	for _, node := range targetNodes {
		wg.Add(1)
		go func(n *dag.Node) {
			defer wg.Done()
			if err := a.runDevTarget(ctx, n, coordinator); err != nil {
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

func (a *DevAction) runDevTarget(ctx context.Context, node *dag.Node, coordinator *bundleBuildCoordinator) error {
	target := node.Target
	targetLog := a.log.WithPrefix(target.ID())

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

		if startErr := cmd.Start(); startErr != nil {
			targetLog.Error("failed to start", logger.Err(startErr))
			return
		}

		go func() {
			cmd.Wait()
		}()
	}

	w.OnChange(func(path string) {
		status, buildErr := coordinator.Build(ctx, target.BundleName)
		if buildErr != nil {
			targetLog.Error("build failed, not restarting", logger.Err(buildErr))
			return
		}

		if status == buildStatusRan {
			targetLog.Info("file changed, rebuilding...", logger.String("path", path))
		} else if status == buildStatusNoop {
			targetLog.Info("file changed, restarting...", logger.String("path", path))
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

func (a *DevAction) runDependencies(ctx context.Context, targetIDs []string) error {
	subgraph := a.graph.SubgraphFor(targetIDs)
	sorted, err := subgraph.TopologicalSort()
	if err != nil {
		return err
	}

	for _, node := range sorted {
		target := node.Target
		if target.HasSuffix("_dev") {
			continue
		}

		targetLog := a.log.WithPrefix(target.ID())
		if target.HasSuffix("_build") {
			targetLog.Info("building...")
		} else {
			targetLog.Info("running dependency...")
		}

		bundle := a.config.Bundles()[target.BundleName]
		env := rpmexec.ComposeEnv(a.config.RepoRoot(), a.config.Repo(), bundle, target)
		workDir := rpmexec.ResolveWorkDir(a.config.RepoRoot(), target)

		runErr := rpmexec.RunCommand(ctx, target.Cmd, &rpmexec.ShellOptions{
			WorkDir: workDir,
			Env:     env,
			Shell:   a.config.Repo().Shell,
			Stdout:  targetLog.Writer(),
			Stderr:  targetLog.Writer(),
		})

		if runErr != nil {
			return runErr
		}
	}

	return nil
}

func (a *DevAction) runBundleBuildTargets(ctx context.Context, _ string, targetIDs []string) error {
	if len(targetIDs) == 0 {
		return nil
	}

	subgraph := a.graph.SubgraphFor(targetIDs)
	sorted, err := subgraph.TopologicalSort()
	if err != nil {
		return err
	}

	for _, node := range sorted {
		target := node.Target
		buildLog := a.log.WithPrefix(target.ID())
		if target.HasSuffix("_build") {
			buildLog.Info("building...")
		} else {
			buildLog.Info("running dependency...")
		}

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

func buildTargetsByBundle(graph *dag.Graph, targetIDs []string) map[string][]string {
	subgraph := graph.SubgraphFor(targetIDs)
	targetsByBundle := make(map[string][]string)

	for _, node := range subgraph.Nodes {
		if !node.Target.HasSuffix("_build") {
			continue
		}
		targetsByBundle[node.Target.BundleName] = append(targetsByBundle[node.Target.BundleName], node.ID)
	}

	for bundle, ids := range targetsByBundle {
		sort.Strings(ids)
		targetsByBundle[bundle] = dedupeSorted(ids)
	}

	return targetsByBundle
}

func dedupeSorted(values []string) []string {
	if len(values) == 0 {
		return values
	}

	result := values[:1]
	last := values[0]
	for i := 1; i < len(values); i++ {
		if values[i] == last {
			continue
		}
		last = values[i]
		result = append(result, values[i])
	}
	return result
}

func newBundleBuildCoordinator(targetsByBundle map[string][]string, run buildRunner) *bundleBuildCoordinator {
	return &bundleBuildCoordinator{
		targetsByBundle: targetsByBundle,
		run:             run,
		inflight:        make(map[string]*buildResult),
		recent:          make(map[string]*recentBuild),
		window:          200 * time.Millisecond,
	}
}

func (c *bundleBuildCoordinator) Build(ctx context.Context, bundleName string) (buildStatus, error) {
	now := time.Now()
	c.mu.Lock()
	if inFlight, ok := c.inflight[bundleName]; ok {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return buildStatusShared, ctx.Err()
		case <-inFlight.done:
			return buildStatusShared, inFlight.err
		}
	}

	if last, ok := c.recent[bundleName]; ok && now.Sub(last.at) <= c.window {
		c.mu.Unlock()
		return buildStatusShared, last.err
	}

	targetIDs := append([]string(nil), c.targetsByBundle[bundleName]...)

	current := &buildResult{done: make(chan struct{})}
	c.inflight[bundleName] = current
	c.mu.Unlock()

	status := buildStatusNoop
	var err error
	if len(targetIDs) > 0 {
		err = c.run(ctx, bundleName, targetIDs)
		status = buildStatusRan
	}

	c.mu.Lock()
	current.err = err
	c.recent[bundleName] = &recentBuild{at: time.Now(), err: err}
	close(current.done)
	delete(c.inflight, bundleName)
	c.mu.Unlock()

	return status, err
}
