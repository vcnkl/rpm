package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/vcnkl/rpm/actions"
	rootconfig "github.com/vcnkl/rpm/config"
	envruntime "github.com/vcnkl/rpm/environments/runtime"
	"github.com/vcnkl/rpm/environments/runtime/docker"
	envstarlark "github.com/vcnkl/rpm/environments/starlark"
)

func buildRpmBinary(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "rpm")

	rpmDir, _ := filepath.Abs("..")
	t.Logf("Building rpm from: %s", rpmDir)
	t.Logf("Output binary: %s", binaryPath)

	cmd := exec.Command("go", "build", "-a", "-o", binaryPath, ".")
	cmd.Dir = rpmDir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	output, err := cmd.CombinedOutput()
	t.Logf("Build output: %s", string(output))
	require.NoError(t, err, "failed to build rpm binary: %s", string(output))

	info, _ := os.Stat(binaryPath)
	t.Logf("Binary size: %d bytes", info.Size())

	return binaryPath
}

func startTestContainer(t *testing.T, ctx context.Context) testcontainers.Container {
	t.Helper()

	binaryPath := buildRpmBinary(t)
	testdataDir, err := filepath.Abs("testdata")
	require.NoError(t, err)

	ctr, err := testcontainers.Run(ctx, "golang:1.24-alpine",
		testcontainers.WithFiles(
			testcontainers.ContainerFile{
				HostFilePath:      binaryPath,
				ContainerFilePath: "/usr/local/bin/rpm",
				FileMode:          0o755,
			},
		),
		testcontainers.WithCmd("tail", "-f", "/dev/null"),
		testcontainers.WithWaitStrategy(
			wait.ForExec([]string{"sh", "-c", "apk update && apk add --no-cache git bash"}).
				WithStartupTimeout(180*time.Second).
				WithPollInterval(2*time.Second),
		),
	)
	require.NoError(t, err, "failed to start container")

	err = ctr.CopyDirToContainer(ctx, filepath.Join(testdataDir, "sample-repo"), "/", 0o755)
	require.NoError(t, err, "failed to copy testdata to container")

	exitCode, reader, err := ctr.Exec(ctx, []string{"sh", "-c", `
		set -e
		echo "=== Checking rpm binary ==="
		ls -la /usr/local/bin/rpm
		/usr/local/bin/rpm env --help
		echo "=== Setting up workspace ==="
		mv /sample-repo /workspace
		cd /workspace
		git config --global --add safe.directory /workspace
		git config --global user.email "test@test.com"
		git config --global user.name "Test"
		git init .
		git add -A
		git commit -m "Initial commit"
	`})
	if exitCode != 0 {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(reader)
		t.Logf("git init failed with exit code %d: %s", exitCode, buf.String())
	}
	require.NoError(t, err)
	require.Zero(t, exitCode, "git init should succeed")

	return ctr
}

func shouldSkip(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if os.Getenv("SKIP_INTEGRATION") == "true" {
		t.Skip("skipping integration test via SKIP_INTEGRATION env var")
	}
}

func TestIntegration_EnvUpNonInteractiveBypassesNodeTUI(t *testing.T) {
	shouldSkip(t)
	t.Parallel()

	repo := rootconfig.NewConfigWithRepoFile(filepath.Join("testdata", "sample-repo", "repo.yml"))
	action := actions.NewEnvAction(repo, nil, nil)

	err := action.Up(context.Background(), actions.EnvUpOptions{
		Blueprint:      "local-stack",
		NoDeps:         true,
		NoReload:       true,
		NonInteractive: true,
	})

	require.NoError(t, err)
}

func TestIntegration_EnvUpResolvesDependencyPortsInDotenv(t *testing.T) {
	shouldSkip(t)

	repoDir := copySampleRepo(t)
	t.Cleanup(removeSampleRepoVolumes)
	repo := rootconfig.NewConfigWithRepoFile(filepath.Join(repoDir, "repo.yml"))
	var out bytes.Buffer
	action := actions.NewEnvAction(repo, &out, &out)

	err := action.Up(context.Background(), actions.EnvUpOptions{
		Blueprint:      "local-stack",
		NoReload:       true,
		NonInteractive: true,
	})

	output := out.String()
	require.NoError(t, err, output)

	portMatch := regexp.MustCompile(`POSTGRES_PORT=(\d+)`).FindStringSubmatch(output)
	require.NotNil(t, portMatch, "postgres port missing from output: %s", output)
	port := portMatch[1]

	assert.Contains(t, output, "BEFORE_POSTGRES_URI=postgresql://localhost:"+port+"/app",
		"before target must receive the dotenv value with the dependency port substituted: %s", output)
	assert.Contains(t, output, "POSTGRES_URI=postgresql://localhost:"+port+"/app",
		"target must receive the dotenv value with the dependency port substituted: %s", output)
	assert.NotContains(t, output, "localhost:${POSTGRES_PORT}", "placeholder must not leak into processes")
	assert.NotContains(t, output, "localhost:/app", "port must not resolve to an empty string")

	envData, err := os.ReadFile(filepath.Join(repoDir, "apps", "go-app", ".env"))
	require.NoError(t, err)
	assert.Contains(t, string(envData), "POSTGRES_PORT="+port+"\n",
		"dependency port must be defined in the dotenv file's own scope for apps that reload it themselves")
	assert.Contains(t, string(envData), "POSTGRES_URI=postgresql://localhost:${POSTGRES_PORT}/app\n",
		"the user's placeholder value must stay intact")
}

func copySampleRepo(t *testing.T) string {
	t.Helper()
	src, err := filepath.Abs(filepath.Join("testdata", "sample-repo"))
	require.NoError(t, err)
	dst := filepath.Join(t.TempDir(), "sample-repo")
	require.NoError(t, filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	}))
	return dst
}

func removeSampleRepoVolumes() {
	names, err := exec.Command("docker", "volume", "ls", "-q", "--filter", "name=sample-repo-").Output()
	if err != nil {
		return
	}
	for _, name := range strings.Fields(string(names)) {
		_ = exec.Command("docker", "volume", "rm", name).Run()
	}
}

