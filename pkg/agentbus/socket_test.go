package agentbus

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shortStateDir creates a short-pathed AGENTBUS_DIR and points the env at it. Unix-domain
// socket paths are capped (104 bytes on macOS), and the default t.TempDir() under
// /var/folders/... overruns that. The helper builds a short dir under os.MkdirTemp's base
// with a minimal name, registers AGENTBUS_DIR via t.Setenv, and schedules cleanup. Tests
// that bind a socket must use this instead of t.TempDir().
func shortStateDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ab")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("AGENTBUS_DIR", filepath.Join(dir, "b"))
	require.NoError(t, EnsureDir())
	return Dir()
}

// startTestAuthServer mints an in-process AuthServer, binds it to a socket under a short
// AGENTBUS_DIR, starts serving, and registers cleanup. It returns the running server so
// callers can read its registry and mac key.
func startTestAuthServer(t *testing.T) *AuthServer {
	t.Helper()
	shortStateDir(t)
	srv, err := NewAuthServer()
	require.NoError(t, err)
	require.NoError(t, srv.Listen(SocketPath()))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.Serve(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		_ = srv.Close()
	})
	return srv
}

func TestNewAuthServerRegistry(t *testing.T) {
	srv, err := NewAuthServer()
	require.NoError(t, err)

	reg := srv.Registry()
	assert.Len(t, reg, len(AllTools))
	for _, tool := range AllTools {
		assert.NotEmpty(t, reg[tool], "token for %s", tool)
		assert.Regexp(t, uuidV4Pattern, reg[tool], "token for %s must be a v4 UUID", tool)
	}

	// tokens are distinct per tool
	seen := map[string]bool{}
	for _, tok := range reg {
		assert.False(t, seen[tok], "duplicate token minted")
		seen[tok] = true
	}

	// mac key is 32 random bytes
	assert.Len(t, srv.MacKey(), 32)

	// Registry returns a copy: mutating it must not affect the server
	reg[ToolWorker] = "tampered"
	assert.NotEqual(t, "tampered", srv.Registry()[ToolWorker])

	// MacKey returns a copy
	k := srv.MacKey()
	k[0] ^= 0xff
	assert.NotEqual(t, k, srv.MacKey())
}

func TestAuthServerProcessAuth(t *testing.T) {
	srv, err := NewAuthServer()
	require.NoError(t, err)
	reg := srv.Registry()

	tests := []struct {
		name string
		req  socketRequest
		want bool
	}{
		{"valid token", socketRequest{Action: actionAuth, Tool: ToolWorker, Token: reg[ToolWorker]}, true},
		{"wrong tool token", socketRequest{Action: actionAuth, Tool: ToolWorker, Token: reg[ToolOracle]}, false},
		{"empty token", socketRequest{Action: actionAuth, Tool: ToolWorker, Token: ""}, false},
		{"unknown tool", socketRequest{Action: actionAuth, Tool: "nope", Token: reg[ToolWorker]}, false},
		{"unknown action", socketRequest{Action: "frobnicate", Tool: ToolWorker, Token: reg[ToolWorker]}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := srv.process(tt.req)
			assert.Equal(t, tt.want, resp.OK)
		})
	}
}

func TestAuthServerProcessHandoff(t *testing.T) {
	t.Setenv("AGENTBUS_DIR", t.TempDir())
	srv, err := NewAuthServer()
	require.NoError(t, err)
	reg := srv.Registry()

	t.Run("legal signed handoff is stored", func(t *testing.T) {
		resp := srv.process(socketRequest{
			Action: actionHandoff, Tool: ToolWorker, Token: reg[ToolWorker],
			To: RoleInspector, Message: "done",
		})
		require.True(t, resp.OK, resp.Error)
		require.NotNil(t, resp.Handoff)
		assert.Equal(t, RoleWorker, resp.Handoff.From)
		assert.True(t, VerifyHandoffMac(*resp.Handoff, srv.MacKey()), "stored handoff must carry a valid mac")
	})

	t.Run("illegal transition rejected", func(t *testing.T) {
		resp := srv.process(socketRequest{
			Action: actionHandoff, Tool: ToolWorker, Token: reg[ToolWorker],
			To: RoleOrchestrator, Message: "x",
		})
		assert.False(t, resp.OK)
	})

	t.Run("bad credential rejected", func(t *testing.T) {
		resp := srv.process(socketRequest{
			Action: actionHandoff, Tool: ToolWorker, Token: reg[ToolOracle],
			To: RoleInspector, Message: "x",
		})
		assert.False(t, resp.OK)
	})
}
