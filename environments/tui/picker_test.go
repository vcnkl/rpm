package envtui

import (
	"io"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"
)

func newTestPicker(t *testing.T, request SelectionRequest) pickerModel {
	t.Helper()
	manager := zone.New()
	t.Cleanup(manager.Close)
	return newPickerModel(newTheme(io.Discard), manager, request)
}

func key(s string) tea.KeyMsg {
	if s == "enter" {
		return tea.KeyMsg{Type: tea.KeyEnter}
	}
	if s == "esc" {
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	if s == " " {
		return tea.KeyMsg{Type: tea.KeySpace}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestPickerToggleAndConfirm(t *testing.T) {
	model := newTestPicker(t, sampleRequest())
	model.state.cursor = 1
	next, _ := model.Update(key(" "))
	model = next.(pickerModel)

	final, cmd := model.Update(key("enter"))
	model = final.(pickerModel)
	if model.canceled {
		t.Fatal("enter must not cancel")
	}
	if !isQuitCmd(cmd) {
		t.Fatal("enter must quit")
	}
	got := selectedRefs(model.state)
	if !reflect.DeepEqual(got, []string{"core:a"}) {
		t.Fatalf("expected [core:a], got %v", got)
	}
}

func TestPickerEscCancels(t *testing.T) {
	model := newTestPicker(t, sampleRequest())
	next, cmd := model.Update(key("esc"))
	model = next.(pickerModel)
	if !model.canceled {
		t.Fatal("esc must cancel")
	}
	if !isQuitCmd(cmd) {
		t.Fatal("esc must quit")
	}
}

func TestPickerRequireOneBlocksEmptyConfirm(t *testing.T) {
	request := sampleRequest()
	request.RequireOne = true
	model := newTestPicker(t, request)
	next, cmd := model.Update(key("enter"))
	model = next.(pickerModel)
	if cmd != nil {
		t.Fatal("enter with nothing selected must not quit when RequireOne")
	}
	if model.canceled {
		t.Fatal("blocked confirm must not cancel")
	}
}

func TestPickerCollapseHidesChildren(t *testing.T) {
	model := newTestPicker(t, sampleRequest())
	model.state.cursor = 0
	next, _ := model.Update(key("left"))
	model = next.(pickerModel)
	if model.state.items[0].Expanded {
		t.Fatal("left should collapse the group")
	}
	if !model.state.items[1].Hidden {
		t.Fatal("collapsed group children should be hidden")
	}
}

func TestPickerViewRendersTitleAndCount(t *testing.T) {
	model := newTestPicker(t, sampleRequest())
	model.state.cursor = 1
	next, _ := model.Update(key(" "))
	model = next.(pickerModel)
	view := model.View()
	for _, want := range []string{"RPM", "Select targets", "1 selected", "confirm"} {
		if !strings.Contains(view, want) {
			t.Fatalf("picker view missing %q\n%s", want, view)
		}
	}
}
