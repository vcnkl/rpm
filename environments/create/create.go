package create

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	rootconfig "github.com/vcwx/rpm/config"
	envconfig "github.com/vcwx/rpm/environments/config"
	"github.com/vcwx/rpm/environments/generator"
	envspec "github.com/vcwx/rpm/environments/spec"
	envtui "github.com/vcwx/rpm/environments/tui"
	"github.com/vcwx/rpm/models"

	"github.com/pkg/errors"
)

var (
	ErrMissingBlueprintName = errors.New("missing blueprint name")
	ErrMissingTargets       = errors.New("missing blueprint targets")
	ErrMissingEditChange    = errors.New("missing blueprint edit change")
)

type CreateOptions struct {
	Name           string
	Targets        []string
	Before         []string
	Dependencies   bool
	ReloadEnabled  bool
	TargetReload   map[string]bool
	NonInteractive bool
	In             io.Reader
	Out            io.Writer
}

type EditOptions struct {
	Name           string
	AddTargets     []string
	RemoveTargets  []string
	AddBefore      []string
	RemoveBefore   []string
	Dependencies   *bool
	ReloadEnabled  *bool
	TargetReload   map[string]bool
	IncludeDeps    []string
	ExcludeDeps    []string
	NonInteractive bool
	In             io.Reader
	Out            io.Writer
}

func RunCreate(repo *rootconfig.Config, opts CreateOptions) error {
	if opts.NonInteractive {
		return createNonInteractive(repo, opts)
	}
	return createInteractive(repo, opts)
}

func RunEdit(repo *rootconfig.Config, opts EditOptions) error {
	if opts.NonInteractive {
		return editNonInteractive(repo, opts)
	}
	return editInteractive(repo, opts)
}

func createNonInteractive(repo *rootconfig.Config, opts CreateOptions) error {
	if opts.Name == "" {
		return ErrMissingBlueprintName
	}
	if len(opts.Targets) == 0 {
		return ErrMissingTargets
	}
	blueprint, err := buildBlueprint(repo, opts.Name, opts.Targets, opts.Before, opts.ReloadEnabled, opts.TargetReload)
	if err != nil {
		return err
	}
	return writeBlueprintAndGenerated(repo, blueprint)
}

func editNonInteractive(repo *rootconfig.Config, opts EditOptions) error {
	if opts.Name == "" {
		return ErrMissingBlueprintName
	}
	if len(opts.AddTargets) == 0 && len(opts.RemoveTargets) == 0 && len(opts.AddBefore) == 0 && len(opts.RemoveBefore) == 0 && opts.Dependencies == nil && opts.ReloadEnabled == nil && len(opts.TargetReload) == 0 && len(opts.IncludeDeps) == 0 && len(opts.ExcludeDeps) == 0 {
		return ErrMissingEditChange
	}

	blueprint, err := envconfig.LoadBlueprint(repo, opts.Name)
	if err != nil {
		return err
	}
	targets := targetMap(blueprint.Targets)
	for _, ref := range opts.AddTargets {
		if err := validateTargetRef(repo, ref); err != nil {
			return err
		}
		targets[ref] = models.EnvironmentTarget{Ref: ref, Env: map[string]string{}}
	}
	for _, ref := range opts.RemoveTargets {
		delete(targets, ref)
	}
	before := stringSet(blueprint.Before)
	for _, ref := range opts.AddBefore {
		if err := validateTargetRef(repo, ref); err != nil {
			return err
		}
		before[ref] = true
	}
	for _, ref := range opts.RemoveBefore {
		delete(before, ref)
	}
	if opts.ReloadEnabled != nil {
		blueprint.ReloadPolicy.Enabled = *opts.ReloadEnabled
	}
	for ref, reload := range opts.TargetReload {
		target, ok := targets[ref]
		if !ok {
			return errors.Wrapf(envconfig.ErrUnknownBlueprintRef, "%s", ref)
		}
		value := reload
		target.Reload = &value
		targets[ref] = target
	}
	blueprint.Targets = targetSlice(targets)
	blueprint.Before = beforeSlice(before)
	if len(blueprint.Targets) == 0 {
		return ErrMissingTargets
	}
	if err := validateBeforeRefs(repo, targetRefs(targets), blueprint.Before); err != nil {
		return err
	}
	blueprint.DependencyPolicy = requiredDependencyPolicy(repo, append(targetRefs(targets), blueprint.Before...))
	return writeBlueprintAndGenerated(repo, blueprint)
}