func TestIntegration_EnvHelp(t *testing.T) {
	shouldSkip(t)

	ctx := context.Background()
	ctr := startTestContainer(t, ctx)
	defer testcontainers.CleanupContainer(t, ctr)

	exitCode, reader, err := ctr.Exec(ctx, []string{"rpm", "env", "--help"})
	require.NoError(t, err)

	var buf bytes.Buffer
	_, err = buf.ReadFrom(reader)
	require.NoError(t, err)

	output := buf.String()
	t.Logf("rpm env --help output (exit code %d): %s", exitCode, output)

	assert.Zero(t, exitCode)
	assert.Contains(t, output, "create")
	assert.Contains(t, output, "edit")
	assert.Contains(t, output, "validate")
	assert.Contains(t, output, "render")
	assert.Contains(t, output, "up")
	assert.Contains(t, output, "down")
}

func TestIntegration_EnvUpNonInteractiveNoDepsNoReload(t *testing.T) {
	shouldSkip(t)

	ctx := context.Background()
	ctr := startTestContainer(t, ctx)
	defer testcontainers.CleanupContainer(t, ctr)

	exitCode, reader, err := ctr.Exec(ctx, []string{"sh", "-c", "cd /workspace && rpm env up local-stack --non-interactive --no-deps --no-reload"})
	require.NoError(t, err)

	var buf bytes.Buffer
	_, err = buf.ReadFrom(reader)
	require.NoError(t, err)

	output := buf.String()
	output = stripDockerStreamHeaders(output)
	t.Logf("rpm env up output (exit code %d): %s", exitCode, output)

	assert.Zero(t, exitCode)
	assert.Contains(t, output, `"type":"process_started","ref":"go-app:prepare"`)
	assert.Contains(t, output, `"type":"process_exited","ref":"go-app:prepare"`)
	assert.Contains(t, output, `"type":"process_started","ref":"go-app:before_file"`)
	assert.Contains(t, output, `"type":"process_exited","ref":"go-app:before_file"`)
	assert.Contains(t, output, `"type":"process_started","ref":"go-app:before_inline"`)
	assert.Contains(t, output, `"type":"process_exited","ref":"go-app:before_inline"`)
	assert.Contains(t, output, `"type":"process_started","ref":"go-app:app_build"`)
	assert.Contains(t, output, `"type":"process_exited","ref":"go-app:app_build"`)
	assert.Contains(t, output, `"type":"process_started","ref":"ts-app:app_build"`)
	assert.Contains(t, output, `"type":"process_exited","ref":"ts-app:app_build"`)
	assert.Contains(t, output, `"type":"process_started","ref":"go-app:echo-123"`)
	assert.Contains(t, output, `"type":"process_exited","ref":"go-app:echo-123"`)
	assert.Contains(t, output, `"type":"process_started","ref":"python-app:echo-456"`)
	assert.Contains(t, output, `"type":"process_exited","ref":"python-app:echo-456"`)
	assert.Contains(t, output, `"type":"process_started","ref":"ts-app:web"`)
	assert.Contains(t, output, `"type":"process_exited","ref":"ts-app:web"`)
	assert.Contains(t, output, "REPO_ROOT=/workspace")
	assert.Contains(t, output, "BUNDLE_ROOT=/workspace/apps/")
	assert.Contains(t, output, "BEFORE_FILE=/workspace/apps/go-app")
	assert.Less(t, strings.Index(output, `"ref":"go-app:prepare"`), strings.Index(output, `"ref":"go-app:echo-123"`))
	assert.Less(t, strings.Index(output, `"ref":"go-app:before_file"`), strings.Index(output, `"ref":"go-app:echo-123"`))
	assert.Less(t, strings.Index(output, `"ref":"go-app:before_inline"`), strings.Index(output, `"ref":"go-app:echo-123"`))
	assert.Less(t, strings.Index(output, `"ref":"go-app:before_inline"`), strings.Index(output, `"ref":"go-app:app_build"`))
	assert.Less(t, strings.Index(output, `"type":"process_exited","ref":"go-app:app_build"`), strings.Index(output, `"type":"process_started","ref":"go-app:echo-123"`))
	assert.Less(t, strings.Index(output, `"type":"process_exited","ref":"ts-app:app_build"`), strings.Index(output, `"type":"process_started","ref":"ts-app:web"`))
}

func TestIntegration_EnvCommandsEndToEnd(t *testing.T) {
	shouldSkip(t)

	ctx := context.Background()
	ctr := startTestContainer(t, ctx)
	defer testcontainers.CleanupContainer(t, ctr)

	validate := runWorkspaceCommand(t, ctx, ctr, "rpm env validate local-stack")
	assert.Zero(t, validate.exitCode, validate.output)

	render := runWorkspaceCommand(t, ctx, ctr, "rpm env render local-stack")
	assert.Zero(t, render.exitCode, render.output)

	golden, err := os.ReadFile(filepath.Join("testdata", "golden", "local-stack.star"))
	require.NoError(t, err)
	rendered := runWorkspaceCommand(t, ctx, ctr, "sed 's#${REPO_ROOT}#<repo>#g' .rpm/envs/local-stack/runtime.gen.star")
	assert.Zero(t, rendered.exitCode, rendered.output)
	assert.Equal(t, string(golden), rendered.output)

	create := runWorkspaceCommand(t, ctx, ctr, "rpm env create --non-interactive smoke --target go-app:echo-123 --deps")
	assert.Zero(t, create.exitCode, create.output)
	created := runWorkspaceCommand(t, ctx, ctr, "rpm env validate smoke")
	assert.Zero(t, created.exitCode, created.output)

	devHelp := runWorkspaceCommand(t, ctx, ctr, "rpm dev --help")
	assert.Zero(t, devHelp.exitCode, devHelp.output)
	assert.Contains(t, strings.ToLower(devHelp.output), "file watching")

	dockerBuild := runWorkspaceCommand(t, ctx, ctr, "rpm build --"+"docker")
	assert.NotZero(t, dockerBuild.exitCode, dockerBuild.output)
	assert.Contains(t, strings.ToLower(dockerBuild.output), "flag")
}

