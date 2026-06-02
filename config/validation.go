package config

import (
	"fmt"
	"strings"

	"github.com/distribution/reference"
	"github.com/pkg/errors"
)

var (
	ErrMissingBundleName    = errors.New("missing bundle name")
	ErrMissingTargetName    = errors.New("missing target name")
	ErrMissingTargetCommand = errors.New("missing target command")
	ErrInvalidTargetRef     = errors.New("invalid target ref")
	ErrInvalidDependency    = errors.New("invalid dependency")
	ErrMissingProjectName   = errors.New("missing project name")
)

func validateRepoConfig(cfg *RepoConfig, path string) error {
	if strings.TrimSpace(cfg.Project.Name) == "" {
		return errors.Wrapf(ErrMissingProjectName, "%s project.name is required", path)
	}

	deps := make(map[string]bool)
	for _, dep := range cfg.Dependencies {
		if dep.Name == "" {
			return errors.Wrap(ErrInvalidDependency, "repo dependency with missing name")
		}
		if deps[dep.Name] {
			return fmt.Errorf("duplicate dependency name %q in repo.yml", dep.Name)
		}
		deps[dep.Name] = true
		if err := ValidateDependencyImage(dep.Image); err != nil {
			return errors.Wrapf(err, "%s", dep.Name)
		}
		volumes := make(map[string]bool)
		for _, volume := range dep.Volumes {
			if strings.TrimSpace(volume) == "" {
				return errors.Wrapf(ErrInvalidDependency, "%s has empty volume path", dep.Name)
			}
			if strings.Contains(volume, ":") {
				return errors.Wrapf(ErrInvalidDependency, "%s volume %q must be a container path only", dep.Name, volume)
			}
			if !strings.HasPrefix(volume, "/") {
				return errors.Wrapf(ErrInvalidDependency, "%s volume %q must be an absolute container path", dep.Name, volume)
			}
			if volumes[volume] {
				return errors.Wrapf(ErrInvalidDependency, "%s has duplicate volume path %q", dep.Name, volume)
			}
			volumes[volume] = true
		}
	}
	return nil
}

func validateBundleConfig(cfg *BundleConfig, path string, repoDeps map[string]bool) error {
	if cfg.Name == "" {
		return errors.Wrapf(ErrMissingBundleName, "%s", path)
	}

	targets := make(map[string]bool)
	for _, target := range cfg.Targets {
		if target.Name == "" {
			return errors.Wrapf(ErrMissingTargetName, "%s", path)
		}
		if target.GetCmd() == "" {
			return errors.Wrapf(ErrMissingTargetCommand, "%s:%s", cfg.Name, target.Name)
		}
		if targets[target.Name] {
			return fmt.Errorf("duplicate target name %q in bundle %q", target.Name, cfg.Name)
		}
		targets[target.Name] = true
		for _, ref := range target.Deps {
			if !validTargetRef(ref) {
				return errors.Wrapf(ErrInvalidTargetRef, "%s:%s dependency %q", cfg.Name, target.Name, ref)
			}
		}
	}

	deps := make(map[string]bool)
	for _, dep := range cfg.Env.Deps {
		if dep == "" {
			return errors.Wrapf(ErrInvalidDependency, "%s has empty env.deps entry", cfg.Name)
		}
		if deps[dep] {
			return fmt.Errorf("duplicate dependency ref %q in bundle %q", dep, cfg.Name)
		}
		deps[dep] = true
		if !repoDeps[dep] {
			return errors.Wrapf(ErrInvalidDependency, "%s references unknown dependency %q", cfg.Name, dep)
		}
	}

	return nil
}

func ValidateDependencyImage(image string) error {
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return errors.Wrapf(ErrInvalidDependency, "invalid dependency image %q", image)
	}
	if _, ok := named.(reference.Tagged); !ok {
		return errors.Wrapf(ErrInvalidDependency, "dependency image %q must include a tag", image)
	}
	if _, ok := named.(reference.Digested); ok {
		return errors.Wrapf(ErrInvalidDependency, "dependency image %q must not use digest syntax", image)
	}
	return nil
}

func validTargetRef(ref string) bool {
	parts := strings.Split(ref, ":")
	switch len(parts) {
	case 2:
		return parts[1] != "" && (parts[0] != "" || strings.HasPrefix(ref, ":"))
	default:
		return false
	}
}
