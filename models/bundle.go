package models

type Bundle struct {
	Name         string
	Path         string
	Env          map[string]string
	Targets      []*Target
	Dependencies []EnvironmentDependency
}

func (b *Bundle) Target(name string) (*Target, bool) {
	for _, t := range b.Targets {
		if t.Name == name {
			return t, true
		}
	}
	return nil, false
}
