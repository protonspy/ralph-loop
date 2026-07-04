package spec

import (
	"regexp"
	"strings"
)

// Task is one node in the tasks.md checklist tree.
type Task struct {
	ID           string   // "1", "1.1", "2.2"
	Title        string   // text after the ID, minus annotations
	Done         bool     // [x] vs [ ]
	Parallel     bool     // carries the (P) marker
	Boundary     string   // _Boundary: Component_
	Requirements []string // _Requirements: 1.1, 1.2_
	Depends      []string // _Depends: 3.1, 4.2_
	Line         int      // 1-based line in tasks.md
	Indent       int      // leading-space count of the checkbox
	Children     []*Task
}

// IsRED / IsGREEN classify TDD sub-tasks by the csdd naming convention.
func (t *Task) IsRED() bool   { return strings.HasPrefix(strings.ToUpper(t.Title), "RED") }
func (t *Task) IsGREEN() bool { return strings.HasPrefix(strings.ToUpper(t.Title), "GREEN") }

var (
	// - [ ] 1.2 Some title _Boundary: X_
	checkboxRe = regexp.MustCompile(`^(\s*)- \[( |x|X)\]\s+([0-9]+(?:\.[0-9]+)*)\.?\s+(.*)$`)
	// _Requirements: 1.1, 1.2_  /  _Depends: 3.1_  /  _Boundary: Comp_
	annotationRe = regexp.MustCompile(`_(Requirements|Depends|Boundary):\s*([^_]+)_`)
	parallelRe   = regexp.MustCompile(`\(P\)`)
)

// ParseTasks builds the task tree from tasks.md content. Nesting is derived
// from checkbox indentation. Annotations may appear inline on the checkbox line
// or on their own indented lines beneath it (both forms occur in csdd output).
func ParseTasks(md string) []*Task {
	var roots []*Task
	// stack of open tasks by indent for parent resolution
	type frame struct {
		indent int
		task   *Task
	}
	var stack []frame
	var last *Task // most recent task, for attaching own-line annotations

	lines := strings.Split(md, "\n")
	inComment := false
	for i, line := range lines {
		// skip HTML comment blocks (annotation legend, notes)
		if strings.Contains(line, "<!--") {
			inComment = true
		}
		if inComment {
			if strings.Contains(line, "-->") {
				inComment = false
			}
			continue
		}

		m := checkboxRe.FindStringSubmatch(line)
		if m == nil {
			// standalone annotation line for the previous task
			if last != nil {
				applyAnnotations(last, line)
			}
			continue
		}

		indent := len(m[1])
		t := &Task{
			ID:     m[3],
			Done:   strings.EqualFold(m[2], "x"),
			Line:   i + 1,
			Indent: indent,
		}
		title := m[4]
		if parallelRe.MatchString(title) {
			t.Parallel = true
		}
		applyAnnotations(t, title)
		t.Title = cleanTitle(title)

		// pop frames whose indent is >= this one; the remaining top is parent
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			roots = append(roots, t)
		} else {
			parent := stack[len(stack)-1].task
			parent.Children = append(parent.Children, t)
		}
		stack = append(stack, frame{indent: indent, task: t})
		last = t
	}
	return roots
}

func applyAnnotations(t *Task, s string) {
	for _, m := range annotationRe.FindAllStringSubmatch(s, -1) {
		key, val := m[1], strings.TrimSpace(m[2])
		switch key {
		case "Boundary":
			t.Boundary = val
		case "Requirements":
			t.Requirements = append(t.Requirements, splitIDs(val)...)
		case "Depends":
			t.Depends = append(t.Depends, splitIDs(val)...)
		}
	}
}

func splitIDs(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// cleanTitle strips annotations and the (P) marker from a checkbox title.
func cleanTitle(s string) string {
	s = annotationRe.ReplaceAllString(s, "")
	s = parallelRe.ReplaceAllString(s, "")
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(s), "—-"))
}
