package agentbus

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// TaskStatus is an optional assessment a role attaches to a handoff (e.g. the oracle
// telling the inspector whether it believes the task is complete).
type TaskStatus string

// Task status values. Empty means "not asserted".
const (
	StatusUnset      TaskStatus = ""
	StatusIncomplete TaskStatus = "incomplete"
	StatusDone       TaskStatus = "done"
)

// Handoff is one append-only record in handoffs.jsonl. It is the durable source of
// truth for a role transition; killing/resuming opencode is best-effort on top of it.
type Handoff struct {
	Seq            int        `json:"seq"`
	From           Role       `json:"from"`
	To             Role       `json:"to"`
	Status         TaskStatus `json:"status,omitempty"`
	ConfirmCurrent string     `json:"confirm_current,omitempty"`
	Message        string     `json:"message"`
	Time           time.Time  `json:"time"`
	Mac            string     `json:"mac,omitempty"`
}

// macForHandoff computes the hex-encoded HMAC-SHA256 of the handoff over all fields
// except the mac itself. The mac field is zeroed before marshaling so signing and
// verification operate on identical bytes; struct field order is stable so the
// marshaled form is deterministic.
func macForHandoff(h Handoff, key []byte) string {
	h.Mac = ""
	data, err := json.Marshal(h)
	if err != nil {
		// json.Marshal of a Handoff (only basic types) cannot fail; guard defensively.
		return ""
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyHandoffMac reports whether h carries a valid HMAC for key. A handoff with a
// missing or tampered mac fails. The comparison is constant-time.
func VerifyHandoffMac(h Handoff, key []byte) bool {
	want := macForHandoff(h, key)
	return hmac.Equal([]byte(want), []byte(h.Mac))
}

// AppendHandoff assigns the next sequence number and appends the envelope to
// handoffs.jsonl under an exclusive lock. It returns the stored record with Seq/Time
// set. It writes an UNSIGNED line; the supervisor rejects unsigned lines, so this is
// only useful in tests that exercise forged-line handling. Production code routes
// through AppendSignedHandoff.
func AppendHandoff(h Handoff) (Handoff, error) {
	return appendHandoffLocked(h, nil)
}

// AppendSignedHandoff assigns Seq/Time, computes the HMAC over the stamped handoff with
// key, and appends the signed line under an exclusive lock. The stored line carries the
// same Seq/Time/Mac that were signed, so a reader can verify the mac and reject any line
// written directly to the file without the supervisor's in-memory key.
func AppendSignedHandoff(h Handoff, key []byte) (Handoff, error) {
	return appendHandoffLocked(h, key)
}

// appendHandoffLocked is the shared locked-append routine: it opens the log, takes an
// exclusive lock, assigns the next Seq and a Time, and (when key is non-nil) signs the
// stamped handoff before writing the JSON line. Seq and Time are assigned BEFORE the mac
// is computed so the stored line and the signed bytes match exactly.
func appendHandoffLocked(h Handoff, key []byte) (Handoff, error) {
	if err := EnsureDir(); err != nil {
		return h, err
	}
	path := handoffsPath()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // G304: path built from trusted internal config, not user input
	if err != nil {
		return h, fmt.Errorf("open handoffs log: %w", err)
	}
	defer f.Close()

	err = lockFile(f)
	if err != nil {
		return h, err
	}
	defer func() { _ = unlockFile(f) }()

	maxSeq, err := scanMaxSeq(f)
	if err != nil {
		return h, err
	}
	h.Seq = maxSeq + 1
	if h.Time.IsZero() {
		h.Time = time.Now().UTC()
	}
	if key != nil {
		h.Mac = macForHandoff(h, key)
	}

	line, err := json.Marshal(h)
	if err != nil {
		return h, fmt.Errorf("marshal handoff: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return h, fmt.Errorf("append handoff: %w", err)
	}
	return h, nil
}

// ReadHandoffsSince returns all handoffs with Seq strictly greater than afterSeq, in
// file order. A missing log yields an empty slice. Used by the supervisor to process
// new transitions and to replay after a failed kill/resume.
func ReadHandoffsSince(afterSeq int) ([]Handoff, error) {
	f, err := os.Open(handoffsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open handoffs log: %w", err)
	}
	defer f.Close()

	var out []Handoff
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var h Handoff
		if err := json.Unmarshal(line, &h); err != nil {
			return nil, fmt.Errorf("parse handoff line: %w", err)
		}
		if h.Seq > afterSeq {
			out = append(out, h)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan handoffs log: %w", err)
	}
	return out, nil
}

// scanMaxSeq reads the open file from the start and returns the highest Seq seen.
func scanMaxSeq(f *os.File) (int, error) {
	if _, err := f.Seek(0, 0); err != nil {
		return 0, fmt.Errorf("seek handoffs log: %w", err)
	}
	maxSeq := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var h Handoff
		if err := json.Unmarshal(line, &h); err != nil {
			return 0, fmt.Errorf("parse handoff line: %w", err)
		}
		if h.Seq > maxSeq {
			maxSeq = h.Seq
		}
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("scan handoffs log: %w", err)
	}
	return maxSeq, nil
}
