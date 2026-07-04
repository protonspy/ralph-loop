package manager

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/protonspy/ralph-loop/internal/program"
	"github.com/protonspy/ralph-loop/internal/spec"
)

// This file holds the per-phase implementations. Phases split into two kinds:
//
//   - BRAIN phases (staff, decompose, research, spec-up authoring, review, e2e):
//     each is a fresh `claude -p` activation whose output the deterministic body
//     consumes — the body never reasons.
//   - DETERMINISTIC gates (csdd validate/approve, git revert, build loop):
//     real csdd/git calls; build lives in manager.go.
//
// There is NO human phase — the pipeline is end-to-end autonomous; the human
// only receives the final delivered result.

// ---- ⓪ staff ----

// staffedAgents is the FIXED set of csdd-library agents ralph-loop brings in:
// the implementer (drives the build loop) plus csdd's built-in code and security
// reviewers. No brain decision, no default scaffold — just copy exactly these.
// Paths are `agents/<name>` (no extension); `csdd copy agents/code-reviewer`
// materializes .claude/agents/code-reviewer.md.
var staffedAgents = []string{
	"agents/implementer",
	"agents/code-reviewer",
	"agents/security-reviewer",
}

// phaseStaff assembles the team by COPYING the fixed reviewer agents from csdd's
// curated library (`csdd copy agents/<name>.md`). ralph-loop does NOT run
// `csdd init`, scaffold a default team, or author agents from scratch.
func phaseStaff(ctx context.Context, o Options, p *program.PRD) error {
	var copied []string
	for _, path := range staffedAgents {
		if res := o.Csdd.RunIn(ctx, o.Root, "copy", path); !res.OK {
			return fmt.Errorf("csdd copy %s failed (exit %d):\n%s", path, res.ExitCode, res.Output)
		}
		// The activatable agent name is the last path segment.
		name := path[strings.LastIndex(path, "/")+1:]
		p.Team = append(p.Team, name) // remembered so later phases can --agent it
		copied = append(copied, path)
	}
	return program.AppendProgress(o.Root, "team staffed", strings.Join(copied, ", "))
}

// ---- ① decompose ----

// decomposeOutput is the structured shape we ask the brain to return.
type decomposeOutput struct {
	PRDSummary string `json:"prd_summary"`
	Feats      []struct {
		ID      string   `json:"id"`
		Title   string   `json:"title"`
		Depends []string `json:"depends"`
	} `json:"feats"`
}

