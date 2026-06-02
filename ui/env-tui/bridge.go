package envtui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/pkg/errors"
	envruntime "github.com/vcnkl/rpm/environments/runtime"
)

type ActionType string

const (
	ActionQuit       ActionType = "quit"
	ActionRestart    ActionType = "restart"
	ActionRestartAll ActionType = "restart_all"
)

type Action struct {
	Type ActionType `json:"type"`
	Ref  string     `json:"ref,omitempty"`
}

type Controller interface {
	Restart(ctx context.Context, ref string) error
	RestartAll(ctx context.Context) error
	Stop()
}

type ActionSender interface {
	Send(Action)
}

type Bridge struct {
	nodePath string
	script   []byte
	stdout   io.Writer
	stderr   io.Writer
}

func NewBridge(stdout io.Writer, stderr io.Writer) *Bridge {
	return &Bridge{stdout: stdout, stderr: stderr}
}

func (b *Bridge) Run(ctx context.Context, events <-chan envruntime.Event, controller Controller) error {
	nodePath, err := b.lookupNode()
	if err != nil {
		return errors.Wrap(err, "node is required for interactive env up; rerun with --non-interactive to disable the TUI")
	}
	scriptPath, cleanup, err := b.materializeScript()
	if err != nil {
		return err
	}
	defer cleanup()

	cmd := exec.CommandContext(ctx, nodePath, scriptPath)
	eventReader, eventWriter, err := os.Pipe()
	if err != nil {
		return err
	}
	defer eventReader.Close()
	defer eventWriter.Close()
	cmd.ExtraFiles = []*os.File{eventReader}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stdin = os.Stdin
	cmd.Stderr = b.stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	eventReader.Close()

	actionErr := make(chan error, 1)
	encodeErr := make(chan error, 1)
	go func() {
		actionErr <- DecodeActions(ctx, stdout, controller)
	}()
	go func() {
		encodeErr <- EncodeEvents(ctx, eventWriter, events)
	}()

	select {
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil
	case err := <-actionErr:
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return err
		}
		_ = cmd.Process.Kill()
		return waitAfterAction(cmd)
	case err := <-encodeErr:
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return err
		}
		_ = cmd.Process.Kill()
		return waitAfterAction(cmd)
	}
}

func waitAfterAction(cmd *exec.Cmd) error {
	err := cmd.Wait()
	if err == nil {
		return nil
	}
	if cmd.ProcessState != nil {
		if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return nil
		}
	}
	return err
}

func (b *Bridge) lookupNode() (string, error) {
	if b.nodePath != "" {
		return b.nodePath, nil
	}
	return exec.LookPath("node")
}

func (b *Bridge) materializeScript() (string, func(), error) {
	data := b.script
	if data == nil {
		if embeddedBundle != nil {
			data = embeddedBundle
		} else {
			path, err := sourceBundlePath()
			if err != nil {
				return "", func() {}, err
			}
			return path, func() {}, nil
		}
	}
	dir, err := os.MkdirTemp("", "rpm-env-tui-*")
	if err != nil {
		return "", func() {}, err
	}
	path := filepath.Join(dir, "index.js")
	if err := os.WriteFile(path, data, 0755); err != nil {
		os.RemoveAll(dir)
		return "", func() {}, err
	}
	return path, func() { os.RemoveAll(dir) }, nil
}

func sourceBundlePath() (string, error) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("failed to resolve env TUI source path")
	}
	path := filepath.Join(filepath.Dir(filename), "dist", "index.js")
	if _, err := os.Stat(path); err != nil {
		return "", errors.Wrap(err, "env TUI bundle is missing; run `cd ui/env-tui && yarn build`")
	}
	return path, nil
}

func EncodeEvents(ctx context.Context, writer io.WriteCloser, events <-chan envruntime.Event) error {
	defer writer.Close()
	encoder := json.NewEncoder(writer)
	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-events:
			if !ok {
				return nil
			}
			if err := encoder.Encode(event); err != nil {
				return err
			}
		}
	}
}

func DecodeActions(ctx context.Context, reader io.Reader, controller Controller) error {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		var action Action
		if err := json.Unmarshal(scanner.Bytes(), &action); err != nil {
			return err
		}
		switch action.Type {
		case ActionQuit:
			controller.Stop()
			return nil
		case ActionRestart:
			if err := controller.Restart(ctx, action.Ref); err != nil {
				return err
			}
		case ActionRestartAll:
			if err := controller.RestartAll(ctx); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown TUI action %q", action.Type)
		}
	}
	return scanner.Err()
}

func SendActions(ctx context.Context, reader io.Reader, sender ActionSender) error {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		var action Action
		if err := json.Unmarshal(scanner.Bytes(), &action); err != nil {
			return err
		}
		sender.Send(action)
		if action.Type == ActionQuit {
			return nil
		}
	}
	return scanner.Err()
}

type EventSink struct {
	events chan envruntime.Event
}

func NewEventSink(size int) *EventSink {
	if size <= 0 {
		size = 128
	}
	return &EventSink{events: make(chan envruntime.Event, size)}
}

func (s *EventSink) Emit(event envruntime.Event) {
	select {
	case s.events <- event:
	default:
	}
}

func (s *EventSink) Events() <-chan envruntime.Event {
	return s.events
}

func (s *EventSink) Close() {
	close(s.events)
}
