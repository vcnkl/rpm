package models

type EnvironmentBlueprint struct {
	Version          int
	Name             string
	Variables        map[string]string
	Before           []string
	Targets          []EnvironmentTarget
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
	Env     map[string]string
	Ports   []string
	Volumes []string
}

type DependencyPolicy struct {
	Enabled bool
	Include []string
	Exclude []string
}

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
