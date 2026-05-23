package agentbus

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validTask() Task {
	return Task{
		ID:           "t1",
		Summary:      "do the thing",
		MinTests:     []string{"go test ./..."},
		Instructions: "implement the thing carefully",
	}
}

func TestTaskValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Task)
		wantErr bool
	}{
		{"valid", func(*Task) {}, false},
		{"empty id", func(t *Task) { t.ID = "" }, true},
		{"empty summary", func(t *Task) { t.Summary = "" }, true},
		{"multiline summary", func(t *Task) { t.Summary = "line1\nline2" }, true},
		{"no min tests", func(t *Task) { t.MinTests = nil }, true},
		{"blank min tests", func(t *Task) { t.MinTests = []string{"  ", ""} }, true},
		{"empty instructions", func(t *Task) { t.Instructions = "   " }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := validTask()
			tt.mutate(&task)
			err := task.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestPRDRoundTrip(t *testing.T) {
	t.Setenv("AGENTBUS_DIR", t.TempDir())

	require.NoError(t, AddTask(validTask()))

	t2 := validTask()
	t2.ID = "t2"
	t2.Summary = "second"
	require.NoError(t, AddTask(t2))

	// duplicate id rejected
	assert.Error(t, AddTask(validTask()))

	prd, err := LoadPRD()
	require.NoError(t, err)
	assert.Len(t, prd.Tasks, 2)

	// update existing
	upd := validTask()
	upd.Summary = "updated summary"
	require.NoError(t, UpdateTask(upd))
	got, ok := prd2Find(t, "t1")
	require.True(t, ok)
	assert.Equal(t, "updated summary", got.Summary)

	// update missing fails
	missing := validTask()
	missing.ID = "nope"
	assert.Error(t, UpdateTask(missing))

	// remove
	require.NoError(t, RemoveTask("t1"))
	_, ok = prd2Find(t, "t1")
	assert.False(t, ok)
	assert.Error(t, RemoveTask("t1"))
}

func prd2Find(t *testing.T, id string) (Task, bool) {
	t.Helper()
	prd, err := LoadPRD()
	require.NoError(t, err)
	return prd.Find(id)
}

func TestLoadPRDMissing(t *testing.T) {
	t.Setenv("AGENTBUS_DIR", t.TempDir())
	prd, err := LoadPRD()
	require.NoError(t, err)
	assert.Empty(t, prd.Tasks)
}
