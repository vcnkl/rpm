package config

type BundleConfig struct {
	Name    string          `koanf:"name"`
	Env     BundleEnvConfig `koanf:"env"`
	Targets []TargetConfig  `koanf:"targets"`
}

type BundleEnvConfig struct {
	Variables map[string]string `koanf:"variables"`
	Deps      []string          `koanf:"deps"`
}

func (b *BundleConfig) SetDefaults() {
	if b.Env.Variables == nil {
		b.Env.Variables = make(map[string]string)
	}
	if b.Env.Deps == nil {
		b.Env.Deps = []string{}
	}
	for i := range b.Targets {
		b.Targets[i].SetDefaults()
	}
}
