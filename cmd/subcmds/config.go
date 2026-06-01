package subcmds

import (
	"github.com/vcnkl/rpm/config"

	"github.com/urfave/cli/v2"
)

func loadConfig(ctx *cli.Context) *config.Config {
	if path := ctx.String("config"); path != "" {
		return config.NewConfigWithRepoFile(path)
	}
	return config.NewConfig()
}
