package actions

import (
	"context"
	"io"

	"github.com/vcwx/rpm/config"
	"github.com/vcwx/rpm/exec"
	"github.com/vcwx/rpm/logger"
	"github.com/vcwx/rpm/models"
)

func runTargetCommand(ctx context.Context, cfg *config.Config, target *models.Target, out io.Writer) error {
	bundle := cfg.Bundles()[target.BundleName]

	return exec.RunCommand(ctx, target.Cmd, &exec.ShellOptions{
		WorkDir: exec.ResolveWorkDir(cfg.RepoRoot(), target),
		Env:     exec.ComposeEnv(cfg.RepoRoot(), cfg.Repo(), bundle, target),
		Shell:   cfg.Repo().Shell,
		Stdout:  out,
		Stderr:  out,
	})
}

func logTargetPlan(log logger.Logger, cfg *config.Config, target *models.Target) {
	bundle := cfg.Bundles()[target.BundleName]
	env := exec.ComposeEnv(cfg.RepoRoot(), cfg.Repo(), bundle, target)
	workDir := exec.ResolveWorkDir(cfg.RepoRoot(), target)

	log.Info("target", logger.String("id", target.ID()))
	log.Info("workdir", logger.String("path", workDir))
	log.Info("command", logger.String("cmd", target.Cmd))
	for _, e := range env {
		log.Info("env", logger.String("var", e))
	}
}
