package agentbus

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// State is the supervisor's view of the run: which role is active, the current task,
// the shared opencode session, and the highest handoff sequence already processed.
type State struct {
	ActiveRole    Role   `json:"active_role"`
	CurrentTaskID string `json:"current_task_id"`
	SessionID     string `json:"session_id"`
	LastSeq       int    `json:"last_seq"`
}

// LoadState reads state.json. A missing file yields a zero State (not an error), since
// the first orchestrator action legitimately runs before any state has been written.
func LoadState() (State, error) {
	var st State
	data, err := os.ReadFile(statePath())
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, fmt.Errorf("read state: %w", err)
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return st, fmt.Errorf("parse state: %w", err)
	}
	return st, nil
}

// SaveState writes state.json atomically.
func SaveState(st State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	return writeFileAtomic(statePath(), data, 0o600)
}
