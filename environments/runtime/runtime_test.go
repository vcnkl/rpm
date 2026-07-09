package runtime_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	assert.Equal(t, 10*time.Millisecond, watcher.debounce)
	stopCtxErrs := processes.stopContextErrs("api:serve")
	require.Len(t, stopCtxErrs, 1)
	assert.NoError(t, stopCtxErrs[0])
	assert.NoError(t, deps.downCtxErr)
	require.Len(t, watcher.watches, 1)
	assert.Equal(t, "api:serve", watcher.watches[0].Target)
	assert.Contains(t, watcher.watches[0].Roots, "/repo/api")
}

func TestUpReturnsInvalidLiveReloadDebounce(t *testing.T) {
	processes := &fakeProcessRunner{block: true}
	deps := &fakeDependencyRunner{}
	watcher := &fakeWatcher{}
	plan := testPlan()
	plan.Environment.LiveReload.Debounce = "soon"
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:    processes,
		DependencyRunner: deps,
		ReloadWatcher:    watcher,
		EventSink:        &eventRecorder{},
	})

	err := runner.Up(context.Background(), plan)

	require.Error(t, err)
	assert.ErrorContains(t, err, "live_reload.debounce")
	assert.Zero(t, watcher.calls)
	assert.Equal(t, 1, processes.stopCount("api:serve"))
	assert.Equal(t, 1, deps.downCalls)
}

func TestUpReturnsProcessStartupFailure(t *testing.T) {
	processes := &fakeProcessRunner{startErr: assert.AnError}
	runner := envruntime.NewRunner(envruntime.Options{ProcessRunner: processes})

	err := runner.Up(context.Background(), testPlan())

	require.ErrorIs(t, err, assert.AnError)
}

func TestUpRunsReadinessBetweenOrderedTargets(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	order := []string{}
	processes := &fakeProcessRunner{block: true, order: &order}
	readiness := &fakeReadinessRunner{order: &order}
	events := &eventRecorder{order: &order}
	plan := testPlan()
	plan.Targets = []envstarlark.TargetProcess{
		{Ref: "api:first", Command: "first", ReadinessCmd: "check first"},
		{Ref: "api:second", Command: "second", ReadinessCmd: " \t\n"},
		{Ref: "api:third", Command: "third", ReadinessCmd: "check third"},
	}
	plan.RunOrder = []string{"api:first", "api:second", "api:third"}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:   processes,
		ReadinessRunner: readiness,
		EventSink:       events,
		NoDeps:          true,
		NoReload:        true,
	})

	done := make(chan error, 1)
	go func() {
		done <- runner.Up(ctx, plan)
	}()
	require.Eventually(t, func() bool {
		return len(processes.startedRefs()) == 3 && len(readiness.refs()) == 2
	}, time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-done)

	assert.Equal(t, []string{
		"process api:first",
		"event process_started api:first",
		"readiness api:first",
		"process api:second",
		"event process_started api:second",
		"process api:third",
		"event process_started api:third",
		"readiness api:third",
	}, startupOrder(order))
	assert.Equal(t, []string{"api:first", "api:third"}, readiness.refs())
}

func TestUpPassesWorkingDirectoryAndFinalEnvToReadiness(t *testing.T) {
	processes := &fakeProcessRunner{}
	readiness := &fakeReadinessRunner{}
	plan := testPlan()
	plan.Targets[0].ReadinessCmd = "check ready"
	plan.Targets[0].WorkingDir = "/repo/api/service"
	plan.Targets[0].Env["STATIC_VALUE"] = "configured"
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:   processes,
		ReadinessRunner: readiness,
		DependencyRunner: &fakeDependencyRunner{
			env: map[string]string{"POSTGRES_PORT": "49152"},
		},
		NoReload: true,
	})

	err := runner.Up(context.Background(), plan)

	require.NoError(t, err)
	targets := readiness.targetsSnapshot()
	require.Len(t, targets, 1)
	assert.Equal(t, "/repo/api/service", targets[0].WorkingDir)
	assert.Equal(t, "configured", targets[0].Env["STATIC_VALUE"])
	assert.Equal(t, "49152", targets[0].Env["POSTGRES_PORT"])
}

