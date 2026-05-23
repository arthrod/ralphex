package agentbus

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandoffMacRoundTrip(t *testing.T) {
	key := []byte("a-32-byte-or-so-test-hmac-key!!!")
	h := Handoff{Seq: 7, From: RoleWorker, To: RoleInspector, Status: StatusDone, ConfirmCurrent: "task t1", Message: "done", Time: time.Unix(1700000000, 0).UTC()}

	mac := macForHandoff(h, key)
	assert.NotEmpty(t, mac)
	h.Mac = mac
	assert.True(t, VerifyHandoffMac(h, key), "freshly signed handoff must verify")

	t.Run("wrong key rejected", func(t *testing.T) {
		assert.False(t, VerifyHandoffMac(h, []byte("different-key-entirely-here-xxxx")))
	})

	t.Run("missing mac rejected", func(t *testing.T) {
		bare := h
		bare.Mac = ""
		assert.False(t, VerifyHandoffMac(bare, key))
	})

	// tampering with any field invalidates the mac
	tamper := []struct {
		name   string
		mutate func(*Handoff)
	}{
		{"seq", func(x *Handoff) { x.Seq = 8 }},
		{"from", func(x *Handoff) { x.From = RoleOracle }},
		{"to", func(x *Handoff) { x.To = RoleOrchestrator }},
		{"status", func(x *Handoff) { x.Status = StatusIncomplete }},
		{"confirm_current", func(x *Handoff) { x.ConfirmCurrent = "other" }},
		{"message", func(x *Handoff) { x.Message = "altered" }},
		{"time", func(x *Handoff) { x.Time = x.Time.Add(time.Second) }},
	}
	for _, tt := range tamper {
		t.Run("tampered_"+tt.name, func(t *testing.T) {
			altered := h // copy, keeps the original mac
			tt.mutate(&altered)
			assert.False(t, VerifyHandoffMac(altered, key), "tampering with %s must invalidate the mac", tt.name)
		})
	}
}

func TestAppendSignedHandoff(t *testing.T) {
	t.Setenv("AGENTBUS_DIR", t.TempDir())
	key := []byte("append-signed-test-key-32-bytes!")

	stored, err := AppendSignedHandoff(Handoff{From: RoleWorker, To: RoleInspector, Message: "done"}, key)
	require.NoError(t, err)
	assert.Equal(t, 1, stored.Seq)
	assert.False(t, stored.Time.IsZero())
	assert.NotEmpty(t, stored.Mac)
	assert.True(t, VerifyHandoffMac(stored, key))

	// the line read back from disk carries the same seq/time/mac that were signed
	all, err := ReadHandoffsSince(0)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, stored.Mac, all[0].Mac)
	assert.True(t, VerifyHandoffMac(all[0], key), "persisted line must verify")
}

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
