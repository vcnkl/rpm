package builds

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vcnkl/rpm/cache/hashing"
	"github.com/vcnkl/rpm/models"
	"github.com/vcnkl/rpm/pathsafe"
)

type Validator struct {
	repoRoot    string
	shell       string
	toolVersion string
	store       *Store
}

func NewValidator(repoRoot, shell, toolVersion string, store *Store) *Validator {
	return &Validator{
		repoRoot:    repoRoot,
		shell:       shell,
		toolVersion: toolVersion,
		store:       store,
	}
}

func (v *Validator) ShouldBuild(target *models.Target) (bool, string, error) {
	bundleRoot := filepath.Join(v.repoRoot, target.BundlePath)

	filesHash, err := hashing.HashInputs(v.repoRoot, bundleRoot, target.In)
	if err != nil {
		return true, filesHash, err
	}

	currentHash := v.compositeHash(target, filesHash)

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

func (v *Validator) compositeHash(target *models.Target, filesHash string) string {
	return hashing.CombineHash(
		filesHash,
		target.Cmd,
		v.shell,
		v.toolVersion,
		target.Config.WorkingDir,
		joinSortedEnv(target.Env),
		joinSorted(target.Deps),
		joinSorted(target.Out),
	)
}

func joinSortedEnv(env map[string]string) string {
	pairs := make([]string, 0, len(env))
	for name, value := range env {
		pairs = append(pairs, name+"="+value)
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "\n")
}

func joinSorted(values []string) string {
	sorted := append([]string(nil), values...)
	sort.Strings(sorted)
	return strings.Join(sorted, "\n")
}

func (v *Validator) outputsExist(target *models.Target) bool {
	if len(target.Out) == 0 {
		return true
	}

	for _, out := range target.Out {
		path := v.resolveOutputPath(out, target.BundlePath)

		if !pathsafe.Contains(v.repoRoot, path) {
			return false
		}

		if strings.Contains(path, "*") {
			matches, err := hashing.ExpandGlob(path)
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
	if strings.HasPrefix(out, "./") {
		return filepath.Join(v.repoRoot, bundlePath, out[2:])
	}
	return filepath.Join(v.repoRoot, bundlePath, out)
}
