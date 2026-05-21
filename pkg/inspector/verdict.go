// Package inspector implements the credential-gated completion check: a worker proposes
// completion, and a separately-credentialed inspector renders a structured verdict on the
// worker's diff. The worker cannot self-approve because verdict parsing happens in the
// parent process, which holds the inspector credential the worker's env never sees.
package inspector

import (
	"errors"
	"regexp"
	"strings"
)

// VerdictKind is the inspector's decision on a worker's proposed completion.
type VerdictKind string

const (
	VerdictDone   VerdictKind = "done"   // task complete and correct; accept
	VerdictReject VerdictKind = "reject" // incomplete or wrong; worker must retry (payload = yelling)
	VerdictUpdate VerdictKind = "update" // task itself infeasible or malformed; escalate to oracle
)

// Verdict is a parsed inspector decision. Payload carries the all-caps complaint for reject
// or the explanation for update; it is empty for done.
type Verdict struct {
	Kind    VerdictKind
	Payload string
}

// verdictPattern matches a single "VERDICT: <kind> [| <payload>]" line. Case-insensitive
// (i) and multiline (m) so the line can appear anywhere in the inspector's output.
var verdictPattern = regexp.MustCompile(`(?im)^\s*VERDICT:\s+(done|reject|update)\s*(?:\|\s*(.*?))?\s*$`)

// parseVerdict extracts the inspector's verdict from its raw output. If the model restates the
// verdict, the last occurrence wins. Returns an error when no well-formed VERDICT line is found,
// so the caller can retry the inspector or fall back to a conservative reject.
func parseVerdict(output string) (Verdict, error) {
	matches := verdictPattern.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return Verdict{}, errors.New("no well-formed VERDICT line found in inspector output")
	}
	m := matches[len(matches)-1]
	kind := VerdictKind(strings.ToLower(m[1]))
	payload := strings.TrimSpace(m[2])
	// a "done" verdict must not carry a payload: "VERDICT: done | ..." is contradictory output that
	// could otherwise wave incomplete work through. treat it as not-well-formed so the caller retries
	// the inspector and ultimately falls back to a conservative reject rather than accepting.
	if kind == VerdictDone && payload != "" {
		return Verdict{}, fmt.Errorf("malformed verdict: 'done' must not carry a payload (got %q)", payload)
	}
	return Verdict{Kind: kind, Payload: payload}, nil
}