func TestUpReadinessFailureStopsTargetAndBlocksLaterTargets(t *testing.T) {
	processes := &fakeProcessRunner{block: true}
	readiness := &fakeReadinessRunner{errs: map[string]error{"api:second": assert.AnError}}
	deps := &fakeDependencyRunner{}
	events := &eventRecorder{}
	plan := testPlan()
	plan.Targets = []envstarlark.TargetProcess{
		{Ref: "api:first", Command: "first"},
		{Ref: "api:second", Command: "second", ReadinessCmd: "check second"},
		{Ref: "api:third", Command: "third"},
	}
	plan.RunOrder = []string{"api:first", "api:second", "api:third"}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:    processes,
		ReadinessRunner:  readiness,
		DependencyRunner: deps,
		EventSink:        events,
		NoReload:         true,
	})

	err := runner.Up(context.Background(), plan)

	require.ErrorIs(t, err, assert.AnError)
	assert.ErrorContains(t, err, "api:second readiness check failed")
	assert.Equal(t, []string{"api:first", "api:second"}, processes.startedRefs())
	assert.Equal(t, 1, processes.stopCount("api:first"))
	assert.Equal(t, 1, processes.stopCount("api:second"))
	assert.Zero(t, processes.stopCount("api:third"))
	assert.Equal(t, 1, deps.downCalls)
	exits := events.byType(envruntime.EventProcessExited)
	failed := []envruntime.Event{}
	for _, event := range exits {
		if event.Ref == "api:second" && event.Error != "" {
			failed = append(failed, event)
		}
	}
	require.Len(t, failed, 1)
	assert.Contains(t, failed[0].Error, "api:second readiness check failed")
}

func TestInteractiveReadinessFailureHaltsInitialTargetsUntilQuit(t *testing.T) {
	processes := &fakeProcessRunner{block: true}
	readiness := &fakeReadinessRunner{errs: map[string]error{"api:second": assert.AnError}}
	plan := testPlan()
	plan.Targets = []envstarlark.TargetProcess{
		{Ref: "api:first", Command: "first"},
		{Ref: "api:second", Command: "second", ReadinessCmd: "check second"},
		{Ref: "api:third", Command: "third"},
	}
	plan.RunOrder = []string{"api:first", "api:second", "api:third"}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:   processes,
		ReadinessRunner: readiness,
		NoDeps:          true,
		NoReload:        true,
		Interactive:     true,
	})

	done := make(chan error, 1)
	go func() {
		done <- runner.Up(context.Background(), plan)
	}()
	require.Eventually(t, func() bool {
		return len(readiness.refs()) == 1
	}, time.Second, 10*time.Millisecond)
	require.Never(t, func() bool {
		return len(processes.startedRefs()) > 2
	}, 50*time.Millisecond, 10*time.Millisecond)
	assert.Zero(t, processes.stopCount("api:first"))
	assert.Equal(t, 1, processes.stopCount("api:second"))
	runner.Stop()

	require.ErrorIs(t, <-done, assert.AnError)
	assert.Equal(t, []string{"api:first", "api:second"}, processes.startedRefs())
	assert.Equal(t, 1, processes.stopCount("api:first"))
	stopCtxErrs := processes.stopContextErrs("api:first")
	require.Len(t, stopCtxErrs, 1)
	assert.NoError(t, stopCtxErrs[0])
}

func TestRunnerStopCancelsBlockingReadinessWithoutFailure(t *testing.T) {
	processes := &fakeProcessRunner{block: true}
	readiness := &blockingReadinessRunner{started: make(chan struct{})}
	deps := &fakeDependencyRunner{}
	events := &eventRecorder{}
	plan := testPlan()
	plan.Targets[0].ReadinessCmd = "wait forever"
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:    processes,
		ReadinessRunner:  readiness,
		DependencyRunner: deps,
		EventSink:        events,
		NoReload:         true,
		Interactive:      true,
	})

	done := make(chan error, 1)
	go func() {
		done <- runner.Up(context.Background(), plan)
	}()
	select {
	case <-readiness.started:
	case <-time.After(time.Second):
		t.Fatal("readiness did not start")
	}
	runner.Stop()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("runtime did not stop")
	}

	assert.Equal(t, 1, processes.stopCount("api:serve"))
	stopCtxErrs := processes.stopContextErrs("api:serve")
	require.Len(t, stopCtxErrs, 1)
	assert.NoError(t, stopCtxErrs[0])
	assert.Equal(t, 1, deps.downCalls)
	assert.NoError(t, deps.downCtxErr)
	assert.Empty(t, events.byType(envruntime.EventProcessExited))
	assert.Equal(t, []envruntime.Event{{
		Type: envruntime.EventEnvironmentStopped,
		Ref:  "local",
	}}, events.byType(envruntime.EventEnvironmentStopped))
}

