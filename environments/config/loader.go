package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	rootconfig "github.com/vcnkl/rpm/config"
	"github.com/vcnkl/rpm/models"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
	"github.com/pkg/errors"
)

var (
	ErrUnknownBlueprint      = errors.New("unknown blueprint file")
	ErrUnknownBlueprintRef   = errors.New("unknown blueprint target ref")
	ErrDuplicateBlueprintRef = errors.New("duplicate blueprint target ref")
)

type BlueprintConfig struct {
	Version      int                `koanf:"version"`
	Name         string             `koanf:"name"`
	LiveReload   LiveReloadConfig   `koanf:"live_reload"`
	Targets      []TargetConfig     `koanf:"targets"`
	Dependencies DependenciesConfig `koanf:"dependencies"`
	Variables    map[string]string  `koanf:"variables"`
}

type LiveReloadConfig struct {
	Enabled  *bool  `koanf:"enabled"`
	Debounce string `koanf:"debounce"`
}

type TargetConfig struct {
	Ref    string            `koanf:"ref"`
	Reload *bool             `koanf:"reload"`
	Env    map[string]string `koanf:"env"`
}

type DependenciesConfig struct {
	Enabled bool     `koanf:"enabled"`
	Include []string `koanf:"include"`
	Exclude []string `koanf:"exclude"`
}

func LoadBlueprint(repo *rootconfig.Config, name string) (*models.EnvironmentBlueprint, error) {
	path := BlueprintPath(repo, name)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, errors.Wrapf(ErrUnknownBlueprint, "%s", path)
		}
		return nil, err
	}
	return LoadBlueprintFile(repo, path)
}

func LoadBlueprintFile(repo *rootconfig.Config, path string) (*models.EnvironmentBlueprint, error) {
	k := koanf.New(".")
	if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
		return nil, errors.Wrapf(err, "failed to read blueprint %s", path)
	}

	var cfg BlueprintConfig
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, errors.Wrapf(err, "failed to parse blueprint %s", path)
	}
	cfg.SetDefaults()
	if err := cfg.Validate(repo); err != nil {
		return nil, err
	}
	return cfg.Blueprint(), nil
}

func BlueprintPath(repo *rootconfig.Config, name string) string {
	if strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml") || strings.ContainsRune(name, filepath.Separator) {
		if filepath.IsAbs(name) {
			return name
		}
		return filepath.Join(repo.RepoRoot(), name)
	}
	return filepath.Join(repo.RepoRoot(), ".rpm", "envs", name+".yml")
}

func (c *BlueprintConfig) SetDefaults() {
	if c.Version == 0 {
		c.Version = 1
	}
	if c.LiveReload.Enabled == nil {
		enabled := true
		c.LiveReload.Enabled = &enabled
	}
	if c.LiveReload.Debounce == "" {
		c.LiveReload.Debounce = "100ms"
	}
	if c.Variables == nil {
		c.Variables = make(map[string]string)
	}
	for i := range c.Targets {
		if c.Targets[i].Env == nil {
			c.Targets[i].Env = make(map[string]string)
		}
	}
	if c.Dependencies.Include == nil {
		c.Dependencies.Include = []string{}
	}
	if c.Dependencies.Exclude == nil {
		c.Dependencies.Exclude = []string{}
	}
}

func (c *BlueprintConfig) Validate(repo *rootconfig.Config) error {
	seen := make(map[string]bool)
	for _, target := range c.Targets {
		if !validBlueprintTargetRef(target.Ref) {
			return fmt.Errorf("invalid blueprint target ref %q", target.Ref)
		}
		if seen[target.Ref] {
			return errors.Wrapf(ErrDuplicateBlueprintRef, "%s", target.Ref)
		}
		seen[target.Ref] = true
		if _, err := repo.ResolveTarget(target.Ref); err != nil {
			return errors.Wrapf(ErrUnknownBlueprintRef, "%s", target.Ref)
		}
	}
	return nil
}

func (c *BlueprintConfig) Blueprint() *models.EnvironmentBlueprint {
	targets := make([]models.EnvironmentTarget, 0, len(c.Targets))
	for _, target := range c.Targets {
		targets = append(targets, models.EnvironmentTarget{
			Ref:    target.Ref,
			Reload: target.Reload,
			Env:    target.Env,
		})
	}
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].Ref < targets[j].Ref
	})

	return &models.EnvironmentBlueprint{
		Version:   c.Version,
		Name:      c.Name,
		Variables: c.Variables,
		Targets:   targets,
		ReloadPolicy: models.ReloadPolicy{
			Enabled:  *c.LiveReload.Enabled,
			Debounce: c.LiveReload.Debounce,
		},
	}
}

func validBlueprintTargetRef(ref string) bool {
	parts := strings.Split(ref, ":")
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}
