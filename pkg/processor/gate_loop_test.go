package processor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/executor"
	"github.com/umputun/ralphex/pkg/processor/mocks"
	"github.com/umputun/ralphex/pkg/state"
	"github.com/umputun/ralphex/pkg/status"
)

// stubReviewer returns a fixed verdict string regardless of prompt.
type stubReviewer struct{ out string }

func (s stubReviewer) Review(_ context.Context, _ string) (string, error) { return s.out, nil }

// newGatedRunner builds a Runner wired for the inspector gate against a temp plan file and db,
// with a worker that always proposes completion and an injectable inspector verdict.
func newGatedRunner(t *testing.T, verdict string) (*Runner, string) {
	t.Helper()
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.md")
	require.NoError(t, os.WriteFile(planPath, []byte(oneTaskPlan), 0o600))

	worker := &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, _ string) executor.Result {
			return executor.Result{Signal: status.PeasantTired, Output: "done one task"}
		},
	}
	gitMock := &mocks.GitCheckerMock{
		HeadHashFunc: func() (string, error) { return "deadbeef", nil },
		DiffFunc:     func(_, _ string) (string, error) { return "diff --git a/x b/x\n+code", nil },
	}

	appCfg, err := config.Load(t.TempDir())
	require.NoError(t, err)

	cfg := Config{
		Mode:                 ModeFull,
		MaxIterations:        6,
		PlanFile:             planPath,
		InspectorGateEnabled: true,
		AppConfig:            appCfg,
	}
	r := NewWithExecutors(cfg, newMockLogger("progress.txt"), Executors{Claude: worker}, &status.PhaseHolder{})
	r.SetGitChecker(gitMock)
	r.reviewer = stubReviewer{out: verdict}
	r.inspectorStateDB = filepath.Join(dir, "state.db")
	return r, planPath
}

const oneTaskPlan = `# Plan

### Task 1: Do it

- [ ] implement
`

func TestRunTaskPhaseGated_NilReviewerReturnsError(t *testing.T) {
	// gate enabled but no codex/custom reviewer wired: must return a clear error, not panic.
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.md")
	require.NoError(t, os.WriteFile(planPath, []byte(oneTaskPlan), 0o600))
	cfg := Config{Mode: ModeFull, MaxIterations: 3, PlanFile: planPath, InspectorGateEnabled: true}
	r := NewWithExecutors(cfg, newMockLogger("progress.txt"), Executors{Claude: &mocks.ExecutorMock{}}, &status.PhaseHolder{})
	r.inspectorStateDB = filepath.Join(dir, "state.db")
	require.Nil(t, r.reviewer, "no codex/custom executor means no reviewer")

	err := r.runTaskPhaseGated(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no external review tool")
}

func TestRunTaskPhaseGated_DoneAcceptsAndCompletes(t *testing.T) {
	r, planPath := newGatedRunner(t, "VERDICT: done")

	err := r.runTaskPhaseGated(context.Background())
	require.NoError(t, err)

	got, err := os.ReadFile(planPath) //nolint:gosec // test plan path from t.TempDir
	require.NoError(t, err)
	assert.Contains(t, string(got), "- [x] implement", "parent should check the box on done verdict")
}

func TestRunTaskPhaseGated_PersistedNeedsRevisionRoutesToOracleFirst(t *testing.T) {
	// simulate a restart: the store already holds the task as needs_revision (escalated, but the
	// oracle never resolved it). the loop must route it to the oracle before re-running the worker.
	r, planPath := newGatedRunner(t, "VERDICT: done")
	r.codex = &mocks.ExecutorMock{RunFunc: func(_ context.Context, _ string) executor.Result {
		return executor.Result{Output: "OLD: implement\nNEW: build it"}
	}}
	r.inputCollector = &mocks.InputCollectorMock{
		AskQuestionFunc: func(_ context.Context, _ string, _ []string) (string, error) { return "Yes", nil },
	}

	store, err := r.openStateStore()
	require.NoError(t, err)
	require.NoError(t, store.Save(state.TaskState{Position: 1, Status: state.StatusNeedsRevision, AttemptCount: 3}))
	require.NoError(t, store.Close())

	err = r.runTaskPhaseGated(context.Background())
	require.NoError(t, err)

	got, err := os.ReadFile(planPath) //nolint:gosec // test plan path from t.TempDir
	require.NoError(t, err)
	// the oracle ran first (its substitution is in the plan); without the restart guard the worker
	// would have run on the unresolved task and the oracle would never have applied this fix.
	assert.Contains(t, string(got), "build it", "persisted needs_revision must route to the oracle before the worker")
}

func TestRunTaskPhaseGated_RepeatedRejectEscalatesAndOracleDeclineAborts(t *testing.T) {
	r, planPath := newGatedRunner(t, "VERDICT: reject | FIX THE THING")
	// give the runner an oracle engine and a user that declines the proposed fix.
	r.codex = &mocks.ExecutorMock{RunFunc: func(_ context.Context, _ string) executor.Result {
		return executor.Result{Output: "OLD: implement\nNEW: build it"}
	}}
	r.inputCollector = &mocks.InputCollectorMock{
		AskQuestionFunc: func(_ context.Context, _ string, _ []string) (string, error) { return "No", nil },
	}

	err := r.runTaskPhaseGated(context.Background())
	require.ErrorIs(t, err, ErrUserAborted, "3 rejects escalate; declining the oracle aborts the run")

	got, err := os.ReadFile(planPath) //nolint:gosec // test plan path from t.TempDir
	require.NoError(t, err)
	assert.Contains(t, string(got), "FIX THE THING", "rejection should be yelled into the plan")
	assert.Contains(t, string(got), "- [ ] implement", "box must stay unchecked on reject")
}

func TestNewWithExecutors_WiresInspectorStateDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sub", "inspector-state-feature.db")
	cfg := Config{InspectorGateEnabled: true, InspectorStateDB: dbPath}
	r := NewWithExecutors(cfg, newMockLogger("progress.txt"), Executors{}, &status.PhaseHolder{})

	// the Config value must be wired onto the Runner field independently of openStateStore.
	assert.Equal(t, dbPath, r.inspectorStateDB, "InspectorStateDB must be wired through to the Runner")

	store, err := r.openStateStore()
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	// the store must open at the configured path (creating parent dirs), not the CWD default.
	assert.FileExists(t, dbPath, "openStateStore must honor the configured InspectorStateDB path")
}

