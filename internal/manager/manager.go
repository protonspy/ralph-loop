// Package manager is ralph-loop's OUTER loop — the "manager" that turns a raw
// challenge into a delivered program. It owns the phases above the csdd
// contract: staff a team, decompose the PRD into feats, and for each feat run
// research → spec-up → build (the inner csdd loop) → E2E acceptance.
//
// Division of labor: this package is the deterministic body (sequencing, state,
// gates); every judgment/creative phase is a Claude Code "brain" activation —
// a fresh `claude -p` process wired in phases.go. The body never reasons.
package manager

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/protonspy/ralph-loop/internal/gate"
	"github.com/protonspy/ralph-loop/internal/gitx"
	"github.com/protonspy/ralph-loop/internal/loop"
	"github.com/protonspy/ralph-loop/internal/program"
	"github.com/protonspy/ralph-loop/internal/tool"
)

// Options configures a manager run.
type Options struct {
	Root        string        // workspace root (holds .claude/ and .ralph/)
	Challenge   string        // the natural-language challenge (empty ⇒ resume existing program)
	Tool        tool.Runner   // AI tool for brain activations + inner loop
	Csdd        gate.Resolver // how to invoke csdd (gate + factory)
	Context7    string        // optional MCP command for Context7 docs (empty = disabled)
	MaxIter  int // inner loop max iterations per feat
	MaxRetry int // inner loop retries per unit
	DryRun   bool // print the end-to-end plan; mutate nothing, spawn nothing
	Out      io.Writer
}

// Run drives the whole program: rl run "<challenge>".
func Run(ctx context.Context, o Options) error {
	logf := func(format string, a ...any) { fmt.Fprintf(o.Out, format+"\n", a...) }

	if o.DryRun {
		return printPlan(o, logf)
	}

	// The manager pipeline needs claude for structured (JSON) brain activations;
	// amp only drives the inner loop's plain-text iterations.
	if o.Tool.Name != "" && o.Tool.Name != "claude" {
		return fmt.Errorf("the challenge pipeline needs claude for structured brain activations; --tool %q only works for the inner loop (rl run <spec-dir>)", o.Tool.Name)
	}

	p, err := program.Bootstrap(o.Root, o.Challenge)
	if err != nil {
		return fmt.Errorf("bootstrap program: %w", err)
	}
	logf("program %q on branch %s", p.Program, p.Branch)

	// Isolate the program's autonomous work on its own branch so every commit
	// (spec-up, build) lands there rather than on the base branch.
	if err := gitx.EnsureBranch(ctx, o.Root, p.Branch); err != nil {
		logf("   ⚠ could not check out %s (commits will land on the current branch): %v", p.Branch, err)
	}

	// Front of the pipeline: staff a team, then decompose the PRD into feats.
	if len(p.Feats) == 0 {
		logf("⓪ staffing a team for the challenge …")
		if err := phaseStaff(ctx, o, p); err != nil {
			// Soft: a missing bespoke team degrades quality, not correctness.
			logf("   ⚠ staffing failed (continuing with the default brain): %v", err)
		}
		logf("① decomposing the challenge into feats …")
		if err := phaseDecompose(ctx, o, p); err != nil {
			return fmt.Errorf("phase decompose: %w", err)
		}
		if err := program.Save(o.Root, p); err != nil {
			return err
		}
		logf("   → %d feats written to %s", len(p.Feats), program.Path(o.Root))
		projectGraph(o, p)
	}

	// Commit the front-of-pipeline artifacts (staffed team under .claude/, the
	// seeded CLAUDE.md) so the build gate starts from a clean tree.
	if err := gitx.CommitAll(ctx, o.Root, "chore("+p.Program+"): staff team + project conventions"); err != nil {
		logf("   ⚠ could not commit initial workspace state: %v", err)
	}

	// Outer loop: drive each feat to "done" (built AND E2E-accepted).
	for {
		p, err = program.Load(o.Root)
		if err != nil {
			return err
		}
		feat, allDone := p.NextFeat()
		if allDone {
			logf("✓ program %q complete", p.Program)
			return nil
		}
		if feat == nil {
			return fmt.Errorf("blocked: remaining feats have unmet dependencies")
		}
		if err := driveFeat(ctx, o, p, feat); err != nil {
			return err
		}
	}
}

// driveFeat takes one feat from wherever it is to done.
func driveFeat(ctx context.Context, o Options, p *program.PRD, feat *program.Feat) error {
	logf := func(format string, a ...any) { fmt.Fprintf(o.Out, format+"\n", a...) }
	logf("── feat %q (%s)", feat.ID, feat.Status)

	// ② research + ③ spec-up: manufacture the csdd contract for this feat.
	if feat.Status == program.StatusPlanned {
		logf("  ② research …")
		if err := phaseResearch(ctx, o, p, feat); err != nil {
			// Soft: an unresearched spec is worse, not impossible (D9 best-effort).
			logf("  ⚠ research failed (continuing unresearched): %v", err)
		}
		logf("  ③ spec-up (author → review → approve, per csdd phase) …")
		if err := phaseSpecUp(ctx, o, p, feat); err != nil {
			return fmt.Errorf("phase spec-up: %w", err)
		}
		feat.Status = program.StatusSpecGenerated
		if err := program.Save(o.Root, p); err != nil {
			return err
		}
		projectGraph(o, p)
		// Project the freshly-authored contract (requirements/components/traces)
		// into the living documentation graph. Best-effort.
		if err := projectSpecGraph(o, p, feat); err != nil {
			logf("  ⚠ graph spec projection: %v", err)
		}
	}

	// ③b AUTONOMOUS approval gate — NO human in the loop. The contextual
	// reviews and csdd approvals ran per phase inside spec-up; this is the
	// final mechanical latch before implementation.
	if feat.Status == program.StatusSpecGenerated {
		logf("  ③b autonomous approval (final validate + ready check) …")
		if err := phaseApprove(ctx, o, feat); err != nil {
			return fmt.Errorf("phase approve: %w", err)
		}
		feat.Status = program.StatusApproved
		if err := program.Save(o.Root, p); err != nil {
			return err
		}
		projectGraph(o, p)
	}

	// ④ build: the inner csdd contract loop (already implemented).
	if feat.Status == program.StatusApproved || feat.Status == program.StatusImplementing {
		feat.Status = program.StatusImplementing
		if err := program.Save(o.Root, p); err != nil {
			return err
		}
		projectGraph(o, p)
		logf("  ④ build (inner loop) …")
		if err := phaseBuild(ctx, o, feat); err != nil {
			return err
		}
	}

	// ⑤ E2E acceptance (Tier-2 DoD): prove the feat actually works.
	logf("  ⑤ E2E acceptance …")
	if err := phaseE2E(ctx, o, p, feat); err != nil {
		return fmt.Errorf("phase e2e: %w", err)
	}

	feat.Status = program.StatusDone
	if err := program.Save(o.Root, p); err != nil {
		return err
	}
	projectGraph(o, p)
	return program.AppendProgress(o.Root, feat.ID+" — done", "Built to csdd contract and E2E-accepted.")
}

// phaseBuild runs the inner loop over the feat's csdd spec.
func phaseBuild(ctx context.Context, o Options, feat *program.Feat) error {
	return loop.Run(ctx, loop.Options{
		Root:     o.Root,
		SpecDir:  filepath.Join(o.Root, feat.Spec),
		Tool:     o.Tool,
		Csdd:     o.Csdd,
		Max:      o.MaxIter,
		MaxRetry: o.MaxRetry,
		Out:      o.Out,
	})
}

