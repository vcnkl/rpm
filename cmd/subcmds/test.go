package subcmds

import (
	"strings"

	"github.com/vcwx/rpm/actions"
	"github.com/vcwx/rpm/dag"
	"github.com/vcwx/rpm/git"
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

			debug := ctx.Bool("debug")
			parallel := ctx.Int("jobs")

			level := logger.InfoLevel
			if debug {
				level = logger.DebugLevel
			}
			cfg := validator.cfg
			logFile, err := openLogFile(ctx, cfg, cfg.Repo().Logs.Test.Out)
			if err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}
			if logFile != nil {
				defer closeLogFile(logFile)
			}
			log := logger.NewWithDateTimeFormat(level, cfg.Repo().Logger.DateTime.Format, logFile)

			graph := validator.graph

			suffix := "_test"
			var targetIDs []string

			selector := dag.NewSelector(graph, cfg.RepoRoot())
			if affected {
				changedFiles, err := git.GetChangedFiles(cfg.RepoRoot())
				if err != nil {
					return cli.Exit("error: "+err.Error(), 1)
				}
				targets := selector.SelectAffected(changedFiles)
				for _, t := range targets {
					if strings.HasSuffix(t.Target.Name, suffix) {
						targetIDs = append(targetIDs, t.ID)
					}
				}
			} else if ctx.Args().Len() > 0 {
				targetIDs = validator.targetIds
			} else {
				targets := selector.SelectBySuffix(suffix)
				for _, t := range targets {
					targetIDs = append(targetIDs, t.ID)
				}
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
