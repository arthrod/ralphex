package oracle

import (
	"context"
	"fmt"

	"github.com/umputun/ralphex/pkg/plan"
)

// Proposer runs the oracle model on a prompt and returns its raw output. Consumer-side interface
// so pkg/oracle stays decoupled from the executor package.
type Proposer interface {
	Propose(ctx context.Context, prompt string) (string, error)
}

// Approver presents a proposed substitution to a human and reports whether it is approved. It is
// an interface so the interactive y/N prompt can be faked in tests.
type Approver interface {
	Approve(oldStr, newStr string) (bool, error)
}

// Outcome is the result of an oracle resolution.
type Outcome struct {
	Plan    string // the (possibly substituted) plan content
	Applied bool   // true when an approved substitution was applied
}

// Resolve asks the oracle model to propose one substring substitution for an escalated task,
// presents it to the human for approval, and applies it on approval. A rejected proposal leaves
// the plan untouched (Applied=false). A malformed proposal, a proposer failure, or a substitution
// whose OLD string is not in the plan are all errors — the oracle never silently changes nothing.
func Resolve(ctx context.Context, proposer Proposer, approver Approver, planContent, taskTitle, reason string) (Outcome, error) {
	out, err := proposer.Propose(ctx, buildOraclePrompt(planContent, taskTitle, reason))
	if err != nil {
		return Outcome{}, fmt.Errorf("oracle propose: %w", err)
	}

	oldStr, newStr, err := parseProposal(out)
	if err != nil {
		return Outcome{}, fmt.Errorf("parse oracle proposal: %w", err)
	}

	approved, err := approver.Approve(oldStr, newStr)
	if err != nil {
		return Outcome{}, fmt.Errorf("oracle approval: %w", err)
	}
	if !approved {
		return Outcome{Plan: planContent, Applied: false}, nil
	}

	newPlan, err := plan.ApplySubstitution(planContent, oldStr, newStr)
	if err != nil {
		return Outcome{}, fmt.Errorf("apply oracle substitution: %w", err)
	}
	return Outcome{Plan: newPlan, Applied: true}, nil
}

// buildOraclePrompt builds the prompt sent to the oracle model.
func buildOraclePrompt(planContent, taskTitle, reason string) string {
	r := reason
	if r == "" {
		r = "the task was rejected repeatedly by the inspector"
	}
	return "A task in an implementation plan appears infeasible or malformed and needs revision.\n\n" +
		"TASK: " + taskTitle + "\n" +
		"REASON FOR ESCALATION: " + r + "\n\n" +
		"Here is the full plan for context:\n\n" + planContent + "\n\n" +
		"Propose ONE literal substring substitution to the plan that would make the task feasible.\n" +
		"Respond with EXACTLY two lines and nothing else:\n" +
		"OLD: <exact substring currently in the plan>\n" +
		"NEW: <replacement text>"
}
