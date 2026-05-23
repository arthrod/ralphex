package agentbus

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Task is one unit of work the orchestrator assigns to the worker. A task must carry a
// single-line summary, at least one minimum test (the bar for "done"), and descriptive
// instructions.
type Task struct {
	ID           string   `yaml:"id"`
	Summary      string   `yaml:"summary"`
	MinTests     []string `yaml:"min_tests"`
	Instructions string   `yaml:"instructions"`
}

// PRD is the task store, persisted as prd.yaml.
type PRD struct {
	Tasks []Task `yaml:"tasks"`
}

// Validate checks a single task: single-line non-empty summary, at least one non-empty
// minimum test, and non-empty instructions.
func (t Task) Validate() error {
	if strings.TrimSpace(t.ID) == "" {
		return errors.New("task id is required")
	}
	s := strings.TrimSpace(t.Summary)
	if s == "" {
		return fmt.Errorf("task %q: summary is required", t.ID)
	}
	if strings.ContainsAny(s, "\r\n") {
		return fmt.Errorf("task %q: summary must be a single line", t.ID)
	}
	if countNonEmpty(t.MinTests) == 0 {
		return fmt.Errorf("task %q: at least one min_test is required", t.ID)
	}
	if strings.TrimSpace(t.Instructions) == "" {
		return fmt.Errorf("task %q: instructions are required", t.ID)
	}
	return nil
}

// Validate checks every task and rejects duplicate ids.
func (p PRD) Validate() error {
	seen := make(map[string]bool, len(p.Tasks))
	for _, t := range p.Tasks {
		if err := t.Validate(); err != nil {
			return err
		}
		if seen[t.ID] {
			return fmt.Errorf("duplicate task id %q", t.ID)
		}
		seen[t.ID] = true
	}
	return nil
}

// Find returns the task with the given id and whether it was found.
func (p PRD) Find(id string) (Task, bool) {
	for _, t := range p.Tasks {
		if t.ID == id {
			return t, true
		}
	}
	return Task{}, false
}

// LoadPRD reads prd.yaml. A missing file yields an empty PRD (not an error).
func LoadPRD() (PRD, error) {
	var p PRD
	data, err := os.ReadFile(PRDPath())
	if errors.Is(err, fs.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return p, fmt.Errorf("read prd: %w", err)
	}
	if err := yaml.Unmarshal(data, &p); err != nil {
		return p, fmt.Errorf("parse prd: %w", err)
	}
	return p, nil
}

// SavePRD validates and writes prd.yaml atomically.
func SavePRD(p PRD) error {
	if err := p.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal prd: %w", err)
	}
	return writeFileAtomic(PRDPath(), data, 0o600)
}

// AddTask appends a task, rejecting a duplicate id, and persists the store.
func AddTask(t Task) error {
	if err := t.Validate(); err != nil {
		return err
	}
	p, err := LoadPRD()
	if err != nil {
		return err
	}
	if _, ok := p.Find(t.ID); ok {
		return fmt.Errorf("task %q already exists", t.ID)
	}
	p.Tasks = append(p.Tasks, t)
	return SavePRD(p)
}

// UpdateTask replaces the task with t.ID, requiring it to exist, and persists the store.
func UpdateTask(t Task) error {
	if err := t.Validate(); err != nil {
		return err
	}
	p, err := LoadPRD()
	if err != nil {
		return err
	}
	for i := range p.Tasks {
		if p.Tasks[i].ID == t.ID {
			p.Tasks[i] = t
			return SavePRD(p)
		}
	}
	return fmt.Errorf("task %q not found", t.ID)
}

// RemoveTask deletes the task with the given id, requiring it to exist, and persists.
func RemoveTask(id string) error {
	p, err := LoadPRD()
	if err != nil {
		return err
	}
	for i := range p.Tasks {
		if p.Tasks[i].ID == id {
			p.Tasks = append(p.Tasks[:i], p.Tasks[i+1:]...)
			return SavePRD(p)
		}
	}
	return fmt.Errorf("task %q not found", id)
}

func countNonEmpty(ss []string) int {
	n := 0
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			n++
		}
	}
	return n
}
