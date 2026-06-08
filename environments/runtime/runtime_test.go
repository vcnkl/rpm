package runtime_test

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	envruntime "github.com/vcnkl/rpm/environments/runtime"
	envstarlark "github.com/vcnkl/rpm/environments/starlark"
)

func TestUpHonorsNoDepsAndNoReload(t *testing.T) {
	processes := &fakeProcessRunner{}
	deps := &fakeDependencyRunner{}
	watcher := &fakeWatcher{}
	events := &eventRecorder{}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:    processes,
		DependencyRunner: deps,
		ReloadWatcher:    watcher,
		EventSink:        events,
		NoDeps:           true,
		NoReload:         true,
	})

	err := runner.Up(context.Background(), testPlan())
	require.NoError(t, err)

	assert.Equal(t, []string{"api:serve"}, processes.startedRefs())
	assert.Zero(t, deps.upCalls)
	assert.Zero(t, watcher.calls)
	assert.Contains(t, events.types(), envruntime.EventEnvironmentStopped)
}

func TestUpStartsDependenciesAndUsesEnabledWatches(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	processes := &fakeProcessRunner{block: true}
	deps := &fakeDependencyRunner{}
	watcher := &fakeWatcher{block: true, ready: make(chan struct{})}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:    processes,
		DependencyRunner: deps,
		ReloadWatcher:    watcher,
		EventSink:        &eventRecorder{},
	})

	done := make(chan error, 1)
	go func() {
		done <- runner.Up(ctx, testPlan())
	}()
	require.Eventually(t, func() bool {
		select {
		case <-watcher.ready:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-done)

	assert.Equal(t, 1, deps.upCalls)
	assert.Equal(t, 1, watcher.calls)
	require.Len(t, watcher.watches, 1)
	assert.Equal(t, "api:serve", watcher.watches[0].Target)
	assert.Contains(t, watcher.watches[0].Roots, "/repo/api")
}

func TestUpReturnsProcessStartupFailure(t *testing.T) {
	processes := &fakeProcessRunner{startErr: assert.AnError}
	runner := envruntime.NewRunner(envruntime.Options{ProcessRunner: processes})

	err := runner.Up(context.Background(), testPlan())

	require.ErrorIs(t, err, assert.AnError)
}

func TestUpReturnsRuntimeFailureWithoutWaitingForUnrelatedProcess(t *testing.T) {
	processes := &fakeProcessRunner{
		blockRefs: map[string]bool{"api:serve": true},
		waitErrs:  map[string]error{"worker:run": assert.AnError},
	}
	plan := testPlan()
	plan.Targets = append(plan.Targets, envstarlark.TargetProcess{
		Ref:        "worker:run",
		Command:    "exit 1",
		WorkingDir: "/repo/worker",
	})
	plan.RunOrder = []string{"api:serve", "worker:run"}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner: processes,
		NoReload:      true,
	})

	err := runner.Up(context.Background(), plan)

	require.ErrorIs(t, err, assert.AnError)
	assert.GreaterOrEqual(t, processes.stopCount("api:serve"), 1)
}

func TestUpRunsBeforeTargetsAfterDependenciesBeforeTargets(t *testing.T) {
	order := []string{}
	processes := &fakeProcessRunner{order: &order}
	deps := &fakeDependencyRunner{order: &order, env: map[string]string{"POSTGRES_PORT": "49152"}}
	plan := testPlan()
	plan.BeforeTargets = []envstarlark.TargetProcess{
		{Ref: "api:migrate", Command: "echo migrate", WorkingDir: "/repo/api"},
		{Ref: "api:seed", Command: "echo seed", WorkingDir: "/repo/api"},
	}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:    processes,
		DependencyRunner: deps,
		NoReload:         true,
	})

	err := runner.Up(context.Background(), plan)

	require.NoError(t, err)
	assert.Equal(t, []string{
		"deps up",
		"process api:migrate",
		"process api:seed",
		"process api:serve",
		"deps down",
	}, order)
	assert.Equal(t, []string{"api:migrate", "api:seed", "api:serve"}, processes.startedRefs())
	require.Len(t, processes.started, 3)
	assert.Equal(t, "49152", processes.started[0].Env["POSTGRES_PORT"])
	assert.Equal(t, "49152", processes.started[1].Env["POSTGRES_PORT"])
	assert.Equal(t, "49152", processes.started[2].Env["POSTGRES_PORT"])
}

