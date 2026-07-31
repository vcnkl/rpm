package envtui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vcwx/rpm/environments/metrics"
	envruntime "github.com/vcwx/rpm/environments/runtime"
)

const frameInterval = time.Second / 60

const metricsInterval = time.Second

type eventMsg struct {
	event envruntime.Event
}

type metricsMsg struct {
	snapshot metrics.Snapshot
}

type tickMsg struct{}

type runnerDoneMsg struct {
	err error
}

func tick() tea.Cmd {
	return tea.Tick(frameInterval, func(time.Time) tea.Msg { return tickMsg{} })
}
