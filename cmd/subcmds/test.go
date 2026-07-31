package subcmds

import (
	"github.com/vcwx/rpm/actions"
	"github.com/vcwx/rpm/logger"

	"github.com/urfave/cli/v2"
)

func TestCmd() *cli.Command {
	return &cli.Command{
		Name:      "test",
		Usage:     "Run test targets (*_test suffix)",
		ArgsUsage: "[targets...]",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "affected",
				Usage: "Only test targets affected by git changes",
			},
			&cli.BoolFlag{
				Name:  "coverage",
				Usage: "Pass coverage flags (target must handle)",
			},
		},
		Action: func(ctx *cli.Context) error {
			affected := ctx.Bool("affected")
			validator := newCmdValidator(ctx).loadConfig().resolveGraph()
			if !affected && ctx.Args().Len() > 0 {
				validator = validator.resolveTargetRefs("_test")
			}
			validation := validator.validate()
			if !validation.ok() {
				return cli.Exit(ValidationError(validation.errors()).Error(), 1)
			}

			parallel := ctx.Int("jobs")

			cfg := validator.cfg
			log, closeLog, err := newCommandLogger(ctx, cfg, cfg.Repo().Logs.Test.Out)
			if err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}
			defer closeLog()

			graph := validator.graph

			targetIDs, err := selectCommandTargets(ctx, validator, "_test")
			if err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}

			if len(targetIDs) == 0 {
				log.Info("no test targets found")
				return nil
			}

			action := actions.NewTestAction(cfg, graph, log, parallel)
			result, err := action.Execute(ctx.Context, targetIDs)
			if err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}

			log.Info("tests completed",
				logger.Int("passed", len(result.Executed)),
				logger.Int("failed", len(result.Failed)),
				logger.Duration("duration", result.Duration))

			if len(result.Failed) > 0 {
				return cli.Exit("tests failed", 1)
			}

			return nil
		},
	}
}
