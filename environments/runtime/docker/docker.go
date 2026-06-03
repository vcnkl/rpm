package docker

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/containerd/errdefs"
	sdkclient "github.com/docker/go-sdk/client"
	sdkcontainer "github.com/docker/go-sdk/container"
	sdknetwork "github.com/docker/go-sdk/network"
	sdkvolume "github.com/docker/go-sdk/volume"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/pkg/errors"
	envruntime "github.com/vcnkl/rpm/environments/runtime"
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
	VolumeNamer   VolumeNamer
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
	volumeNamer   VolumeNamer
}

func NewCLI(opts Options) *CLI {
	backend := opts.Backend
	if backend == nil {
		backend = sdkBackend{}
	}
	portAllocator := opts.PortAllocator
	if portAllocator == nil {
		portAllocator = ephemeralPortAllocator{}
	}
	volumeNamer := opts.VolumeNamer
	if volumeNamer == nil {
		volumeNamer = NewMemoryVolumeNamer("rpm")
	}
	return &CLI{backend: backend, portAllocator: portAllocator, volumeNamer: volumeNamer}
}

func (c *CLI) Up(ctx context.Context, blueprint string, plan *envstarlark.RuntimePlan) (envruntime.DependencyStartup, error) {
	network := networkName(blueprint)
	if err := c.backend.EnsureNetwork(ctx, network); err != nil {
		return envruntime.DependencyStartup{}, err
	}
	startup := envruntime.DependencyStartup{Env: make(map[string]string)}
	for _, dep := range plan.Dependencies {
		volumeNames, volumeBinds, err := c.resolveVolumes(ctx, blueprint, dep)
		if err != nil {
			return envruntime.DependencyStartup{}, err
		}
		for _, volume := range volumeNames {
			if err = c.backend.EnsureVolume(ctx, volume); err != nil {
				return envruntime.DependencyStartup{}, err
			}
		}
		for _, name := range containerNames(blueprint, dep) {
			spec, env, err := c.containerSpec(ctx, network, name, dep, volumeBinds)
			if err != nil {
				return envruntime.DependencyStartup{}, err
			}
			if err = c.backend.RunContainer(ctx, spec); err != nil {
				return envruntime.DependencyStartup{}, err
			}
			for key, value := range env {
				startup.Env[key] = value
			}
		}
	}
	return startup, nil
}

