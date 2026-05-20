package processor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/umputun/ralphex/pkg/inspector"
	"github.com/umputun/ralphex/pkg/oracle"
	"github.com/umputun/ralphex/pkg/plan"
	"github.com/umputun/ralphex/pkg/state"
	"github.com/umputun/ralphex/pkg/status"
)

// defaultMaxTaskAttempts is the reject threshold after which a task escalates to the oracle.
const defaultMaxTaskAttempts = 3

// executorReviewer adapts an Executor to inspector.Reviewer so the configured external-review tool
// (codex/custom) can serve as the inspector engine.
type executorReviewer struct{ exec Executor }

func (e executorReviewer) Review(ctx context.Context, prompt string) (string, error) {
	res := e.exec.Run(ctx, prompt)
	if res.Error != nil {
		return "", res.Error
	}
	return res.Output, nil
}

// runTaskPhaseGated runs the credential-gated per-task loop: the worker does ONE task and proposes
// completion, then a separately-credentialed inspector renders a verdict the parent acts on. The
// worker can never self-certify because the parent both checks the boxes (on done) and parses the
// verdict (which the worker's scrubbed env cannot forge).
func (r *Runner) runTaskPhaseGated(ctx context.Context) error {
	// the gate needs a separately-credentialed inspector. when the gate is enabled but no external
	// review tool (codex/custom) is configured, reviewer is nil; fail with a clear error instead of
	// panicking on a nil dereference inside inspector.Inspect.
	if r.reviewer == nil {
		return errors.New("inspector gate enabled but no external review tool (codex/custom) is configured")
	}

	store, err := r.openStateStore()
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	prompt := r.replacePromptVariables(buildGatedTaskPrompt())

	for i := 1; i <= r.cfg.MaxIterations; i++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("task phase: %w", err)
		}

		taskNum := r.nextPlanTaskPosition()
		if taskNum == 0 {
			r.log.PrintRaw("\nall tasks accepted by inspector, starting code review...\n")
			return nil
		}
		r.log.PrintSection(status.NewTaskIterationSection(taskNum))

		st, err := store.Get(taskNum)
		if err != nil {
			return fmt.Errorf("read task %d state: %w", taskNum, err)
		}

		preHash, err := r.git.HeadHash()
		if err != nil {
			return fmt.Errorf("capture pre-task HEAD: %w", err)
		}

		result := r.runWithLimitRetry(ctx, r.claude.Run, prompt, "claude")
		if result.Error != nil {
			if perr := r.handlePatternMatchError(result.Error, "claude"); perr != nil {
				return perr
			}
			return fmt.Errorf("claude execution: %w", result.Error)
		}
		if !isPeasantTired(result.Signal) {
			// worker did not propose completion for this task; re-run it.
			r.log.Print("worker did not signal task completion, retrying task")
			continue
		}

		outcome, err := r.inspectAndApply(ctx, taskNum, preHash, st)
		if err != nil {
			return err
		}
		if err := os.WriteFile(r.resolvePlanFilePath(), []byte(outcome.plan), 0o600); err != nil {
			return fmt.Errorf("write plan after verdict: %w", err)
		}
		if err := store.Save(outcome.state); err != nil {
			return fmt.Errorf("save task %d state: %w", taskNum, err)
		}

		if outcome.escalate {
			r.log.Print("task %d escalated to oracle after %d attempt(s)", taskNum, outcome.state.AttemptCount)
			resumed, oerr := r.runOracle(ctx, taskNum, store)
			if oerr != nil {
				return oerr
			}
			if !resumed {
				return ErrUserAborted
			}
			// oracle applied an approved fix and reset the task to pending: loop re-runs it.
		}
		// accepted or rejected-under-threshold: loop continues (next task, or retry with yelling).
	}
	return fmt.Errorf("max iterations (%d) reached without all tasks accepted", r.cfg.MaxIterations)
}

// inspectAndApply diffs the worker's changes for the task, runs the inspector, and applies the
// verdict to the plan and state. Pure orchestration around the tested gate primitives.
func (r *Runner) inspectAndApply(ctx context.Context, taskNum int, preHash string, cur state.TaskState) (verdictOutcome, error) {
	postHash, err := r.git.HeadHash()
	if err != nil {
		return verdictOutcome{}, fmt.Errorf("capture post-task HEAD: %w", err)
	}
	cur.LastCommit = postHash

	diff, err := r.git.Diff(preHash, postHash)
	if err != nil {
		return verdictOutcome{}, fmt.Errorf("diff worker changes: %w", err)
	}

	planContent, err := os.ReadFile(r.resolvePlanFilePath())
	if err != nil {
		return verdictOutcome{}, fmt.Errorf("read plan for inspection: %w", err)
	}

	taskTitle := r.planTaskTitle(taskNum)
	verdict, err := inspector.Inspect(ctx, r.reviewer, buildInspectorPrompt(taskTitle, diff))
	if err != nil {
		return verdictOutcome{}, fmt.Errorf("inspector: %w", err)
	}
	r.log.Print("inspector verdict for task %d: %s", taskNum, verdict.Kind)

	maxAttempts := r.maxTaskAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxTaskAttempts
	}
	return applyVerdict(string(planContent), taskNum, cur, verdict, maxAttempts)
}

