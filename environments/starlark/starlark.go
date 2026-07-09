package starlark

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/pkg/errors"
	gostarlark "go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

const stepBudget = 100000

type RuntimePlan struct {
	Environment   Environment
	Dependencies  []Dependency
	BeforeTargets []TargetProcess
	DepTargets    []TargetProcess
	Targets       []TargetProcess
	Watches       []Watch
	RunOrder      []string
}

type Environment struct {
	Name       string
	Variables  map[string]string
	LiveReload ReloadPolicy
}

type ReloadPolicy struct {
	Enabled  bool
	Debounce string
}

type Dependency struct {
	Ref          string
	ConfigPath   string
	Name         string
	Image        string
	Env          map[string]string
	Ports        []string
	Volumes      []string
	ReadinessCmd string
}

type TargetProcess struct {
	Ref          string
	ConfigPath   string
	Command      string
	WorkingDir   string
	ReadinessCmd string
	Env          map[string]string
	DotenvEnv    map[string]string
	DotenvFiles  []string
	Reload       bool
}

type Watch struct {
	Target  string
	Roots   []string
	Ignore  []string
	Reload  bool
	Enabled bool
}

func InterpretSource(ctx context.Context, blueprint string, filename string, src []byte) (*RuntimePlan, error) {
	return interpret(ctx, blueprint, filename, src)
}

func interpret(ctx context.Context, blueprint string, filename string, src []byte) (*RuntimePlan, error) {
	pb := &planBuilder{}
	thread := &gostarlark.Thread{Name: "rpm-env:" + blueprint}
	thread.SetLocal("plan", pb)
	thread.SetMaxExecutionSteps(stepBudget)

	cancelled := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			thread.Cancel(ctx.Err().Error())
		case <-cancelled:
		}
	}()
	defer close(cancelled)

	predeclared := gostarlark.StringDict{
		"rpm_environment":   gostarlark.NewBuiltin("rpm_environment", rpmEnvironment),
		"rpm_dependency":    gostarlark.NewBuiltin("rpm_dependency", rpmDependency),
		"rpm_before_target": gostarlark.NewBuiltin("rpm_before_target", rpmBeforeTarget),
		"rpm_dep_target":    gostarlark.NewBuiltin("rpm_dep_target", rpmDepTarget),
		"rpm_target":        gostarlark.NewBuiltin("rpm_target", rpmTarget),
		"rpm_watch":         gostarlark.NewBuiltin("rpm_watch", rpmWatch),
		"rpm_run":           gostarlark.NewBuiltin("rpm_run", rpmRun),
	}
	predeclared.Freeze()

	opts := &syntax.FileOptions{}
	if _, err := gostarlark.ExecFileOptions(opts, thread, filename, src, predeclared); err != nil {
		return nil, evalError(err)
	}
	return pb.plan(), nil
}

type planBuilder struct {
	environment   Environment
	dependencies  []Dependency
	beforeTargets []TargetProcess
	depTargets    []TargetProcess
	targets       []TargetProcess
	watches       []Watch
	runOrder      []string
}

func (b *planBuilder) plan() *RuntimePlan {
	dependencies := append([]Dependency{}, b.dependencies...)
	sort.Slice(dependencies, func(i, j int) bool { return dependencies[i].Ref < dependencies[j].Ref })
	targets := append([]TargetProcess{}, b.targets...)
	sort.Slice(targets, func(i, j int) bool { return targets[i].Ref < targets[j].Ref })
	watches := append([]Watch{}, b.watches...)
	sort.Slice(watches, func(i, j int) bool { return watches[i].Target < watches[j].Target })
	return &RuntimePlan{
		Environment:   b.environment,
		Dependencies:  dependencies,
		BeforeTargets: append([]TargetProcess{}, b.beforeTargets...),
		DepTargets:    append([]TargetProcess{}, b.depTargets...),
		Targets:       targets,
		Watches:       watches,
		RunOrder:      append([]string{}, b.runOrder...),
	}
}

func rpmEnvironment(thread *gostarlark.Thread, fn *gostarlark.Builtin, args gostarlark.Tuple, kwargs []gostarlark.Tuple) (gostarlark.Value, error) {
	var name string
	var liveReloadValue, variablesValue gostarlark.Value
	if err := unpackKwargs(fn.Name(), args, kwargs, "name", &name, "live_reload", &liveReloadValue, "variables", &variablesValue); err != nil {
		return nil, err
	}
	liveReload, err := reloadPolicy(liveReloadValue)
	if err != nil {
		return nil, err
	}
	variables, err := stringDict(variablesValue)
	if err != nil {
		return nil, err
	}
	builder(thread).environment = Environment{Name: name, LiveReload: liveReload, Variables: variables}
	return gostarlark.None, nil
}

