package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vcnkl/rpm/models"
)

type Config struct {
	repoRoot   string
	rpmDir     string
	buildsPath string
	dagPath    string
	repo       *RepoConfig
	bundles    map[string]*models.Bundle
}

func NewConfig() *Config {
	repoRoot := findRepoRoot()
	repo := loadRepoConfig(filepath.Join(repoRoot, "repo.yml"))
	bundles := discoverBundles(repoRoot, repo.Ignore)

	bundleMap := make(map[string]*models.Bundle, len(bundles))
	for _, b := range bundles {
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

func (c *Config) initBuildsPath() string {
	buildsPath := filepath.Join(c.rpmDir, "builds.json")
	if _, err := os.Stat(buildsPath); os.IsNotExist(err) {
		if err = os.WriteFile(buildsPath, []byte("{}"), 0644); err != nil {
			panic(fmt.Sprintf("failed to create builds.json: %v", err))
		}
	}
	return buildsPath
}

func (c *Config) initDagPath() string {
	return filepath.Join(c.rpmDir, "dag.json")
}

func (c *Config) RepoRoot() string {
	return c.repoRoot
}

func (c *Config) BuildsPath() string {
	return c.buildsPath
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

func (c *Config) AllTargets() []*models.Target {
	var targets []*models.Target
	for _, bundle := range c.bundles {
		targets = append(targets, bundle.Targets...)
	}
	return targets
}

func (c *Config) TargetsByType(suffix string) []*models.Target {
	var targets []*models.Target
	for _, bundle := range c.bundles {
		targets = append(targets, bundle.TargetsByType(suffix)...)
	}
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
