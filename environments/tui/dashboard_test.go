package envtui

import (
	"context"
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"
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
