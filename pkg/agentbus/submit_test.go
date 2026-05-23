package agentbus

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubmitHandoff(t *testing.T) {
	reg := startTestAuthServer(t).Registry()

	t.Run("authenticated legal handoff is recorded", func(t *testing.T) {
		t.Setenv(tokenEnv, reg[ToolWorker])
		h, err := SubmitHandoff(ToolWorker, RoleInspector, StatusUnset, "task t1", "done")
		require.NoError(t, err)
		assert.Equal(t, RoleWorker, h.From)
		assert.Equal(t, RoleInspector, h.To)
		assert.GreaterOrEqual(t, h.Seq, 1)

		all, err := ReadHandoffsSince(0)
		require.NoError(t, err)
		require.NotEmpty(t, all)
		assert.Equal(t, "done", all[len(all)-1].Message)
	})

	t.Run("bad credential is rejected", func(t *testing.T) {
		t.Setenv(tokenEnv, reg[ToolOracle]) // wrong tool's token
		_, err := SubmitHandoff(ToolWorker, RoleInspector, StatusUnset, "", "x")
		assert.Error(t, err)
	})

	t.Run("illegal transition is rejected", func(t *testing.T) {
		t.Setenv(tokenEnv, reg[ToolWorker])
		_, err := SubmitHandoff(ToolWorker, RoleOrchestrator, StatusUnset, "", "x")
		assert.Error(t, err)
	})
}
