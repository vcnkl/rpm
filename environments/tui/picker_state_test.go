package envtui

import (
	"reflect"
	"testing"
)

func sampleRequest() SelectionRequest {
	return SelectionRequest{
		Title: "Select targets",
		Items: []SelectionItem{
			{Label: "core", Group: "core", Expandable: true, Expanded: true},
			{Ref: "core:a", Label: "a", Group: "core"},
			{Ref: "core:b", Label: "b", Group: "core"},
			{Label: "api", Group: "api", Expandable: true, Expanded: true},
			{Ref: "api:x", Label: "x", Group: "api"},
		},
	}
}

func TestInitialSelectionCursorSkipsHeaderlessExpandable(t *testing.T) {
	state := initialSelectionState(sampleRequest())
	if state.cursor != 0 {
		t.Fatalf("expected cursor on first expandable group, got %d", state.cursor)
	}
}

func TestMoveCursorSkipsHidden(t *testing.T) {
	state := initialSelectionState(sampleRequest())
	state.items[1].Hidden = true
	state = moveSelectionCursor(state, 1)
	if state.items[state.cursor].Ref != "core:b" {
		t.Fatalf("expected to skip hidden core:a, landed on %q", state.items[state.cursor].Ref)
	}
}

func TestToggleLeafSelection(t *testing.T) {
	state := initialSelectionState(sampleRequest())
	state.cursor = 1
	state = toggleSelectionItem(state)
	if !state.items[1].Selected {
		t.Fatalf("expected core:a selected")
	}
	state = toggleSelectionItem(state)
	if state.items[1].Selected {
		t.Fatalf("expected core:a deselected")
	}
}

func TestToggleGroupTriState(t *testing.T) {
	state := initialSelectionState(sampleRequest())
	state.cursor = 0
	state = toggleSelectionItem(state)
	if !state.items[1].Selected || !state.items[2].Selected {
		t.Fatalf("group toggle should select all members")
	}
	state = toggleSelectionItem(state)
	if state.items[1].Selected || state.items[2].Selected {
		t.Fatalf("group toggle should deselect all members")
	}
}

func TestToggleGroupExpansionHidesChildren(t *testing.T) {
	state := initialSelectionState(sampleRequest())
	state.cursor = 0
	state = toggleGroupExpansion(state)
	if state.items[0].Expanded {
		t.Fatalf("group should be collapsed")
	}
	if !state.items[1].Hidden || !state.items[2].Hidden {
		t.Fatalf("children should be hidden when collapsed")
	}
}

func TestSelectedRefsSorted(t *testing.T) {
	state := initialSelectionState(sampleRequest())
	state.items[2].Selected = true
	state.items[4].Selected = true
	state.items[1].Selected = true
	got := selectedRefs(state)
	want := []string{"api:x", "core:a", "core:b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selectedRefs=%v want %v", got, want)
	}
}