func TestOpenStateStore_DefaultsToCWDWhenUnset(t *testing.T) {
	// gate enabled but no explicit InspectorStateDB: openStateStore falls back to the CWD-relative
	// default under .ralphex/. run in a temp CWD so the default lands there, not the real repo.
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := Config{InspectorGateEnabled: true}
	r := NewWithExecutors(cfg, newMockLogger("progress.txt"), Executors{}, &status.PhaseHolder{})
	require.Empty(t, r.inspectorStateDB, "no configured path")

	store, err := r.openStateStore()
	require.NoError(t, err)
	defer func() { _ = store.Close() }()
	assert.FileExists(t, filepath.Join(dir, ".ralphex", "inspector-state.db"), "falls back to the CWD default")
}

func TestRunOracle_AutoApproveAppliesWithoutInputCollector(t *testing.T) {
	r, planPath := newGatedRunner(t, "VERDICT: done") // verdict unused here
	r.codex = &mocks.ExecutorMock{RunFunc: func(_ context.Context, _ string) executor.Result {
		return executor.Result{Output: "OLD: implement\nNEW: build it unattended"}
	}}
	r.oracleAutoApprove = true
	r.inputCollector = nil // unattended: no terminal available

	store, err := r.openStateStore()
	require.NoError(t, err)
	defer func() { _ = store.Close() }()
	require.NoError(t, store.Save(state.TaskState{Position: 1, Status: state.StatusNeedsRevision, AttemptCount: 3}))

	resumed, err := r.runOracle(context.Background(), 1, store, "rejected repeatedly")
	require.NoError(t, err)
	assert.True(t, resumed, "auto-approve resumes the loop without a terminal")

	got, err := os.ReadFile(planPath) //nolint:gosec // test plan path from t.TempDir
	require.NoError(t, err)
	assert.Contains(t, string(got), "build it unattended", "auto-approved substitution applied to plan")

	st, err := store.Get(1)
	require.NoError(t, err)
	assert.Equal(t, state.StatusPending, st.Status, "task reset to pending after auto-approved oracle")
	assert.Equal(t, 0, st.AttemptCount, "attempts reset after auto-approved oracle, same as interactive approval")
}

