package subcmds

import (
	"io"

	"github.com/vcwx/rpm/config"
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
