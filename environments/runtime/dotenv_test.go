package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpsertDependencyEnvBlockDefinesAllVars(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	body := "#MONGODB_PORT=27017\nMONGODB_URI=mongodb://localhost:${MONGODB_PORT}/mydb\nAMQP_HOSTNAME=localhost\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	upsertDependencyEnvBlock(path, map[string]string{
		"MONGODB_PORT": "49152",
		"REDIS_PORT":   "49153",
		"AMQP_PORT":    "49154",
	})

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, dotenvBlockBegin+"\n"+
		"AMQP_PORT=49154\n"+
		"MONGODB_PORT=49152\n"+
		"REDIS_PORT=49153\n"+
		dotenvBlockEnd+"\n"+
		body, string(data))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestUpsertDependencyEnvBlockReplacesPreviousBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	body := "MONGODB_URI=mongodb://localhost:${MONGODB_PORT}/mydb\n"
	stale := dotenvBlockBegin + "\nMONGODB_PORT=40001\n" + dotenvBlockEnd + "\n" + body
	require.NoError(t, os.WriteFile(path, []byte(stale), 0o644))

	upsertDependencyEnvBlock(path, map[string]string{"MONGODB_PORT": "49152"})

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, dotenvBlockBegin+"\nMONGODB_PORT=49152\n"+dotenvBlockEnd+"\n"+body, string(data))
}

func TestUpsertDependencyEnvBlockRemovesBlockWhenNoDependencyEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	body := "STATIC_VAR=value\n"
	stale := dotenvBlockBegin + "\nMONGODB_PORT=40001\n" + dotenvBlockEnd + "\n" + body
	require.NoError(t, os.WriteFile(path, []byte(stale), 0o644))

	upsertDependencyEnvBlock(path, map[string]string{})

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, body, string(data))
}

func TestUpsertDependencyEnvBlockInjectsIntoUnreferencedFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	body := "STATIC_VAR=value\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))

	upsertDependencyEnvBlock(path, map[string]string{"MONGODB_PORT": "49152"})

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, dotenvBlockBegin+"\nMONGODB_PORT=49152\n"+dotenvBlockEnd+"\n"+body, string(data))
}

func TestUpsertDependencyEnvBlockIgnoresMissingFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")

	upsertDependencyEnvBlock(path, map[string]string{"MONGODB_PORT": "49152"})

	_, err := os.Stat(path)
	assert.True(t, os.IsNotExist(err))
}
