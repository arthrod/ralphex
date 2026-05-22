package inspector

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeReviewer returns canned outputs in sequence, recording how many times it was called.
type fakeReviewer struct {
	outputs []string
	err     error
	calls   int
}

func (f *fakeReviewer) Review(_ context.Context, _ string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	out := f.outputs[min(f.calls, len(f.outputs)-1)]
	f.calls++
	return out, nil
}

func TestInspect_WellFormedVerdict(t *testing.T) {
	r := &fakeReviewer{outputs: []string{"VERDICT: done"}}

	v, err := Inspect(context.Background(), r, "prompt")
	require.NoError(t, err)
	assert.Equal(t, VerdictDone, v.Kind)
	assert.Equal(t, 1, r.calls)
}

func TestInspect_RejectWithPayload(t *testing.T) {
	r := &fakeReviewer{outputs: []string{"VERDICT: reject | MISSING TESTS"}}

	v, err := Inspect(context.Background(), r, "prompt")
	require.NoError(t, err)
	assert.Equal(t, VerdictReject, v.Kind)
	assert.Equal(t, "MISSING TESTS", v.Payload)
}

func TestInspect_RetriesMalformedThenSucceeds(t *testing.T) {
	r := &fakeReviewer{outputs: []string{"garbage", "still garbage", "VERDICT: done"}}

	v, err := Inspect(context.Background(), r, "prompt")
	require.NoError(t, err)
	assert.Equal(t, VerdictDone, v.Kind)
	assert.Equal(t, 3, r.calls, "should retry malformed output before succeeding")
}

func TestInspect_MalformedAfterRetriesFallsBackToReject(t *testing.T) {
	r := &fakeReviewer{outputs: []string{"nope"}}

	v, err := Inspect(context.Background(), r, "prompt")
	require.NoError(t, err)
	assert.Equal(t, VerdictReject, v.Kind, "conservative fallback is reject, never silent accept")
	assert.NotEmpty(t, v.Payload, "fallback reject explains the malformed verdict")
	assert.Equal(t, 3, r.calls, "one initial call + two retries")
}

func TestInspect_ReviewerErrorPropagates(t *testing.T) {
	r := &fakeReviewer{err: errors.New("tool exploded")}

	_, err := Inspect(context.Background(), r, "prompt")
	require.Error(t, err)
}
