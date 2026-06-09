package actions

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vcnkl/rpm/config"
	"github.com/vcnkl/rpm/environments/generator"
	envruntime "github.com/vcnkl/rpm/environments/runtime"
	runtimedocker "github.com/vcnkl/rpm/environments/runtime/docker"
	envstarlark "github.com/vcnkl/rpm/environments/starlark"
	envtui "github.com/vcnkl/rpm/ui/env-tui"

	"github.com/pkg/errors"
)

type EnvAction struct {
	config *config.Config
	out    io.Writer
	err    io.Writer
}

func NewEnvAction(cfg *config.Config, out io.Writer, err io.Writer) *EnvAction {
	return &EnvAction{config: cfg, out: out, err: err}
}

type EnvUpOptions struct {
	Blueprint      string
	NoDeps         bool
	NoReload       bool
	NonInteractive bool
}

func (a *EnvAction) Up(ctx context.Context, opts EnvUpOptions) error {
	plan, err := a.loadPlan(ctx, opts.Blueprint)
	if err != nil {
		return err
	}

	sink := envruntime.EventSink(envruntime.NewLineEventSink(a.out, a.err))
	tuiSink := (*envtui.EventSink)(nil)
	if !opts.NonInteractive {
		tuiSink = envtui.NewEventSink(512)
		sink = tuiSink
	}
	controlActions := make(chan envruntime.ControlAction, 16)

	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:    envruntime.NewShellProcessRunner(a.config.Repo().Shell, a.out, a.err),
		DependencyRunner: a.dockerCLI(),
		ReloadWatcher:    envruntime.NewWatcherFactory(),
		EventSink:        sink,
		ControlActions:   controlActions,
		NoDeps:           opts.NoDeps,
		NoReload:         opts.NoReload,
		Interactive:      !opts.NonInteractive,
	})
	if !opts.NonInteractive {
		bridge := envtui.NewBridge(a.out, stderrOrDefault(a.err))
		runErr := make(chan error, 1)
		go func() {
			runErr <- runner.Up(ctx, plan)
			tuiSink.Close()
		}()
		if err := bridge.Run(ctx, tuiSink.Events(), controlSender{actions: controlActions}); err != nil {
			runner.Stop()
			_ = <-runErr
			return err
		}
		return <-runErr
	}
	return runner.Up(ctx, plan)
}

type controlSender struct {
	actions chan<- envruntime.ControlAction
}

func (s controlSender) Restart(_ context.Context, ref string) error {
	s.Send(envtui.Action{Type: envtui.ActionRestart, Ref: ref})
	return nil
}

func (s controlSender) RestartAll(context.Context) error {
	s.Send(envtui.Action{Type: envtui.ActionRestartAll})
	return nil
}

func (s controlSender) Stop() {
	s.Send(envtui.Action{Type: envtui.ActionQuit})
}

func (s controlSender) Send(action envtui.Action) {
	switch action.Type {
	case envtui.ActionRestart:
		s.actions <- envruntime.ControlAction{Type: envruntime.ActionRestartTarget, Ref: action.Ref}
	case envtui.ActionRestartAll:
		s.actions <- envruntime.ControlAction{Type: envruntime.ActionRestartAll}
	case envtui.ActionQuit:
		s.actions <- envruntime.ControlAction{Type: envruntime.ActionStop}
	}
}

type EnvDownOptions struct {
	Blueprint string
}

func stderrOrDefault(w io.Writer) io.Writer {
	if w != nil {
		return w
	}
	return os.Stderr
}

func (a *EnvAction) Down(ctx context.Context, opts EnvDownOptions) error {
	plan, err := a.loadPlan(ctx, opts.Blueprint)
	if err != nil {
		return err
	}

	runner := envruntime.NewRunner(envruntime.Options{
		DependencyRunner: a.dockerCLI(),
		EventSink:        envruntime.NewLineEventSink(a.out, a.err),
	})
	return runner.Down(ctx, plan)
}

type EnvPruneOptions struct {
	Blueprint string
}

func (a *EnvAction) Prune(_ context.Context, opts EnvPruneOptions) error {
	return runtimedocker.PruneVolumeCache(a.volumeCachePath(), opts.Blueprint)
}

func (a *EnvAction) dockerCLI() *runtimedocker.CLI {
	return runtimedocker.NewCLI(runtimedocker.Options{
		VolumeNamer: runtimedocker.NewFileVolumeNamer(a.volumeCachePath(), a.config.Repo().Project.Name),
		Shell:       a.config.Repo().Shell,
	})
}

func (a *EnvAction) volumeCachePath() string {
	return filepath.Join(a.config.CacheDir(), "env-volumes.json")
}

func (a *EnvAction) loadPlan(ctx context.Context, name string) (*envstarlark.RuntimePlan, error) {
	path := generator.CachePath(a.config, name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.Wrapf(err, "generated Starlark not found at %s; run `rpm env render %s`", path, name)
		}
		return nil, errors.Wrapf(err, "failed to read generated Starlark %s", path)
	}
	plan, err := envstarlark.InterpretSource(ctx, name, path, data)
	if err != nil {
		return nil, err
	}
	return expandRepoRoot(plan, a.config.RepoRoot()), nil
}

func expandRepoRoot(plan *envstarlark.RuntimePlan, repoRoot string) *envstarlark.RuntimePlan {
	next := *plan
	next.Environment.Variables = expandMap(next.Environment.Variables, repoRoot)
	next.Dependencies = append([]envstarlark.Dependency{}, plan.Dependencies...)
	for i := range next.Dependencies {
		next.Dependencies[i].Env = expandMap(next.Dependencies[i].Env, repoRoot)
		next.Dependencies[i].Ports = expandList(next.Dependencies[i].Ports, repoRoot)
		next.Dependencies[i].Volumes = expandList(next.Dependencies[i].Volumes, repoRoot)
		next.Dependencies[i].ReadinessCmd = expandString(next.Dependencies[i].ReadinessCmd, repoRoot)
	}
	next.BeforeTargets = append([]envstarlark.TargetProcess{}, plan.BeforeTargets...)
	for i := range next.BeforeTargets {
		next.BeforeTargets[i] = expandTargetProcess(next.BeforeTargets[i], repoRoot)
	}
	next.Targets = append([]envstarlark.TargetProcess{}, plan.Targets...)
	for i := range next.Targets {
		next.Targets[i] = expandTargetProcess(next.Targets[i], repoRoot)
	}
	next.Watches = append([]envstarlark.Watch{}, plan.Watches...)
	for i := range next.Watches {
		next.Watches[i].Roots = expandList(next.Watches[i].Roots, repoRoot)
		next.Watches[i].Ignore = expandList(next.Watches[i].Ignore, repoRoot)
	}
	return &next
}

func expandTargetProcess(target envstarlark.TargetProcess, repoRoot string) envstarlark.TargetProcess {
	target.WorkingDir = expandString(target.WorkingDir, repoRoot)
	target.Env = expandMap(target.Env, repoRoot)
	return target
}

func expandMap(values map[string]string, repoRoot string) map[string]string {
	if values == nil {
		return nil
	}
	next := make(map[string]string, len(values))
	for key, value := range values {
		next[key] = expandString(value, repoRoot)
	}
	return next
}

func expandList(values []string, repoRoot string) []string {
	next := append([]string{}, values...)
	for i := range next {
		next[i] = expandString(next[i], repoRoot)
	}
	return next
}

func expandString(value string, repoRoot string) string {
	return strings.ReplaceAll(value, generator.RepoRootToken, repoRoot)
}