func TestRunOracle_ApprovedAppliesFixAndResetsState(t *testing.T) {
	r, planPath := newGatedRunner(t, "VERDICT: done") // verdict unused here
	r.codex = &mocks.ExecutorMock{RunFunc: func(_ context.Context, _ string) executor.Result {
		return executor.Result{Output: "OLD: implement\nNEW: build the thing"}
	}}
	r.inputCollector = &mocks.InputCollectorMock{
		AskQuestionFunc: func(_ context.Context, _ string, _ []string) (string, error) { return "Yes", nil },
	}
	store, err := r.openStateStore()
	require.NoError(t, err)
	defer func() { _ = store.Close() }()
	require.NoError(t, store.Save(state.TaskState{Position: 1, Status: state.StatusNeedsRevision, AttemptCount: 3}))

	resumed, err := r.runOracle(context.Background(), 1, store, "rejected repeatedly")
	require.NoError(t, err)
	assert.True(t, resumed, "approved oracle fix resumes the loop")

	got, err := os.ReadFile(planPath) //nolint:gosec // test plan path from t.TempDir
	require.NoError(t, err)
	assert.Contains(t, string(got), "build the thing", "approved substitution applied to plan")

	st, err := store.Get(1)
	require.NoError(t, err)
	assert.Equal(t, state.StatusPending, st.Status, "task reset to pending after oracle")
	assert.Equal(t, 0, st.AttemptCount, "attempts reset after oracle")
}

func TestRunTaskPhaseGated_PreTickedBoxesDoNotBypassInspection(t *testing.T) {
	// a worker that ignores the "don't tick checkboxes" instruction and ticks them itself must not
	// be able to skip inspection: selection and the all-done decision come from the store, not the
	// plan's checkboxes. here the plan arrives fully pre-ticked but the store is empty.
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.md")
	preTicked := "# Plan\n\n### Task 1: Do it\n\n- [x] implement\n"
	require.NoError(t, os.WriteFile(planPath, []byte(preTicked), 0o600))

	var workerCalls int
	worker := &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, _ string) executor.Result {
			workerCalls++
			return executor.Result{Signal: status.PeasantTired, Output: "done one task"}
		},
	}
	gitMock := &mocks.GitCheckerMock{
		HeadHashFunc: func() (string, error) { return "deadbeef", nil },
		DiffFunc:     func(_, _ string) (string, error) { return "diff", nil },
	}
	appCfg, err := config.Load(t.TempDir())
	require.NoError(t, err)
	cfg := Config{Mode: ModeFull, MaxIterations: 6, PlanFile: planPath, InspectorGateEnabled: true, AppConfig: appCfg}
	r := NewWithExecutors(cfg, newMockLogger("progress.txt"), Executors{Claude: worker}, &status.PhaseHolder{})
	r.SetGitChecker(gitMock)
	r.reviewer = stubReviewer{out: "VERDICT: done"}
	r.inspectorStateDB = filepath.Join(dir, "state.db")

	err = r.runTaskPhaseGated(context.Background())
	require.NoError(t, err)
	assert.Positive(t, workerCalls, "pre-ticked checkboxes must not let the worker skip inspection")
}

func TestRunTaskPhaseGated_ResetsForgedCheckboxBeforeWorkerRuns(t *testing.T) {
	// the plan arrives with a forged tick (worker ignored the instruction). before the worker is
	// re-pointed at the task, the parent must reset that box so the worker sees the true pending
	// state and the store stays the only completion authority.
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.md")
	forged := "# Plan\n\n### Task 1: Do it\n\n- [x] implement\n"
	require.NoError(t, os.WriteFile(planPath, []byte(forged), 0o600))

	var sawChecked bool
	worker := &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, _ string) executor.Result {
			b, _ := os.ReadFile(planPath) //nolint:gosec // test plan path from t.TempDir
			if strings.Contains(string(b), "- [x] implement") {
				sawChecked = true
			}
			return executor.Result{Signal: status.PeasantTired, Output: "x"}
		},
	}
	gitMock := &mocks.GitCheckerMock{
		HeadHashFunc: func() (string, error) { return "deadbeef", nil },
		DiffFunc:     func(_, _ string) (string, error) { return "diff", nil },
	}
	appCfg, err := config.Load(t.TempDir())
	require.NoError(t, err)
	cfg := Config{Mode: ModeFull, MaxIterations: 6, PlanFile: planPath, InspectorGateEnabled: true, AppConfig: appCfg}
	r := NewWithExecutors(cfg, newMockLogger("progress.txt"), Executors{Claude: worker}, &status.PhaseHolder{})
	r.SetGitChecker(gitMock)
	r.reviewer = stubReviewer{out: "VERDICT: done"}
	r.inspectorStateDB = filepath.Join(dir, "state.db")

	err = r.runTaskPhaseGated(context.Background())
	require.NoError(t, err)
	assert.False(t, sawChecked, "forged checkbox must be reset before the worker runs")
}

