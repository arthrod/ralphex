package plan

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const yellingSamplePlan = `# My Plan

## Overview

Some context here.

### Task 1: First task

- [ ] do the thing
- [ ] test the thing

### Task 2: Second task

- [ ] another thing

## Success criteria

- everything works
`

func TestAppendYelling_InsertsIntoTargetTaskSection(t *testing.T) {
	got, err := AppendYelling(yellingSamplePlan, 1, 1, "YOU FORGOT THE ERROR HANDLING")
	require.NoError(t, err)

	assert.Contains(t, got, "YOU FORGOT THE ERROR HANDLING")
	assert.Contains(t, got, "attempt 1")

	// the yelling must land inside Task 1's section: before the "### Task 2" header
	idxYell := strings.Index(got, "YOU FORGOT THE ERROR HANDLING")
	idxTask2 := strings.Index(got, "### Task 2:")
	require.NotEqual(t, -1, idxYell)
	require.NotEqual(t, -1, idxTask2)
	assert.Less(t, idxYell, idxTask2, "yelling should be inside Task 1, before Task 2")
}

func TestAppendYelling_PreservesParseability(t *testing.T) {
	got, err := AppendYelling(yellingSamplePlan, 1, 2, "REDO IT")
	require.NoError(t, err)

	// the plan must still parse to the same two tasks with the same checkboxes
	p, err := ParsePlan(got)
	require.NoError(t, err)
	require.Len(t, p.Tasks, 2)
	assert.Equal(t, "First task", p.Tasks[0].Title)
	assert.Len(t, p.Tasks[0].Checkboxes, 2)
	assert.Equal(t, "Second task", p.Tasks[1].Title)
	assert.Len(t, p.Tasks[1].Checkboxes, 1)
}

func TestAppendYelling_Accumulates(t *testing.T) {
	once, err := AppendYelling(yellingSamplePlan, 2, 1, "FIRST COMPLAINT")
	require.NoError(t, err)
	twice, err := AppendYelling(once, 2, 2, "SECOND COMPLAINT")
	require.NoError(t, err)

	assert.Contains(t, twice, "FIRST COMPLAINT")
	assert.Contains(t, twice, "SECOND COMPLAINT")
	assert.Contains(t, twice, "attempt 1")
	assert.Contains(t, twice, "attempt 2")
}

func TestAppendYelling_LastTaskAppendsAtEnd(t *testing.T) {
	planNoTrailingSection := `# Plan

### Task 1: Only task

- [ ] do it
`
	got, err := AppendYelling(planNoTrailingSection, 1, 1, "NOPE")
	require.NoError(t, err)
	assert.Contains(t, got, "NOPE")

	p, err := ParsePlan(got)
	require.NoError(t, err)
	require.Len(t, p.Tasks, 1)
	assert.Len(t, p.Tasks[0].Checkboxes, 1)
}

func TestAppendYelling_UnknownTaskErrors(t *testing.T) {
	_, err := AppendYelling(yellingSamplePlan, 99, 1, "WHATEVER")
	require.Error(t, err)
}
