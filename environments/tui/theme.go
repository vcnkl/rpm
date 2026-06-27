package envtui

import (
	"io"

	"github.com/charmbracelet/lipgloss"
)

var (
	colBrandA  = lipgloss.Color("#22d3ee")
	colBrandB  = lipgloss.Color("#c084fc")
	colRunning = lipgloss.Color("#34d399")
	colReload  = lipgloss.Color("#fbbf24")
	colPending = lipgloss.Color("#94a3b8")
	colFailed  = lipgloss.Color("#f87171")
	colExited  = lipgloss.Color("#9ca3af")
	colStopped = lipgloss.Color("#6b7280")
	colDep     = lipgloss.Color("#60a5fa")
	colBefore  = lipgloss.Color("#c084fc")

	colText   = lipgloss.AdaptiveColor{Light: "#0f172a", Dark: "#e2e8f0"}
	colFaint  = lipgloss.AdaptiveColor{Light: "#64748b", Dark: "#64748b"}
	colBorder = lipgloss.AdaptiveColor{Light: "#cbd5e1", Dark: "#334155"}
	colPanel  = lipgloss.AdaptiveColor{Light: "#e2e8f0", Dark: "#1e293b"}
)

type theme struct {
	r *lipgloss.Renderer

	base       lipgloss.Style
	wordmark   lipgloss.Style
	callsign   lipgloss.Style
	panel      lipgloss.Style
	panelFocus lipgloss.Style
	panelTitle lipgloss.Style
	rowBase    lipgloss.Style
	rowSel     lipgloss.Style
	badge      lipgloss.Style
	dim        lipgloss.Style
	logLine    lipgloss.Style
	errLine    lipgloss.Style
	keycap     lipgloss.Style
	keylabel   lipgloss.Style
	groupRule  lipgloss.Style
	pillBase   lipgloss.Style
	overlay    lipgloss.Style
}

func newTheme(w io.Writer) *theme {
	r := lipgloss.NewRenderer(w)
	t := &theme{r: r}
	t.base = r.NewStyle().Foreground(colText)
	t.wordmark = r.NewStyle().Bold(true).Foreground(colBrandA)
	t.callsign = r.NewStyle().Bold(true).Foreground(colBrandB)
	t.panel = r.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colBorder).Padding(0, 1)
	t.panelFocus = r.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colBrandA).Padding(0, 1)
	t.panelTitle = r.NewStyle().Bold(true).Foreground(colBrandA)
	t.rowBase = r.NewStyle().Foreground(colText)
	t.rowSel = r.NewStyle().Foreground(colText).Bold(true).Background(colPanel)
	t.badge = r.NewStyle().Bold(true)
	t.dim = r.NewStyle().Foreground(colFaint)
	t.logLine = r.NewStyle().Foreground(colText)
	t.errLine = r.NewStyle().Foreground(colFailed)
	t.keycap = r.NewStyle().Bold(true).Foreground(colBrandA)
	t.keylabel = r.NewStyle().Foreground(colFaint)
	t.groupRule = r.NewStyle().Bold(true).Foreground(colFaint)
	t.pillBase = r.NewStyle().Bold(true)
	t.overlay = r.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colBrandB).Padding(1, 3)
	return t
}

func statusColor(status unitStatus) lipgloss.TerminalColor {
	switch status {
	case statusRunning:
		return colRunning
	case statusReloading, statusStarting:
		return colReload
	case statusPending:
		return colPending
	case statusFailed:
		return colFailed
	case statusExited:
		return colExited
	case statusStopped:
		return colStopped
	default:
		return colText
	}
}

func statusGlyph(status unitStatus, frame int) string {
	switch status {
	case statusRunning:
		return "●"
	case statusReloading, statusStarting:
		return spinnerFrame(frame)
	case statusPending:
		return "◌"
	case statusFailed:
		return "✕"
	case statusExited:
		return "◍"
	case statusStopped:
		return "○"
	default:
		return "·"
	}
}

var spinnerFrames = []string{"◜", "◠", "◝", "◞", "◡", "◟"}

func spinnerFrame(frame int) string {
	if len(spinnerFrames) == 0 {
		return "*"
	}
	return spinnerFrames[((frame%len(spinnerFrames))+len(spinnerFrames))%len(spinnerFrames)]
}

func kindBadge(kind unitKind) (string, lipgloss.TerminalColor) {
	switch kind {
	case kindDependency:
		return "dep", colDep
	case kindBefore:
		return "before", colBefore
	case kindTarget:
		return "target", colBrandA
	case kindEnvironment:
		return "env", colFaint
	default:
		return "unit", colFaint
	}
}

func groupLabel(kind unitKind) string {
	switch kind {
	case kindDependency:
		return "DEPENDENCIES"
	case kindBefore:
		return "BEFORE"
	case kindTarget:
		return "TARGETS"
	default:
		return "ENVIRONMENT"
	}
}