// phaseDecompose turns the raw challenge into the thinnest sequence of
// vertical-slice feats (each = one csdd spec), dependency-ordered, in p.Feats.
func phaseDecompose(ctx context.Context, o Options, p *program.PRD) error {
	var out decomposeOutput
	if err := brainJSON(ctx, o, decomposePrompt(p.Challenge), &out); err != nil {
		return err
	}
	if len(out.Feats) == 0 {
		return fmt.Errorf("decompose returned no feats")
	}
	seen := map[string]bool{}
	for _, f := range out.Feats {
		id := program.Slug(f.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		p.Feats = append(p.Feats, &program.Feat{
			ID:      id,
			Title:   strings.TrimSpace(f.Title),
			Spec:    filepath.Join("specs", id),
			Depends: slugAll(f.Depends),
			Status:  program.StatusPlanned,
		})
	}
	if rel, err := program.WritePRDDoc(o.Root, p, out.PRDSummary); err == nil {
		p.PRDPath = rel
	}
	_ = program.AppendProgress(o.Root, "decomposition", out.PRDSummary)
	return nil
}

func slugAll(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if s := program.Slug(id); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func decomposePrompt(challenge string) string {
	return fmt.Sprintf(`You are a pragmatic product architect decomposing a challenge into an
implementable program for an autonomous, spec-driven build system.

CHALLENGE: %s

Decompose it into a SEQUENCE OF VERTICAL-SLICE FEATURES ("feats"). Rules:
- A feat is the thinnest end-to-end slice that delivers observable, testable value.
  It must fit in a single focused implementation context (roughly 1-3 files, one concern).
- The FIRST feat must be the minimal usable/playable slice (a walking skeleton), NOT setup.
- Do NOT try to cover the whole vision. Produce the first 3-8 feats that build on each other.
  It is correct and expected to leave most of a huge challenge out of scope for now.
- Order by dependency: schema/foundation → core behavior → integration → UI/aggregation.
  Earlier feats MUST NOT depend on later ones.
- id: short kebab-case (e.g. "tile-map-movement"). depends: list of earlier feat ids ([] if none).

Respond with ONLY this JSON, no prose, no code fences:
{"prd_summary": "<2-4 sentence summary of the sliced program and what's out of scope>",
 "feats": [{"id": "<kebab>", "title": "<one line>", "depends": ["<id>", ...]}]}`, challenge)
}

// ---- ② research ----

type researchOutput struct {
	Summary      string `json:"summary"`
	FactsIndexed int    `json:"facts_indexed"`
}

// phaseResearch grounds a feat before it is specified: the brain researches the
// domain (open web + any configured doc source) and indexes durable findings
// into our graph KB, so requirements are grounded, not guessed. The caller
// treats failure as soft — an unresearched spec is worse, not impossible.
func phaseResearch(ctx context.Context, o Options, p *program.PRD, feat *program.Feat) error {
	cfg, err := writeMCPConfig(o.Root, false, o.Context7)
	if err != nil {
		return err
	}
	agent := pickAgent(p.Team, "research", "analyst", "scout")
	var out researchOutput
	if err := brainJSON(ctx, o, researchPrompt(p, feat), &out, brainArgs(cfg, agent)...); err != nil {
		return err
	}
	return program.AppendProgress(o.Root, feat.ID+" — research",
		fmt.Sprintf("%s\n\n(%d facts indexed into the graph KB)", out.Summary, out.FactsIndexed))
}

func researchPrompt(p *program.PRD, feat *program.Feat) string {
	return fmt.Sprintf(`You are the researcher of an autonomous delivery pipeline, with a FRESH context.
Your job: gather the domain knowledge needed to specify ONE feat well, and index
it into the knowledge graph so later phases (spec authoring, implementation)
build on facts instead of guesses.

%s
FEAT UNDER RESEARCH: %s — %s

%s

METHOD
1. Query the graph first (search_facts, neighbors on feat %q) — do not re-research
   what is already indexed.
2. Research what is missing for THIS feat only: use WebSearch/WebFetch for current
   library choices, APIs, best practices and known pitfalls (and Context7 tools if
   available). Prefer authoritative sources.
3. Index what you learned: one add_episode per source consulted, then 5-15
   add_fact calls — concrete, implementation-relevant facts (library versions,
   API shapes, constraints, pitfalls), each linked to its episode. Use concept/
   component nodes and RELATES_TO/DEPENDS_ON relations; connect key concepts to
   the feat node (kind "feat", name %q).

Respond with ONLY this JSON, no prose, no code fences:
{"summary": "<4-8 sentences: what you learned that shapes this feat's spec>",
 "facts_indexed": <number of add_fact calls that succeeded>}`,
		programContext(p, feat), feat.ID, feat.Title, graphToolsHint, feat.ID, feat.ID)
}

// ---- ③ spec-up (author + review + approve, per csdd phase) ----

// csddPhases in contract order. csdd gates each artifact on the previous
// phase's approval, so authoring and approval are interleaved per phase.
var csddPhases = []string{"requirements", "design", "tasks"}

// phaseSpecUp manufactures the csdd contract for a feat. For each contract
// phase (requirements → design → tasks) it runs the autonomous version of
// csdd's human loop:
//
//	author (brain, agentic) → contextual review (brain, D8) → csdd approve (mechanical)
//
// A rejected artifact routes the reviewer's reasons back into a fresh authoring
// activation (the refine loop) instead of stopping — bounded by MaxRetry.
func phaseSpecUp(ctx context.Context, o Options, p *program.PRD, feat *program.Feat) error {
	logf := func(format string, a ...any) { fmt.Fprintf(o.Out, format+"\n", a...) }
	retries := max(o.MaxRetry, 1)
	cfg, err := writeMCPConfig(o.Root, false, o.Context7)
	if err != nil {
		return err
	}
	lead := pickAgent(p.Team, "lead", "architect", "tech", "planner")

	for _, phase := range csddPhases {
		feedback := ""
		approved := false
		for attempt := 1; attempt <= retries && !approved; attempt++ {
			logf("     %s: authoring (attempt %d/%d) …", phase, attempt, retries)
			if _, err := o.Tool.Run(ctx, o.Root, authorPrompt(o, p, feat, phase, feedback), brainArgs(cfg, lead)...); err != nil {
				logf("     tool exited with error: %v (continuing to review)", err)
			}

			verdict, err := reviewSpec(ctx, o, p, feat, phase)
			if err != nil {
				return fmt.Errorf("review %s/%s: %w", feat.ID, phase, err)
			}
			if !verdict.Approve {
				feedback = "The previous attempt was REJECTED by the reviewer. Fix ALL of:\n- " +
					strings.Join(verdict.Reasons, "\n- ")
				logf("     %s: rejected by reviewer — refining (%s)", phase, strings.Join(verdict.Reasons, "; "))
				continue
			}

			res := o.Csdd.Approve(ctx, o.Root, feat.ID, phase)
			if !res.OK {
				feedback = fmt.Sprintf("csdd rejected the %s approval (exit %d). Its output:\n%s\nFix the artifact so `%s spec validate %s` passes.",
					phase, res.ExitCode, res.Output, o.Csdd.Command(), feat.ID)
				logf("     %s: csdd approve failed — refining", phase)
				continue
			}
			approved = true
			logf("     %s: reviewed + approved ✓", phase)
		}
		if !approved {
			return fmt.Errorf("spec-up for %s: phase %s not approved after %d attempts", feat.ID, phase, retries)
		}
	}
	return nil
}

// authorPrompt scopes one authoring activation to exactly one contract phase.
func authorPrompt(o Options, p *program.PRD, feat *program.Feat, phase, feedback string) string {
	var b strings.Builder
	pr := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	pr("You are the tech-lead of an autonomous spec-driven pipeline, authoring the")
	pr("%s artifact of the csdd contract for ONE feat. FRESH context: everything", strings.ToUpper(phase))
	pr("you need is in files and the knowledge graph.")
	pr("")
	pr("%s", programContext(p, feat))
	pr("FEAT: %s — %s     SPEC DIR: %s", feat.ID, feat.Title, feat.Spec)
	pr("csdd is invoked as: %s", o.Csdd.Command())
	pr("")
	pr("%s", graphToolsHint)
	pr("")
	pr("STEPS")
	pr("1. Read .ralph/progress.md (this feat's research summary) and query the graph")
	pr("   (search_facts, neighbors on feat %q) before writing anything.", feat.ID)
	switch phase {
	case "requirements":
		pr("2. If %s/spec.json does not exist, scaffold it: %s spec init %s", feat.Spec, o.Csdd.Command(), feat.ID)
		pr("3. Author %s/requirements.md: EARS-format requirements (WHEN/WHILE/IF-THEN", feat.Spec)
		pr("   shall-statements), each grounded in the challenge and the indexed research.")
		pr("   Scope = THIS feat only; sibling feats own their own requirements.")
		pr("   Include concrete acceptance criteria — the E2E phase will judge against them.")
	case "design":
		pr("2. Generate the template: %s spec generate %s --artifact design", o.Csdd.Command(), feat.ID)
		pr("3. Author %s/design.md: architecture for this feat — components with clear", feat.Spec)
		pr("   boundaries, data flow, technology choices justified by graph facts, and a")
		pr("   boundary map consistent with the approved requirements.")
	case "tasks":
		pr("2. Generate the template: %s spec generate %s --artifact tasks", o.Csdd.Command(), feat.ID)
		pr("3. Author %s/tasks.md: small RED/GREEN TDD task pairs (test-first), each", feat.Spec)
		pr("   tracing to requirement IDs, each with a boundary from the design. The final")
		pr("   phase must contain the E2E task proving the feat end-to-end.")
	}
	pr("4. Validate until green: %s spec validate %s --root . (fix, re-run; exit 0 required).", o.Csdd.Command(), feat.ID)
	pr("5. Commit ONLY the spec directory: git add %s && git commit -m %q", feat.Spec, "docs("+feat.ID+"): author "+phase)
	pr("   Do not push. Do not touch other feats' specs or application code.")
	if feedback != "" {
		pr("")
		pr("REVIEWER FEEDBACK ON YOUR PREVIOUS ATTEMPT")
		pr("%s", feedback)
	}
	return b.String()
}

// ---- ③b review (the brain half of the autonomous approval gate) ----

type reviewVerdict struct {
	Approve bool     `json:"approve"`
	Reasons []string `json:"reasons"`
}

// reviewSpec is the brain's contextual approval judgment (D8): it judges what
// was created for the spec against the GENERAL context — challenge, PRD,
// sibling feats, the knowledge graph — not just mechanical validity. csdd's
// mechanical gate runs separately on approve.
func reviewSpec(ctx context.Context, o Options, p *program.PRD, feat *program.Feat, phase string) (reviewVerdict, error) {
	cfg, err := writeMCPConfig(o.Root, false, o.Context7)
	if err != nil {
		return reviewVerdict{}, err
	}
	agent := pickAgent(p.Team, "review", "critic", "qa")
	var v reviewVerdict
	if err := brainJSON(ctx, o, reviewPrompt(p, feat, phase), &v, brainArgs(cfg, agent)...); err != nil {
		return reviewVerdict{}, err
	}
	if !v.Approve && len(v.Reasons) == 0 {
		v.Reasons = []string{"reviewer rejected without reasons; author a substantially better artifact"}
	}
	return v, nil
}

func reviewPrompt(p *program.PRD, feat *program.Feat, phase string) string {
	return fmt.Sprintf(`You are the adversarial spec reviewer of an autonomous delivery pipeline —
the judgment a human approver would apply, made yours. FRESH context.

%s
UNDER REVIEW: the %s artifact of feat %s — read %s/%s.md and its spec.json,
plus the earlier artifacts in the same directory for consistency.

%s

Judge the artifact against the GENERAL context, not just internal coherence:
- Does it serve the challenge and THIS feat's slice (no scope creep into siblings)?
- Is it consistent with dependencies and already-done feats?
- Is it grounded — does it agree with the indexed research facts (query the graph)?
- Is it actionable for a fresh-context implementer (concrete, testable, bounded)?
- requirements: EARS form, acceptance criteria observable end-to-end.
- design: boundaries real, tech choices justified. tasks: RED/GREEN pairs, traced.

Approve ONLY what you would stake the delivery on. Reject with specific,
fixable reasons otherwise.

Respond with ONLY this JSON, no prose, no code fences:
{"approve": true|false, "reasons": ["<specific reason>", ...]}`,
		programContext(p, feat), phase, feat.ID, feat.Spec, artifactFile(phase), graphToolsHint)
}

func artifactFile(phase string) string {
	if phase == "requirements" || phase == "design" || phase == "tasks" {
		return phase
	}
	return "spec"
}

// ---- ③b approve (final mechanical gate) ----

// phaseApprove is the final latch of the autonomous approval gate. The
// contextual reviews and per-phase csdd approvals already ran inside spec-up
// (csdd gates each artifact on the previous approval, so they interleave);
// here the body verifies the outcome mechanically: the whole contract must
// validate and the spec must be ready_for_implementation.
func phaseApprove(ctx context.Context, o Options, feat *program.Feat) error {
	if res := o.Csdd.Validate(ctx, o.Root, feat.ID); !res.OK {
		return fmt.Errorf("csdd validate %s failed after spec-up (exit %d):\n%s", feat.ID, res.ExitCode, res.Output)
	}
	s, err := spec.Load(filepath.Join(o.Root, feat.Spec))
	if err != nil {
		return fmt.Errorf("load spec after approval: %w", err)
	}
	if !s.Ready() {
		return fmt.Errorf("spec %s approved phases but is not ready_for_implementation", feat.ID)
	}
	return nil
}

// ---- ⑤ E2E ----

type e2eVerdict struct {
	Passed   bool     `json:"passed"`
	Evidence string   `json:"evidence"`
	Reasons  []string `json:"reasons"`
}

// phaseE2E is the Tier-2 delivery gate: drive the REAL feat (run the app;
// Playwright as hands/eyes for UI) against the acceptance criteria and judge
// pass/fail. Only a pass flips the feat to done.
func phaseE2E(ctx context.Context, o Options, p *program.PRD, feat *program.Feat) error {
	cfg, err := writeMCPConfig(o.Root, true, o.Context7)
	if err != nil {
		return err
	}
	agent := pickAgent(p.Team, "e2e", "qa", "accept")
	var v e2eVerdict
	if err := brainJSON(ctx, o, e2ePrompt(p, feat), &v, brainArgs(cfg, agent)...); err != nil {
		return err
	}
	if !v.Passed {
		return fmt.Errorf("E2E rejected feat %s:\n- %s", feat.ID, strings.Join(v.Reasons, "\n- "))
	}
	return program.AppendProgress(o.Root, feat.ID+" — E2E evidence", v.Evidence)
}

func e2ePrompt(p *program.PRD, feat *program.Feat) string {
	return fmt.Sprintf(`You are the E2E acceptance judge of an autonomous delivery pipeline. The feat
below claims to be built to contract (unit tests + validation green). Your job
is Tier 2: prove it actually WORKS end-to-end, as a user would experience it.
FRESH context.

%s
FEAT UNDER ACCEPTANCE: %s — %s
ACCEPTANCE CRITERIA: %s/requirements.md (read it; judge against it, not vibes).

METHOD
1. Run the real thing: build/start the application from this workspace.
2. Exercise THIS feat's behavior end-to-end. For anything with a UI use the
   Playwright MCP tools (navigate, interact, screenshot); for CLIs/services,
   drive the real binary/endpoints via Bash.
3. Judge every acceptance criterion. Unmet criterion or broken core flow = fail.
   Sibling feats' unfinished behavior does NOT fail this feat.
4. Record the outcome in the knowledge graph: add_episode (source "e2e") with
   the evidence, and add_fact linking test/feat nodes (e.g. VERIFIES).

Respond with ONLY this JSON, no prose, no code fences:
{"passed": true|false,
 "evidence": "<what you ran and observed, concretely>",
 "reasons": ["<why it failed — specific and reproducible>", ...]}`,
		programContext(p, feat), feat.ID, feat.Title, feat.Spec)
}

// ---- --dry-run plan ----

// printPlan renders the full end-to-end pipeline for --dry-run without touching
// disk or spawning anything.
func printPlan(o Options, logf func(string, ...any)) error {
	slug := program.Slug(o.Challenge)
	logf("plan for: %q", o.Challenge)
	logf("  program: %s   branch: ralph/%s   state: .ralph/prd.json + .ralph/progress.md", slug, slug)
	logf("")
	logf("FRONT (once):")
	logf("  ⓪ init       ralph scaffolds .claude/ + CLAUDE.md (NOT csdd init)")
	logf("  ⓪ staff      csdd copy agents/{implementer,code-reviewer,security-reviewer} (soft-fails)")
	logf("  ① decompose  brain → PRD + feats+deps → .ralph/prd.json → graph")
	logf("")
	logf("OUTER LOOP (per feat, dependency order):")
	logf("  ② research   brain + WebSearch → facts indexed into the graph KB (soft-fails)")
	logf("  ③ spec-up    per csdd phase: brain authors → reviewer brain judges vs general")
	logf("               context → refine loop on reject → csdd approve (mechanical)")
	logf("  ③b approve   final latch: csdd validate green + ready_for_implementation")
	logf("  ④ build      inner loop as --agent implementer: RED+GREEN, gate + git revert")
	logf("  ⑤ e2e        brain e2e-qa + Playwright MCP → verdict JSON → feat done")
	logf("")
	logf("No human in the loop; the human receives only the final delivered program.")

	if program.Exists(o.Root) {
		if p, err := program.Load(o.Root); err == nil && len(p.Feats) > 0 {
			logf("")
			logf("existing program %q — feats:", p.Program)
			for _, f := range p.Feats {
				logf("  [%s] %s  (deps: %v)", f.Status, f.ID, f.Depends)
			}
			if next, done := p.NextFeat(); done {
				logf("→ all feats done")
			} else if next != nil {
				logf("→ next feat: %s", next.ID)
			}
		}
	}
	return nil
}
