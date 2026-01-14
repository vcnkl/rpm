package git

import (
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
)

func IsTracked(path string) (bool, error) {
	cmd := exec.Command("git", "ls-files", "--error-unmatch", path)
	err := cmd.Run()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func GetChangedFiles(repoRoot string) ([]string, error) {
	cmd := exec.Command("git", "diff", "--name-only", "HEAD")
	cmd.Dir = repoRoot
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		ok := errors.As(err, &exitErr)
		if ok && len(exitErr.Stderr) > 0 {
			return nil, err
		}
	}

	stagedCmd := exec.Command("git", "diff", "--name-only", "--cached")
	stagedCmd.Dir = repoRoot
	stagedOutput, err := stagedCmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		ok := errors.As(err, &exitErr)
		if ok && len(exitErr.Stderr) > 0 {
			return nil, err
		}
	}

	untrackedCmd := exec.Command("git", "ls-files", "--others", "--exclude-standard")
	untrackedCmd.Dir = repoRoot
	untrackedOutput, err := untrackedCmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		ok := errors.As(err, &exitErr)
		if ok && len(exitErr.Stderr) > 0 {
			return nil, err
		}
	}

	combined := string(output) + string(stagedOutput) + string(untrackedOutput)

	seen := make(map[string]bool)
	var files []string

	for _, line := range strings.Split(combined, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		absPath := filepath.Join(repoRoot, line)
		if !seen[absPath] {
			seen[absPath] = true
			files = append(files, absPath)
		}
	}

	return files, nil
}
