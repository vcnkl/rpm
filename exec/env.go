package exec

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/vcnkl/rpm/config"
	"github.com/vcnkl/rpm/models"
	"github.com/vcnkl/rpm/pathsafe"
)

func ComposeEnv(repoRoot string, repo *config.RepoConfig, bundle *models.Bundle, target *models.Target) []string {
	overrides := make([]string, 0)

	for k, v := range repo.Env.Vars {
		overrides = append(overrides, k+"="+v)
	}

	overrides = append(overrides, "REPO_ROOT="+repoRoot)

	bundleRoot := filepath.Join(repoRoot, bundle.Path)
	overrides = append(overrides, "BUNDLE_ROOT="+bundleRoot)

	for k, v := range bundle.Env {
		overrides = append(overrides, k+"="+v)
	}

	for k, v := range target.Env {
		overrides = append(overrides, k+"="+v)
	}

	if target.Config.Dotenv.Enabled {
		dotenvPath := filepath.Join(bundleRoot, ".env")
		if dotenvVars, err := LoadDotenv(dotenvPath); err == nil {
			for k, v := range dotenvVars {
				overrides = append(overrides, k+"="+v)
			}
		}

		for _, file := range target.Config.Dotenv.Files {
			pattern, err := pathsafe.Resolve(bundleRoot, file)
			if err != nil {
				continue
			}
			matches, err := filepath.Glob(pattern)
			if err != nil || len(matches) == 0 {
				matches = []string{pattern}
			}
			for _, filePath := range matches {
				if fileVars, err := LoadDotenv(filePath); err == nil {
					for k, v := range fileVars {
						overrides = append(overrides, k+"="+v)
					}
				}
			}
		}
	}

	return MergeEnv(os.Environ(), overrides)
}

func LoadDotenv(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	result := make(map[string]string)
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		idx := strings.Index(line, "=")
		if idx == -1 {
			continue
		}

		key := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])

		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}

		result[key] = value
	}

	return result, scanner.Err()
}

func MergeEnv(base, override []string) []string {
	envMap := make(map[string]string)

	for _, e := range base {
		idx := strings.Index(e, "=")
		if idx != -1 {
			envMap[e[:idx]] = e[idx+1:]
		}
	}

	for _, e := range override {
		idx := strings.Index(e, "=")
		if idx != -1 {
			envMap[e[:idx]] = e[idx+1:]
		}
	}

	result := make([]string, 0, len(envMap))
	for k, v := range envMap {
		result = append(result, k+"="+v)
	}

	return result
}
