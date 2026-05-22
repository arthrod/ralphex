// Package oracle implements the human-in-the-loop escalation: when a task is rejected too many
// times or judged infeasible, a separate model proposes one literal substring substitution to the
// task spec, a human approves it, and the substitution is applied so the worker can try again.
package oracle

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	// [ \t]* (not \s*) for inter-token spacing so an empty "OLD:" cannot swallow the next line.
	oldLineRe = regexp.MustCompile(`(?im)^[ \t]*OLD:[ \t]*(.*?)[ \t]*$`)
	newLineRe = regexp.MustCompile(`(?im)^[ \t]*NEW:[ \t]*(.*?)[ \t]*$`)
)

// parseProposal extracts a single OLD/NEW substring substitution proposed by the oracle model.
// OLD must be non-empty (there must be something to match); NEW may be empty (a deletion). Other
// surrounding text is ignored. Because the proposal drives a literal plan edit, ambiguity is
// rejected rather than guessed: more than one OLD: or NEW: line is an error, as is a missing line
// or an empty OLD string — so the oracle never silently applies an unintended substitution.
func parseProposal(output string) (oldStr, newStr string, err error) {
	oldMatches := oldLineRe.FindAllStringSubmatch(output, -1)
	if len(oldMatches) == 0 {
		return "", "", errors.New("no OLD: line found in oracle proposal")
	}
	if len(oldMatches) > 1 {
		return "", "", fmt.Errorf("oracle proposal has %d OLD: lines; it must propose exactly one substitution", len(oldMatches))
	}
	newMatches := newLineRe.FindAllStringSubmatch(output, -1)
	if len(newMatches) == 0 {
		return "", "", errors.New("no NEW: line found in oracle proposal")
	}
	if len(newMatches) > 1 {
		return "", "", fmt.Errorf("oracle proposal has %d NEW: lines; it must propose exactly one substitution", len(newMatches))
	}
	oldStr = strings.TrimSpace(oldMatches[0][1])
	if oldStr == "" {
		return "", "", errors.New("oracle proposal has an empty OLD string")
	}
	return oldStr, strings.TrimSpace(newMatches[0][1]), nil
}
