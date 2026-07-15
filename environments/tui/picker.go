package envtui

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
)

var ErrSelectionCanceled = errors.New("env TUI selection canceled")

func Select(ctx context.Context, in io.Reader, out io.Writer, request SelectionRequest) ([]string, error) {
	if !CanSelect(in, out) {
		return nil, errors.New("env TUI selection requires a terminal")
	}
	manager := zone.New()
	defer manager.Close()

	model := newPickerModel(newTheme(out), manager, request)
	program := tea.NewProgram(model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		tea.WithContext(ctx),
		tea.WithInput(in),
		tea.WithOutput(out),
	)
	final, err := program.Run()
	if err != nil {
		return nil, err
	}
	result, ok := final.(pickerModel)
	if !ok || result.canceled {
		return nil, ErrSelectionCanceled
	}
	return selectedRefs(result.state), nil
}

type pickerModel struct {
	theme      *theme
	zones      *zone.Manager
	zonePrefix string
	state      selectionState
	requireOne bool
	width      int
	height     int
	scroll     int
	canceled   bool
}

func newPickerModel(t *theme, manager *zone.Manager, request SelectionRequest) pickerModel {
	return pickerModel{
		theme:      t,
		zones:      manager,
		zonePrefix: manager.NewPrefix(),
		state:      initialSelectionState(request),
		requireOne: request.RequireOne,
		width:      100,
		height:     30,
	}
}

func (m pickerModel) Init() tea.Cmd {
	return nil
}

func (m pickerModel) Update(in tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := in.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	return m, nil
}

func (m pickerModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.canceled = true
		return m, tea.Quit
	case "enter":
		if m.requireOne && selectedCount(m.state) == 0 {
			return m, nil
		}
		return m, tea.Quit
	case "up", "k":
		m.state = moveSelectionCursor(m.state, -1)
		return m, nil
	case "down", "j":
		m.state = moveSelectionCursor(m.state, 1)
		return m, nil
	case " ":
		m.state = toggleSelectionItem(m.state)
		return m, nil
	case "right", "l":
		m.state = expandCursor(m.state, true)
		return m, nil
	case "left", "h":
		m.state = expandCursor(m.state, false)
		return m, nil
	}
	return m, nil
}

func (m pickerModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	if msg.Button == tea.MouseButtonWheelUp {
		m.state = moveSelectionCursor(m.state, -1)
		return m, nil
	}
	if msg.Button == tea.MouseButtonWheelDown {
		m.state = moveSelectionCursor(m.state, 1)
		return m, nil
	}
	if msg.Button != tea.MouseButtonLeft {
		return m, nil
	}
	if m.zones.Get(m.zonePrefix + zoneConfirm).InBounds(msg) {
		if m.requireOne && selectedCount(m.state) == 0 {
			return m, nil
		}
		return m, tea.Quit
	}
	if m.zones.Get(m.zonePrefix + zoneCancel).InBounds(msg) {
		m.canceled = true
		return m, tea.Quit
	}
	for index := range m.state.items {
		if m.state.items[index].Hidden {
			continue
		}
		if m.zones.Get(zoneRow(m.zonePrefix, index)).InBounds(msg) {
			m.state.cursor = index
			m.state = toggleSelectionItem(m.state)
			return m, nil
		}
	}
	return m, nil
}

func expandCursor(state selectionState, expand bool) selectionState {
	if state.cursor < 0 || state.cursor >= len(state.items) {
		return state
	}
	item := state.items[state.cursor]
	if !item.Expandable || item.Expanded == expand {
		return state
	}
	return toggleGroupExpansion(state)
}

func (m pickerModel) View() string {
	if m.width < 24 || m.height < 8 {
		return m.theme.dim.Render("rpm env · enlarge the terminal")
	}
	header := m.renderHeader()
	footer := m.renderFooter()
	bodyHeight := m.height - lipgloss.Height(header) - lipgloss.Height(footer)
	if bodyHeight < 3 {
		bodyHeight = 3
	}
	body := m.renderList(m.width, bodyHeight)
	frame := lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
	return m.zones.Scan(frame)
}

