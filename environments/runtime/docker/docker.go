package docker

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/containerd/errdefs"
	sdkclient "github.com/docker/go-sdk/client"
	sdkcontainer "github.com/docker/go-sdk/container"
	sdknetwork "github.com/docker/go-sdk/network"
	sdkvolume "github.com/docker/go-sdk/volume"
	"github.com/moby/moby/api/types/container"
	mobynetwork "github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/pkg/errors"
	envruntime "github.com/vcnkl/rpm/environments/runtime"
	envstarlark "github.com/vcnkl/rpm/environments/starlark"
)

type Backend interface {
	EnsureNetwork(ctx context.Context, name string) (bool, error)
	EnsureVolume(ctx context.Context, name string) (bool, error)
	EnsureContainer(ctx context.Context, spec ContainerSpec) (ContainerState, error)
	RemoveContainer(ctx context.Context, name string) error
	RemoveNetwork(ctx context.Context, name string) error
	RemoveVolume(ctx context.Context, name string) error
}

type PortAllocator interface {
	Allocate(ctx context.Context) (int, error)
}

type ReadinessRunner interface {
	Run(ctx context.Context, shell string, command string, env map[string]string) error
}

type Options struct {
	Backend         Backend
	PortAllocator   PortAllocator
	VolumeNamer     VolumeNamer
	ReadinessRunner ReadinessRunner
	Shell           string
}

type ContainerSpec struct {
	Name    string
	Image   string
	Network string
	Env     map[string]string
	Ports   []string
	Volumes []string
}

type ContainerState struct {
	Created bool
	// Ports maps container ports ("27017/tcp") to the host ports an existing
	// container is actually bound to. Reused containers keep the bindings they
	// were created with, which may differ from the ports in the current spec.
	Ports map[string]string
}

type CLI struct {
	backend         Backend
	portAllocator   PortAllocator
	volumeNamer     VolumeNamer
	readinessRunner ReadinessRunner
	shell           string
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
	readinessRunner := opts.ReadinessRunner
	if readinessRunner == nil {
		readinessRunner = shellReadinessRunner{}
	}
	shell := opts.Shell
	if shell == "" {
		shell = "/bin/sh"
	}
	return &CLI{backend: backend, portAllocator: portAllocator, volumeNamer: volumeNamer, readinessRunner: readinessRunner, shell: shell}
}

func (c *CLI) Up(ctx context.Context, blueprint string, plan *envstarlark.RuntimePlan) (envruntime.DependencyStartup, error) {
	dependencies, err := normalizeDependencies(blueprint, plan.Dependencies)
	if err != nil {
		return envruntime.DependencyStartup{}, err
	}
	network := networkName(blueprint)
	createdNetwork, err := c.backend.EnsureNetwork(ctx, network)
	if err != nil {
		return envruntime.DependencyStartup{}, err
	}
	startup := envruntime.DependencyStartup{Env: make(map[string]string)}
	startedContainers := []string{}
	createdVolumes := []string{}
	fail := func(ref string, cause error) (envruntime.DependencyStartup, error) {
		depErr := envruntime.NewDependencyError(ref, cause)
		if cleanupErr := c.cleanup(ctx, network, createdNetwork, startedContainers, createdVolumes); cleanupErr != nil {
			return envruntime.DependencyStartup{}, errors.Wrapf(depErr, "%v", cleanupErr)
		}
		return envruntime.DependencyStartup{}, depErr
	}
	for _, dep := range dependencies {
		volumeNames, volumeBinds, err := c.resolveVolumes(ctx, blueprint, dep)
		if err != nil {
			return fail(dep.Ref, err)
		}
		for _, volume := range volumeNames {
			created, err := c.backend.EnsureVolume(ctx, volume)
			if err != nil {
				return fail(dep.Ref, err)
			}
			if created {
				createdVolumes = append(createdVolumes, volume)
			}
		}
		for _, name := range containerNames(blueprint, dep) {
			spec, mappings, err := c.containerSpec(ctx, network, name, dep, volumeBinds)
			if err != nil {
				return fail(dep.Ref, err)
			}
			state, err := c.ensureContainer(ctx, spec, mappings)
			if err != nil {
				return fail(dep.Ref, err)
			}
			if state.Created {
				startedContainers = append(startedContainers, name)
			}
			env, err := dependencyPortEnv(name, dep, mappings, state)
			if err != nil {
				return fail(dep.Ref, err)
			}
			if err = c.runReadiness(ctx, name, dep, env); err != nil {
				return fail(dep.Ref, err)
			}
			for key, value := range env {
				startup.Env[key] = value
			}
		}
	}
	return startup, nil
}

