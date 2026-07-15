package envtui

type keyHint struct {
	keys  string
	label string
}

var dashboardHints = []keyHint{
	{"↑/↓", "move"},
	{"r", "restart"},
	{"R", "restart all"},
	{"d", "deps"},
	{"f", "focus"},
	{"/", "filter"},
	{"W", "wrap"},
	{"S", "auto-scroll"},
	{"?", "keys"},
	{"q", "quit"},
}

var pickerHints = []keyHint{
	{"↑/↓", "move"},
	{"space", "toggle"},
	{"→/←", "expand"},
	{"enter", "confirm"},
	{"esc", "cancel"},
}

func matchKey(key string, candidates ...string) bool {
	for _, candidate := range candidates {
		if key == candidate {
			return true
		}
	}
	return false
}
