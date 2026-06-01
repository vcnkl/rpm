package create

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"golang.org/x/term"

	"github.com/vcnkl/rpm/dag"
	"github.com/vcnkl/rpm/models"
)

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiCyan   = "\x1b[36m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiInvert = "\x1b[7m"
)

type terminalSelector struct {
	in  *os.File
	out *os.File
}

type selectItem struct {
	ref      string
	label    string
	detail   string
	group    string
	tier     int
	selected bool
	defaults bool
	header   bool
	muted    bool
}

type selectorModel struct {
	title  string
	items  []selectItem
	cursor int
	offset int
}

func newTerminalSelector(in io.Reader, out io.Writer) (*terminalSelector, bool) {
	inFile, ok := in.(*os.File)
	if !ok {
		return nil, false
	}
	outFile, ok := out.(*os.File)
	if !ok {
		return nil, false
	}
	if !term.IsTerminal(int(inFile.Fd())) || !term.IsTerminal(int(outFile.Fd())) {
		return nil, false
	}
	return &terminalSelector{in: inFile, out: outFile}, true
}

func (s *terminalSelector) selectTargets(title string, targets []*models.Target, graph *dag.Graph, selected []string) ([]string, error) {
	model := selectorModel{
		title: title,
		items: targetSelectItems(targets, graph, selected),
	}
	model.cursor = model.firstSelectable()
	return s.run(model, ErrMissingTargets)
}

func (s *terminalSelector) selectDependencies(title string, refs []string, selected []string) ([]string, error) {
	model := selectorModel{
		title: title,
		items: dependencySelectItems(refs, selected),
	}
	model.cursor = model.firstSelectable()
	return s.run(model, nil)
}

func (s *terminalSelector) run(model selectorModel, emptyErr error) ([]string, error) {
	oldState, err := term.MakeRaw(int(s.in.Fd()))
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = term.Restore(int(s.in.Fd()), oldState)
		_, _ = fmt.Fprint(s.out, "\x1b[?1049l\x1b[?25h")
	}()

	_, _ = fmt.Fprint(s.out, "\x1b[?1049h\x1b[?25l")
	reader := bufio.NewReader(s.in)
	for {
		s.render(model)
		key, err := readSelectorKey(reader)
		if err != nil {
			return nil, err
		}
		switch key {
		case selectorKeyUp:
			model.move(-1)
		case selectorKeyDown:
			model.move(1)
		case selectorKeyToggle:
			model.toggle()
		case selectorKeyEnter:
			refs := model.selectedRefs()
			if len(refs) == 0 && emptyErr != nil {
				return nil, emptyErr
			}
			return refs, nil
		case selectorKeyCancel:
			return nil, fmt.Errorf("selection cancelled")
		}
	}
}

func (s *terminalSelector) render(model selectorModel) {
	width, height, err := term.GetSize(int(s.out.Fd()))
	if err != nil || width < 20 {
		width = 80
	}
	if height < 8 {
		height = 24
	}
	bodyHeight := height - 5
	model.ensureVisible(bodyHeight)

	var b strings.Builder
	b.WriteString("\x1b[H\x1b[2J")
	b.WriteString(colorPad(ansiBold+ansiCyan, model.title, width))
	b.WriteString("\n")
	b.WriteString(pad(fmt.Sprintf("%d selected  %s", len(model.selectedRefs()), "up/down move  space toggle  enter accept  esc cancel"), width))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("-", width))
	b.WriteString("\n")
	end := min(len(model.items), model.offset+bodyHeight)
	for i := model.offset; i < end; i++ {
		item := model.items[i]
		line := renderSelectItem(item, i == model.cursor, width)
		b.WriteString(line)
		b.WriteString("\n")
	}
	for i := end - model.offset; i < bodyHeight; i++ {
		b.WriteString(strings.Repeat(" ", width))
		b.WriteString("\n")
	}
	b.WriteString(strings.Repeat("-", width))
	b.WriteString("\n")
	b.WriteString(pad("Default selections are checked before the list opens.", width))
	_, _ = fmt.Fprint(s.out, b.String())
}

func targetSelectItems(targets []*models.Target, graph *dag.Graph, selected []string) []selectItem {
	selectedSet := stringSet(selected)
	useDefaultSuffixes := len(selectedSet) == 0
	items := make([]selectItem, 0, len(targets)+8)
	for _, target := range targets {
		ref := target.ID()
		defaults := strings.HasSuffix(target.Name, "_dev") || strings.HasSuffix(target.Name, "_serve")
		isSelected := selectedSet[ref] || (useDefaultSuffixes && defaults)
		group := "other targets"
		if defaults {
			group = "env candidates"
		}
		items = append(items, selectItem{
			ref:      ref,
			label:    ref,
			detail:   target.BundlePath,
			group:    group,
			tier:     targetTier(graph, ref),
			selected: isSelected,
			defaults: defaults,
			muted:    !defaults,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].group != items[j].group {
			return items[i].group == "env candidates"
		}
		if items[i].tier != items[j].tier {
			return items[i].tier < items[j].tier
		}
		return items[i].ref < items[j].ref
	})
	return withTierHeadings(items)
}

func dependencySelectItems(refs []string, selected []string) []selectItem {
	selectedSet := stringSet(selected)
	items := make([]selectItem, 0, len(refs)+8)
	for _, ref := range refs {
		bundle, name, ok := strings.Cut(ref, ":")
		if !ok {
			bundle = "dependencies"
			name = ref
		}
		items = append(items, selectItem{
			ref:      ref,
			label:    name,
			detail:   ref,
			group:    bundle,
			selected: selectedSet[ref],
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].group != items[j].group {
			return items[i].group < items[j].group
		}
		return items[i].ref < items[j].ref
	})
	return withGroupHeadings(items)
}