func normalizeDependencies(blueprint string, dependencies []envstarlark.Dependency) ([]envstarlark.Dependency, error) {
	seen := make(map[string]envstarlark.Dependency)
	normalized := make([]envstarlark.Dependency, 0, len(dependencies))
	for _, dep := range dependencies {
		for _, name := range containerNames(blueprint, dep) {
			existing, ok := seen[name]
			if !ok {
				seen[name] = dep
				normalized = append(normalized, dep)
				continue
			}
			if sameDependency(existing, dep) {
				continue
			}
			return nil, envruntime.NewDependencyError(dep.Ref, fmt.Errorf("duplicate dependency container %q for refs %q and %q", name, existing.Ref, dep.Ref))
		}
	}
	return normalized, nil
}

func sameDependency(left, right envstarlark.Dependency) bool {
	return left.Ref == right.Ref &&
		left.ConfigPath == right.ConfigPath &&
		left.Name == right.Name &&
		left.Image == right.Image &&
		reflect.DeepEqual(left.Env, right.Env) &&
		reflect.DeepEqual(left.Ports, right.Ports) &&
		reflect.DeepEqual(left.Volumes, right.Volumes) &&
		left.ReadinessCmd == right.ReadinessCmd
}

func (c *CLI) cleanup(ctx context.Context, network string, createdNetwork bool, containers []string, volumes []string) error {
	var msgs []string
	record := func(err error) {
		if err != nil && !isMissingDockerResource(err) {
			msgs = append(msgs, err.Error())
		}
	}
	for i := len(containers) - 1; i >= 0; i-- {
		record(c.backend.RemoveContainer(ctx, containers[i]))
	}
	for i := len(volumes) - 1; i >= 0; i-- {
		record(c.backend.RemoveVolume(ctx, volumes[i]))
	}
	if createdNetwork {
		record(c.backend.RemoveNetwork(ctx, network))
	}
	if len(msgs) == 0 {
		return nil
	}
	return fmt.Errorf("cleanup: %s", strings.Join(msgs, "; "))
}

// ensureContainer ensures the dependency container exists, replacing an
// existing container whose port bindings no longer satisfy the declared
// mappings (the dependency configuration changed since it was created).
func (c *CLI) ensureContainer(ctx context.Context, spec ContainerSpec, mappings []portMapping) (ContainerState, error) {
	state, err := c.backend.EnsureContainer(ctx, spec)
	if err != nil {
		return ContainerState{}, err
	}
	if state.Created || reusablePorts(mappings, state.Ports) {
		return state, nil
	}
	if err = c.backend.RemoveContainer(ctx, spec.Name); err != nil {
		return ContainerState{}, errors.Wrapf(err, "replace dependency container %s", spec.Name)
	}
	return c.backend.EnsureContainer(ctx, spec)
}

// reusablePorts reports whether an existing container's published ports cover
// every declared mapping: allocated host ports defer to whatever the container
// is bound to, while host ports pinned in configuration must match exactly.
func reusablePorts(mappings []portMapping, actual map[string]string) bool {
	for _, mapping := range mappings {
		hostPort, ok := actual[portKey(mapping.Container)]
		if !ok {
			return false
		}
		if !mapping.Allocated && hostPort != mapping.HostPort {
			return false
		}
	}
	return true
}

// dependencyPortEnv resolves the host ports exported as dependency env vars.
// Created containers bind the ports published by this run; reused containers
// keep the bindings they were created with, so the ports reported by the
// backend are the source of truth.
func dependencyPortEnv(name string, dep envstarlark.Dependency, mappings []portMapping, state ContainerState) (map[string]string, error) {
	if len(mappings) == 0 {
		return nil, nil
	}
	env := make(map[string]string, len(mappings))
	multiplePorts := len(mappings) > 1
	for _, mapping := range mappings {
		hostPort := mapping.HostPort
		if !state.Created {
			actual, ok := state.Ports[portKey(mapping.Container)]
			if !ok {
				return nil, fmt.Errorf("existing container %q does not publish container port %q", name, mapping.Container)
			}
			hostPort = actual
		}
		env[mapping.EnvName(dep.Name, multiplePorts)] = hostPort
	}
	return env, nil
}

