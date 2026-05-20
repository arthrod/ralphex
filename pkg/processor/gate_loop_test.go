package processor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/executor"
	"github.com/umputun/ralphex/pkg/processor/mocks"
	"github.com/umputun/ralphex/pkg/status"
)

// stubReviewer returns a fixed verdict string regardless of prompt.
type stubReviewer struct{ out string }

func (s stubReviewer) Review(_ context.Context, _ string) (string, error) { return s.out, nil }

// newGatedRunner builds a Runner wired for the inspector gate against a temp plan file and db,
// with a worker that always proposes completion and an injectable inspector verdict.
func newGatedRunner(t *testing.T, planBody, verdict string) (*Runner, string) {
	t.Helper()
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.md")
	require.NoError(t, os.WriteFile(planPath, []byte(planBody), 0o600))

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

func TestRunTaskPhaseGated_DoneAcceptsAndCompletes(t *testing.T) {
	r, planPath := newGatedRunner(t, oneTaskPlan, "VERDICT: done")

	err := r.runTaskPhaseGated(context.Background())
	require.NoError(t, err)

	got, err := os.ReadFile(planPath)
	require.NoError(t, err)
	assert.Contains(t, string(got), "- [x] implement", "parent should check the box on done verdict")
}

func TestRunTaskPhaseGated_RepeatedRejectEscalatesToOracle(t *testing.T) {
	r, planPath := newGatedRunner(t, oneTaskPlan, "VERDICT: reject | FIX THE THING")

	err := r.runTaskPhaseGated(context.Background())
	require.ErrorIs(t, err, errOracleNeeded, "3 rejects should escalate to the oracle")

	got, err := os.ReadFile(planPath)
	require.NoError(t, err)
	assert.Contains(t, string(got), "FIX THE THING", "rejection should be yelled into the plan")
	assert.Contains(t, string(got), "- [ ] implement", "box must stay unchecked on reject")
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
