package agentbus

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendAndReadHandoffs(t *testing.T) {
	t.Setenv("AGENTBUS_DIR", t.TempDir())

	h1, err := AppendHandoff(Handoff{From: RoleWorker, To: RoleInspector, Message: "done"})
	require.NoError(t, err)
	assert.Equal(t, 1, h1.Seq)
	assert.False(t, h1.Time.IsZero())

	h2, err := AppendHandoff(Handoff{From: RoleInspector, To: RoleOrchestrator, Message: "looks good"})
	require.NoError(t, err)
	assert.Equal(t, 2, h2.Seq)

	all, err := ReadHandoffsSince(0)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "done", all[0].Message)
	assert.Equal(t, RoleOrchestrator, all[1].To)

	since1, err := ReadHandoffsSince(1)
	require.NoError(t, err)
	require.Len(t, since1, 1)
	assert.Equal(t, 2, since1[0].Seq)
}

func TestReadHandoffsMissing(t *testing.T) {
	t.Setenv("AGENTBUS_DIR", t.TempDir())
	all, err := ReadHandoffsSince(0)
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestAppendHandoffConcurrent(t *testing.T) {
	t.Setenv("AGENTBUS_DIR", t.TempDir())

	const n = 25
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for range n {
		wg.Go(func() {
			_, err := AppendHandoff(Handoff{From: RoleOracle, To: RoleWorker, Message: "go"})
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	all, err := ReadHandoffsSince(0)
	require.NoError(t, err)
	require.Len(t, all, n)

	// every sequence number is unique and covers 1..n (no interleaved corruption)
	seqs := map[int]bool{}
	for _, h := range all {
		assert.False(t, seqs[h.Seq], "duplicate seq %d", h.Seq)
		seqs[h.Seq] = true
	}
	for i := 1; i <= n; i++ {
		assert.True(t, seqs[i], "missing seq %d", i)
	}
}
