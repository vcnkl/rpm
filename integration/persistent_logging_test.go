package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcwx/rpm/cmd"
	envruntime "github.com/vcwx/rpm/environments/runtime"
)

var loggingCommandMu sync.Mutex

func TestIntegration_PersistentCommandLogging(t *testing.T) {
	shouldSkip(t)
	t.Parallel()

	t.Run("disabled by default", func(t *testing.T) {
		repoRoot := newLoggingRepo(t, "")
		runLoggingCommand(t, repoRoot, "build")
		assert.NoDirExists(t, filepath.Join(repoRoot, "log"))
	})

	t.Run("disabled dev preserves raw output", func(t *testing.T) {
		repoRoot := newLoggingRepo(t, "")
		binary := buildRpmBinary(t)
		command := exec.Command(binary, "--config", filepath.Join(repoRoot, "repo.yml"), "dev", "app:app_serve")
		output, err := command.CombinedOutput()
		require.NoError(t, err, string(output))
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		assert.Contains(t, lines, "dev-output")
		assert.NoDirExists(t, filepath.Join(repoRoot, "log"))
	})

	t.Run("yaml uses default path", func(t *testing.T) {
		repoRoot := newLoggingRepo(t, "logs:\n  enabled: true\n")
		runLoggingCommand(t, repoRoot, "build")
		path := oneLogFile(t, filepath.Join(repoRoot, "log", "rpm", "build"))
		assertLogFile(t, path, "build-output")
	})

	t.Run("flag enables yaml false", func(t *testing.T) {
		repoRoot := newLoggingRepo(t, "")
		runLoggingCommand(t, repoRoot, "--logs", "test")
		path := oneLogFile(t, filepath.Join(repoRoot, "log", "rpm", "test"))
		assertLogFile(t, path, "test-output")
	})

	t.Run("explicit false disables yaml true", func(t *testing.T) {
		repoRoot := newLoggingRepo(t, "logs:\n  enabled: true\n")
		runLoggingCommand(t, repoRoot, "--logs=false", "build")
		assert.NoDirExists(t, filepath.Join(repoRoot, "log"))
	})

	t.Run("custom command paths", func(t *testing.T) {
		repoRoot := newLoggingRepo(t, `logs:
  enabled: true
  build:
    out: records/build
  test:
    out: records/test
  dev:
    out: records/dev
`)
		for _, command := range []struct {
			name    string
			message string
		}{
			{name: "build", message: "build-output"},
			{name: "test", message: "test-output"},
			{name: "dev", message: "dev-output"},
		} {
			runLoggingCommand(t, repoRoot, command.name)
			path := oneLogFile(t, filepath.Join(repoRoot, "records", command.name))
			assertLogFile(t, path, command.message)
		}
	})
}

func TestIntegration_PersistentEnvironmentLogging(t *testing.T) {
	shouldSkip(t)
	t.Parallel()

	repoRoot := newLoggingRepo(t, `logs:
  enabled: true
  env:
    out: records/env
`)
	writeLoggingRuntime(t, repoRoot)
	visible := runLoggingCommand(t, repoRoot, "env", "up", "local", "--non-interactive", "--no-deps", "--no-reload")
	path := oneLogFile(t, filepath.Join(repoRoot, "records", "env", "local"))
	fileEvents := readEnvironmentEvents(t, readFile(t, path))
	visibleEvents := readEnvironmentEvents(t, visible)
	assert.Equal(t, visibleEvents, fileEvents)

	sources := make(map[string]bool)
	outputs := make(map[string]string)
	for _, event := range fileEvents {
		require.NotEmpty(t, event.Source)
		sources[event.Source] = true
		if event.Type == envruntime.EventProcessOutput {
			outputs[event.Line] = event.Source
		}
	}
	assert.True(t, sources["database"])
	assert.True(t, sources["app:before_task"])
	assert.True(t, sources["app:dep_task"])
	assert.True(t, sources["app:main_serve"])
	assert.True(t, sources["local"])
	assert.Equal(t, "app:before_task", outputs["before-output"])
	assert.Equal(t, "app:dep_task", outputs["dep-output"])
	assert.Equal(t, "app:main_serve", outputs["env-output"])

	var fallback bytes.Buffer
	sink := envruntime.NewSourceEventSink("local", envruntime.NewLineEventSink(&fallback, &fallback))
	sink.Emit(envruntime.Event{Type: "diagnostic"})
	assert.Equal(t, "rpm", readEnvironmentEvents(t, fallback.String())[0].Source)
}