func TestRunnerStopDuringDependencyStartupSuppressesLifecycleEvents(t *testing.T) {
	tests := []struct {
		name          string
		returnError   bool
		expectedDowns int
	}{
		{name: "canceled startup", returnError: true},
		{name: "completed startup", expectedDowns: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := &blockingDependencyStartup{
				started:     make(chan struct{}),
				returnError: tt.returnError,
			}
			events := &eventRecorder{}
			runner := envruntime.NewRunner(envruntime.Options{
				ProcessRunner:    &fakeProcessRunner{},
				DependencyRunner: deps,
				EventSink:        events,
				NoReload:         true,
				Interactive:      true,
			})

			done := make(chan error, 1)
			go func() {
				done <- runner.Up(context.Background(), testPlan())
			}()
			select {
			case <-deps.started:
			case <-time.After(time.Second):
				t.Fatal("dependency startup did not start")
			}
			runner.Stop()

			require.NoError(t, <-done)
			assert.Empty(t, events.byType(envruntime.EventDependencyStarted))
			assert.Empty(t, events.byType(envruntime.EventDependencyFailed))
			assert.Equal(t, tt.expectedDowns, deps.downCalls)
			assert.NoError(t, deps.downCtxErr)
		})
	}
}

func TestRunnerStopDuringBeforeTargetStartSuppressesFailureAndLaterStarts(t *testing.T) {
	processes := &blockingProcessStart{started: make(chan struct{})}
	events := &eventRecorder{}
	plan := testPlan()
	plan.Dependencies = nil
	plan.BeforeTargets = []envstarlark.TargetProcess{
		{Ref: "api:first", Command: "first"},
		{Ref: "api:second", Command: "second"},
	}
	plan.Targets = nil
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner: processes,
		EventSink:     events,
		NoReload:      true,
		Interactive:   true,
	})

	done := make(chan error, 1)
	go func() {
		done <- runner.Up(context.Background(), plan)
	}()
	select {
	case <-processes.started:
	case <-time.After(time.Second):
		t.Fatal("process startup did not start")
	}
	runner.Stop()

	require.NoError(t, <-done)
	assert.Equal(t, []string{"api:first"}, processes.startedRefs())
	assert.Empty(t, events.byType(envruntime.EventProcessStarted))
	assert.Empty(t, events.byType(envruntime.EventProcessExited))
}

func TestRunnerStopDuringBeforeTargetExecutionUsesLiveCleanupContext(t *testing.T) {
	processes := &fakeProcessRunner{block: true}
	events := &eventRecorder{}
	plan := testPlan()
	plan.Dependencies = nil
	plan.BeforeTargets = []envstarlark.TargetProcess{
		{Ref: "api:first", Command: "first"},
		{Ref: "api:second", Command: "second"},
	}
	plan.Targets = nil
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner: processes,
		EventSink:     events,
		NoReload:      true,
		Interactive:   true,
	})

	done := make(chan error, 1)
	go func() {
		done <- runner.Up(context.Background(), plan)
	}()
	require.Eventually(t, func() bool {
		return len(processes.startedRefs()) == 1
	}, time.Second, 10*time.Millisecond)
	runner.Stop()

	require.NoError(t, <-done)
	assert.Equal(t, []string{"api:first"}, processes.startedRefs())
	stopCtxErrs := processes.stopContextErrs("api:first")
	require.Len(t, stopCtxErrs, 1)
	assert.NoError(t, stopCtxErrs[0])
	assert.Empty(t, events.byType(envruntime.EventProcessExited))
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

