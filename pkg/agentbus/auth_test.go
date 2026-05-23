package agentbus

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// uuidV4Pattern matches a canonical RFC 4122 version-4 UUID: 8-4-4-4-12 lowercase hex with the
// version nibble pinned to 4 and the variant nibble to 8/9/a/b.
var uuidV4Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

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

	// each minted token is an actual RFC 4122 v4 UUID
	for _, tool := range AllTools {
		assert.Regexp(t, uuidV4Pattern, reg[tool], "token for %s must be a v4 UUID", tool)
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

// TestAuthenticatePointToPoint exhaustively verifies the core isolation guarantee across every
// (holder, target) tool pair: a token minted for one tool authenticates ONLY that tool, and is
// rejected by every other tool. This is the "won't work with different tools" property — proven
// point to point rather than for a single example pair.
func TestAuthenticatePointToPoint(t *testing.T) {
	t.Setenv("AGENTBUS_DIR", t.TempDir())
	reg, err := GenerateRegistry()
	require.NoError(t, err)

	for _, holder := range AllTools {
		for _, target := range AllTools {
			t.Run(holder+"_key_vs_"+target, func(t *testing.T) {
				t.Setenv(tokenEnv, reg[holder])
				if holder == target {
					require.NoError(t, Authenticate(target), "a tool's own key must authenticate it")
					return
				}
				require.Error(t, Authenticate(target), "%s key must NOT authenticate %s", holder, target)
			})
		}
	}
}

func TestAuthenticateNoRegistry(t *testing.T) {
	t.Setenv("AGENTBUS_DIR", t.TempDir())
	t.Setenv(tokenEnv, "anything")
	assert.Error(t, Authenticate(ToolWorker))
}
