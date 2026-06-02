package envtui

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"

	"github.com/pkg/errors"
	"golang.org/x/term"
)

type SelectionRequest struct {
	Title      string          `json:"title"`
	Items      []SelectionItem `json:"items"`
	RequireOne bool            `json:"requireOne"`
}

type SelectionItem struct {
	Ref        string `json:"ref,omitempty"`
	Label      string `json:"label"`
	Detail     string `json:"detail,omitempty"`
	Group      string `json:"group,omitempty"`
	Status     string `json:"status,omitempty"`
	Selected   bool   `json:"selected,omitempty"`
	Defaults   bool   `json:"defaults,omitempty"`
	Header     bool   `json:"header,omitempty"`
	Muted      bool   `json:"muted,omitempty"`
	Expanded   bool   `json:"expanded,omitempty"`
	Expandable bool   `json:"expandable,omitempty"`
	Hidden     bool   `json:"hidden,omitempty"`
}

type SelectionResponse struct {
	Refs []string `json:"refs"`
}

func CanSelect(in io.Reader, out io.Writer) bool {
	inFile, ok := in.(*os.File)
	if !ok {
		return false
	}
	outFile, ok := out.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(inFile.Fd())) && term.IsTerminal(int(outFile.Fd()))
}

func Select(ctx context.Context, in io.Reader, out io.Writer, request SelectionRequest) ([]string, error) {
	if !CanSelect(in, out) {
		return nil, errors.New("env TUI selection requires a terminal")
	}
	bridge := NewBridge(io.Discard, out)
	nodePath, err := bridge.lookupNode()
	if err != nil {
		return nil, errors.Wrap(err, "node is required for interactive env selection")
	}
	scriptPath, cleanup, err := bridge.materializeScript()
	if err != nil {
		return nil, err
	}
	defer cleanup()

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	payloadReader, payloadWriter, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer payloadReader.Close()
	defer payloadWriter.Close()

	cmd := exec.CommandContext(ctx, nodePath, scriptPath)
	cmd.Env = append(os.Environ(), "RPM_ENV_TUI_MODE=select")
	cmd.Stdin = in
	cmd.Stderr = out
	cmd.ExtraFiles = []*os.File{payloadReader}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	payloadReader.Close()
	go func() {
		_, _ = payloadWriter.Write(payload)
		_ = payloadWriter.Close()
	}()
	data, readErr := io.ReadAll(stdout)
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if waitErr != nil {
		return nil, waitErr
	}
	var response SelectionResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	return response.Refs, nil
}