func TestUpRunsDepTargetsAfterBeforeTargetsBeforeTargets(t *testing.T) {
	order := []string{}
	processes := &fakeProcessRunner{order: &order}
	deps := &fakeDependencyRunner{order: &order, env: map[string]string{"POSTGRES_PORT": "49152"}}
	events := &eventRecorder{}
	plan := testPlan()
	plan.BeforeTargets = []envstarlark.TargetProcess{
		{Ref: "api:migrate", Command: "echo migrate", WorkingDir: "/repo/api"},
	}
	plan.DepTargets = []envstarlark.TargetProcess{
		{Ref: "api:codegen", Command: "echo codegen", WorkingDir: "/repo/api"},
		{Ref: "api:app_build", Command: "echo build", WorkingDir: "/repo/api"},
	}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:    processes,
		DependencyRunner: deps,
		EventSink:        events,
		NoReload:         true,
	})

	err := runner.Up(context.Background(), plan)

	require.NoError(t, err)
	assert.Equal(t, []string{
		"deps up",
		"process api:migrate",
		"process api:codegen",
		"process api:app_build",
		"process api:serve",
		"deps down",
	}, order)
	require.Len(t, processes.started, 4)
	assert.Equal(t, "49152", processes.started[1].Env["POSTGRES_PORT"])
	assert.Equal(t, "49152", processes.started[2].Env["POSTGRES_PORT"])
	declaredKinds := map[string]string{}
	for _, event := range events.byType(envruntime.EventUnitDeclared) {
		declaredKinds[event.Ref] = event.Kind
	}
	assert.Equal(t, "dep_target", declaredKinds["api:codegen"])
	assert.Equal(t, "dep_target", declaredKinds["api:app_build"])
}

func TestUpStopsDependenciesWhenDepTargetFailsNonInteractive(t *testing.T) {
	order := []string{}
	processes := &fakeProcessRunner{
		order:    &order,
		waitErrs: map[string]error{"api:app_build": assert.AnError},
	}
	deps := &fakeDependencyRunner{order: &order}
	events := &eventRecorder{}
	plan := testPlan()
	plan.DepTargets = []envstarlark.TargetProcess{
		{Ref: "api:app_build", Command: "exit 1", WorkingDir: "/repo/api"},
	}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:    processes,
		DependencyRunner: deps,
		EventSink:        events,
		NoReload:         true,
	})

	err := runner.Up(context.Background(), plan)

	require.ErrorIs(t, err, assert.AnError)
	assert.Equal(t, []string{"deps up", "process api:app_build", "deps down"}, order)
	assert.Equal(t, []string{"api:app_build"}, processes.startedRefs())
	assert.Equal(t, 1, deps.downCalls)
	assert.Contains(t, events.types(), envruntime.EventEnvironmentStopped)
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

func TestUpSubstitutesDependencyEnvInDotenvValues(t *testing.T) {
	processes := &fakeProcessRunner{}
	plan := testPlan()
	plan.Targets[0].Env["MONGODB_URI"] = "mongodb://localhost:${MONGODB_PORT}/mydb"
	plan.Targets[0].Env["STATIC_URI"] = "mongodb://localhost:${MONGODB_PORT}/static"
	plan.Targets[0].DotenvEnv = map[string]string{
		"MONGODB_URI": "mongodb://localhost:${MONGODB_PORT}/mydb",
	}
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
	assert.Equal(t, "mongodb://localhost:49152/mydb", processes.started[0].Env["MONGODB_URI"])
	assert.Equal(t, "mongodb://localhost:${MONGODB_PORT}/static", processes.started[0].Env["STATIC_URI"])
	assert.Equal(t, "49152", processes.started[0].Env["MONGODB_PORT"])
}

func TestUpLeavesMissingDotenvSubstitutionLiteral(t *testing.T) {
	processes := &fakeProcessRunner{}
	plan := testPlan()
	plan.Targets[0].Env["MONGODB_URI"] = "mongodb://localhost:${MONGODB_PORT}/mydb"
	plan.Targets[0].DotenvEnv = map[string]string{
		"MONGODB_URI": "mongodb://localhost:${MONGODB_PORT}/mydb",
	}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner: processes,
		DependencyRunner: &fakeDependencyRunner{
			env: map[string]string{"POSTGRES_PORT": "49152"},
		},
		NoReload: true,
	})

	err := runner.Up(context.Background(), plan)

	require.NoError(t, err)
	require.Len(t, processes.started, 1)
	assert.Equal(t, "mongodb://localhost:${MONGODB_PORT}/mydb", processes.started[0].Env["MONGODB_URI"])
}

func TestUpSubstitutedDotenvValuePersistsAcrossRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processes := &fakeProcessRunner{block: true}
	actions := make(chan envruntime.ControlAction, 1)
	plan := testPlan()
	plan.Targets[0].Env["MONGODB_URI"] = "mongodb://localhost:${MONGODB_PORT}/mydb"
	plan.Targets[0].DotenvEnv = map[string]string{
		"MONGODB_URI": "mongodb://localhost:${MONGODB_PORT}/mydb",
	}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner: processes,
		DependencyRunner: &fakeDependencyRunner{
			env: map[string]string{"MONGODB_PORT": "49152"},
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
	assert.Equal(t, "mongodb://localhost:49152/mydb", processes.started[0].Env["MONGODB_URI"])
	assert.Equal(t, "mongodb://localhost:49152/mydb", processes.started[1].Env["MONGODB_URI"])
}

