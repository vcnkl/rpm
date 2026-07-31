package subcmds

import (
	"fmt"

	"github.com/vcwx/rpm/actions"
	"github.com/vcwx/rpm/config"
	envcreate "github.com/vcwx/rpm/environments/create"
	"github.com/vcwx/rpm/environments/generator"
	"github.com/vcwx/rpm/environments/spec"
	"github.com/vcwx/rpm/models"

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
			envPruneCmd(),
		},
	}
}

func envCreateCmd() *cli.Command {
	return &cli.Command{
		Name:                   "create",
		Usage:                  "Create an environment blueprint",
		ArgsUsage:              "[blueprint]",
		UseShortOptionHandling: true,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "non-interactive",
				Usage: "Run without interactive prompts",
			},
			&cli.StringSliceFlag{
				Name:  "target",
				Usage: "Add target ref to the blueprint",
			},
			&cli.StringSliceFlag{
				Name:  "before",
				Usage: "Add target ref to run before environment startup",
			},
			&cli.BoolFlag{
				Name:  "deps",
				Usage: "Accepted for compatibility; dependencies are derived from selected bundles",
			},
			&cli.BoolFlag{
				Name:  "reload",
				Usage: "Enable blueprint-level live reload",
			},
			&cli.BoolFlag{
				Name:  "no-reload",
				Usage: "Disable blueprint-level live reload",
			},
			&cli.StringSliceFlag{
				Name:  "target-reload",
				Usage: "Set per-target reload override as `ref=true|false`",
			},
		},
		Action: func(ctx *cli.Context) error {
			validator := newCmdValidator(ctx).parseTrailingFlags().loadConfig()
			targetReload := append(ctx.StringSlice("target-reload"), validator.trailingStrings["target-reload"]...)
			validator = validator.parseBoolAssignments(targetReload)
			validation := validator.validate()
			if !validation.ok() {
				return cli.Exit(ValidationError(validation.errors()).Error(), 1)
			}

			reload := !ctx.Bool("no-reload") && !validator.trailingBools["no-reload"]
			if ctx.Bool("reload") || validator.trailingBools["reload"] {
				reload = true
			}
			err := envcreate.RunCreate(validator.cfg, envcreate.CreateOptions{
				Name:           validator.argument,
				Targets:        append(ctx.StringSlice("target"), validator.trailingStrings["target"]...),
				Before:         append(ctx.StringSlice("before"), validator.trailingStrings["before"]...),
				Dependencies:   ctx.Bool("deps") || validator.trailingBools["deps"],
				ReloadEnabled:  reload,
				TargetReload:   validator.boolAssignments,
				NonInteractive: ctx.Bool("non-interactive") || validator.trailingBools["non-interactive"],
			})
			if err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}
			return nil
		},
	}
}

