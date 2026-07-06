package metrics

import (
	"context"
	"runtime"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

type processSampler struct {
	registry *Registry
	usage    func(ctx context.Context, pid int32) (uint64, float64, bool)
	now      func() time.Time
	cores    int

	mu       sync.Mutex
	prevWall time.Time
	prevCPU  map[string]float64
	prevPID  map[string]int32
}

func NewSampler(registry *Registry) Sampler {
	return &processSampler{
		registry: registry,
		usage:    subtreeUsage,
		now:      time.Now,
		cores:    runtime.NumCPU(),
		prevCPU:  make(map[string]float64),
		prevPID:  make(map[string]int32),
	}
}

func (s *processSampler) Sample(ctx context.Context) Snapshot {
	pids := s.registry.snapshot()
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()

	elapsed := now.Sub(s.prevWall).Seconds()
	first := s.prevWall.IsZero()
	cores := s.cores
	if cores < 1 {
		cores = 1
	}

	snapshot := Snapshot{Targets: make(map[string]Sample, len(pids))}
	nextCPU := make(map[string]float64, len(pids))
	nextPID := make(map[string]int32, len(pids))

	for ref, pid := range pids {
		mem, cpuSeconds, ok := s.usage(ctx, pid)
		if !ok {
			continue
		}
		nextCPU[ref] = cpuSeconds
		nextPID[ref] = pid

		percent := 0.0
		if !first && elapsed > 0 && s.prevPID[ref] == pid {
			if prev, seen := s.prevCPU[ref]; seen {
				if delta := cpuSeconds - prev; delta > 0 {
					percent = delta / elapsed / float64(cores) * 100
				}
			}
		}

		sample := Sample{CPU: percent, MemBytes: mem}
		snapshot.Targets[ref] = sample
		snapshot.Total.CPU += percent
		snapshot.Total.MemBytes += mem
	}

	s.prevCPU = nextCPU
	s.prevPID = nextPID
	s.prevWall = now
	return snapshot
}

func subtreeUsage(ctx context.Context, pid int32) (uint64, float64, bool) {
	root, err := process.NewProcessWithContext(ctx, pid)
	if err != nil {
		return 0, 0, false
	}

	var mem uint64
	var cpuSeconds float64
	seen := make(map[int32]struct{})

	var walk func(p *process.Process)
	walk = func(p *process.Process) {
		if p == nil {
			return
		}
		if _, ok := seen[p.Pid]; ok {
			return
		}
		seen[p.Pid] = struct{}{}
		if info, err := p.MemoryInfoWithContext(ctx); err == nil && info != nil {
			mem += info.RSS
		}
		if times, err := p.TimesWithContext(ctx); err == nil && times != nil {
			cpuSeconds += times.User + times.System
		}
		children, err := p.ChildrenWithContext(ctx)
		if err != nil {
			return
		}
		for _, child := range children {
			walk(child)
		}
	}
	walk(root)

	return mem, cpuSeconds, true
}
