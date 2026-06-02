package docker

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/containerd/errdefs"
	sdkclient "github.com/docker/go-sdk/client"
	sdkcontainer "github.com/docker/go-sdk/container"
	dockercontext "github.com/docker/go-sdk/context"
	sdknetwork "github.com/docker/go-sdk/network"
	sdkvolume "github.com/docker/go-sdk/volume"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/pkg/errors"
	envstarlark "github.com/vcnkl/rpm/environments/starlark"
)

type Backend interface {
	EnsureNetwork(ctx context.Context, name string) error
	EnsureVolume(ctx context.Context, name string) error
	RunContainer(ctx context.Context, spec ContainerSpec) error
	RemoveContainer(ctx context.Context, name string) error
	RemoveNetwork(ctx context.Context, name string) error
}

type PortAllocator interface {
	Allocate(ctx context.Context) (int, error)
}

type Options struct {
	Backend       Backend
	PortAllocator PortAllocator
}

type ContainerSpec struct {
	Name    string
	Image   string
	Network string
	Env     map[string]string
	Ports   []string
	Volumes []string
}

type CLI struct {
	backend       Backend
	portAllocator PortAllocator
}

func NewCLI(opts Options) *CLI {
	backend := opts.Backend
	if backend == nil {
		backend = sdkBackend{currentDockerContext: dockercontext.Current}
	}
	portAllocator := opts.PortAllocator
	if portAllocator == nil {
		portAllocator = ephemeralPortAllocator{}
	}
	return &CLI{backend: backend, portAllocator: portAllocator}
}

