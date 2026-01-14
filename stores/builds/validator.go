package builds

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/vcnkl/rpm/cache/hashing"
	"github.com/vcnkl/rpm/models"
)

type Validator struct {
	repoRoot string
	store    *Store
}

func NewValidator(repoRoot string, store *Store) *Validator {
	return &Validator{
		repoRoot: repoRoot,
		store:    store,
	}
}

func (v *Validator) ShouldBuild(target *models.Target) (bool, string, error) {
	bundleRoot := filepath.Join(v.repoRoot, target.BundlePath)

	currentHash, err := hashing.HashInputs(bundleRoot, target.In)
	if err != nil {
		return true, currentHash, err
	}

	entry, ok := v.store.Get(target.ID())
	if !ok {
		return true, currentHash, nil
	}

	if entry.InputHash != currentHash {
		return true, currentHash, nil
	}

	if !v.outputsExist(target) {
		return true, currentHash, nil
	}

	return false, currentHash, nil
}

func (v *Validator) outputsExist(target *models.Target) bool {
	if len(target.Out) == 0 {
		return true
	}

	for _, out := range target.Out {
		if strings.HasPrefix(out, "@docker::") {
			continue
		}

		path := v.resolveOutputPath(out, target.BundlePath)

		if strings.Contains(path, "*") {
			matches, err := filepath.Glob(path)
			if err != nil || len(matches) == 0 {
				return false
			}
			continue
		}

		if _, err := os.Stat(path); os.IsNotExist(err) {
			return false
		}
	}

	return true
}

func (v *Validator) resolveOutputPath(out, bundlePath string) string {
	if strings.HasPrefix(out, "//") {
		return filepath.Join(v.repoRoot, out[2:])
	}
	if strings.HasPrefix(out, "@docker::") {
		return ""
	}
	if strings.HasPrefix(out, "./") {
		return filepath.Join(v.repoRoot, bundlePath, out[2:])
	}
	return filepath.Join(v.repoRoot, bundlePath, out)
}
