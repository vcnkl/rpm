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
			// assume tracked when not in a git repo
			if exitError.ExitCode() == 128 {
				return true, nil
			}
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func runGit(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		ok := errors.As(err, &exitErr)
		if ok && len(exitErr.Stderr) > 0 {
			return nil, err
		}
	}
	return output, nil
}

func GetChangedFiles(repoRoot string) ([]string, error) {
	output, err := runGit(repoRoot, "diff", "--name-only", "HEAD")
	if err != nil {
		return nil, err
	}

	stagedOutput, err := runGit(repoRoot, "diff", "--name-only", "--cached")
	if err != nil {
		return nil, err
	}

	untrackedOutput, err := runGit(repoRoot, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
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