func TestUpDependencyEnvOverridesSubstitutedDotenvValue(t *testing.T) {
	processes := &fakeProcessRunner{}
	plan := testPlan()
	plan.Targets[0].Env["MONGODB_PORT"] = "${POSTGRES_PORT}"
	plan.Targets[0].DotenvEnv = map[string]string{
		"MONGODB_PORT": "${POSTGRES_PORT}",
	}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner: processes,
		DependencyRunner: &fakeDependencyRunner{
			env: map[string]string{"MONGODB_PORT": "49152", "POSTGRES_PORT": "5432"},
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

func TestUpSubstitutesDependencyEnvInBeforeTargetDotenvValues(t *testing.T) {
	processes := &fakeProcessRunner{}
	plan := testPlan()
	plan.BeforeTargets = []envstarlark.TargetProcess{{
		Ref:        "api:migrate",
		Command:    "echo migrate",
		WorkingDir: "/repo/api",
		Env:        map[string]string{"MONGODB_URI": "mongodb://localhost:${MONGODB_PORT}/mydb"},
		DotenvEnv:  map[string]string{"MONGODB_URI": "mongodb://localhost:${MONGODB_PORT}/mydb"},
	}}
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
	assert.Equal(t, "mongodb://localhost:49152/mydb", processes.started[0].Env["MONGODB_URI"])
}

func TestUpSubstitutesDependencyEnvInBeforeTargetDotenvFiles(t *testing.T) {
	workDir := t.TempDir()
	envFile := filepath.Join(workDir, ".env")
	require.NoError(t, os.WriteFile(envFile, []byte("MONGODB_URI=mongodb://localhost:${MONGODB_PORT}/mydb\n"), 0644))
	events := &eventRecorder{}
	plan := testPlan()
	plan.BeforeTargets = []envstarlark.TargetProcess{{
		Ref:         "api:migrate",
		Command:     `printf '%s\n' "$MONGODB_URI"`,
		WorkingDir:  workDir,
		DotenvFiles: []string{envFile},
	}}
	plan.Targets = []envstarlark.TargetProcess{}
	plan.Watches = []envstarlark.Watch{}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner: envruntime.NewShellProcessRunner("/bin/sh", &bytes.Buffer{}, &bytes.Buffer{}),
		DependencyRunner: &fakeDependencyRunner{
			env: map[string]string{"MONGODB_PORT": "49152"},
		},
		EventSink: events,
		NoReload:  true,
	})

	err := runner.Up(context.Background(), plan)

	require.NoError(t, err)
	assert.Equal(t, []string{"mongodb://localhost:49152/mydb"}, events.outputLines("api:migrate", "stdout"))
}

func TestUpSubstitutesDependencyEnvInTargetDotenvFiles(t *testing.T) {
	bundleRoot := t.TempDir()
	envFile := filepath.Join(bundleRoot, ".env")
	localFile := filepath.Join(bundleRoot, ".env.local")
	require.NoError(t, os.WriteFile(envFile, []byte("MONGODB_URI=mongodb://localhost:${MONGODB_PORT}/base\n"), 0644))
	require.NoError(t, os.WriteFile(localFile, []byte("MONGODB_URI=mongodb://localhost:${MONGODB_PORT}/local\n"), 0644))
	processes := &fakeProcessRunner{}
	plan := testPlan()
	plan.Targets[0].DotenvFiles = []string{envFile, localFile}
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
	assert.Equal(t, "mongodb://localhost:49152/local", processes.started[0].Env["MONGODB_URI"])
}

func TestUpDefinesDependencyEnvInsideReferencingDotenvFiles(t *testing.T) {
	bundleRoot := t.TempDir()
	envFile := filepath.Join(bundleRoot, ".env")
	body := "#MONGODB_PORT=27017\nMONGODB_URI=mongodb://localhost:${MONGODB_PORT}/mydb\n"
	require.NoError(t, os.WriteFile(envFile, []byte(body), 0644))
	processes := &fakeProcessRunner{}
	plan := testPlan()
	plan.Targets[0].DotenvFiles = []string{envFile}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner: processes,
		DependencyRunner: &fakeDependencyRunner{
			env: map[string]string{"MONGODB_PORT": "49152"},
		},
		NoReload: true,
	})

	err := runner.Up(context.Background(), plan)

	require.NoError(t, err)
	data, err := os.ReadFile(envFile)
	require.NoError(t, err)
	assert.Contains(t, string(data), "MONGODB_PORT=49152\n", "dependency port is defined in the file's own scope")
	assert.Contains(t, string(data), body, "user content stays intact")
	require.Len(t, processes.started, 1)
	assert.Equal(t, "mongodb://localhost:49152/mydb", processes.started[0].Env["MONGODB_URI"])
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

