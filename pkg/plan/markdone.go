package plan

import (
	"fmt"
	"strings"
)

// MarkTaskDone checks every actionable unchecked checkbox within the given task's section,
// returning the updated plan markdown. The inspector gate calls this on a "done" verdict so that
// completion is recorded by the parent process, not self-asserted by the worker. Non-actionable
// example checkboxes (those whose text contains a literal "[ ]") and boxes outside the target
// task are left untouched. Returns an error if taskNumber is not found.
func MarkTaskDone(content string, taskNumber int) (string, error) {
	lines := strings.Split(content, "\n")

	hdr := -1
	var ft fenceTracker
	for i, line := range lines {
		if ft.skip(line) {
			continue
		}
		if m := taskHeaderPattern.FindStringSubmatch(line); m != nil && parseTaskNum(m[1]) == taskNumber {
			hdr = i
			break
		}
	}
	if hdr == -1 {
		return "", fmt.Errorf("task %d not found in plan", taskNumber)
	}

	var sectFence fenceTracker
	for i := hdr + 1; i < len(lines); i++ {
		line := lines[i]
		if sectFence.skip(line) {
			continue
		}
		if sectionCloses(line) {
			break
		}
		m := checkboxPattern.FindStringSubmatch(line)
		if m == nil || m[1] != " " {
			continue // not a checkbox, or already checked
		}
		if !(Checkbox{Text: strings.TrimSpace(m[2])}).IsActionable() {
			continue // example checkbox, not part of completion
		}
		lines[i] = strings.Replace(line, "[ ]", "[x]", 1)
	}

	return strings.Join(lines, "\n"), nil
}
