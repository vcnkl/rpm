package subcmds

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/vcnkl/rpm/actions"
	"github.com/vcnkl/rpm/config"
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
				Name:  "no-deps",
				Usage: "Don't start dependency dev targets",
			},
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

			var targetIDs []string

			selector := dag.NewSelector(graph, cfg.RepoRoot())
			if ctx.Args().Len() > 0 {
				targetIDs = selector.ResolveTargetRefs(ctx.Args().Slice(), "_dev")
			} else {
				targets := selector.SelectBySuffix("_dev")
				for _, t := range targets {
					targetIDs = append(targetIDs, t.ID)
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

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

			devCtx, cancel := signal.NotifyContext(ctx.Context, syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			go func() {
				<-sigCh
				log.Info("shutting down...")
				cancel()
			}()

			action := actions.NewDevAction(cfg, graph, log)
			result, err := action.Execute(devCtx, targetIDs)
			if err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}

			log.Info("dev stopped", logger.Duration("duration", result.Duration))

			return nil
		},
	}
}