func TestUpDependencyStartupFailurePreventsBeforeTargets(t *testing.T) {
	processes := &fakeProcessRunner{}
	deps := &fakeDependencyRunner{upErr: assert.AnError}
	plan := testPlan()
	plan.BeforeTargets = []envstarlark.TargetProcess{
		{Ref: "api:migrate", Command: "echo migrate", WorkingDir: "/repo/api"},
	}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:    processes,
		DependencyRunner: deps,
		NoReload:         true,
	})

	err := runner.Up(context.Background(), plan)

	require.ErrorIs(t, err, assert.AnError)
	assert.Equal(t, 1, deps.upCalls)
	assert.Empty(t, processes.startedRefs())
}

func TestUpScopedDependencyStartupFailureMarksOnlyFailingDependency(t *testing.T) {
	processes := &fakeProcessRunner{}
	events := &eventRecorder{}
	deps := &fakeDependencyRunner{upErr: envruntime.NewDependencyError("mongodb", assert.AnError)}
	plan := testPlan()
	plan.Dependencies = []envstarlark.Dependency{
		{Ref: "mongodb", Name: "mongodb", Image: "mongo:8.0.23-noble"},
		{Ref: "postgres", Name: "postgres", Image: "postgres:16"},
		{Ref: "rabbitmq", Name: "rabbitmq", Image: "rabbitmq:4.1.3"},
		{Ref: "redis", Name: "redis", Image: "redis:7"},
	}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:    processes,
		DependencyRunner: deps,
		EventSink:        events,
		NoReload:         true,
	})

	err := runner.Up(context.Background(), plan)

	require.ErrorIs(t, err, assert.AnError)
	assert.Empty(t, processes.startedRefs())
	assert.Equal(t, []envruntime.Event{{
		Type:  envruntime.EventDependencyFailed,
		Ref:   "mongodb",
		Error: assert.AnError.Error(),
	}}, events.byType(envruntime.EventDependencyFailed))
}

func TestUpUnscopedDependencyStartupFailureMarksAggregateDependency(t *testing.T) {
	events := &eventRecorder{}
	deps := &fakeDependencyRunner{upErr: assert.AnError}
	plan := testPlan()
	plan.Dependencies = []envstarlark.Dependency{
		{Ref: "mongodb", Name: "mongodb", Image: "mongo:8.0.23-noble"},
		{Ref: "postgres", Name: "postgres", Image: "postgres:16"},
	}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:    &fakeProcessRunner{},
		DependencyRunner: deps,
		EventSink:        events,
		NoReload:         true,
	})

	err := runner.Up(context.Background(), plan)

	require.ErrorIs(t, err, assert.AnError)
	assert.Equal(t, []envruntime.Event{{
		Type:  envruntime.EventDependencyFailed,
		Ref:   "dependencies",
		Error: assert.AnError.Error(),
	}}, events.byType(envruntime.EventDependencyFailed))
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
		Interactive:   true,
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
	runner.Stop()

	require.ErrorIs(t, <-done, assert.AnError)
	assert.Equal(t, 1, processes.stopCount("api:serve"))
}

func TestRestartAndRestartAllDoNotRunReadinessAgain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processes := &fakeProcessRunner{block: true}
	readiness := &fakeReadinessRunner{}
	plan := testPlan()
	plan.Targets = []envstarlark.TargetProcess{
		{Ref: "api:first", Command: "first", ReadinessCmd: "check first"},
		{Ref: "api:second", Command: "second", ReadinessCmd: "check second"},
	}
	plan.RunOrder = []string{"api:first", "api:second"}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:   processes,
		ReadinessRunner: readiness,
		NoDeps:          true,
		NoReload:        true,
	})

	done := make(chan error, 1)
	go func() {
		done <- runner.Up(ctx, plan)
	}()
	require.Eventually(t, func() bool {
		return len(processes.startedRefs()) == 2 && len(readiness.refs()) == 2
	}, time.Second, 10*time.Millisecond)
	require.NoError(t, runner.Restart(ctx, "api:first"))
	require.NoError(t, runner.RestartAll(ctx))
	require.Eventually(t, func() bool {
		return len(processes.startedRefs()) == 5
	}, time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-done)

	assert.Equal(t, []string{"api:first", "api:second"}, readiness.refs())
}

