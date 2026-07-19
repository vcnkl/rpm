package subcmds

import (
	"os/signal"
	"syscall"

	"github.com/vcnkl/rpm/actions"
	"github.com/vcnkl/rpm/dag"
	"github.com/vcnkl/rpm/logger"

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
			debug := ctx.Bool("debug")
			dryRun := ctx.Bool("dry-run")

			level := logger.InfoLevel
			if debug {
				level = logger.DebugLevel
			}
			cfg := loadConfig(ctx)
			logFile, err := openLogFile(ctx, cfg, cfg.Repo().Logs.Dev.Out)
			if err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}
			if logFile != nil {
				defer closeLogFile(logFile)
			}
			log := logger.NewWithDateTimeFormat(level, cfg.Repo().Logger.DateTime.Format, logFile)

			graph := dag.NewGraph()
			for _, bundle := range cfg.Bundles() {
				for _, target := range bundle.Targets {
					graph.AddTarget(target)
				}
			}

			if err := graph.Resolve(cfg.Bundles()); err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}

			selector := dag.NewSelector(graph, cfg.RepoRoot())
			suffixes := []string{"_dev", "_serve"}
			var targetIDs []string
			seen := make(map[string]bool)

			if ctx.Args().Len() > 0 {
				for _, ref := range ctx.Args().Slice() {
					matched := false
					for _, suffix := range suffixes {
						for _, id := range selector.ResolveTargetRefs([]string{ref}, suffix) {
							if _, ok := graph.Nodes[id]; !ok {
								continue
							}
							matched = true
							if !seen[id] {
								seen[id] = true
								targetIDs = append(targetIDs, id)
							}
						}
					}
					if !matched {
						return cli.Exit("error: target not found: "+ref, 1)
					}
				}
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
