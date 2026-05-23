package agentbus

import (
	"crypto/rand"
	"fmt"
	"os"
)

// tokenEnv is the environment variable each role's tmux session carries; it holds the
// token minted for that role's tool only.
const tokenEnv = "TOOL_API_KEY" //nolint:gosec // G101: env var name, not a hardcoded credential

// Authenticate verifies that TOOL_API_KEY is valid for the given tool by asking the
// supervisor's auth socket. Because each binary passes its own hardcoded tool name, a
// token minted for one tool never authenticates another — a worker's key cannot drive
// oracle-task. When the socket is unreachable there is no supervisor to validate
// against, so authentication is denied.
func Authenticate(tool string) error {
	provided := os.Getenv(tokenEnv)
	if provided == "" {
		return fmt.Errorf("%s not set; %s must run with its own credential", tokenEnv, tool)
	}
	resp, err := dialAndSend(socketRequest{Action: actionAuth, Tool: tool, Token: provided})
	if err != nil {
		return fmt.Errorf("auth service unavailable: %w", err)
	}
	if !resp.OK {
		if resp.Error != "" {
			return fmt.Errorf("auth failed: %s", resp.Error)
		}
		return fmt.Errorf("auth failed: %s is not valid for %s", tokenEnv, tool)
	}
	return nil
}

// randomToken mints a per-tool credential as an RFC 4122 version-4 UUID. The UUID is generated
// from crypto/rand (not a dependency) so each tool's key is a genuine random UUID; isolation
// comes from the registry mapping, not from any structure in the value.
func randomToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
