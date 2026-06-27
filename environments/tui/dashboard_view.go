package envtui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const barWidth = 22

const (
	cpuColWidth      = 6
	memColWidth      = 7
	restartSlotWidth = 2
	metricsBlockSep  = 1
	rosterBaseUsed   = 11
	minRosterLabel   = 8
)

const metricsBlockWidth = metricsBlockSep + cpuColWidth + memColWidth + restartSlotWidth

func metricsColumnsFit(interior int) bool {
	return interior >= rosterBaseUsed+metricsBlockWidth+minRosterLabel
}

func (m dashboardModel) View() string {
	if m.width < 24 || m.height < 8 {
		return m.theme.dim.Render("rpm env · enlarge the terminal")
	}
	header := m.renderHeader(m.width)
	footer := m.renderFooter(m.width)
	bodyHeight := m.height - lipgloss.Height(header) - lipgloss.Height(footer)
	if bodyHeight < 3 {
		bodyHeight = 3
	}
	body := m.renderBody(m.width, bodyHeight)
	frame := lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
	if m.showHelp {
		frame = m.renderHelp()
	}
	return m.zones.Scan(frame)
}

func (m dashboardModel) barFraction() float64 {
	if m.animate {
		return clamp01(m.barPos)
	}
	return m.runningFraction()
}

func (m dashboardModel) renderHeader(width int) string {
	summary := summarize(m.state.units)
	frac := m.barFraction()

	if width < logFloorWidth {
		line := fmt.Sprintf("%s%s  %s",
			m.theme.wordmark.Render("⟫ RPM"),
			m.theme.callsign.Render(" ENV"),
			m.theme.base.Bold(true).Render(m.state.blueprint))
		rule := m.theme.r.NewStyle().Foreground(colBorder).Render(strings.Repeat("─", width))
		return lipgloss.JoinVertical(lipgloss.Left, truncateStyled(line, width), rule)
	}

	left := lipgloss.JoinVertical(lipgloss.Left,
		m.theme.wordmark.Render("⟫ RPM")+m.theme.callsign.Render(" ENV"),
		m.theme.base.Bold(true).Render(m.state.blueprint),
		m.theme.dim.Render(fmt.Sprintf("%d targets", summary.targets)),
	)

	barRow := progressBar(m.theme, frac, barWidth) +
		" " + m.theme.callsign.Render(fmt.Sprintf("%d%%", int(math.Round(frac*100))))
	right := lipgloss.JoinVertical(lipgloss.Right,
		barRow,
		m.renderPills(summary),
		m.metricsReadout(),
		m.theme.dim.Render("⧗ "+formatDuration(time.Since(m.startedAt))),
	)

	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	spacer := m.theme.r.NewStyle().Width(gap).Render("")
	top := lipgloss.JoinHorizontal(lipgloss.Top, left, spacer, right)
	rule := m.theme.r.NewStyle().Foreground(colBrandA).Render(strings.Repeat("━", width))
	return lipgloss.JoinVertical(lipgloss.Left, top, rule)
}

func (m dashboardModel) renderPills(summary unitSummary) string {
	pills := []string{m.pill(fmt.Sprintf("● %d running", summary.running), colRunning)}
	if summary.reloading > 0 {
		pills = append(pills, m.pill(fmt.Sprintf("↻ %d", summary.reloading), colReload))
	}
	if summary.pending > 0 {
		pills = append(pills, m.pill(fmt.Sprintf("◌ %d", summary.pending), colPending))
	}
	if summary.failed > 0 {
		pills = append(pills, m.pill(fmt.Sprintf("✕ %d failed", summary.failed), colFailed))
	}
	return strings.Join(pills, "  ")
}

func (m dashboardModel) pill(label string, color lipgloss.TerminalColor) string {
	return m.theme.r.NewStyle().Bold(true).Foreground(color).Render(label)
}

func (m dashboardModel) metricsReadout() string {
	return m.theme.dim.Render("CPU ") +
		m.theme.callsign.Render(fmt.Sprintf("%.1f%%", m.metricsTotal.CPU)) +
		m.theme.dim.Render("  MEM ") +
		m.theme.callsign.Render(formatBytes(m.metricsTotal.MemBytes))
}

