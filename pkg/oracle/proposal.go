// Package oracle implements the human-in-the-loop escalation: when a task is rejected too many
// times or judged infeasible, a separate model proposes one literal substring substitution to the
// task spec, a human approves it, and the substitution is applied so the worker can try again.
package oracle

import (
	"errors"
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
// surrounding text is ignored. Returns an error if either line is missing or OLD is empty.
func parseProposal(output string) (oldStr, newStr string, err error) {
	oldMatch := oldLineRe.FindStringSubmatch(output)
	if oldMatch == nil {
		return "", "", errors.New("no OLD: line found in oracle proposal")
	}
	newMatch := newLineRe.FindStringSubmatch(output)
	if newMatch == nil {
		return "", "", errors.New("no NEW: line found in oracle proposal")
	}
	oldStr = strings.TrimSpace(oldMatch[1])
	if oldStr == "" {
		return "", "", errors.New("oracle proposal has an empty OLD string")
	}
	return oldStr, strings.TrimSpace(newMatch[1]), nil
}
