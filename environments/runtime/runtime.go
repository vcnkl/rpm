package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/errors"
	envstarlark "github.com/vcnkl/rpm/environments/starlark"
	rpmexec "github.com/vcnkl/rpm/exec"
)

type PlanLoader interface {
	LoadPlan(ctx context.Context, blueprint string) (*envstarlark.RuntimePlan, error)
}

type ProcessRunner interface {
	Start(ctx context.Context, target envstarlark.TargetProcess, sink EventSink) (Process, error)
}

type ReadinessRunner interface {
	Run(ctx context.Context, target envstarlark.TargetProcess, sink EventSink) error
}

type Process interface {
	Wait() error
	Stop(ctx context.Context) error
}

type DependencyRunner interface {
	Up(ctx context.Context, blueprint string, plan *envstarlark.RuntimePlan) (DependencyStartup, error)
	Down(ctx context.Context, blueprint string, plan *envstarlark.RuntimePlan) error
}

type DependencyStartup struct {
	Env map[string]string
}

type DependencyError struct {
	Ref string
	Err error
}

func NewDependencyError(ref string, err error) error {
	if err == nil {
		return nil
	}
	return DependencyError{Ref: ref, Err: err}
}

func (e DependencyError) Error() string {
	return e.Err.Error()
}

func (e DependencyError) Unwrap() error {
	return e.Err
}

type ReloadWatcher interface {
	Watch(ctx context.Context, watches []envstarlark.Watch, debounce time.Duration, onChange func(target string, path string)) error
}

type EventSink interface {
	Emit(Event)
}

type ControlAction struct {
	Type string
	Ref  string
}

