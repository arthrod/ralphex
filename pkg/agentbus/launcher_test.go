package agentbus

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordedCmd struct {
	name string
	args []string
}

// newFakeLauncher returns a TmuxLauncher whose runner records commands instead of
// spawning processes, plus a pointer to the recorded slice. has-session returns an error
// (session absent) so EnsureSessions always creates, and session list returns one id.
func newFakeLauncher() (*TmuxLauncher, *[]recordedCmd) {
	var recorded []recordedCmd
	l := &TmuxLauncher{
		tmuxBin:     "tmux",
		opencodeBin: "opencode",
		run: func(_ context.Context, _ []string, name string, args ...string) (string, error) {
			recorded = append(recorded, recordedCmd{name: name, args: args})
			if name == "opencode" && len(args) >= 2 && args[0] == "session" && args[1] == "list" {
				return `[{"id":"ses_abc","updated":200,"created":100}]`, nil
			}
			if name == "tmux" && len(args) > 0 && args[0] == "has-session" {
				return "", errors.New("can't find session") // simulate absent session
			}
			return "", nil
		},
	}
	return l, &recorded
}

func TestTmuxResumeFirstLaunchDiscoversSession(t *testing.T) {
	l, rec := newFakeLauncher()
	sid, err := l.Resume(context.Background(), RoleWorker, "", "do task")
	require.NoError(t, err)
	assert.Equal(t, "ses_abc", sid)

	var sawSendKeys, sawList bool
	for _, c := range *rec {
		if c.name == "tmux" && len(c.args) > 0 && c.args[0] == "send-keys" {
			sawSendKeys = true
			joined := strings.Join(c.args, " ")
			assert.Contains(t, joined, "opencode run --agent worker")
			assert.NotContains(t, joined, "--session", "first launch has no session id")
		}
		if c.name == "opencode" && len(c.args) >= 2 && c.args[1] == "list" {
			sawList = true
		}
	}
	assert.True(t, sawSendKeys, "expected an opencode run sent into tmux")
	assert.True(t, sawList, "expected a session list query to discover the new id")
}

func TestTmuxResumeExistingSession(t *testing.T) {
	l, rec := newFakeLauncher()
	sid, err := l.Resume(context.Background(), RoleInspector, "ses_xyz", "review please")
	require.NoError(t, err)
	assert.Equal(t, "ses_xyz", sid)

	var joined string
	for _, c := range *rec {
		if c.name == "tmux" && len(c.args) > 0 && c.args[0] == "send-keys" {
			joined = strings.Join(c.args, " ")
		}
	}
	assert.Contains(t, joined, "--agent inspector")
	assert.Contains(t, joined, "--session 'ses_xyz'")
}

func TestTmuxEnsureSessions(t *testing.T) {
	l, rec := newFakeLauncher()
	roleEnv := map[Role][]string{
		RoleWorker:       {"TOOL_API_KEY=tw"},
		RoleOracle:       {"TOOL_API_KEY=to"},
		RoleInspector:    {"TOOL_API_KEY=ti"},
		RoleOrchestrator: {"TOOL_API_KEY=tc"},
	}
	require.NoError(t, l.EnsureSessions(context.Background(), roleEnv))

	creates := 0
	for _, c := range *rec {
		if c.name == "tmux" && len(c.args) > 0 && c.args[0] == "new-session" {
			creates++
			assert.Contains(t, c.args, "-e")
		}
	}
	assert.Equal(t, 4, creates, "one tmux session created per role")
}

func TestShellQuote(t *testing.T) {
	assert.Equal(t, `'plain'`, shellQuote("plain"))
	assert.Equal(t, `'it'\''s'`, shellQuote("it's"))
}

func TestNewTmuxLauncher(t *testing.T) {
	l := NewTmuxLauncher()
	assert.Equal(t, "tmux", l.tmuxBin)
	assert.Equal(t, "opencode", l.opencodeBin)
	assert.NotNil(t, l.run)
}

func TestEnsureSessionsSkipsExisting(t *testing.T) {
	var recorded []recordedCmd
	l := &TmuxLauncher{tmuxBin: "tmux", opencodeBin: "opencode",
		run: func(_ context.Context, _ []string, name string, args ...string) (string, error) {
			recorded = append(recorded, recordedCmd{name: name, args: args})
			return "", nil // has-session succeeds => session already exists
		}}
	require.NoError(t, l.EnsureSessions(context.Background(), map[Role][]string{RoleWorker: {"K=V"}}))
	for _, c := range recorded {
		assert.NotEqual(t, "new-session", firstArg(c.args), "should not create when session exists")
	}
}

func TestKillPane(t *testing.T) {
	t.Run("interrupts an existing session", func(t *testing.T) {
		var recorded []recordedCmd
		l := &TmuxLauncher{tmuxBin: "tmux", run: func(_ context.Context, _ []string, name string, args ...string) (string, error) {
			recorded = append(recorded, recordedCmd{name: name, args: args})
			return "", nil // has-session ok
		}}
		require.NoError(t, l.KillPane(context.Background(), RoleWorker))
		var sentInterrupt bool
		for _, c := range recorded {
			if firstArg(c.args) == "send-keys" && contains(c.args, "C-c") {
				sentInterrupt = true
			}
		}
		assert.True(t, sentInterrupt)
	})

	t.Run("missing session is a no-op", func(t *testing.T) {
		l := &TmuxLauncher{tmuxBin: "tmux", run: func(_ context.Context, _ []string, _ string, _ ...string) (string, error) {
			return "", errors.New("no session")
		}}
		assert.NoError(t, l.KillPane(context.Background(), RoleWorker))
	})
}

func TestSendHealth(t *testing.T) {
	var recorded []recordedCmd
	l := &TmuxLauncher{tmuxBin: "tmux", run: func(_ context.Context, _ []string, name string, args ...string) (string, error) {
		recorded = append(recorded, recordedCmd{name: name, args: args})
		return "", nil
	}}
	require.NoError(t, l.SendHealth(context.Background(), RoleOracle, "ping"))
	var sent bool
	for _, c := range recorded {
		if firstArg(c.args) == "send-keys" && contains(c.args, "ping") {
			sent = true
		}
	}
	assert.True(t, sent)
}

func TestDiscoverLatestSessionEmpty(t *testing.T) {
	l := &TmuxLauncher{opencodeBin: "opencode", run: func(_ context.Context, _ []string, _ string, _ ...string) (string, error) {
		return "[]", nil
	}}
	_, err := l.discoverLatestSession(context.Background())
	assert.Error(t, err)
}

func TestDiscoverLatestSessionPicksNewest(t *testing.T) {
	l := &TmuxLauncher{opencodeBin: "opencode", run: func(_ context.Context, _ []string, _ string, _ ...string) (string, error) {
		return `[{"id":"old","updated":100},{"id":"new","updated":300},{"id":"mid","updated":200}]`, nil
	}}
	sid, err := l.discoverLatestSession(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "new", sid)
}

func TestExecRun(t *testing.T) {
	t.Run("success returns trimmed stdout", func(t *testing.T) {
		out, err := execRun(context.Background(), nil, "printf", "hello")
		require.NoError(t, err)
		assert.Equal(t, "hello", out)
	})
	t.Run("missing binary errors", func(t *testing.T) {
		_, err := execRun(context.Background(), nil, "definitely-not-a-real-binary-xyz")
		assert.Error(t, err)
	})
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
