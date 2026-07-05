package cmd

import (
	"runtime"

	"github.com/vcnkl/rpm/cmd/subcmds"
	"github.com/vcnkl/rpm/version"

	"github.com/urfave/cli/v2"
)

func NewApp() *cli.App {
	return &cli.App{
		Name:    "rpm",
		Usage:   "Repo Manager v2 - Build orchestration for monorepos",
		Version: version.Version,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "debug",
				Aliases: []string{"d"},
				Usage:   "Enable debug logging",
			},
			&cli.StringFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Usage:   "Path to repo.yml (default: auto-detect via git root)",
			},
			&cli.IntFlag{
				Name:    "jobs",
				Aliases: []string{"j"},
				Value:   runtime.NumCPU(),
				Usage:   "Max parallel jobs",
			},
		},
		Commands: []*cli.Command{
			subcmds.InitCmd(),
			subcmds.BuildCmd(),
			subcmds.TestCmd(),
			subcmds.EnvCmd(),
			subcmds.RunCmd(),
			subcmds.GraphCmd(),
		},
	}
}
