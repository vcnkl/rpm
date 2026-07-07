package spec

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pkg/errors"
	rootconfig "github.com/vcnkl/rpm/config"
	rpmexec "github.com/vcnkl/rpm/exec"
	"github.com/vcnkl/rpm/models"
	"github.com/vcnkl/rpm/pathsafe"
)

type ResolvedEnvironment struct {
	Name          string
	Variables     []EnvVar
	Bundles       []Bundle
	BeforeTargets []BeforeTarget
	DepTargets    []BeforeTarget
	Targets       []Target
	Dependencies  []Dependency
	RuntimeUnits  []RuntimeUnit
	ReloadPolicy  models.ReloadPolicy
}

type Bundle struct {
	Name string
	Path string
	Env  []EnvVar
}

type Target struct {
	Ref         string
	ConfigPath  string
	Command     string
	WorkingDir  string
	Env         []EnvVar
	ExplicitEnv []EnvVar
	OverrideEnv []EnvVar
	DotenvEnv   []EnvVar
	Reload      bool
	Watch       Watch
	Dotenv      Dotenv
}

type BeforeTarget struct {
	Ref        string
	ConfigPath string
	Command    string
	WorkingDir string
	Env        []EnvVar
	DotenvEnv  []EnvVar
	Dotenv     Dotenv
}

type resolvedBeforeTarget struct {
	target BeforeTarget
	index  *int
}

type Dependency struct {
	Ref          string
	ConfigPath   string
	Name         string
	Image        string
	Env          []EnvVar
	Ports        []string
	Volumes      []string
	ReadinessCmd string
}

type EnvVar struct {
	Name  string
	Value string
}

type Dotenv struct {
	Enabled bool
	Files   []string
}

