package agentbus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	// seed an initial commit so HEAD exists
	require.NoError(t, os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed"), 0o600))
	g := NewGit(dir)
	_, committed, err := g.CheckpointAll(context.Background(), "seed")
	require.NoError(t, err)
	require.True(t, committed)
	return dir
}

func TestCheckpointAll(t *testing.T) {
	ctx := context.Background()
	dir := initRepo(t)
	g := NewGit(dir)

	t.Run("clean tree is a no-op", func(t *testing.T) {
		hash, committed, err := g.CheckpointAll(ctx, "noop")
		require.NoError(t, err)
		assert.False(t, committed)
		assert.Empty(t, hash)
	})

	t.Run("dirty tree commits", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o600))
		hash, committed, err := g.CheckpointAll(ctx, "add a")
		require.NoError(t, err)
		assert.True(t, committed)
		assert.NotEmpty(t, hash)

		clean, err := g.IsClean(ctx)
		require.NoError(t, err)
		assert.True(t, clean)
	})
}

func TestRollbackTo(t *testing.T) {
	ctx := context.Background()
	dir := initRepo(t)
	g := NewGit(dir)

	base, err := g.Head(ctx)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("data"), 0o600))
	_, committed, err := g.CheckpointAll(ctx, "add b")
	require.NoError(t, err)
	require.True(t, committed)
	assert.FileExists(t, filepath.Join(dir, "b.txt"))

	require.NoError(t, g.RollbackTo(ctx, base))

	head, err := g.Head(ctx)
	require.NoError(t, err)
	assert.Equal(t, base, head)
	assert.NoFileExists(t, filepath.Join(dir, "b.txt"))

	assert.Error(t, g.RollbackTo(ctx, ""))
}
