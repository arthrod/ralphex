package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/inspector"
	"github.com/umputun/ralphex/pkg/state"
)

const gatePlan = `# Plan

### Task 1: Do the thing

- [ ] implement it
- [ ] test it
`

func TestApplyVerdict_Done_MarksTaskAndAccepts(t *testing.T) {
	cur := state.TaskState{Position: 1, Status: state.StatusPending}
	out, err := applyVerdict(gatePlan, 1, cur, inspector.Verdict{Kind: inspector.VerdictDone}, 3)
	require.NoError(t, err)

	assert.True(t, out.accepted)
	assert.False(t, out.escalate)
	assert.Equal(t, state.StatusDone, out.state.Status)
	assert.NotContains(t, out.plan, "- [ ] implement it", "boxes should be checked")
	assert.Contains(t, out.plan, "- [x] implement it")
}

func TestApplyVerdict_RejectUnderThreshold_YellsAndRetries(t *testing.T) {
	cur := state.TaskState{Position: 1, Status: state.StatusPending, AttemptCount: 0}
	out, err := applyVerdict(gatePlan, 1, cur,
		inspector.Verdict{Kind: inspector.VerdictReject, Payload: "NO TESTS"}, 3)
	require.NoError(t, err)

	assert.False(t, out.accepted)
	assert.False(t, out.escalate, "1st reject is under threshold 3")
	assert.Equal(t, state.StatusRejected, out.state.Status)
	assert.Equal(t, 1, out.state.AttemptCount)
	assert.Contains(t, out.plan, "NO TESTS")
	assert.Contains(t, out.plan, "attempt 1")
}

func TestApplyVerdict_RejectHittingThreshold_Escalates(t *testing.T) {
	cur := state.TaskState{Position: 1, Status: state.StatusRejected, AttemptCount: 2}
	out, err := applyVerdict(gatePlan, 1, cur,
		inspector.Verdict{Kind: inspector.VerdictReject, Payload: "STILL BROKEN"}, 3)
	require.NoError(t, err)

	assert.False(t, out.accepted)
	assert.True(t, out.escalate, "3rd reject hits threshold -> oracle")
	assert.Equal(t, state.StatusNeedsRevision, out.state.Status)
	assert.Equal(t, 3, out.state.AttemptCount)
	assert.Contains(t, out.plan, "STILL BROKEN")
	assert.Equal(t, "STILL BROKEN", out.reason, "escalation carries the inspector's complaint to the oracle")
}

func TestApplyVerdict_Update_EscalatesWithoutCountingAttempt(t *testing.T) {
	cur := state.TaskState{Position: 1, Status: state.StatusPending, AttemptCount: 1}
	out, err := applyVerdict(gatePlan, 1, cur,
		inspector.Verdict{Kind: inspector.VerdictUpdate, Payload: "task references missing API"}, 3)
	require.NoError(t, err)

	assert.False(t, out.accepted)
	assert.True(t, out.escalate)
	assert.Equal(t, state.StatusNeedsRevision, out.state.Status)
	assert.Equal(t, 1, out.state.AttemptCount, "update is not the worker's fault; attempt count unchanged")
	assert.Equal(t, "task references missing API", out.reason, "update verdict's explanation reaches the oracle")
}

func TestApplyVerdict_UnknownTaskErrors(t *testing.T) {
	cur := state.TaskState{Position: 99, Status: state.StatusPending}
	_, err := applyVerdict(gatePlan, 99, cur, inspector.Verdict{Kind: inspector.VerdictDone}, 3)
	require.Error(t, err)
}
