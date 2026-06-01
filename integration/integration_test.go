package integration

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
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

func TestIntegration_EnvHelp(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if os.Getenv("SKIP_INTEGRATION") == "true" {
		t.Skip("skipping integration test via SKIP_INTEGRATION env var")
	}

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
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if os.Getenv("SKIP_INTEGRATION") == "true" {
		t.Skip("skipping integration test via SKIP_INTEGRATION env var")
	}

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
	assert.Contains(t, output, `"type":"process_started","ref":"go-app:scripts/pre.sh"`)
	assert.Contains(t, output, `"type":"process_exited","ref":"go-app:scripts/pre.sh"`)
	assert.Contains(t, output, `"type":"process_started","ref":"pre:inline:3"`)
	assert.Contains(t, output, `"type":"process_exited","ref":"pre:inline:3"`)
	assert.Contains(t, output, `"type":"process_started","ref":"go-app:run"`)
	assert.Contains(t, output, `"type":"process_exited","ref":"go-app:run"`)
	assert.Contains(t, output, `"type":"process_started","ref":"python-app:run"`)
	assert.Contains(t, output, `"type":"process_exited","ref":"python-app:run"`)
	assert.Contains(t, output, `"type":"process_started","ref":"ts-app:web"`)
	assert.Contains(t, output, `"type":"process_exited","ref":"ts-app:web"`)
	assert.Contains(t, output, "REPO_ROOT=/workspace")
	assert.Contains(t, output, "BUNDLE_ROOT=/workspace/apps/")
	assert.Contains(t, output, "PRE_TARGET=/workspace/apps/go-app")
	assert.Contains(t, output, "PRE_FILE=/workspace/apps/go-app")
	assert.Contains(t, output, "PRE_INLINE=/workspace")
	assert.Less(t, strings.Index(output, `"ref":"go-app:prepare"`), strings.Index(output, `"ref":"go-app:run"`))
	assert.Less(t, strings.Index(output, `"ref":"go-app:scripts/pre.sh"`), strings.Index(output, `"ref":"go-app:run"`))
	assert.Less(t, strings.Index(output, `"ref":"pre:inline:3"`), strings.Index(output, `"ref":"go-app:run"`))
}

func TestIntegration_EnvCommandsEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if os.Getenv("SKIP_INTEGRATION") == "true" {
		t.Skip("skipping integration test via SKIP_INTEGRATION env var")
	}

	ctx := context.Background()
	ctr := startTestContainer(t, ctx)
	defer testcontainers.CleanupContainer(t, ctr)

	validate := runWorkspaceCommand(t, ctx, ctr, "rpm env validate local-stack")
	assert.Zero(t, validate.exitCode, validate.output)

	render := runWorkspaceCommand(t, ctx, ctr, "rpm env render local-stack")
	assert.Zero(t, render.exitCode, render.output)

	golden, err := os.ReadFile(filepath.Join("testdata", "golden", "local-stack.star"))
	require.NoError(t, err)
	rendered := runWorkspaceCommand(t, ctx, ctr, "sed 's#/workspace#<repo>#g' .rpm/cache/starlark/local-stack/env.star")
	assert.Zero(t, rendered.exitCode, rendered.output)
	assert.Equal(t, string(golden), rendered.output)

	create := runWorkspaceCommand(t, ctx, ctr, "rpm env create --non-interactive smoke --target go-app:run --deps")
	assert.Zero(t, create.exitCode, create.output)
	created := runWorkspaceCommand(t, ctx, ctr, "rpm env validate smoke")
	assert.Zero(t, created.exitCode, created.output)

	devHelp := runWorkspaceCommand(t, ctx, ctr, "rpm dev --help")
	assert.NotZero(t, devHelp.exitCode, devHelp.output)
	assert.Contains(t, strings.ToLower(devHelp.output), "no help topic")

	dockerBuild := runWorkspaceCommand(t, ctx, ctr, "rpm build --"+"docker")
	assert.NotZero(t, dockerBuild.exitCode, dockerBuild.output)
	assert.Contains(t, strings.ToLower(dockerBuild.output), "flag")
}

func TestIntegration_BuildCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if os.Getenv("SKIP_INTEGRATION") == "true" {
		t.Skip("skipping integration test via SKIP_INTEGRATION env var")
	}

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
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if os.Getenv("SKIP_INTEGRATION") == "true" {
		t.Skip("skipping integration test via SKIP_INTEGRATION env var")
	}

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
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if os.Getenv("SKIP_INTEGRATION") == "true" {
		t.Skip("skipping integration test via SKIP_INTEGRATION env var")
	}

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
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if os.Getenv("SKIP_INTEGRATION") == "true" {
		t.Skip("skipping integration test via SKIP_INTEGRATION env var")
	}

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
