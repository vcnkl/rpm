package config

import (
	"strings"
)

type TargetConfig struct {
	Name   string            `koanf:"name"`
	In     []string          `koanf:"in"`
	Out    []string          `koanf:"out"`
	Deps   []string          `koanf:"deps"`
	Env    map[string]string `koanf:"env"`
	Cmd    interface{}       `koanf:"cmd"`
	Config TargetOptions     `koanf:"config"`
}

type TargetOptions struct {
	WorkingDir string       `koanf:"working_dir"`
	Dotenv     DotenvConfig `koanf:"dotenv"`
	Reload     *bool        `koanf:"reload"`
	Ignore     []string     `koanf:"ignore"`
}

type DotenvConfig struct {
	Enabled *bool `koanf:"enabled"`
}

func (t *TargetConfig) GetCmd() string {
	switch v := t.Cmd.(type) {
	case string:
		return v
	case []interface{}:
		var cmds []string
		for _, c := range v {
			if s, ok := c.(string); ok {
				cmds = append(cmds, s)
			}
		}
		return strings.Join(cmds, "\n")
	case []string:
		return strings.Join(v, "\n")
	}
	return ""
}

func (t *TargetConfig) SetDefaults() {
	if t.Env == nil {
		t.Env = make(map[string]string)
	}
	if t.In == nil {
		t.In = []string{}
	}
	if t.Out == nil {
		t.Out = []string{}
	}
	if t.Deps == nil {
		t.Deps = []string{}
	}
	if t.Config.WorkingDir == "" {
		t.Config.WorkingDir = "local"
	}
	if t.Config.Dotenv.Enabled == nil {
		enabled := true
		t.Config.Dotenv.Enabled = &enabled
	}
	if t.Config.Reload == nil {
		reload := true
		t.Config.Reload = &reload
	}
	if t.Config.Ignore == nil {
		t.Config.Ignore = []string{}
	}
}
