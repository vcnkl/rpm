package subcmds

import (
	"github.com/vcnkl/rpm/actions"
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
			validator := newCmdValidator(ctx).loadConfig().resolveGraph()
			validation := validator.validate()
			if !validation.ok() {
				return cli.Exit(ValidationError(validation.errors()).Error(), 1)
			}

			debug := ctx.Bool("debug")
			force := ctx.Bool("force")

			level := logger.InfoLevel
			if debug {
				level = logger.DebugLevel
			}
			cfg := validator.cfg
			log := logger.NewWithDateTimeFormat(level, cfg.Repo().Logger.DateTime.Format)

			action := actions.NewInitAction(cfg, validator.graph, log, force)
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
