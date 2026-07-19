package subcmds

import (
	"fmt"
	"os"
	"strings"

	"github.com/vcnkl/rpm/config"
	"github.com/vcnkl/rpm/dag"
	envconfig "github.com/vcnkl/rpm/environments/config"
	"github.com/vcnkl/rpm/environments/generator"
	"github.com/vcnkl/rpm/models"

	"github.com/pkg/errors"
	"github.com/urfave/cli/v2"
)

type ValidationError []error

func (e ValidationError) Error() string {
	lines := make([]string, 0, len(e))
	for _, err := range e {
		lines = append(lines, "error: "+err.Error())
	}
	return strings.Join(lines, "\n")
}

type ValidationResult struct {
	failures []error
}

func (r ValidationResult) ok() bool {
	return len(r.failures) == 0
}

func (r ValidationResult) errors() []error {
	return append([]error(nil), r.failures...)
}

type cmdValidator struct {
	ValidationResult
	ctx             *cli.Context
	cfg             *config.Config
	graph           *dag.Graph
	args            []string
	argument        string
	trailingStrings map[string][]string
	trailingBools   map[string]bool
	boolAssignments map[string]bool
	blueprint       *models.EnvironmentBlueprint
	targetIds       []string
	format          string
}

func newCmdValidator(ctx *cli.Context) cmdValidator {
	return cmdValidator{
		ctx:             ctx,
		args:            ctx.Args().Slice(),
		trailingStrings: make(map[string][]string),
		trailingBools:   make(map[string]bool),
		boolAssignments: make(map[string]bool),
	}
}

func (v cmdValidator) validate() ValidationResult {
	return v.ValidationResult
}

func (v cmdValidator) useFirstArgument() cmdValidator {
	if len(v.args) > 1 {
		v.args = v.args[:1]
	}
	return v
}

func (v cmdValidator) requireArgument(name string) cmdValidator {
	if v.argument == "" && len(v.args) > 0 && !strings.HasPrefix(v.args[0], "--") {
		v.argument = v.args[0]
	}
	if v.argument == "" {
		v.failures = append(v.failures, fmt.Errorf("%s argument required", name))
	}
	return v
}

func (v cmdValidator) parseTrailingFlags() cmdValidator {
	argument, values, bools, err := trailingFlags(v.args)
	v.argument = argument
	v.trailingStrings = values
	v.trailingBools = bools
	if err != nil {
		v.failures = append(v.failures, err)
		return v
	}
	return v
}

func (v cmdValidator) parseBoolAssignments(values []string) cmdValidator {
	assignments, failures := booleanAssignments(values)
	v.boolAssignments = assignments
	v.failures = append(v.failures, failures...)
	return v
}

func (v cmdValidator) loadConfig() (result cmdValidator) {
	result = v
	defer func() {
		if recovered := recover(); recovered != nil {
			result.cfg = nil
			result.failures = append(result.failures, recoveredError(recovered))
		}
	}()
	result.cfg = loadConfig(v.ctx)
	return result
}

func (v cmdValidator) resolveGraph() cmdValidator {
	if v.cfg == nil {
		return v
	}
	graph := dag.NewGraph()
	for _, bundle := range v.cfg.Bundles() {
		for _, target := range bundle.Targets {
			graph.AddTarget(target)
		}
	}
	if err := graph.Resolve(v.cfg.Bundles()); err != nil {
		v.failures = append(v.failures, err)
		return v
	}
	v.graph = graph
	return v
}

func (v cmdValidator) allowFormat(allowed ...string) cmdValidator {
	v.format = v.ctx.String("format")
	for _, format := range allowed {
		if v.format == format {
			return v
		}
	}
	v.failures = append(v.failures, fmt.Errorf("invalid output format %q (expected %s)", v.format, strings.Join(allowed, ", ")))
	return v
}

func (v cmdValidator) loadBlueprint() cmdValidator {
	if v.cfg == nil || v.argument == "" {
		return v
	}
	blueprint, err := envconfig.LoadBlueprint(v.cfg, v.argument)
	if err != nil {
		v.failures = append(v.failures, err)
		return v
	}
	v.blueprint = blueprint
	return v
}

