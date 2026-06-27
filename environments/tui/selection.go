package envtui

import (
	"io"
	"os"

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
