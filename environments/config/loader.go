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
	yamlv3 "gopkg.in/yaml.v3"
)

var (
	ErrUnknownBlueprint      = errors.New("unknown blueprint file")
	ErrInvalidBlueprintName  = errors.New("invalid blueprint name")
	ErrUnknownBlueprintRef   = errors.New("unknown blueprint target ref")
	ErrDuplicateBlueprintRef = errors.New("duplicate blueprint target ref")
	ErrUnknownDependencyRef  = errors.New("unknown blueprint dependency ref")
	ErrUnsupportedPre        = errors.New("pre is no longer supported; use before with existing target refs")
)

type BlueprintConfig struct {
	Version      int                `koanf:"version"`
	Name         string             `koanf:"name"`
	LiveReload   LiveReloadConfig   `koanf:"live_reload"`
	Before       []string           `koanf:"before"`
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
	path, err := BlueprintPath(repo, name)
	if err != nil {
		return nil, err
	}
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
	if k.Exists("pre") {
		return nil, ErrUnsupportedPre
	}
	cfg.SetDefaults()
	if err := cfg.Validate(repo); err != nil {
		return nil, err
	}
	return cfg.Blueprint(), nil
}

func WriteBlueprint(repo *rootconfig.Config, blueprint *models.EnvironmentBlueprint) error {
	path, err := BlueprintPath(repo, blueprint.Name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return errors.Wrapf(err, "failed to create blueprint directory")
	}
	data, err := MarshalBlueprint(blueprint)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func MarshalBlueprint(blueprint *models.EnvironmentBlueprint) ([]byte, error) {
	sortBlueprint(blueprint)
	root := mappingNode(
		scalarNode("version"), intNode(blueprint.Version),
		scalarNode("name"), scalarNode(blueprint.Name),
		scalarNode("live_reload"), liveReloadNode(blueprint.ReloadPolicy),
		scalarNode("before"), stringSliceNode(blueprint.Before),
		scalarNode("targets"), targetsNode(blueprint.Targets),
		scalarNode("dependencies"), dependencyPolicyNode(blueprint.DependencyPolicy),
		scalarNode("variables"), stringMapNode(blueprint.Variables),
	)
	data, err := yamlv3.Marshal(root)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal blueprint")
	}
	return data, nil
}

func BlueprintPath(repo *rootconfig.Config, name string) (string, error) {
	if !ValidBlueprintName(name) {
		return "", errors.Wrapf(ErrInvalidBlueprintName, "%q", name)
	}
	return filepath.Join(repo.RepoRoot(), ".rpm", "envs", name+".yml"), nil
}

