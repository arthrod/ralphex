package agentbus

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoleEnvFromRegistry(t *testing.T) {
	t.Setenv("AGENTBUS_DIR", t.TempDir())
	reg, err := GenerateRegistry()
	require.NoError(t, err)

	env, err := RoleEnvFromRegistry()
	require.NoError(t, err)
	require.Len(t, env, len(AllTools))

	// each role carries only its own tool's token
	assert.Contains(t, env[RoleWorker], tokenEnv+"="+reg[ToolWorker])
	assert.Contains(t, env[RoleOracle], tokenEnv+"="+reg[ToolOracle])
	for _, kv := range env[RoleWorker] {
		assert.NotEqual(t, tokenEnv+"="+reg[ToolOracle], kv, "worker session must not carry oracle's token")
	}
	assert.Contains(t, env[RoleInspector], "AGENTBUS_DIR="+Dir())
}

func TestAssignAndRun(t *testing.T) {
	t.Setenv("AGENTBUS_DIR", t.TempDir())
	_, err := GenerateRegistry()
	require.NoError(t, err)
	require.NoError(t, AddTask(Task{
		ID:           "t1",
		Summary:      "implement feature",
		MinTests:     []string{"go test ./..."},
		Instructions: "build it",
	}))

	launcher := &fakeLauncher{}
	require.NoError(t, AssignAndRun(context.Background(), launcher, "t1"))

	assert.Equal(t, 1, launcher.ensureCount)
	require.Len(t, launcher.resumes, 1)
	assert.Equal(t, RoleWorker, launcher.resumes[0].role)
	assert.Contains(t, launcher.resumes[0].message, "Task t1: implement feature")
	assert.Contains(t, launcher.resumes[0].message, "go test ./...")

	st, err := LoadState()
	require.NoError(t, err)
	assert.Equal(t, "t1", st.CurrentTaskID)
	assert.Equal(t, RoleWorker, st.ActiveRole)
	assert.Equal(t, "sid-worker", st.SessionID)
}

func TestAssignAndRunMissingTask(t *testing.T) {
	t.Setenv("AGENTBUS_DIR", t.TempDir())
	_, err := GenerateRegistry()
	require.NoError(t, err)
	assert.Error(t, AssignAndRun(context.Background(), &fakeLauncher{}, "nope"))
}

func TestCurrentTask(t *testing.T) {
	t.Setenv("AGENTBUS_DIR", t.TempDir())

	_, err := CurrentTask()
	assert.Error(t, err, "no task assigned yet")

	require.NoError(t, AddTask(Task{
		ID:           "t1",
		Summary:      "s",
		MinTests:     []string{"go test"},
		Instructions: "i",
	}))
	require.NoError(t, SaveState(State{CurrentTaskID: "t1"}))

	task, err := CurrentTask()
	require.NoError(t, err)
	assert.Equal(t, "t1", task.ID)
}

func TestRepoRoot(t *testing.T) {
	assert.NotEmpty(t, RepoRoot())
}

func TestWorkerPrompt(t *testing.T) {
	p := workerPrompt(Task{
		ID:           "t9",
		Summary:      "do x",
		MinTests:     []string{"make test", ""},
		Instructions: "instructions here",
	})
	assert.Contains(t, p, "Task t9: do x")
	assert.Contains(t, p, "instructions here")
	assert.Contains(t, p, "- make test")
	assert.True(t, strings.Contains(p, "no_further_actions"))
}
