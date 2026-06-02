package config

import "time"

type RepoConfig struct {
	Project      ProjectConfig           `koanf:"project"`
	Shell        string                  `koanf:"shell"`
	Env          map[string]string       `koanf:"env"`
	Deps         []Dependency            `koanf:"deps"`
	Dependencies []EnvironmentDependency `koanf:"dependencies"`
	Ignore       []string                `koanf:"ignore"`
	Logger       LoggerConfig            `koanf:"logger"`
}

type ProjectConfig struct {
	Name string `koanf:"name"`
}

type Dependency struct {
	Label      string `koanf:"label"`
	CheckCmd   string `koanf:"check_cmd"`
	InstallCmd string `koanf:"install_cmd"`
}

type EnvironmentDependency struct {
	Name    string            `koanf:"name"`
	Image   string            `koanf:"image"`
	Env     map[string]string `koanf:"env"`
	Ports   []string          `koanf:"ports"`
	Volumes []string          `koanf:"volumes"`
}

type LoggerConfig struct {
	DateTime LoggerDateTimeConfig `koanf:"datetime"`
}

type LoggerDateTimeConfig struct {
	Format string `koanf:"format"`
}

func (r *RepoConfig) SetDefaults() {
	if r.Shell == "" {
		r.Shell = "/bin/sh"
	}
	if r.Env == nil {
		r.Env = make(map[string]string)
	}
	if r.Dependencies == nil {
		r.Dependencies = []EnvironmentDependency{}
	}
	for i := range r.Dependencies {
		r.Dependencies[i].SetDefaults()
	}
	if r.Ignore == nil {
		r.Ignore = make([]string, 0)
	}
	if r.Logger.DateTime.Format == "" {
		r.Logger.DateTime.Format = time.RFC3339
	}
}

func (d *EnvironmentDependency) SetDefaults() {
	if d.Env == nil {
		d.Env = make(map[string]string)
	}
	if d.Ports == nil {
		d.Ports = []string{}
	}
	if d.Volumes == nil {
		d.Volumes = []string{}
	}
}
