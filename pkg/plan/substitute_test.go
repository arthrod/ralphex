package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplySubstitution_ReplacesUniqueOccurrence(t *testing.T) {
	content := "use the FooBar API in the widget task\n"
	got, err := ApplySubstitution(content, "FooBar", "FooBaz")
	require.NoError(t, err)
	assert.Equal(t, "use the FooBaz API in the widget task\n", got)
}

func TestApplySubstitution_AmbiguousMatchErrors(t *testing.T) {
	// a non-unique OLD string is rejected: replacing the first of several could edit the wrong task.
	content := "use the FooBar API\nthen use the FooBar API again\n"
	_, err := ApplySubstitution(content, "FooBar", "FooBaz")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")
}

func TestApplySubstitution_NotFoundErrors(t *testing.T) {
	_, err := ApplySubstitution("nothing matches here", "ABSENT", "x")
	require.Error(t, err)
}

func TestApplySubstitution_EmptyNewDeletes(t *testing.T) {
	got, err := ApplySubstitution("remove THIS please", "THIS ", "")
	require.NoError(t, err)
	assert.Equal(t, "remove please", got)
}

func TestApplySubstitution_EmptyOldErrors(t *testing.T) {
	_, err := ApplySubstitution("anything", "", "x")
	require.Error(t, err)
}
