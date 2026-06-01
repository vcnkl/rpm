package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	envstarlark "github.com/vcnkl/rpm/environments/starlark"
)

type PlanLoader interface {
	LoadPlan(ctx context.Context, blueprint string) (*envstarlark.RuntimePlan, error)
}

type ProcessRunner interface {
	Start(ctx context.Context, target envstarlark.TargetProcess, sink EventSink) (Process, error)
}

type Process interface {
	Wait() error
	Stop(ctx context.Context) error
}

type DependencyRunner interface {
	Up(ctx context.Context, blueprint string, plan *envstarlark.RuntimePlan) error
	Down(ctx context.Context, blueprint string, plan *envstarlark.RuntimePlan) error
}

type ReloadWatcher interface {
	Watch(ctx context.Context, watches []envstarlark.Watch, onChange func(target string, path string)) error
}

type EventSink interface {
	Emit(Event)
}

type Event struct {
	Type    string `json:"type"`
	Ref     string `json:"ref,omitempty"`
	Path    string `json:"path,omitempty"`
	Line    string `json:"line,omitempty"`
	Error   string `json:"error,omitempty"`
	Stream  string `json:"stream,omitempty"`
	Message string `json:"message,omitempty"`
}

const (
	EventProcessStarted     = "process_started"
	EventProcessOutput      = "process_output"
	EventProcessExited      = "process_exited"
	EventDependencyStarted  = "dependency_started"
	EventDependencyFailed   = "dependency_failed"
	EventReloadStarted      = "reload_started"
	EventReloadCompleted    = "reload_completed"
	EventEnvironmentStopped = "environment_stopped"
)

type Options struct {
	ProcessRunner    ProcessRunner
	DependencyRunner DependencyRunner
	ReloadWatcher    ReloadWatcher
	EventSink        EventSink
	NoDeps           bool
	NoReload         bool
}

type Runner struct {
	processes map[string]Process
	done      chan processExit
	mu        sync.Mutex
	opts      Options
}

func NewRunner(opts Options) *Runner {
	if opts.EventSink == nil {
		opts.EventSink = discardSink{}
	}
	return &Runner{opts: opts, processes: make(map[string]Process), done: make(chan processExit)}
}

func (r *Runner) Up(ctx context.Context, plan *envstarlark.RuntimePlan) error {
	startedDeps := false
	if r.opts.DependencyRunner != nil && !r.opts.NoDeps && len(plan.Dependencies) > 0 {
		if err := r.opts.DependencyRunner.Up(ctx, plan.Environment.Name, plan); err != nil {
			r.opts.EventSink.Emit(Event{Type: EventDependencyFailed, Error: err.Error()})
			return err
		}
		startedDeps = true
		for _, dep := range plan.Dependencies {
			r.opts.EventSink.Emit(Event{Type: EventDependencyStarted, Ref: dep.Ref})
		}
	}

	for _, ref := range targetOrder(plan) {
		target, ok := targetByRef(plan, ref)
		if !ok {
			continue
		}
		if err := r.startTarget(ctx, target); err != nil {
			r.stopProcesses(ctx)
			if startedDeps {
				_ = r.opts.DependencyRunner.Down(ctx, plan.Environment.Name, plan)
			}
			return err
		}
	}

	watchCancel := func() {}
	if r.reloadEnabled(plan) && r.opts.ReloadWatcher != nil {
		watches := enabledWatches(plan)
		if len(watches) > 0 {
			watchCtx, cancel := context.WithCancel(ctx)
			watchCancel = cancel
			go func() {
				if err := r.opts.ReloadWatcher.Watch(watchCtx, watches, func(ref string, path string) {
					r.reloadTarget(ctx, plan, ref, path)
				}); err != nil && ctx.Err() == nil {
					r.done <- processExit{err: err}
				}
			}()
		}
	}
	defer watchCancel()

	for {
		if r.activeCount() == 0 {
			r.opts.EventSink.Emit(Event{Type: EventEnvironmentStopped, Ref: plan.Environment.Name})
			return nil
		}
		select {
		case <-ctx.Done():
			r.stopProcesses(ctx)
			r.opts.EventSink.Emit(Event{Type: EventEnvironmentStopped, Ref: plan.Environment.Name})
			return nil
		case exit := <-r.done:
			if exit.ref != "" {
				r.removeProcess(exit.ref, exit.process)
				event := Event{Type: EventProcessExited, Ref: exit.ref}
				if exit.err != nil {
					event.Error = exit.err.Error()
				}
				r.opts.EventSink.Emit(event)
			}
			if exit.err != nil {
				r.stopProcesses(ctx)
				if startedDeps {
					_ = r.opts.DependencyRunner.Down(ctx, plan.Environment.Name, plan)
				}
				r.opts.EventSink.Emit(Event{Type: EventEnvironmentStopped, Ref: plan.Environment.Name})
				return exit.err
			}
		}
	}
}

type processExit struct {
	ref     string
	process Process
	err     error
}

func (r *Runner) activeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.processes)
}

func (r *Runner) removeProcess(ref string, process Process) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.processes[ref] == process {
		delete(r.processes, ref)
	}
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
		}
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
	if r.opts.ProcessRunner == nil {
		return fmt.Errorf("process runner is required")
	}
	process, err := r.opts.ProcessRunner.Start(ctx, target, r.opts.EventSink)
	if err != nil {
		r.opts.EventSink.Emit(Event{Type: EventProcessExited, Ref: target.Ref, Error: err.Error()})
		return err
	}
	r.mu.Lock()
	r.processes[target.Ref] = process
	r.mu.Unlock()
	go func() {
		r.done <- processExit{ref: target.Ref, process: process, err: process.Wait()}
	}()
	r.opts.EventSink.Emit(Event{Type: EventProcessStarted, Ref: target.Ref})
	return nil
}

func (r *Runner) reloadTarget(ctx context.Context, plan *envstarlark.RuntimePlan, ref string, path string) {
	target, ok := targetByRef(plan, ref)
	if !ok || !target.Reload {
		return
	}
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
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go scanOutput(target.Ref, "stdout", stdout, sink)
	go scanOutput(target.Ref, "stderr", stderr, sink)
	process := &shellProcess{cmd: cmd, waitDone: make(chan struct{})}
	go process.wait()
	return process, nil
}

type shellProcess struct {
	cmd      *exec.Cmd
	waitDone chan struct{}
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
	case <-time.After(2 * time.Second):
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
		close(p.waitDone)
	})
}

type LineEventSink struct {
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
	w := s.out
	if (event.Error != "" || event.Stream == "stderr") && s.err != nil {
		w = s.err
	}
	if w == nil {
		return
	}
	data, err := json.Marshal(event)
	if err != nil {
		fmt.Fprintln(w, `{"type":"event_error","error":`+strconvQuote(err.Error())+`}`)
		return
	}
	fmt.Fprintln(w, string(data))
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

func scanOutput(ref string, stream string, reader io.Reader, sink EventSink) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		sink.Emit(Event{Type: EventProcessOutput, Ref: ref, Stream: stream, Line: scanner.Text()})
	}
}

type discardSink struct{}

func (discardSink) Emit(Event) {}

func processEnv(values map[string]string) []string {
	env := append([]string{}, os.Environ()...)
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+values[key])
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

func strconvQuote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
