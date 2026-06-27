package integration

import (
	"context"
	"io"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcnkl/rpm/environments/metrics"
	envruntime "github.com/vcnkl/rpm/environments/runtime"
	envstarlark "github.com/vcnkl/rpm/environments/starlark"
)

func TestMetricsSamplerReportsProcessUsage(t *testing.T) {
	shouldSkip(t)

	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("process metrics unsupported on %s", runtime.GOOS)
	}

	registry := metrics.NewRegistry()
	runner := metrics.NewProcessRunner(envruntime.NewShellProcessRunner("/bin/sh", io.Discard, io.Discard), registry)
	sampler := metrics.NewSampler(registry)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	target := envstarlark.TargetProcess{Ref: "metrics:probe", Command: "sleep 5"}
	process, err := runner.Start(ctx, target, nil)
	require.NoError(t, err)
	defer func() { _ = process.Stop(context.Background()) }()

	var snapshot metrics.Snapshot
	require.Eventually(t, func() bool {
		snapshot = sampler.Sample(ctx)
		sample, ok := snapshot.Targets[target.Ref]
		return ok && sample.MemBytes > 0
	}, 5*time.Second, 100*time.Millisecond, "sampler should report memory for the started target")

	assert.Equal(t, snapshot.Targets[target.Ref].MemBytes, snapshot.Total.MemBytes,
		"total memory should equal the single target's memory")
}
