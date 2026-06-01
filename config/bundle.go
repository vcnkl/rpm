package config

import "github.com/vcnkl/rpm/models"

type BundleConfig struct {
	Name         string             `koanf:"name"`
	Env          map[string]string  `koanf:"env"`
	Targets      []TargetConfig     `koanf:"targets"`
	Dependencies []DependencyConfig `koanf:"dependencies"`
}

type DependencyConfig struct {
	Name    string                        `koanf:"name"`
	Image   string                        `koanf:"image"`
	Mode    models.DependencyInstanceMode `koanf:"mode"`
	Env     map[string]string             `koanf:"env"`
	Ports   []string                      `koanf:"ports"`
	Volumes []string                      `koanf:"volumes"`
}

func (b *BundleConfig) SetDefaults() {
	if b.Env == nil {
		b.Env = make(map[string]string)
	}
	if b.Dependencies == nil {
		b.Dependencies = []DependencyConfig{}
	}
	for i := range b.Targets {
		b.Targets[i].SetDefaults()
	}
	for i := range b.Dependencies {
		b.Dependencies[i].SetDefaults()
	}
}

func (d *DependencyConfig) SetDefaults() {
	d.Mode = models.DefaultDependencyInstanceMode(d.Mode)
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
