package subcmds

import (
	"github.com/vcnkl/rpm/actions"
	"github.com/vcnkl/rpm/config"
	"github.com/vcnkl/rpm/dag"
	"github.com/vcnkl/rpm/logger"

	"github.com/urfave/cli/v2"
)

func InitCmd() *cli.Command {
	return &cli.Command{
		Name:  "init",
		Usage: "Configure local environment, install global dependencies",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "force",
				Aliases: []string{"f"},
				Usage:   "Re-run all install commands even if check passes",
			},
		},
		Action: func(ctx *cli.Context) error {
			debug := ctx.Bool("debug")
			force := ctx.Bool("force")

			level := logger.InfoLevel
			if debug {
				level = logger.DebugLevel
			}
			log := logger.New(level)

			cfg := config.NewConfig()

			graph := dag.NewGraph()
			for _, bundle := range cfg.Bundles() {
				for _, target := range bundle.Targets {
					graph.AddTarget(target)
				}
			}

			if err := graph.Resolve(cfg.Bundles()); err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}

			action := actions.NewInitAction(cfg, graph, log, force)
			result, err := action.Execute(ctx.Context)
			if err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}

			log.Info("init completed",
				logger.Int("executed", len(result.Executed)),
				logger.Int("failed", len(result.Failed)),
				logger.Duration("duration", result.Duration))

			if len(result.Failed) > 0 {
				return cli.Exit("init failed", 1)
			}

			return nil
		},
	}
}
