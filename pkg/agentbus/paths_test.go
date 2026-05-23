package agentbus

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDir(t *testing.T) {
	t.Run("honors AGENTBUS_DIR", func(t *testing.T) {
		t.Setenv("AGENTBUS_DIR", "/custom/place")
		assert.Equal(t, "/custom/place", Dir())
	})
	t.Run("defaults to .agentbus", func(t *testing.T) {
		t.Setenv("AGENTBUS_DIR", "")
		assert.Equal(t, ".agentbus", Dir())
	})
}

func TestEnsureDir(t *testing.T) {
	base := t.TempDir()
	t.Setenv("AGENTBUS_DIR", filepath.Join(base, "sub", "bus"))
	require.NoError(t, EnsureDir())
	fi, err := os.Stat(Dir())
	require.NoError(t, err)
	assert.True(t, fi.IsDir())
}