func rpmDependency(thread *gostarlark.Thread, fn *gostarlark.Builtin, args gostarlark.Tuple, kwargs []gostarlark.Tuple) (gostarlark.Value, error) {
	var ref, configPath, name, image, readinessCmd string
	var envValue, portsValue, volumesValue gostarlark.Value
	if err := unpackKwargs(fn.Name(), args, kwargs,
		"ref", &ref,
		"config?", &configPath,
		"name?", &name,
		"image?", &image,
		"env?", &envValue,
		"ports?", &portsValue,
		"volumes?", &volumesValue,
		"readiness_cmd?", &readinessCmd,
	); err != nil {
		return nil, err
	}
	env, err := optionalStringDict(envValue)
	if err != nil {
		return nil, err
	}
	ports, err := optionalStringList(portsValue)
	if err != nil {
		return nil, err
	}
	volumes, err := optionalStringList(volumesValue)
	if err != nil {
		return nil, err
	}
	b := builder(thread)
	for _, dep := range b.dependencies {
		if dep.Ref == ref {
			return nil, fmt.Errorf("duplicate dependency ref %q", ref)
		}
	}
	b.dependencies = append(b.dependencies, Dependency{
		Ref: ref, ConfigPath: configPath, Name: name, Image: image, Env: env, Ports: ports, Volumes: volumes, ReadinessCmd: readinessCmd,
	})
	return gostarlark.None, nil
}

func rpmTarget(thread *gostarlark.Thread, fn *gostarlark.Builtin, args gostarlark.Tuple, kwargs []gostarlark.Tuple) (gostarlark.Value, error) {
	var ref, configPath, command, workdir, readinessCmd string
	var reload bool
	var envValue gostarlark.Value
	if err := unpackKwargs(fn.Name(), args, kwargs,
		"ref", &ref,
		"config?", &configPath,
		"command?", &command,
		"workdir?", &workdir,
		"readiness_cmd?", &readinessCmd,
		"env?", &envValue,
		"reload", &reload,
	); err != nil {
		return nil, err
	}
	env, err := optionalStringDict(envValue)
	if err != nil {
		return nil, err
	}
	b := builder(thread)
	for _, t := range b.targets {
		if t.Ref == ref {
			return nil, fmt.Errorf("duplicate target ref %q", ref)
		}
	}
	b.targets = append(b.targets, TargetProcess{
		Ref: ref, ConfigPath: configPath, Command: command, WorkingDir: workdir, ReadinessCmd: readinessCmd, Env: env, Reload: reload,
	})
	return gostarlark.None, nil
}

func rpmBeforeTarget(thread *gostarlark.Thread, fn *gostarlark.Builtin, args gostarlark.Tuple, kwargs []gostarlark.Tuple) (gostarlark.Value, error) {
	target, err := targetProcess(fn, args, kwargs, false)
	if err != nil {
		return nil, err
	}
	b := builder(thread)
	for _, bt := range b.beforeTargets {
		if bt.Ref == target.Ref {
			return nil, fmt.Errorf("duplicate before_target ref %q", target.Ref)
		}
	}
	b.beforeTargets = append(b.beforeTargets, target)
	return gostarlark.None, nil
}

func rpmDepTarget(thread *gostarlark.Thread, fn *gostarlark.Builtin, args gostarlark.Tuple, kwargs []gostarlark.Tuple) (gostarlark.Value, error) {
	target, err := targetProcess(fn, args, kwargs, false)
	if err != nil {
		return nil, err
	}
	b := builder(thread)
	for _, dt := range b.depTargets {
		if dt.Ref == target.Ref {
			return nil, fmt.Errorf("duplicate dep_target ref %q", target.Ref)
		}
	}
	b.depTargets = append(b.depTargets, target)
	return gostarlark.None, nil
}

func rpmWatch(thread *gostarlark.Thread, fn *gostarlark.Builtin, args gostarlark.Tuple, kwargs []gostarlark.Tuple) (gostarlark.Value, error) {
	var target string
	var reload, enabled bool
	var rootsValue, ignoreValue gostarlark.Value
	if err := unpackKwargs(fn.Name(), args, kwargs,
		"target", &target,
		"roots", &rootsValue,
		"ignore", &ignoreValue,
		"reload", &reload,
		"enabled", &enabled,
	); err != nil {
		return nil, err
	}
	roots, err := stringList(rootsValue)
	if err != nil {
		return nil, err
	}
	ignore, err := stringList(ignoreValue)
	if err != nil {
		return nil, err
	}
	builder(thread).watches = append(builder(thread).watches, Watch{
		Target: target, Roots: roots, Ignore: ignore, Reload: reload, Enabled: enabled,
	})
	return gostarlark.None, nil
}

func targetProcess(fn *gostarlark.Builtin, args gostarlark.Tuple, kwargs []gostarlark.Tuple, reload bool) (TargetProcess, error) {
	var ref, configPath, command, workdir string
	var envValue gostarlark.Value
	if err := unpackKwargs(fn.Name(), args, kwargs,
		"ref", &ref,
		"config?", &configPath,
		"command?", &command,
		"workdir?", &workdir,
		"env?", &envValue,
	); err != nil {
		return TargetProcess{}, err
	}
	env, err := optionalStringDict(envValue)
	if err != nil {
		return TargetProcess{}, err
	}
	return TargetProcess{
		Ref: ref, ConfigPath: configPath, Command: command, WorkingDir: workdir, Env: env, Reload: reload,
	}, nil
}

