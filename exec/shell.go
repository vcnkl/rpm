package exec

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bitfield/script"
	"github.com/vcnkl/rpm/models"
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
	shellCmd := shellParts[0]
	shellArgs := shellParts[1:]

	wrappedCmd := cmdStr
	if opts.WorkDir != "" {
		wrappedCmd = fmt.Sprintf("cd %q && (\n%s\n)", opts.WorkDir, cmdStr)
	}

	fullCmd := shellCmd
	for _, arg := range shellArgs {
		fullCmd += " " + arg
	}
	fullCmd += " -c " + shellQuote(wrappedCmd)

	done := make(chan error, 1)
	go func() {
		pipe := script.NewPipe().WithEnv(opts.Env)
		pipe = pipe.Exec(fullCmd)
		pipe = pipe.WithStdout(opts.Stdout).WithStderr(opts.Stderr)
		_, err := pipe.Stdout()
		exitStatus := pipe.ExitStatus()
		if err == nil && exitStatus != 0 {
			err = fmt.Errorf("command exited with status %d", exitStatus)
		}
		done <- err
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
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
		return filepath.Join(repoRoot, target.BundlePath, workDir)
	}
}
