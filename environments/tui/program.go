package envtui

import (
	"context"
	"io"
	"os"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"
	envruntime "github.com/vcnkl/rpm/environments/runtime"
)

type Controller interface {
	Restart(ctx context.Context, ref string) error
	RestartAll(ctx context.Context) error
	Stop()
}

type ProgramSink struct {
	mu      sync.RWMutex
	program *tea.Program
}

func NewProgramSink() *ProgramSink {
	return &ProgramSink{}
}

func (s *ProgramSink) Bind(program *tea.Program) {
	s.mu.Lock()
	s.program = program
	s.mu.Unlock()
}

func (s *ProgramSink) Emit(event envruntime.Event) {
	s.mu.RLock()
	program := s.program
	s.mu.RUnlock()
	if program != nil {
		program.Send(eventMsg{event: event})
	}
}

type DashboardConfig struct {
	Blueprint  string
	Sink       *ProgramSink
	Controller Controller
	Run        func(ctx context.Context) error
	Input      io.Reader
	Output     io.Writer
}

func RunDashboard(ctx context.Context, cfg DashboardConfig) error {
	zone.NewGlobal()
	defer zone.Close()

	output := cfg.Output
	if output == nil {
		output = os.Stderr
	}
	model := newDashboardModel(newTheme(output), zone.DefaultManager, cfg.Controller, cfg.Blueprint, true)
	model.ctx = ctx

	opts := []tea.ProgramOption{
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		tea.WithContext(ctx),
		tea.WithOutput(output),
	}
	if cfg.Input != nil {
		opts = append(opts, tea.WithInput(cfg.Input))
	}
	program := tea.NewProgram(model, opts...)
	cfg.Sink.Bind(program)

	runErr := make(chan error, 1)
	go func() {
		err := cfg.Run(ctx)
		runErr <- err
		program.Send(runnerDoneMsg{err: err})
	}()

	_, uiErr := program.Run()
	if err := <-runErr; err != nil {
		return err
	}
	return uiErr
}
