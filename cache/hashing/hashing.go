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
)

func HashInputs(bundleRoot string, patterns []string) (string, error) {
	var allFiles []string

	for _, pattern := range patterns {
		fullPattern := pattern
		if !filepath.IsAbs(pattern) && !startsWithRepoRoot(pattern) {
			fullPattern = filepath.Join(bundleRoot, pattern)
		} else if startsWithRepoRoot(pattern) {
			fullPattern = pattern[2:]
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
		h.Write([]byte(fileHash))
	}

	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
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

		matched, err := filepath.Match(suffix, filepath.Base(path))
		if err != nil {
			return nil
		}
		if matched {
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
	defer f.Close()

	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", fmt.Errorf("failed to hash file %s: %w", path, err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func startsWithRepoRoot(pattern string) bool {
	return len(pattern) >= 2 && pattern[0:2] == "//"
}
