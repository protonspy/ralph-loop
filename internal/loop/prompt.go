package loop

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/protonspy/ralph-loop/internal/spec"
)

// buildPrompt renders the instruction for one fresh-context iteration. It scopes
// the AI to exactly one behavior (a RED+GREEN pair or a single leaf), points it
// at the csdd contract artifacts, and tells it to use the graph MCP for prior
// learnings instead of guessing.
//
// Per the chosen methodology the AI owns the checkbox flip and the commit; the
// driver verifies the gate afterwards and reverts on failure.
func buildPrompt(s *spec.Spec, unit []*spec.Task) string {
	req := filepath.Join(s.Dir, "requirements.md")
	des := filepath.Join(s.Dir, "design.md")
	tasks := filepath.Join(s.Dir, "tasks.md")

	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	p("You are one iteration of an autonomous, spec-driven implementation loop.")
	p("You have a FRESH context: everything you need is in files, not memory.")
	p("")
	p("Feature: %s   (development flow: %s)", s.Name, orDefault(s.JSON.DevelopmentFlow, "tdd"))
	p("Contract artifacts (read them before coding):")
	p("  - requirements:  %s", req)
	p("  - design:        %s", des)
	p("  - tasks:         %s", tasks)
	p("")
	p("## Your scope this iteration — implement ONLY these task(s):")
	for _, t := range unit {
		p("  - [ ] %s  %s", t.ID, t.Title)
		if t.Boundary != "" {
			p("        boundary:     %s   (do NOT edit outside this component)", t.Boundary)
		}
		if len(t.Requirements) > 0 {
			p("        requirements: %s", strings.Join(t.Requirements, ", "))
		}
	}
	p("")
	p("## Method")
	if isTDDPair(unit) {
		p("This is a RED+GREEN pair. Follow strict TDD:")
		p("  1. RED:   write the failing test for the behavior; run it; confirm it")
		p("            fails for the EXPECTED reason (not a syntax/import error).")
		p("  2. GREEN: write the minimal code to make that test pass; refactor under green.")
	} else {
		p("Implement this task, then cover its behavior with a test. Keep the suite green.")
	}
	p("Stay strictly inside the declared boundary. Do not start any other task.")
	p("")
	p("## Knowledge graph (MCP server \"graph\")")
	p("  - Before coding, search it (mcp__graph__search_facts / search_nodes) for prior")
	p("    learnings and decisions on this component; reuse instead of guessing.")
	p("  - When you hit a durable gotcha, record it (add_episode then add_fact).")
	p("")
	p("## Definition of done for THIS iteration")
	p("  1. The scoped test(s) pass and the full suite is green (no regressions).")
	p("  2. Check the box(es) for EXACTLY these task IDs in tasks.md: [ ] -> [x].")
	p("     Touch no other checkboxes.")
	p("  3. Record any reusable learning/gotcha under '## Implementation Notes' in tasks.md.")
	p("  4. Commit ALL changes with a Conventional Commit:")
	p("       feat(%s): %s", s.Name, unitSummary(unit))
	p("  5. Do not push. Do not commit on a failing suite.")
	p("")
	p("If you cannot complete the scope, make NO commit and explain what blocked you.")
	return b.String()
}

// UnitIDs returns the task IDs in a unit, for logging and completion checks.
func UnitIDs(unit []*spec.Task) []string {
	ids := make([]string, len(unit))
	for i, t := range unit {
		ids[i] = t.ID
	}
	return ids
}

func isTDDPair(unit []*spec.Task) bool {
	return len(unit) == 2 && unit[0].IsRED() && unit[1].IsGREEN()
}

func unitSummary(unit []*spec.Task) string {
	// prefer the GREEN/implementation task's title for the commit subject
	for _, t := range unit {
		if t.IsGREEN() || !t.IsRED() {
			return strings.ToLower(firstSentence(t.Title))
		}
	}
	return strings.ToLower(firstSentence(unit[0].Title))
}

func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, ".;"); i > 0 {
		return s[:i]
	}
	return s
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
