package metrics

import (
	"context"
	"testing"
	"time"
)

func TestRegistryTrackForgetSnapshot(t *testing.T) {
	registry := NewRegistry()
	registry.track("a", 1)
	registry.track("b", 2)

	snapshot := registry.snapshot()
	if snapshot["a"] != 1 || snapshot["b"] != 2 {
		t.Fatalf("unexpected snapshot: %v", snapshot)
	}

	registry.forget("a", 999)
	if registry.snapshot()["a"] != 1 {
		t.Fatal("forget with a stale pid must not remove the entry")
	}

	registry.forget("a", 1)
	if _, ok := registry.snapshot()["a"]; ok {
		t.Fatal("forget with the tracked pid must remove the entry")
	}

	snapshot["b"] = 100
	if registry.snapshot()["b"] != 2 {
		t.Fatal("snapshot must return a copy")
	}
}

func TestSamplerComputesCPUPercentFromDeltas(t *testing.T) {
	registry := NewRegistry()
	registry.track("api:serve", 100)

	wall := time.Unix(0, 0)
	cpuSeconds := 0.0
	sampler := &processSampler{
		registry: registry,
		usage: func(context.Context, int32) (uint64, float64, bool) {
			return 200 * 1024 * 1024, cpuSeconds, true
		},
		now:     func() time.Time { return wall },
		prevCPU: make(map[string]float64),
		prevPID: make(map[string]int32),
	}

	first := sampler.Sample(context.Background())
	if got := first.Targets["api:serve"].CPU; got != 0 {
		t.Fatalf("first sample CPU must be 0, got %v", got)
	}
	if first.Targets["api:serve"].MemBytes != 200*1024*1024 {
		t.Fatalf("unexpected mem bytes: %d", first.Targets["api:serve"].MemBytes)
	}

	wall = wall.Add(time.Second)
	cpuSeconds = 0.5
	second := sampler.Sample(context.Background())
	if got := second.Targets["api:serve"].CPU; got != 50 {
		t.Fatalf("expected 50%% cpu, got %v", got)
	}
	if second.Total.CPU != 50 || second.Total.MemBytes != 200*1024*1024 {
		t.Fatalf("unexpected total: %+v", second.Total)
	}
}

func TestSamplerResetsCPUOnPIDChange(t *testing.T) {
	registry := NewRegistry()
	registry.track("api:serve", 100)

	wall := time.Unix(0, 0)
	used := map[int32]float64{100: 0, 200: 0}
	sampler := &processSampler{
		registry: registry,
		usage: func(_ context.Context, pid int32) (uint64, float64, bool) {
			return 1024, used[pid], true
		},
		now:     func() time.Time { return wall },
		prevCPU: make(map[string]float64),
		prevPID: make(map[string]int32),
	}

	sampler.Sample(context.Background())

	wall = wall.Add(time.Second)
	used[100] = 0.5
	if got := sampler.Sample(context.Background()).Targets["api:serve"].CPU; got != 50 {
		t.Fatalf("expected 50%% before restart, got %v", got)
	}

	registry.track("api:serve", 200)
	wall = wall.Add(time.Second)
	used[200] = 0.1
	if got := sampler.Sample(context.Background()).Targets["api:serve"].CPU; got != 0 {
		t.Fatalf("cpu must reset to 0 after a pid change, got %v", got)
	}
}
