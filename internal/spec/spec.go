// Package spec reads the csdd contract for a feature: the spec.json state
// tracker and the tasks.md checklist. ralph-loop treats these as the single
// source of truth for the autonomous loop — there is no separate prd.json.
//
// The csdd binary remains the authority for *validation*; this package only
// parses the artifacts so the loop can decide what to work on next.
package spec

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Approval mirrors csdd's per-artifact approval flag in spec.json.
type Approval struct {
	Generated bool `json:"generated"`
	Approved  bool `json:"approved"`
}

// JSON mirrors the csdd spec.json schema (only the fields the loop needs).
type JSON struct {
	FeatureName            string              `json:"feature_name"`
	Phase                  string              `json:"phase"`
	DevelopmentFlow        string              `json:"development_flow"`
	Approvals              map[string]Approval `json:"approvals"`
	ReadyForImplementation bool                `json:"ready_for_implementation"`
	CreatedAt              string              `json:"created_at"`
}

// Spec is a loaded feature: its state plus its parsed task tree.
type Spec struct {
	Dir   string // specs/<feature>
	Name  string
	JSON  JSON
	Tasks []*Task // top-level tasks; leaves reached via Children
}

// Load reads spec.json and tasks.md from a feature directory.
func Load(dir string) (*Spec, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "spec.json"))
	if err != nil {
		return nil, fmt.Errorf("read spec.json: %w", err)
	}
	var sj JSON
	if err := json.Unmarshal(raw, &sj); err != nil {
		return nil, fmt.Errorf("parse spec.json: %w", err)
	}

	name := sj.FeatureName
	if name == "" {
		name = filepath.Base(dir)
	}

	s := &Spec{Dir: dir, Name: name, JSON: sj}

	tasksRaw, err := os.ReadFile(filepath.Join(dir, "tasks.md"))
	if err != nil {
		return nil, fmt.Errorf("read tasks.md: %w", err)
	}
	s.Tasks = ParseTasks(string(tasksRaw))
	return s, nil
}

// Ready reports whether the contract permits implementation to begin.
func (s *Spec) Ready() bool { return s.JSON.ReadyForImplementation }

// Leaves returns every leaf task (a task with no children), in document order.
func (s *Spec) Leaves() []*Task {
	var out []*Task
	var walk func(t *Task)
	walk = func(t *Task) {
		if len(t.Children) == 0 {
			out = append(out, t)
			return
		}
		for _, c := range t.Children {
			walk(c)
		}
	}
	for _, t := range s.Tasks {
		walk(t)
	}
	return out
}

// NextActionable returns the first not-done leaf whose dependencies are all
// satisfied (every _Depends:_ ID resolves to a done task). Returns nil when the
// spec is complete or blocked. The bool reports whether the spec is fully done.
func (s *Spec) NextActionable() (*Task, bool) {
	leaves := s.Leaves()
	done := map[string]bool{}
	for _, t := range leaves {
		done[t.ID] = t.Done
	}
	allDone := true
	for _, t := range leaves {
		if t.Done {
			continue
		}
		allDone = false
		if s.dependenciesMet(t, done) {
			return t, false
		}
	}
	return nil, allDone
}

// NextUnit returns the tasks the next iteration should implement as ONE
// behavior: a RED leaf paired with the GREEN leaf that immediately follows it,
// or a single non-TDD leaf (foundation, e2e). This keeps each fresh-context
// iteration to one testable behavior and never commits a lone failing test.
// The bool reports whether the spec is fully complete.
func (s *Spec) NextUnit() ([]*Task, bool) {
	next, allDone := s.NextActionable()
	if next == nil {
		return nil, allDone
	}
	if !next.IsRED() {
		return []*Task{next}, false
	}
	leaves := s.Leaves()
	for i, t := range leaves {
		if t == next && i+1 < len(leaves) && leaves[i+1].IsGREEN() {
			return []*Task{next, leaves[i+1]}, false
		}
	}
	return []*Task{next}, false
}

func (s *Spec) dependenciesMet(t *Task, done map[string]bool) bool {
	for _, dep := range t.Depends {
		// A dependency may name a parent task; treat it met if the ID is done
		// or every leaf under that ID is done.
		if done[dep] {
			continue
		}
		if !s.subtreeDone(dep) {
			return false
		}
	}
	return true
}

// subtreeDone reports whether every leaf under the task identified by id is
// done. Unknown ids are treated as not done (a dangling _Depends:_ blocks).
func (s *Spec) subtreeDone(id string) bool {
	target := s.find(id)
	if target == nil {
		return false
	}
	for _, leaf := range leavesOf(target) {
		if !leaf.Done {
			return false
		}
	}
	return true
}

func (s *Spec) find(id string) *Task {
	var found *Task
	var walk func(t *Task)
	walk = func(t *Task) {
		if found != nil {
			return
		}
		if t.ID == id {
			found = t
			return
		}
		for _, c := range t.Children {
			walk(c)
		}
	}
	for _, t := range s.Tasks {
		walk(t)
	}
	return found
}

func leavesOf(t *Task) []*Task {
	if len(t.Children) == 0 {
		return []*Task{t}
	}
	var out []*Task
	for _, c := range t.Children {
		out = append(out, leavesOf(c)...)
	}
	return out
}