func (m dashboardModel) renderBody(width, height int) string {
	if m.focusMode {
		return m.renderLog(width, height, true)
	}
	if width < stackWidth {
		rosterH := height / 2
		if rosterH < 3 {
			rosterH = 3
		}
		logH := height - rosterH
		if logH < 3 {
			logH = 3
			rosterH = height - logH
		}
		roster := m.renderRoster(width, rosterH)
		log := m.renderLog(width, logH, false)
		return lipgloss.JoinVertical(lipgloss.Left, roster, log)
	}
	leftW := int(float64(width) * 0.44)
	if leftW < leftPanelMin {
		leftW = leftPanelMin
	}
	if leftW > width-leftPanelMin {
		leftW = width - leftPanelMin
	}
	rightW := width - leftW
	roster := m.renderRoster(leftW, height)
	log := m.renderLog(rightW, height, false)
	return lipgloss.JoinHorizontal(lipgloss.Top, roster, log)
}

func (m dashboardModel) renderRoster(totalW, totalH int) string {
	interior := totalW - 4
	if interior < 10 {
		interior = 10
	}
	interiorH := totalH - 2
	if interiorH < 1 {
		interiorH = 1
	}
	title := m.rosterTitle(interior)
	rowsHeight := interiorH - 1
	if rowsHeight < 1 {
		rowsHeight = 1
	}

	rows, selectedRow := m.buildRosterRows()
	start, count := visibleWindow(len(rows), selectedRow, rowsHeight)
	lines := make([]string, 0, rowsHeight)
	for i := start; i < start+count; i++ {
		lines = append(lines, m.renderRosterRow(rows[i], interior))
	}
	for len(lines) < rowsHeight {
		lines = append(lines, m.theme.r.NewStyle().Width(interior).Render(""))
	}
	inner := lipgloss.JoinVertical(lipgloss.Left, append([]string{title}, lines...)...)
	return m.theme.panel.Width(interior + 2).Height(interiorH).Render(inner)
}

func (m dashboardModel) rosterTitle(interior int) string {
	label := m.theme.panelTitle.Render("TARGETS")
	suffix := ""
	if m.filtering || m.filter != "" {
		suffix = m.theme.callsign.Render(" /" + m.filter + cursorGlyph(m.filtering))
	} else if !m.state.showDependencies {
		suffix = m.theme.dim.Render(" deps hidden")
	}
	left := label + suffix
	if !metricsColumnsFit(interior) {
		return truncateStyled(left, interior)
	}
	headers := strings.Repeat(" ", metricsBlockSep) +
		m.theme.dim.Render(padLeft("CPU", cpuColWidth)) +
		m.theme.dim.Render(padLeft("MEM", memColWidth)) +
		strings.Repeat(" ", restartSlotWidth)
	gap := interior - lipgloss.Width(left) - lipgloss.Width(headers)
	if gap < 1 {
		return truncateStyled(left+headers, interior)
	}
	return left + strings.Repeat(" ", gap) + headers
}

func cursorGlyph(active bool) string {
	if active {
		return "▏"
	}
	return ""
}

type rosterRow struct {
	header    bool
	kind      unitKind
	unit      unitState
	unitIndex int
}

func (m dashboardModel) buildRosterRows() ([]rosterRow, int) {
	visible := m.visibleUnits()
	rows := make([]rosterRow, 0, len(visible)+3)
	selectedRow := 0
	lastKind := unitKind("")
	for i, unit := range visible {
		if unit.Kind != lastKind {
			rows = append(rows, rosterRow{header: true, kind: unit.Kind})
			lastKind = unit.Kind
		}
		if unit.Ref == m.selected {
			selectedRow = len(rows)
		}
		rows = append(rows, rosterRow{unit: unit, kind: unit.Kind, unitIndex: i})
	}
	return rows, selectedRow
}

