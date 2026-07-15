package envtui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcnkl/rpm/environments/metrics"
	envruntime "github.com/vcnkl/rpm/environments/runtime"
)

type fakeController struct {
	restarted  []string
	restialled int
	stopped    int
}

func (c *fakeController) Restart(_ context.Context, ref string) error {
	c.restarted = append(c.restarted, ref)
	return nil
}

func (c *fakeController) RestartAll(context.Context) error {
	c.restialled++
	return nil
}

func (c *fakeController) Stop() {
	c.stopped++
}

func newTestDashboard(t *testing.T, controller Controller) (dashboardModel, *zone.Manager) {
	t.Helper()
	manager := zone.New()
	t.Cleanup(manager.Close)
	model := newDashboardModel(newTheme(io.Discard), manager, controller, "local-stack", false)
	return model, manager
}

func feed(model dashboardModel, events ...envruntime.Event) dashboardModel {
	for _, event := range events {
		next, _ := model.Update(eventMsg{event: event})
		model = next.(dashboardModel)
	}
	return model
}

func press(model dashboardModel, key string) dashboardModel {
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return next.(dashboardModel)
}

func logViewport(model dashboardModel, height int) string {
	unit, ok := model.selectedUnit()
	interior := model.logInteriorWidth()
	rows := model.logRows(unit, ok, interior)
	return strings.Join(model.logWindow(unit, ok, rows, height, interior), "\n")
}

