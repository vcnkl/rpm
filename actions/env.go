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
	envspec "github.com/vcnkl/rpm/environments/spec"
	envstarlark "github.com/vcnkl/rpm/environments/starlark"
	envtui "github.com/vcnkl/rpm/environments/tui"
	"github.com/vcnkl/rpm/models"

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

	controlActions := make(chan envruntime.ControlAction, 16)

	var sink envruntime.EventSink = envruntime.NewLineEventSink(a.out, a.err)
	progSink := (*envtui.ProgramSink)(nil)
	if !opts.NonInteractive {
		progSink = envtui.NewProgramSink()
		sink = progSink
	}

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
	if opts.NonInteractive {
		return runner.Up(ctx, plan)
	}
	return envtui.RunDashboard(ctx, envtui.DashboardConfig{
		Blueprint:  plan.Environment.Name,
		Sink:       progSink,
		Controller: controlSender{actions: controlActions},
		Run:        func(runCtx context.Context) error { return runner.Up(runCtx, plan) },
		Input:      os.Stdin,
		Output:     stderrOrDefault(a.err),
	})
}

type controlSender struct {
	actions chan<- envruntime.ControlAction
}

func (s controlSender) Restart(_ context.Context, ref string) error {
	s.actions <- envruntime.ControlAction{Type: envruntime.ActionRestartTarget, Ref: ref}
	return nil
}

func (s controlSender) RestartAll(context.Context) error {
	s.actions <- envruntime.ControlAction{Type: envruntime.ActionRestartAll}
	return nil
}

func (s controlSender) Stop() {
	s.actions <- envruntime.ControlAction{Type: envruntime.ActionStop}
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
	plan = expandRepoRoot(plan, a.config.RepoRoot())
	return a.resolvePlanReferences(plan)
}

