package envtui

import (
	"testing"

	envruntime "github.com/vcnkl/rpm/environments/runtime"
)

func find(units []unitState, ref string) (unitState, bool) {
	for _, unit := range units {
		if unit.Ref == ref {
			return unit, true
		}
	}
	return unitState{}, false
}

func TestApplyEventDeclaresAndTransitions(t *testing.T) {
	state := newEnvState("local-stack")
	state = applyEvent(state, envruntime.Event{Type: envruntime.EventUnitDeclared, Ref: "go-app:web", Kind: "target", Status: "pending", Bundle: "go-app", Name: "web"})
	unit, ok := find(state.units, "go-app:web")
	if !ok || unit.Status != statusPending || unit.Kind != kindTarget {
		t.Fatalf("declare failed: %+v", unit)
	}
	if unit.Bundle != "go-app" || unit.Name != "web" {
		t.Fatalf("bundle/name not captured: %+v", unit)
	}

	state = applyEvent(state, envruntime.Event{Type: envruntime.EventProcessStarted, Ref: "go-app:web"})
	if unit, _ := find(state.units, "go-app:web"); unit.Status != statusRunning {
		t.Fatalf("expected running, got %s", unit.Status)
	}

	state = applyEvent(state, envruntime.Event{Type: envruntime.EventProcessExited, Ref: "go-app:web", Error: "boom"})
	unit, _ = find(state.units, "go-app:web")
	if unit.Status != statusFailed {
		t.Fatalf("expected failed, got %s", unit.Status)
	}
	if len(unit.Output) == 0 || unit.Output[len(unit.Output)-1] != "boom" {
		t.Fatalf("error not appended to output: %+v", unit.Output)
	}
}

func TestApplyEventRecoversAfterFailure(t *testing.T) {
	state := newEnvState("env")
	state = applyEvent(state, envruntime.Event{Type: envruntime.EventUnitDeclared, Ref: "go-app:web", Kind: "target", Status: "pending"})
	state = applyEvent(state, envruntime.Event{Type: envruntime.EventProcessExited, Ref: "go-app:web", Error: "exit status 1"})
	if unit, _ := find(state.units, "go-app:web"); unit.Status != statusFailed {
		t.Fatalf("expected failed, got %s", unit.Status)
	}

	state = applyEvent(state, envruntime.Event{Type: envruntime.EventProcessStarted, Ref: "go-app:web"})
	unit, _ := find(state.units, "go-app:web")
	if unit.Status != statusRunning {
		t.Fatalf("expected running after restart, got %s", unit.Status)
	}
}

func TestApplyEventKeepsDeclaredKind(t *testing.T) {
	state := newEnvState("env")
	state = applyEvent(state, envruntime.Event{Type: envruntime.EventUnitDeclared, Ref: "postgres", Kind: "dependency", Status: "pending"})
	state = applyEvent(state, envruntime.Event{Type: envruntime.EventProcessOutput, Ref: "postgres", Line: "ready"})
	unit, _ := find(state.units, "postgres")
	if unit.Kind != kindDependency {
		t.Fatalf("kind should remain dependency, got %s", unit.Kind)
	}
}

func TestAppendOutputRingCap(t *testing.T) {
	state := newEnvState("env")
	state = applyEvent(state, envruntime.Event{Type: envruntime.EventUnitDeclared, Ref: "a", Kind: "target", Status: "running"})
	for i := 0; i < maxOutputLines+50; i++ {
		state = applyEvent(state, envruntime.Event{Type: envruntime.EventProcessOutput, Ref: "a", Line: "line"})
	}
	unit, _ := find(state.units, "a")
	if len(unit.Output) != maxOutputLines {
		t.Fatalf("expected ring cap %d, got %d", maxOutputLines, len(unit.Output))
	}
}

func TestEnvironmentStoppedPreservesFailed(t *testing.T) {
	state := newEnvState("env")
	state = applyEvent(state, envruntime.Event{Type: envruntime.EventUnitDeclared, Ref: "ok", Kind: "target", Status: "running"})
	state = applyEvent(state, envruntime.Event{Type: envruntime.EventUnitDeclared, Ref: "bad", Kind: "target", Status: "failed"})
	state = applyEvent(state, envruntime.Event{Type: envruntime.EventEnvironmentStopped, Ref: "env"})
	ok, _ := find(state.units, "ok")
	bad, _ := find(state.units, "bad")
	if ok.Status != statusStopped {
		t.Fatalf("expected stopped, got %s", ok.Status)
	}
	if bad.Status != statusFailed {
		t.Fatalf("expected failed preserved, got %s", bad.Status)
	}
}

func TestOrderUnitsByKindThenStatus(t *testing.T) {
	units := []unitState{
		{Ref: "t1", Kind: kindTarget, Status: statusRunning},
		{Ref: "d1", Kind: kindDependency, Status: statusRunning},
		{Ref: "t0", Kind: kindTarget, Status: statusFailed},
		{Ref: "b1", Kind: kindBefore, Status: statusExited},
	}
	ordered := orderUnits(units, true)
	want := []string{"d1", "b1", "t0", "t1"}
	for i, ref := range want {
		if ordered[i].Ref != ref {
			t.Fatalf("order[%d]=%s want %s (%v)", i, ordered[i].Ref, ref, refsOf(ordered))
		}
	}
	hidden := orderUnits(units, false)
	if _, ok := find(hidden, "d1"); ok {
		t.Fatalf("dependency should be hidden when showDependencies=false")
	}
}

func TestSummarizeCounts(t *testing.T) {
	units := []unitState{
		{Kind: kindDependency, Status: statusRunning},
		{Kind: kindTarget, Status: statusRunning},
		{Kind: kindTarget, Status: statusFailed},
		{Kind: kindBefore, Status: statusExited},
	}
	s := summarize(units)
	if s.total != 4 || s.dependencies != 1 || s.targets != 2 || s.before != 1 || s.running != 2 || s.failed != 1 {
		t.Fatalf("summary mismatch: %+v", s)
	}
}

func TestVisibleWindowCentersSelection(t *testing.T) {
	start, count := visibleWindow(10, 5, 4)
	if count != 4 {
		t.Fatalf("expected 4 rows, got %d", count)
	}
	if start < 0 || start+count > 10 {
		t.Fatalf("window out of bounds: start=%d count=%d", start, count)
	}
	if s, c := visibleWindow(0, 0, 4); s != 0 || c != 0 {
		t.Fatalf("empty list should yield empty window")
	}
}

func TestClampSelectionBounds(t *testing.T) {
	state := newEnvState("env")
	state.units = []unitState{{Ref: "a", Kind: kindTarget, Status: statusRunning}}
	state.selected = 9
	state = clampSelection(state)
	if state.selected != 0 {
		t.Fatalf("expected clamp to 0, got %d", state.selected)
	}
}

func refsOf(units []unitState) []string {
	refs := make([]string, len(units))
	for i, unit := range units {
		refs[i] = unit.Ref
	}
	return refs
}
