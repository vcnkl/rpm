package metrics

import (
	"context"
	"os"
	"sync"

	"github.com/shirou/gopsutil/v4/process"
	envruntime "github.com/vcnkl/rpm/environments/runtime"
	envstarlark "github.com/vcnkl/rpm/environments/starlark"
)

type trackingRunner struct {
	inner    envruntime.ProcessRunner
	registry *Registry
	self     *process.Process
	mu       sync.Mutex
}

func NewProcessRunner(inner envruntime.ProcessRunner, registry *Registry) envruntime.ProcessRunner {
	self, _ := process.NewProcess(int32(os.Getpid()))
	return &trackingRunner{inner: inner, registry: registry, self: self}
}

func (r *trackingRunner) Start(ctx context.Context, target envstarlark.TargetProcess, sink envruntime.EventSink) (envruntime.Process, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	before := r.childPIDs()
	proc, err := r.inner.Start(ctx, target, sink)
	if err != nil {
		return nil, err
	}
	pid := newChild(before, r.childPIDs())
	if pid != 0 {
		r.registry.track(target.Ref, pid)
	}
	return &trackedProcess{Process: proc, registry: r.registry, ref: target.Ref, pid: pid}, nil
}

func (r *trackingRunner) childPIDs() map[int32]struct{} {
	pids := make(map[int32]struct{})
	if r.self == nil {
		return pids
	}
	children, err := r.self.Children()
	if err != nil {
		return pids
	}
	for _, child := range children {
		pids[child.Pid] = struct{}{}
	}
	return pids
}

func newChild(before, after map[int32]struct{}) int32 {
	var found int32
	count := 0
	for pid := range after {
		if _, ok := before[pid]; !ok {
			found = pid
			count++
		}
	}
	if count == 1 {
		return found
	}
	return 0
}

type trackedProcess struct {
	envruntime.Process
	registry *Registry
	ref      string
	pid      int32
}

func (p *trackedProcess) Wait() error {
	err := p.Process.Wait()
	p.registry.forget(p.ref, p.pid)
	return err
}