func (m dashboardModel) renderRosterRow(row rosterRow, interior int) string {
	if row.header {
		if row.kind == kindTarget {
			return m.theme.groupRule.Render(truncate(strings.Repeat("─", interior), interior))
		}
		rule := "── " + groupLabel(row.kind) + " "
		rule = rule + strings.Repeat("─", maxInt(0, interior-len([]rune(rule))))
		return m.theme.groupRule.Render(truncate(rule, interior))
	}
	unit := row.unit
	selected := unit.Ref == m.selected

	accent := "  "
	if selected {
		accent = m.theme.r.NewStyle().Foreground(colBrandA).Render("▎") + " "
	}
	glyph := m.theme.r.NewStyle().Foreground(statusColor(unit.Status)).Render(statusGlyph(unit.Status, m.spinnerTicks))
	badgeText, badgeColor := kindBadge(unit.Kind)
	badge := m.theme.r.NewStyle().Foreground(badgeColor).Bold(true).Render(padRight(badgeText, 6))

	showMetrics := metricsColumnsFit(interior)
	used := rosterBaseUsed
	var right string
	if showMetrics {
		used += metricsBlockWidth
		right = m.rosterMetricsBlock(unit, row.unitIndex)
	} else if unit.Kind == kindTarget {
		used += restartSlotWidth
		right = " " + m.zones.Mark(zoneRestart(m.zonePrefix, row.unitIndex), m.theme.r.NewStyle().Foreground(colReload).Render("⟳"))
	}
	avail := interior - used
	if avail < 1 {
		avail = 1
	}
	label := unit.Ref
	if unit.Error != "" {
		label = unit.Ref + "  " + unit.Error
	}
	label = truncate(label, avail)
	if showMetrics {
		label = padRight(label, avail)
	}
	labelStyle := m.theme.rowBase
	if unit.Status == statusFailed {
		labelStyle = m.theme.errLine
	}

	inner := accent + glyph + " " + badge + " " + labelStyle.Render(label) + right
	wrap := m.theme.r.NewStyle().Width(interior)
	if selected {
		wrap = wrap.Background(colPanel).Bold(true)
	}
	return m.zones.Mark(zoneRow(m.zonePrefix, row.unitIndex), wrap.Render(inner))
}

func (m dashboardModel) rosterMetricsBlock(unit unitState, index int) string {
	cpuText, memText := "—", "—"
	if sample, ok := m.targetMetrics[unit.Ref]; ok {
		cpuText = fmt.Sprintf("%.1f%%", sample.CPU)
		memText = formatBytes(sample.MemBytes)
	}
	cpuCol := m.theme.dim.Render(padLeft(cpuText, cpuColWidth))
	memCol := m.theme.dim.Render(padLeft(memText, memColWidth))
	restart := strings.Repeat(" ", restartSlotWidth)
	if unit.Kind == kindTarget {
		restart = " " + m.zones.Mark(zoneRestart(m.zonePrefix, index), m.theme.r.NewStyle().Foreground(colReload).Render("⟳"))
	}
	return strings.Repeat(" ", metricsBlockSep) + cpuCol + memCol + restart
}

func (m dashboardModel) renderLog(totalW, totalH int, focused bool) string {
	interior := totalW - 4
	if interior < 10 {
		interior = 10
	}
	interiorH := totalH - 2
	if interiorH < 1 {
		interiorH = 1
	}
	unit, ok := m.selectedUnit()
	title := m.logTitle(unit, ok, interior, focused)
	logHeight := interiorH - 1
	if logHeight < 1 {
		logHeight = 1
	}

	lines := m.logWindow(unit, ok, logHeight, interior)
	inner := lipgloss.JoinVertical(lipgloss.Left, append([]string{title}, lines...)...)
	style := m.theme.panel
	if focused {
		style = m.theme.panelFocus
	}
	panel := style.Width(interior + 2).Height(interiorH).Render(inner)
	return m.zones.Mark(zoneLog, panel)
}

