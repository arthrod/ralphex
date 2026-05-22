package inspector

import (
	"context"
	"fmt"
)

// maxVerdictRetries is how many extra times Inspect re-runs the reviewer when its output cannot
// be parsed into a verdict, before falling back to a conservative reject.
const maxVerdictRetries = 2

// Reviewer runs the external review tool on a prompt and returns its raw output. It is a
// consumer-side interface so pkg/inspector stays decoupled from the executor package; the runner
// adapts the configured codex/custom executor to it.
type Reviewer interface {
	Review(ctx context.Context, prompt string) (string, error)
}

// Inspect runs the reviewer on prompt and parses its verdict. Malformed output is retried up to
// maxVerdictRetries times; if it is still unparseable, Inspect returns a reject (never a silent
// accept) explaining the malformed verdict, so a confused inspector can never wave work through.
// A reviewer execution error (the tool failing to run) is propagated to the caller.
func Inspect(ctx context.Context, reviewer Reviewer, prompt string) (Verdict, error) {
	for attempt := 0; attempt <= maxVerdictRetries; attempt++ {
		out, err := reviewer.Review(ctx, prompt)
		if err != nil {
			return Verdict{}, fmt.Errorf("inspector review: %w", err)
		}
		if v, perr := parseVerdict(out); perr == nil {
			return v, nil
		}
	}
	return Verdict{
		Kind:    VerdictReject,
		Payload: "YOUR VERDICT WAS NOT IN THE REQUIRED FORMAT (VERDICT: done | reject | update). TREATING AS REJECTION — RE-READ THE TASK AND THE DIFF.",
	}, nil
}