func isQuitCmd(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestDashboardViewRendersRoster(t *testing.T) {
	model, _ := newTestDashboard(t, &fakeController{})
	model = feed(model,
		envruntime.Event{Type: envruntime.EventUnitDeclared, Ref: "postgres", Kind: "dependency", Status: "running"},
		envruntime.Event{Type: envruntime.EventUnitDeclared, Ref: "api:serve", Kind: "target", Status: "pending"},
		envruntime.Event{Type: envruntime.EventProcessStarted, Ref: "api:serve"},
	)
	view := model.View()
	for _, want := range []string{"RPM", "TARGETS", "api:serve", "LOGS"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestDashboardQuitHandshake(t *testing.T) {
	controller := &fakeController{}
	model, _ := newTestDashboard(t, controller)
	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	model = next.(dashboardModel)
	if !model.stopping {
		t.Fatal("expected stopping after q")
	}
	if cmd == nil {
		t.Fatal("first q must dispatch a stop command")
	}
	if _, ok := cmd().(tea.QuitMsg); ok {
		t.Fatal("first q must not quit before runner drains")
	}
	if controller.stopped != 1 {
		t.Fatalf("expected Stop dispatched once, got %d", controller.stopped)
	}
	done, doneCmd := model.Update(runnerDoneMsg{err: nil})
	if !isQuitCmd(doneCmd) {
		t.Fatal("runnerDoneMsg must return tea.Quit")
	}
	if !done.(dashboardModel).finished {
		t.Fatal("expected finished after runnerDoneMsg")
	}
}

func TestDashboardRestartOnlyTargets(t *testing.T) {
	controller := &fakeController{}
	model, _ := newTestDashboard(t, controller)
	model = feed(model,
		envruntime.Event{Type: envruntime.EventUnitDeclared, Ref: "postgres", Kind: "dependency", Status: "running"},
	)
	model.selected = "postgres"
	if cmd := model.restartSelected(); cmd != nil {
		t.Fatal("dependency restart must be a no-op")
	}

	model = feed(model,
		envruntime.Event{Type: envruntime.EventUnitDeclared, Ref: "api:serve", Kind: "target", Status: "running"},
	)
	model.selected = "api:serve"
	cmd := model.restartSelected()
	if cmd == nil {
		t.Fatal("target restart must dispatch")
	}
	cmd()
	if len(controller.restarted) != 1 || controller.restarted[0] != "api:serve" {
		t.Fatalf("expected restart of api:serve, got %v", controller.restarted)
	}
}

func TestDashboardDepsToggleHidesDependencies(t *testing.T) {
	model, _ := newTestDashboard(t, &fakeController{})
	model = feed(model,
		envruntime.Event{Type: envruntime.EventUnitDeclared, Ref: "postgres", Kind: "dependency", Status: "running"},
		envruntime.Event{Type: envruntime.EventUnitDeclared, Ref: "api:serve", Kind: "target", Status: "running"},
	)
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	model = next.(dashboardModel)
	for _, unit := range model.visibleUnits() {
		if unit.Kind == kindDependency {
			t.Fatal("dependencies should be hidden after d")
		}
	}
}

func TestDashboardFilterNarrowsRoster(t *testing.T) {
	model, _ := newTestDashboard(t, &fakeController{})
	model = feed(model,
		envruntime.Event{Type: envruntime.EventUnitDeclared, Ref: "api:serve", Kind: "target", Status: "running"},
		envruntime.Event{Type: envruntime.EventUnitDeclared, Ref: "web:dev", Kind: "target", Status: "running"},
	)
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	model = next.(dashboardModel)
	for _, r := range "web" {
		next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = next.(dashboardModel)
	}
	visible := model.visibleUnits()
	if len(visible) != 1 || visible[0].Ref != "web:dev" {
		t.Fatalf("filter should keep only web:dev, got %v", refsOf(visible))
	}
}

func TestDashboardSingleTickChain(t *testing.T) {
	manager := zone.New()
	t.Cleanup(manager.Close)
	model := newDashboardModel(newTheme(io.Discard), manager, &fakeController{}, "x", true)

	next, cmd := model.Update(eventMsg{event: envruntime.Event{Type: envruntime.EventUnitDeclared, Ref: "api:serve", Kind: "target", Status: "running"}})
	model = next.(dashboardModel)
	if cmd == nil || !model.ticking {
		t.Fatal("first state change must start the tick chain")
	}

	next, cmd = model.Update(eventMsg{event: envruntime.Event{Type: envruntime.EventProcessOutput, Ref: "api:serve", Line: "hello"}})
	model = next.(dashboardModel)
	if cmd != nil {
		t.Fatal("a second event must not start a parallel tick chain")
	}

	next, cmd = model.Update(tickMsg{})
	model = next.(dashboardModel)
	if cmd == nil || !model.ticking {
		t.Fatal("tick should re-arm while the progress bar is still settling")
	}
}

func TestDashboardRendersMetrics(t *testing.T) {
	manager := zone.New()
	t.Cleanup(manager.Close)
	model := newDashboardModel(newTheme(io.Discard), manager, &fakeController{}, "local-stack", false)
	model.width = 140
	model.height = 30
	model = feed(model,
		envruntime.Event{Type: envruntime.EventUnitDeclared, Ref: "postgres", Kind: "dependency", Status: "running"},
		envruntime.Event{Type: envruntime.EventUnitDeclared, Ref: "api:serve", Kind: "target", Status: "running"},
	)

	next, _ := model.Update(metricsMsg{snapshot: metrics.Snapshot{
		Targets: map[string]metrics.Sample{"api:serve": {CPU: 12.3, MemBytes: 256 * 1024 * 1024}},
		Total:   metrics.Sample{CPU: 12.3, MemBytes: 256 * 1024 * 1024},
	}})
	model = next.(dashboardModel)

	view := model.View()
	for _, want := range []string{"CPU", "MEM", "12.3%", "256MB", "—"} {
		if !strings.Contains(view, want) {
			t.Fatalf("metrics view missing %q\n%s", want, view)
		}
	}
}

func TestDashboardFailureShowsFailedPill(t *testing.T) {
	model, _ := newTestDashboard(t, &fakeController{})
	model = feed(model,
		envruntime.Event{Type: envruntime.EventUnitDeclared, Ref: "api:serve", Kind: "target", Status: "running"},
		envruntime.Event{Type: envruntime.EventProcessExited, Ref: "api:serve", Error: "exit status 1"},
	)
	if !strings.Contains(model.View(), "1 failed") {
		t.Fatalf("failed unit should surface a failed count\n%s", model.View())
	}
}

func TestDashboardAutoScrollPausePreservesLogWindow(t *testing.T) {
	model, _ := newTestDashboard(t, nil)
	model.width = 30
	model.focusMode = true
	model = feed(model, envruntime.Event{Type: envruntime.EventUnitDeclared, Ref: "api:serve", Kind: "target", Status: "running"})
	for i := range 12 {
		model = feed(model, envruntime.Event{Type: envruntime.EventProcessOutput, Ref: "api:serve", Line: fmt.Sprintf("line-%02d", i)})
	}

	model = press(model, "W")
	model = press(model, "S")
	model = press(model, "pgup")
	before := logViewport(model, 4)
	model = feed(model, envruntime.Event{
		Type: envruntime.EventProcessOutput,
		Ref:  "api:serve",
		Line: "newest-" + strings.Repeat("x", 52),
	})

	assert.Equal(t, before, logViewport(model, 4))
	assert.NotContains(t, logViewport(model, 4), "newest")
	assert.Contains(t, model.followBadge(len(model.logRows(selectedUnit(t, model), true, model.logInteriorWidth()))), "PAUSED")

	model = press(model, "S")
	assert.Contains(t, logViewport(model, 4), "newest")
	assert.Contains(t, model.followBadge(len(model.logRows(selectedUnit(t, model), true, model.logInteriorWidth()))), "LIVE")
}

func TestDashboardPausedLogIgnoresStatusAndOtherUnitOutput(t *testing.T) {
	model, _ := newTestDashboard(t, nil)
	model.width = 40
	model.focusMode = true
	model = feed(model,
		envruntime.Event{Type: envruntime.EventUnitDeclared, Ref: "api:serve", Kind: "target", Status: "running"},
		envruntime.Event{Type: envruntime.EventUnitDeclared, Ref: "web:serve", Kind: "target", Status: "running"},
	)
	model.selected = "api:serve"
	for i := range 10 {
		model = feed(model, envruntime.Event{Type: envruntime.EventProcessOutput, Ref: "api:serve", Line: fmt.Sprintf("api-%02d", i)})
	}
	model = press(press(model, "S"), "pgup")
	before := logViewport(model, 4)

	model = feed(model,
		envruntime.Event{Type: envruntime.EventReloadStarted, Ref: "api:serve"},
		envruntime.Event{Type: envruntime.EventProcessOutput, Ref: "web:serve", Line: "web-newest"},
	)

	assert.Equal(t, before, logViewport(model, 4))
}

func TestDashboardPausedLogSurvivesHistoryRotation(t *testing.T) {
	model, _ := newTestDashboard(t, nil)
	model.width = 40
	model.focusMode = true
	model = feed(model, envruntime.Event{Type: envruntime.EventUnitDeclared, Ref: "api:serve", Kind: "target", Status: "running"})
	for i := range maxOutputLines {
		model = feed(model, envruntime.Event{Type: envruntime.EventProcessOutput, Ref: "api:serve", Line: fmt.Sprintf("line-%03d", i)})
	}
	model = press(press(model, "S"), "pgup")
	before := logViewport(model, 4)

	model = feed(model, envruntime.Event{Type: envruntime.EventProcessOutput, Ref: "api:serve", Line: "line-500"})

	assert.Equal(t, before, logViewport(model, 4))
}

func TestDashboardWrapShowsAndScrollsLongLines(t *testing.T) {
	model, _ := newTestDashboard(t, nil)
	model.width = 30
	model.focusMode = true
	model = feed(model,
		envruntime.Event{Type: envruntime.EventUnitDeclared, Ref: "api:serve", Kind: "target", Status: "running"},
		envruntime.Event{Type: envruntime.EventProcessOutput, Ref: "api:serve", Line: "HEAD" + strings.Repeat("x", 130) + "TAIL"},
		envruntime.Event{Type: envruntime.EventProcessOutput, Ref: "api:serve", Line: "LATEST"},
	)

	assert.NotContains(t, logViewport(model, 8), "TAIL")
	model = press(model, "W")
	assert.Contains(t, logViewport(model, 8), "TAIL")
	assert.NotContains(t, logViewport(model, 2), "HEAD")

	model = press(model, "pgup")
	assert.Contains(t, logViewport(model, 2), "HEAD")
	assert.NotContains(t, logViewport(model, 2), "TAIL")
	assert.NotContains(t, logViewport(model, 2), "LATEST")

	model = press(model, "W")
	assert.NotContains(t, logViewport(model, 8), "TAIL")
	assert.Contains(t, logViewport(model, 2), "LATEST")
}

func TestDashboardLayoutChangesReturnToLatestLog(t *testing.T) {
	tests := []struct {
		name   string
		update func(dashboardModel) dashboardModel
	}{
		{name: "focus", update: func(model dashboardModel) dashboardModel { return press(model, "f") }},
		{name: "width", update: func(model dashboardModel) dashboardModel {
			next, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: model.height})
			return next.(dashboardModel)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, _ := newTestDashboard(t, nil)
			model = feed(model, envruntime.Event{Type: envruntime.EventUnitDeclared, Ref: "api:serve", Kind: "target", Status: "running"})
			for i := range 10 {
				model = feed(model, envruntime.Event{Type: envruntime.EventProcessOutput, Ref: "api:serve", Line: fmt.Sprintf("line-%02d", i)})
			}
			model = press(press(model, "S"), "pgup")
			require.NotContains(t, logViewport(model, 2), "line-09")

			model = tt.update(model)

			assert.Contains(t, logViewport(model, 2), "line-09")
			assert.Contains(t, model.followBadge(len(model.logRows(selectedUnit(t, model), true, model.logInteriorWidth()))), "PAUSED")
		})
	}
}

func TestDashboardHelpShowsLogControls(t *testing.T) {
	model, _ := newTestDashboard(t, nil)
	model.width = 140
	model.height = 40
	model = press(model, "?")

	view := model.View()
	require.Contains(t, view, "W")
	require.Contains(t, view, "wrap")
	require.Contains(t, view, "S")
	require.Contains(t, view, "auto-scroll")
}

func selectedUnit(t *testing.T, model dashboardModel) unitState {
	t.Helper()
	unit, ok := model.selectedUnit()
	require.True(t, ok)
	return unit
}