func TestWatcherChangeRestartsAffectedProcessOnly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processes := &fakeProcessRunner{block: true}
	readiness := &fakeReadinessRunner{}
	watcher := &fakeWatcher{trigger: true, block: true, ready: make(chan struct{})}
	plan := testPlan()
	plan.Targets[0].ReadinessCmd = "check ready"
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:   processes,
		ReadinessRunner: readiness,
		ReloadWatcher:   watcher,
		EventSink:       &eventRecorder{},
	})

	done := make(chan error, 1)
	go func() {
		done <- runner.Up(ctx, plan)
	}()
	require.Eventually(t, func() bool {
		return len(processes.startedRefs()) >= 2
	}, time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-done)

	assert.Equal(t, []string{"api:serve", "api:serve"}, processes.startedRefs())
	assert.Equal(t, []string{"api:serve"}, readiness.refs())
	assert.GreaterOrEqual(t, processes.stopCount("api:serve"), 1)
}

func TestControlActionRestartsSelectedTarget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processes := &fakeProcessRunner{block: true}
	readiness := &fakeReadinessRunner{}
	actions := make(chan envruntime.ControlAction, 1)
	plan := testPlan()
	plan.Targets[0].ReadinessCmd = "check ready"
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:   processes,
		ReadinessRunner: readiness,
		EventSink:       &eventRecorder{},
		ControlActions:  actions,
		NoReload:        true,
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

	assert.Equal(t, []string{"api:serve", "api:serve"}, processes.startedRefs())
	assert.Equal(t, []string{"api:serve"}, readiness.refs())
	assert.GreaterOrEqual(t, processes.stopCount("api:serve"), 1)
}

func TestControlActionRestartDoesNotFlickerFailedWhenStopErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processes := &fakeProcessRunner{
		block:    true,
		waitErrs: map[string]error{"api:serve": assert.AnError},
	}
	actions := make(chan envruntime.ControlAction, 1)
	events := &eventRecorder{}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner:  processes,
		EventSink:      events,
		ControlActions: actions,
		NoDeps:         true,
		NoReload:       true,
		Interactive:    true,
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
		return len(processes.startedRefs()) == 2 && processes.stopCount("api:serve") == 1
	}, time.Second, 10*time.Millisecond)

	failedExit := func() bool {
		for _, event := range events.byType(envruntime.EventProcessExited) {
			if event.Ref == "api:serve" && event.Error != "" {
				return true
			}
		}
		return false
	}
	require.Never(t, failedExit, 200*time.Millisecond, 10*time.Millisecond)

	cancel()
	require.NoError(t, <-done)

	reloads := events.byType(envruntime.EventReloadCompleted)
	require.NotEmpty(t, reloads)
	assert.Empty(t, reloads[len(reloads)-1].Error)
}

func TestRunnerStopStopsRunningEnvironment(t *testing.T) {
	processes := &fakeProcessRunner{block: true}
	runner := envruntime.NewRunner(envruntime.Options{
		ProcessRunner: processes,
		EventSink:     &eventRecorder{},
		NoReload:      true,
		Interactive:   true,
	})

	done := make(chan error, 1)
	go func() {
		done <- runner.Up(context.Background(), testPlan())
	}()
	require.Eventually(t, func() bool {
		return len(processes.startedRefs()) == 1
	}, time.Second, 10*time.Millisecond)
	runner.Stop()

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
	mu          sync.Mutex
	started     []envstarlark.TargetProcess
	stopped     map[string]int
	stopCtxErrs map[string][]error
	startErr    error
	block       bool
	blockRefs   map[string]bool
	waitErrs    map[string]error
	order       *[]string
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
	if r.stopCtxErrs == nil {
		r.stopCtxErrs = make(map[string][]error)
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

func (r *fakeProcessRunner) stopContextErrs(ref string) []error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]error{}, r.stopCtxErrs[ref]...)
}

type fakeReadinessRunner struct {
	mu      sync.Mutex
	targets []envstarlark.TargetProcess
	errs    map[string]error
	order   *[]string
}