func createInteractive(repo *rootconfig.Config, opts CreateOptions) error {
	in := opts.In
	if in == nil {
		in = os.Stdin
	}
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	prompt := newPrompt(in, out)
	name := opts.Name
	var err error
	if name == "" {
		name, err = prompt.ask("Blueprint name")
		if err != nil {
			return err
		}
	}
	if strings.TrimSpace(name) == "" {
		return ErrMissingBlueprintName
	}
	targets, err := prompt.chooseTargets(repo.QueryTargets(environmentMainTarget), nil)
	if err != nil {
		return err
	}
	before, err := prompt.chooseBeforeTargets(repo.QueryTargets(environmentBeforeTarget), targets, opts.Before)
	if err != nil {
		return err
	}
	disableReload, err := prompt.askBool("Disable live reload", false)
	if err != nil {
		return err
	}
	reload := !disableReload
	blueprint, err := buildBlueprint(repo, name, targets, before, reload, nil)
	if err != nil {
		return err
	}
	return writeBlueprintAndGenerated(repo, blueprint)
}

func editInteractive(repo *rootconfig.Config, opts EditOptions) error {
	blueprint, err := envconfig.LoadBlueprint(repo, opts.Name)
	if err != nil {
		return err
	}
	in := opts.In
	if in == nil {
		in = os.Stdin
	}
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	prompt := newPrompt(in, out)
	existingTargets := targetMap(blueprint.Targets)
	targets, err := prompt.chooseTargets(repo.QueryTargets(environmentMainTarget), targetRefs(existingTargets))
	if err != nil {
		return err
	}
	before, err := prompt.chooseBeforeTargets(repo.QueryTargets(environmentBeforeTarget), targets, blueprint.Before)
	if err != nil {
		return err
	}
	disableReload, err := prompt.askBool("Disable live reload", !blueprint.ReloadPolicy.Enabled)
	if err != nil {
		return err
	}
	reload := !disableReload
	next, err := buildBlueprint(repo, blueprint.Name, targets, before, reload, nil)
	if err != nil {
		return err
	}
	for i := range next.Targets {
		if existing, ok := existingTargets[next.Targets[i].Ref]; ok {
			next.Targets[i].Env = existing.Env
		}
	}
	next.Variables = blueprint.Variables
	return writeBlueprintAndGenerated(repo, next)
}

func writeBlueprintAndGenerated(repo *rootconfig.Config, blueprint *models.EnvironmentBlueprint) error {
	if err := envconfig.WriteBlueprint(repo, blueprint); err != nil {
		return err
	}
	resolved, err := envspec.Resolve(repo, blueprint)
	if err != nil {
		return err
	}
	_, err = generator.Write(repo, resolved, "")
	return err
}

func buildBlueprint(repo *rootconfig.Config, name string, targetRefs []string, beforeRefs []string, reload bool, targetReload map[string]bool) (*models.EnvironmentBlueprint, error) {
	seen := make(map[string]bool)
	targets := make([]models.EnvironmentTarget, 0, len(targetRefs))
	for _, ref := range targetRefs {
		if seen[ref] {
			continue
		}
		if err := validateTargetRef(repo, ref); err != nil {
			return nil, err
		}
		seen[ref] = true
		target := models.EnvironmentTarget{Ref: ref, Env: map[string]string{}}
		if targetReload != nil {
			if value, ok := targetReload[ref]; ok {
				reloadValue := value
				target.Reload = &reloadValue
			}
		}
		targets = append(targets, target)
	}
	for ref := range targetReload {
		if !seen[ref] {
			return nil, errors.Wrapf(envconfig.ErrUnknownBlueprintRef, "%s", ref)
		}
	}
	if len(targets) == 0 {
		return nil, ErrMissingTargets
	}
	before, err := normalizeBeforeRefs(repo, targetRefs, beforeRefs)
	if err != nil {
		return nil, err
	}
	return &models.EnvironmentBlueprint{
		Version:          1,
		Name:             name,
		Variables:        map[string]string{},
		Before:           before,
		Targets:          targets,
		DependencyPolicy: requiredDependencyPolicy(repo, append(append([]string{}, targetRefs...), before...)),
		ReloadPolicy: models.ReloadPolicy{
			Enabled:  reload,
			Debounce: "100ms",
		},
	}, nil
}

