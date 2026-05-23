package agentbus

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRoleForTool(t *testing.T) {
	assert.Equal(t, RoleWorker, RoleForTool(ToolWorker))
	assert.Equal(t, RoleOracle, RoleForTool(ToolOracle))
	assert.Equal(t, RoleInspector, RoleForTool(ToolInspector))
	assert.Equal(t, RoleOrchestrator, RoleForTool(ToolOrchestrator))
	assert.Equal(t, Role(""), RoleForTool("unknown"))
}

func TestCheckTransition(t *testing.T) {
	tests := []struct {
		name    string
		from    Role
		to      Role
		allowed bool
	}{
		{"worker to inspector", RoleWorker, RoleInspector, true},
		{"worker to oracle", RoleWorker, RoleOracle, true},
		{"worker to orchestrator", RoleWorker, RoleOrchestrator, false},
		{"oracle to worker", RoleOracle, RoleWorker, true},
		{"oracle to inspector", RoleOracle, RoleInspector, true},
		{"oracle to orchestrator", RoleOracle, RoleOrchestrator, true},
		{"inspector to orchestrator", RoleInspector, RoleOrchestrator, true},
		{"inspector to oracle", RoleInspector, RoleOracle, true},
		{"inspector to worker", RoleInspector, RoleWorker, false},
		{"orchestrator to worker", RoleOrchestrator, RoleWorker, true},
		{"orchestrator to inspector", RoleOrchestrator, RoleInspector, false},
		{"invalid source", Role("bogus"), RoleWorker, false},
		{"invalid target", RoleWorker, Role("bogus"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.allowed, AllowedTransition(tt.from, tt.to))
			err := CheckTransition(tt.from, tt.to)
			if tt.allowed {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}
