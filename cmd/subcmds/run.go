package subcmds

import (
	"github.com/vcnkl/rpm/actions"
	"github.com/vcnkl/rpm/config"
	"github.com/vcnkl/rpm/dag"
	"github.com/vcnkl/rpm/logger"

	"github.com/urfave/cli/v2"
)

func RunCmd() *cli.Command {
	return &cli.Command{
		Name:      "run",
		Usage:     "Run any arbitrary target by exact name",
		ArgsUsage: "<target>",
		Action: func(ctx *cli.Context) error {
			if ctx.Args().Len() == 0 {
				return cli.Exit("error: target argument required", 1)
			}

			debug := ctx.Bool("debug")
			targetID := ctx.Args().First()

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

			action := actions.NewRunAction(cfg, graph, log)
			result, err := action.Execute(ctx.Context, targetID)
			if err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}

			log.Info("run completed",
				logger.Int("executed", len(result.Executed)),
				logger.Duration("duration", result.Duration))

			if len(result.Failed) > 0 {
				return cli.Exit("run failed", 1)
			}

			return nil
		},
	}
}
