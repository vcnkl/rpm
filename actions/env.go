package actions

import (
	"context"
	"io"

	"github.com/vcnkl/rpm/config"
	envconfig "github.com/vcnkl/rpm/environments/config"
	"github.com/vcnkl/rpm/environments/generator"
	envruntime "github.com/vcnkl/rpm/environments/runtime"
	runtimedocker "github.com/vcnkl/rpm/environments/runtime/docker"
	envspec "github.com/vcnkl/rpm/environments/spec"
	envstarlark "github.com/vcnkl/rpm/environments/starlark"
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

	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:    envruntime.NewShellProcessRunner(a.config.Repo().Shell, a.out, a.err),
		DependencyRunner: runtimedocker.NewCLI(runtimedocker.Options{}),
		ReloadWatcher:    envruntime.NewWatcherFactory(),
		EventSink:        envruntime.NewLineEventSink(a.out, a.err),
		NoDeps:           opts.NoDeps,
		NoReload:         opts.NoReload,
	})
	return runner.Up(ctx, plan)
}

type EnvDownOptions struct {
	Blueprint string
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
