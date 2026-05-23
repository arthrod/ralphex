package agentbus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// commandRunner runs an external command and returns trimmed stdout. It is a field on
// TmuxLauncher so tests can substitute a fake without spawning tmux/opencode.
type commandRunner func(ctx context.Context, env []string, name string, args ...string) (string, error)

// TmuxLauncher drives the observable side of the system: one tmux session per role,
// each carrying that role's TOOL_API_KEY, and the opencode invocations that run a role
// autonomously against the shared session. It implements the supervisor's Launcher.
type TmuxLauncher struct {
	tmuxBin     string
	opencodeBin string
	run         commandRunner
}

// NewTmuxLauncher returns a launcher using the "tmux" and "opencode" binaries on PATH.
func NewTmuxLauncher() *TmuxLauncher {
	return &TmuxLauncher{tmuxBin: "tmux", opencodeBin: "opencode", run: execRun}
}

func execRun(ctx context.Context, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if len(env) > 0 {
		cmd.Env = env
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func sessionName(r Role) string { return "ab-" + string(r) }

// EnsureSessions creates a detached tmux session per role (if absent), each carrying the
// per-role environment (notably its TOOL_API_KEY) so any opencode started inside it can
// only authenticate that role's tool.
func (l *TmuxLauncher) EnsureSessions(ctx context.Context, roleEnv map[Role][]string) error {
	for _, r := range []Role{RoleWorker, RoleOracle, RoleInspector, RoleOrchestrator} {
		name := sessionName(r)
		if _, err := l.run(ctx, nil, l.tmuxBin, "has-session", "-t", name); err == nil {
			continue // already exists
		}
		args := []string{"new-session", "-d", "-s", name}
		for _, kv := range roleEnv[r] {
			args = append(args, "-e", kv)
		}
		if _, err := l.run(ctx, nil, l.tmuxBin, args...); err != nil {
			return fmt.Errorf("create tmux session %s: %w", name, err)
		}
	}
	return nil
}

// KillPane stops whatever opencode is running in a role's tmux session by sending an
// interrupt. It is best-effort: a missing session is not an error, because the durable
// handoff log — not the kill — is the source of truth for the transition.
func (l *TmuxLauncher) KillPane(ctx context.Context, r Role) error {
	name := sessionName(r)
	if _, err := l.run(ctx, nil, l.tmuxBin, "has-session", "-t", name); err != nil {
		return nil
	}
	_, _ = l.run(ctx, nil, l.tmuxBin, "send-keys", "-t", name, "C-c")
	return nil
}

// Resume runs the target role autonomously against the shared opencode session by
// sending an `opencode run` command into the role's tmux session. When sessionID is
// empty (first launch) it starts a fresh session, then discovers and returns the new id;
// otherwise it resumes the given session and returns it unchanged.
func (l *TmuxLauncher) Resume(ctx context.Context, r Role, sessionID, message string) (string, error) {
	name := sessionName(r)
	cmd := fmt.Sprintf("%s run --agent %s", l.opencodeBin, string(r))
	if sessionID != "" {
		cmd += " --session " + shellQuote(sessionID)
	}
	cmd += " " + shellQuote(message)
	if _, err := l.run(ctx, nil, l.tmuxBin, "send-keys", "-t", name, cmd, "Enter"); err != nil {
		return "", fmt.Errorf("resume %s: %w", r, err)
	}
	if sessionID != "" {
		return sessionID, nil
	}
	return l.discoverLatestSession(ctx)
}

// SendHealth injects a health-check message into the active role's tmux session so a
// stalled or looping run is nudged to report whether it is making progress.
func (l *TmuxLauncher) SendHealth(ctx context.Context, r Role, message string) error {
	name := sessionName(r)
	if _, err := l.run(ctx, nil, l.tmuxBin, "has-session", "-t", name); err != nil {
		return nil
	}
	_, err := l.run(ctx, nil, l.tmuxBin, "send-keys", "-t", name, message, "Enter")
	return err
}

// discoverLatestSession returns the id of the most recently updated opencode session.
func (l *TmuxLauncher) discoverLatestSession(ctx context.Context) (string, error) {
	out, err := l.run(ctx, nil, l.opencodeBin, "session", "list", "--format", "json")
	if err != nil {
		return "", err
	}
	var sessions []struct {
		ID      string `json:"id"`
		Updated int64  `json:"updated"`
		Created int64  `json:"created"`
	}
	if err := json.Unmarshal([]byte(out), &sessions); err != nil {
		return "", fmt.Errorf("parse session list: %w", err)
	}
	if len(sessions) == 0 {
		return "", fmt.Errorf("no opencode sessions found after launch")
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].Updated != sessions[j].Updated {
			return sessions[i].Updated > sessions[j].Updated
		}
		return sessions[i].Created > sessions[j].Created
	})
	return sessions[0].ID, nil
}

// shellQuote wraps s in single quotes for safe inclusion in a tmux send-keys command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
