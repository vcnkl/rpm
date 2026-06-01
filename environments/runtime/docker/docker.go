package docker

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	envstarlark "github.com/vcnkl/rpm/environments/starlark"
)

type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) error
}

type Options struct {
	CommandRunner CommandRunner
}

type CLI struct {
	runner CommandRunner
}

func NewCLI(opts Options) *CLI {
	runner := opts.CommandRunner
	if runner == nil {
		runner = osRunner{}
	}
	return &CLI{runner: runner}
}

func (c *CLI) Up(ctx context.Context, blueprint string, plan *envstarlark.RuntimePlan) error {
	network := networkName(blueprint)
	if err := c.runner.Run(ctx, "docker", "network", "create", network); err != nil {
		return err
	}
	for _, dep := range plan.Dependencies {
		for _, volume := range volumeNames(dep.Volumes) {
			if err := c.runner.Run(ctx, "docker", "volume", "create", volume); err != nil {
				return err
			}
		}
		for _, name := range containerNames(blueprint, dep, plan.Targets) {
			args := dockerRunArgs(network, name, dep)
			if err := c.runner.Run(ctx, "docker", args...); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *CLI) Down(ctx context.Context, blueprint string, plan *envstarlark.RuntimePlan) error {
	for _, dep := range plan.Dependencies {
		for _, name := range containerNames(blueprint, dep, plan.Targets) {
			if err := c.runner.Run(ctx, "docker", "rm", "-f", name); err != nil && !isMissingDockerResource(err) {
				return err
			}
		}
	}
	if err := c.runner.Run(ctx, "docker", "network", "rm", networkName(blueprint)); err != nil && !isMissingDockerResource(err) {
		return err
	}
	return nil
}

type osRunner struct{}

func (osRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	data, err := cmd.CombinedOutput()
	if err != nil {
		return CommandError{
			Name:   name,
			Args:   args,
			Err:    err,
			Output: strings.TrimSpace(string(data)),
		}
	}
	return nil
}

type CommandError struct {
	Name   string
	Args   []string
	Err    error
	Output string
}

func (e CommandError) Error() string {
	return fmt.Sprintf("%s %s failed: %v: %s", e.Name, strings.Join(e.Args, " "), e.Err, e.Output)
}

func isMissingDockerResource(err error) bool {
	commandErr, ok := err.(CommandError)
	if !ok {
		return false
	}
	output := strings.ToLower(commandErr.Output)
	return strings.Contains(output, "no such container") ||
		strings.Contains(output, "no such network") ||
		strings.Contains(output, "not found")
}

func networkName(blueprint string) string {
	return "rpm-" + sanitize(blueprint)
}

func dockerRunArgs(network string, name string, dep envstarlark.Dependency) []string {
	args := []string{"run", "--detach", "--name", name, "--network", network}
	envKeys := make([]string, 0, len(dep.Env))
	for key := range dep.Env {
		envKeys = append(envKeys, key)
	}
	sort.Strings(envKeys)
	for _, key := range envKeys {
		value := dep.Env[key]
		args = append(args, "--env", key+"="+value)
	}
	for _, port := range dep.Ports {
		args = append(args, "--publish", port)
	}
	for _, volume := range dep.Volumes {
		args = append(args, "--volume", volume)
	}
	args = append(args, dep.Image)
	return args
}

func containerNames(blueprint string, dep envstarlark.Dependency, targets []envstarlark.TargetProcess) []string {
	if dep.Mode != "dedicated" {
		return []string{networkName(blueprint) + "-" + sanitize(dep.Ref)}
	}
	prefix := depBundle(dep.Ref) + ":"
	names := []string{}
	for _, target := range targets {
		if strings.HasPrefix(target.Ref, prefix) {
			names = append(names, networkName(blueprint)+"-"+sanitize(dep.Ref)+"-"+sanitize(target.Ref))
		}
	}
	if len(names) == 0 {
		names = append(names, networkName(blueprint)+"-"+sanitize(dep.Ref))
	}
	sort.Strings(names)
	return names
}

func depBundle(ref string) string {
	bundle, _, ok := strings.Cut(ref, ":")
	if !ok {
		return ref
	}
	return bundle
}

func volumeNames(volumes []string) []string {
	seen := make(map[string]bool)
	names := make([]string, 0, len(volumes))
	for _, volume := range volumes {
		name, _, _ := strings.Cut(volume, ":")
		if name != "" && !strings.Contains(name, "/") && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

var invalidNameChars = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

func sanitize(value string) string {
	value = invalidNameChars.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if value == "" {
		return "env"
	}
	return strings.ToLower(value)
}
