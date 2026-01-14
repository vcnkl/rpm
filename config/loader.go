package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	var repo RepoConfig
	if err := k.Unmarshal("", &repo); err != nil {
		panic(fmt.Sprintf("failed to parse repo.yml: %v", err))
	}

	repo.SetDefaults()
	return &repo
}

func discoverBundles(repoRoot string, ignore []string) []*models.Bundle {
	var bundles []*models.Bundle

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
			bundle := loadBundleConfig(path, repoRoot)
			bundles = append(bundles, bundle)
		}

		return nil
	})

	if err != nil {
		panic(fmt.Sprintf("failed to discover bundles: %v", err))
	}

	return bundles
}

func loadBundleConfig(path string, repoRoot string) *models.Bundle {
	k := koanf.New(".")
	if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
		panic(fmt.Sprintf("failed to read rpm.yml at %s: %v", path, err))
	}

	var cfg BundleConfig
	if err := k.Unmarshal("", &cfg); err != nil {
		panic(fmt.Sprintf("failed to parse rpm.yml at %s: %v", path, err))
	}

	cfg.SetDefaults()

	bundlePath := filepath.Dir(path)
	relPath, err := filepath.Rel(repoRoot, bundlePath)
	if err != nil {
		panic(fmt.Sprintf("failed to get relative path for bundle: %v", err))
	}

	bundle := &models.Bundle{
		Name:    cfg.Name,
		Path:    relPath,
		Env:     cfg.Env,
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
				},
				Reload: *tc.Config.Reload,
				Ignore: tc.Config.Ignore,
			},
		}
		bundle.Targets = append(bundle.Targets, target)
	}

	return bundle
}
