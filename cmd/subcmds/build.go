package subcmds

import (
	"strings"

	"github.com/vcnkl/rpm/actions"
	"github.com/vcnkl/rpm/dag"
	"github.com/vcnkl/rpm/git"
	"github.com/vcnkl/rpm/logger"
	"github.com/vcnkl/rpm/stores/builds"

	"github.com/urfave/cli/v2"
)

func BuildCmd() *cli.Command {
	return &cli.Command{
		Name:      "build",
		Usage:     "Build specified targets (or all *_build targets if none specified)",
		ArgsUsage: "[targets...]",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "force",
				Aliases: []string{"f"},
				Usage:   "Ignore cache, rebuild all",
			},
			&cli.BoolFlag{
				Name:  "affected",
				Usage: "Only build targets affected by git changes",
			},
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "Print what would be built without executing",
			},
		},
		Action: func(ctx *cli.Context) error {
			affected := ctx.Bool("affected")
			validator := newCmdValidator(ctx).loadConfig().resolveGraph()
			if !affected && ctx.Args().Len() > 0 {
				validator = validator.resolveTargetRefs("_build")
			}
			validation := validator.validate()
			if !validation.ok() {
				return cli.Exit(ValidationError(validation.errors()).Error(), 1)
			}

			debug := ctx.Bool("debug")
			force := ctx.Bool("force")
			dryRun := ctx.Bool("dry-run")
			parallel := ctx.Int("jobs")

			level := logger.InfoLevel
			if debug {
				level = logger.DebugLevel
			}
			cfg := validator.cfg
			logFile, err := openLogFile(ctx, cfg, cfg.Repo().Logs.Build.Out)
			if err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}
			if logFile != nil {
				defer closeLogFile(logFile)
			}
			log := logger.NewWithDateTimeFormat(level, cfg.Repo().Logger.DateTime.Format, logFile)

			graph := validator.graph

			store := builds.NewStore(cfg.BuildsPath())
			if err := store.Load(); err != nil {
				log.Warn("failed to load cache", logger.Err(err))
			}

			var targetIDs []string

			selector := dag.NewSelector(graph, cfg.RepoRoot())
			if affected {
				changedFiles, err := git.GetChangedFiles(cfg.RepoRoot())
				if err != nil {
					return cli.Exit("error: "+err.Error(), 1)
				}
				targets := selector.SelectAffected(changedFiles)
				for _, t := range targets {
					if strings.HasSuffix(t.Target.Name, "_build") {
						targetIDs = append(targetIDs, t.ID)
					}
				}
			} else if ctx.Args().Len() > 0 {
				targetIDs = validator.targetIds
			} else {
				targets := selector.SelectBySuffix("_build")
				for _, t := range targets {
					targetIDs = append(targetIDs, t.ID)
				}
			}

			if len(targetIDs) == 0 {
				log.Info("no targets to build")
				return nil
			}

			action := actions.NewBuildAction(cfg, graph, store, log, parallel, force)

			if dryRun {
				action.DryRun(targetIDs)
				return nil
			}

			result, err := action.Execute(ctx.Context, targetIDs)
			if err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}

			log.Info("build completed",
				logger.Int("executed", len(result.Executed)),
				logger.Int("skipped", len(result.Skipped)),
				logger.Int("failed", len(result.Failed)),
				logger.Duration("duration", result.Duration))

			if len(result.Failed) > 0 {
				return cli.Exit("build failed", 1)
			}

			return nil
		},
	}
}
