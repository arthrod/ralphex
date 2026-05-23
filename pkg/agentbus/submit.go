package agentbus

import "fmt"

// SubmitHandoff authenticates the calling tool, stamps the source role, validates the
// transition, and appends the envelope to the durable log. It is the single entry point
// the worker/oracle/inspector binaries use, so every handoff is authenticated and legal
// before it is recorded.
func SubmitHandoff(tool string, to Role, status TaskStatus, confirmCurrent, message string) (Handoff, error) {
	if err := Authenticate(tool); err != nil {
		return Handoff{}, err
	}
	from := RoleForTool(tool)
	if err := CheckTransition(from, to); err != nil {
		return Handoff{}, err
	}
	h := Handoff{
		From:           from,
		To:             to,
		Status:         status,
		ConfirmCurrent: confirmCurrent,
		Message:        message,
	}
	stored, err := AppendHandoff(h)
	if err != nil {
		return Handoff{}, fmt.Errorf("record handoff: %w", err)
	}
	return stored, nil
}
