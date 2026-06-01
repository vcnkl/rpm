package tui_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	envruntime "github.com/vcnkl/rpm/environments/runtime"
	"github.com/vcnkl/rpm/environments/tui"
)

func TestEncodeEventsWritesNDJSON(t *testing.T) {
	events := make(chan envruntime.Event, 2)
	events <- envruntime.Event{Type: envruntime.EventProcessStarted, Ref: "api:serve"}
	events <- envruntime.Event{Type: envruntime.EventProcessOutput, Ref: "api:serve", Stream: "stdout", Line: "hello"}
	close(events)
	writer := &closeBuffer{}

	err := tui.EncodeEvents(context.Background(), writer, events)

	require.NoError(t, err)
	assert.Equal(t, "{\"type\":\"process_started\",\"ref\":\"api:serve\"}\n{\"type\":\"process_output\",\"ref\":\"api:serve\",\"line\":\"hello\",\"stream\":\"stdout\"}\n", writer.String())
}

func TestDecodeActionsCallsController(t *testing.T) {
	controller := &recordingController{}
	input := bytes.NewBufferString("{\"type\":\"restart\",\"ref\":\"api:serve\"}\n{\"type\":\"restart_all\"}\n{\"type\":\"quit\"}\n")

	err := tui.DecodeActions(context.Background(), input, controller)

	require.NoError(t, err)
	assert.Equal(t, []string{"restart api:serve", "restart_all", "stop"}, controller.calls)
}

type closeBuffer struct {
	bytes.Buffer
}

func (b *closeBuffer) Close() error {
	return nil
}

var _ io.WriteCloser = (*closeBuffer)(nil)

type recordingController struct {
	calls []string
}

func (c *recordingController) Restart(_ context.Context, ref string) error {
	c.calls = append(c.calls, "restart "+ref)
	return nil
}

func (c *recordingController) RestartAll(context.Context) error {
	c.calls = append(c.calls, "restart_all")
	return nil
}

func (c *recordingController) Stop() {
	c.calls = append(c.calls, "stop")
}
