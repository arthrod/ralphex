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
