package agentbus

import (
	"bufio"
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
}

// AppendHandoff assigns the next sequence number and appends the envelope to
// handoffs.jsonl under an exclusive lock (read-max-seq then write, atomically with
// respect to other role processes). It returns the stored record with Seq/Time set.
func AppendHandoff(h Handoff) (Handoff, error) {
	if err := EnsureDir(); err != nil {
		return h, err
	}
	path := handoffsPath()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return h, fmt.Errorf("open handoffs log: %w", err)
	}
	defer f.Close()

	if err := lockFile(f); err != nil {
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