// portKey normalizes a container port to the "<port>/<proto>" form used by
// Docker port bindings, defaulting the protocol to tcp.
func portKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	port, proto, ok := strings.Cut(value, "/")
	if !ok || strings.TrimSpace(proto) == "" {
		return strings.TrimSpace(port) + "/tcp"
	}
	return strings.TrimSpace(port) + "/" + strings.TrimSpace(proto)
}

func (c *CLI) runReadiness(ctx context.Context, container string, dep envstarlark.Dependency, env map[string]string) error {
	if strings.TrimSpace(dep.ReadinessCmd) == "" {
		return nil
	}
	values := make(map[string]string, len(env)+1)
	for key, value := range env {
		values[key] = value
	}
	values["DOCKER_CONTAINER_NAME"] = container
	if err := c.readinessRunner.Run(ctx, c.shell, dep.ReadinessCmd, values); err != nil {
		return errors.Wrapf(err, "%s readiness check failed", dep.Name)
	}
	return nil
}

type shellReadinessRunner struct{}

func (shellReadinessRunner) Run(ctx context.Context, shell string, command string, env map[string]string) error {
	parts := strings.Fields(shell)
	if len(parts) == 0 {
		parts = []string{"/bin/sh"}
	}
	args := append([]string{}, parts[1:]...)
	args = append(args, "-c", command)
	cmd := exec.CommandContext(ctx, parts[0], args...)
	cmd.Env = readinessEnv(env)
	return cmd.Run()
}

func readinessEnv(values map[string]string) []string {
	envMap := make(map[string]string)
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			envMap[key] = value
		}
	}
	for key, value := range values {
		envMap[key] = value
	}
	keys := make([]string, 0, len(envMap))
	for key := range envMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+envMap[key])
	}
	return env
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

func (b sdkBackend) EnsureNetwork(ctx context.Context, name string) (bool, error) {
	cli, err := b.client(ctx)
	if err != nil {
		return false, err
	}
	defer cli.Close()

	if _, err = sdknetwork.FindByName(ctx, name, sdknetwork.WithListClient(cli)); err == nil {
		return false, nil
	} else if !isMissingDockerResource(err) {
		return false, errors.Wrapf(err, "find docker network %s", name)
	}
	if _, err = sdknetwork.New(ctx, sdknetwork.WithClient(cli), sdknetwork.WithName(name)); err == nil {
		return true, nil
	} else if !isExistingDockerNetwork(err) {
		return false, errors.Wrapf(err, "create docker network %s", name)
	}
	if _, err = sdknetwork.FindByName(ctx, name, sdknetwork.WithListClient(cli)); err != nil {
		return false, errors.Wrapf(err, "find existing docker network %s", name)
	}
	return false, nil
}

func (b sdkBackend) EnsureVolume(ctx context.Context, name string) (bool, error) {
	cli, err := b.client(ctx)
	if err != nil {
		return false, err
	}
	defer cli.Close()

	if _, err = sdkvolume.FindByID(ctx, name, sdkvolume.WithFindClient(cli)); err == nil {
		return false, nil
	} else if !isMissingDockerResource(err) {
		return false, errors.Wrapf(err, "find docker volume %s", name)
	}
	if _, err = sdkvolume.New(ctx, sdkvolume.WithClient(cli), sdkvolume.WithName(name)); err != nil {
		if isExistingDockerVolume(err) {
			return false, nil
		}
		return false, errors.Wrapf(err, "create docker volume %s", name)
	}
	return true, nil
}