func ValidBlueprintName(name string) bool {
	return name != "" &&
		name == filepath.Base(name) &&
		name != "." &&
		name != ".." &&
		!strings.ContainsAny(name, `/\`)
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
	if c.Before == nil {
		c.Before = []string{}
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
	beforeSeen := make(map[string]bool)
	for _, ref := range c.Before {
		if !validBlueprintTargetRef(ref) {
			return fmt.Errorf("invalid blueprint before target ref %q", ref)
		}
		if seen[ref] {
			return errors.Wrapf(ErrDuplicateBlueprintRef, "%s is also listed in targets", ref)
		}
		if beforeSeen[ref] {
			return errors.Wrapf(ErrDuplicateBlueprintRef, "%s", ref)
		}
		beforeSeen[ref] = true
		if _, err := repo.ResolveTarget(ref); err != nil {
			return errors.Wrapf(ErrUnknownBlueprintRef, "%s", ref)
		}
	}
	dependencies := dependencyRefs(repo)
	for _, ref := range append(append([]string{}, c.Dependencies.Include...), c.Dependencies.Exclude...) {
		if !validBlueprintTargetRef(ref) {
			return fmt.Errorf("invalid blueprint dependency ref %q", ref)
		}
		if !dependencies[ref] {
			return errors.Wrapf(ErrUnknownDependencyRef, "%s", ref)
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
		Before:    append([]string{}, c.Before...),
		Targets:   targets,
		DependencyPolicy: models.DependencyPolicy{
			Enabled: c.Dependencies.Enabled,
			Include: sortedStrings(c.Dependencies.Include),
			Exclude: sortedStrings(c.Dependencies.Exclude),
		},
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

func sortBlueprint(blueprint *models.EnvironmentBlueprint) {
	if blueprint.Version == 0 {
		blueprint.Version = 1
	}
	if blueprint.ReloadPolicy.Debounce == "" {
		blueprint.ReloadPolicy.Debounce = "100ms"
	}
	if blueprint.Variables == nil {
		blueprint.Variables = make(map[string]string)
	}
	if blueprint.Before == nil {
		blueprint.Before = []string{}
	}
	blueprint.Before = sortedStrings(blueprint.Before)
	sort.Slice(blueprint.Targets, func(i, j int) bool {
		return blueprint.Targets[i].Ref < blueprint.Targets[j].Ref
	})
	blueprint.DependencyPolicy.Include = sortedStrings(blueprint.DependencyPolicy.Include)
	blueprint.DependencyPolicy.Exclude = sortedStrings(blueprint.DependencyPolicy.Exclude)
}

func dependencyRefs(repo *rootconfig.Config) map[string]bool {
	refs := make(map[string]bool)
	for _, bundle := range repo.Bundles() {
		for _, dep := range bundle.Dependencies {
			refs[bundle.Name+":"+dep.Name] = true
		}
	}
	return refs
}

func sortedStrings(values []string) []string {
	result := append([]string{}, values...)
	sort.Strings(result)
	return result
}

func liveReloadNode(policy models.ReloadPolicy) *yamlv3.Node {
	return mappingNode(
		scalarNode("enabled"), boolNode(policy.Enabled),
		scalarNode("debounce"), scalarNode(policy.Debounce),
	)
}

func targetsNode(targets []models.EnvironmentTarget) *yamlv3.Node {
	node := &yamlv3.Node{Kind: yamlv3.SequenceNode}
	for _, target := range targets {
		fields := []*yamlv3.Node{
			scalarNode("ref"), scalarNode(target.Ref),
		}
		if target.Reload != nil {
			fields = append(fields, scalarNode("reload"), boolNode(*target.Reload))
		}
		if len(target.Env) > 0 {
			fields = append(fields, scalarNode("env"), stringMapNode(target.Env))
		}
		node.Content = append(node.Content, mappingNode(fields...))
	}
	return node
}

func dependencyPolicyNode(policy models.DependencyPolicy) *yamlv3.Node {
	return mappingNode(
		scalarNode("enabled"), boolNode(policy.Enabled),
		scalarNode("include"), stringSliceNode(policy.Include),
		scalarNode("exclude"), stringSliceNode(policy.Exclude),
	)
}

func stringMapNode(values map[string]string) *yamlv3.Node {
	node := &yamlv3.Node{Kind: yamlv3.MappingNode}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		node.Content = append(node.Content, scalarNode(key), scalarNode(values[key]))
	}
	return node
}

func stringSliceNode(values []string) *yamlv3.Node {
	node := &yamlv3.Node{Kind: yamlv3.SequenceNode}
	for _, value := range values {
		node.Content = append(node.Content, scalarNode(value))
	}
	return node
}

func mappingNode(nodes ...*yamlv3.Node) *yamlv3.Node {
	return &yamlv3.Node{Kind: yamlv3.MappingNode, Content: nodes}
}

func scalarNode(value string) *yamlv3.Node {
	return &yamlv3.Node{Kind: yamlv3.ScalarNode, Tag: "!!str", Value: value}
}

func boolNode(value bool) *yamlv3.Node {
	if value {
		return &yamlv3.Node{Kind: yamlv3.ScalarNode, Tag: "!!bool", Value: "true"}
	}
	return &yamlv3.Node{Kind: yamlv3.ScalarNode, Tag: "!!bool", Value: "false"}
}

func intNode(value int) *yamlv3.Node {
	return &yamlv3.Node{Kind: yamlv3.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", value)}
}
