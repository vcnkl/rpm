package metrics

import (
	"context"
	"sync"
)

type Sample struct {
	CPU      float64
	MemBytes uint64
}

type Snapshot struct {
	Targets map[string]Sample
	Total   Sample
}

type Sampler interface {
	Sample(ctx context.Context) Snapshot
}

type Registry struct {
	mu   sync.Mutex
	pids map[string]int32
}

func NewRegistry() *Registry {
	return &Registry{pids: make(map[string]int32)}
}

func (r *Registry) track(ref string, pid int32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pids[ref] = pid
}

func (r *Registry) forget(ref string, pid int32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pids[ref] == pid {
		delete(r.pids, ref)
	}
}

func (r *Registry) snapshot() map[string]int32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int32, len(r.pids))
	for ref, pid := range r.pids {
		out[ref] = pid
	}
	return out
}
