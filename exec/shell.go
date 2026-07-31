package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vcwx/rpm/models"
	"github.com/vcwx/rpm/pathsafe"
)

type ShellOptions struct {
	WorkDir string
	Env     []string
	Shell   string
	Stdout  io.Writer
	Stderr  io.Writer
	Timeout time.Duration
}

func RunCommand(ctx context.Context, cmdStr string, opts *ShellOptions) error {
	if opts.Shell == "" {
		opts.Shell = "/usr/bin/env bash"
	}

	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	shellParts := strings.Fields(opts.Shell)
	if len(shellParts) == 0 {
		shellParts = []string{"/bin/sh"}
	}
	args := append([]string{}, shellParts[1:]...)
	args = append(args, "-c", cmdStr)

	cmd := exec.CommandContext(ctx, shellParts[0], args...)
	cmd.Env = opts.Env
	cmd.Dir = opts.WorkDir
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("command exited with status %d", exitErr.ExitCode())
		}
		return err
	}
	return nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func ResolveWorkDir(repoRoot string, target *models.Target) string {
	workDir := target.Config.WorkingDir
	switch workDir {
	case "", "local":
		return filepath.Join(repoRoot, target.BundlePath)
	case "repo_root":
		return repoRoot
	default:
		if filepath.IsAbs(workDir) {
			return workDir
		}
		resolved, err := pathsafe.Resolve(repoRoot, filepath.Join(target.BundlePath, workDir))
		if err != nil {
			return filepath.Join(repoRoot, target.BundlePath)
		}
		return resolved
	}
}
