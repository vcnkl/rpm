package envtui

import "sort"

type selectionState struct {
	title  string
	items  []SelectionItem
	cursor int
}

func initialSelectionState(request SelectionRequest) selectionState {
	cursor := 0
	for i, item := range request.Items {
		if !item.Header {
			cursor = i
			break
		}
	}
	return selectionState{title: request.Title, items: request.Items, cursor: cursor}
}

func moveSelectionCursor(state selectionState, delta int) selectionState {
	next := state.cursor
	for {
		next += delta
		if next < 0 || next >= len(state.items) {
			return state
		}
		if state.items[next].Hidden {
			continue
		}
		if !state.items[next].Header || state.items[next].Expandable {
			state.cursor = next
			return state
		}
	}
}

func toggleSelectionItem(state selectionState) selectionState {
	if state.cursor < 0 || state.cursor >= len(state.items) {
		return state
	}
	item := state.items[state.cursor]
	if item.Expandable {
		return toggleGroupSelection(state, item)
	}
	if item.Header {
		return state
	}
	items := make([]SelectionItem, len(state.items))
	copy(items, state.items)
	items[state.cursor].Selected = !items[state.cursor].Selected
	state.items = items
	return state
}

func toggleGroupSelection(state selectionState, group SelectionItem) selectionState {
	indexes := visibleGroupIndexes(state.items, group)
	shouldSelect := false
	for _, index := range indexes {
		if !state.items[index].Selected {
			shouldSelect = true
			break
		}
	}
	items := make([]SelectionItem, len(state.items))
	copy(items, state.items)
	for _, index := range indexes {
		items[index].Selected = shouldSelect
	}
	state.items = items
	return state
}

func toggleGroupExpansion(state selectionState) selectionState {
	if state.cursor < 0 || state.cursor >= len(state.items) {
		return state
	}
	item := state.items[state.cursor]
	if !item.Expandable {
		return state
	}
	expanded := !item.Expanded
	items := make([]SelectionItem, len(state.items))
	copy(items, state.items)
	for i := range items {
		if items[i].Group != item.Group {
			continue
		}
		if items[i].Expandable {
			items[i].Expanded = expanded
		} else {
			items[i].Hidden = !expanded
		}
	}
	state.items = items
	return state
}

func visibleGroupIndexes(items []SelectionItem, group SelectionItem) []int {
	indexes := make([]int, 0)
	for i, item := range items {
		if item.Group != group.Group || item.Header || item.Expandable || item.Hidden {
			continue
		}
		indexes = append(indexes, i)
	}
	return indexes
}

func selectedRefs(state selectionState) []string {
	refs := make([]string, 0)
	for _, item := range state.items {
		if item.Selected && !item.Header && !item.Expandable && item.Ref != "" {
			refs = append(refs, item.Ref)
		}
	}
	sort.Strings(refs)
	return refs
}

func selectedCount(state selectionState) int {
	return len(selectedRefs(state))
}
