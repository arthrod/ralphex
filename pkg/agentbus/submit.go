package agentbus

import (
	"errors"
	"fmt"
	"os"
)

// SubmitHandoff sends an authenticated handoff request to the supervisor's auth socket.
// The supervisor validates the calling tool's token, stamps the source role, checks the
// transition, and appends an HMAC-signed envelope to the durable log. It is the single
// entry point the worker/oracle/inspector binaries use, so every handoff is authenticated
// and legal before it is recorded — and signed by the sole writer.
func SubmitHandoff(tool string, to Role, status TaskStatus, confirmCurrent, message string) (Handoff, error) {
	token := os.Getenv(tokenEnv)
	if token == "" {
		return Handoff{}, fmt.Errorf("%s not set; %s must run with its own credential", tokenEnv, tool)
	}
	resp, err := dialAndSend(socketRequest{
		Action:         actionHandoff,
		Tool:           tool,
		Token:          token,
		To:             to,
		Status:         status,
		ConfirmCurrent: confirmCurrent,
		Message:        message,
	})
	if err != nil {
		return Handoff{}, fmt.Errorf("auth service unavailable: %w", err)
	}
	if !resp.OK {
		if resp.Error != "" {
			return Handoff{}, errors.New(resp.Error)
		}
		return Handoff{}, errors.New("handoff rejected")
	}
	if resp.Handoff == nil {
		return Handoff{}, errors.New("handoff accepted but no record returned")
	}
	return *resp.Handoff, nil
}