type Event struct {
	Type    string `json:"type"`
	Ref     string `json:"ref,omitempty"`
	Bundle  string `json:"bundle,omitempty"`
	Name    string `json:"name,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Status  string `json:"status,omitempty"`
	Path    string `json:"path,omitempty"`
	Line    string `json:"line,omitempty"`
	Error   string `json:"error,omitempty"`
	Stream  string `json:"stream,omitempty"`
	Message string `json:"message,omitempty"`
}

const (
	EventUnitDeclared       = "unit_declared"
	EventProcessStarted     = "process_started"
	EventProcessOutput      = "process_output"
	EventProcessExited      = "process_exited"
	EventDependencyStarted  = "dependency_started"
	EventDependencyFailed   = "dependency_failed"
	EventReloadStarted      = "reload_started"
	EventReloadCompleted    = "reload_completed"
	EventEnvironmentStopped = "environment_stopped"

	ActionRestartTarget = "restart_target"
	ActionRestartAll    = "restart_all"
)

type Options struct {
	ProcessRunner    ProcessRunner
	ReadinessRunner  ReadinessRunner
	DependencyRunner DependencyRunner
	ReloadWatcher    ReloadWatcher
	EventSink        EventSink
	ControlActions   <-chan ControlAction
	NoDeps           bool
	NoReload         bool
	Interactive      bool
}

type Runner struct {
	processes  map[string]Process
	plan       *envstarlark.RuntimePlan
	done       chan processExit
	mu         sync.Mutex
	opts       Options
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	closing    chan struct{}
	closeOnce  sync.Once
	stopping   bool
	restarting int
}

func NewRunner(opts Options) *Runner {
	if opts.EventSink == nil {
		opts.EventSink = discardSink{}
	}
	return &Runner{opts: opts, processes: make(map[string]Process), done: make(chan processExit), closing: make(chan struct{})}
}

func (r *Runner) setCancel(cancel context.CancelFunc) {
	r.mu.Lock()
	r.cancel = cancel
	stopping := r.stopping
	r.mu.Unlock()
	if stopping {
		cancel()
	}
}

func (r *Runner) setPlan(plan *envstarlark.RuntimePlan) {
	r.mu.Lock()
	r.plan = plan
	r.mu.Unlock()
}

func (r *Runner) getPlan() *envstarlark.RuntimePlan {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.plan
}

func (r *Runner) shutdown(ctx context.Context) {
	r.mu.Lock()
	r.stopping = true
	processes := make(map[string]Process, len(r.processes))
	for ref, process := range r.processes {
		processes[ref] = process
	}
	r.mu.Unlock()
	r.closeOnce.Do(func() { close(r.closing) })
	for _, ref := range sortedProcessRefs(processes) {
		if process := processes[ref]; process != nil {
			_ = process.Stop(ctx)
		}
	}
	r.wg.Wait()
}

func (r *Runner) emitExit(exit processExit) {
	select {
	case r.done <- exit:
	case <-r.closing:
	}
}

type startupContext struct {
	plan        *envstarlark.RuntimePlan
	startedDeps bool
	watchCancel context.CancelFunc
	recordedErr error
}

type startupStep struct {
	name string
	run  func(context.Context, *startupContext) error
}

func (r *Runner) startupSteps() []startupStep {
	return []startupStep{
		{name: "dependencies", run: r.startDependencies},
		{name: "before_targets", run: r.runBeforeTargets},
		{name: "dep_targets", run: r.runDepTargets},
		{name: "targets", run: r.startTargets},
		{name: "reload_watcher", run: r.startReloadWatcher},
	}
}

func (r *Runner) startDependencies(ctx context.Context, state *startupContext) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if r.opts.DependencyRunner == nil || r.opts.NoDeps || len(state.plan.Dependencies) == 0 {
		return nil
	}
	startup, err := r.opts.DependencyRunner.Up(ctx, state.plan.Environment.Name, state.plan)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.emitDependencyFailure(state.plan, err)
		return err
	}
	state.plan = planWithDependencyEnv(state.plan, startup.Env)
	state.startedDeps = true
	r.setPlan(state.plan)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for _, dep := range state.plan.Dependencies {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.opts.EventSink.Emit(Event{Type: EventDependencyStarted, Ref: dep.Ref})
	}
	return nil
}

func (r *Runner) runBeforeTargets(ctx context.Context, state *startupContext) error {
	for _, target := range state.plan.BeforeTargets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := r.runBlockingTarget(ctx, target); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) runDepTargets(ctx context.Context, state *startupContext) error {
	for _, target := range state.plan.DepTargets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := r.runBlockingTarget(ctx, target); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) startTargets(ctx context.Context, state *startupContext) error {
	for _, ref := range targetOrder(state.plan) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		target, ok := targetByRef(state.plan, ref)
		if !ok {
			continue
		}
		if err := r.startTarget(ctx, target); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if r.opts.Interactive {
				state.recordedErr = firstErr(state.recordedErr, err)
				continue
			}
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := r.runReadiness(ctx, target); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) startReloadWatcher(ctx context.Context, state *startupContext) error {
	if !r.reloadEnabled(state.plan) || r.opts.ReloadWatcher == nil {
		return nil
	}
	watches := enabledWatches(state.plan)
	if len(watches) == 0 {
		return nil
	}
	debounce, err := time.ParseDuration(state.plan.Environment.LiveReload.Debounce)
	if err != nil {
		return fmt.Errorf("invalid live_reload.debounce: %w", err)
	}
	watchCtx, cancel := context.WithCancel(ctx)
	state.watchCancel = cancel
	plan := state.plan
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		if err = r.opts.ReloadWatcher.Watch(watchCtx, watches, debounce, func(ref string, path string) {
			r.reloadTarget(ctx, plan, ref, path)
		}); err != nil && ctx.Err() == nil {
			r.emitExit(processExit{err: err})
		}
	}()
	return nil
}

func (r *Runner) Up(ctx context.Context, plan *envstarlark.RuntimePlan) error {
	ctx, cancel := context.WithCancel(ctx)
	r.setCancel(cancel)
	defer cancel()
	defer func() { r.shutdown(context.WithoutCancel(ctx)) }()
	r.declareUnits(plan)
	r.setPlan(plan)

	state := &startupContext{plan: plan, watchCancel: func() {}}
	for _, step := range r.startupSteps() {
		if ctx.Err() != nil {
			return r.stopStartup(ctx, state)
		}
		err := step.run(ctx, state)
		if ctx.Err() != nil {
			return r.stopStartup(ctx, state)
		}
		if err != nil {
			if r.opts.Interactive {
				return r.waitForQuitAfterError(ctx, state.plan, err, state.startedDeps)
			}
			r.stopProcesses(ctx)
			r.stopDependencies(ctx, state.plan, state.startedDeps)
			r.opts.EventSink.Emit(Event{Type: EventEnvironmentStopped, Ref: state.plan.Environment.Name})
			return err
		}
	}
	defer state.watchCancel()

	plan = state.plan
	startedDeps := state.startedDeps
	recordedErr := state.recordedErr

	for {
		if r.idle() && recordedErr == nil {
			r.stopDependencies(ctx, plan, startedDeps)
			r.opts.EventSink.Emit(Event{Type: EventEnvironmentStopped, Ref: plan.Environment.Name})
			return nil
		}
		select {
		case <-ctx.Done():
			cleanupCtx := context.WithoutCancel(ctx)
			r.stopProcesses(cleanupCtx)
			r.stopDependencies(cleanupCtx, plan, startedDeps)
			r.opts.EventSink.Emit(Event{Type: EventEnvironmentStopped, Ref: plan.Environment.Name})
			return recordedErr
		case action, ok := <-r.opts.ControlActions:
			if !ok {
				continue
			}
			r.handleControlAction(ctx, plan, action)
		case exit := <-r.done:
			if exit.ref != "" {
				if !r.removeProcess(exit.ref, exit.process) {
					continue
				}
				event := Event{Type: EventProcessExited, Ref: exit.ref}
				if exit.err != nil {
					event.Error = exit.err.Error()
				}
				r.opts.EventSink.Emit(event)
			}
			if exit.err != nil {
				if r.opts.Interactive {
					recordedErr = firstErr(recordedErr, exit.err)
					continue
				}
				r.stopProcesses(ctx)
				r.stopDependencies(ctx, plan, startedDeps)
				r.opts.EventSink.Emit(Event{Type: EventEnvironmentStopped, Ref: plan.Environment.Name})
				return exit.err
			}
		}
	}
}

func (r *Runner) Restart(ctx context.Context, ref string) error {
	plan := r.getPlan()
	if plan == nil {
		return fmt.Errorf("runtime is not running")
	}
	target, ok := targetByRef(plan, ref)
	if !ok {
		return fmt.Errorf("unknown target %q", ref)
	}
	if process := r.processSnapshot()[ref]; process != nil {
		if err := process.Stop(ctx); err != nil {
			return err
		}
	}
	r.opts.EventSink.Emit(Event{Type: EventReloadStarted, Ref: ref})
	if err := r.startTarget(ctx, target); err != nil {
		r.opts.EventSink.Emit(Event{Type: EventReloadCompleted, Ref: ref, Error: err.Error()})
		return err
	}
	r.opts.EventSink.Emit(Event{Type: EventReloadCompleted, Ref: ref})
	return nil
}

func (r *Runner) RestartAll(ctx context.Context) error {
	plan := r.getPlan()
	if plan == nil {
		return fmt.Errorf("runtime is not running")
	}
	for _, ref := range targetOrder(plan) {
		if _, ok := targetByRef(plan, ref); ok {
			if err := r.Restart(ctx, ref); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Runner) Stop() {
	r.mu.Lock()
	r.stopping = true
	if r.cancel != nil {
		r.cancel()
	}
	r.mu.Unlock()
}

func (r *Runner) stopStartup(ctx context.Context, state *startupContext) error {
	cleanupCtx := context.WithoutCancel(ctx)
	r.stopProcesses(cleanupCtx)
	r.stopDependencies(cleanupCtx, state.plan, state.startedDeps)
	r.opts.EventSink.Emit(Event{Type: EventEnvironmentStopped, Ref: state.plan.Environment.Name})
	return state.recordedErr
}

func (r *Runner) handleControlAction(ctx context.Context, plan *envstarlark.RuntimePlan, action ControlAction) {
	switch action.Type {
	case ActionRestartTarget:
		if action.Ref != "" {
			r.reloadTarget(ctx, plan, action.Ref, "tui")
		}
	case ActionRestartAll:
		for _, ref := range targetOrder(plan) {
			r.reloadTarget(ctx, plan, ref, "tui")
		}
	}
}

type processExit struct {
	ref     string
	process Process
	err     error
}

func (r *Runner) idle() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.processes) == 0 && r.restarting == 0
}

func (r *Runner) beginRestart() {
	r.mu.Lock()
	r.restarting++
	r.mu.Unlock()
}

func (r *Runner) endRestart() {
	r.mu.Lock()
	r.restarting--
	r.mu.Unlock()
}

func (r *Runner) removeProcess(ref string, process Process) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.processes[ref] == process {
		delete(r.processes, ref)
		return true
	}
	return false
}

func (r *Runner) processSnapshot() map[string]Process {
	r.mu.Lock()
	defer r.mu.Unlock()
	processes := make(map[string]Process, len(r.processes))
	for ref, process := range r.processes {
		processes[ref] = process
	}
	return processes
}

func (r *Runner) stopProcesses(ctx context.Context) {
	processes := r.processSnapshot()
	for _, ref := range sortedProcessRefs(processes) {
		if process := processes[ref]; process != nil {
			_ = process.Stop(ctx)
			r.removeProcess(ref, process)
		}
	}
}

func (r *Runner) stopProcess(ctx context.Context, ref string) {
	if process := r.processSnapshot()[ref]; process != nil {
		_ = process.Stop(ctx)
		r.removeProcess(ref, process)
	}
}

func (r *Runner) Down(ctx context.Context, plan *envstarlark.RuntimePlan) error {
	if r.opts.DependencyRunner != nil {
		if err := r.opts.DependencyRunner.Down(ctx, plan.Environment.Name, plan); err != nil {
			return err
		}
	}
	r.opts.EventSink.Emit(Event{Type: EventEnvironmentStopped, Ref: plan.Environment.Name})
	return nil
}

func (r *Runner) startTarget(ctx context.Context, target envstarlark.TargetProcess) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if r.opts.ProcessRunner == nil {
		return fmt.Errorf("process runner is required")
	}
	process, err := r.opts.ProcessRunner.Start(ctx, target, r.opts.EventSink)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.opts.EventSink.Emit(Event{Type: EventProcessExited, Ref: target.Ref, Error: err.Error()})
		return err
	}
	r.mu.Lock()
	if r.stopping || ctx.Err() != nil {
		r.mu.Unlock()
		_ = process.Stop(context.WithoutCancel(ctx))
		return ctx.Err()
	}
	r.processes[target.Ref] = process
	r.wg.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.wg.Done()
		r.emitExit(processExit{ref: target.Ref, process: process, err: process.Wait()})
	}()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	r.opts.EventSink.Emit(Event{Type: EventProcessStarted, Ref: target.Ref})
	return nil
}

func (r *Runner) runReadiness(ctx context.Context, target envstarlark.TargetProcess) error {
	if strings.TrimSpace(target.ReadinessCmd) == "" {
		return nil
	}
	err := fmt.Errorf("readiness runner is required")
	if r.opts.ReadinessRunner != nil {
		err = r.opts.ReadinessRunner.Run(ctx, target, r.opts.EventSink)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		return nil
	}
	err = errors.Wrapf(err, "%s readiness check failed", target.Ref)
	r.stopProcess(ctx, target.Ref)
	r.opts.EventSink.Emit(Event{Type: EventProcessExited, Ref: target.Ref, Error: err.Error()})
	return err
}

func (r *Runner) runBlockingTarget(ctx context.Context, target envstarlark.TargetProcess) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if r.opts.ProcessRunner == nil {
		return fmt.Errorf("process runner is required")
	}
	process, err := r.opts.ProcessRunner.Start(ctx, target, r.opts.EventSink)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.opts.EventSink.Emit(Event{Type: EventProcessExited, Ref: target.Ref, Error: err.Error()})
		return err
	}
	if ctx.Err() != nil {
		_ = process.Stop(context.WithoutCancel(ctx))
		return ctx.Err()
	}
	r.opts.EventSink.Emit(Event{Type: EventProcessStarted, Ref: target.Ref})
	wait := make(chan error, 1)
	go func() {
		wait <- process.Wait()
	}()
	select {
	case err = <-wait:
	case <-ctx.Done():
		_ = process.Stop(context.WithoutCancel(ctx))
		<-wait
		return ctx.Err()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	event := Event{Type: EventProcessExited, Ref: target.Ref}
	if err != nil {
		event.Error = err.Error()
	}
	r.opts.EventSink.Emit(event)
	return err
}

func (r *Runner) reloadTarget(ctx context.Context, plan *envstarlark.RuntimePlan, ref string, path string) {
	target, ok := targetByRef(plan, ref)
	if !ok || !target.Reload {
		return
	}
	r.beginRestart()
	defer r.endRestart()
	r.opts.EventSink.Emit(Event{Type: EventReloadStarted, Ref: ref, Path: path})
	if process := r.processSnapshot()[ref]; process != nil {
		_ = process.Stop(ctx)
	}
	if err := r.startTarget(ctx, target); err != nil {
		r.opts.EventSink.Emit(Event{Type: EventReloadCompleted, Ref: ref, Path: path, Error: err.Error()})
		return
	}
	r.opts.EventSink.Emit(Event{Type: EventReloadCompleted, Ref: ref, Path: path})
}

func (r *Runner) reloadEnabled(plan *envstarlark.RuntimePlan) bool {
	return !r.opts.NoReload && plan.Environment.LiveReload.Enabled
}

type ShellProcessRunner struct {
	shell string
	out   io.Writer
	err   io.Writer
}

const shellProcessStopTimeout = 10 * time.Second

func NewShellProcessRunner(shell string, out io.Writer, err io.Writer) *ShellProcessRunner {
	return &ShellProcessRunner{shell: shell, out: out, err: err}
}

func (r *ShellProcessRunner) Start(ctx context.Context, target envstarlark.TargetProcess, sink EventSink) (Process, error) {
	if sink == nil {
		sink = discardSink{}
	}
	parts := strings.Fields(r.shell)
	if len(parts) == 0 {
		parts = []string{"/bin/sh"}
	}
	args := append([]string{}, parts[1:]...)
	args = append(args, "-c", target.Command)
	cmd := exec.CommandContext(ctx, parts[0], args...)
	cmd.Dir = target.WorkingDir
	cmd.Env = processEnv(target.Env)
	stdout := &lineEventWriter{sink: sink, ref: target.Ref, stream: "stdout"}
	stderr := &lineEventWriter{sink: sink, ref: target.Ref, stream: "stderr"}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	process := &shellProcess{cmd: cmd, waitDone: make(chan struct{}), stdout: stdout, stderr: stderr}
	go process.wait()
	return process, nil
}

func (r *ShellProcessRunner) Run(ctx context.Context, target envstarlark.TargetProcess, sink EventSink) error {
	target.Command = target.ReadinessCmd
	process, err := r.Start(ctx, target, sink)
	if err != nil {
		return err
	}
	return process.Wait()
}

type shellProcess struct {
	cmd      *exec.Cmd
	waitDone chan struct{}
	stdout   *lineEventWriter
	stderr   *lineEventWriter
	waitErr  error
	once     sync.Once
}

func (p *shellProcess) Wait() error {
	<-p.waitDone
	return p.waitErr
}

func (p *shellProcess) Stop(ctx context.Context) error {
	if p.cmd.Process == nil {
		return nil
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.waitDone:
		return nil
	case <-time.After(shellProcessStopTimeout):
		_ = p.cmd.Process.Kill()
	case <-ctx.Done():
		_ = p.cmd.Process.Kill()
	}
	<-p.waitDone
	return nil
}

func (p *shellProcess) wait() {
	p.once.Do(func() {
		p.waitErr = p.cmd.Wait()
		p.stdout.flush()
		p.stderr.flush()
		close(p.waitDone)
	})
}

type LineEventSink struct {
	mu  sync.Mutex
	out io.Writer
	err io.Writer
}

func NewLineEventSink(out io.Writer, err io.Writer) *LineEventSink {
	return &LineEventSink{out: out, err: err}
}

func (s *LineEventSink) Emit(event Event) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.out
	if (event.Error != "" || event.Stream == "stderr") && s.err != nil {
		w = s.err
	}
	if w == nil {
		return
	}
	data, err := json.Marshal(event)
	if err != nil {
		_, _ = fmt.Fprintln(w, `{"type":"event_error","error":`+strconvQuote(err.Error())+`}`)
		return
	}
	_, _ = fmt.Fprintln(w, string(data))
}

type PrefixWriter struct {
	Sink   EventSink
	Ref    string
	Stream string
}

func (w PrefixWriter) Write(data []byte) (int, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		w.Sink.Emit(Event{Type: EventProcessOutput, Ref: w.Ref, Stream: w.Stream, Line: scanner.Text()})
	}
	return len(data), scanner.Err()
}

type lineEventWriter struct {
	sink    EventSink
	ref     string
	stream  string
	mu      sync.Mutex
	pending []byte
}

func (w *lineEventWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, data...)
	w.emitLines(false)
	return len(data), nil
}

func (w *lineEventWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.emitLines(true)
}

func (w *lineEventWriter) emitLines(atEOF bool) {
	for len(w.pending) > 0 {
		advance, line, _ := bufio.ScanLines(w.pending, atEOF)
		if advance == 0 {
			return
		}
		w.sink.Emit(Event{Type: EventProcessOutput, Ref: w.ref, Stream: w.stream, Line: string(line)})
		w.pending = w.pending[advance:]
	}
}

type discardSink struct{}

func (discardSink) Emit(Event) {}

func processEnv(values map[string]string) []string {
	envMap := make(map[string]string)
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			envMap[key] = value
		}
	}
	for key, value := range values {
		envMap[key] = value
	}

	keys := make([]string, 0, len(values))
	for key := range envMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+envMap[key])
	}
	return env
}

func targetOrder(plan *envstarlark.RuntimePlan) []string {
	if len(plan.RunOrder) == 0 {
		refs := make([]string, 0, len(plan.Targets))
		for _, target := range plan.Targets {
			refs = append(refs, target.Ref)
		}
		sort.Strings(refs)
		return refs
	}
	refs := make([]string, 0, len(plan.RunOrder))
	for _, ref := range plan.RunOrder {
		if _, ok := targetByRef(plan, ref); ok {
			refs = append(refs, ref)
		}
	}
	return refs
}

func targetByRef(plan *envstarlark.RuntimePlan, ref string) (envstarlark.TargetProcess, bool) {
	for _, target := range plan.Targets {
		if target.Ref == ref {
			return target, true
		}
	}
	return envstarlark.TargetProcess{}, false
}

func (r *Runner) declareUnits(plan *envstarlark.RuntimePlan) {
	for _, dep := range plan.Dependencies {
		bundle, name := splitRef(dep.Ref)
		r.opts.EventSink.Emit(Event{
			Type:   EventUnitDeclared,
			Ref:    dep.Ref,
			Bundle: bundle,
			Name:   name,
			Kind:   "dependency",
			Status: "pending",
		})
	}
	for _, before := range plan.BeforeTargets {
		bundle, name := splitRef(before.Ref)
		r.opts.EventSink.Emit(Event{
			Type:   EventUnitDeclared,
			Ref:    before.Ref,
			Bundle: bundle,
			Name:   name,
			Kind:   "before",
			Status: "pending",
		})
	}
	for _, dep := range plan.DepTargets {
		bundle, name := splitRef(dep.Ref)
		r.opts.EventSink.Emit(Event{
			Type:   EventUnitDeclared,
			Ref:    dep.Ref,
			Bundle: bundle,
			Name:   name,
			Kind:   "dep_target",
			Status: "pending",
		})
	}
	for _, target := range plan.Targets {
		bundle, name := splitRef(target.Ref)
		r.opts.EventSink.Emit(Event{
			Type:   EventUnitDeclared,
			Ref:    target.Ref,
			Bundle: bundle,
			Name:   name,
			Kind:   "target",
			Status: "pending",
		})
	}
}

func (r *Runner) emitDependencyFailure(plan *envstarlark.RuntimePlan, err error) {
	var depErr DependencyError
	if errors.As(err, &depErr) && depErr.Ref != "" {
		r.opts.EventSink.Emit(Event{Type: EventDependencyFailed, Ref: depErr.Ref, Error: err.Error()})
		return
	}
	r.opts.EventSink.Emit(Event{Type: EventDependencyFailed, Ref: "dependencies", Error: err.Error()})
}

func (r *Runner) waitForQuitAfterError(ctx context.Context, plan *envstarlark.RuntimePlan, err error, startedDeps bool) error {
	for {
		select {
		case <-ctx.Done():
			cleanupCtx := context.WithoutCancel(ctx)
			r.stopProcesses(cleanupCtx)
			r.stopDependencies(cleanupCtx, plan, startedDeps)
			r.opts.EventSink.Emit(Event{Type: EventEnvironmentStopped, Ref: plan.Environment.Name})
			return err
		case action, ok := <-r.opts.ControlActions:
			if !ok {
				continue
			}
			r.handleControlAction(ctx, plan, action)
		case exit := <-r.done:
			if exit.ref == "" {
				continue
			}
			if !r.removeProcess(exit.ref, exit.process) {
				continue
			}
			event := Event{Type: EventProcessExited, Ref: exit.ref}
			if exit.err != nil {
				event.Error = exit.err.Error()
			}
			r.opts.EventSink.Emit(event)
		}
	}
}

func (r *Runner) stopDependencies(ctx context.Context, plan *envstarlark.RuntimePlan, started bool) {
	if started && r.opts.DependencyRunner != nil {
		_ = r.opts.DependencyRunner.Down(ctx, plan.Environment.Name, plan)
	}
}

func splitRef(ref string) (string, string) {
	bundle, name, ok := strings.Cut(ref, ":")
	if !ok {
		return "", ref
	}
	return bundle, name
}

func firstErr(existing error, next error) error {
	if existing != nil {
		return existing
	}
	return next
}

func enabledWatches(plan *envstarlark.RuntimePlan) []envstarlark.Watch {
	watches := []envstarlark.Watch{}
	for _, watch := range plan.Watches {
		if watch.Enabled && watch.Reload {
			watches = append(watches, watch)
		}
	}
	return watches
}

func sortedProcessRefs(processes map[string]Process) []string {
	refs := make([]string, 0, len(processes))
	for ref := range processes {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs
}

func planWithDependencyEnv(plan *envstarlark.RuntimePlan, env map[string]string) *envstarlark.RuntimePlan {
	if len(env) == 0 {
		return plan
	}
	syncDependencyEnvDotenvFiles(plan, env)
	next := *plan
	next.BeforeTargets = append([]envstarlark.TargetProcess{}, plan.BeforeTargets...)
	for i := range next.BeforeTargets {
		next.BeforeTargets[i] = targetWithDependencyEnv(next.BeforeTargets[i], env)
	}
	next.DepTargets = append([]envstarlark.TargetProcess{}, plan.DepTargets...)
	for i := range next.DepTargets {
		next.DepTargets[i] = targetWithDependencyEnv(next.DepTargets[i], env)
	}
	next.Targets = append([]envstarlark.TargetProcess{}, plan.Targets...)
	for i := range next.Targets {
		next.Targets[i] = targetWithDependencyEnv(next.Targets[i], env)
	}
	return &next
}

const (
	dotenvBlockBegin = "# >>> rpm dependency env (managed by `rpm env up`) >>>"
	dotenvBlockEnd   = "# <<< rpm dependency env <<<"
)

// syncDependencyEnvDotenvFiles defines dependency env vars (such as published
// ports) inside every target dotenv file. Processes get resolved values
// injected into their environment, but applications that reload dotenv files
// themselves expand ${VAR} from the file's own scope, so the definitions must
// exist in the file for those values to resolve.
func syncDependencyEnvDotenvFiles(plan *envstarlark.RuntimePlan, env map[string]string) {
	seen := make(map[string]bool)
	targets := append([]envstarlark.TargetProcess{}, plan.BeforeTargets...)
	targets = append(targets, plan.DepTargets...)
	targets = append(targets, plan.Targets...)
	for _, target := range targets {
		for _, file := range target.DotenvFiles {
			if seen[file] {
				continue
			}
			seen[file] = true
			upsertDependencyEnvBlock(file, env)
		}
	}
}

// upsertDependencyEnvBlock prepends a managed block defining all dependency
// env vars, replacing any block from a previous run. The block sits at the top
// of the file because dotenv loaders expand ${VAR} from definitions made
// earlier in the same file. Best effort: missing, unreadable or unwritable
// files are left alone.
func upsertDependencyEnvBlock(path string, env map[string]string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	content := string(data)
	body, hadBlock := stripDependencyEnvBlock(content)
	if len(env) == 0 {
		if hadBlock {
			_ = atomicWriteFile(path, []byte(body), dotenvFileMode(path))
		}
		return
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var block strings.Builder
	block.WriteString(dotenvBlockBegin + "\n")
	for _, key := range keys {
		block.WriteString(key + "=" + env[key] + "\n")
	}
	block.WriteString(dotenvBlockEnd + "\n")
	next := block.String() + body
	if next == content {
		return
	}
	_ = atomicWriteFile(path, []byte(next), dotenvFileMode(path))
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".rpm-dotenv-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func stripDependencyEnvBlock(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	stripped := false
	inBlock := false
	for _, line := range lines {
		if line == dotenvBlockBegin {
			inBlock = true
			stripped = true
			continue
		}
		if inBlock {
			if line == dotenvBlockEnd {
				inBlock = false
			}
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n"), stripped
}

func dotenvFileMode(path string) os.FileMode {
	if info, err := os.Stat(path); err == nil {
		return info.Mode().Perm()
	}
	return 0o644
}

func targetWithDependencyEnv(target envstarlark.TargetProcess, env map[string]string) envstarlark.TargetProcess {
	values := copyStringMap(target.Env)
	if values == nil {
		values = make(map[string]string)
	}
	dotenv := targetDotenvEnv(target)
	for key, value := range dotenv {
		dotenv[key] = substituteEnv(value, env)
		values[key] = dotenv[key]
	}
	for key, value := range env {
		values[key] = value
	}
	target.Env = values
	target.DotenvEnv = dotenv
	return target
}

func targetDotenvEnv(target envstarlark.TargetProcess) map[string]string {
	values := make(map[string]string)
	for _, file := range target.DotenvFiles {
		fileVars, err := rpmexec.LoadDotenv(file)
		if err != nil {
			continue
		}
		for key, value := range fileVars {
			values[key] = value
		}
	}
	if len(values) > 0 || len(target.DotenvFiles) > 0 {
		return values
	}
	return copyStringMap(target.DotenvEnv)
}

func substituteEnv(value string, env map[string]string) string {
	if !strings.Contains(value, "${") {
		return value
	}
	var result strings.Builder
	for {
		start := strings.Index(value, "${")
		if start == -1 {
			result.WriteString(value)
			return result.String()
		}
		result.WriteString(value[:start])
		rest := value[start+2:]
		end := strings.Index(rest, "}")
		if end == -1 {
			result.WriteString(value[start:])
			return result.String()
		}
		name := rest[:end]
		if replacement, ok := env[name]; ok {
			result.WriteString(replacement)
		} else {
			result.WriteString(value[start : start+end+3])
		}
		value = rest[end+1:]
	}
}

func copyStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	copied := make(map[string]string, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}

func strconvQuote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