func TestIntegration_BuildCommand(t *testing.T) {
	shouldSkip(t)

	ctx := context.Background()
	ctr := startTestContainer(t, ctx)
	defer testcontainers.CleanupContainer(t, ctr)

	tests := []struct {
		name        string
		command     []string
		expectError bool
		checkOutput func(t *testing.T, output string)
	}{
		{
			name:        "build go-app bundle",
			command:     []string{"rpm", "build", "--dry-run", "go-app"},
			expectError: false,
			checkOutput: func(t *testing.T, output string) {
				assert.Contains(t, output, "go-app:app_build")
			},
		},
		{
			name:        "build specific target",
			command:     []string{"rpm", "build", "--dry-run", "go-app:app_build"},
			expectError: false,
			checkOutput: func(t *testing.T, output string) {
				assert.Contains(t, output, "app_build")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shellCmd := "cd /workspace && " + strings.Join(tt.command, " ")
			exitCode, reader, err := ctr.Exec(ctx, []string{"sh", "-c", shellCmd})
			require.NoError(t, err)

			var buf bytes.Buffer
			_, err = buf.ReadFrom(reader)
			require.NoError(t, err)

			output := buf.String()
			t.Logf("Command output: %s", output)

			if tt.expectError {
				assert.NotZero(t, exitCode, "command should fail")
			} else {
				assert.Zerof(t, exitCode, "command should succeed, output: %s", output)
			}

			if tt.checkOutput != nil {
				tt.checkOutput(t, output)
			}
		})
	}
}

type commandResult struct {
	exitCode int
	output   string
}

func runWorkspaceCommand(t *testing.T, ctx context.Context, ctr testcontainers.Container, command string) commandResult {
	t.Helper()
	exitCode, reader, err := ctr.Exec(ctx, []string{"sh", "-c", "cd /workspace && " + command})
	require.NoError(t, err)
	var buf bytes.Buffer
	_, err = buf.ReadFrom(reader)
	require.NoError(t, err)
	return commandResult{exitCode: exitCode, output: stripDockerStreamHeaders(buf.String())}
}

func stripDockerStreamHeaders(output string) string {
	data := []byte(output)
	cleaned := make([]byte, 0, len(data))
	for len(data) >= 8 && (data[0] == 1 || data[0] == 2) && data[1] == 0 && data[2] == 0 && data[3] == 0 {
		size := int(data[4])<<24 | int(data[5])<<16 | int(data[6])<<8 | int(data[7])
		if size < 0 || size > len(data)-8 {
			break
		}
		cleaned = append(cleaned, data[8:8+size]...)
		data = data[8+size:]
	}
	if len(cleaned) == 0 {
		return output
	}
	cleaned = append(cleaned, data...)
	return string(cleaned)
}

func TestIntegration_TargetResolution(t *testing.T) {
	shouldSkip(t)

	ctx := context.Background()
	ctr := startTestContainer(t, ctx)
	defer testcontainers.CleanupContainer(t, ctr)

	tests := []struct {
		name              string
		command           []string
		expectError       bool
		expectedTargets   []string
		unexpectedTargets []string
	}{
		{
			name:            "build resolves bundle to build targets",
			command:         []string{"rpm", "build", "--dry-run", "go-app"},
			expectedTargets: []string{"go-app:app_build"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shellCmd := "cd /workspace && " + strings.Join(tt.command, " ")
			exitCode, reader, err := ctr.Exec(ctx, []string{"sh", "-c", shellCmd})
			require.NoError(t, err)

			var buf bytes.Buffer
			_, err = buf.ReadFrom(reader)
			require.NoError(t, err)

			output := buf.String()
			t.Logf("Command output: %s", output)

			if tt.expectError {
				assert.NotZero(t, exitCode, "command should fail")
			} else {
				assert.Zerof(t, exitCode, "command should succeed, output: %s", output)
			}

			for _, target := range tt.expectedTargets {
				assert.Contains(t, output, target,
					"output should contain target %s", target)
			}

			for _, target := range tt.unexpectedTargets {
				assert.NotContains(t, output, target,
					"output should not contain target %s", target)
			}
		})
	}
}

func TestIntegration_WorkingDirectory(t *testing.T) {
	shouldSkip(t)

	ctx := context.Background()
	ctr := startTestContainer(t, ctx)
	defer testcontainers.CleanupContainer(t, ctr)

	t.Run("local working dir runs in bundle directory", func(t *testing.T) {
		exitCode, reader, err := ctr.Exec(ctx, []string{"sh", "-c", "cd /workspace/apps/go-app && pwd"})
		require.NoError(t, err)

		var buf bytes.Buffer
		_, _ = buf.ReadFrom(reader)
		output := strings.TrimSpace(buf.String())

		assert.Zero(t, exitCode)
		assert.Contains(t, output, "/workspace/apps/go-app")
	})
}

func TestIntegration_ErrorHandling(t *testing.T) {
	shouldSkip(t)

	ctx := context.Background()
	ctr := startTestContainer(t, ctx)
	defer testcontainers.CleanupContainer(t, ctr)

	tests := []struct {
		name           string
		command        []string
		expectedErrors []string
	}{
		{
			name:           "nonexistent bundle returns error",
			command:        []string{"rpm", "build", "nonexistent"},
			expectedErrors: []string{"target not found"},
		},
		{
			name:           "nonexistent target returns error",
			command:        []string{"rpm", "build", "go-app:nonexistent_build"},
			expectedErrors: []string{"target not found"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shellCmd := "cd /workspace && " + strings.Join(tt.command, " ")
			exitCode, reader, err := ctr.Exec(ctx, []string{"sh", "-c", shellCmd})
			require.NoError(t, err)

			var buf bytes.Buffer
			_, err = buf.ReadFrom(reader)
			require.NoError(t, err)

			output := buf.String()
			t.Logf("Command output: %s", output)

			assert.NotZero(t, exitCode, "command should fail with nonexistent target")

			for _, expectedError := range tt.expectedErrors {
				assert.Contains(t, strings.ToLower(output), strings.ToLower(expectedError),
					"output should contain error message: %s", expectedError)
			}
		})
	}
}