func (m dashboardModel) logTitle(unit unitState, ok bool, interior int, focused bool) string {
	name := m.theme.panelTitle.Render("LOGS")
	if focused {
		name = m.theme.panelTitle.Render("LOGS · FOCUS")
	}
	if !ok {
		return truncateStyled(name, interior)
	}
	badge := m.theme.r.NewStyle().Foreground(statusColor(unit.Status)).Bold(true).
		Render(" " + unit.Ref + " · " + string(unit.Status))
	follow := m.followBadge(unit)
	head := name + badge
	gap := interior - lipgloss.Width(head) - lipgloss.Width(follow)
	if gap < 1 {
		return truncateStyled(head+" "+follow, interior)
	}
	return head + strings.Repeat(" ", gap) + follow
}

func (m dashboardModel) followBadge(unit unitState) string {
	if m.logScroll == 0 {
		return m.theme.r.NewStyle().Foreground(colRunning).Bold(true).Render("LIVE")
	}
	shown := len(unit.Output) - m.logScroll
	if shown < 0 {
		shown = 0
	}
	return m.theme.r.NewStyle().Foreground(colReload).Bold(true).
		Render(fmt.Sprintf("PAUSED %d/%d", shown, len(unit.Output)))
}

func (m dashboardModel) logWindow(unit unitState, ok bool, height, interior int) []string {
	lines := make([]string, 0, height)
	if !ok || len(unit.Output) == 0 {
		hint := "no output yet"
		if ok {
			hint = "waiting for " + unit.Ref
		}
		lines = append(lines, m.theme.dim.Render(truncate(hint, interior)))
		for len(lines) < height {
			lines = append(lines, m.theme.r.NewStyle().Width(interior).Render(""))
		}
		return lines
	}
	end := len(unit.Output) - m.logScroll
	if end > len(unit.Output) {
		end = len(unit.Output)
	}
	if end < 1 {
		end = 1
	}
	start := end - height
	if start < 0 {
		start = 0
	}
	for _, line := range unit.Output[start:end] {
		lines = append(lines, m.theme.logLine.Render(truncate(line, interior)))
	}
	for len(lines) < height {
		lines = append(lines, m.theme.r.NewStyle().Width(interior).Render(""))
	}
	return lines
}

func (m dashboardModel) renderFooter(width int) string {
	chips := make([]string, 0, len(dashboardHints))
	for _, hint := range dashboardHints {
		chip := m.theme.keycap.Render(hint.keys) + " " + m.theme.keylabel.Render(hint.label)
		switch hint.label {
		case "restart all":
			chip = m.zones.Mark(m.zonePrefix+zoneRestartAll, chip)
		case "deps":
			chip = m.zones.Mark(m.zonePrefix+zoneDeps, chip)
		case "focus":
			chip = m.zones.Mark(m.zonePrefix+zoneFocus, chip)
		case "filter":
			chip = m.zones.Mark(m.zonePrefix+zoneFilter, chip)
		case "quit":
			chip = m.zones.Mark(m.zonePrefix+zoneQuit, chip)
		}
		chips = append(chips, chip)
	}
	left := strings.Join(chips, "  ")
	right := m.footerReadout()
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return truncateStyled(left, width)
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m dashboardModel) footerReadout() string {
	visible := m.visibleUnits()
	if len(visible) == 0 {
		return m.theme.dim.Render("no targets")
	}
	position := m.selectedIndex(visible) + 1
	return m.theme.dim.Render(fmt.Sprintf("%d/%d", position, len(visible)))
}

func (m dashboardModel) renderHelp() string {
	rows := make([]string, 0, len(dashboardHints)+1)
	rows = append(rows, m.theme.panelTitle.Render("KEYBOARD"))
	for _, hint := range dashboardHints {
		rows = append(rows, m.theme.keycap.Render(padRight(hint.keys, 6))+"  "+m.theme.keylabel.Render(hint.label))
	}
	box := m.theme.overlay.Render(lipgloss.JoinVertical(lipgloss.Left, rows...))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func truncateStyled(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(s)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	value := float64(b) / float64(div)
	suffix := []string{"KB", "MB", "GB", "TB", "PB"}[exp]
	if value >= 100 {
		return fmt.Sprintf("%.0f%s", value, suffix)
	}
	return fmt.Sprintf("%.1f%s", value, suffix)
}
