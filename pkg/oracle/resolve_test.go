package oracle

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeProposer struct {
	out string
	err error
}

func (f fakeProposer) Propose(_ context.Context, _ string) (string, error) { return f.out, f.err }

type fakeApprover struct {
	approve bool
	gotOld  string
	gotNew  string
}

func (f *fakeApprover) Approve(oldStr, newStr string) (bool, error) {
	f.gotOld, f.gotNew = oldStr, newStr
	return f.approve, nil
}

const resolvePlan = "### Task 1: Do it\n\n- [ ] use the FooBar API\n"

func TestResolve_ApprovedAppliesSubstitution(t *testing.T) {
	prop := fakeProposer{out: "OLD: FooBar\nNEW: FooBaz"}
	appr := &fakeApprover{approve: true}

	out, err := Resolve(context.Background(), prop, appr, resolvePlan, "Do it", "infeasible")
	require.NoError(t, err)

	assert.True(t, out.Applied)
	assert.Contains(t, out.Plan, "FooBaz")
	assert.NotContains(t, out.Plan, "FooBar")
	assert.Equal(t, "FooBar", appr.gotOld, "approver is shown the proposed substitution")
	assert.Equal(t, "FooBaz", appr.gotNew)
}

func TestResolve_RejectedLeavesPlanUnchanged(t *testing.T) {
	prop := fakeProposer{out: "OLD: FooBar\nNEW: FooBaz"}
	appr := &fakeApprover{approve: false}

	out, err := Resolve(context.Background(), prop, appr, resolvePlan, "Do it", "infeasible")
	require.NoError(t, err)

	assert.False(t, out.Applied)
	assert.Equal(t, resolvePlan, out.Plan, "rejected proposal must not mutate the plan")
}

func TestResolve_MalformedProposalErrors(t *testing.T) {
	prop := fakeProposer{out: "I have no idea"}
	appr := &fakeApprover{approve: true}

	_, err := Resolve(context.Background(), prop, appr, resolvePlan, "Do it", "infeasible")
	require.Error(t, err)
}

func TestResolve_ProposerErrorPropagates(t *testing.T) {
	prop := fakeProposer{err: errors.New("model down")}
	appr := &fakeApprover{approve: true}

	_, err := Resolve(context.Background(), prop, appr, resolvePlan, "Do it", "infeasible")
	require.Error(t, err)
}

func TestResolve_SubstitutionNotInPlanErrors(t *testing.T) {
	prop := fakeProposer{out: "OLD: not present anywhere\nNEW: x"}
	appr := &fakeApprover{approve: true}

	_, err := Resolve(context.Background(), prop, appr, resolvePlan, "Do it", "infeasible")
	require.Error(t, err)
}
