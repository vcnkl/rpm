package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vcnkl/rpm/models"
)

type Config struct {
	repoRoot   string
	rpmDir     string
	cacheDir   string
	buildsPath string
	dagPath    string
	repo       *RepoConfig
	bundles    map[string]*models.Bundle
}

func NewConfig() *Config {
	repoRoot := findRepoRoot()
	return newConfig(repoRoot, filepath.Join(repoRoot, "repo.yml"))
}

func NewConfigWithRepoFile(path string) *Config {
	absPath, err := filepath.Abs(path)
	if err != nil {
		panic(fmt.Sprintf("failed to resolve repo config path %s: %v", path, err))
	}
	return newConfig(filepath.Dir(absPath), absPath)
}

func newConfig(repoRoot string, repoFile string) *Config {
	repo := loadRepoConfig(repoFile)
	bundles := discoverBundles(repoRoot, repo.Ignore, repoDependencyRefs(repo))

	bundleMap := make(map[string]*models.Bundle, len(bundles))
	for _, b := range bundles {
		if _, ok := bundleMap[b.Name]; ok {
			panic(fmt.Sprintf("duplicate bundle name %q", b.Name))
		}
		bundleMap[b.Name] = b
	}

	cfg := &Config{
		repoRoot: repoRoot,
		repo:     repo,
		bundles:  bundleMap,
	}

	cfg.initPaths()

	return cfg
}

func (c *Config) initPaths() {
	c.rpmDir = c.initRpmDir()
	c.cacheDir = c.initCacheDir()
	c.buildsPath = c.initBuildsPath()
	c.dagPath = c.initDagPath()
}

func (c *Config) initRpmDir() string {
	rpmDir := filepath.Join(c.repoRoot, ".rpm")
	if err := os.MkdirAll(rpmDir, 0755); err != nil {
		panic(fmt.Sprintf("failed to create .rpm directory: %v", err))
	}
	return rpmDir
}

func (c *Config) initCacheDir() string {
	cacheDir := filepath.Join(c.rpmDir, "cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		panic(fmt.Sprintf("failed to create .rpm/cache directory: %v", err))
	}
	return cacheDir
}

func (c *Config) initBuildsPath() string {
	buildsPath := filepath.Join(c.cacheDir, "builds.json")
	if _, err := os.Stat(buildsPath); os.IsNotExist(err) {
		if err = os.WriteFile(buildsPath, []byte("{}"), 0644); err != nil {
			panic(fmt.Sprintf("failed to create builds.json: %v", err))
		}
	}
	return buildsPath
}

func (c *Config) initDagPath() string {
	return filepath.Join(c.cacheDir, "dag.json")
}

func (c *Config) RepoRoot() string {
	return c.repoRoot
}

func (c *Config) BuildsPath() string {
	return c.buildsPath
}

func (c *Config) CacheDir() string {
	return c.cacheDir
}

func (c *Config) DagPath() string {
	return c.dagPath
}

func (c *Config) Repo() *RepoConfig {
	return c.repo
}

func (c *Config) Bundles() map[string]*models.Bundle {
	return c.bundles
}

func (c *Config) EnvironmentDependencies() map[string]models.EnvironmentDependency {
	deps := make(map[string]models.EnvironmentDependency, len(c.repo.Dependencies))
	for _, dep := range c.repo.Dependencies {
		deps[dep.Name] = models.EnvironmentDependency{
			Name:    dep.Name,
			Image:   dep.Image,
			Env:     dep.Env,
			Ports:   dep.Ports,
			Volumes: dep.Volumes,
		}
	}
	return deps
}

func (c *Config) AllTargets() []*models.Target {
	return c.QueryTargets(nil)
}

func (c *Config) QueryTargets(query func(*models.Target) bool) []*models.Target {
	var targets []*models.Target
	for _, bundle := range c.bundles {
		for _, target := range bundle.Targets {
			if query == nil || query(target) {
				targets = append(targets, target)
			}
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].ID() < targets[j].ID()
	})
	return targets
}

func (c *Config) ResolveTarget(ref string) (*models.Target, error) {
	parts := strings.Split(ref, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid target reference: %s (expected bundle:target)", ref)
	}

	bundleName := parts[0]
	targetName := parts[1]

	bundle, ok := c.bundles[bundleName]
	if !ok {
		return nil, fmt.Errorf("bundle not found: %s", bundleName)
	}

	target, ok := bundle.Target(targetName)
	if !ok {
		return nil, fmt.Errorf("target not found: %s:%s", bundleName, targetName)
	}

	return target, nil
}