// Docker dependency runtime tests exercising CLI.Up/Down orchestration
// (container lifecycle, port publication, readiness, cleanup) through the
// exported docker API against a recording backend.

func TestUpStartsSharedDependencyWithNetworkVolumeEnvPortsAndBinds(t *testing.T) {
	shouldSkip(t)

	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{Backend: backend, VolumeNamer: fixedVolumeNamer{
		names: map[string]string{"local-stack|postgres|/var/lib/postgresql/data": "sample-repo-postgres-local-stack-123456"},
	}})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:     "postgres",
			Name:    "postgres",
			Image:   "postgres:16",
			Env:     map[string]string{"POSTGRES_PASSWORD": "example"},
			Ports:   []string{"5432:5432"},
			Volumes: []string{"/var/lib/postgresql/data"},
		}},
	}

	startup, err := runner.Up(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"network rpm-local-stack",
		"volume sample-repo-postgres-local-stack-123456",
		"run rpm-local-stack-postgres",
	}, backend.calls)
	assert.Equal(t, []docker.ContainerSpec{{
		Name:    "rpm-local-stack-postgres",
		Image:   "postgres:16",
		Network: "rpm-local-stack",
		Env:     map[string]string{"POSTGRES_PASSWORD": "example"},
		Ports:   []string{"5432:5432"},
		Volumes: []string{"sample-repo-postgres-local-stack-123456:/var/lib/postgresql/data"},
	}}, backend.containers)
	assert.Equal(t, map[string]string{"POSTGRES_PORT": "5432"}, startup.Env)
}

func TestUpReusesExistingDockerNetwork(t *testing.T) {
	shouldSkip(t)

	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{Backend: backend})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:   "postgres",
			Name:  "postgres",
			Image: "postgres:16",
		}},
	}

	startup, err := runner.Up(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"network rpm-local-stack",
		"run rpm-local-stack-postgres",
	}, backend.calls)
	assert.Equal(t, []docker.ContainerSpec{{
		Name:    "rpm-local-stack-postgres",
		Image:   "postgres:16",
		Network: "rpm-local-stack",
	}}, backend.containers)
	assert.Empty(t, startup.Env)
}

func TestUpAllocatesDynamicHostPortForSingleContainerPort(t *testing.T) {
	shouldSkip(t)

	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{
		Backend: backend,
		PortAllocator: &fixedPortAllocator{
			port: 49152,
		},
	})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:   "postgres",
			Name:  "postgres",
			Image: "postgres:16",
			Ports: []string{"5432"},
		}},
	}

	startup, err := runner.Up(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []docker.ContainerSpec{{
		Name:    "rpm-local-stack-postgres",
		Image:   "postgres:16",
		Network: "rpm-local-stack",
		Ports:   []string{"49152:5432"},
	}}, backend.containers)
	assert.Equal(t, map[string]string{"POSTGRES_PORT": "49152"}, startup.Env)
}

func TestUpAllocatesDynamicHostPortsForMultipleBarePorts(t *testing.T) {
	shouldSkip(t)

	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{
		Backend: backend,
		PortAllocator: &fixedPortAllocator{
			port: 49152,
		},
	})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:   "mailhog",
			Name:  "mailhog",
			Image: "mailhog/mailhog:v1.0.1",
			Ports: []string{"1025", "8025"},
		}},
	}

	startup, err := runner.Up(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []docker.ContainerSpec{{
		Name:    "rpm-local-stack-mailhog",
		Image:   "mailhog/mailhog:v1.0.1",
		Network: "rpm-local-stack",
		Ports:   []string{"49152:1025", "49153:8025"},
	}}, backend.containers)
	assert.Equal(t, map[string]string{
		"MAILHOG_PORT_1025": "49152",
		"MAILHOG_PORT_8025": "49153",
	}, startup.Env)
}

func TestUpMixesExplicitAndDynamicDependencyPorts(t *testing.T) {
	shouldSkip(t)

	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{
		Backend: backend,
		PortAllocator: &fixedPortAllocator{
			port: 49153,
		},
	})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{
			{
				Ref:   "redis",
				Name:  "redis",
				Image: "redis:7",
				Ports: []string{"6379:6379"},
			},
			{
				Ref:   "mailhog",
				Name:  "mailhog",
				Image: "mailhog/mailhog:v1.0.1",
				Ports: []string{"1025"},
			},
		},
		Targets: []envstarlark.TargetProcess{
			{Ref: "python-app:echo-456"},
			{Ref: "ts-app:web"},
		},
	}

	startup, err := runner.Up(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []docker.ContainerSpec{
		{
			Name:    "rpm-local-stack-redis",
			Image:   "redis:7",
			Network: "rpm-local-stack",
			Ports:   []string{"6379:6379"},
		},
		{
			Name:    "rpm-local-stack-mailhog",
			Image:   "mailhog/mailhog:v1.0.1",
			Network: "rpm-local-stack",
			Ports:   []string{"49153:1025"},
		},
	}, backend.containers)
	assert.Equal(t, map[string]string{
		"REDIS_PORT":   "6379",
		"MAILHOG_PORT": "49153",
	}, startup.Env)
}

func TestUpUsesHostPortFromExplicitHostBinding(t *testing.T) {
	shouldSkip(t)

	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{Backend: backend})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:   "postgres",
			Name:  "postgres",
			Image: "postgres:16",
			Ports: []string{"127.0.0.1:5433:5432"},
		}},
	}

	startup, err := runner.Up(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []docker.ContainerSpec{{
		Name:    "rpm-local-stack-postgres",
		Image:   "postgres:16",
		Network: "rpm-local-stack",
		Ports:   []string{"127.0.0.1:5433:5432"},
	}}, backend.containers)
	assert.Equal(t, map[string]string{"POSTGRES_PORT": "5433"}, startup.Env)
}

