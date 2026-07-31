package subcmds

import (
	"github.com/vcwx/rpm/actions"
	"github.com/vcwx/rpm/logger"

	"github.com/urfave/cli/v2"
)

func RunCmd() *cli.Command {
	return &cli.Command{
		Name:      "run",
		Usage:     "Run any arbitrary target by exact name",
		ArgsUsage: "<target>",
		Action: func(ctx *cli.Context) error {
			validator := newCmdValidator(ctx).
				useFirstArgument().
				requireArgument("target").
				loadConfig().
				resolveGraph().
				resolveTargetRefs()
			validation := validator.validate()
			if !validation.ok() {
				return cli.Exit(ValidationError(validation.errors()).Error(), 1)
			}

			debug := ctx.Bool("debug")
			targetID := validator.targetIds[0]

			level := logger.InfoLevel
			if debug {
				level = logger.DebugLevel
			}
			cfg := validator.cfg
			log := logger.NewWithDateTimeFormat(level, cfg.Repo().Logger.DateTime.Format)

			action := actions.NewRunAction(cfg, validator.graph, log)
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
