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
)

func validateBundleConfig(cfg *BundleConfig, path string) error {
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
	for _, dep := range cfg.Dependencies {
		if dep.Name == "" {
			return errors.Wrapf(ErrInvalidDependency, "%s has dependency with missing name", cfg.Name)
		}
		if deps[dep.Name] {
			return fmt.Errorf("duplicate dependency name %q in bundle %q", dep.Name, cfg.Name)
		}
		deps[dep.Name] = true
		if err := ValidateDependencyImage(dep.Image); err != nil {
			return errors.Wrapf(err, "%s:%s", cfg.Name, dep.Name)
		}
		if !dep.Mode.Valid() {
			return fmt.Errorf("invalid dependency mode %q for %s:%s", dep.Mode, cfg.Name, dep.Name)
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