func TestUpDependencyEnvOverridesTargetEnvAndPersistsAcrossRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processes := &fakeProcessRunner{block: true}
	actions := make(chan envruntime.ControlAction, 1)

	plan := testPlan()
	plan.Targets[0].Env["POSTGRES_PORT"] = "static"
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner: processes,
		DependencyRunner: &fakeDependencyRunner{
			env: map[string]string{"POSTGRES_PORT": "49152"},
		},
		ControlActions: actions,
		NoReload:       true,
	})

	done := make(chan error, 1)
	go func() {
		done <- runner.Up(ctx, plan)
	}()
	require.Eventually(t, func() bool {
		return len(processes.startedRefs()) == 1
	}, time.Second, 10*time.Millisecond)
	actions <- envruntime.ControlAction{Type: envruntime.ActionRestartTarget, Ref: "api:serve"}
	require.Eventually(t, func() bool {
		return len(processes.startedRefs()) == 2
	}, time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-done)

	require.Len(t, processes.started, 2)
	assert.Equal(t, "49152", processes.started[0].Env["POSTGRES_PORT"])
	assert.Equal(t, "49152", processes.started[1].Env["POSTGRES_PORT"])
}

func TestUpDependencyEnvOverridesDotenvValueInFinalEnvMap(t *testing.T) {
	processes := &fakeProcessRunner{}
	plan := testPlan()
	plan.Targets[0].Env["MONGODB_PORT"] = "27017"
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner: processes,
		DependencyRunner: &fakeDependencyRunner{
			env: map[string]string{"MONGODB_PORT": "49152"},
		},
		NoReload: true,
	})

	err := runner.Up(context.Background(), plan)

	require.NoError(t, err)
	require.Len(t, processes.started, 1)
	assert.Equal(t, "49152", processes.started[0].Env["MONGODB_PORT"])
}

func TestUpDependencyEnvOverridesBeforeTargetEnvInNormalizedPlan(t *testing.T) {
	processes := &fakeProcessRunner{}
	plan := testPlan()
	plan.BeforeTargets = []envstarlark.TargetProcess{{
		Ref:        "api:migrate",
		Command:    "echo migrate",
		WorkingDir: "/repo/api",
		Env:        map[string]string{"MONGODB_PORT": "27017"},
	}}
	plan.Targets[0].Env["MONGODB_PORT"] = "27017"
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner: processes,
		DependencyRunner: &fakeDependencyRunner{
			env: map[string]string{"MONGODB_PORT": "49152"},
		},
		NoReload: true,
	})

	err := runner.Up(context.Background(), plan)

	require.NoError(t, err)
	require.Len(t, processes.started, 2)
	assert.Equal(t, "api:migrate", processes.started[0].Ref)
	assert.Equal(t, "49152", processes.started[0].Env["MONGODB_PORT"])
	assert.Equal(t, "api:serve", processes.started[1].Ref)
	assert.Equal(t, "49152", processes.started[1].Env["MONGODB_PORT"])
}

func TestUpShellBeforeTargetUsesDynamicDependencyEnv(t *testing.T) {
	t.Setenv("MONGODB_PORT", "27017")
	out := &bytes.Buffer{}
	events := &eventRecorder{}
	plan := testPlan()
	plan.BeforeTargets = []envstarlark.TargetProcess{{
		Ref:        "api:migrate",
		Command:    `printf '%s\n' "$MONGODB_PORT"`,
		WorkingDir: t.TempDir(),
		Env:        map[string]string{"MONGODB_PORT": "27017"},
	}}
	plan.Targets = []envstarlark.TargetProcess{}
	plan.Watches = []envstarlark.Watch{}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner: envruntime.NewShellProcessRunner("/bin/sh", out, &bytes.Buffer{}),
		DependencyRunner: &fakeDependencyRunner{
			env: map[string]string{"MONGODB_PORT": "49152"},
		},
		EventSink: events,
		NoReload:  true,
	})

	err := runner.Up(context.Background(), plan)

	require.NoError(t, err)
	assert.Equal(t, []string{"49152"}, events.outputLines("api:migrate", "stdout"))
}

