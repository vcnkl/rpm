package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vcnkl/rpm/git"
	"github.com/vcnkl/rpm/models"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

func findRepoRoot() string {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			panic(fmt.Sprintf("failed to find git repo root and current directory: %v, %v", err, cwdErr))
		}
		return cwd
	}
	return strings.TrimSpace(string(output))
}

func loadRepoConfig(path string) *RepoConfig {
	k := koanf.New(".")
	if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
		panic(fmt.Sprintf("failed to read repo.yml at %s: %v", path, err))
	}
	validateRepoConfigSchema(k, path)

	var repo RepoConfig
	if err := k.Unmarshal("", &repo); err != nil {
		panic(fmt.Sprintf("failed to parse repo.yml: %v", err))
	}

	repo.SetDefaults()
	if err := validateRepoConfig(&repo, path); err != nil {
		panic(fmt.Sprintf("invalid repo.yml at %s: %v", path, err))
	}
	return &repo
}

func discoverBundles(repoRoot string, ignore []string, repoDeps map[string]bool) []*models.Bundle {
	var paths []string

	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(repoRoot, path)
		if err != nil {
			panic(err)
		}
		for _, pattern := range ignore {
			if skip, _ := filepath.Match(pattern, relPath); skip {
				return nil
			}
		}

		if info.IsDir() {
			tracked, err := git.IsTracked(path)
			if err != nil {
				return err
			}
			if !tracked {
				return filepath.SkipDir
			}
			return nil
		}

		if info.Name() == "rpm.yml" {
			paths = append(paths, path)
		}

		return nil
	})

	if err != nil {
		panic(fmt.Sprintf("failed to discover bundles: %v", err))
	}

	sort.Slice(paths, func(i, j int) bool {
		left, leftErr := filepath.Rel(repoRoot, paths[i])
		right, rightErr := filepath.Rel(repoRoot, paths[j])
		if leftErr != nil || rightErr != nil {
			return paths[i] < paths[j]
		}
		return filepath.ToSlash(left) < filepath.ToSlash(right)
	})

	bundles := make([]*models.Bundle, 0, len(paths))
	for _, path := range paths {
		bundles = append(bundles, loadBundleConfig(path, repoRoot, repoDeps))
	}
	return bundles
}

func loadBundleConfig(path string, repoRoot string, repoDeps map[string]bool) *models.Bundle {
	k := koanf.New(".")
	if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
		panic(fmt.Sprintf("failed to read rpm.yml at %s: %v", path, err))
	}
	if k.Exists("env.dependencies") {
		panic(fmt.Sprintf("invalid rpm.yml at %s: env.dependencies is not supported; use env.deps with repo.yml env.deps", path))
	}

	var cfg BundleConfig
	if err := k.Unmarshal("", &cfg); err != nil {
		panic(fmt.Sprintf("failed to parse rpm.yml at %s: %v", path, err))
	}

	cfg.SetDefaults()
	if err := validateBundleConfig(&cfg, path, repoDeps); err != nil {
		panic(fmt.Sprintf("invalid rpm.yml at %s: %v", path, err))
	}

	bundlePath := filepath.Dir(path)
	relPath, err := filepath.Rel(repoRoot, bundlePath)
	if err != nil {
		panic(fmt.Sprintf("failed to get relative path for bundle: %v", err))
	}

	bundle := &models.Bundle{
		Name:    cfg.Name,
		Path:    relPath,
		Env:     cfg.Env.Variables,
		Targets: make([]*models.Target, 0, len(cfg.Targets)),
	}

	for _, tc := range cfg.Targets {
		target := &models.Target{
			Name:       tc.Name,
			BundleName: cfg.Name,
			BundlePath: relPath,
			In:         tc.In,
			Out:        tc.Out,
			Deps:       tc.Deps,
			Env:        tc.Env,
			Cmd:        tc.GetCmd(),
			Config: models.TargetConfig{
				WorkingDir: tc.Config.WorkingDir,
				Dotenv: models.DotenvConfig{
					Enabled: *tc.Config.Dotenv.Enabled,
					Files:   tc.Config.Dotenv.Files,
				},
				Reload: *tc.Config.Reload,
				Ignore: tc.Config.Ignore,
				Index:  tc.Config.Index,
			},
		}
		bundle.Targets = append(bundle.Targets, target)
	}
	bundle.Dependencies = append(bundle.Dependencies, cfg.Env.Deps...)

	return bundle
}

func repoDependencyRefs(repo *RepoConfig) map[string]bool {
	refs := make(map[string]bool, len(repo.Env.Deps))
	for _, dep := range repo.Env.Deps {
		refs[dep.Name] = true
	}
	return refs
}

func validateRepoConfigSchema(k *koanf.Koanf, path string) {
	if k.Exists("deps") {
		panic(fmt.Sprintf("invalid repo.yml at %s: repo.yml deps is not supported; use init", path))
	}
	if k.Exists("dependencies") {
		panic(fmt.Sprintf("invalid repo.yml at %s: repo.yml dependencies is not supported; use env.deps", path))
	}
	for _, key := range k.Keys() {
		if !strings.HasPrefix(key, "env.") {
			continue
		}
		parts := strings.Split(key, ".")
		if len(parts) >= 2 && parts[1] != "vars" && parts[1] != "deps" {
			panic(fmt.Sprintf("invalid repo.yml at %s: repo.yml env variables must be declared under env.vars", path))
		}
	}
}