func TestIntegration_DisabledEnvironmentLoggingPreservesSchema(t *testing.T) {
	shouldSkip(t)
	t.Parallel()

	repoRoot := newLoggingRepo(t, "")
	writeLoggingRuntime(t, repoRoot)
	visible := runLoggingCommand(t, repoRoot, "env", "up", "local", "--non-interactive", "--no-deps", "--no-reload")
	for _, line := range strings.Split(strings.TrimSpace(visible), "\n") {
		var event map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &event), line)
		assert.NotContains(t, event, "source")
	}
	assert.NoDirExists(t, filepath.Join(repoRoot, "log"))
}

func newLoggingRepo(t *testing.T, logs string) string {
	t.Helper()
	repoRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "app"), 0755))
	repo := `project:
  name: persistent-logging
shell: /bin/sh
env:
  deps:
    - name: database
      image: busybox:1
` + logs
	bundle := `name: app
targets:
  - name: app_build
    cmd: echo build-output
  - name: app_test
    cmd: echo test-output
  - name: app_serve
    cmd: echo dev-output
    config:
      reload: false
  - name: before_task
    cmd: echo before-output
  - name: dep_task
    cmd: echo dep-output
  - name: main_serve
    cmd: echo env-output
    deps:
      - :dep_task
    config:
      reload: false
`
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "repo.yml"), []byte(repo), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "app", "rpm.yml"), []byte(bundle), 0644))
	git := exec.Command("git", "init", "--quiet")
	git.Dir = repoRoot
	require.NoError(t, git.Run())
	git = exec.Command("git", "add", ".")
	git.Dir = repoRoot
	require.NoError(t, git.Run())
	return repoRoot
}

func writeLoggingRuntime(t *testing.T, repoRoot string) {
	t.Helper()
	runtimeDir := filepath.Join(repoRoot, ".rpm", "envs", "local")
	require.NoError(t, os.MkdirAll(runtimeDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(runtimeDir, "runtime.gen.star"), []byte(`rpm_environment(name = "local", live_reload = {"enabled": False, "debounce": "100ms"}, variables = {})
rpm_dependency(ref = "database", config = "repo.yml")
rpm_before_target(ref = "app:before_task", config = "app/rpm.yml")
rpm_target(ref = "app:main_serve", config = "app/rpm.yml", env = {}, reload = False)
rpm_run(order = ["database", "app:before_task", "app:dep_task", "app:main_serve"])
`), 0644))
}

func runLoggingCommand(t *testing.T, repoRoot string, args ...string) string {
	t.Helper()
	loggingCommandMu.Lock()
	defer loggingCommandMu.Unlock()
	app := cmd.NewApp()
	var output bytes.Buffer
	app.Writer = &output
	app.ErrWriter = &output
	command := append([]string{"rpm", "--config", filepath.Join(repoRoot, "repo.yml")}, args...)
	require.NoError(t, app.Run(command), output.String())
	return output.String()
}

func oneLogFile(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Regexp(t, regexp.MustCompile(`^[0-9]{13}\.txt$`), entries[0].Name())
	return filepath.Join(dir, entries[0].Name())
}

func assertLogFile(t *testing.T, path string, message string) {
	t.Helper()
	found := false
	for _, line := range strings.Split(strings.TrimSpace(readFile(t, path)), "\n") {
		var event map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &event), line)
		if strings.Contains(fmt.Sprint(event["message"]), message) {
			found = true
		}
	}
	assert.True(t, found)
}

func readEnvironmentEvents(t *testing.T, data string) []envruntime.Event {
	t.Helper()
	var events []envruntime.Event
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		var event envruntime.Event
		require.NoError(t, json.Unmarshal([]byte(line), &event), line)
		events = append(events, event)
	}
	return events
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