func TestNoDepsDoesNotOverrideStaticPortEnv(t *testing.T) {
	processes := &fakeProcessRunner{}
	plan := testPlan()
	plan.BeforeTargets = []envstarlark.TargetProcess{{
		Ref:        "api:migrate",
		Command:    "echo migrate",
		WorkingDir: "/repo/api",
		Env:        map[string]string{"MONGODB_PORT": "27017"},
	}}
	plan.Targets[0].Env["MONGODB_PORT"] = "27017"
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:    processes,
		DependencyRunner: &fakeDependencyRunner{env: map[string]string{"MONGODB_PORT": "49152"}},
		NoDeps:           true,
		NoReload:         true,
	})

	err := runner.Up(context.Background(), plan)

	require.NoError(t, err)
	require.Len(t, processes.started, 2)
	assert.Equal(t, "27017", processes.started[0].Env["MONGODB_PORT"])
	assert.Equal(t, "27017", processes.started[1].Env["MONGODB_PORT"])
}

func TestShellProcessRunnerUsesFinalEnvMap(t *testing.T) {
	events := &eventRecorder{}
	runner := envruntime.NewShellProcessRunner("/bin/sh", &bytes.Buffer{}, &bytes.Buffer{})

	process, err := runner.Start(context.Background(), envstarlark.TargetProcess{
		Ref:        "api:serve",
		Command:    `printf '%s\n' "$MONGODB_PORT"`,
		WorkingDir: t.TempDir(),
		Env:        map[string]string{"MONGODB_PORT": "49152"},
	}, events)
	require.NoError(t, err)
	require.NoError(t, process.Wait())

	assert.Equal(t, []string{"49152"}, events.outputLines("api:serve", "stdout"))
}

func TestUpStopsDependenciesWhenBeforeTargetFailsNonInteractive(t *testing.T) {
	order := []string{}
	processes := &fakeProcessRunner{
		order:    &order,
		waitErrs: map[string]error{"api:migrate": assert.AnError},
	}
	deps := &fakeDependencyRunner{order: &order}
	events := &eventRecorder{}
	plan := testPlan()
	plan.BeforeTargets = []envstarlark.TargetProcess{
		{Ref: "api:migrate", Command: "exit 1", WorkingDir: "/repo/api"},
	}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:    processes,
		DependencyRunner: deps,
		EventSink:        events,
		NoReload:         true,
	})

	err := runner.Up(context.Background(), plan)

	require.ErrorIs(t, err, assert.AnError)
	assert.Equal(t, []string{"deps up", "process api:migrate", "deps down"}, order)
	assert.Equal(t, []string{"api:migrate"}, processes.startedRefs())
	assert.Equal(t, 1, deps.downCalls)
	assert.Contains(t, events.types(), envruntime.EventEnvironmentStopped)
}

func TestNoDepsStillRunsBeforeTargetsBeforeTargets(t *testing.T) {
	order := []string{}
	processes := &fakeProcessRunner{order: &order}
	deps := &fakeDependencyRunner{order: &order}
	plan := testPlan()
	plan.BeforeTargets = []envstarlark.TargetProcess{
		{Ref: "api:migrate", Command: "echo migrate", WorkingDir: "/repo/api"},
	}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:    processes,
		DependencyRunner: deps,
		NoDeps:           true,
		NoReload:         true,
	})

	err := runner.Up(context.Background(), plan)

	require.NoError(t, err)
	assert.Equal(t, []string{"process api:migrate", "process api:serve"}, order)
	assert.Zero(t, deps.upCalls)
}

func TestUpDeclaresRuntimeUnitsBeforeDependencyStartup(t *testing.T) {
	order := []string{}
	events := &eventRecorder{order: &order}
	deps := &fakeDependencyRunner{order: &order}
	plan := testPlan()
	plan.BeforeTargets = []envstarlark.TargetProcess{{Ref: "api:migrate", Command: "echo migrate", WorkingDir: "/repo/api"}}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:    &fakeProcessRunner{},
		DependencyRunner: deps,
		EventSink:        events,
		NoReload:         true,
	})

	err := runner.Up(context.Background(), plan)

	require.NoError(t, err)
	assert.Equal(t, "event unit_declared postgres", order[0])
	assert.Equal(t, "event unit_declared api:migrate", order[1])
	assert.Equal(t, "event unit_declared api:serve", order[2])
	assert.Equal(t, "deps up", order[3])
}

