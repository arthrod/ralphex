package agentbus

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateRegistry(t *testing.T) {
	t.Setenv("AGENTBUS_DIR", t.TempDir())

	reg, err := GenerateRegistry()
	require.NoError(t, err)
	assert.Len(t, reg, len(AllTools))
	for _, tool := range AllTools {
		assert.NotEmpty(t, reg[tool], "token for %s", tool)
	}

	// tokens are distinct per tool
	seen := map[string]bool{}
	for _, tok := range reg {
		assert.False(t, seen[tok], "duplicate token minted")
		seen[tok] = true
	}

	// registry file is 0o600
	fi, err := os.Stat(filepath.Join(Dir(), tokensFile))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())

	assert.True(t, RegistryExists())
}

func TestAuthenticate(t *testing.T) {
	t.Setenv("AGENTBUS_DIR", t.TempDir())
	reg, err := GenerateRegistry()
	require.NoError(t, err)

	t.Run("correct token passes", func(t *testing.T) {
		t.Setenv(tokenEnv, reg[ToolWorker])
		assert.NoError(t, Authenticate(ToolWorker))
	})

	t.Run("token minted for another tool is rejected", func(t *testing.T) {
		// the worker's key must not authenticate oracle-task — the core guarantee
		t.Setenv(tokenEnv, reg[ToolWorker])
		assert.Error(t, Authenticate(ToolOracle))
	})

	t.Run("missing token is rejected", func(t *testing.T) {
		t.Setenv(tokenEnv, "")
		assert.Error(t, Authenticate(ToolWorker))
	})

	t.Run("garbage token is rejected", func(t *testing.T) {
		t.Setenv(tokenEnv, "not-a-real-token")
		assert.Error(t, Authenticate(ToolWorker))
	})
}

func TestAuthenticateNoRegistry(t *testing.T) {
	t.Setenv("AGENTBUS_DIR", t.TempDir())
	t.Setenv(tokenEnv, "anything")
	assert.Error(t, Authenticate(ToolWorker))
}