type Watch struct {
	Roots   []string
	Ignore  []string
	Reload  bool
	Enabled bool
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
	dependencySeen := make(map[string]bool)
	dependencies := repo.EnvironmentDependencies()
	excludedDependencies := make(map[string]bool)
	if blueprint.DependencyPolicy.Enabled {
		for _, ref := range blueprint.DependencyPolicy.Exclude {
			excludedDependencies[ref] = true
		}
	}
	addDependency := func(ref string, required bool) error {
		if dependencySeen[ref] {
			return nil
		}
		dep, ok := dependencies[ref]
		if !ok {
			if required {
				return errors.Errorf("unknown dependency ref %q", ref)
			}
			return nil
		}
		resolved.Dependencies = append(resolved.Dependencies, dependency(dep))
		dependencySeen[ref] = true
		return nil
	}
	addBundle := func(bundle *models.Bundle) {
		if bundleSeen[bundle.Name] {
			return
		}
		resolved.Bundles = append(resolved.Bundles, Bundle{
			Name: bundle.Name,
			Path: filepath.Join(repo.RepoRoot(), bundle.Path),
			Env:  envVars(bundle.Env),
		})
		bundleSeen[bundle.Name] = true
		if !blueprint.DependencyPolicy.Enabled {
			return
		}
		for _, ref := range bundle.Dependencies {
			if excludedDependencies[ref] {
				continue
			}
			_ = addDependency(ref, false)
		}
	}
	if blueprint.DependencyPolicy.Enabled {
		for _, ref := range blueprint.DependencyPolicy.Include {
			if err := addDependency(ref, true); err != nil {
				return nil, err
			}
		}
	}

	targetSeen := make(map[string]bool)
	for _, bpTarget := range blueprint.Targets {
		targetSeen[bpTarget.Ref] = true
	}
	beforeSeen := make(map[string]bool)
	beforeTargets := make([]resolvedBeforeTarget, 0, len(blueprint.Before))
	for _, ref := range blueprint.Before {
		if beforeSeen[ref] {
			return nil, errors.Errorf("duplicate before target ref %q", ref)
		}
		if targetSeen[ref] {
			return nil, errors.Errorf("before target %q is also listed in targets", ref)
		}
		beforeSeen[ref] = true
		targetConfig, err := repo.ResolveTarget(ref)
		if err != nil {
			return nil, errors.Wrapf(err, "unknown before target ref %q", ref)
		}
		target := resolveBeforeTarget(repo, blueprint, targetConfig)
		addBundle(repo.Bundles()[targetConfig.BundleName])
		beforeTargets = append(beforeTargets, resolvedBeforeTarget{target: target, index: targetConfig.Config.Index})
	}
	sort.Slice(beforeTargets, func(i, j int) bool {
		return compareBeforeTargets(beforeTargets[i], beforeTargets[j])
	})
	for _, before := range beforeTargets {
		resolved.BeforeTargets = append(resolved.BeforeTargets, before.target)
	}

	for _, bpTarget := range blueprint.Targets {
		target, err := repo.ResolveTarget(bpTarget.Ref)
		if err != nil {
			return nil, err
		}
		bundle := repo.Bundles()[target.BundleName]
		addBundle(bundle)
		env, dotenvEnv := resolveGeneratedTargetEnv(repo, bundle, target, blueprint, bpTarget)

		reload := false
		if blueprint.ReloadPolicy.Enabled {
			reload = target.Config.Reload
			if bpTarget.Reload != nil {
				reload = *bpTarget.Reload
			}
		}
		resolved.Targets = append(resolved.Targets, Target{
			Ref:         target.ID(),
			ConfigPath:  bundleConfigPath(bundle),
			Command:     target.Cmd,
			WorkingDir:  ResolveWorkingDir(repo.RepoRoot(), target),
			Env:         ResolveTargetEnv(repo, bundle, target, blueprint, bpTarget),
			ExplicitEnv: env,
			OverrideEnv: envVars(bpTarget.Env),
			DotenvEnv:   dotenvEnv,
			Reload:      reload,
			Watch: Watch{
				Roots:   ResolveWatchRoots(repo.RepoRoot(), bundle, target),
				Ignore:  sortedStrings(target.Config.Ignore),
				Reload:  reload,
				Enabled: reload,
			},
			Dotenv: Dotenv{
				Enabled: target.Config.Dotenv.Enabled,
				Files:   ResolveDotenvFiles(repo.RepoRoot(), bundle, target),
			},
		})
	}

	depTargets, err := resolveDepTargets(repo, blueprint, targetSeen, beforeSeen, addBundle)
	if err != nil {
		return nil, err
	}
	resolved.DepTargets = depTargets

	sort.Slice(resolved.Bundles, func(i, j int) bool {
		return resolved.Bundles[i].Name < resolved.Bundles[j].Name
	})
	sort.Slice(resolved.Targets, func(i, j int) bool {
		return resolved.Targets[i].Ref < resolved.Targets[j].Ref
	})
	sort.Slice(resolved.Dependencies, func(i, j int) bool {
		return resolved.Dependencies[i].Ref < resolved.Dependencies[j].Ref
	})
	for _, before := range resolved.BeforeTargets {
		resolved.RuntimeUnits = append(resolved.RuntimeUnits, RuntimeUnit{Id: before.Ref, Kind: "before"})
	}
	for _, dep := range resolved.Dependencies {
		resolved.RuntimeUnits = append(resolved.RuntimeUnits, RuntimeUnit{Id: dep.Ref, Kind: "dependency"})
	}
	for _, dep := range resolved.DepTargets {
		resolved.RuntimeUnits = append(resolved.RuntimeUnits, RuntimeUnit{Id: dep.Ref, Kind: "dep_target"})
	}
	for _, target := range resolved.Targets {
		resolved.RuntimeUnits = append(resolved.RuntimeUnits, RuntimeUnit{Id: target.Ref, Kind: "target"})
	}

	return resolved, nil
}

func ResolveWorkingDir(repoRoot string, target *models.Target) string {
	return rpmexec.ResolveWorkDir(repoRoot, target)
}

func ResolveBeforeTarget(repo *rootconfig.Config, blueprint *models.EnvironmentBlueprint, ref string) (BeforeTarget, error) {
	target, err := repo.ResolveTarget(ref)
	if err != nil {
		return BeforeTarget{}, errors.Wrapf(err, "unknown before target ref %q", ref)
	}
	return resolveBeforeTarget(repo, blueprint, target), nil
}

