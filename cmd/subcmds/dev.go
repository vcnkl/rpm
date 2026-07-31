package subcmds

import (
	"os/signal"
	"syscall"

	"github.com/vcwx/rpm/actions"
	"github.com/vcwx/rpm/dag"
	"github.com/vcwx/rpm/logger"

	"github.com/urfave/cli/v2"
)

func DevCmd() *cli.Command {
	return &cli.Command{
		Name:      "dev",
		Usage:     "Run dev targets with file watching and hot reload",
		ArgsUsage: "[targets...]",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "Print what would be executed without running",
			},
		},
		Action: func(ctx *cli.Context) error {
			validator := newCmdValidator(ctx).loadConfig().resolveGraph()
			if ctx.Args().Len() > 0 {
				validator = validator.resolveTargetRefs("_dev", "_serve")
			}
			validation := validator.validate()
			if !validation.ok() {
				return cli.Exit(ValidationError(validation.errors()).Error(), 1)
			}

			dryRun := ctx.Bool("dry-run")

			cfg := validator.cfg
			log, closeLog, err := newCommandLogger(ctx, cfg, cfg.Repo().Logs.Dev.Out)
			if err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}
			defer closeLog()

			graph := validator.graph

			selector := dag.NewSelector(graph, cfg.RepoRoot())
			suffixes := []string{"_dev", "_serve"}
			var targetIDs []string
			seen := make(map[string]bool)

			if ctx.Args().Len() > 0 {
				targetIDs = validator.targetIds
			} else {
				for _, suffix := range suffixes {
					for _, n := range selector.SelectBySuffix(suffix) {
						if !seen[n.ID] {
							seen[n.ID] = true
							targetIDs = append(targetIDs, n.ID)
						}
					}
				}
			}

			if len(targetIDs) == 0 {
				log.Info("no dev targets found")
				return nil
			}

			if dryRun {
				action := actions.NewDevAction(cfg, graph, log)
				action.DryRun(targetIDs)
				return nil
			}

			devCtx, stopSignals := signal.NotifyContext(ctx.Context, syscall.SIGINT, syscall.SIGTERM)
			defer stopSignals()

			action := actions.NewDevAction(cfg, graph, log)
			result, err := action.Execute(devCtx, targetIDs)
			if devCtx.Err() != nil {
				log.Info("shutting down...")
			}
			if err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}

			log.Info("dev stopped", logger.Duration("duration", result.Duration))

			return nil
		},
	}
}
