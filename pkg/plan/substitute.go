package plan

import (
	"errors"
	"fmt"
	"strings"
)

// ApplySubstitution replaces the single occurrence of old with replacement in the plan content,
// returning the updated content. It is used by the oracle to apply a human-approved fix to a task
// spec. old must occur exactly once: zero occurrences means the fix can't be applied, and multiple
// occurrences are ambiguous — replacing the first could silently edit a different task than the one
// that was escalated. Both cases return an error, so a wrong-target or no-op edit is never applied.
func ApplySubstitution(content, old, replacement string) (string, error) {
	if old == "" {
		return "", errors.New("substitution OLD string is empty")
	}
	switch n := strings.Count(content, old); n {
	case 0:
		return "", fmt.Errorf("substitution OLD string not found in plan: %q", old)
	case 1:
		return strings.Replace(content, old, replacement, 1), nil
	default:
		return "", fmt.Errorf("substitution OLD string is ambiguous (found %d times); it must uniquely identify the text to change: %q", n, old)
	}
}