func resolveBeforeTarget(repo *rootconfig.Config, blueprint *models.EnvironmentBlueprint, target *models.Target) BeforeTarget {
	bundle := repo.Bundles()[target.BundleName]
	env, dotenvEnv := resolveGeneratedTargetEnv(repo, bundle, target, blueprint, models.EnvironmentTarget{})
	dotenvFiles := ResolveDotenvFiles(repo.RepoRoot(), bundle, target)
	return BeforeTarget{
		Ref:        target.ID(),
		ConfigPath: bundleConfigPath(bundle),
		Command:    target.Cmd,
		WorkingDir: ResolveWorkingDir(repo.RepoRoot(), target),
		Env:        env,
		DotenvEnv:  dotenvEnv,
		Dotenv: Dotenv{
			Enabled: target.Config.Dotenv.Enabled,
			Files:   dotenvFiles,
		},
	}
}

func resolveDepTargets(repo *rootconfig.Config, blueprint *models.EnvironmentBlueprint, targetSeen map[string]bool, beforeSeen map[string]bool, addBundle func(*models.Bundle)) ([]BeforeTarget, error) {
	ordered := []BeforeTarget{}
	visited := make(map[string]bool)
	inStack := make(map[string]bool)
	var visit func(ref string, path []string) error
	visit = func(ref string, path []string) error {
		if inStack[ref] {
			return errors.Errorf("target dependency cycle detected: %s", strings.Join(append(path, ref), " -> "))
		}
		if visited[ref] || beforeSeen[ref] || (targetSeen[ref] && len(path) > 0) {
			return nil
		}
		visited[ref] = true
		inStack[ref] = true
		target, err := repo.ResolveTarget(ref)
		if err != nil {
			return errors.Wrapf(err, "unknown dep target ref %q", ref)
		}
		path = append(path, ref)
		for _, depRef := range target.Deps {
			if err := visit(qualifyTargetRef(depRef, target.BundleName), path); err != nil {
				return err
			}
		}
		inStack[ref] = false
		if len(path) > 1 && !longRunningTarget(target.Name) {
			addBundle(repo.Bundles()[target.BundleName])
			ordered = append(ordered, resolveBeforeTarget(repo, blueprint, target))
		}
		return nil
	}
	for _, bpTarget := range blueprint.Targets {
		if err := visit(bpTarget.Ref, nil); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

func qualifyTargetRef(ref string, bundleName string) string {
	if strings.HasPrefix(ref, ":") {
		return bundleName + ref
	}
	return ref
}

func longRunningTarget(name string) bool {
	return strings.HasSuffix(name, "_dev") || strings.HasSuffix(name, "_serve")
}

func compareBeforeTargets(left, right resolvedBeforeTarget) bool {
	if left.index != nil && right.index != nil {
		if *left.index != *right.index {
			return *left.index < *right.index
		}
		return left.target.Ref < right.target.Ref
	}
	if left.index != nil {
		return true
	}
	if right.index != nil {
		return false
	}
	return left.target.Ref < right.target.Ref
}

func ResolveRepoEnv(repo *rootconfig.Config, blueprint *models.EnvironmentBlueprint) []EnvVar {
	env := appendEnvMap(nil, repo.Repo().Env.Vars)
	env = append(env, "REPO_ROOT="+repo.RepoRoot())
	env = appendEnvMap(env, blueprint.Variables)
	return mergeEnvVars(env)
}

func ResolveBundleEnv(repo *rootconfig.Config, bundle *models.Bundle, blueprint *models.EnvironmentBlueprint) []EnvVar {
	env := appendEnvMap(nil, repo.Repo().Env.Vars)
	env = append(env, "REPO_ROOT="+repo.RepoRoot())
	env = append(env, "BUNDLE_ROOT="+filepath.Join(repo.RepoRoot(), bundle.Path))
	env = appendEnvMap(env, bundle.Env)
	env = appendEnvMap(env, blueprint.Variables)
	return mergeEnvVars(env)
}

func ResolveTargetEnv(repo *rootconfig.Config, bundle *models.Bundle, target *models.Target, blueprint *models.EnvironmentBlueprint, bpTarget models.EnvironmentTarget) []EnvVar {
	env := os.Environ()
	env = appendExplicitTargetEnv(env, repo, bundle, target, blueprint, bpTarget)

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

func ResolveGeneratedTargetEnv(repo *rootconfig.Config, bundle *models.Bundle, target *models.Target, blueprint *models.EnvironmentBlueprint, bpTarget models.EnvironmentTarget) []EnvVar {
	env, _ := resolveGeneratedTargetEnv(repo, bundle, target, blueprint, bpTarget)
	return env
}

func resolveGeneratedTargetEnv(repo *rootconfig.Config, bundle *models.Bundle, target *models.Target, blueprint *models.EnvironmentBlueprint, bpTarget models.EnvironmentTarget) ([]EnvVar, []EnvVar) {
	env := appendExplicitTargetEnv(nil, repo, bundle, target, blueprint, bpTarget)
	dotenv := []string{}
	if target.Config.Dotenv.Enabled {
		for _, filePath := range ResolveDotenvFiles(repo.RepoRoot(), bundle, target) {
			fileVars, err := rpmexec.LoadDotenv(filePath)
			if err == nil {
				env = appendEnvMap(env, fileVars)
				dotenv = appendEnvMap(dotenv, fileVars)
			}
		}
	}
	return mergeEnvVars(env), mergeEnvVars(dotenv)
}

func ResolveWatchRoots(repoRoot string, bundle *models.Bundle, target *models.Target) []string {
	bundleRoot := filepath.Join(repoRoot, bundle.Path)
	roots := make([]string, 0, len(target.In)+1)
	if len(target.In) == 0 {
		roots = append(roots, bundleRoot)
	} else {
		for _, input := range target.In {
			roots = append(roots, filepath.Join(bundleRoot, input))
		}
	}
	return sortedStrings(roots)
}

func appendExplicitTargetEnv(env []string, repo *rootconfig.Config, bundle *models.Bundle, target *models.Target, blueprint *models.EnvironmentBlueprint, bpTarget models.EnvironmentTarget) []string {
	env = appendEnvMap(env, repo.Repo().Env.Vars)
	env = append(env, "REPO_ROOT="+repo.RepoRoot())
	env = append(env, "BUNDLE_ROOT="+filepath.Join(repo.RepoRoot(), bundle.Path))
	env = appendEnvMap(env, bundle.Env)
	env = appendEnvMap(env, target.Env)
	env = appendEnvMap(env, blueprint.Variables)
	env = appendEnvMap(env, bpTarget.Env)
	return env
}

func ResolveDotenvFiles(repoRoot string, bundle *models.Bundle, target *models.Target) []string {
	if !target.Config.Dotenv.Enabled {
		return []string{}
	}

	bundleRoot := filepath.Join(repoRoot, bundle.Path)
	files := []string{}
	seen := make(map[string]bool)
	add := func(path string) {
		if seen[path] {
			return
		}
		seen[path] = true
		files = append(files, path)
	}
	add(filepath.Join(bundleRoot, ".env"))
	for _, file := range target.Config.Dotenv.Files {
		pattern, err := pathsafe.Resolve(bundleRoot, file)
		if err != nil {
			continue
		}
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) == 0 {
			matches = []string{pattern}
		}
		sort.Strings(matches)
		for _, match := range matches {
			add(match)
		}
	}
	return files
}

func dependency(dep models.EnvironmentDependency) Dependency {
	ports := append([]string{}, dep.Ports...)
	volumes := append([]string{}, dep.Volumes...)
	sort.Strings(ports)
	sort.Strings(volumes)
	return Dependency{
		Ref:          dep.Name,
		ConfigPath:   "repo.yml",
		Name:         dep.Name,
		Image:        dep.Image,
		Env:          envVars(dep.Env),
		Ports:        ports,
		Volumes:      volumes,
		ReadinessCmd: dep.ReadinessCmd,
	}
}

func bundleConfigPath(bundle *models.Bundle) string {
	return filepath.ToSlash(filepath.Join(bundle.Path, "rpm.yml"))
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

func sortedStrings(values []string) []string {
	result := append([]string{}, values...)
	sort.Strings(result)
	return result
}
