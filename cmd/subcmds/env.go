package subcmds

import (
	envconfig "github.com/vcnkl/rpm/environments/config"

	"github.com/urfave/cli/v2"
)

func EnvCmd() *cli.Command {
	return &cli.Command{
		Name:  "env",
		Usage: "Manage repository environment blueprints",
		Subcommands: []*cli.Command{
			envCreateCmd(),
			envEditCmd(),
			envValidateCmd(),
			envRenderCmd(),
			envUpCmd(),
			envDownCmd(),
		},
	}
}

func envCreateCmd() *cli.Command {
	return &cli.Command{
		Name:      "create",
		Usage:     "Create an environment blueprint",
		ArgsUsage: "[blueprint]",
		Action: func(ctx *cli.Context) error {
			return envPlaceholder("create")
		},
	}
}

func envEditCmd() *cli.Command {
	return &cli.Command{
		Name:      "edit",
		Usage:     "Edit an environment blueprint",
		ArgsUsage: "<blueprint>",
		Action: func(ctx *cli.Context) error {
			if ctx.Args().Len() == 0 {
				return cli.Exit("error: blueprint argument required", 1)
			}
			return envPlaceholder("edit")
		},
	}
}

func envValidateCmd() *cli.Command {
	return &cli.Command{
		Name:      "validate",
		Usage:     "Validate an environment blueprint",
		ArgsUsage: "<blueprint>",
		Action: func(ctx *cli.Context) error {
			if ctx.Args().Len() == 0 {
				return cli.Exit("error: blueprint argument required", 1)
			}
			cfg := loadConfig(ctx)
			if _, err := envconfig.LoadBlueprint(cfg, ctx.Args().First()); err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}
			return nil
		},
	}
}

func envRenderCmd() *cli.Command {
	return &cli.Command{
		Name:      "render",
		Usage:     "Render an environment blueprint",
		ArgsUsage: "<blueprint>",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "out",
				Usage: "Write rendered Starlark to `path`",
			},
		},
		Action: func(ctx *cli.Context) error {
			if ctx.Args().Len() == 0 {
				return cli.Exit("error: blueprint argument required", 1)
			}
			return envPlaceholder("render")
		},
	}
}

func envUpCmd() *cli.Command {
	return &cli.Command{
		Name:      "up",
		Usage:     "Validate, render, and run an environment blueprint",
		ArgsUsage: "<blueprint>",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "non-interactive",
				Usage: "Run without interactive prompts",
			},
			&cli.BoolFlag{
				Name:  "no-reload",
				Usage: "Disable live reload",
			},
			&cli.BoolFlag{
				Name:  "no-deps",
				Usage: "Do not start environment dependencies",
			},
			&cli.BoolFlag{
				Name:  "render-only",
				Usage: "Render Starlark and exit without running",
			},
		},
		Action: func(ctx *cli.Context) error {
			if ctx.Args().Len() == 0 {
				return cli.Exit("error: blueprint argument required", 1)
			}
			return envPlaceholder("up")
		},
	}
}

func envDownCmd() *cli.Command {
	return &cli.Command{
		Name:      "down",
		Usage:     "Stop a running environment blueprint",
		ArgsUsage: "<blueprint>",
		Action: func(ctx *cli.Context) error {
			if ctx.Args().Len() == 0 {
				return cli.Exit("error: blueprint argument required", 1)
			}
			return envPlaceholder("down")
		},
	}
}

func envPlaceholder(action string) error {
	return cli.Exit("error: env "+action+" is not implemented until the environment runtime is added", 1)
}
