package agentbus

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"os"
)

// tokenEnv is the environment variable each role's tmux session carries; it holds the
// token minted for that role's tool only.
const tokenEnv = "TOOL_API_KEY" //nolint:gosec // G101: env var name, not a hardcoded credential

// GenerateRegistry mints a random token per tool and writes the registry to tokens.json
// (0o600). It overwrites any existing registry, so it is called once at supervisor start.
func GenerateRegistry() (map[string]string, error) {
	if err := EnsureDir(); err != nil {
		return nil, err
	}
	reg := make(map[string]string, len(AllTools))
	for _, tool := range AllTools {
		tok, err := randomToken()
		if err != nil {
			return nil, err
		}
		reg[tool] = tok
	}
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal token registry: %w", err)
	}
	if err := writeFileAtomic(tokensPath(), data, 0o600); err != nil {
		return nil, err
	}
	return reg, nil
}

// RegistryExists reports whether a token registry has already been generated.
func RegistryExists() bool {
	_, err := os.Stat(tokensPath())
	return err == nil
}

// LoadRegistry reads the token registry from disk.
func LoadRegistry() (map[string]string, error) {
	data, err := os.ReadFile(tokensPath())
	if err != nil {
		return nil, fmt.Errorf("read token registry: %w", err)
	}
	var reg map[string]string
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("parse token registry: %w", err)
	}
	return reg, nil
}

// TokenFor returns the registry token for a tool, or an error if absent.
func TokenFor(tool string) (string, error) {
	reg, err := LoadRegistry()
	if err != nil {
		return "", err
	}
	tok, ok := reg[tool]
	if !ok || tok == "" {
		return "", fmt.Errorf("no token registered for %q", tool)
	}
	return tok, nil
}

// Authenticate verifies that TOOL_API_KEY matches the registry token minted for the
// given tool. Because each binary passes its own hardcoded tool name, a token minted
// for one tool never authenticates another — a worker's key cannot drive oracle-task.
// The comparison is constant-time.
func Authenticate(tool string) error {
	provided := os.Getenv(tokenEnv)
	if provided == "" {
		return fmt.Errorf("%s not set; %s must run with its own credential", tokenEnv, tool)
	}
	expected, err := TokenFor(tool)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
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
