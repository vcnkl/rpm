package subcmds

import (
	"fmt"
	"strings"

	"github.com/vcnkl/rpm/actions"
	envconfig "github.com/vcnkl/rpm/environments/config"
	envcreate "github.com/vcnkl/rpm/environments/create"
	"github.com/vcnkl/rpm/environments/generator"
	"github.com/vcnkl/rpm/environments/spec"

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
			name, trailingStrings, trailingBools, err := parseTrailingFlags(ctx.Args().Slice())
			if err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}
			cfg := loadConfig(ctx)
			reload := !ctx.Bool("no-reload") && !trailingBools["no-reload"]
			if ctx.Bool("reload") || trailingBools["reload"] {
				reload = true
			}
			targets := append(ctx.StringSlice("target"), trailingStrings["target"]...)
			before := append(ctx.StringSlice("before"), trailingStrings["before"]...)
			targetReload := append(ctx.StringSlice("target-reload"), trailingStrings["target-reload"]...)
			targetReloadValues, err := parseBoolAssignments(targetReload)
			if err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}
			err = envcreate.RunCreate(cfg, envcreate.CreateOptions{
				Name:           name,
				Targets:        targets,
				Before:         before,
				Dependencies:   ctx.Bool("deps") || trailingBools["deps"],
				ReloadEnabled:  reload,
				TargetReload:   targetReloadValues,
				NonInteractive: ctx.Bool("non-interactive") || trailingBools["non-interactive"],
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
			name, trailingStrings, trailingBools, err := parseTrailingFlags(ctx.Args().Slice())
			if err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}
			if name == "" {
				return cli.Exit("error: blueprint argument required", 1)
			}
			cfg := loadConfig(ctx)
			var deps *bool
			if ctx.Bool("deps") || ctx.Bool("no-deps") || trailingBools["deps"] || trailingBools["no-deps"] {
				value := (ctx.Bool("deps") || trailingBools["deps"]) && !ctx.Bool("no-deps") && !trailingBools["no-deps"]
				deps = &value
			}
			var reload *bool
			if ctx.Bool("reload") || ctx.Bool("no-reload") || trailingBools["reload"] || trailingBools["no-reload"] {
				value := (ctx.Bool("reload") || trailingBools["reload"]) && !ctx.Bool("no-reload") && !trailingBools["no-reload"]
				reload = &value
			}
			targetReload := append(ctx.StringSlice("target-reload"), trailingStrings["target-reload"]...)
			targetReloadValues, err := parseBoolAssignments(targetReload)
			if err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}
			err = envcreate.RunEdit(cfg, envcreate.EditOptions{
				Name:           name,
				AddTargets:     append(ctx.StringSlice("add-target"), trailingStrings["add-target"]...),
				RemoveTargets:  append(ctx.StringSlice("remove-target"), trailingStrings["remove-target"]...),
				AddBefore:      append(ctx.StringSlice("add-before"), trailingStrings["add-before"]...),
				RemoveBefore:   append(ctx.StringSlice("remove-before"), trailingStrings["remove-before"]...),
				Dependencies:   deps,
				ReloadEnabled:  reload,
				TargetReload:   targetReloadValues,
				IncludeDeps:    append(ctx.StringSlice("include-dep"), trailingStrings["include-dep"]...),
				ExcludeDeps:    append(ctx.StringSlice("exclude-dep"), trailingStrings["exclude-dep"]...),
				NonInteractive: ctx.Bool("non-interactive") || trailingBools["non-interactive"],
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
			name, trailingStrings, _, err := parseTrailingFlags(ctx.Args().Slice())
			if err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}
			if name == "" {
				return cli.Exit("error: blueprint argument required", 1)
			}
			out := ctx.String("out")
			if out == "" && len(trailingStrings["out"]) > 0 {
				out = trailingStrings["out"][len(trailingStrings["out"])-1]
			}
			path, err := renderEnvironment(ctx, name, out, renderOptions{})
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
			name, _, trailingBools, err := parseTrailingFlags(ctx.Args().Slice())
			if err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}
			if name == "" {
				return cli.Exit("error: blueprint argument required", 1)
			}
			noReload := ctx.Bool("no-reload") || trailingBools["no-reload"]
			noDeps := ctx.Bool("no-deps") || trailingBools["no-deps"]
			cfg := loadConfig(ctx)
			action := actions.NewEnvAction(cfg, ctx.App.Writer, ctx.App.ErrWriter)
			if err := action.Up(ctx.Context, actions.EnvUpOptions{
				Blueprint:      name,
				NoReload:       noReload,
				NoDeps:         noDeps,
				NonInteractive: ctx.Bool("non-interactive") || trailingBools["non-interactive"],
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
			if ctx.Args().Len() == 0 {
				return cli.Exit("error: blueprint argument required", 1)
			}
			cfg := loadConfig(ctx)
			action := actions.NewEnvAction(cfg, ctx.App.Writer, ctx.App.ErrWriter)
			if err := action.Down(ctx.Context, actions.EnvDownOptions{Blueprint: ctx.Args().First()}); err != nil {
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
			if ctx.Args().Len() == 0 {
				return cli.Exit("error: blueprint argument required", 1)
			}
			cfg := loadConfig(ctx)
			action := actions.NewEnvAction(cfg, ctx.App.Writer, ctx.App.ErrWriter)
			if err := action.Prune(ctx.Context, actions.EnvPruneOptions{Blueprint: ctx.Args().First()}); err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}
			return nil
		},
	}
}

type renderOptions struct {
	NoReload bool
}

func renderEnvironment(ctx *cli.Context, name string, out string, opts renderOptions) (string, error) {
	cfg := loadConfig(ctx)
	blueprint, err := envconfig.LoadBlueprint(cfg, name)
	if err != nil {
		return "", err
	}
	blueprint = spec.BlueprintWithRuntimeOptions(blueprint, spec.RuntimeOptions{
		NoReload: opts.NoReload,
	})
	resolved, err := spec.Resolve(cfg, blueprint)
	if err != nil {
		return "", err
	}
	return generator.Write(cfg, resolved, out)
}

func parseBoolAssignments(values []string) (map[string]bool, error) {
	result := make(map[string]bool)
	for _, value := range values {
		ref, raw, ok := strings.Cut(value, "=")
		if !ok {
			return nil, fmt.Errorf("invalid boolean assignment %q (expected ref=true|false)", value)
		}
		if ref == "" {
			return nil, fmt.Errorf("invalid boolean assignment %q (missing ref)", value)
		}
		switch raw {
		case "true", "1", "yes":
			result[ref] = true
		case "false", "0", "no":
			result[ref] = false
		default:
			return nil, fmt.Errorf("invalid boolean value %q for %s", raw, ref)
		}
	}
	return result, nil
}

func parseTrailingFlags(args []string) (string, map[string][]string, map[string]bool, error) {
	values := make(map[string][]string)
	bools := make(map[string]bool)
	name := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			if name == "" {
				name = arg
			}
			continue
		}
		flag := strings.TrimPrefix(arg, "--")
		if key, value, ok := strings.Cut(flag, "="); ok {
			values[key] = append(values[key], value)
			continue
		}
		switch flag {
		case "target", "before", "target-reload", "add-target", "remove-target", "add-before", "remove-before", "include-dep", "exclude-dep", "out":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				return "", nil, nil, fmt.Errorf("--%s requires a value", flag)
			}
			i++
			values[flag] = append(values[flag], args[i])
		default:
			bools[flag] = true
		}
	}
	return name, values, bools, nil
}
