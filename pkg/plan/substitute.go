package plan

import (
	"errors"
	"fmt"
	"strings"
)

// ApplySubstitution replaces the first occurrence of old with replacement in the plan content,
// returning the updated content. It is used by the oracle to apply a human-approved fix to a task
// spec. Returns an error if old is empty or is not present, so a no-op substitution is never
// silently accepted as a fix.
func ApplySubstitution(content, old, replacement string) (string, error) {
	if old == "" {
		return "", errors.New("substitution OLD string is empty")
	}
	if !strings.Contains(content, old) {
		return "", fmt.Errorf("substitution OLD string not found in plan: %q", old)
	}
	return strings.Replace(content, old, replacement, 1), nil
}
