package docker

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	envstarlark "github.com/vcnkl/rpm/environments/starlark"
)

type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) error
}

type PortAllocator interface {
	Allocate(ctx context.Context) (int, error)
}

type Options struct {
	CommandRunner CommandRunner
	PortAllocator PortAllocator
}

type CLI struct {
	runner        CommandRunner
	portAllocator PortAllocator
}

func NewCLI(opts Options) *CLI {
	runner := opts.CommandRunner
	if runner == nil {
		runner = osRunner{}
	}
	portAllocator := opts.PortAllocator
	if portAllocator == nil {
		portAllocator = ephemeralPortAllocator{}
	}
	return &CLI{runner: runner, portAllocator: portAllocator}
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
			args, err := c.dockerRunArgs(ctx, network, name, dep)
			if err != nil {
				return err
			}
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

type ephemeralPortAllocator struct{}

func (ephemeralPortAllocator) Allocate(ctx context.Context) (int, error) {
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected listener address %q", listener.Addr().String())
	}
	return addr.Port, nil
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

func (c *CLI) dockerRunArgs(ctx context.Context, network string, name string, dep envstarlark.Dependency) ([]string, error) {
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
	ports, err := c.publishPorts(ctx, dep.Ports)
	if err != nil {
		return nil, err
	}
	for _, port := range ports {
		args = append(args, "--publish", port)
	}
	for _, volume := range dep.Volumes {
		args = append(args, "--volume", volume)
	}
	args = append(args, dep.Image)
	return args, nil
}

func (c *CLI) publishPorts(ctx context.Context, ports []string) ([]string, error) {
	if len(ports) != 1 || !singleContainerPort(ports[0]) {
		return append([]string{}, ports...), nil
	}
	hostPort, err := c.portAllocator.Allocate(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to allocate dependency host port")
	}
	return []string{strconv.Itoa(hostPort) + ":" + ports[0]}, nil
}

func singleContainerPort(port string) bool {
	port = strings.TrimSpace(port)
	return port != "" && !strings.Contains(port, ":")
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
