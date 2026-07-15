package envtui

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/harmonica"
	zone "github.com/lrstanley/bubblezone"
	"github.com/vcnkl/rpm/environments/metrics"
)

const (
	animationFPS  = 30
	stackWidth    = 90
	logFloorWidth = 60
	leftPanelMin  = 28
	settleEpsilon = 0.004
)

type dashboardModel struct {
	theme      *theme
	zones      *zone.Manager
	zonePrefix string
	controller Controller
	ctx        context.Context

	state envState

	width  int
	height int

	focusMode bool
	filtering bool
	filter    string
	showHelp  bool

	logScroll  int
	autoScroll bool
	wrapLogs   bool
	selected   string

	stopping bool
	finished bool
	runErr   error

	animate      bool
	ticking      bool
	spring       harmonica.Spring
	barPos       float64
	barVel       float64
	spinnerTicks int

	targetMetrics map[string]metrics.Sample
	metricsTotal  metrics.Sample

	startedAt time.Time
}

func newDashboardModel(t *theme, mgr *zone.Manager, controller Controller, blueprint string, animate bool) dashboardModel {
	return dashboardModel{
		theme:         t,
		zones:         mgr,
		zonePrefix:    mgr.NewPrefix(),
		controller:    controller,
		state:         newEnvState(blueprint),
		width:         100,
		height:        30,
		autoScroll:    true,
		animate:       animate,
		spring:        harmonica.NewSpring(harmonica.FPS(animationFPS), 6.0, 0.85),
		targetMetrics: make(map[string]metrics.Sample),
		startedAt:     time.Now(),
	}
}

func (m dashboardModel) Init() tea.Cmd {
	return nil
}

