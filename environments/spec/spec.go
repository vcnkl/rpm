package spec

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	rootconfig "github.com/vcnkl/rpm/config"
	rpmexec "github.com/vcnkl/rpm/exec"
	"github.com/vcnkl/rpm/models"
)

type ResolvedEnvironment struct {
	Name         string
	Variables    []EnvVar
	Bundles      []Bundle
	Targets      []Target
	Dependencies []Dependency
	RuntimeUnits []RuntimeUnit
	ReloadPolicy models.ReloadPolicy
}

type Bundle struct {
	Name string
	Path string
	Env  []EnvVar
}

type Target struct {
	Ref        string
	Command    string
	WorkingDir string
	Env        []EnvVar
	Reload     bool
	Dotenv     Dotenv
}

type Dependency struct {
	Ref     string
	Name    string
	Image   string
	Mode    models.DependencyInstanceMode
	Env     []EnvVar
	Ports   []string
	Volumes []string
}

type EnvVar struct {
	Name  string
	Value string
}

type Dotenv struct {
	Enabled bool
	Files   []string
}

type RuntimeUnit struct {
	Id   string
	Kind string
}

func Resolve(repo *rootconfig.Config, blueprint *models.EnvironmentBlueprint) (*ResolvedEnvironment, error) {
	resolved := &ResolvedEnvironment{
		Name:         blueprint.Name,
		Variables:    envVars(blueprint.Variables),
		ReloadPolicy: blueprint.ReloadPolicy,
	}

	bundleSeen := make(map[string]bool)
	for _, bpTarget := range blueprint.Targets {
		target, err := repo.ResolveTarget(bpTarget.Ref)
		if err != nil {
			return nil, err
		}
		bundle := repo.Bundles()[target.BundleName]
		if !bundleSeen[bundle.Name] {
			resolved.Bundles = append(resolved.Bundles, Bundle{
				Name: bundle.Name,
				Path: filepath.Join(repo.RepoRoot(), bundle.Path),
				Env:  envVars(bundle.Env),
			})
			bundleSeen[bundle.Name] = true
			for _, dep := range bundle.Dependencies {
				resolved.Dependencies = append(resolved.Dependencies, dependency(bundle.Name, dep))
			}
		}

		reload := blueprint.ReloadPolicy.Enabled
		if bpTarget.Reload != nil {
			reload = *bpTarget.Reload
		}
		resolved.Targets = append(resolved.Targets, Target{
			Ref:        target.ID(),
			Command:    target.Cmd,
			WorkingDir: ResolveWorkingDir(repo.RepoRoot(), target),
			Env:        ResolveTargetEnv(repo, bundle, target, blueprint, bpTarget),
			Reload:     reload,
			Dotenv: Dotenv{
				Enabled: target.Config.Dotenv.Enabled,
				Files:   ResolveDotenvFiles(repo.RepoRoot(), bundle, target),
			},
		})
	}

	sort.Slice(resolved.Bundles, func(i, j int) bool {
		return resolved.Bundles[i].Name < resolved.Bundles[j].Name
	})
	sort.Slice(resolved.Targets, func(i, j int) bool {
		return resolved.Targets[i].Ref < resolved.Targets[j].Ref
	})
	sort.Slice(resolved.Dependencies, func(i, j int) bool {
		return resolved.Dependencies[i].Ref < resolved.Dependencies[j].Ref
	})
	for _, target := range resolved.Targets {
		resolved.RuntimeUnits = append(resolved.RuntimeUnits, RuntimeUnit{Id: target.Ref, Kind: "target"})
	}
	for _, dep := range resolved.Dependencies {
		resolved.RuntimeUnits = append(resolved.RuntimeUnits, RuntimeUnit{Id: dep.Ref, Kind: "dependency"})
	}
	sort.Slice(resolved.RuntimeUnits, func(i, j int) bool {
		if resolved.RuntimeUnits[i].Kind == resolved.RuntimeUnits[j].Kind {
			return resolved.RuntimeUnits[i].Id < resolved.RuntimeUnits[j].Id
		}
		return resolved.RuntimeUnits[i].Kind < resolved.RuntimeUnits[j].Kind
	})

	return resolved, nil
}

func ResolveWorkingDir(repoRoot string, target *models.Target) string {
	return rpmexec.ResolveWorkDir(repoRoot, target)
}

func ResolveTargetEnv(repo *rootconfig.Config, bundle *models.Bundle, target *models.Target, blueprint *models.EnvironmentBlueprint, bpTarget models.EnvironmentTarget) []EnvVar {
	env := os.Environ()
	env = appendEnvMap(env, repo.Repo().Env)
	env = append(env, "REPO_ROOT="+repo.RepoRoot())
	env = append(env, "BUNDLE_ROOT="+filepath.Join(repo.RepoRoot(), bundle.Path))
	env = appendEnvMap(env, bundle.Env)
	env = appendEnvMap(env, target.Env)
	env = appendEnvMap(env, blueprint.Variables)
	env = appendEnvMap(env, bpTarget.Env)

	if target.Config.Dotenv.Enabled {
		for _, filePath := range ResolveDotenvFiles(repo.RepoRoot(), bundle, target) {
			fileVars, err := rpmexec.LoadDotenv(filePath)
			if err == nil {
				env = appendEnvMap(env, fileVars)
			}
		}
	}

	return mergeEnvVars(env)
}

func ResolveDotenvFiles(repoRoot string, bundle *models.Bundle, target *models.Target) []string {
	if !target.Config.Dotenv.Enabled {
		return []string{}
	}

	bundleRoot := filepath.Join(repoRoot, bundle.Path)
	files := []string{filepath.Join(bundleRoot, ".env")}
	for _, file := range target.Config.Dotenv.Files {
		pattern := filepath.Join(bundleRoot, file)
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) == 0 {
			matches = []string{pattern}
		}
		sort.Strings(matches)
		files = append(files, matches...)
	}
	return files
}

func dependency(bundleName string, dep models.EnvironmentDependency) Dependency {
	ports := append([]string{}, dep.Ports...)
	volumes := append([]string{}, dep.Volumes...)
	sort.Strings(ports)
	sort.Strings(volumes)
	return Dependency{
		Ref:     bundleName + ":" + dep.Name,
		Name:    dep.Name,
		Image:   dep.Image,
		Mode:    dep.Mode,
		Env:     envVars(dep.Env),
		Ports:   ports,
		Volumes: volumes,
	}
}

func appendEnvMap(env []string, values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+values[key])
	}
	return env
}

func mergeEnvVars(env []string) []EnvVar {
	values := make(map[string]string)
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	return envVars(values)
}

func envVars(values map[string]string) []EnvVar {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := make([]EnvVar, 0, len(keys))
	for _, key := range keys {
		result = append(result, EnvVar{Name: key, Value: values[key]})
	}
	return result
}
