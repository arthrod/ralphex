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
		// use the regex's matched positions to flip exactly the checkbox mark, rather than a blind
		// strings.Replace of "[ ]" that could in principle touch other text on the line.
		loc := checkboxPattern.FindStringSubmatchIndex(line)
		if loc == nil {
			continue // not a checkbox
		}
		mark := line[loc[2]:loc[3]]
		text := line[loc[4]:loc[5]]
		if mark != " " {
			continue // already checked
		}
		if !(Checkbox{Text: strings.TrimSpace(text)}).IsActionable() {
			continue // example checkbox, not part of completion
		}
		lines[i] = line[:loc[2]] + "x" + line[loc[3]:]
	}

	return strings.Join(lines, "\n"), nil
}
