package envtui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func clamp01(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func progressBar(t *theme, frac float64, width int) string {
	if width < 4 {
		width = 4
	}
	frac = clamp01(frac)
	filled := int(math.Round(frac * float64(width)))

	var b strings.Builder
	for i := 0; i < width; i++ {
		glyph := "▱"
		var color lipgloss.TerminalColor = colFaint
		if i < filled {
			glyph = "▰"
			color = colRunning
		}
		b.WriteString(t.r.NewStyle().Foreground(color).Render(glyph))
	}
	return b.String()
}
