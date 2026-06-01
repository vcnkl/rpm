package actions

import (
	"context"
	"io"
	"os"

	"github.com/vcnkl/rpm/config"
	envconfig "github.com/vcnkl/rpm/environments/config"
	"github.com/vcnkl/rpm/environments/generator"
	envruntime "github.com/vcnkl/rpm/environments/runtime"
	runtimedocker "github.com/vcnkl/rpm/environments/runtime/docker"
	envspec "github.com/vcnkl/rpm/environments/spec"
	envstarlark "github.com/vcnkl/rpm/environments/starlark"
	"github.com/vcnkl/rpm/environments/tui"
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
	plan, err := a.loadPlan(ctx, opts.Blueprint, renderRuntimeOptions{
		NoDeps:   opts.NoDeps,
		NoReload: opts.NoReload,
	})
	if err != nil {
		return err
	}

	sink := envruntime.EventSink(envruntime.NewLineEventSink(a.out, a.err))
	tuiSink := (*tui.EventSink)(nil)
	if !opts.NonInteractive {
		tuiSink = tui.NewEventSink(512)
		sink = tuiSink
	}
	controlActions := make(chan envruntime.ControlAction, 16)

	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:    envruntime.NewShellProcessRunner(a.config.Repo().Shell, a.out, a.err),
		DependencyRunner: runtimedocker.NewCLI(runtimedocker.Options{}),
		ReloadWatcher:    envruntime.NewWatcherFactory(),
		EventSink:        sink,
		ControlActions:   controlActions,
		NoDeps:           opts.NoDeps,
		NoReload:         opts.NoReload,
	})
	if !opts.NonInteractive {
		bridge := tui.NewBridge(a.out, stderrOrDefault(a.err))
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
	s.Send(tui.Action{Type: tui.ActionRestart, Ref: ref})
	return nil
}

func (s controlSender) RestartAll(context.Context) error {
	s.Send(tui.Action{Type: tui.ActionRestartAll})
	return nil
}

func (s controlSender) Stop() {
	s.Send(tui.Action{Type: tui.ActionQuit})
}

func (s controlSender) Send(action tui.Action) {
	switch action.Type {
	case tui.ActionRestart:
		s.actions <- envruntime.ControlAction{Type: envruntime.ActionRestartTarget, Ref: action.Ref}
	case tui.ActionRestartAll:
		s.actions <- envruntime.ControlAction{Type: envruntime.ActionRestartAll}
	case tui.ActionQuit:
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
	plan, err := a.loadPlan(ctx, opts.Blueprint, renderRuntimeOptions{})
	if err != nil {
		return err
	}

	runner := envruntime.NewRunner(envruntime.Options{
		DependencyRunner: runtimedocker.NewCLI(runtimedocker.Options{}),
		EventSink:        envruntime.NewLineEventSink(a.out, a.err),
	})
	return runner.Down(ctx, plan)
}

type renderRuntimeOptions struct {
	NoDeps   bool
	NoReload bool
}

func (a *EnvAction) loadPlan(ctx context.Context, name string, opts renderRuntimeOptions) (*envstarlark.RuntimePlan, error) {
	blueprint, err := envconfig.LoadBlueprint(a.config, name)
	if err != nil {
		return nil, err
	}
	blueprint = envspec.BlueprintWithRuntimeOptions(blueprint, envspec.RuntimeOptions{
		NoDeps:   opts.NoDeps,
		NoReload: opts.NoReload,
	})
	resolved, err := envspec.Resolve(a.config, blueprint)
	if err != nil {
		return nil, err
	}
	data, err := generator.Render(resolved)
	if err != nil {
		return nil, err
	}
	return envstarlark.InterpretSource(ctx, name, generator.CachePath(a.config, name), data)
}