func (m dashboardModel) Update(in tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := in.(type) {
	case tea.WindowSizeMsg:
		if m.width != msg.Width {
			m.logScroll = 0
		}
		m.width = msg.Width
		m.height = msg.Height
		next, cmd := m.scheduleTick()
		return next, cmd
	case eventMsg:
		before, beforeOK := m.selectedUnit()
		m.state = applyEvent(m.state, msg.event)
		m.reconcileSelection()
		after, afterOK := m.selectedUnit()
		if !beforeOK || !afterOK || before.Ref != after.Ref {
			m.logScroll = 0
		} else if after.OutputCount > before.OutputCount {
			if m.autoScroll {
				m.logScroll = 0
			} else {
				added := after.OutputCount - before.OutputCount
				if added > len(after.Output) {
					added = len(after.Output)
				}
				for _, line := range after.Output[len(after.Output)-added:] {
					m.logScroll += len(m.logLineRows(line, m.logInteriorWidth()))
				}
			}
		}
		next, cmd := m.scheduleTick()
		return next, cmd
	case metricsMsg:
		m.targetMetrics = msg.snapshot.Targets
		m.metricsTotal = msg.snapshot.Total
		return m, nil
	case runnerDoneMsg:
		m.finished = true
		m.runErr = msg.err
		return m, tea.Quit
	case tickMsg:
		m.ticking = false
		return m.advance()
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m dashboardModel) advance() (tea.Model, tea.Cmd) {
	if !m.animate {
		return m, nil
	}
	target := m.runningFraction()
	m.barPos, m.barVel = m.spring.Update(m.barPos, m.barVel, target)
	m.spinnerTicks++
	return m.scheduleTick()
}

func (m dashboardModel) scheduleTick() (dashboardModel, tea.Cmd) {
	if m.animate && !m.ticking && m.needsAnimation() {
		m.ticking = true
		return m, tick()
	}
	return m, nil
}

func (m dashboardModel) needsAnimation() bool {
	target := m.runningFraction()
	if absf(m.barPos-target) > settleEpsilon || absf(m.barVel) > settleEpsilon {
		return true
	}
	for _, unit := range m.state.units {
		if unit.Status == statusReloading || unit.Status == statusStarting {
			return true
		}
	}
	return false
}

func (m dashboardModel) runningFraction() float64 {
	total, running := 0, 0
	for _, unit := range m.state.units {
		if unit.Kind == kindDependency {
			continue
		}
		total++
		if unit.Status == statusRunning {
			running++
		}
	}
	if total == 0 {
		for _, unit := range m.state.units {
			total++
			if unit.Status == statusRunning {
				running++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return clamp01(float64(running) / float64(total))
}

func (m dashboardModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.showHelp {
		if matchKey(key, "?", "esc", "enter", "q") {
			m.showHelp = false
		}
		return m, nil
	}
	if m.filtering {
		return m.handleFilterKey(msg)
	}
	switch {
	case matchKey(key, "q", "ctrl+c"):
		return m.requestStop()
	case matchKey(key, "up", "k"):
		m.moveSelection(-1)
		m.logScroll = 0
		return m, nil
	case matchKey(key, "down", "j"):
		m.moveSelection(1)
		m.logScroll = 0
		return m, nil
	case matchKey(key, "pgup"):
		m.logScroll += 5
		return m, nil
	case matchKey(key, "pgdown"):
		m.logScroll -= 5
		if m.logScroll < 0 {
			m.logScroll = 0
		}
		return m, nil
	case matchKey(key, "W"):
		m.wrapLogs = !m.wrapLogs
		m.logScroll = 0
		return m, nil
	case matchKey(key, "S"):
		m.autoScroll = !m.autoScroll
		if m.autoScroll {
			m.logScroll = 0
		}
		return m, nil
	case matchKey(key, "r"):
		return m, m.restartSelected()
	case matchKey(key, "R"):
		return m, m.restartAll()
	case matchKey(key, "d"):
		m.state.showDependencies = !m.state.showDependencies
		m.reconcileSelection()
		return m, nil
	case matchKey(key, "f"):
		m.focusMode = !m.focusMode
		m.logScroll = 0
		return m, nil
	case matchKey(key, "/"):
		m.filtering = true
		return m, nil
	case matchKey(key, "?"):
		m.showHelp = true
		return m, nil
	}
	return m, nil
}

func (m dashboardModel) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filtering = false
		m.filter = ""
		m.reconcileSelection()
		return m, nil
	case "enter":
		m.filtering = false
		return m, nil
	case "backspace":
		if m.filter != "" {
			_, size := utf8.DecodeLastRuneInString(m.filter)
			m.filter = m.filter[:len(m.filter)-size]
		}
		m.reconcileSelection()
		return m, nil
	case "up":
		m.moveSelection(-1)
		return m, nil
	case "down":
		m.moveSelection(1)
		return m, nil
	}
	if msg.Type == tea.KeyRunes {
		m.filter += string(msg.Runes)
		m.reconcileSelection()
	}
	return m, nil
}

func (m dashboardModel) requestStop() (tea.Model, tea.Cmd) {
	if m.stopping {
		return m, tea.Quit
	}
	m.stopping = true
	controller := m.controller
	return m, func() tea.Msg {
		if controller != nil {
			controller.Stop()
		}
		return nil
	}
}

func (m dashboardModel) restartSelected() tea.Cmd {
	unit, ok := m.selectedUnit()
	if !ok || unit.Kind != kindTarget || m.controller == nil {
		return nil
	}
	controller := m.controller
	ctx := m.dispatchContext()
	ref := unit.Ref
	return func() tea.Msg {
		_ = controller.Restart(ctx, ref)
		return nil
	}
}

func (m dashboardModel) restartAll() tea.Cmd {
	if m.controller == nil {
		return nil
	}
	controller := m.controller
	ctx := m.dispatchContext()
	return func() tea.Msg {
		_ = controller.RestartAll(ctx)
		return nil
	}
}

func (m dashboardModel) dispatchContext() context.Context {
	if m.ctx != nil {
		return m.ctx
	}
	return context.Background()
}

func (m dashboardModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	if msg.Button == tea.MouseButtonWheelUp && m.zones.Get(zoneLog).InBounds(msg) {
		m.logScroll++
		return m, nil
	}
	if msg.Button == tea.MouseButtonWheelDown && m.zones.Get(zoneLog).InBounds(msg) {
		if m.logScroll > 0 {
			m.logScroll--
		}
		return m, nil
	}
	if msg.Button != tea.MouseButtonLeft {
		return m, nil
	}
	switch {
	case m.zones.Get(m.zonePrefix + zoneRestartAll).InBounds(msg):
		return m, m.restartAll()
	case m.zones.Get(m.zonePrefix + zoneDeps).InBounds(msg):
		m.state.showDependencies = !m.state.showDependencies
		m.reconcileSelection()
		return m, nil
	case m.zones.Get(m.zonePrefix + zoneFocus).InBounds(msg):
		m.focusMode = !m.focusMode
		m.logScroll = 0
		return m, nil
	case m.zones.Get(m.zonePrefix + zoneFilter).InBounds(msg):
		m.filtering = !m.filtering
		return m, nil
	case m.zones.Get(m.zonePrefix + zoneQuit).InBounds(msg):
		return m.requestStop()
	}
	visible := m.visibleUnits()
	for i, unit := range visible {
		if m.zones.Get(zoneRestart(m.zonePrefix, i)).InBounds(msg) {
			if unit.Kind == kindTarget {
				m.selected = unit.Ref
				return m, m.restartSelected()
			}
		}
		if m.zones.Get(zoneRow(m.zonePrefix, i)).InBounds(msg) {
			m.selected = unit.Ref
			m.logScroll = 0
			return m, nil
		}
	}
	return m, nil
}

func (m *dashboardModel) moveSelection(delta int) {
	visible := m.visibleUnits()
	if len(visible) == 0 {
		m.selected = ""
		return
	}
	index := m.selectedIndex(visible)
	index += delta
	if index < 0 {
		index = 0
	}
	if index > len(visible)-1 {
		index = len(visible) - 1
	}
	m.selected = visible[index].Ref
}

func (m *dashboardModel) reconcileSelection() {
	visible := m.visibleUnits()
	if len(visible) == 0 {
		m.selected = ""
		return
	}
	for _, unit := range visible {
		if unit.Ref == m.selected {
			return
		}
	}
	all := orderUnits(m.state.units, m.state.showDependencies)
	if m.state.selected >= 0 && m.state.selected < len(all) {
		ref := all[m.state.selected].Ref
		for _, unit := range visible {
			if unit.Ref == ref {
				m.selected = ref
				return
			}
		}
	}
	m.selected = visible[0].Ref
}

func (m dashboardModel) visibleUnits() []unitState {
	ordered := orderUnits(m.state.units, m.state.showDependencies)
	if strings.TrimSpace(m.filter) == "" {
		return ordered
	}
	filtered := make([]unitState, 0, len(ordered))
	for _, unit := range ordered {
		if subsequenceMatch(m.filter, unit.Ref) {
			filtered = append(filtered, unit)
		}
	}
	return filtered
}

func (m dashboardModel) selectedIndex(visible []unitState) int {
	for i, unit := range visible {
		if unit.Ref == m.selected {
			return i
		}
	}
	return 0
}

func (m dashboardModel) selectedUnit() (unitState, bool) {
	visible := m.visibleUnits()
	if len(visible) == 0 {
		return unitState{}, false
	}
	return visible[m.selectedIndex(visible)], true
}

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func subsequenceMatch(pattern, target string) bool {
	pattern = strings.ToLower(pattern)
	target = strings.ToLower(target)
	if pattern == "" {
		return true
	}
	i := 0
	for _, r := range target {
		if rune(pattern[i]) == r {
			i++
			if i == len(pattern) {
				return true
			}
		}
	}
	return false
}

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(s)
	return string(runes[:width-1]) + "…"
}

func padRight(s string, width int) string {
	gap := width - utf8.RuneCountInString(s)
	if gap <= 0 {
		return s
	}
	return s + strings.Repeat(" ", gap)
}

func padLeft(s string, width int) string {
	gap := width - utf8.RuneCountInString(s)
	if gap <= 0 {
		return s
	}
	return strings.Repeat(" ", gap) + s
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d", total/60, total%60)
}
