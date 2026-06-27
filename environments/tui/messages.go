package envtui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	envruntime "github.com/vcnkl/rpm/environments/runtime"
)

const frameInterval = time.Second / 60

type eventMsg struct {
	event envruntime.Event
}

type tickMsg struct{}

type runnerDoneMsg struct {
	err error
}

func tick() tea.Cmd {
	return tea.Tick(frameInterval, func(time.Time) tea.Msg { return tickMsg{} })
}