func withTierHeadings(items []selectItem) []selectItem {
	result := make([]selectItem, 0, len(items)+8)
	lastGroup := ""
	lastTier := -1
	for _, item := range items {
		if item.group != lastGroup || item.tier != lastTier {
			result = append(result, selectItem{
				label:  fmt.Sprintf("%s / tier %d", item.group, item.tier),
				group:  item.group,
				tier:   item.tier,
				header: true,
				muted:  item.group != "env candidates",
			})
			lastGroup = item.group
			lastTier = item.tier
		}
		result = append(result, item)
	}
	return result
}

func withGroupHeadings(items []selectItem) []selectItem {
	result := make([]selectItem, 0, len(items)+8)
	lastGroup := ""
	for _, item := range items {
		if item.group != lastGroup {
			result = append(result, selectItem{label: item.group, group: item.group, header: true})
			lastGroup = item.group
		}
		result = append(result, item)
	}
	return result
}

func targetTier(graph *dag.Graph, ref string) int {
	if graph == nil {
		return 0
	}
	seen := map[string]bool{}
	var visit func(id string) int
	visit = func(id string) int {
		if seen[id] {
			return 0
		}
		seen[id] = true
		node, ok := graph.Nodes[id]
		if !ok {
			return 0
		}
		depth := 0
		for _, dep := range node.Deps {
			depth = max(depth, visit(dep.ID)+1)
		}
		seen[id] = false
		return depth
	}
	return visit(ref)
}

func (m *selectorModel) firstSelectable() int {
	for i, item := range m.items {
		if !item.header {
			return i
		}
	}
	return 0
}

func (m *selectorModel) move(delta int) {
	if len(m.items) == 0 {
		return
	}
	next := m.cursor
	for {
		next += delta
		if next < 0 || next >= len(m.items) {
			return
		}
		if !m.items[next].header {
			m.cursor = next
			return
		}
	}
}

func (m *selectorModel) toggle() {
	if m.cursor < 0 || m.cursor >= len(m.items) || m.items[m.cursor].header {
		return
	}
	m.items[m.cursor].selected = !m.items[m.cursor].selected
}

func (m *selectorModel) selectedRefs() []string {
	refs := make([]string, 0)
	for _, item := range m.items {
		if item.selected && !item.header {
			refs = append(refs, item.ref)
		}
	}
	sort.Strings(refs)
	return refs
}

func (m *selectorModel) ensureVisible(height int) {
	if height <= 0 {
		m.offset = 0
		return
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+height {
		m.offset = m.cursor - height + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func renderSelectItem(item selectItem, active bool, width int) string {
	if item.header {
		line := "  " + strings.ToUpper(item.label)
		if item.muted {
			return colorPad(ansiDim, line, width)
		}
		return colorPad(ansiYellow, line, width)
	}
	check := "[ ]"
	color := ""
	if item.selected {
		check = ansiGreen + "[x]" + ansiReset
	}
	if item.defaults {
		color = ansiGreen
	} else if item.muted {
		color = ansiDim
	}
	line := fmt.Sprintf("  %s %s", check, item.label)
	if item.detail != "" {
		line += "  " + ansiDim + item.detail + ansiReset
	}
	if active {
		return colorPad(ansiInvert, line, width)
	}
	return colorPad(color, line, width)
}

type selectorKey int

const (
	selectorKeyNone selectorKey = iota
	selectorKeyUp
	selectorKeyDown
	selectorKeyToggle
	selectorKeyEnter
	selectorKeyCancel
)

func readSelectorKey(reader *bufio.Reader) (selectorKey, error) {
	b, err := reader.ReadByte()
	if err != nil {
		return selectorKeyNone, err
	}
	switch b {
	case '\r', '\n':
		return selectorKeyEnter, nil
	case ' ':
		return selectorKeyToggle, nil
	case 'k':
		return selectorKeyUp, nil
	case 'j':
		return selectorKeyDown, nil
	case 0x03, 0x1b:
		if b == 0x1b {
			if reader.Buffered() < 2 {
				return selectorKeyCancel, nil
			}
			next, err := reader.Peek(2)
			if err == nil && next[0] == '[' {
				_, _ = reader.Discard(2)
				switch next[1] {
				case 'A':
					return selectorKeyUp, nil
				case 'B':
					return selectorKeyDown, nil
				}
			}
		}
		return selectorKeyCancel, nil
	default:
		return selectorKeyNone, nil
	}
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = true
		}
	}
	return set
}

func colorPad(color string, value string, width int) string {
	line := truncateANSI(value, width)
	line = line + strings.Repeat(" ", max(0, width-visibleLen(line)))
	if color == "" {
		return line
	}
	return color + line + ansiReset
}

func pad(value string, width int) string {
	line := truncateANSI(value, width)
	return line + strings.Repeat(" ", max(0, width-visibleLen(line)))
}

func truncateANSI(value string, width int) string {
	if visibleLen(value) <= width {
		return value
	}
	var b strings.Builder
	visible := 0
	inEscape := false
	for i := 0; i < len(value) && visible < width; i++ {
		ch := value[i]
		b.WriteByte(ch)
		if inEscape {
			if ch == 'm' {
				inEscape = false
			}
			continue
		}
		if ch == 0x1b {
			inEscape = true
			continue
		}
		visible++
	}
	b.WriteString(ansiReset)
	return b.String()
}

func visibleLen(value string) int {
	length := 0
	inEscape := false
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if inEscape {
			if ch == 'm' {
				inEscape = false
			}
			continue
		}
		if ch == 0x1b {
			inEscape = true
			continue
		}
		length++
	}
	return length
}