func TestUpUsesCustomPortEnvName(t *testing.T) {
	shouldSkip(t)

	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{
		Backend: backend,
		PortAllocator: &fixedPortAllocator{
			port: 49152,
		},
	})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:   "mongodb",
			Name:  "mongodb",
			Image: "mongo:8.0.23-noble",
			Ports: []string{"MONGODB_PORT=27017"},
		}},
	}

	startup, err := runner.Up(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []docker.ContainerSpec{{
		Name:    "rpm-local-stack-mongodb",
		Image:   "mongo:8.0.23-noble",
		Network: "rpm-local-stack",
		Ports:   []string{"49152:27017"},
	}}, backend.containers)
	assert.Equal(t, map[string]string{"MONGODB_PORT": "49152"}, startup.Env)
}

func TestUpRunsDependencyReadinessWithContainerNameAndResolvedPorts(t *testing.T) {
	shouldSkip(t)

	backend := &recordingBackend{}
	readiness := &recordingReadinessRunner{}
	runner := docker.NewCLI(docker.Options{
		Backend:         backend,
		ReadinessRunner: readiness,
		Shell:           "/bin/bash",
		PortAllocator: &fixedPortAllocator{
			port: 49152,
		},
	})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:          "postgres",
			Name:         "postgres",
			Image:        "postgres:16",
			Ports:        []string{"POSTGRES_PORT=5432"},
			ReadinessCmd: `docker exec ${DOCKER_CONTAINER_NAME} pg_isready`,
		}},
	}

	startup, err := runner.Up(context.Background(), "local-stack", plan)

	require.NoError(t, err)
	assert.Equal(t, map[string]string{"POSTGRES_PORT": "49152"}, startup.Env)
	require.Len(t, readiness.calls, 1)
	assert.Equal(t, "/bin/bash", readiness.calls[0].shell)
	assert.Equal(t, `docker exec ${DOCKER_CONTAINER_NAME} pg_isready`, readiness.calls[0].command)
	assert.Equal(t, "rpm-local-stack-postgres", readiness.calls[0].env["DOCKER_CONTAINER_NAME"])
	assert.Equal(t, "49152", readiness.calls[0].env["POSTGRES_PORT"])
	assert.Equal(t, []string{
		"network rpm-local-stack",
		"run rpm-local-stack-postgres",
	}, backend.calls)
}

func TestUpReturnsDependencyReadinessFailure(t *testing.T) {
	shouldSkip(t)

	backend := &recordingBackend{}
	readiness := &recordingReadinessRunner{err: assert.AnError}
	runner := docker.NewCLI(docker.Options{Backend: backend, ReadinessRunner: readiness})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:          "postgres",
			Name:         "postgres",
			Image:        "postgres:16",
			ReadinessCmd: "false",
		}},
	}

	_, err := runner.Up(context.Background(), "local-stack", plan)

	require.ErrorIs(t, err, assert.AnError)
	var depErr envruntime.DependencyError
	require.True(t, errors.As(err, &depErr))
	assert.Equal(t, "postgres", depErr.Ref)
	assert.Contains(t, err.Error(), "postgres readiness check failed")
	require.Len(t, readiness.calls, 1)
	assert.Equal(t, "rpm-local-stack-postgres", readiness.calls[0].env["DOCKER_CONTAINER_NAME"])
	assert.Equal(t, []string{
		"network rpm-local-stack",
		"run rpm-local-stack-postgres",
		"remove-container rpm-local-stack-postgres",
		"remove-network rpm-local-stack",
	}, backend.calls)
}

