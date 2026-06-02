package models

const (
	DependencyInstanceModeShared    DependencyInstanceMode = "shared"
	DependencyInstanceModeDedicated DependencyInstanceMode = "dedicated"
)

type EnvironmentBlueprint struct {
	Version          int
	Name             string
	Variables        map[string]string
	Before           []string
	Targets          []EnvironmentTarget
	Dependencies     []EnvironmentDependency
	DependencyPolicy DependencyPolicy
	ReloadPolicy     ReloadPolicy
}

type EnvironmentTarget struct {
	Ref    string
	Reload *bool
	Env    map[string]string
}

type EnvironmentDependency struct {
	Name    string
	Image   string
	Mode    DependencyInstanceMode
	Env     map[string]string
	Ports   []string
	Volumes []string
}

type DependencyPolicy struct {
	Enabled bool
	Include []string
	Exclude []string
}

type DependencyInstanceMode string

type ReloadPolicy struct {
	Enabled  bool
	Debounce string
}

type ResolvedEnvironment struct {
	Name         string
	Variables    map[string]string
	Targets      []EnvironmentTarget
	Dependencies []EnvironmentDependency
	ReloadPolicy ReloadPolicy
}

func DefaultDependencyInstanceMode(mode DependencyInstanceMode) DependencyInstanceMode {
	if mode == "" {
		return DependencyInstanceModeShared
	}
	return mode
}

func (m DependencyInstanceMode) Valid() bool {
	switch m {
	case DependencyInstanceModeShared, DependencyInstanceModeDedicated:
		return true
	default:
		return false
	}
}