func (b sdkBackend) RemoveVolume(ctx context.Context, name string) error {
	cli, err := b.client(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	found, err := sdkvolume.FindByID(ctx, name, sdkvolume.WithFindClient(cli))
	if err != nil {
		return err
	}
	if err = found.Terminate(ctx, sdkvolume.WithForce()); err != nil {
		return errors.Wrapf(err, "remove docker volume %s", name)
	}
	return nil
}

func (b sdkBackend) EnsureContainer(ctx context.Context, spec ContainerSpec) (ContainerState, error) {
	cli, err := b.client(ctx)
	if err != nil {
		return ContainerState{}, err
	}
	defer cli.Close()

	found, err := cli.FindContainerByName(ctx, spec.Name)
	if err == nil {
		state, reused, err := reuseExistingContainer(ctx, cli, found, spec)
		if err != nil {
			return ContainerState{}, err
		}
		if reused {
			return state, nil
		}
		// the stale container was removed; create a fresh one below
	} else if !isMissingDockerResource(err) {
		return ContainerState{}, errors.Wrapf(err, "find docker container %s", spec.Name)
	}

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
		return ContainerState{}, errors.Wrapf(err, "run docker container %s", spec.Name)
	}
	return ContainerState{Created: true}, nil
}

// reuseExistingContainer reuses a found dependency container, reporting the
// host ports it is bound to. A stopped container that fails to start (for
// example its host port has since been taken by another process) is removed so
// the caller can create a fresh container bound to this run's ports.
func reuseExistingContainer(ctx context.Context, cli sdkclient.SDKClient, found *container.Summary, spec ContainerSpec) (ContainerState, bool, error) {
	inspect, err := cli.ContainerInspect(ctx, found.ID, client.ContainerInspectOptions{})
	if err != nil {
		return ContainerState{}, false, errors.Wrapf(err, "inspect docker container %s", spec.Name)
	}
	if found.State == "running" {
		return ContainerState{Ports: publishedPorts(inspect.Container)}, true, nil
	}
	startErr := error(nil)
	if _, startErr = cli.ContainerStart(ctx, found.ID, client.ContainerStartOptions{}); startErr == nil {
		return ContainerState{Ports: publishedPorts(inspect.Container)}, true, nil
	}
	if _, removeErr := cli.ContainerRemove(ctx, found.ID, client.ContainerRemoveOptions{Force: true}); removeErr != nil {
		return ContainerState{}, false, errors.Wrapf(removeErr, "remove unstartable docker container %s (start error: %v)", spec.Name, startErr)
	}
	return ContainerState{}, false, nil
}

// publishedPorts maps the container ports an existing container publishes to
// their bound host ports. Host config bindings cover stopped containers, while
// network settings reflect the live bindings of a running container and win
// when both are present.
func publishedPorts(inspect container.InspectResponse) map[string]string {
	ports := make(map[string]string)
	if inspect.HostConfig != nil {
		collectHostPorts(ports, inspect.HostConfig.PortBindings)
	}
	if inspect.NetworkSettings != nil {
		collectHostPorts(ports, inspect.NetworkSettings.Ports)
	}
	return ports
}

func collectHostPorts(ports map[string]string, bindings mobynetwork.PortMap) {
	for port, portBindings := range bindings {
		for _, binding := range portBindings {
			if binding.HostPort != "" {
				ports[portKey(port.String())] = binding.HostPort
				break
			}
		}
	}
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
	if _, err = cli.ContainerRemove(ctx, found.ID, client.ContainerRemoveOptions{Force: true}); err != nil {
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

func (c *CLI) containerSpec(ctx context.Context, network string, name string, dep envstarlark.Dependency, volumeBinds []string) (ContainerSpec, []portMapping, error) {
	ports, mappings, err := c.publishPorts(ctx, dep)
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
	return spec, mappings, nil
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

func (c *CLI) publishPorts(ctx context.Context, dep envstarlark.Dependency) ([]string, []portMapping, error) {
	if len(dep.Ports) == 0 {
		return nil, nil, nil
	}
	ports := make([]string, 0, len(dep.Ports))
	mappings := make([]portMapping, 0, len(dep.Ports))
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
			port.Allocated = true
		}
		ports = append(ports, port.Host+":"+port.Container)
		mappings = append(mappings, port)
	}
	return ports, mappings, nil
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
	// Allocated marks host ports picked by the allocator rather than pinned in
	// configuration; they defer to an existing container's actual binding.
	Allocated bool
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
