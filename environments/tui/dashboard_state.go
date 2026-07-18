package envtui

import (
	"sort"

	envruntime "github.com/vcnkl/rpm/environments/runtime"
)

type unitStatus string

const (
	statusPending   unitStatus = "pending"
	statusStarting  unitStatus = "starting"
	statusRunning   unitStatus = "running"
	statusReloading unitStatus = "reloading"
	statusExited    unitStatus = "exited"
	statusFailed    unitStatus = "failed"
	statusStopped   unitStatus = "stopped"
)

type unitKind string

const (
	kindDependency  unitKind = "dependency"
	kindBefore      unitKind = "before"
	kindDepTarget   unitKind = "dep_target"
	kindTarget      unitKind = "target"
	kindEnvironment unitKind = "environment"
)

const maxOutputLines = 500

var statusOrder = map[unitStatus]int{
	statusFailed:    0,
	statusReloading: 1,
	statusStarting:  2,
	statusRunning:   3,
	statusPending:   4,
	statusExited:    4,
	statusStopped:   6,
}

var kindOrder = map[unitKind]int{
	kindDependency:  0,
	kindBefore:      1,
	kindDepTarget:   2,
	kindTarget:      3,
	kindEnvironment: 4,
}

type unitState struct {
	Ref         string
	Bundle      string
	Name        string
	Kind        unitKind
	Status      unitStatus
	Output      []string
	OutputCount int
}

type envState struct {
	blueprint        string
	selected         int
	showDependencies bool
	units            []unitState
}

func newEnvState(blueprint string) envState {
	if blueprint == "" {
		blueprint = "environment"
	}
	return envState{blueprint: blueprint, showDependencies: true}
}

func applyEvent(state envState, event envruntime.Event) envState {
	var next envState
	switch event.Type {
	case envruntime.EventUnitDeclared:
		kind := unitKind(event.Kind)
		if kind == "" {
			kind = kindTarget
		}
		status := unitStatus(event.Status)
		if status == "" {
			status = statusPending
		}
		next = upsertUnit(state, event.Ref, kind, status, event)
	case envruntime.EventProcessStarted:
		next = upsertUnit(state, event.Ref, kindTarget, statusRunning, event)
	case envruntime.EventProcessOutput:
		next = appendOutput(upsertUnit(state, event.Ref, kindTarget, outputStatus(state, event.Ref), event), event.Ref, event.Line)
	case envruntime.EventProcessExited:
		status := statusExited
		if event.Error != "" {
			status = statusFailed
		}
		next = upsertUnit(state, event.Ref, kindTarget, status, event)
		if event.Error != "" {
			next = appendOutput(next, event.Ref, event.Error)
		}
	case envruntime.EventDependencyStarted:
		next = upsertUnit(state, event.Ref, kindDependency, statusRunning, event)
	case envruntime.EventDependencyFailed:
		ref := event.Ref
		if ref == "" {
			ref = "dependencies"
		}
		next = upsertUnit(state, ref, kindDependency, statusFailed, event)
		next = appendOutput(next, ref, firstNonEmpty(event.Error, event.Message, "dependency failed"))
	case envruntime.EventReloadStarted:
		next = upsertUnit(state, event.Ref, kindTarget, statusReloading, event)
	case envruntime.EventReloadCompleted:
		status := statusRunning
		if event.Error != "" {
			status = statusFailed
		}
		next = upsertUnit(state, event.Ref, kindTarget, status, event)
		if event.Error != "" {
			next = appendOutput(next, event.Ref, event.Error)
		}
	case envruntime.EventEnvironmentStopped:
		next = stopUnits(state)
		if msg := firstNonEmpty(event.Error, event.Message); msg != "" {
			ref := event.Ref
			if ref == "" {
				ref = "environment"
			}
			next = appendOutput(next, ref, msg)
		}
	default:
		return state
	}
	return clampSelection(next)
}