func (v cmdValidator) requireGeneratedEnvironment() cmdValidator {
	if v.cfg == nil || v.argument == "" {
		return v
	}
	path := generator.CachePath(v.cfg, v.argument)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			v.failures = append(v.failures, errors.Wrapf(err, "generated Starlark not found at %s; run `rpm env render %s`", path, v.argument))
			return v
		}
		v.failures = append(v.failures, errors.Wrapf(err, "failed to stat generated Starlark %s", path))
	}
	return v
}

func (v cmdValidator) resolveTargetRefs(suffixes ...string) cmdValidator {
	if v.cfg == nil || v.graph == nil {
		return v
	}
	refs := v.args
	if v.argument != "" && len(refs) == 0 {
		refs = []string{v.argument}
	}
	if len(refs) == 0 {
		v.targetIds = nil
		return v
	}
	invalidBundles := make(map[int]bool)
	for i, ref := range refs {
		bundleName, _, qualified := strings.Cut(ref, ":")
		if !qualified {
			continue
		}
		if _, ok := v.cfg.Bundles()[bundleName]; !ok {
			v.failures = append(v.failures, fmt.Errorf("bundle %q not found in %s", bundleName, v.cfg.Repo().Project.Name))
			invalidBundles[i] = true
		}
	}
	exact := len(suffixes) == 0
	selector := dag.NewSelector(v.graph, v.cfg.RepoRoot())
	seen := make(map[string]bool)
	for i, ref := range refs {
		if invalidBundles[i] {
			continue
		}
		if exact {
			if _, ok := v.graph.Nodes[ref]; !ok {
				v.failures = append(v.failures, fmt.Errorf("target not found: %s", ref))
				continue
			}
			if !seen[ref] {
				seen[ref] = true
				v.targetIds = append(v.targetIds, ref)
			}
			continue
		}
		resolved := resolveRef(selector, v.graph, ref, suffixes)
		if len(resolved) == 0 {
			v.failures = append(v.failures, fmt.Errorf("target not found: %s", ref))
			continue
		}
		for _, id := range resolved {
			if seen[id] {
				continue
			}
			seen[id] = true
			v.targetIds = append(v.targetIds, id)
		}
	}
	return v
}

func trailingFlags(args []string) (string, map[string][]string, map[string]bool, error) {
	values := make(map[string][]string)
	bools := make(map[string]bool)
	argument := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			if argument == "" {
				argument = arg
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
				return argument, values, bools, fmt.Errorf("--%s requires a value", flag)
			}
			i++
			values[flag] = append(values[flag], args[i])
		default:
			bools[flag] = true
		}
	}
	return argument, values, bools, nil
}

func booleanAssignments(values []string) (map[string]bool, []error) {
	result := make(map[string]bool)
	var failures []error
	for _, value := range values {
		ref, raw, ok := strings.Cut(value, "=")
		if !ok {
			failures = append(failures, fmt.Errorf("invalid boolean assignment %q (expected ref=true|false)", value))
			continue
		}
		if ref == "" {
			failures = append(failures, fmt.Errorf("invalid boolean assignment %q (missing ref)", value))
			continue
		}
		switch raw {
		case "true", "1", "yes":
			result[ref] = true
		case "false", "0", "no":
			result[ref] = false
		default:
			failures = append(failures, fmt.Errorf("invalid boolean value %q for %s", raw, ref))
		}
	}
	return result, failures
}

func recoveredError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return err
	}
	return fmt.Errorf("%v", recovered)
}

func resolveRef(selector *dag.Selector, graph *dag.Graph, ref string, suffixes []string) []string {
	seen := make(map[string]bool)
	var resolved []string
	for _, suffix := range suffixes {
		for _, id := range selector.ResolveTargetRefs([]string{ref}, suffix) {
			if _, ok := graph.Nodes[id]; !ok || seen[id] {
				continue
			}
			seen[id] = true
			resolved = append(resolved, id)
		}
	}
	return resolved
}