func (c *CLI) Down(ctx context.Context, blueprint string, plan *envstarlark.RuntimePlan) error {
	for _, dep := range plan.Dependencies {
		for _, name := range containerNames(blueprint, dep) {
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

type sdkClientFactory func(context.Context) (sdkclient.SDKClient, error)

type sdkBackend struct {
	newClient sdkClientFactory
}

func (b sdkBackend) EnsureNetwork(ctx context.Context, name string) error {
	cli, err := b.client(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	if _, err = sdknetwork.FindByName(ctx, name, sdknetwork.WithListClient(cli)); err == nil {
		return nil
	} else if !isMissingDockerResource(err) {
		return errors.Wrapf(err, "find docker network %s", name)
	}
	if _, err = sdknetwork.New(ctx, sdknetwork.WithClient(cli), sdknetwork.WithName(name)); err == nil {
		return nil
	} else if !isExistingDockerNetwork(err) {
		return errors.Wrapf(err, "create docker network %s", name)
	}
	if _, err = sdknetwork.FindByName(ctx, name, sdknetwork.WithListClient(cli)); err != nil {
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

	if _, err = sdkvolume.FindByID(ctx, name, sdkvolume.WithFindClient(cli)); err == nil {
		return nil
	} else if !isMissingDockerResource(err) {
		return errors.Wrapf(err, "find docker volume %s", name)
	}
	if _, err = sdkvolume.New(ctx, sdkvolume.WithClient(cli), sdkvolume.WithName(name)); err != nil && !isExistingDockerVolume(err) {
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
	if _, err = sdkcontainer.Run(ctx, opts...); err != nil {
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
	if _, err = cli.ContainerRemove(ctx, found.ID, client.ContainerRemoveOptions{RemoveVolumes: true, Force: true}); err != nil {
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
	if _, err = cli.NetworkRemove(ctx, found.ID, client.NetworkRemoveOptions{}); err != nil {
		return errors.Wrapf(err, "remove docker network %s", name)
	}
	return nil
}

func (b sdkBackend) client(ctx context.Context) (sdkclient.SDKClient, error) {
	newClient := b.newClient
	if newClient == nil {
		newClient = func(ctx context.Context) (sdkclient.SDKClient, error) {
			return sdkclient.New(ctx)
		}
	}
	return newClient(ctx)
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

func (c *CLI) containerSpec(ctx context.Context, network string, name string, dep envstarlark.Dependency, volumeBinds []string) (ContainerSpec, map[string]string, error) {
	ports, env, err := c.publishPorts(ctx, dep)
	if err != nil {
		return ContainerSpec{}, nil, err
	}
	spec := ContainerSpec{
		Name:    name,
		Image:   dep.Image,
		Network: network,
		Env:     dep.Env,
		Ports:   ports,
	}
	if len(volumeBinds) > 0 {
		spec.Volumes = append([]string{}, volumeBinds...)
	}
	return spec, env, nil
}

func (c *CLI) resolveVolumes(ctx context.Context, blueprint string, dep envstarlark.Dependency) ([]string, []string, error) {
	if len(dep.Volumes) == 0 {
		return nil, nil, nil
	}
	volumeNames := make([]string, 0, len(dep.Volumes))
	volumeBinds := make([]string, 0, len(dep.Volumes))
	for _, path := range dep.Volumes {
		name, err := c.volumeNamer.Name(ctx, blueprint, dep.Name, path)
		if err != nil {
			return nil, nil, err
		}
		volumeNames = append(volumeNames, name)
		volumeBinds = append(volumeBinds, name+":"+path)
	}
	return volumeNames, volumeBinds, nil
}

func (c *CLI) publishPorts(ctx context.Context, dep envstarlark.Dependency) ([]string, map[string]string, error) {
	if len(dep.Ports) == 0 {
		return nil, nil, nil
	}
	ports := make([]string, 0, len(dep.Ports))
	env := make(map[string]string)
	multiplePorts := len(dep.Ports) > 1
	for _, item := range dep.Ports {
		port, err := parsePort(item)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "%s port %q", dep.Name, item)
		}
		if port.Host == "" {
			hostPort, err := c.portAllocator.Allocate(ctx)
			if err != nil {
				return nil, nil, errors.Wrap(err, "failed to allocate dependency host port")
			}
			port.Host = strconv.Itoa(hostPort)
			port.HostPort = port.Host
		}
		ports = append(ports, port.Host+":"+port.Container)
		env[port.EnvName(dep.Name, multiplePorts)] = port.HostPort
	}
	return ports, env, nil
}

func containerNames(blueprint string, dep envstarlark.Dependency) []string {
	return []string{networkName(blueprint) + "-" + sanitize(dep.Name)}
}

var invalidNameChars = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)
var invalidEnvNameChars = regexp.MustCompile(`[^A-Z0-9_]+`)
var validEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type portMapping struct {
	Name      string
	Host      string
	HostPort  string
	Container string
}

func (p portMapping) EnvName(dependency string, multiplePorts bool) string {
	if p.Name != "" {
		return p.Name
	}
	name := envSafeName(dependency) + "_PORT"
	if multiplePorts {
		name += "_" + containerPortNumber(p.Container)
	}
	return name
}

func parsePort(value string) (portMapping, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return portMapping{}, fmt.Errorf("empty port")
	}
	name := ""
	if left, right, ok := strings.Cut(value, "="); ok {
		name = strings.TrimSpace(left)
		if !validEnvName.MatchString(name) {
			return portMapping{}, fmt.Errorf("invalid env name %q", name)
		}
		value = strings.TrimSpace(right)
		if value == "" {
			return portMapping{}, fmt.Errorf("empty port")
		}
	}
	if !strings.Contains(value, ":") {
		if strings.TrimSpace(value) == "" {
			return portMapping{}, fmt.Errorf("empty container port")
		}
		return portMapping{Name: name, Container: strings.TrimSpace(value)}, nil
	}
	index := strings.LastIndex(value, ":")
	host := strings.TrimSpace(value[:index])
	container := strings.TrimSpace(value[index+1:])
	if host == "" || container == "" {
		return portMapping{}, fmt.Errorf("invalid port mapping %q", value)
	}
	hostPort := host
	if hostIndex := strings.LastIndex(host, ":"); hostIndex >= 0 {
		hostPort = strings.TrimSpace(host[hostIndex+1:])
	}
	if hostPort == "" {
		return portMapping{}, fmt.Errorf("invalid port mapping %q", value)
	}
	return portMapping{Name: name, Host: host, HostPort: hostPort, Container: container}, nil
}

func envSafeName(value string) string {
	value = invalidEnvNameChars.ReplaceAllString(strings.ToUpper(value), "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return "DEPENDENCY"
	}
	if value[0] >= '0' && value[0] <= '9' {
		value = "_" + value
	}
	return value
}

func containerPortNumber(value string) string {
	value = strings.TrimSpace(value)
	value, _, _ = strings.Cut(value, "/")
	value = invalidEnvNameChars.ReplaceAllString(strings.ToUpper(value), "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return "PORT"
	}
	return value
}

func sanitize(value string) string {
	value = invalidNameChars.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if value == "" {
		return "env"
	}
	return strings.ToLower(value)
}