func TestUpRejectsInvalidCustomPortEnvName(t *testing.T) {
	shouldSkip(t)

	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{Backend: backend})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:   "mongodb",
			Name:  "mongodb",
			Image: "mongo:8.0.23-noble",
			Ports: []string{"MONGO-PORT=27017"},
		}},
	}

	_, err := runner.Up(context.Background(), "local-stack", plan)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid env name "MONGO-PORT"`)
}

func TestUpBuildsOneDockerContainerPerDependency(t *testing.T) {
	shouldSkip(t)

	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{
		Backend: backend,
		PortAllocator: &fixedPortAllocator{
			port: 49152,
		},
		VolumeNamer: fixedVolumeNamer{
			names: map[string]string{"dev|postgres|/var/lib/postgresql/data": "sample-repo-postgres-dev-123456"},
		},
	})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{
			{
				Ref:   "mongodb",
				Name:  "mongodb",
				Image: "mongo:8.0.23-noble",
				Ports: []string{"MONGODB_PORT=27017"},
			},
			{
				Ref:     "postgres",
				Name:    "postgres",
				Image:   "postgis/postgis:17-3.5",
				Env:     map[string]string{"POSTGRES_PASSWORD": "example"},
				Ports:   []string{"POSTGRES_PORT=5432"},
				Volumes: []string{"/var/lib/postgresql/data"},
			},
			{
				Ref:   "rabbitmq",
				Name:  "rabbitmq",
				Image: "rabbitmq:4.1.3",
				Ports: []string{"RABBITMQ_PORT=5672"},
			},
			{
				Ref:   "redis",
				Name:  "redis",
				Image: "redis:7",
				Ports: []string{"REDIS_PORT=6379"},
			},
		},
	}

	startup, err := runner.Up(context.Background(), "dev", plan)
	require.NoError(t, err)

	assert.Equal(t, []docker.ContainerSpec{
		{
			Name:    "rpm-dev-mongodb",
			Image:   "mongo:8.0.23-noble",
			Network: "rpm-dev",
			Ports:   []string{"49152:27017"},
		},
		{
			Name:    "rpm-dev-postgres",
			Image:   "postgis/postgis:17-3.5",
			Network: "rpm-dev",
			Env:     map[string]string{"POSTGRES_PASSWORD": "example"},
			Ports:   []string{"49153:5432"},
			Volumes: []string{"sample-repo-postgres-dev-123456:/var/lib/postgresql/data"},
		},
		{
			Name:    "rpm-dev-rabbitmq",
			Image:   "rabbitmq:4.1.3",
			Network: "rpm-dev",
			Ports:   []string{"49154:5672"},
		},
		{
			Name:    "rpm-dev-redis",
			Image:   "redis:7",
			Network: "rpm-dev",
			Ports:   []string{"49155:6379"},
		},
	}, backend.containers)
	assert.Equal(t, map[string]string{
		"MONGODB_PORT":  "49152",
		"POSTGRES_PORT": "49153",
		"RABBITMQ_PORT": "49154",
		"REDIS_PORT":    "49155",
	}, startup.Env)
}

func TestUpCollapsesIdenticalDuplicateDependencies(t *testing.T) {
	shouldSkip(t)

	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{Backend: backend})
	dependency := envstarlark.Dependency{
		Ref:          "rabbitmq",
		Name:         "rabbitmq",
		Image:        "rabbitmq:4.1.3",
		Ports:        []string{"5672:5672"},
		ReadinessCmd: "true",
	}
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{dependency, dependency},
	}

	startup, err := runner.Up(context.Background(), "dev", plan)

	require.NoError(t, err)
	assert.Equal(t, []string{
		"network rpm-dev",
		"run rpm-dev-rabbitmq",
	}, backend.calls)
	assert.Equal(t, []docker.ContainerSpec{{
		Name:    "rpm-dev-rabbitmq",
		Image:   "rabbitmq:4.1.3",
		Network: "rpm-dev",
		Ports:   []string{"5672:5672"},
	}}, backend.containers)
	assert.Equal(t, map[string]string{"RABBITMQ_PORT": "5672"}, startup.Env)
}

func TestUpRejectsConflictingDuplicateDependencyContainersBeforeStartup(t *testing.T) {
	shouldSkip(t)

	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{Backend: backend})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{
			{Ref: "rabbitmq", Name: "rabbitmq", Image: "rabbitmq:4.1.3", Ports: []string{"5672:5672"}},
			{Ref: "rabbitmq-alt", Name: "rabbitmq", Image: "rabbitmq:3-management", Ports: []string{"5673:5672"}},
		},
	}

	_, err := runner.Up(context.Background(), "dev", plan)

	require.Error(t, err)
	var depErr envruntime.DependencyError
	require.True(t, errors.As(err, &depErr))
	assert.Equal(t, "rabbitmq-alt", depErr.Ref)
	assert.Contains(t, err.Error(), `duplicate dependency container "rpm-dev-rabbitmq"`)
	assert.Empty(t, backend.calls)
	assert.Empty(t, backend.containers)
}

func TestUpStartsExistingStoppedDependencyContainer(t *testing.T) {
	shouldSkip(t)

	backend := &recordingBackend{
		existingContainers: map[string]string{"rpm-dev-rabbitmq": "exited"},
	}
	readiness := &recordingReadinessRunner{}
	runner := docker.NewCLI(docker.Options{Backend: backend, ReadinessRunner: readiness})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:          "rabbitmq",
			Name:         "rabbitmq",
			Image:        "rabbitmq:4.1.3",
			Ports:        []string{"5672:5672"},
			ReadinessCmd: "true",
		}},
	}

	startup, err := runner.Up(context.Background(), "dev", plan)

	require.NoError(t, err)
	assert.Equal(t, []string{
		"network rpm-dev",
		"start rpm-dev-rabbitmq",
	}, backend.calls)
	assert.Empty(t, backend.containers)
	assert.Equal(t, map[string]string{"RABBITMQ_PORT": "5672"}, startup.Env)
	require.Len(t, readiness.calls, 1)
	assert.Equal(t, "rpm-dev-rabbitmq", readiness.calls[0].env["DOCKER_CONTAINER_NAME"])
}

func TestUpReusesExistingRunningDependencyContainer(t *testing.T) {
	shouldSkip(t)

	backend := &recordingBackend{
		existingContainers: map[string]string{"rpm-dev-rabbitmq": "running"},
	}
	readiness := &recordingReadinessRunner{}
	runner := docker.NewCLI(docker.Options{Backend: backend, ReadinessRunner: readiness})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:          "rabbitmq",
			Name:         "rabbitmq",
			Image:        "rabbitmq:4.1.3",
			Ports:        []string{"5672:5672"},
			ReadinessCmd: "true",
		}},
	}

	startup, err := runner.Up(context.Background(), "dev", plan)

	require.NoError(t, err)
	assert.Equal(t, []string{
		"network rpm-dev",
		"reuse rpm-dev-rabbitmq",
	}, backend.calls)
	assert.Empty(t, backend.containers)
	assert.Equal(t, map[string]string{"RABBITMQ_PORT": "5672"}, startup.Env)
	require.Len(t, readiness.calls, 1)
	assert.Equal(t, "rpm-dev-rabbitmq", readiness.calls[0].env["DOCKER_CONTAINER_NAME"])
}

func TestUpReportsActualPortForReusedDependencyContainer(t *testing.T) {
	shouldSkip(t)

	backend := &recordingBackend{
		existingContainers:     map[string]string{"rpm-dev-mongodb": "running"},
		existingContainerPorts: map[string]map[string]string{"rpm-dev-mongodb": {"27017/tcp": "49152"}},
	}
	readiness := &recordingReadinessRunner{}
	runner := docker.NewCLI(docker.Options{
		Backend:         backend,
		PortAllocator:   &fixedPortAllocator{port: 49807},
		ReadinessRunner: readiness,
	})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:          "mongodb",
			Name:         "mongodb",
			Image:        "mongo:7",
			Ports:        []string{"27017"},
			ReadinessCmd: "true",
		}},
	}

	startup, err := runner.Up(context.Background(), "dev", plan)

	require.NoError(t, err)
	assert.Equal(t, []string{
		"network rpm-dev",
		"reuse rpm-dev-mongodb",
	}, backend.calls)
	assert.Equal(t, map[string]string{"MONGODB_PORT": "49152"}, startup.Env)
	require.Len(t, readiness.calls, 1)
	assert.Equal(t, "49152", readiness.calls[0].env["MONGODB_PORT"])
}

func TestUpReportsActualPortForRestartedDependencyContainer(t *testing.T) {
	shouldSkip(t)

	backend := &recordingBackend{
		existingContainers:     map[string]string{"rpm-dev-mongodb": "exited"},
		existingContainerPorts: map[string]map[string]string{"rpm-dev-mongodb": {"27017/tcp": "49152"}},
	}
	runner := docker.NewCLI(docker.Options{
		Backend:       backend,
		PortAllocator: &fixedPortAllocator{port: 49807},
	})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:   "mongodb",
			Name:  "mongodb",
			Image: "mongo:7",
			Ports: []string{"27017"},
		}},
	}

	startup, err := runner.Up(context.Background(), "dev", plan)

	require.NoError(t, err)
	assert.Equal(t, []string{
		"network rpm-dev",
		"start rpm-dev-mongodb",
	}, backend.calls)
	assert.Equal(t, map[string]string{"MONGODB_PORT": "49152"}, startup.Env)
}

func TestUpRecreatesReusedContainerWhenDeclaredPortNotPublished(t *testing.T) {
	shouldSkip(t)

	backend := &recordingBackend{
		existingContainers:     map[string]string{"rpm-dev-mongodb": "running"},
		existingContainerPorts: map[string]map[string]string{"rpm-dev-mongodb": {}},
	}
	runner := docker.NewCLI(docker.Options{
		Backend:       backend,
		PortAllocator: &fixedPortAllocator{port: 49807},
	})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:   "mongodb",
			Name:  "mongodb",
			Image: "mongo:7",
			Ports: []string{"27017"},
		}},
	}

	startup, err := runner.Up(context.Background(), "dev", plan)

	require.NoError(t, err)
	assert.Equal(t, []string{
		"network rpm-dev",
		"reuse rpm-dev-mongodb",
		"remove-container rpm-dev-mongodb",
		"run rpm-dev-mongodb",
	}, backend.calls)
	require.Len(t, backend.containers, 1)
	assert.Equal(t, []string{"49807:27017"}, backend.containers[0].Ports)
	assert.Equal(t, map[string]string{"MONGODB_PORT": "49807"}, startup.Env)
}

func TestUpRecreatesReusedContainerWhenPinnedHostPortChanged(t *testing.T) {
	shouldSkip(t)

	backend := &recordingBackend{
		existingContainers:     map[string]string{"rpm-dev-postgres": "running"},
		existingContainerPorts: map[string]map[string]string{"rpm-dev-postgres": {"5432/tcp": "49152"}},
	}
	runner := docker.NewCLI(docker.Options{Backend: backend})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{
			Ref:   "postgres",
			Name:  "postgres",
			Image: "postgres:16",
			Ports: []string{"5432:5432"},
		}},
	}

	startup, err := runner.Up(context.Background(), "dev", plan)

	require.NoError(t, err)
	assert.Equal(t, []string{
		"network rpm-dev",
		"reuse rpm-dev-postgres",
		"remove-container rpm-dev-postgres",
		"run rpm-dev-postgres",
	}, backend.calls)
	assert.Equal(t, map[string]string{"POSTGRES_PORT": "5432"}, startup.Env)
}

func TestUpReturnsScopedDependencyFailureAndCleansStartedContainers(t *testing.T) {
	shouldSkip(t)

	backend := &recordingBackend{
		ensureErrs: map[string]error{"rpm-dev-rabbitmq": assert.AnError},
	}
	runner := docker.NewCLI(docker.Options{Backend: backend})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{
			{Ref: "mongodb", Name: "mongodb", Image: "mongo:8.0.23-noble"},
			{Ref: "postgres", Name: "postgres", Image: "postgis/postgis:17-3.5"},
			{Ref: "rabbitmq", Name: "rabbitmq", Image: "rabbitmq:4.1.3"},
			{Ref: "redis", Name: "redis", Image: "redis:7"},
		},
	}

	_, err := runner.Up(context.Background(), "dev", plan)

	require.ErrorIs(t, err, assert.AnError)
	var depErr envruntime.DependencyError
	require.True(t, errors.As(err, &depErr))
	assert.Equal(t, "rabbitmq", depErr.Ref)
	assert.Equal(t, []string{
		"network rpm-dev",
		"run rpm-dev-mongodb",
		"run rpm-dev-postgres",
		"ensure rpm-dev-rabbitmq",
		"remove-container rpm-dev-postgres",
		"remove-container rpm-dev-mongodb",
		"remove-network rpm-dev",
	}, backend.calls)
}

func TestUpDoesNotRemoveReusedContainerWhenLaterDependencyFails(t *testing.T) {
	shouldSkip(t)

	backend := &recordingBackend{
		existingContainers: map[string]string{"rpm-dev-rabbitmq": "running"},
		ensureErrs:         map[string]error{"rpm-dev-redis": assert.AnError},
	}
	runner := docker.NewCLI(docker.Options{Backend: backend})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{
			{Ref: "postgres", Name: "postgres", Image: "postgis/postgis:17-3.5"},
			{Ref: "rabbitmq", Name: "rabbitmq", Image: "rabbitmq:4.1.3"},
			{Ref: "redis", Name: "redis", Image: "redis:7"},
		},
	}

	_, err := runner.Up(context.Background(), "dev", plan)

	require.ErrorIs(t, err, assert.AnError)
	var depErr envruntime.DependencyError
	require.True(t, errors.As(err, &depErr))
	assert.Equal(t, "redis", depErr.Ref)
	assert.Equal(t, []string{
		"network rpm-dev",
		"run rpm-dev-postgres",
		"reuse rpm-dev-rabbitmq",
		"ensure rpm-dev-redis",
		"remove-container rpm-dev-postgres",
		"remove-network rpm-dev",
	}, backend.calls)
}

func TestDownRemovesDependencyContainersAndNetwork(t *testing.T) {
	shouldSkip(t)

	backend := &recordingBackend{}
	runner := docker.NewCLI(docker.Options{Backend: backend})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{Ref: "postgres", Name: "postgres", Image: "postgres:16"}},
	}

	err := runner.Down(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"remove-container rpm-local-stack-postgres",
		"remove-network rpm-local-stack",
	}, backend.calls)
}

func TestDownIgnoresMissingDependencyContainersAndNetwork(t *testing.T) {
	shouldSkip(t)

	backend := &recordingBackend{missingContainers: map[string]bool{
		"rpm-local-stack-postgres": true,
	}, missingNetworks: map[string]bool{
		"rpm-local-stack": true,
	}}
	runner := docker.NewCLI(docker.Options{Backend: backend})
	plan := &envstarlark.RuntimePlan{
		Dependencies: []envstarlark.Dependency{{Ref: "postgres", Name: "postgres", Image: "postgres:16"}},
	}

	err := runner.Down(context.Background(), "local-stack", plan)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"remove-container rpm-local-stack-postgres",
		"remove-network rpm-local-stack",
	}, backend.calls)
}

func TestFileVolumeNamerPersistsAndPrunesBlueprintEntries(t *testing.T) {
	shouldSkip(t)

	path := filepath.Join(t.TempDir(), "env-volumes.json")
	namer := docker.NewFileVolumeNamer(path, "sample-repo")

	first, err := namer.Name(context.Background(), "local-stack", "postgres", "/var/lib/postgresql/data")
	require.NoError(t, err)
	second, err := namer.Name(context.Background(), "local-stack", "postgres", "/var/lib/postgresql/data")
	require.NoError(t, err)

	assert.Equal(t, first, second)
	assert.Regexp(t, regexp.MustCompile(`^sample-repo-postgres-local-stack-[0-9]{6}$`), first)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var cache map[string]map[string]map[string]string
	require.NoError(t, json.Unmarshal(data, &cache))
	assert.Equal(t, first, cache["local-stack"]["postgres"]["/var/lib/postgresql/data"])

	require.NoError(t, docker.PruneVolumeCache(path, "local-stack"))
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	cache = nil
	require.NoError(t, json.Unmarshal(data, &cache))
	assert.NotContains(t, cache, "local-stack")
}

type recordingBackend struct {
	calls                  []string
	containers             []docker.ContainerSpec
	missingContainers      map[string]bool
	missingNetworks        map[string]bool
	missingVolumes         map[string]bool
	ensureErrs             map[string]error
	existingContainers     map[string]string
	existingContainerPorts map[string]map[string]string
}

type readinessCall struct {
	shell   string
	command string
	env     map[string]string
}

type recordingReadinessRunner struct {
	calls []readinessCall
	err   error
}

func (r *recordingReadinessRunner) Run(_ context.Context, shell string, command string, env map[string]string) error {
	values := make(map[string]string, len(env))
	for key, value := range env {
		values[key] = value
	}
	r.calls = append(r.calls, readinessCall{shell: shell, command: command, env: values})
	return r.err
}

func (b *recordingBackend) EnsureNetwork(_ context.Context, name string) (bool, error) {
	b.calls = append(b.calls, "network "+name)
	return true, nil
}

func (b *recordingBackend) EnsureVolume(_ context.Context, name string) (bool, error) {
	b.calls = append(b.calls, "volume "+name)
	return true, nil
}

func (b *recordingBackend) EnsureContainer(_ context.Context, spec docker.ContainerSpec) (docker.ContainerState, error) {
	if b.ensureErrs[spec.Name] != nil {
		b.calls = append(b.calls, "ensure "+spec.Name)
		return docker.ContainerState{}, b.ensureErrs[spec.Name]
	}
	if state := b.existingContainers[spec.Name]; state != "" {
		if state == "running" {
			b.calls = append(b.calls, "reuse "+spec.Name)
		} else {
			b.calls = append(b.calls, "start "+spec.Name)
		}
		return docker.ContainerState{Ports: b.reusedPorts(spec)}, nil
	}
	b.calls = append(b.calls, "run "+spec.Name)
	b.containers = append(b.containers, spec)
	return docker.ContainerState{Created: true}, nil
}

// reusedPorts reports the host ports the fake existing container is bound to:
// the configured bindings when set, otherwise bindings matching the spec.
func (b *recordingBackend) reusedPorts(spec docker.ContainerSpec) map[string]string {
	if ports, ok := b.existingContainerPorts[spec.Name]; ok {
		return ports
	}
	ports := make(map[string]string, len(spec.Ports))
	for _, item := range spec.Ports {
		index := strings.LastIndex(item, ":")
		host, container := item[:index], item[index+1:]
		if hostIndex := strings.LastIndex(host, ":"); hostIndex >= 0 {
			host = host[hostIndex+1:]
		}
		if !strings.Contains(container, "/") {
			container += "/tcp"
		}
		ports[container] = host
	}
	return ports
}

func (b *recordingBackend) RemoveContainer(_ context.Context, name string) error {
	b.calls = append(b.calls, "remove-container "+name)
	if b.missingContainers[name] {
		return errors.New("no such container")
	}
	delete(b.existingContainers, name)
	delete(b.existingContainerPorts, name)
	return nil
}

func (b *recordingBackend) RemoveNetwork(_ context.Context, name string) error {
	b.calls = append(b.calls, "remove-network "+name)
	if b.missingNetworks[name] {
		return errors.New("no such network")
	}
	return nil
}

func (b *recordingBackend) RemoveVolume(_ context.Context, name string) error {
	b.calls = append(b.calls, "remove-volume "+name)
	if b.missingVolumes[name] {
		return errors.New("no such volume")
	}
	return nil
}

type fixedPortAllocator struct {
	port  int
	calls int
}

func (a *fixedPortAllocator) Allocate(context.Context) (int, error) {
	port := a.port + a.calls
	a.calls++
	return port, nil
}

type fixedVolumeNamer struct {
	names map[string]string
}

func (n fixedVolumeNamer) Name(_ context.Context, blueprint string, dependency string, path string) (string, error) {
	key := blueprint + "|" + dependency + "|" + path
	if name := n.names[key]; name != "" {
		return name, nil
	}
	return "sample-repo-" + dependency + "-" + blueprint + "-123456", nil
}