func outputStatus(state envState, ref string) unitStatus {
	for _, unit := range state.units {
		if unit.Ref == ref {
			return unit.Status
		}
	}
	return statusRunning
}

func upsertUnit(state envState, ref string, kind unitKind, status unitStatus, event envruntime.Event) envState {
	if ref == "" {
		ref = "environment"
	}
	units := make([]unitState, len(state.units))
	copy(units, state.units)
	index := -1
	for i := range units {
		if units[i].Ref == ref {
			index = i
			break
		}
	}
	if index >= 0 {
		unit := units[index]
		if event.Bundle != "" {
			unit.Bundle = event.Bundle
		}
		if event.Name != "" {
			unit.Name = event.Name
		}
		if unit.Kind == kindEnvironment {
			unit.Kind = kind
		}
		unit.Status = status
		units[index] = unit
	} else {
		unit := unitState{Ref: ref, Kind: kind, Status: status, Output: []string{}}
		if event.Bundle != "" {
			unit.Bundle = event.Bundle
		}
		if event.Name != "" {
			unit.Name = event.Name
		}
		units = append(units, unit)
	}
	state.units = units
	return state
}

func appendOutput(state envState, ref string, line string) envState {
	if ref == "" {
		ref = "environment"
	}
	units := make([]unitState, len(state.units))
	copy(units, state.units)
	for i := range units {
		if units[i].Ref != ref {
			continue
		}
		output := make([]string, len(units[i].Output), len(units[i].Output)+1)
		copy(output, units[i].Output)
		output = append(output, line)
		if len(output) > maxOutputLines {
			output = output[len(output)-maxOutputLines:]
		}
		units[i].Output = output
		units[i].OutputCount++
		break
	}
	state.units = units
	return state
}

func stopUnits(state envState) envState {
	units := make([]unitState, len(state.units))
	copy(units, state.units)
	for i := range units {
		if units[i].Status != statusFailed {
			units[i].Status = statusStopped
		}
	}
	state.units = units
	return state
}

func orderUnits(units []unitState, showDependencies bool) []unitState {
	out := make([]unitState, 0, len(units))
	for _, unit := range units {
		if !showDependencies && unit.Kind == kindDependency {
			continue
		}
		out = append(out, unit)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return kindOrder[out[i].Kind] < kindOrder[out[j].Kind]
		}
		if statusOrder[out[i].Status] != statusOrder[out[j].Status] {
			return statusOrder[out[i].Status] < statusOrder[out[j].Status]
		}
		return out[i].Ref < out[j].Ref
	})
	return out
}

func clampSelection(state envState) envState {
	visible := orderUnits(state.units, state.showDependencies)
	selected := state.selected
	if selected > len(visible)-1 {
		selected = len(visible) - 1
	}
	if selected < 0 {
		selected = 0
	}
	state.selected = selected
	return state
}

type unitSummary struct {
	total        int
	dependencies int
	targets      int
	before       int
	pending      int
	running      int
	reloading    int
	failed       int
	stopped      int
}

func summarize(units []unitState) unitSummary {
	var summary unitSummary
	for _, unit := range units {
		summary.total++
		switch unit.Kind {
		case kindDependency:
			summary.dependencies++
		case kindTarget:
			summary.targets++
		case kindBefore:
			summary.before++
		}
		switch unit.Status {
		case statusPending:
			summary.pending++
		case statusRunning:
			summary.running++
		case statusReloading:
			summary.reloading++
		case statusFailed:
			summary.failed++
		case statusStopped:
			summary.stopped++
		}
	}
	return summary
}

func visibleWindow(length, selected, height int) (int, int) {
	if height <= 0 || length == 0 {
		return 0, 0
	}
	bounded := selected
	if bounded < 0 {
		bounded = 0
	}
	if bounded > length-1 {
		bounded = length - 1
	}
	start := bounded - height/2
	if start > length-height {
		start = length - height
	}
	if start < 0 {
		start = 0
	}
	count := height
	if start+count > length {
		count = length - start
	}
	return start, count
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
