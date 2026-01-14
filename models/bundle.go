package models

type Bundle struct {
	Name    string
	Path    string
	Env     map[string]string
	Targets []*Target
}

func (b *Bundle) Target(name string) (*Target, bool) {
	for _, t := range b.Targets {
		if t.Name == name {
			return t, true
		}
	}
	return nil, false
}

func (b *Bundle) TargetsByType(suffix string) []*Target {
	var result []*Target
	for _, t := range b.Targets {
		if hasSuffix(t.Name, suffix) {
			result = append(result, t)
		}
	}
	return result
}

func hasSuffix(s, suffix string) bool {
	if len(s) < len(suffix) {
		return false
	}
	return s[len(s)-len(suffix):] == suffix
}