func (r *fakeReadinessRunner) Run(_ context.Context, target envstarlark.TargetProcess, _ envruntime.EventSink) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.targets = append(r.targets, target)
	if r.order != nil {
		*r.order = append(*r.order, "readiness "+target.Ref)
	}
	return r.errs[target.Ref]
}

func (r *fakeReadinessRunner) refs() []string {
	targets := r.targetsSnapshot()
	refs := make([]string, 0, len(targets))
	for _, target := range targets {
		refs = append(refs, target.Ref)
	}
	return refs
}

func (r *fakeReadinessRunner) targetsSnapshot() []envstarlark.TargetProcess {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]envstarlark.TargetProcess{}, r.targets...)
}

type blockingReadinessRunner struct {
	started chan struct{}
}

func (r *blockingReadinessRunner) Run(ctx context.Context, _ envstarlark.TargetProcess, _ envruntime.EventSink) error {
	close(r.started)
	<-ctx.Done()
	return ctx.Err()
}

type blockingDependencyStartup struct {
	started     chan struct{}
	returnError bool
	downCalls   int
	downCtxErr  error
}

func (r *blockingDependencyStartup) Up(ctx context.Context, _ string, _ *envstarlark.RuntimePlan) (envruntime.DependencyStartup, error) {
	close(r.started)
	<-ctx.Done()
	if r.returnError {
		return envruntime.DependencyStartup{}, ctx.Err()
	}
	return envruntime.DependencyStartup{}, nil
}

func (r *blockingDependencyStartup) Down(ctx context.Context, _ string, _ *envstarlark.RuntimePlan) error {
	r.downCalls++
	r.downCtxErr = ctx.Err()
	return nil
}

type blockingProcessStart struct {
	mu      sync.Mutex
	started chan struct{}
	refs    []string
}

func (r *blockingProcessStart) Start(ctx context.Context, target envstarlark.TargetProcess, _ envruntime.EventSink) (envruntime.Process, error) {
	r.mu.Lock()
	r.refs = append(r.refs, target.Ref)
	close(r.started)
	r.mu.Unlock()
	<-ctx.Done()
	return nil, ctx.Err()
}

func (r *blockingProcessStart) startedRefs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.refs...)
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

func (p *fakeProcess) Stop(ctx context.Context) error {
	p.runner.mu.Lock()
	p.runner.stopped[p.ref]++
	p.runner.stopCtxErrs[p.ref] = append(p.runner.stopCtxErrs[p.ref], ctx.Err())
	p.runner.mu.Unlock()
	p.once.Do(func() { close(p.done) })
	return nil
}

type fakeDependencyRunner struct {
	upCalls    int
	downCalls  int
	downCtxErr error
	order      *[]string
	env        map[string]string
	upErr      error
}

func (r *fakeDependencyRunner) Up(context.Context, string, *envstarlark.RuntimePlan) (envruntime.DependencyStartup, error) {
	r.upCalls++
	if r.order != nil {
		*r.order = append(*r.order, "deps up")
	}
	if r.upErr != nil {
		return envruntime.DependencyStartup{}, r.upErr
	}
	return envruntime.DependencyStartup{Env: r.env}, nil
}

func (r *fakeDependencyRunner) Down(ctx context.Context, _ string, _ *envstarlark.RuntimePlan) error {
	r.downCalls++
	r.downCtxErr = ctx.Err()
	if r.order != nil {
		*r.order = append(*r.order, "deps down")
	}
	return nil
}

type fakeWatcher struct {
	calls    int
	watches  []envstarlark.Watch
	debounce time.Duration
	trigger  bool
	block    bool
	ready    chan struct{}
}

func (w *fakeWatcher) Watch(ctx context.Context, watches []envstarlark.Watch, debounce time.Duration, onChange func(target string, path string)) error {
	w.calls++
	w.watches = append(w.watches, watches...)
	w.debounce = debounce
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

func (r *eventRecorder) byType(eventType string) []envruntime.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	events := []envruntime.Event{}
	for _, event := range r.events {
		if event.Type == eventType {
			events = append(events, event)
		}
	}
	return events
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

func startupOrder(order []string) []string {
	result := []string{}
	for _, item := range order {
		if strings.HasPrefix(item, "process ") || strings.HasPrefix(item, "readiness ") || strings.HasPrefix(item, "event process_started ") {
			result = append(result, item)
		}
	}
	return result
}
