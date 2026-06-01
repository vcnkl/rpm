package runtime_test

import (
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
			{Ref: "api:postgres", Name: "postgres", Image: "postgres:16", Mode: "shared"},
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
		RunOrder: []string{"api:postgres", "api:serve"},
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
}

func (r *fakeProcessRunner) Start(ctx context.Context, target envstarlark.TargetProcess, sink envruntime.EventSink) (envruntime.Process, error) {
	if r.startErr != nil {
		return nil, r.startErr
	}
	r.mu.Lock()
	r.started = append(r.started, target)
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
}

func (r *fakeDependencyRunner) Up(context.Context, string, *envstarlark.RuntimePlan) error {
	r.upCalls++
	return nil
}

func (r *fakeDependencyRunner) Down(context.Context, string, *envstarlark.RuntimePlan) error {
	r.downCalls++
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
}

func (r *eventRecorder) Emit(event envruntime.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
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
