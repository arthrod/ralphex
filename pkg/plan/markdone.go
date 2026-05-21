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
		if m := taskHeaderPattern.FindStringSubmatch(line); len(m) != 0 && parseTaskNum(m[1]) == taskNumber {
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

// UncheckTask resets every actionable checked checkbox within the given task's section back to
// unchecked, returning the updated plan markdown. The inspector gate calls this before re-pointing
// the worker at a task whose store status is not done, so a worker that ticked its own boxes
// (forging completion) cannot misdirect the next attempt — the parent's store remains the only
// source of completion truth. Non-actionable example checkboxes and boxes outside the target task
// are left untouched. Returns an error if taskNumber is not found.
func UncheckTask(content string, taskNumber int) (string, error) {
	lines := strings.Split(content, "\n")

	hdr := -1
	var ft fenceTracker
	for i, line := range lines {
		if ft.skip(line) {
			continue
		}
		if m := taskHeaderPattern.FindStringSubmatch(line); len(m) != 0 && parseTaskNum(m[1]) == taskNumber {
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
		if len(m) == 0 || m[1] == " " {
			continue // not a checkbox, or already unchecked
		}
		if !(Checkbox{Text: strings.TrimSpace(m[2])}).IsActionable() {
			continue // example checkbox, not part of completion
		}
		lines[i] = swapCheckMarker(line)
	}

	return strings.Join(lines, "\n"), nil
}

// swapCheckMarker replaces the first "[x]" or "[X]" checkbox marker on a line with "[ ]",
// preserving surrounding indentation and text.
func swapCheckMarker(line string) string {
	if before, after, found := strings.Cut(line, "[x]"); found {
		return before + "[ ]" + after
	}
	if before, after, found := strings.Cut(line, "[X]"); found {
		return before + "[ ]" + after
	}
	return line
}
