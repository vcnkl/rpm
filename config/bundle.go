package config

type BundleConfig struct {
	Name    string            `koanf:"name"`
	Env     map[string]string `koanf:"env"`
	Targets []TargetConfig    `koanf:"targets"`
}

func (b *BundleConfig) SetDefaults() {
	if b.Env == nil {
		b.Env = make(map[string]string)
	}
	for i := range b.Targets {
		b.Targets[i].SetDefaults()
	}
}
