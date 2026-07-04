package spec

import "testing"

// sample mirrors the shape of a real csdd tasks.md, with a cross-boundary
// _Depends:_ so we can exercise dependency gating.
const sample = `# Tasks

<!-- legend: annotations here should be ignored (P) _Boundary: Ignored_ -->

## Phase 1: Foundation

- [ ] 1. [Foundation] _Boundary: Store_
  - [ ] 1.1 [schema skeleton]
    - _Requirements: 1.1_

## Phase 2: Core

- [ ] 2. [Core] (P) _Boundary: Api_
  - [ ] 2.1 RED — failing test
    - _Requirements: 1.2_
    - _Depends: 1.1_
  - [ ] 2.2 GREEN — make it pass
    - _Requirements: 1.2_
`

func load(t *testing.T, md string) *Spec {
	t.Helper()
	return &Spec{Tasks: ParseTasks(md)}
}

func TestParseStructure(t *testing.T) {
	s := load(t, sample)
	if len(s.Tasks) != 2 {
		t.Fatalf("want 2 top-level tasks, got %d", len(s.Tasks))
	}
	// legend comment must not leak into the tree
	leaves := s.Leaves()
	if len(leaves) != 3 {
		t.Fatalf("want 3 leaves (1.1, 2.1, 2.2), got %d: %+v", len(leaves), ids(leaves))
	}

	t21 := s.find("2.1")
	if t21 == nil || !t21.IsRED() {
		t.Fatalf("2.1 should parse as RED: %+v", t21)
	}
	if got := t21.Depends; len(got) != 1 || got[0] != "1.1" {
		t.Fatalf("2.1 depends want [1.1], got %v", got)
	}
	if b := s.find("2").Boundary; b != "Api" {
		t.Fatalf("task 2 boundary want Api, got %q", b)
	}
	if !s.find("2").Parallel {
		t.Fatalf("task 2 should be marked (P)")
	}
	// (P) inside a comment must not have flipped an unrelated task
	if s.find("1").Parallel {
		t.Fatalf("task 1 should not be parallel")
	}
}

func TestNextActionableRespectsDepends(t *testing.T) {
	s := load(t, sample)

	// nothing done → first leaf 1.1
	if n, _ := s.NextActionable(); n == nil || n.ID != "1.1" {
		t.Fatalf("want next 1.1, got %v", idOrNil(n))
	}

	// 1.1 done → 2.1 unblocks (its _Depends: 1.1_ is now met)
	s.find("1.1").Done = true
	if n, _ := s.NextActionable(); n == nil || n.ID != "2.1" {
		t.Fatalf("after 1.1 done, want 2.1, got %v", idOrNil(n))
	}

	// 2.1 done → 2.2 (sequential, no unmet depends)
	s.find("2.1").Done = true
	if n, _ := s.NextActionable(); n == nil || n.ID != "2.2" {
		t.Fatalf("after 2.1 done, want 2.2, got %v", idOrNil(n))
	}

	// everything done → allDone
	s.find("2.2").Done = true
	n, allDone := s.NextActionable()
	if n != nil || !allDone {
		t.Fatalf("want complete, got next=%v allDone=%v", idOrNil(n), allDone)
	}
}

func TestBlockedByUnmetDependency(t *testing.T) {
	// 2.1 depends on 1.1; if 1.1 is somehow skipped and only later leaves remain
	// unmet, the loop must report blocked rather than run out of order.
	md := `## P
- [x] 1. [done] _Boundary: A_
  - [x] 1.1 [x] done _Requirements: 1_
- [ ] 2. [core] _Boundary: B_
  - [ ] 2.1 needs a missing dep
    - _Depends: 9.9_
`
	s := load(t, md)
	n, allDone := s.NextActionable()
	if n != nil || allDone {
		t.Fatalf("want blocked (nil, false), got next=%v allDone=%v", idOrNil(n), allDone)
	}
}

func TestNextUnitPairsRedGreen(t *testing.T) {
	s := load(t, sample)

	// 1.1 is a plain foundation leaf → single-task unit
	u, _ := s.NextUnit()
	if len(u) != 1 || u[0].ID != "1.1" {
		t.Fatalf("want single unit [1.1], got %v", ids(u))
	}

	// once 1.1 is done, the next unit is the RED+GREEN pair 2.1+2.2
	s.find("1.1").Done = true
	u, _ = s.NextUnit()
	if len(u) != 2 || u[0].ID != "2.1" || u[1].ID != "2.2" {
		t.Fatalf("want pair [2.1 2.2], got %v", ids(u))
	}
	if !u[0].IsRED() || !u[1].IsGREEN() {
		t.Fatalf("pair should be RED then GREEN")
	}

	// all done → complete
	s.find("2.1").Done = true
	s.find("2.2").Done = true
	u, done := s.NextUnit()
	if u != nil || !done {
		t.Fatalf("want complete, got unit=%v done=%v", ids(u), done)
	}
}

func ids(ts []*Task) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}

func idOrNil(t *Task) string {
	if t == nil {
		return "<nil>"
	}
	return t.ID
}