func envEditCmd() *cli.Command {
	return &cli.Command{
		Name:                   "edit",
		Usage:                  "Edit an environment blueprint",
		ArgsUsage:              "<blueprint>",
		UseShortOptionHandling: true,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "non-interactive",
				Usage: "Run without interactive prompts",
			},
			&cli.StringSliceFlag{
				Name:  "add-target",
				Usage: "Add target ref to the blueprint",
			},
			&cli.StringSliceFlag{
				Name:  "remove-target",
				Usage: "Remove target ref from the blueprint",
			},
			&cli.StringSliceFlag{
				Name:  "add-before",
				Usage: "Add target ref to run before environment startup",
			},
			&cli.StringSliceFlag{
				Name:  "remove-before",
				Usage: "Remove target ref from environment startup before targets",
			},
			&cli.BoolFlag{
				Name:  "deps",
				Usage: "Accepted for compatibility; dependencies are derived from selected bundles",
			},
			&cli.BoolFlag{
				Name:  "no-deps",
				Usage: "Accepted for compatibility; dependencies are derived from selected bundles",
			},
			&cli.BoolFlag{
				Name:  "reload",
				Usage: "Enable blueprint-level live reload",
			},
			&cli.BoolFlag{
				Name:  "no-reload",
				Usage: "Disable blueprint-level live reload",
			},
			&cli.StringSliceFlag{
				Name:  "target-reload",
				Usage: "Set per-target reload override as `ref=true|false`",
			},
			&cli.StringSliceFlag{
				Name:  "include-dep",
				Usage: "Accepted for compatibility; dependencies are derived from selected bundles",
			},
			&cli.StringSliceFlag{
				Name:  "exclude-dep",
				Usage: "Accepted for compatibility; dependencies are derived from selected bundles",
			},
		},
		Action: func(ctx *cli.Context) error {
			validator := newCmdValidator(ctx).parseTrailingFlags().requireArgument("blueprint").loadConfig().loadBlueprint()
			targetReload := append(ctx.StringSlice("target-reload"), validator.trailingStrings["target-reload"]...)
			validator = validator.parseBoolAssignments(targetReload)
			validation := validator.validate()
			if !validation.ok() {
				return cli.Exit(ValidationError(validation.errors()).Error(), 1)
			}

			var deps *bool
			if ctx.Bool("deps") || ctx.Bool("no-deps") || validator.trailingBools["deps"] || validator.trailingBools["no-deps"] {
				value := (ctx.Bool("deps") || validator.trailingBools["deps"]) && !ctx.Bool("no-deps") && !validator.trailingBools["no-deps"]
				deps = &value
			}
			var reload *bool
			if ctx.Bool("reload") || ctx.Bool("no-reload") || validator.trailingBools["reload"] || validator.trailingBools["no-reload"] {
				value := (ctx.Bool("reload") || validator.trailingBools["reload"]) && !ctx.Bool("no-reload") && !validator.trailingBools["no-reload"]
				reload = &value
			}
			err := envcreate.RunEdit(validator.cfg, envcreate.EditOptions{
				Name:           validator.argument,
				AddTargets:     append(ctx.StringSlice("add-target"), validator.trailingStrings["add-target"]...),
				RemoveTargets:  append(ctx.StringSlice("remove-target"), validator.trailingStrings["remove-target"]...),
				AddBefore:      append(ctx.StringSlice("add-before"), validator.trailingStrings["add-before"]...),
				RemoveBefore:   append(ctx.StringSlice("remove-before"), validator.trailingStrings["remove-before"]...),
				Dependencies:   deps,
				ReloadEnabled:  reload,
				TargetReload:   validator.boolAssignments,
				IncludeDeps:    append(ctx.StringSlice("include-dep"), validator.trailingStrings["include-dep"]...),
				ExcludeDeps:    append(ctx.StringSlice("exclude-dep"), validator.trailingStrings["exclude-dep"]...),
				NonInteractive: ctx.Bool("non-interactive") || validator.trailingBools["non-interactive"],
			})
			if err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}
			return nil
		},
	}
}

func envValidateCmd() *cli.Command {
	return &cli.Command{
		Name:      "validate",
		Usage:     "Validate an environment blueprint",
		ArgsUsage: "<blueprint>",
		Action: func(ctx *cli.Context) error {
			validator := newCmdValidator(ctx).requireArgument("blueprint").loadConfig().loadBlueprint()
			validation := validator.validate()
			if !validation.ok() {
				return cli.Exit(ValidationError(validation.errors()).Error(), 1)
			}
			return nil
		},
	}
}

func envRenderCmd() *cli.Command {
	return &cli.Command{
		Name:                   "render",
		Usage:                  "Render an environment blueprint",
		ArgsUsage:              "<blueprint>",
		UseShortOptionHandling: true,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "out",
				Usage: "Write rendered Starlark to `path`",
			},
		},
		Action: func(ctx *cli.Context) error {
			validator := newCmdValidator(ctx).parseTrailingFlags().requireArgument("blueprint").loadConfig().loadBlueprint()
			validation := validator.validate()
			if !validation.ok() {
				return cli.Exit(ValidationError(validation.errors()).Error(), 1)
			}

			out := ctx.String("out")
			if out == "" && len(validator.trailingStrings["out"]) > 0 {
				out = validator.trailingStrings["out"][len(validator.trailingStrings["out"])-1]
			}
			path, err := renderEnvironment(validator.cfg, validator.blueprint, out, renderOptions{})
			if err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}
			_, _ = fmt.Fprintln(ctx.App.Writer, path)
			return nil
		},
	}
}

