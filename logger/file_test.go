package logger_test

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcnkl/rpm/logger"
)

func TestOpenFile(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	files := make([]io.WriteCloser, 16)
	for i := range files {
		file, err := logger.OpenFile(repoRoot, "logs", "local")
		require.NoError(t, err)
		files[i] = file
	}
	for _, file := range files {
		require.NoError(t, file.Close())
	}
	entries, err := os.ReadDir(filepath.Join(repoRoot, "logs", "local"))
	require.NoError(t, err)
	require.Len(t, entries, 16)
	for _, entry := range entries {
		assert.Regexp(t, regexp.MustCompile(`^[0-9]{13}\.txt$`), entry.Name())
	}
}

func TestOpenFileRejectsUnsafePaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		out    string
		nested []string
	}{
		{name: "absolute", out: "/tmp/rpm-log"},
		{name: "escaping", out: "../rpm-log"},
		{name: "nested escaping", out: "logs", nested: []string{"../outside"}},
		{name: "nested absolute", out: "logs", nested: []string{"/tmp/outside"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoRoot := t.TempDir()
			_, err := logger.OpenFile(repoRoot, tt.out, tt.nested...)
			require.Error(t, err)
		})
	}
}

func TestOpenFileRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(repoRoot, "logs")))
	_, err := logger.OpenFile(repoRoot, "logs")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolves outside repo root")
}
