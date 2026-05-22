package state

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStore_Get_AbsentReturnsPending(t *testing.T) {
	s := newTestStore(t)

	got, err := s.Get(1)
	require.NoError(t, err)
	assert.Equal(t, TaskState{Position: 1, Status: StatusPending, AttemptCount: 0}, got)
}

func TestStore_SaveThenGet_RoundTrips(t *testing.T) {
	s := newTestStore(t)

	want := TaskState{Position: 2, Status: StatusRejected, AttemptCount: 1, LastCommit: "abc123"}
	require.NoError(t, s.Save(want))

	got, err := s.Get(2)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestStore_Save_UpsertsOnSamePosition(t *testing.T) {
	s := newTestStore(t)

	require.NoError(t, s.Save(TaskState{Position: 3, Status: StatusRejected, AttemptCount: 1}))
	require.NoError(t, s.Save(TaskState{Position: 3, Status: StatusDone, AttemptCount: 2, LastCommit: "deadbeef"}))

	got, err := s.Get(3)
	require.NoError(t, err)
	assert.Equal(t, TaskState{Position: 3, Status: StatusDone, AttemptCount: 2, LastCommit: "deadbeef"}, got)
}

func TestStore_Get_IsolatesByPosition(t *testing.T) {
	s := newTestStore(t)

	require.NoError(t, s.Save(TaskState{Position: 1, Status: StatusDone, AttemptCount: 0}))
	require.NoError(t, s.Save(TaskState{Position: 2, Status: StatusNeedsRevision, AttemptCount: 3}))

	t1, err := s.Get(1)
	require.NoError(t, err)
	assert.Equal(t, StatusDone, t1.Status)

	t2, err := s.Get(2)
	require.NoError(t, err)
	assert.Equal(t, StatusNeedsRevision, t2.Status)
	assert.Equal(t, 3, t2.AttemptCount)
}

func TestStore_PersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	s1, err := Open(path)
	require.NoError(t, err)
	require.NoError(t, s1.Save(TaskState{Position: 5, Status: StatusRejected, AttemptCount: 2, LastCommit: "cafe"}))
	require.NoError(t, s1.Close())

	s2, err := Open(path)
	require.NoError(t, err)
	defer func() { _ = s2.Close() }()

	got, err := s2.Get(5)
	require.NoError(t, err)
	assert.Equal(t, TaskState{Position: 5, Status: StatusRejected, AttemptCount: 2, LastCommit: "cafe"}, got)
}