func TestInteractiveMainTargetFailureKeepsOtherTargetsRunningUntilQuit(t *testing.T) {
	processes := &fakeProcessRunner{
		blockRefs: map[string]bool{"api:serve": true},
		waitErrs:  map[string]error{"worker:run": assert.AnError},
	}
	actions := make(chan envruntime.ControlAction, 1)
	plan := testPlan()
	plan.Targets = append(plan.Targets, envstarlark.TargetProcess{
		Ref:        "worker:run",
		Command:    "exit 1",
		WorkingDir: "/repo/worker",
	})
	plan.RunOrder = []string{"api:serve", "worker:run"}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:  processes,
		ControlActions: actions,
		NoReload:       true,
		Interactive:    true,
	})

	done := make(chan error, 1)
	go func() {
		done <- runner.Up(context.Background(), plan)
	}()
	require.Eventually(t, func() bool {
		return len(processes.startedRefs()) == 2
	}, time.Second, 10*time.Millisecond)
	require.Never(t, func() bool {
		return processes.stopCount("api:serve") > 0
	}, 50*time.Millisecond, 10*time.Millisecond)
	actions <- envruntime.ControlAction{Type: envruntime.ActionStop}

	require.ErrorIs(t, <-done, assert.AnError)
	assert.Equal(t, 1, processes.stopCount("api:serve"))
}

func TestWatcherChangeRestartsAffectedProcessOnly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processes := &fakeProcessRunner{block: true}
	watcher := &fakeWatcher{trigger: true, block: true, ready: make(chan struct{})}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner: processes,
		ReloadWatcher: watcher,
		EventSink:     &eventRecorder{},
	})

	done := make(chan error, 1)
	go func() {
		done <- runner.Up(ctx, testPlan())
	}()
	require.Eventually(t, func() bool {
		return len(processes.startedRefs()) >= 2
	}, time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-done)

	assert.Equal(t, []string{"api:serve", "api:serve"}, processes.startedRefs())
	assert.GreaterOrEqual(t, processes.stopCount("api:serve"), 1)
}

func TestControlActionRestartsSelectedTarget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processes := &fakeProcessRunner{block: true}
	actions := make(chan envruntime.ControlAction, 1)
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:  processes,
		EventSink:      &eventRecorder{},
		ControlActions: actions,
		NoReload:       true,
	})

	done := make(chan error, 1)
	go func() {
		done <- runner.Up(ctx, testPlan())
	}()
	require.Eventually(t, func() bool {
		return len(processes.startedRefs()) == 1
	}, time.Second, 10*time.Millisecond)
	actions <- envruntime.ControlAction{Type: envruntime.ActionRestartTarget, Ref: "api:serve"}
	require.Eventually(t, func() bool {
		return len(processes.startedRefs()) == 2
	}, time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-done)

	assert.Equal(t, []string{"api:serve", "api:serve"}, processes.startedRefs())
	assert.GreaterOrEqual(t, processes.stopCount("api:serve"), 1)
}

func TestControlActionStopsEnvironment(t *testing.T) {
	processes := &fakeProcessRunner{block: true}
	actions := make(chan envruntime.ControlAction, 1)
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:  processes,
		EventSink:      &eventRecorder{},
		ControlActions: actions,
		NoReload:       true,
	})

	done := make(chan error, 1)
	go func() {
		done <- runner.Up(context.Background(), testPlan())
	}()
	require.Eventually(t, func() bool {
		return len(processes.startedRefs()) == 1
	}, time.Second, 10*time.Millisecond)
	actions <- envruntime.ControlAction{Type: envruntime.ActionStop}

	require.NoError(t, <-done)
	assert.Equal(t, 1, processes.stopCount("api:serve"))
}