func expandRepoRoot(plan *envstarlark.RuntimePlan, repoRoot string) *envstarlark.RuntimePlan {
	next := *plan
	next.Environment.Variables = expandMap(next.Environment.Variables, repoRoot)
	next.Dependencies = append([]envstarlark.Dependency{}, plan.Dependencies...)
	for i := range next.Dependencies {
		next.Dependencies[i].ConfigPath = expandString(next.Dependencies[i].ConfigPath, repoRoot)
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
	target.ConfigPath = expandString(target.ConfigPath, repoRoot)
	target.WorkingDir = expandString(target.WorkingDir, repoRoot)
	target.Env = expandMap(target.Env, repoRoot)
	target.DotenvEnv = expandMap(target.DotenvEnv, repoRoot)
	target.DotenvFiles = expandList(target.DotenvFiles, repoRoot)
	return target
}

func (a *EnvAction) resolvePlanReferences(plan *envstarlark.RuntimePlan) (*envstarlark.RuntimePlan, error) {
	if !planUsesConfigReferences(plan) {
		return plan, nil
	}
	blueprint := &models.EnvironmentBlueprint{
		Version:   1,
		Name:      plan.Environment.Name,
		Variables: plan.Environment.Variables,
		Before:    beforeRefs(plan.BeforeTargets),
		Targets:   planTargets(plan.Targets),
		DependencyPolicy: models.DependencyPolicy{
			Enabled: len(plan.Dependencies) > 0,
			Include: dependencyRefs(plan.Dependencies),
			Exclude: []string{},
		},
		ReloadPolicy: models.ReloadPolicy{
			Enabled:  plan.Environment.LiveReload.Enabled,
			Debounce: plan.Environment.LiveReload.Debounce,
		},
	}
	resolved, err := envspec.Resolve(a.config, blueprint)
	if err != nil {
		return nil, err
	}
	return resolvedPlan(resolved, plan.RunOrder), nil
}

func planUsesConfigReferences(plan *envstarlark.RuntimePlan) bool {
	for _, dep := range plan.Dependencies {
		if dep.ConfigPath != "" || dep.Image == "" {
			return true
		}
	}
	for _, target := range plan.BeforeTargets {
		if target.ConfigPath != "" || target.Command == "" {
			return true
		}
	}
	for _, target := range plan.Targets {
		if target.ConfigPath != "" || target.Command == "" {
			return true
		}
	}
	return false
}

func beforeRefs(targets []envstarlark.TargetProcess) []string {
	refs := make([]string, 0, len(targets))
	for _, target := range targets {
		refs = append(refs, target.Ref)
	}
	return refs
}

func dependencyRefs(dependencies []envstarlark.Dependency) []string {
	refs := make([]string, 0, len(dependencies))
	for _, dep := range dependencies {
		refs = append(refs, dep.Ref)
	}
	return refs
}

func planTargets(targets []envstarlark.TargetProcess) []models.EnvironmentTarget {
	result := make([]models.EnvironmentTarget, 0, len(targets))
	for _, target := range targets {
		reload := target.Reload
		result = append(result, models.EnvironmentTarget{
			Ref:    target.Ref,
			Reload: &reload,
			Env:    target.Env,
		})
	}
	return result
}

func resolvedPlan(env *envspec.ResolvedEnvironment, runOrder []string) *envstarlark.RuntimePlan {
	plan := &envstarlark.RuntimePlan{
		Environment: envstarlark.Environment{
			Name:      env.Name,
			Variables: envVarMap(env.Variables),
			LiveReload: envstarlark.ReloadPolicy{
				Enabled:  env.ReloadPolicy.Enabled,
				Debounce: env.ReloadPolicy.Debounce,
			},
		},
		RunOrder: append([]string{}, runOrder...),
	}
	for _, dep := range env.Dependencies {
		plan.Dependencies = append(plan.Dependencies, envstarlark.Dependency{
			Ref:          dep.Ref,
			ConfigPath:   dep.ConfigPath,
			Name:         dep.Name,
			Image:        dep.Image,
			Env:          envVarMap(dep.Env),
			Ports:        append([]string{}, dep.Ports...),
			Volumes:      append([]string{}, dep.Volumes...),
			ReadinessCmd: dep.ReadinessCmd,
		})
	}
	for _, target := range env.BeforeTargets {
		plan.BeforeTargets = append(plan.BeforeTargets, envstarlark.TargetProcess{
			Ref:         target.Ref,
			ConfigPath:  target.ConfigPath,
			Command:     target.Command,
			WorkingDir:  target.WorkingDir,
			Env:         envVarMap(target.Env),
			DotenvEnv:   envVarMap(target.DotenvEnv),
			DotenvFiles: append([]string{}, target.Dotenv.Files...),
		})
	}
	for _, target := range env.Targets {
		plan.Targets = append(plan.Targets, envstarlark.TargetProcess{
			Ref:         target.Ref,
			ConfigPath:  target.ConfigPath,
			Command:     target.Command,
			WorkingDir:  target.WorkingDir,
			Env:         envVarMap(target.ExplicitEnv),
			DotenvEnv:   envVarMap(target.DotenvEnv),
			DotenvFiles: append([]string{}, target.Dotenv.Files...),
			Reload:      target.Reload,
		})
		plan.Watches = append(plan.Watches, envstarlark.Watch{
			Target:  target.Ref,
			Roots:   append([]string{}, target.Watch.Roots...),
			Ignore:  append([]string{}, target.Watch.Ignore...),
			Reload:  target.Watch.Reload,
			Enabled: target.Watch.Enabled,
		})
	}
	if len(plan.RunOrder) == 0 {
		for _, before := range plan.BeforeTargets {
			plan.RunOrder = append(plan.RunOrder, before.Ref)
		}
		for _, dep := range plan.Dependencies {
			plan.RunOrder = append(plan.RunOrder, dep.Ref)
		}
		for _, target := range plan.Targets {
			plan.RunOrder = append(plan.RunOrder, target.Ref)
		}
	}
	return plan
}

func envVarMap(values []envspec.EnvVar) map[string]string {
	if len(values) == 0 {
		return map[string]string{}
	}
	result := make(map[string]string, len(values))
	for _, value := range values {
		result[value.Name] = value.Value
	}
	return result
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
