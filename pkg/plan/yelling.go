package plan

import (
	"fmt"
	"strings"
)

// AppendYelling inserts an inspector's rejection note at the end of the given task's section in
// the plan markdown. The note is plain text (no heading/checkbox syntax) so it never disturbs
// parsing or orphans checkboxes, and it accumulates across attempts — every rejection the worker
// has earned stays visible when the next fresh worker session re-reads the plan.
//
// Boundaries follow ParsePlan's rules: a task section ends at the next task header, the next h2,
// the next h1, or end of file, and code-fenced regions are skipped so a fenced "### Task" inside
// a section is not mistaken for the boundary. Returns an error if taskNumber is not found.
func AppendYelling(content string, taskNumber, attempt int, payload string) (string, error) {
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

	// find the section end: the first line after the header that closes the task.
	end := len(lines)
	var sectFence fenceTracker
	for i := hdr + 1; i < len(lines); i++ {
		line := lines[i]
		if sectFence.skip(line) {
			continue
		}
		if sectionCloses(line) {
			end = i
			break
		}
	}

	// insert after the last non-blank line within the section so the note hugs the content.
	insertAt := end
	for insertAt > hdr+1 && strings.TrimSpace(lines[insertAt-1]) == "" {
		insertAt--
	}

	note := []string{
		"",
		fmt.Sprintf("⚠️ INSPECTOR REJECTION (attempt %d):", attempt),
		payload,
	}

	out := make([]string, 0, len(lines)+len(note))
	out = append(out, lines[:insertAt]...)
	out = append(out, note...)
	out = append(out, lines[insertAt:]...)
	return strings.Join(out, "\n"), nil
}

// sectionCloses reports whether line ends the current task section, mirroring ParsePlan: a new
// task header (h3 "### Task"/"### Iteration"), any h2, or any h1 closes it; other h3+ subsections
// do not.
func sectionCloses(line string) bool {
	if taskHeaderPattern.MatchString(line) {
		return true
	}
	isH2 := strings.HasPrefix(line, "##") && !strings.HasPrefix(line, "###")
	isH1 := strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "##")
	return isH2 || isH1
}
