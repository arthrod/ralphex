package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplySubstitution_ReplacesFirstOccurrence(t *testing.T) {
	content := "use the FooBar API\nthen use the FooBar API again\n"
	got, err := ApplySubstitution(content, "FooBar", "FooBaz")
	require.NoError(t, err)
	assert.Equal(t, "use the FooBaz API\nthen use the FooBar API again\n", got,
		"only the first occurrence is substituted")
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
