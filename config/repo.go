package config

import "time"

type RepoConfig struct {
	Project ProjectConfig `koanf:"project"`
	Shell   string        `koanf:"shell"`
	Env     RepoEnvConfig `koanf:"env"`
	Init    []InitStep    `koanf:"init"`
	Ignore  []string      `koanf:"ignore"`
	Logger  LoggerConfig  `koanf:"logger"`
	Logs    LogsConfig    `koanf:"logs"`
}

type ProjectConfig struct {
	Name string `koanf:"name"`
}

type RepoEnvConfig struct {
	Vars map[string]string       `koanf:"vars"`
	Deps []EnvironmentDependency `koanf:"deps"`
}

type InitStep struct {
	Label      string `koanf:"label"`
	CheckCmd   string `koanf:"check_cmd"`
	InstallCmd string `koanf:"install_cmd"`
}

type EnvironmentDependency struct {
	Name         string            `koanf:"name"`
	Image        string            `koanf:"image"`
	Env          map[string]string `koanf:"env"`
	Ports        []string          `koanf:"ports"`
	Volumes      []string          `koanf:"volumes"`
	ReadinessCmd string            `koanf:"readiness-cmd"`
}

type LoggerConfig struct {
	DateTime LoggerDateTimeConfig `koanf:"datetime"`
}

type LoggerDateTimeConfig struct {
	Format string `koanf:"format"`
}

type LogsConfig struct {
	Enabled bool            `koanf:"enabled"`
	Build   LogOutputConfig `koanf:"build"`
	Test    LogOutputConfig `koanf:"test"`
	Dev     LogOutputConfig `koanf:"dev"`
	Env     LogOutputConfig `koanf:"env"`
}

type LogOutputConfig struct {
	Out string `koanf:"out"`
}

func (r *RepoConfig) SetDefaults() {
	if r.Shell == "" {
		r.Shell = "/bin/sh"
	}
	if r.Env.Vars == nil {
		r.Env.Vars = make(map[string]string)
	}
	if r.Env.Deps == nil {
		r.Env.Deps = []EnvironmentDependency{}
	}
	for i := range r.Env.Deps {
		r.Env.Deps[i].SetDefaults()
	}
	if r.Init == nil {
		r.Init = []InitStep{}
	}
	if r.Ignore == nil {
		r.Ignore = make([]string, 0)
	}
	if r.Logger.DateTime.Format == "" {
		r.Logger.DateTime.Format = time.RFC3339
	}
	if r.Logs.Build.Out == "" {
		r.Logs.Build.Out = "log/rpm/build"
	}
	if r.Logs.Test.Out == "" {
		r.Logs.Test.Out = "log/rpm/test"
	}
	if r.Logs.Dev.Out == "" {
		r.Logs.Dev.Out = "log/rpm/dev"
	}
	if r.Logs.Env.Out == "" {
		r.Logs.Env.Out = "log/rpm/env"
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
