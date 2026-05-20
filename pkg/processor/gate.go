package processor

import (
	"fmt"

	"github.com/umputun/ralphex/pkg/inspector"
	"github.com/umputun/ralphex/pkg/plan"
	"github.com/umputun/ralphex/pkg/state"
)

// verdictOutcome is the result of applying an inspector verdict to a task: the (possibly mutated)
// plan content, the new persisted task state, and routing flags for the task loop.
type verdictOutcome struct {
	plan     string          // updated plan markdown
	state    state.TaskState // new state to persist
	accepted bool            // task is done; advance to the next task
	escalate bool            // route to the oracle (needs_revision)
}

// applyVerdict turns an inspector verdict into a plan mutation and a state transition. It is the
// gate's policy core, kept pure so it can be tested without the streaming loop:
//
//   - done   -> parent checks the task's boxes (MarkTaskDone); status done; accepted.
//   - reject -> attempt++, yelling appended to the plan; status rejected, or needs_revision
//     (escalate) once attempts reach maxAttempts.
//   - update -> status needs_revision; escalate. Not the worker's fault, so attempts are unchanged
//     and no yelling is written; the oracle handles the spec.
func applyVerdict(planContent string, taskNum int, cur state.TaskState, v inspector.Verdict, maxAttempts int) (verdictOutcome, error) {
	out := verdictOutcome{plan: planContent, state: cur}

	switch v.Kind {
	case inspector.VerdictDone:
		newPlan, err := plan.MarkTaskDone(planContent, taskNum)
		if err != nil {
			return verdictOutcome{}, fmt.Errorf("mark task %d done: %w", taskNum, err)
		}
		out.plan = newPlan
		out.state.Status = state.StatusDone
		out.accepted = true

	case inspector.VerdictReject:
		out.state.AttemptCount = cur.AttemptCount + 1
		newPlan, err := plan.AppendYelling(planContent, taskNum, out.state.AttemptCount, v.Payload)
		if err != nil {
			return verdictOutcome{}, fmt.Errorf("append yelling to task %d: %w", taskNum, err)
		}
		out.plan = newPlan
		if out.state.AttemptCount >= maxAttempts {
			out.state.Status = state.StatusNeedsRevision
			out.escalate = true
		} else {
			out.state.Status = state.StatusRejected
		}

	case inspector.VerdictUpdate:
		// update is not the worker's fault: attempts unchanged, no yelling. the oracle handles the
		// spec from here. the loop only ever passes a live nextPlanTaskPosition, so no plan mutation
		// (and thus no task-existence check) is needed.
		out.state.Status = state.StatusNeedsRevision
		out.escalate = true

	default:
		return verdictOutcome{}, fmt.Errorf("unknown verdict kind %q", v.Kind)
	}

	return out, nil
}