func rpmRun(thread *gostarlark.Thread, fn *gostarlark.Builtin, args gostarlark.Tuple, kwargs []gostarlark.Tuple) (gostarlark.Value, error) {
	var orderValue gostarlark.Value
	if err := unpackKwargs(fn.Name(), args, kwargs, "order", &orderValue); err != nil {
		return nil, err
	}
	order, err := stringList(orderValue)
	if err != nil {
		return nil, err
	}
	builder(thread).runOrder = order
	return gostarlark.None, nil
}

func unpackKwargs(fn string, args gostarlark.Tuple, kwargs []gostarlark.Tuple, pairs ...any) error {
	if len(args) > 0 {
		return fmt.Errorf("%s: unexpected positional arguments", fn)
	}
	return gostarlark.UnpackArgs(fn, args, kwargs, pairs...)
}

func reloadPolicy(value gostarlark.Value) (ReloadPolicy, error) {
	values, err := stringValueDict(value)
	if err != nil {
		return ReloadPolicy{}, err
	}
	enabled, err := boolFromValue(values["enabled"])
	if err != nil {
		return ReloadPolicy{}, errors.Wrap(err, "live_reload.enabled")
	}
	debounce, err := stringFromValue(values["debounce"])
	if err != nil {
		return ReloadPolicy{}, errors.Wrap(err, "live_reload.debounce")
	}
	return ReloadPolicy{Enabled: enabled, Debounce: debounce}, nil
}

func stringDict(value gostarlark.Value) (map[string]string, error) {
	values, err := stringValueDict(value)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(values))
	for k, v := range values {
		sv, err := stringFromValue(v)
		if err != nil {
			return nil, errors.Wrapf(err, "%s", k)
		}
		result[k] = sv
	}
	return result, nil
}

func optionalStringDict(value gostarlark.Value) (map[string]string, error) {
	if value == nil {
		return map[string]string{}, nil
	}
	return stringDict(value)
}

func stringValueDict(value gostarlark.Value) (map[string]gostarlark.Value, error) {
	dictValue, ok := value.(*gostarlark.Dict)
	if !ok {
		return nil, fmt.Errorf("expected dict, got %s", value.Type())
	}
	result := make(map[string]gostarlark.Value, dictValue.Len())
	for _, keyValue := range dictValue.Keys() {
		key, err := stringFromValue(keyValue)
		if err != nil {
			return nil, errors.Wrap(err, "dict key")
		}
		item, _, err := dictValue.Get(keyValue)
		if err != nil {
			return nil, err
		}
		result[key] = item
	}
	return result, nil
}

func stringList(value gostarlark.Value) ([]string, error) {
	var iterable gostarlark.Iterable
	switch typed := value.(type) {
	case *gostarlark.List:
		iterable = typed
	case gostarlark.Tuple:
		iterable = typed
	default:
		return nil, fmt.Errorf("expected list or tuple, got %s", value.Type())
	}
	var item gostarlark.Value
	var result []string
	iter := iterable.Iterate()
	defer iter.Done()
	for iter.Next(&item) {
		converted, err := stringFromValue(item)
		if err != nil {
			return nil, err
		}
		result = append(result, converted)
	}
	return result, nil
}

func optionalStringList(value gostarlark.Value) ([]string, error) {
	if value == nil {
		return []string{}, nil
	}
	return stringList(value)
}

func stringFromValue(value gostarlark.Value) (string, error) {
	if value == nil {
		return "", fmt.Errorf("expected string, got missing value")
	}
	stringValue, ok := gostarlark.AsString(value)
	if !ok {
		return "", fmt.Errorf("expected string, got %s", value.Type())
	}
	return stringValue, nil
}

func boolFromValue(value gostarlark.Value) (bool, error) {
	if value == nil {
		return false, fmt.Errorf("expected bool, got missing value")
	}
	boolValue, ok := value.(gostarlark.Bool)
	if !ok {
		return false, fmt.Errorf("expected bool, got %s", value.Type())
	}
	return bool(boolValue), nil
}

func intFromValue(value gostarlark.Value) (int, error) {
	if value == nil {
		return 0, fmt.Errorf("expected int, got missing value")
	}
	intValue, ok := value.(gostarlark.Int)
	if !ok {
		return 0, fmt.Errorf("expected int, got %s", value.Type())
	}
	i, ok := intValue.Int64()
	if !ok || i > math.MaxInt || i < math.MinInt {
		return 0, fmt.Errorf("int out of range")
	}
	return int(i), nil
}

func builder(thread *gostarlark.Thread) *planBuilder {
	value := thread.Local("plan")
	builder, ok := value.(*planBuilder)
	if !ok {
		panic("missing runtime plan builder")
	}
	return builder
}

func evalError(err error) error {
	var evalErr *gostarlark.EvalError
	if errors.As(err, &evalErr) {
		return fmt.Errorf("%w\n%s", err, evalErr.Backtrace())
	}
	return err
}

var _ = intFromValue
