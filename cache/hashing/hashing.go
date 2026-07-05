package hashing

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vcnkl/rpm/pathsafe"
)

func HashInputs(repoRoot string, bundleRoot string, patterns []string) (string, error) {
	var allFiles []string

	for _, pattern := range patterns {
		fullPattern := pattern
		if startsWithRepoRoot(pattern) {
			fullPattern = filepath.Join(repoRoot, pattern[2:])
		} else if !filepath.IsAbs(pattern) {
			fullPattern = filepath.Join(bundleRoot, pattern)
		}

		if !pathsafe.Contains(repoRoot, fullPattern) {
			return "", fmt.Errorf("input pattern %q escapes repository root", pattern)
		}

		matches, err := expandGlob(fullPattern)
		if err != nil {
			return "", fmt.Errorf("failed to expand glob pattern %s: %w", pattern, err)
		}
		allFiles = append(allFiles, matches...)
	}

	sort.Strings(allFiles)

	h := sha256.New()
	for _, file := range allFiles {
		info, err := os.Stat(file)
		if err != nil {
			continue
		}
		if info.IsDir() {
			continue
		}

		fileHash, err := HashFile(file)
		if err != nil {
			return "", err
		}

		rel, err := filepath.Rel(repoRoot, file)
		if err != nil {
			rel = file
		}
		h.Write([]byte(rel))
		h.Write([]byte{0})
		h.Write([]byte(fileHash))
		h.Write([]byte{0})
	}

	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func CombineHash(components ...string) string {
	h := sha256.New()
	for _, component := range components {
		h.Write([]byte(component))
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func ExpandGlob(pattern string) ([]string, error) {
	return expandGlob(pattern)
}

func expandGlob(pattern string) ([]string, error) {
	if strings.Contains(pattern, "**") {
		return expandDoubleStarGlob(pattern)
	}
	return filepath.Glob(pattern)
}

func expandDoubleStarGlob(pattern string) ([]string, error) {
	parts := strings.Split(pattern, "**")
	if len(parts) != 2 {
		return filepath.Glob(pattern)
	}

	baseDir := strings.TrimSuffix(parts[0], "/")
	if baseDir == "" {
		baseDir = "."
	}
	suffix := strings.TrimPrefix(parts[1], "/")

	var matches []string
	err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}

		if suffix == "" {
			matches = append(matches, path)
			return nil
		}

		rel, relErr := filepath.Rel(baseDir, path)
		if relErr != nil {
			return nil
		}
		if pathsafe.MatchSuffix(suffix, rel) {
			matches = append(matches, path)
		}

		return nil
	})

	return matches, err
}

func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open file %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", fmt.Errorf("failed to hash file %s: %w", path, err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func startsWithRepoRoot(pattern string) bool {
	return len(pattern) >= 2 && pattern[0:2] == "//"
}
