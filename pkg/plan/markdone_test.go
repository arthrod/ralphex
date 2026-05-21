package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const markDoneSamplePlan = `# Plan

### Task 1: First

- [ ] do a
- [x] already done b

### Task 2: Second

- [ ] untouched c
`

func TestMarkTaskDone_ChecksTargetTaskBoxes(t *testing.T) {
	got, err := MarkTaskDone(markDoneSamplePlan, 1)
	require.NoError(t, err)

	p, err := ParsePlan(got)
	require.NoError(t, err)
	require.Len(t, p.Tasks, 2)

	// task 1 fully checked
	assert.Equal(t, TaskStatusDone, p.Tasks[0].Status)
	for _, cb := range p.Tasks[0].Checkboxes {
		assert.True(t, cb.Checked, "task 1 box %q should be checked", cb.Text)
	}

	// task 2 left alone
	assert.False(t, p.Tasks[1].Checkboxes[0].Checked, "task 2 must be untouched")
}

func TestMarkTaskDone_AlreadyDoneStaysDone(t *testing.T) {
	got, err := MarkTaskDone(markDoneSamplePlan, 1)
	require.NoError(t, err)
	// the already-checked box must not be corrupted (no double marker)
	assert.NotContains(t, got, "[x] already done b\n- [x] already done b")
	assert.Contains(t, got, "- [x] already done b")
}

func TestMarkTaskDone_UnknownTaskErrors(t *testing.T) {
	_, err := MarkTaskDone(markDoneSamplePlan, 99)
	require.Error(t, err)
}

func TestUncheckTask_ResetsTargetTaskBoxes(t *testing.T) {
	// a worker that ticked its own boxes (forging completion) must have them reset before it is
	// re-pointed at the task, so the parent's store stays the only source of completion truth.
	forged := "# Plan\n\n### Task 1: First\n\n- [x] do a\n- [x] do b\n\n### Task 2: Second\n\n- [x] untouched c\n"

	got, err := UncheckTask(forged, 1)
	require.NoError(t, err)

	p, err := ParsePlan(got)
	require.NoError(t, err)
	require.Len(t, p.Tasks, 2)

	// task 1 boxes reset to unchecked
	for _, cb := range p.Tasks[0].Checkboxes {
		assert.False(t, cb.Checked, "task 1 box %q should be unchecked", cb.Text)
	}
	// task 2 left alone
	assert.True(t, p.Tasks[1].Checkboxes[0].Checked, "task 2 must be untouched")
}

func TestUncheckTask_AlreadyUncheckedIsNoOp(t *testing.T) {
	// task 2 of the sample has only an unchecked box, so unchecking it must change nothing.
	got, err := UncheckTask(markDoneSamplePlan, 2)
	require.NoError(t, err)
	assert.Equal(t, markDoneSamplePlan, got, "unchecking an already-unchecked task changes nothing")
}

func TestUncheckTask_UnknownTaskErrors(t *testing.T) {
	_, err := UncheckTask(markDoneSamplePlan, 99)
	require.Error(t, err)
}
