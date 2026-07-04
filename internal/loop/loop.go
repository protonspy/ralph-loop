// Package loop is the ralph-loop engine: a stateless iteration driver over a
// csdd spec. Each iteration works one behavior in a fresh AI process, then the
// driver enforces the csdd contract as an atomic gate — on failure the git
// state is reverted so a bad iteration never poisons the next one.
package loop

import (
	"context"
	"fmt"
	"io"

	"github.com/protonspy/ralph-loop/internal/gate"
	"github.com/protonspy/ralph-loop/internal/gitx"
	"github.com/protonspy/ralph-loop/internal/spec"
	"github.com/protonspy/ralph-loop/internal/tool"
)

// Options configures a run.
type Options struct {
	Root     string        // workspace root (contains .claude/)
	SpecDir  string        // specs/<feature>
	Tool     tool.Runner   // AI tool for iterations
	Csdd     gate.Resolver // how to invoke csdd for the gate
	Max      int           // max iterations
	MaxRetry int           // consecutive failures on the same unit before giving up
	DryRun   bool          // plan only: print prompts, never spawn/commit
	Out      io.Writer     // progress sink (defaults handled by caller)
	OnUnit   func(UnitResult) // optional: called after a unit passes the gate (observational)
}

// UnitResult reports a committed, gate-passed unit to an optional observer, so
// higher layers (e.g. the living documentation graph) can record what a build
// iteration produced. Purely observational — it never affects the gate.
type UnitResult struct {
	Tasks  []*spec.Task // the unit's task(s)
	Files  []string     // repo-relative files changed in the unit's commit
	Commit string       // the new HEAD after the AI's commit
}

// Run executes the loop until the spec is complete, blocked, or Max is reached.
func Run(ctx context.Context, o Options) error {
	logf := func(format string, a ...any) { fmt.Fprintf(o.Out, format+"\n", a...) }

	s, err := spec.Load(o.SpecDir)
	if err != nil {
		return err
	}
	if !s.Ready() && !o.DryRun {
		return fmt.Errorf("spec %q is not ready_for_implementation; approve the tasks phase in csdd first", s.Name)
	}

	var lastUnit string
	fails := 0

	for iter := 1; iter <= o.Max; iter++ {
		// Reload state every iteration — files are the source of truth.
		s, err = spec.Load(o.SpecDir)
		if err != nil {
			return err
		}
		unit, allDone := s.NextUnit()
		if allDone {
			logf("✓ %s: all tasks complete", s.Name)
			return nil
		}
		if unit == nil {
			return fmt.Errorf("blocked: remaining tasks have unmet dependencies")
		}

		ids := UnitIDs(unit)
		logf("── iteration %d/%d — task(s) %v: %s", iter, o.Max, ids, unit[len(unit)-1].Title)
		prompt := buildPrompt(s, unit)

		if o.DryRun {
			logf("[dry-run] would spawn %q with this prompt:\n%s", toolName(o.Tool), indentBlock(prompt))
			logf("[dry-run] gate would run: csdd spec validate %s", s.Name)
			logf("[dry-run] stopping after first planned iteration")
			return nil
		}

		snapshot, err := gitx.Head(ctx, o.Root)
		if err != nil {
			return fmt.Errorf("git snapshot: %w", err)
		}

		if _, err := o.Tool.Run(ctx, o.Root, prompt); err != nil {
			logf("  tool exited with error: %v (continuing to gate)", err)
		}

		ok, reason := verify(ctx, o, snapshot, ids)
		if ok {
			logf("  ✓ gate passed; committed by tool")
			if o.OnUnit != nil {
				if head, herr := gitx.Head(ctx, o.Root); herr == nil {
					files, _ := gitx.ChangedFiles(ctx, o.Root, snapshot, head)
					o.OnUnit(UnitResult{Tasks: unit, Files: files, Commit: head})
				}
			}
			lastUnit, fails = "", 0
			continue
		}

		// Atomic rollback: discard the failed iteration entirely.
		logf("  ✗ gate failed: %s — reverting to %s", reason, short(snapshot))
		if err := gitx.ResetHard(ctx, o.Root, snapshot); err != nil {
			return fmt.Errorf("revert after failed iteration: %w", err)
		}

		if joinIDs(ids) == lastUnit {
			fails++
		} else {
			lastUnit, fails = joinIDs(ids), 1
		}
		if fails >= o.MaxRetry {
			return fmt.Errorf("task(s) %v failed the gate %d times in a row; stopping for human review", ids, fails)
		}
		logf("  will retry task(s) %v with a fresh context (%d/%d)", ids, fails, o.MaxRetry)
	}

	return fmt.Errorf("reached max iterations (%d) without completing %s", o.Max, s.Name)
}

// verify checks that the iteration actually advanced the contract: the AI must
// have committed (HEAD moved past the pre-iteration snapshot), left no dirty
// tree, ticked exactly the scoped checkboxes, and passed csdd validate. Any miss
// rejects the iteration.
func verify(ctx context.Context, o Options, snapshot string, ids []string) (bool, string) {
	moved, err := gitx.HeadMoved(ctx, o.Root, snapshot)
	if err != nil {
		return false, "git head check: " + err.Error()
	}
	if !moved {
		return false, "tool made no commit"
	}
	if dirty, _ := gitx.Dirty(ctx, o.Root); dirty {
		return false, "working tree left dirty (uncommitted changes)"
	}
	s, err := spec.Load(o.SpecDir)
	if err != nil {
		return false, "reload spec: " + err.Error()
	}
	if missing := uncheckedAmong(s, ids); len(missing) > 0 {
		return false, fmt.Sprintf("task(s) %v not checked off in tasks.md", missing)
	}
	if g := o.Csdd.Validate(ctx, o.Root, s.Name); !g.OK {
		if g.Violation {
			return false, "csdd contract violation (exit 2)"
		}
		return false, fmt.Sprintf("csdd validate error (exit %d)", g.ExitCode)
	}
	return true, ""
}

func uncheckedAmong(s *spec.Spec, ids []string) []string {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var missing []string
	for _, leaf := range s.Leaves() {
		if want[leaf.ID] && !leaf.Done {
			missing = append(missing, leaf.ID)
		}
	}
	return missing
}
