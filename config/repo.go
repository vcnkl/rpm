package config

type RepoConfig struct {
	Shell  string            `koanf:"shell"`
	Env    map[string]string `koanf:"env"`
	Docker DockerConfig      `koanf:"docker"`
	Deps   []Dependency      `koanf:"deps"`
	Ignore []string          `koanf:"ignore"`
}

type DockerConfig struct {
	Backend string `koanf:"backend"`
	URL     string `koanf:"url"`
}

type Dependency struct {
	Label      string `koanf:"label"`
	CheckCmd   string `koanf:"check_cmd"`
	InstallCmd string `koanf:"install_cmd"`
}

func (r *RepoConfig) SetDefaults() {
	if r.Shell == "" {
		r.Shell = "/bin/sh"
	}
	if r.Env == nil {
		r.Env = make(map[string]string)
	}
	if r.Docker.Backend == "" {
		r.Docker.Backend = "local"
	}
	if r.Ignore == nil {
		r.Ignore = make([]string, 0)
	}
}
