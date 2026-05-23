package agentbus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// RoleEnvFromRegistry builds the per-role environment for each tmux session: the role's
// own tool token (so an agent in that session can only authenticate its own tool) plus
// AGENTBUS_DIR so the tools resolve the same state directory.
func RoleEnvFromRegistry() (map[Role][]string, error) {
	reg, err := LoadRegistry()
	if err != nil {
		return nil, err
	}
	dir := Dir()
	env := make(map[Role][]string, len(AllTools))
	for _, tool := range AllTools {
		role := RoleForTool(tool)
		tok, ok := reg[tool]
		if !ok {
			return nil, fmt.Errorf("registry missing token for %s", tool)
		}
		env[role] = []string{
			tokenEnv + "=" + tok,
			"AGENTBUS_DIR=" + dir,
		}
	}
	return env, nil
}

// workerPrompt renders the initial instruction handed to the worker for a task.
func workerPrompt(t Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Task %s: %s\n\n", t.ID, t.Summary)
	fmt.Fprintf(&b, "Instructions:\n%s\n\n", strings.TrimSpace(t.Instructions))
	b.WriteString("Minimum tests that must pass before you hand off:\n")
	for _, mt := range t.MinTests {
		if strings.TrimSpace(mt) == "" {
			continue
		}
		fmt.Fprintf(&b, "  - %s\n", mt)
	}
	b.WriteString("\nWhen done, run: worker-task no_further_actions --confirm-current \"<this task>\" --handoff \"<message to inspector>\".")
	return b.String()
}

// AssignAndRun validates a PRD task, records it as current, ensures the tmux sessions
// exist with per-role credentials, and launches the worker on it against the shared
// opencode session.
func AssignAndRun(ctx context.Context, launcher Launcher, taskID string) error {
	prd, err := LoadPRD()
	if err != nil {
		return err
	}
	task, ok := prd.Find(taskID)
	if !ok {
		return fmt.Errorf("task %q not found", taskID)
	}
	err = task.Validate()
	if err != nil {
		return err
	}

	roleEnv, err := RoleEnvFromRegistry()
	if err != nil {
		return err
	}
	if err = launcher.EnsureSessions(ctx, roleEnv); err != nil {
		return fmt.Errorf("ensure sessions: %w", err)
	}

	st, err := LoadState()
	if err != nil {
		return err
	}
	newSID, err := launcher.Resume(ctx, RoleWorker, st.SessionID, workerPrompt(task))
	if err != nil {
		return fmt.Errorf("launch worker: %w", err)
	}
	st.CurrentTaskID = task.ID
	st.ActiveRole = RoleWorker
	st.SessionID = newSID
	return SaveState(st)
}

// CurrentTask returns the task currently assigned (per state.json), or an error if none
// is set or it no longer exists in the PRD.
func CurrentTask() (Task, error) {
	st, err := LoadState()
	if err != nil {
		return Task{}, err
	}
	if st.CurrentTaskID == "" {
		return Task{}, errors.New("no current task assigned")
	}
	prd, err := LoadPRD()
	if err != nil {
		return Task{}, err
	}
	task, ok := prd.Find(st.CurrentTaskID)
	if !ok {
		return Task{}, fmt.Errorf("current task %q not found in prd", st.CurrentTaskID)
	}
	return task, nil
}

// RepoRoot returns the working directory, used as the git checkpoint root.
func RepoRoot() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}