func testPlan() *envstarlark.RuntimePlan {
	return &envstarlark.RuntimePlan{
		Environment: envstarlark.Environment{
			Name:       "local",
			LiveReload: envstarlark.ReloadPolicy{Enabled: true, Debounce: "10ms"},
		},
		Dependencies: []envstarlark.Dependency{
			{Ref: "postgres", Name: "postgres", Image: "postgres:16"},
		},
		Targets: []envstarlark.TargetProcess{
			{
				Ref:        "api:serve",
				Command:    "echo ok",
				WorkingDir: "/repo/api",
				Env:        map[string]string{"BUNDLE_ROOT": "/repo/api", "APP_PORT": "8080"},
				Reload:     true,
			},
		},
		Watches: []envstarlark.Watch{
			{Target: "api:serve", Roots: []string{"/repo/api"}, Reload: true, Enabled: true},
		},
		RunOrder: []string{"postgres", "api:serve"},
	}
}

type fakeProcessRunner struct {
	mu        sync.Mutex
	started   []envstarlark.TargetProcess
	stopped   map[string]int
	startErr  error
	block     bool
	blockRefs map[string]bool
	waitErrs  map[string]error
	order     *[]string
}

func (r *fakeProcessRunner) Start(ctx context.Context, target envstarlark.TargetProcess, sink envruntime.EventSink) (envruntime.Process, error) {
	if r.startErr != nil {
		return nil, r.startErr
	}
	r.mu.Lock()
	r.started = append(r.started, target)
	if r.order != nil {
		*r.order = append(*r.order, "process "+target.Ref)
	}
	if r.stopped == nil {
		r.stopped = make(map[string]int)
	}
	r.mu.Unlock()
	return &fakeProcess{
		ref:     target.Ref,
		runner:  r,
		done:    make(chan struct{}),
		block:   r.block || r.blockRefs[target.Ref],
		waitErr: r.waitErrs[target.Ref],
	}, nil
}

func (r *fakeProcessRunner) startedRefs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	refs := make([]string, 0, len(r.started))
	for _, target := range r.started {
		refs = append(refs, target.Ref)
	}
	return refs
}

func (r *fakeProcessRunner) stopCount(ref string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopped[ref]
}

type fakeProcess struct {
	ref     string
	runner  *fakeProcessRunner
	done    chan struct{}
	once    sync.Once
	block   bool
	waitErr error
}

func (p *fakeProcess) Wait() error {
	if p.block {
		<-p.done
	}
	return p.waitErr
}

func (p *fakeProcess) Stop(context.Context) error {
	p.runner.mu.Lock()
	p.runner.stopped[p.ref]++
	p.runner.mu.Unlock()
	p.once.Do(func() { close(p.done) })
	return nil
}

type fakeDependencyRunner struct {
	upCalls   int
	downCalls int
	order     *[]string
	env       map[string]string
}

func (r *fakeDependencyRunner) Up(context.Context, string, *envstarlark.RuntimePlan) (envruntime.DependencyStartup, error) {
	r.upCalls++
	if r.order != nil {
		*r.order = append(*r.order, "deps up")
	}
	return envruntime.DependencyStartup{Env: r.env}, nil
}

func (r *fakeDependencyRunner) Down(context.Context, string, *envstarlark.RuntimePlan) error {
	r.downCalls++
	if r.order != nil {
		*r.order = append(*r.order, "deps down")
	}
	return nil
}

type fakeWatcher struct {
	calls   int
	watches []envstarlark.Watch
	trigger bool
	block   bool
	ready   chan struct{}
}

func (w *fakeWatcher) Watch(ctx context.Context, watches []envstarlark.Watch, onChange func(target string, path string)) error {
	w.calls++
	w.watches = append(w.watches, watches...)
	if w.ready != nil {
		close(w.ready)
	}
	if w.trigger {
		onChange(watches[0].Target, fmt.Sprintf("%s/main.go", watches[0].Roots[0]))
	}
	if w.block {
		<-ctx.Done()
	}
	return nil
}

type eventRecorder struct {
	mu     sync.Mutex
	events []envruntime.Event
	order  *[]string
}

func (r *eventRecorder) Emit(event envruntime.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	if r.order != nil {
		*r.order = append(*r.order, "event "+event.Type+" "+event.Ref)
	}
}

func (r *eventRecorder) types() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	types := make([]string, 0, len(r.events))
	for _, event := range r.events {
		types = append(types, event.Type)
	}
	return types
}

func (r *eventRecorder) outputLines(ref string, stream string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	lines := []string{}
	for _, event := range r.events {
		if event.Type == envruntime.EventProcessOutput && event.Ref == ref && event.Stream == stream {
			lines = append(lines, event.Line)
		}
	}
	return lines
}
