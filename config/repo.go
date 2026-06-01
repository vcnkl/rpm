package config

import "time"

type RepoConfig struct {
	Shell  string            `koanf:"shell"`
	Env    map[string]string `koanf:"env"`
	Deps   []Dependency      `koanf:"deps"`
	Ignore []string          `koanf:"ignore"`
	Logger LoggerConfig      `koanf:"logger"`
}

type Dependency struct {
	Label      string `koanf:"label"`
	CheckCmd   string `koanf:"check_cmd"`
	InstallCmd string `koanf:"install_cmd"`
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
	if r.Ignore == nil {
		r.Ignore = make([]string, 0)
	}
	if r.Logger.DateTime.Format == "" {
		r.Logger.DateTime.Format = time.RFC3339
	}
}
