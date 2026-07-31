package subcmds

import (
	"io"
	"strings"

	"github.com/vcwx/rpm/config"
	"github.com/vcwx/rpm/dag"
	"github.com/vcwx/rpm/git"
	"github.com/vcwx/rpm/logger"

	"github.com/urfave/cli/v2"
)

func loadConfig(ctx *cli.Context) *config.Config {
	if path := ctx.String("config"); path != "" {
		return config.NewConfigWithRepoFile(path)
	}
	return config.NewConfig()
}

func openLogFile(ctx *cli.Context, cfg *config.Config, out string, nested ...string) (io.WriteCloser, error) {
	enabled := cfg.Repo().Logs.Enabled
	if ctx.IsSet("logs") {
		enabled = ctx.Bool("logs")
	}
	if !enabled {
		return nil, nil
	}
	return logger.OpenFile(cfg.RepoRoot(), out, nested...)
}

func closeLogFile(file io.Closer) {
	_ = file.Close()
}

func newCommandLogger(ctx *cli.Context, cfg *config.Config, out string) (logger.Logger, func(), error) {
	level := logger.InfoLevel
	if ctx.Bool("debug") {
		level = logger.DebugLevel
	}

	logFile, err := openLogFile(ctx, cfg, out)
	if err != nil {
		return nil, nil, err
	}

	closeLog := func() {
		if logFile == nil {
			return
		}
		closeLogFile(logFile)
	}

	return logger.NewWithDateTimeFormat(level, cfg.Repo().Logger.DateTime.Format, logFile), closeLog, nil
}

func selectCommandTargets(ctx *cli.Context, v cmdValidator, suffix string) ([]string, error) {
	selector := dag.NewSelector(v.graph, v.cfg.RepoRoot())

	if ctx.Bool("affected") {
		changedFiles, err := git.GetChangedFiles(v.cfg.RepoRoot())
		if err != nil {
			return nil, err
		}
		var targetIDs []string
		for _, t := range selector.SelectAffected(changedFiles) {
			if strings.HasSuffix(t.Target.Name, suffix) {
				targetIDs = append(targetIDs, t.ID)
			}
		}
		return targetIDs, nil
	}

	if ctx.Args().Len() > 0 {
		return v.targetIds, nil
	}

	var targetIDs []string
	for _, t := range selector.SelectBySuffix(suffix) {
		targetIDs = append(targetIDs, t.ID)
	}
	return targetIDs, nil
}