func (c *CLI) Up(ctx context.Context, blueprint string, plan *envstarlark.RuntimePlan) error {
	network := networkName(blueprint)
	if err := c.backend.EnsureNetwork(ctx, network); err != nil {
		return err
	}
	for _, dep := range plan.Dependencies {
		for _, volume := range volumeNames(dep.Volumes) {
			if err := c.backend.EnsureVolume(ctx, volume); err != nil {
				return err
			}
		}
		for _, name := range containerNames(blueprint, dep, plan.Targets) {
			spec, err := c.containerSpec(ctx, network, name, dep)
			if err != nil {
				return err
			}
			if err := c.backend.RunContainer(ctx, spec); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *CLI) Down(ctx context.Context, blueprint string, plan *envstarlark.RuntimePlan) error {
	for _, dep := range plan.Dependencies {
		for _, name := range containerNames(blueprint, dep, plan.Targets) {
			if err := c.backend.RemoveContainer(ctx, name); err != nil && !isMissingDockerResource(err) {
				return err
			}
		}
	}
	if err := c.backend.RemoveNetwork(ctx, networkName(blueprint)); err != nil && !isMissingDockerResource(err) {
		return err
	}
	return nil
}

type sdkBackend struct {
	currentDockerContext func() (string, error)
}

func (b sdkBackend) EnsureNetwork(ctx context.Context, name string) error {
	cli, err := b.client(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	if _, err := sdknetwork.FindByName(ctx, name, sdknetwork.WithListClient(cli)); err == nil {
		return nil
	} else if !isMissingDockerResource(err) {
		return errors.Wrapf(err, "find docker network %s", name)
	}
	if _, err := sdknetwork.New(ctx, sdknetwork.WithClient(cli), sdknetwork.WithName(name)); err == nil {
		return nil
	} else if !isExistingDockerNetwork(err) {
		return errors.Wrapf(err, "create docker network %s", name)
	}
	if _, err := sdknetwork.FindByName(ctx, name, sdknetwork.WithListClient(cli)); err != nil {
		return errors.Wrapf(err, "find existing docker network %s", name)
	}
	return nil
}

func (b sdkBackend) EnsureVolume(ctx context.Context, name string) error {
	cli, err := b.client(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	if _, err := sdkvolume.FindByID(ctx, name, sdkvolume.WithFindClient(cli)); err == nil {
		return nil
	} else if !isMissingDockerResource(err) {
		return errors.Wrapf(err, "find docker volume %s", name)
	}
	if _, err := sdkvolume.New(ctx, sdkvolume.WithClient(cli), sdkvolume.WithName(name)); err != nil && !isExistingDockerVolume(err) {
		return errors.Wrapf(err, "create docker volume %s", name)
	}
	return nil
}

func (b sdkBackend) RunContainer(ctx context.Context, spec ContainerSpec) error {
	cli, err := b.client(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	opts := []sdkcontainer.ContainerCustomizer{
		sdkcontainer.WithClient(cli),
		sdkcontainer.WithName(spec.Name),
		sdkcontainer.WithImage(spec.Image),
		sdkcontainer.WithNetworkName(nil, spec.Network),
	}
	if len(spec.Env) > 0 {
		opts = append(opts, sdkcontainer.WithEnv(spec.Env))
	}
	if len(spec.Ports) > 0 {
		opts = append(opts, sdkcontainer.WithExposedPorts(spec.Ports...))
	}
	if len(spec.Volumes) > 0 {
		opts = append(opts, sdkcontainer.WithHostConfigModifier(func(hostConfig *container.HostConfig) {
			hostConfig.Binds = append(hostConfig.Binds, spec.Volumes...)
		}))
	}
	if _, err := sdkcontainer.Run(ctx, opts...); err != nil {
		return errors.Wrapf(err, "run docker container %s", spec.Name)
	}
	return nil
}

func (b sdkBackend) RemoveContainer(ctx context.Context, name string) error {
	cli, err := b.client(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	found, err := cli.FindContainerByName(ctx, name)
	if err != nil {
		return err
	}
	if _, err := cli.ContainerRemove(ctx, found.ID, client.ContainerRemoveOptions{RemoveVolumes: true, Force: true}); err != nil {
		return errors.Wrapf(err, "remove docker container %s", name)
	}
	return nil
}

func (b sdkBackend) RemoveNetwork(ctx context.Context, name string) error {
	cli, err := b.client(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	found, err := sdknetwork.FindByName(ctx, name, sdknetwork.WithListClient(cli))
	if err != nil {
		return err
	}
	if _, err := cli.NetworkRemove(ctx, found.ID, client.NetworkRemoveOptions{}); err != nil {
		return errors.Wrapf(err, "remove docker network %s", name)
	}
	return nil
}

func (b sdkBackend) client(ctx context.Context) (sdkclient.SDKClient, error) {
	currentDockerContext := b.currentDockerContext
	if currentDockerContext == nil {
		currentDockerContext = dockercontext.Current
	}
	dockerContext, err := currentDockerContext()
	if err != nil {
		return nil, errors.Wrap(err, "resolve current docker context")
	}
	return sdkclient.New(ctx, sdkclient.WithDockerContext(dockerContext))
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

func isMissingDockerResource(err error) bool {
	if errdefs.IsNotFound(err) {
		return true
	}
	output := strings.ToLower(err.Error())
	return strings.Contains(output, "no such container") ||
		strings.Contains(output, "no such network") ||
		strings.Contains(output, "no such volume") ||
		strings.Contains(output, "no networks found") ||
		(strings.Contains(output, "container ") && strings.Contains(output, " not found")) ||
		(strings.Contains(output, "network ") && strings.Contains(output, " not found")) ||
		(strings.Contains(output, "volume ") && strings.Contains(output, " not found"))
}

func isExistingDockerNetwork(err error) bool {
	output := strings.ToLower(err.Error())
	return strings.Contains(output, "network") && strings.Contains(output, "already exists")
}

func isExistingDockerVolume(err error) bool {
	output := strings.ToLower(err.Error())
	return strings.Contains(output, "volume") && strings.Contains(output, "already exists")
}

func networkName(blueprint string) string {
	return "rpm-" + sanitize(blueprint)
}

func (c *CLI) containerSpec(ctx context.Context, network string, name string, dep envstarlark.Dependency) (ContainerSpec, error) {
	ports, err := c.publishPorts(ctx, dep.Ports)
	if err != nil {
		return ContainerSpec{}, err
	}
	volumes := dep.Volumes
	if len(volumes) > 0 {
		volumes = append([]string{}, dep.Volumes...)
	}
	return ContainerSpec{
		Name:    name,
		Image:   dep.Image,
		Network: network,
		Env:     dep.Env,
		Ports:   ports,
		Volumes: volumes,
	}, nil
}

func (c *CLI) publishPorts(ctx context.Context, ports []string) ([]string, error) {
	if len(ports) == 0 {
		return nil, nil
	}
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