func environmentMainTarget(target *models.Target) bool {
	return environmentBeforeTarget(target) &&
		(strings.HasSuffix(target.Name, "_dev") || strings.HasSuffix(target.Name, "_serve"))
}

func environmentBeforeTarget(target *models.Target) bool {
	name := target.Name
	return name != "build" &&
		name != "test" &&
		!strings.HasSuffix(name, "_build") &&
		!strings.HasSuffix(name, "_test")
}

func normalizeBeforeRefs(repo *rootconfig.Config, targetRefs []string, beforeRefs []string) ([]string, error) {
	seen := make(map[string]bool)
	before := make([]string, 0, len(beforeRefs))
	for _, ref := range beforeRefs {
		if seen[ref] {
			return nil, errors.Wrapf(envconfig.ErrDuplicateBlueprintRef, "%s", ref)
		}
		if err := validateTargetRef(repo, ref); err != nil {
			return nil, err
		}
		seen[ref] = true
		before = append(before, ref)
	}
	if err := validateBeforeRefs(repo, targetRefs, before); err != nil {
		return nil, err
	}
	sort.Strings(before)
	return before, nil
}

func validateBeforeRefs(repo *rootconfig.Config, targetRefs []string, beforeRefs []string) error {
	targetSet := stringSet(targetRefs)
	seen := make(map[string]bool)
	for _, ref := range beforeRefs {
		if targetSet[ref] {
			return errors.Wrapf(envconfig.ErrDuplicateBlueprintRef, "%s is also listed in targets", ref)
		}
		if seen[ref] {
			return errors.Wrapf(envconfig.ErrDuplicateBlueprintRef, "%s", ref)
		}
		seen[ref] = true
		if err := validateTargetRef(repo, ref); err != nil {
			return err
		}
	}
	return nil
}

func validateTargetRef(repo *rootconfig.Config, ref string) error {
	if _, err := repo.ResolveTarget(ref); err != nil {
		return errors.Wrapf(envconfig.ErrUnknownBlueprintRef, "%s", ref)
	}
	return nil
}

func requiredDependencyPolicy(repo *rootconfig.Config, targetRefs []string) models.DependencyPolicy {
	refs := requiredDependencyRefs(repo, targetRefs)
	return models.DependencyPolicy{
		Enabled: len(refs) > 0,
		Include: refs,
		Exclude: []string{},
	}
}

func requiredDependencyRefs(repo *rootconfig.Config, targetRefs []string) []string {
	bundles := make(map[string]bool)
	for _, ref := range targetRefs {
		bundle, _, ok := strings.Cut(ref, ":")
		if ok {
			bundles[bundle] = true
		}
	}
	seen := make(map[string]bool)
	var refs []string
	for name, bundle := range repo.Bundles() {
		if !bundles[name] {
			continue
		}
		for _, dep := range bundle.Dependencies {
			if !seen[dep] {
				seen[dep] = true
				refs = append(refs, dep)
			}
		}
	}
	sort.Strings(refs)
	return refs
}

func targetMap(targets []models.EnvironmentTarget) map[string]models.EnvironmentTarget {
	result := make(map[string]models.EnvironmentTarget, len(targets))
	for _, target := range targets {
		result[target.Ref] = target
	}
	return result
}

func targetSlice(targets map[string]models.EnvironmentTarget) []models.EnvironmentTarget {
	result := make([]models.EnvironmentTarget, 0, len(targets))
	for _, target := range targets {
		result = append(result, target)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Ref < result[j].Ref
	})
	return result
}

