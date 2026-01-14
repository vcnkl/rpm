package subcmds

import (
	"strings"

	"github.com/vcnkl/rpm/actions"
	"github.com/vcnkl/rpm/config"
	"github.com/vcnkl/rpm/dag"
	"github.com/vcnkl/rpm/git"
	"github.com/vcnkl/rpm/logger"

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
			debug := ctx.Bool("debug")
			affected := ctx.Bool("affected")
			parallel := ctx.Int("jobs")

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
				targetIDs = selector.ResolveTargetRefs(ctx.Args().Slice(), suffix)
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
