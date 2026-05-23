// Package agentbus implements a multi-agent orchestration layer on top of opencode.
//
// Four roles (worker, oracle, inspector, orchestrator) each run in their own tmux
// session and share a single opencode conversation. A role does its work, then calls
// a per-role CLI tool to hand off to another role. Handoffs are recorded as durable,
// append-only JSONL lines; a supervisor tails that log, makes an automatic git
// checkpoint commit, and resumes the shared opencode session as the target role.
//
// Roles are NOT differentiated by restricting which tools an agent may use: every
// agent may use whatever tools it wants. Role identity is enforced only by which
// handoff CLI a role can authenticate to (a random per-tool token) and the durable
// handoff log. Safety comes from checkpoint/rollback, not sandboxing.
package agentbus

import (
	"os"
	"path/filepath"
)

// Tool names — each maps 1:1 to a CLI binary and to a Role (the "-task" suffix dropped).
const (
	ToolWorker       = "worker-task"
	ToolOracle       = "oracle-task"
	ToolInspector    = "inspector-task"
	ToolOrchestrator = "orchestrator-task"
)

// AllTools lists every tool that gets a token in the registry.
var AllTools = []string{ToolWorker, ToolOracle, ToolInspector, ToolOrchestrator}

// File names within the agentbus state directory.
const (
	tokensFile   = "tokens.json"
	handoffsFile = "handoffs.jsonl"
	stateFile    = "state.json"
	prdFile      = "prd.yaml"
)

// Dir returns the agentbus state directory, honoring AGENTBUS_DIR and defaulting to
// ".agentbus" under the current working directory.
func Dir() string {
	if d := os.Getenv("AGENTBUS_DIR"); d != "" {
		return d
	}
	return ".agentbus"
}

// EnsureDir creates the state directory (0o700) if it does not exist.
func EnsureDir() error {
	return os.MkdirAll(Dir(), 0o700)
}

func tokensPath() string   { return filepath.Join(Dir(), tokensFile) }
func handoffsPath() string { return filepath.Join(Dir(), handoffsFile) }
func statePath() string    { return filepath.Join(Dir(), stateFile) }

// PRDPath returns the path to the PRD/task-store file.
func PRDPath() string { return filepath.Join(Dir(), prdFile) }