func targetRefs(targets map[string]models.EnvironmentTarget) []string {
	refs := make([]string, 0, len(targets))
	for ref := range targets {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs
}

func beforeSlice(refs map[string]bool) []string {
	result := make([]string, 0, len(refs))
	for ref := range refs {
		result = append(result, ref)
	}
	sort.Strings(result)
	return result
}

type prompt struct {
	in      io.Reader
	scanner *bufio.Scanner
	out     io.Writer
}

func newPrompt(in io.Reader, out io.Writer) prompt {
	return prompt{in: in, scanner: bufio.NewScanner(in), out: out}
}

func (p prompt) ask(label string) (string, error) {
	_, _ = fmt.Fprintf(p.out, "%s: ", label)
	if !p.scanner.Scan() {
		return "", p.scanner.Err()
	}
	return strings.TrimSpace(p.scanner.Text()), nil
}

func (p prompt) askDefault(label string, fallback string) (string, error) {
	promptLabel := label
	if fallback != "" {
		promptLabel += " [" + fallback + "]"
	}
	value, err := p.ask(promptLabel)
	if err != nil {
		return "", err
	}
	if value == "" {
		return fallback, nil
	}
	return value, nil
}

func (p prompt) askBool(label string, fallback bool) (bool, error) {
	suffix := "y/N"
	if fallback {
		suffix = "Y/n"
	}
	value, err := p.ask(label + " [" + suffix + "]")
	if err != nil {
		return false, err
	}
	if value == "" {
		return fallback, nil
	}
	switch strings.ToLower(value) {
	case "y", "yes", "true", "1":
		return true, nil
	case "n", "no", "false", "0":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean answer %q", value)
	}
}

func (p prompt) chooseTargets(targets []*models.Target, selected []string) ([]string, error) {
	if envtui.CanSelect(p.in, p.out) {
		return envtui.Select(context.Background(), p.in, p.out, envtui.SelectionRequest{
			Title:      "Select environment targets",
			Items:      targetSelectItems(targets, selected),
			RequireOne: true,
		})
	}
	choices := runnableTargets(targets)
	for i, target := range choices {
		_, _ = fmt.Fprintf(p.out, "%d) %s\n", i+1, target.ID())
	}
	answer, err := p.ask("Targets (comma-separated numbers or refs)")
	if err != nil {
		return nil, err
	}
	if answer == "" {
		return nil, ErrMissingTargets
	}
	targetByRef := make(map[string]bool)
	for _, target := range targets {
		targetByRef[target.ID()] = true
	}
	var refs []string
	for _, item := range strings.Split(answer, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if index, err := strconv.Atoi(item); err == nil {
			if index < 1 || index > len(choices) {
				return nil, fmt.Errorf("target selection out of range: %d", index)
			}
			refs = append(refs, choices[index-1].ID())
			continue
		}
		if !targetByRef[item] {
			return nil, errors.Wrapf(envconfig.ErrUnknownBlueprintRef, "%s", item)
		}
		refs = append(refs, item)
	}
	return refs, nil
}

func (p prompt) chooseBeforeTargets(targets []*models.Target, mainRefs []string, selected []string) ([]string, error) {
	if envtui.CanSelect(p.in, p.out) {
		return envtui.Select(context.Background(), p.in, p.out, envtui.SelectionRequest{
			Title: "Select targets to run before startup",
			Items: beforeSelectItems(targets, mainRefs, selected),
		})
	}
	mainSet := stringSet(mainRefs)
	choices := make([]*models.Target, 0, len(targets))
	for _, target := range targets {
		if !mainSet[target.ID()] {
			choices = append(choices, target)
		}
	}
	sort.Slice(choices, func(i, j int) bool {
		return choices[i].ID() < choices[j].ID()
	})
	for i, target := range choices {
		_, _ = fmt.Fprintf(p.out, "%d) %s\n", i+1, target.ID())
	}
	answer, err := p.askDefault("Run before targets (comma-separated numbers or refs)", strings.Join(selected, ","))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(answer) == "" {
		return []string{}, nil
	}
	targetByRef := make(map[string]bool)
	for _, target := range choices {
		targetByRef[target.ID()] = true
	}
	var refs []string
	for _, item := range strings.Split(answer, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if index, err := strconv.Atoi(item); err == nil {
			if index < 1 || index > len(choices) {
				return nil, fmt.Errorf("before target selection out of range: %d", index)
			}
			refs = append(refs, choices[index-1].ID())
			continue
		}
		if !targetByRef[item] {
			return nil, errors.Wrapf(envconfig.ErrUnknownBlueprintRef, "%s", item)
		}
		refs = append(refs, item)
	}
	sort.Strings(refs)
	return refs, nil
}

func runnableTargets(targets []*models.Target) []*models.Target {
	choices := append([]*models.Target{}, targets...)
	sort.Slice(choices, func(i, j int) bool {
		return choices[i].ID() < choices[j].ID()
	})
	return choices
}