func envUpCmd() *cli.Command {
	return &cli.Command{
		Name:                   "up",
		Usage:                  "Run an environment blueprint from generated Starlark",
		ArgsUsage:              "<blueprint>",
		UseShortOptionHandling: true,
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
		},
		Action: func(ctx *cli.Context) error {
			validator := newCmdValidator(ctx).parseTrailingFlags().requireArgument("blueprint").loadConfig().requireGeneratedEnvironment()
			validation := validator.validate()
			if !validation.ok() {
				return cli.Exit(ValidationError(validation.errors()).Error(), 1)
			}

			noReload := ctx.Bool("no-reload") || validator.trailingBools["no-reload"]
			noDeps := ctx.Bool("no-deps") || validator.trailingBools["no-deps"]
			cfg := validator.cfg
			logFile, err := openLogFile(ctx, cfg, cfg.Repo().Logs.Env.Out, validator.argument)
			if err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}
			if logFile != nil {
				defer closeLogFile(logFile)
			}
			action := actions.NewEnvAction(cfg, ctx.App.Writer, ctx.App.ErrWriter)
			if err := action.Up(ctx.Context, actions.EnvUpOptions{
				Blueprint:      validator.argument,
				NoReload:       noReload,
				NoDeps:         noDeps,
				NonInteractive: ctx.Bool("non-interactive") || validator.trailingBools["non-interactive"],
				LogDestination: logFile,
			}); err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}
			return nil
		},
	}
}

func envDownCmd() *cli.Command {
	return &cli.Command{
		Name:      "down",
		Usage:     "Stop a running environment blueprint",
		ArgsUsage: "<blueprint>",
		Action: func(ctx *cli.Context) error {
			validator := newCmdValidator(ctx).requireArgument("blueprint").loadConfig().requireGeneratedEnvironment()
			validation := validator.validate()
			if !validation.ok() {
				return cli.Exit(ValidationError(validation.errors()).Error(), 1)
			}

			action := actions.NewEnvAction(validator.cfg, ctx.App.Writer, ctx.App.ErrWriter)
			if err := action.Down(ctx.Context, actions.EnvDownOptions{Blueprint: validator.argument}); err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}
			return nil
		},
	}
}

func envPruneCmd() *cli.Command {
	return &cli.Command{
		Name:      "prune",
		Usage:     "Reset cached runtime resources for an environment blueprint",
		ArgsUsage: "<blueprint>",
		Action: func(ctx *cli.Context) error {
			validator := newCmdValidator(ctx).requireArgument("blueprint").loadConfig()
			validation := validator.validate()
			if !validation.ok() {
				return cli.Exit(ValidationError(validation.errors()).Error(), 1)
			}

			action := actions.NewEnvAction(validator.cfg, ctx.App.Writer, ctx.App.ErrWriter)
			if err := action.Prune(ctx.Context, actions.EnvPruneOptions{Blueprint: validator.argument}); err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}
			return nil
		},
	}
}

type renderOptions struct {
	NoReload bool
}

func renderEnvironment(cfg *config.Config, blueprint *models.EnvironmentBlueprint, out string, opts renderOptions) (string, error) {
	blueprint = spec.BlueprintWithRuntimeOptions(blueprint, spec.RuntimeOptions{
		NoReload: opts.NoReload,
	})
	resolved, err := spec.Resolve(cfg, blueprint)
	if err != nil {
		return "", err
	}
	return generator.Write(cfg, resolved, out)
}
