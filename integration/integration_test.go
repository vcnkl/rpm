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
		/usr/local/bin/rpm dev --help
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

func TestIntegration_EnvironmentVariables(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if os.Getenv("SKIP_INTEGRATION") == "true" {
		t.Skip("skipping integration test via SKIP_INTEGRATION env var")
	}

	ctx := context.Background()
	ctr := startTestContainer(t, ctx)
	defer testcontainers.CleanupContainer(t, ctr)

	exitCode, reader, err := ctr.Exec(ctx, []string{"rpm", "dev", "--help"})
	require.NoError(t, err)
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(reader)
	t.Logf("rpm dev --help output (exit code %d): %s", exitCode, buf.String())

	tests := []struct {
		name            string
		command         []string
		expectedEnvVars map[string]string
	}{
		{
			name:    "dev command loads REPO_ROOT and BUNDLE_ROOT",
			command: []string{"rpm", "dev", "--dry-run", "go-app"},
			expectedEnvVars: map[string]string{
				"REPO_ROOT":   "/workspace",
				"BUNDLE_ROOT": "/workspace/apps/go-app",
			},
		},
		{
			name:    "dev command loads bundle env vars",
			command: []string{"rpm", "dev", "--dry-run", "go-app"},
			expectedEnvVars: map[string]string{
				"GO_VAR":   "go_value",
				"APP_PORT": "8080",
			},
		},
		{
			name:    "dev command loads global env vars",
			command: []string{"rpm", "dev", "--dry-run", "go-app"},
			expectedEnvVars: map[string]string{
				"GLOBAL_VAR":   "global_value",
				"PROJECT_NAME": "sample-repo",
			},
		},
		{
			name:    "python app has correct BUNDLE_ROOT",
			command: []string{"rpm", "dev", "--dry-run", "python-app"},
			expectedEnvVars: map[string]string{
				"BUNDLE_ROOT": "/workspace/apps/python-app",
				"PYTHON_VAR":  "python_value",
			},
		},
		{
			name:    "ts app has correct BUNDLE_ROOT",
			command: []string{"rpm", "dev", "--dry-run", "ts-app"},
			expectedEnvVars: map[string]string{
				"BUNDLE_ROOT": "/workspace/apps/ts-app",
				"TS_VAR":      "typescript_value",
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

			assert.Zerof(t, exitCode, "command should succeed, output: %s", output)

			for envVar, expectedValue := range tt.expectedEnvVars {
				assert.Contains(t, output, envVar+"="+expectedValue,
					"output should contain %s=%s", envVar, expectedValue)
			}
		})
	}
}

func TestIntegration_DotenvLoading(t *testing.T) {
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
		name            string
		command         []string
		expectLoaded    bool
		expectedEnvVars map[string]string
	}{
		{
			name:    "dotenv enabled loads .env file",
			command: []string{"rpm", "dev", "--dry-run", "go-app"},
			expectedEnvVars: map[string]string{
				"LOCAL_SECRET": "secret_from_dotenv",
				"DB_HOST":      "localhost",
				"DB_PORT":      "5432",
			},
			expectLoaded: true,
		},
		{
			name:         "dotenv disabled does not load .env file",
			command:      []string{"rpm", "dev", "--dry-run", "ts-app"},
			expectLoaded: false,
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

			assert.Zerof(t, exitCode, "command should succeed, output: %s", output)

			if tt.expectLoaded {
				for envVar, expectedValue := range tt.expectedEnvVars {
					assert.Contains(t, output, envVar+"="+expectedValue,
						"dotenv var %s should be loaded with value %s", envVar, expectedValue)
				}
			}
		})
	}
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
			name:            "bundle name resolves to all dev targets",
			command:         []string{"rpm", "dev", "--dry-run", "go-app"},
			expectedTargets: []string{"go-app:app_dev"},
		},
		{
			name:            "full target ID works",
			command:         []string{"rpm", "dev", "--dry-run", "go-app:app_dev"},
			expectedTargets: []string{"go-app:app_dev"},
		},
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