func TestBuildInspectorPrompt_IncludesAcceptanceCriteriaAndScopeInstruction(t *testing.T) {
	appCfg, err := config.Load(t.TempDir())
	require.NoError(t, err)
	r := NewWithExecutors(Config{AppConfig: appCfg}, newMockLogger("progress.txt"), Executors{}, &status.PhaseHolder{})

	criteria := "- [ ] Create append_line.sh\n- [ ] Make it executable"
	got := r.buildInspectorPrompt("Create the append script", criteria, "diff --git a/x b/x\n+code")

	assert.Contains(t, got, "Create the append script", "prompt should name the task")
	assert.Contains(t, got, criteria, "prompt should give the inspector the task's acceptance criteria")
	assert.Contains(t, got, "+code", "prompt should include the diff")
	// the inspector must be told to judge scope, not just whether work happened.
	assert.Contains(t, strings.ToLower(got), "scope", "prompt should instruct the inspector to judge scope against the criteria")
}

func TestBuildInspectorPrompt_DefaultsWhenCriteriaEmpty(t *testing.T) {
	appCfg, err := config.Load(t.TempDir())
	require.NoError(t, err)
	r := NewWithExecutors(Config{AppConfig: appCfg}, newMockLogger("progress.txt"), Executors{}, &status.PhaseHolder{})

	got := r.buildInspectorPrompt("A task with no checklist", "", "diff")
	assert.Contains(t, got, "(no explicit acceptance criteria provided)", "empty criteria falls back to explicit default text")
	assert.NotContains(t, got, "{{ACCEPTANCE_CRITERIA}}", "placeholder must always be substituted")
}

func TestBuildGatedTaskPrompt_BindsCompletionSignalAndPlanFile(t *testing.T) {
	appCfg, err := config.Load(t.TempDir())
	require.NoError(t, err)
	r := NewWithExecutors(Config{AppConfig: appCfg, PlanFile: "docs/plans/x.md"}, newMockLogger("progress.txt"), Executors{}, &status.PhaseHolder{})

	got := r.buildGatedTaskPrompt()
	assert.Contains(t, got, status.PeasantTired, "completion signal must be bound from the authoritative constant")
	assert.NotContains(t, got, "{{COMPLETION_SIGNAL}}", "template placeholder must be substituted")
	assert.Contains(t, got, "docs/plans/x.md", "{{PLAN_FILE}} must be expanded")
}

func TestBuildOraclePrompt_InjectsTitleReasonAndPlan(t *testing.T) {
	appCfg, err := config.Load(t.TempDir())
	require.NoError(t, err)
	r := NewWithExecutors(Config{AppConfig: appCfg}, newMockLogger("progress.txt"), Executors{}, &status.PhaseHolder{})

	got := r.buildOraclePrompt("Wire the widget", "rejected three times", "### Task 1: Wire the widget\n")
	assert.Contains(t, got, "Wire the widget")
	assert.Contains(t, got, "rejected three times")
	assert.Contains(t, got, "### Task 1: Wire the widget")
	assert.NotContains(t, got, "{{PLAN_CONTENT}}")
}

func TestBuildOraclePrompt_DefaultsEmptyReason(t *testing.T) {
	appCfg, err := config.Load(t.TempDir())
	require.NoError(t, err)
	r := NewWithExecutors(Config{AppConfig: appCfg}, newMockLogger("progress.txt"), Executors{}, &status.PhaseHolder{})

	got := r.buildOraclePrompt("T", "", "plan")
	assert.Contains(t, got, "rejected repeatedly by the inspector", "empty reason falls back to default text")
}

func TestRunTaskPhaseGated_WorkerNeverTired_StopsAtMaxIterations(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.md")
	require.NoError(t, os.WriteFile(planPath, []byte(oneTaskPlan), 0o600))

	worker := &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, _ string) executor.Result {
			return executor.Result{Output: "still working"} // no PEASANT_IS_TIRED
		},
	}
	gitMock := &mocks.GitCheckerMock{
		HeadHashFunc: func() (string, error) { return "h", nil },
		DiffFunc:     func(_, _ string) (string, error) { return "", nil },
	}
	appCfg, err := config.Load(t.TempDir())
	require.NoError(t, err)
	cfg := Config{Mode: ModeFull, MaxIterations: 3, PlanFile: planPath, InspectorGateEnabled: true, AppConfig: appCfg}
	r := NewWithExecutors(cfg, newMockLogger("progress.txt"), Executors{Claude: worker}, &status.PhaseHolder{})
	r.SetGitChecker(gitMock)
	r.reviewer = stubReviewer{out: "VERDICT: done"}
	r.inspectorStateDB = filepath.Join(dir, "state.db")

	err = r.runTaskPhaseGated(context.Background())
	require.Error(t, err, "worker that never proposes completion should hit max iterations")
}