// openStateStore opens (creating dirs as needed) the per-task inspector state store.
func (r *Runner) openStateStore() (*state.Store, error) {
	path := r.inspectorStateDB
	if path == "" {
		path = filepath.Join(".ralphex", "inspector-state.db")
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("create state dir: %w", err)
		}
	}
	store, err := state.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open inspector state: %w", err)
	}
	return store, nil
}

// planTaskTitle returns the title of the task at the given 1-based position, or "" if unavailable.
func (r *Runner) planTaskTitle(taskNum int) string {
	p, err := plan.ParsePlanFile(r.resolvePlanFilePath())
	if err != nil || taskNum < 1 || taskNum > len(p.Tasks) {
		return ""
	}
	return p.Tasks[taskNum-1].Title
}

// buildGatedTaskPrompt instructs the worker to complete exactly one task and propose completion,
// without ticking checkboxes (the parent does that only after the inspector approves).
func buildGatedTaskPrompt() string {
	return "Work on ONLY the next uncompleted task in the plan file {{PLAN_FILE}}.\n\n" +
		"Rules:\n" +
		"- Implement just that one task. Do NOT start other tasks.\n" +
		"- Do NOT tick any checkboxes. Completion is recorded by a separate inspector, not by you.\n" +
		"- Commit your work with git when finished.\n" +
		"- If a previous attempt left an 'INSPECTOR REJECTION' note in the task, address it specifically.\n" +
		"- When you have committed your work for this one task, output exactly:\n" +
		status.PeasantTired + "\n"
}

// runOracle resolves an escalated task: it proposes a substitution via the external tool, asks the
// user to approve it, and on approval applies it to the plan and resets the task to pending so the
// loop retries it. Returns resumed=false when the user declines (the run should abort).
func (r *Runner) runOracle(ctx context.Context, taskNum int, store *state.Store) (resumed bool, err error) {
	exec := r.externalExecutor()
	if exec == nil {
		return false, fmt.Errorf("oracle requires an external review tool (codex/custom) but none is configured")
	}
	if r.inputCollector == nil {
		return false, fmt.Errorf("oracle requires interactive input but no input collector is set")
	}

	planContent, err := os.ReadFile(r.resolvePlanFilePath())
	if err != nil {
		return false, fmt.Errorf("read plan for oracle: %w", err)
	}

	out, err := oracle.Resolve(ctx, executorProposer{exec: exec}, inputApprover{ic: r.inputCollector, ctx: ctx},
		string(planContent), r.planTaskTitle(taskNum), "rejected repeatedly by the inspector")
	if err != nil {
		return false, fmt.Errorf("oracle: %w", err)
	}
	if !out.Applied {
		r.log.Print("oracle proposal declined; aborting run")
		return false, nil
	}

	if err := os.WriteFile(r.resolvePlanFilePath(), []byte(out.Plan), 0o600); err != nil {
		return false, fmt.Errorf("write plan after oracle: %w", err)
	}
	st, err := store.Get(taskNum)
	if err != nil {
		return false, fmt.Errorf("read task %d state after oracle: %w", taskNum, err)
	}
	st.AttemptCount = 0
	st.Status = state.StatusPending
	if err := store.Save(st); err != nil {
		return false, fmt.Errorf("reset task %d state after oracle: %w", taskNum, err)
	}
	r.log.Print("oracle fix applied to task %d; retrying", taskNum)
	return true, nil
}

// externalExecutor returns the configured external tool (custom preferred, else codex) used as the
// inspector and oracle engine, or nil if none is configured.
func (r *Runner) externalExecutor() Executor {
	if r.custom != nil {
		return r.custom
	}
	return r.codex
}

// executorProposer adapts an Executor to oracle.Proposer.
type executorProposer struct{ exec Executor }

func (p executorProposer) Propose(ctx context.Context, prompt string) (string, error) {
	res := p.exec.Run(ctx, prompt)
	if res.Error != nil {
		return "", res.Error
	}
	return res.Output, nil
}

// inputApprover adapts the runner's InputCollector to oracle.Approver, presenting the proposed
// substitution as a Yes/No question.
type inputApprover struct {
	ic  InputCollector
	ctx context.Context //nolint:containedctx // short-lived per-resolve adapter; Approver has no ctx param
}

func (a inputApprover) Approve(oldStr, newStr string) (bool, error) {
	q := fmt.Sprintf("Oracle proposes a fix to the task spec:\n  OLD: %s\n  NEW: %s\nApply this change?", oldStr, newStr)
	ans, err := a.ic.AskQuestion(a.ctx, q, []string{"Yes", "No"})
	if err != nil {
		return false, err
	}
	return ans == "Yes", nil
}

// buildInspectorPrompt builds the verdict prompt sent to the external inspector tool.
func buildInspectorPrompt(taskTitle, diff string) string {
	return "You are inspecting a worker agent's completion of a single task.\n\n" +
		"TASK: " + taskTitle + "\n\n" +
		"The worker's git diff for this task:\n\n" +
		diff + "\n\n" +
		"Decide one of:\n" +
		"- VERDICT: done — the task is complete and correct\n" +
		"- VERDICT: reject | <YELLING IN CAPS WITH SPECIFICS> — incomplete or wrong; the worker must retry\n" +
		"- VERDICT: update | <polite explanation> — the task itself appears infeasible or malformed\n\n" +
		"Respond with EXACTLY one line in that format and nothing else."
}
