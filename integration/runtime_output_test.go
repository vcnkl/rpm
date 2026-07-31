package integration

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	envruntime "github.com/vcwx/rpm/environments/runtime"
	envstarlark "github.com/vcwx/rpm/environments/starlark"
)

func TestIntegration_ShellProcessRunnerWaitsForSlowOutputDrain(t *testing.T) {
	shouldSkip(t)
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	reader, writer := io.Pipe()
	var process envruntime.Process
	t.Cleanup(func() {
		cancel()
		_ = reader.Close()
		_ = writer.Close()
		if process != nil {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer stopCancel()
			_ = process.Stop(stopCtx)
		}
	})

	workingDir := t.TempDir()
	marker := filepath.Join(workingDir, "command-completed")
	target := envstarlark.TargetProcess{
		Ref:        "runtime:slow-output",
		Command:    `printf 'first\nsecond\nfinal'; : > "$COMPLETION_MARKER"`,
		WorkingDir: workingDir,
		Env:        map[string]string{"COMPLETION_MARKER": marker},
	}
	runner := envruntime.NewShellProcessRunner("/bin/sh", io.Discard, io.Discard)
	sink := envruntime.NewLineEventSink(writer, writer)

	var err error
	process, err = runner.Start(ctx, target, sink)
	require.NoError(t, err)
	waitForFile(t, ctx, marker)

	drainDelay := time.NewTimer(3 * time.Second)
	defer drainDelay.Stop()
	select {
	case <-drainDelay.C:
	case <-ctx.Done():
		require.FailNow(t, "output drain delay timed out", ctx.Err().Error())
	}

	type pipeResult struct {
		output []byte
		err    error
	}
	result := make(chan pipeResult, 1)
	go func() {
		output, readErr := io.ReadAll(reader)
		result <- pipeResult{output: output, err: readErr}
	}()

	waitErr := process.Wait()
	closeErr := writer.Close()
	var read pipeResult
	select {
	case read = <-result:
	case <-ctx.Done():
		require.FailNow(t, "output drain timed out", ctx.Err().Error())
	}

	require.NoError(t, waitErr)
	require.NoError(t, closeErr)
	require.NoError(t, read.err)
	require.Equal(t, []envruntime.Event{
		{Type: envruntime.EventProcessOutput, Ref: target.Ref, Line: "first", Stream: "stdout"},
		{Type: envruntime.EventProcessOutput, Ref: target.Ref, Line: "second", Stream: "stdout"},
		{Type: envruntime.EventProcessOutput, Ref: target.Ref, Line: "final", Stream: "stdout"},
	}, runtimeEvents(t, string(read.output)))
}
