package processor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

		taskNum, err := r.nextGatedTaskPosition(store)
		if err != nil {
			return err
		}
		if taskNum == 0 {
			r.log.PrintRaw("\nall tasks accepted by inspector, starting code review...\n")
			return nil
		}
		r.log.PrintSection(status.NewTaskIterationSection(taskNum))

		// reconcile: the store does not consider this task done, so reset any checkbox a worker may
		// have ticked itself. the plan stays a faithful projection of the parent-controlled store,
		// and the worker is pointed at the true pending task rather than a forged-complete one.
		if rerr := r.resetTaskCheckboxes(taskNum); rerr != nil {
			return rerr
		}

		st, err := store.Get(taskNum)
		if err != nil {
			return fmt.Errorf("read task %d state: %w", taskNum, err)
		}

		// a task persisted as needs_revision (escalated, but the oracle never resolved it — e.g. the
		// process was interrupted after escalation) must be routed to the oracle BEFORE the worker
		// runs again. otherwise a restart would re-dispatch an unresolved escalated task to the worker,
		// skipping the escalation state machine.
		if st.Status == state.StatusNeedsRevision {
			r.log.Print("task %d is awaiting oracle revision; resolving before re-running the worker", taskNum)
			if eerr := r.resolveEscalation(ctx, taskNum, store, "previously escalated to the oracle for revision"); eerr != nil {
				return eerr
			}
			continue // oracle reset the task to pending; re-select and run the worker
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
		if err := writePlanPreservingMode(r.resolvePlanFilePath(), []byte(outcome.plan)); err != nil {
			return err
		}
		if err := store.Save(outcome.state); err != nil {
			return fmt.Errorf("save task %d state: %w", taskNum, err)
		}

		if outcome.escalate {
			r.log.Print("task %d escalated to oracle after %d attempt(s)", taskNum, outcome.state.AttemptCount)
			if eerr := r.resolveEscalation(ctx, taskNum, store, outcome.reason); eerr != nil {
				return eerr
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
	taskCriteria := r.planTaskCriteria(taskNum)
	verdict, err := inspector.Inspect(ctx, r.reviewer, buildInspectorPrompt(taskTitle, taskCriteria, diff))
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

// nextGatedTaskPosition returns the 1-based position of the first task the store has NOT marked
// done, or 0 when every task is done. Selection and the all-done decision are driven by the
// parent-controlled state store rather than the plan's checkboxes, so a worker that ticks its own
// boxes can neither skip inspection nor end the run — only an inspector "done" verdict sets
// StatusDone.
func (r *Runner) nextGatedTaskPosition(store *state.Store) (int, error) {
	p, err := plan.ParsePlanFile(r.resolvePlanFilePath())
	if err != nil {
		return 0, fmt.Errorf("parse plan for task selection: %w", err)
	}
	for i := range p.Tasks {
		pos := i + 1
		st, err := store.Get(pos)
		if err != nil {
			return 0, fmt.Errorf("read task %d state: %w", pos, err)
		}
		if st.Status != state.StatusDone {
			return pos, nil
		}
	}
	return 0, nil
}

// resetTaskCheckboxes un-ticks the given task's checkboxes in the plan file so a worker-forged tick
// cannot misdirect the next attempt. It is a no-op when the boxes are already unchecked.
func (r *Runner) resetTaskCheckboxes(taskNum int) error {
	path := r.resolvePlanFilePath()
	content, err := os.ReadFile(path) //nolint:gosec // plan path is resolved from trusted config
	if err != nil {
		return fmt.Errorf("read plan to reset task %d checkboxes: %w", taskNum, err)
	}
	reset, err := plan.UncheckTask(string(content), taskNum)
	if err != nil {
		return fmt.Errorf("reset task %d checkboxes: %w", taskNum, err)
	}
	if reset == string(content) {
		return nil
	}
	if err := writePlanPreservingMode(path, []byte(reset)); err != nil {
		return fmt.Errorf("reset task %d checkboxes: %w", taskNum, err)
	}
	return nil
}

// writePlanPreservingMode writes content to the plan path, preserving the file's existing
// permission bits when it already exists (defaulting to 0600 for a new file). This avoids silently
// resetting a repo- or user-chosen mode every time the gate rewrites the plan.
func writePlanPreservingMode(path string, content []byte) error {
	mode := os.FileMode(0o600)
	if fi, statErr := os.Stat(path); statErr == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		return fmt.Errorf("write plan %s: %w", path, err)
	}
	return nil
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

// planTaskCriteria returns the task's checklist items as the acceptance criteria the inspector
// judges the diff against. Empty if the task is unavailable.
//
// The checked/unchecked state is deliberately NOT rendered: a worker could otherwise tick the
// boxes itself to make incomplete work look satisfied and steer the inspector. The inspector must
// judge the diff against what the task requires, so every item is presented as an unchecked
// requirement regardless of the (worker-mutable) on-disk mark.
func (r *Runner) planTaskCriteria(taskNum int) string {
	p, err := plan.ParsePlanFile(r.resolvePlanFilePath())
	if err != nil || taskNum < 1 || taskNum > len(p.Tasks) {
		return ""
	}
	var b strings.Builder
	for _, cb := range p.Tasks[taskNum-1].Checkboxes {
		fmt.Fprintf(&b, "- [ ] %s\n", cb.Text)
	}
	return strings.TrimRight(b.String(), "\n")
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

// resolveEscalation runs the oracle for an escalated task and maps its outcome to a loop-control
// error: nil when the task was resolved (caller re-runs it), ErrUserAborted when the user declined
// the proposal, or the oracle error otherwise. It keeps the two escalation call sites in the gated
// loop small.
func (r *Runner) resolveEscalation(ctx context.Context, taskNum int, store *state.Store, reason string) error {
	resumed, err := r.runOracle(ctx, taskNum, store, reason)
	if err != nil {
		return err
	}
	if !resumed {
		return ErrUserAborted
	}
	return nil
}

// runOracle resolves an escalated task: it proposes a substitution via the external tool, asks the
// user to approve it, and on approval applies it to the plan and resets the task to pending so the
// loop retries it. Returns resumed=false when the user declines (the run should abort).
func (r *Runner) runOracle(ctx context.Context, taskNum int, store *state.Store, reason string) (resumed bool, err error) {
	exec := r.externalExecutor()
	if exec == nil {
		return false, errors.New("oracle requires an external review tool (codex/custom) but none is configured")
	}
	if r.inputCollector == nil {
		return false, errors.New("oracle requires interactive input but no input collector is set")
	}

	planContent, err := os.ReadFile(r.resolvePlanFilePath())
	if err != nil {
		return false, fmt.Errorf("read plan for oracle: %w", err)
	}

	if strings.TrimSpace(reason) == "" {
		reason = "rejected repeatedly by the inspector"
	}
	out, err := oracle.Resolve(ctx, executorProposer{exec: exec}, inputApprover{ic: r.inputCollector, ctx: ctx},
		string(planContent), r.planTaskTitle(taskNum), reason)
	if err != nil {
		return false, fmt.Errorf("oracle: %w", err)
	}
	if !out.Applied {
		r.log.Print("oracle proposal declined; aborting run")
		return false, nil
	}

	if werr := writePlanPreservingMode(r.resolvePlanFilePath(), []byte(out.Plan)); werr != nil {
		return false, werr
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
		return false, fmt.Errorf("oracle approval prompt: %w", err)
	}
	return ans == "Yes", nil
}

// buildInspectorPrompt builds the verdict prompt sent to the external inspector tool. The task's
// acceptance criteria (its checklist) are included so the inspector can judge the diff against what
// the task actually asks for — both completeness (did the criteria get met) and scope (does the diff
// stay within them) — rather than falling back to a blunt "is the diff non-empty" heuristic.
func buildInspectorPrompt(taskTitle, taskCriteria, diff string) string {
	criteria := strings.TrimSpace(taskCriteria)
	if criteria == "" {
		criteria = "(no explicit acceptance criteria provided)"
	}
	return "You are inspecting a worker agent's completion of a single task.\n\n" +
		"TASK: " + taskTitle + "\n\n" +
		"ACCEPTANCE CRITERIA (what this task — and only this task — should accomplish):\n" +
		criteria + "\n\n" +
		"The worker's git diff for this task:\n\n" +
		diff + "\n\n" +
		"Judge the diff against the acceptance criteria on BOTH axes:\n" +
		"- completeness: do the changes satisfy every criterion? an empty diff can still be correct if a\n" +
		"  criterion is phrased as 'ensure X' and X already holds.\n" +
		"- scope: do the changes stay within this task? work that belongs to other tasks is out of scope.\n\n" +
		"Decide one of:\n" +
		"- VERDICT: done — the criteria are met and the diff stays in scope\n" +
		"- VERDICT: reject | <YELLING IN CAPS WITH SPECIFICS> — incomplete, wrong, or out of scope; retry\n" +
		"- VERDICT: update | <polite explanation> — the task itself appears infeasible or malformed\n\n" +
		"Respond with EXACTLY one line in that format and nothing else."
}