func (m pickerModel) renderHeader() string {
	title := m.theme.wordmark.Render("⟫ RPM") + m.theme.callsign.Render(" ENV") +
		"  " + m.theme.base.Bold(true).Render(m.state.title)
	count := m.theme.callsign.Render(strconv.Itoa(selectedCount(m.state)) + " selected")
	gap := m.width - lipgloss.Width(title) - lipgloss.Width(count)
	top := title
	if gap >= 1 {
		top = title + strings.Repeat(" ", gap) + count
	}
	rule := m.theme.r.NewStyle().Foreground(colBrandA).Render(strings.Repeat("━", m.width))
	return lipgloss.JoinVertical(lipgloss.Left, top, rule)
}

func (m pickerModel) renderList(totalW, totalH int) string {
	interior := totalW - 4
	if interior < 10 {
		interior = 10
	}
	interiorH := totalH - 2
	if interiorH < 1 {
		interiorH = 1
	}
	rows := m.buildRows(interior)
	start, count := visibleWindow(len(rows), m.cursorRow(), interiorH)
	lines := make([]string, 0, interiorH)
	for i := start; i < start+count; i++ {
		lines = append(lines, rows[i])
	}
	for len(lines) < interiorH {
		lines = append(lines, m.theme.r.NewStyle().Width(interior).Render(""))
	}
	inner := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return m.theme.panel.Width(interior + 2).Height(interiorH).Render(inner)
}

func (m pickerModel) cursorRow() int {
	row := 0
	for index := range m.state.items {
		if m.state.items[index].Hidden {
			continue
		}
		if index == m.state.cursor {
			return row
		}
		row++
	}
	return 0
}

func (m pickerModel) buildRows(interior int) []string {
	rows := make([]string, 0, len(m.state.items))
	for index := range m.state.items {
		item := m.state.items[index]
		if item.Hidden {
			continue
		}
		rows = append(rows, m.renderRow(index, item, interior))
	}
	return rows
}

func (m pickerModel) renderRow(index int, item SelectionItem, interior int) string {
	selected := index == m.state.cursor
	var box, label string
	if item.Expandable {
		chevron := "▸"
		if item.Expanded {
			chevron = "▾"
		}
		box = m.theme.r.NewStyle().Foreground(colBrandA).Render(chevron + " " + groupBox(m.state, item))
		label = m.theme.base.Bold(true).Render(item.Label)
	} else if item.Header {
		box = "  "
		label = m.theme.groupRule.Render(item.Label)
	} else {
		glyph := "○"
		var color lipgloss.TerminalColor = colFaint
		if item.Selected {
			glyph = "◉"
			color = colRunning
		}
		box = "  " + m.theme.r.NewStyle().Foreground(color).Render(glyph)
		label = m.theme.rowBase.Render(item.Label)
		if item.Detail != "" {
			label += " " + m.theme.dim.Render(item.Detail)
		}
	}
	inner := box + " " + label
	wrap := m.theme.r.NewStyle().Width(interior)
	if selected {
		wrap = wrap.Background(colPanel).Bold(true)
	}
	rendered := wrap.Render(truncateStyled(inner, interior))
	return m.zones.Mark(zoneRow(m.zonePrefix, index), rendered)
}

func groupBox(state selectionState, group SelectionItem) string {
	total, selected := 0, 0
	for _, item := range state.items {
		if item.Group != group.Group || item.Header || item.Expandable {
			continue
		}
		total++
		if item.Selected {
			selected++
		}
	}
	switch {
	case total == 0:
		return "◻"
	case selected == 0:
		return "◻"
	case selected == total:
		return "◼"
	default:
		return "◧"
	}
}

func (m pickerModel) renderFooter() string {
	chips := make([]string, 0, len(pickerHints))
	for _, hint := range pickerHints {
		chips = append(chips, m.theme.keycap.Render(hint.keys)+" "+m.theme.keylabel.Render(hint.label))
	}
	confirm := m.zones.Mark(m.zonePrefix+zoneConfirm, m.theme.r.NewStyle().Bold(true).Foreground(colRunning).Render("[ confirm ]"))
	cancel := m.zones.Mark(m.zonePrefix+zoneCancel, m.theme.r.NewStyle().Bold(true).Foreground(colFailed).Render("[ cancel ]"))
	left := strings.Join(chips, "  ")
	right := confirm + "  " + cancel
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return truncateStyled(left+"  "+right, m.width)
	}
	return left + strings.Repeat(" ", gap) + right
}
