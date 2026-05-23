package agentbus

import "fmt"

// Role identifies one of the four agents.
type Role string

// The four roles.
const (
	RoleWorker       Role = "worker"
	RoleOracle       Role = "oracle"
	RoleInspector    Role = "inspector"
	RoleOrchestrator Role = "orchestrator"
)

// RoleForTool maps a tool name to the role that invokes it.
func RoleForTool(tool string) Role {
	switch tool {
	case ToolWorker:
		return RoleWorker
	case ToolOracle:
		return RoleOracle
	case ToolInspector:
		return RoleInspector
	case ToolOrchestrator:
		return RoleOrchestrator
	default:
		return ""
	}
}

// Valid reports whether r is a known role.
func (r Role) Valid() bool {
	switch r {
	case RoleWorker, RoleOracle, RoleInspector, RoleOrchestrator:
		return true
	default:
		return false
	}
}

// allowedTransitions enumerates which handoffs are legal. The supervisor rejects any
// transition not listed here so a malformed or hostile envelope cannot reroute the run.
var allowedTransitions = map[Role]map[Role]bool{
	RoleWorker:       {RoleInspector: true, RoleOracle: true},
	RoleOracle:       {RoleWorker: true, RoleInspector: true, RoleOrchestrator: true},
	RoleInspector:    {RoleOrchestrator: true, RoleOracle: true},
	RoleOrchestrator: {RoleWorker: true},
}

// AllowedTransition reports whether a handoff from -> to is permitted.
func AllowedTransition(from, to Role) bool {
	return allowedTransitions[from][to]
}

// CheckTransition returns an error describing why a transition is not allowed, or nil.
func CheckTransition(from, to Role) error {
	if !from.Valid() {
		return fmt.Errorf("invalid source role %q", from)
	}
	if !to.Valid() {
		return fmt.Errorf("invalid target role %q", to)
	}
	if !AllowedTransition(from, to) {
		return fmt.Errorf("handoff %s -> %s is not allowed", from, to)
	}
	return nil
}
