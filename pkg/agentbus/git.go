package agentbus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Git runs the checkpoint/rollback operations the supervisor needs. It is intentionally
// separate from pkg/git (whose backend interface is closed) so the agentbus subsystem
// stays self-contained: a handoff makes a checkpoint commit and the orchestrator can
// roll back to any earlier checkpoint instead of sandboxing what agents may do.
type Git struct {
	dir string
	cmd string
}

// NewGit returns a Git operating in dir using the "git" binary.
func NewGit(dir string) *Git { return &Git{dir: dir, cmd: "git"} }

func (g *Git) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, g.cmd, args...)
	cmd.Dir = g.dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// IsClean reports whether the working tree has no changes (tracked or untracked).
func (g *Git) IsClean(ctx context.Context) (bool, error) {
	out, err := g.run(ctx, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out == "", nil
}

// Head returns the current HEAD commit hash.
func (g *Git) Head(ctx context.Context) (string, error) {
	return g.run(ctx, "rev-parse", "HEAD")
}

// CheckpointAll stages everything and commits with msg. If the tree is already clean it
// is a no-op (committed=false). On a successful commit it returns the new HEAD hash.
func (g *Git) CheckpointAll(ctx context.Context, msg string) (hash string, committed bool, err error) {
	if _, err = g.run(ctx, "add", "-A"); err != nil {
		return "", false, err
	}
	clean, err := g.IsClean(ctx)
	if err != nil {
		return "", false, err
	}
	if clean {
		return "", false, nil
	}
	if _, err = g.run(ctx, "commit", "-m", msg); err != nil {
		return "", false, err
	}
	hash, err = g.Head(ctx)
	if err != nil {
		return "", false, err
	}
	return hash, true, nil
}

// RollbackTo hard-resets the working tree to ref. This discards uncommitted changes and
// is only invoked by the orchestrator (never by a worker/oracle/inspector handoff).
func (g *Git) RollbackTo(ctx context.Context, ref string) error {
	if strings.TrimSpace(ref) == "" {
		return errors.New("rollback ref is required")
	}
	_, err := g.run(ctx, "reset", "--hard", ref)
	return err
}
