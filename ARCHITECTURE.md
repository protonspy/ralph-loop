# ralph-loop — Architecture, Decisions & Roadmap

> Handoff document. Everything decided so far, the architecture, and what remains
> to be done. Written to continue the work later without losing context.
> Last updated: 2026-07-03 (all pipeline phases wired and REAL; offline E2E test green).

---

## 1. What ralph-loop is

**ralph-loop** (`rl`) is an autonomous, spec-driven software-delivery **manager**. Given a
single natural-language challenge — e.g. `rl run "fazer um jogo estilo wow"` — it takes the
challenge all the way to delivered, contract-verified code, **with no human in the loop**
(the human only receives the final result).

It fuses three ideas:

- **Ralph** (snarktank/ralph) — the *methodology*: a stateless loop, **fresh context per
  iteration**, file-based state, atomic quality gates. (Ralph's bash script is only a
  reference; we reimplement the methodology natively in Go.)
- **csdd** (protonspy/csdd) — the *contract*: Spec-Driven Development + TDD, mechanically
  enforced (EARS requirements, design/traceability, RED/GREEN tasks, `spec validate`).
  ralph-loop is a first-party sibling of csdd (same owner); `rl` could later become a csdd
  subcommand. **csdd is used as an oracle** (we shell out to its binary) today; it can
  become an in-process Go dependency once csdd exposes public packages.
- A **graph knowledge base** we own (see §7) — the project's living, queryable
  documentation + temporal memory. (We evaluated context-mode and Graphiti; both were
  dropped — see decisions D7–D10.)

**One-line identity:** *rl is the manager that turns a challenge into a bespoke team, the
team into grounded contracts (via csdd), contracts into code, and code into
E2E-proven delivery — repeating feat by feat until the whole program is done.*

---

## 2. The two loop altitudes (rl vs csdd)

The core distinction. There are **two nested loops**:

```
┌── ralph-loop = the CONTROL / manager (ABOVE specs) ──────────────┐
│  PRD/challenge → decompose into FEATS (1 feat = 1 csdd spec)      │
│  OUTER LOOP: for each feat in dependency order →                 │
│     staff · research · spec-up · approve · BUILD · E2E → done     │
│                                     │                            │
│        ┌── csdd = the CONTRACT (per spec) ──────────────────────┐│
│        │  INNER LOOP (rl run <spec>): RED+GREEN per task,        ││
│        │  gate = csdd validate + tests + boundaries              ││
│        └──────────────────────────────────────────────────────┘│
└──────────────────────────────────────────────────────────────────┘
```

| Altitude            | State tracker                 | Owner       |
|---------------------|-------------------------------|-------------|
| Program (PRD→feats) | `.ralph/prd.json`             | ralph-loop  |
| Feat (spec)         | `spec.json` + `tasks.md`      | csdd        |
| Task (RED/GREEN)    | `tasks.md` checkboxes         | csdd        |

Ralph's `prd.json` concept "moves up" one level: our `.ralph/prd.json` tracks **feats**, not
user-stories→tasks. csdd owns everything *inside* a feat.

---

## 3. Brain / body split

Same philosophy as Ralph (bash orchestrates, AI thinks), applied in Go:

- **`rl` (Go binary) = the deterministic body / nervous system.** It sequences phases, holds
  state, applies gates (csdd validate, git revert), calls the csdd factory, and writes the
  graph. **`rl` never reasons.**
- **Claude Code = the brain**, invoked headlessly at every judgment/creative point via the
  `claude` CLI.

Confirmed `claude` CLI capabilities we rely on:
- `-p/--print` + `--output-format json` → structured output rl parses (envelope
  `{type, is_error, result}`; `result` = the model's raw text).
- `--agent <name>` / `--agents <json>` → activate a staffed specialist.
- `--mcp-config` → inject MCP servers (Context7, Playwright, our graph MCP).
- `--model`, `--effort`, `--permission-mode`, `--allowedTools`, `--append-system-prompt`.

**Fresh context per activation is the default** (Ralph fidelity): each `claude -p` is a
stateless brain activation; continuity lives in files (`.ralph/`, `specs/`, git) and the
graph KB — **not** via `--resume`.

---

## 4. The pipeline (entry point `rl run "<challenge>"`)

```
rl run "fazer um jogo estilo wow"
  │  bootstrap: .ralph/prd.json + .ralph/progress.md (+ self-ignoring .ralph/.gitignore)
  ├─ ⓪ STAFF      brain → team roster JSON → csdd agent/skill create   [REAL, soft-fail]
  ├─ ① DECOMPOSE  brain → feats + deps → .ralph/prd.json → graph       [REAL]
  └─ OUTER LOOP per feat (dependency order, status != done):
       ├─ ② RESEARCH   brain + WebSearch/Context7 + graph MCP
       │                → facts indexed into our graph KB              [REAL, soft-fail]
       ├─ ③ SPEC-UP    per csdd phase (requirements→design→tasks):
       │                brain authors via csdd init/generate/validate
       │                → reviewer brain judges vs general context
       │                → refine loop on reject → csdd approve         [REAL]
       ├─ ③b APPROVE   final latch: csdd validate green +
       │                ready_for_implementation                       [REAL]
       ├─ ④ BUILD      INNER LOOP: RED+GREEN per task, gate csdd
       │                validate + git revert on fail                  [REAL]
       ├─ ⑤ E2E        brain e2e-qa + Playwright MCP → verdict JSON     [REAL]
       └─ feat.status = done → append progress.md → project graph → next feat
  └─ all feats done → program complete
```

Note on ③/③b: csdd gates each artifact on the previous phase's approval, so
authoring and approval necessarily interleave per phase. The D8 contextual
review (reviewSpec) therefore runs per phase INSIDE spec-up, before each
`csdd spec approve`; ③b is the final mechanical latch.

`rl run` dispatches: an existing directory containing `spec.json` → the **inner loop**
(debug one feat); anything else → a natural-language **challenge** → the full manager.

### 4.1 The inner loop (BUILT — `internal/loop`)

One iteration = **one behavior unit** = a **RED+GREEN pair** (or a single non-TDD leaf).
Never commits a lone failing test. Flow:
1. Reload spec; `NextUnit()` picks the next actionable unit (dependency-aware).
2. Build a fresh-context prompt (scope = exactly this unit; boundary; requirements; use
   MCP context layer; the AI marks the checkboxes and commits).
3. Spawn `claude` fresh; the AI implements, ticks the tasks.md boxes, commits.
4. **verify** (driver = the atomic judge): HEAD moved (AI committed) + clean tree + the
   scoped checkboxes ticked + `csdd spec validate` green.
5. Pass → next unit. Fail → `git reset --hard` to the pre-iteration snapshot, un-does the
   iteration entirely, retry with fresh context (bounded by `--retry`); persistent failure
   → stop for review.

Decision: **the AI owns the checkbox + commit** (Ralph-style); the driver verifies and
reverts. (D5.)

### 4.2 Two Definition-of-Done tiers

| Tier | Who proves | What it proves | How |
|------|-----------|----------------|-----|
| **Tier 1 — "built to contract"** | csdd (inner, per task) | code satisfies the contract | unit/TDD tests + `csdd spec validate` + boundaries |
| **Tier 2 — "delivered & works"** | **ralph-loop (outer, per feat)** | the feat actually works end-to-end | E2E driving the real app (Playwright) vs. PRD acceptance criteria |

A feat flips to `done` **only after Tier 2 passes**. csdd's `tdd-e2e` flow already puts an
E2E task in Phase 4; ralph-loop runs it for real as the delivery proof.

---

## 5. Fully autonomous — no human in the loop

The human only receives the final delivered result. The SDD human-approval gate becomes an
**autonomous approval gate**: ralph approves based on the **general context** (challenge,
PRD, sibling feats, research/graph KB) **and** what was created for the spec = a brain
**contextual review** (`reviewSpec`) + csdd's mechanical `spec approve` (which re-validates).
Both must pass; a negative verdict routes back to refinement, not a human pause.

**Self-improving via adversarial self-review:** agents analyze each other's output
(spec-reviewer reviews spec-up; csdd code-reviewer/security-reviewer review the build; e2e-qa
judges delivery); reasons feed back → the producer refines → retry. The system improves over
iterations because knowledge accumulates (`.ralph/progress.md`, the graph KB, `AGENTS.md`)
and the team can evolve.

---

## 6. The team (staffing) — csdd is the factory

Before research/spec, ralph-loop **staffs a bespoke team** for the challenge. Example for a
WoW-like game: a **tech-lead (TL)** orchestrator, a **javascript-game-dev** specialist, a
**game-systems** specialist, a **netcode** specialist (later feats), an **e2e-qa** agent,
plus csdd's built-in **code-reviewer** / **security-reviewer**; and skills like
`game-scaffold`, `ecs-entity`, `playwright-scene-verify`.

- **rl = the staffer** (decides the roster from the challenge + initial research).
- **csdd = the factory** — `csdd agent create <name> --tools ...`, `csdd skill create <name>`
  materialize the team into `.claude/agents/` + `.claude/skills/` (least-privilege,
  per-role model/effort).
- Specialists are created **with domain context baked in** (references to Context7-indexed
  docs + graph-KB search hints) → precise, not generic.
- **Elegant closure:** rl writes the team via csdd; Claude reads it natively (`--agent` /
  auto-discovery). Staffing literally reshapes how the brain thinks in later phases.

Orchestration hierarchy: **rl (program manager) → TL agent (per-challenge orchestrator) →
specialist agents/skills.**

---

## 7. The graph knowledge base (`internal/graph`)

Our own **local, embedded** temporal knowledge graph — pure-Go SQLite
(`modernc.org/sqlite`, **no cgo, no Docker, no service**), modeled on Graphiti
(getzep/graphiti — see `docs/getzep-graphiti.md` for the blueprint). It is ralph-loop's
**single knowledge base** and the project's **living documentation graph**.

### 7.1 Bi-temporal model (Graphiti's four dates)

- `valid_at` — when the fact became true in the real world.
- `invalid_at` — when it ceased to be true (null = still true).
- `created_at` — when we first wrote it (system time, immutable).
- `expired_at` — when the system recorded it as superseded (soft-delete marker).
- A fact is **current** iff `expired_at IS NULL`. **Nothing is ever deleted** — a superseded
  fact gets `invalid_at` + `expired_at` set and leaves the current view but survives
  "as-of" queries. This is the differentiating feature.

The store is **mechanical**: in Graphiti an LLM extracts entities/edges; in ralph-loop the
**brain** does that and calls our tools. The store persists, dedupes (normalized text),
supersedes, and searches.

### 7.2 Schema & ops (BUILT + tested)

Tables: `entity_nodes` (dedup by normalized name within group+kind), `episodic_nodes`
(raw content/provenance), `entity_edges` (bi-temporal). Ops: `UpsertEntity`, `AddEpisode`,
`AddFact` (dedup fast-path + `Supersedes` → temporal invalidation), `InvalidateFact`,
`SearchNodes` / `SearchFacts` (LIKE for now), `Neighbors` (recursive-CTE BFS over current
edges), `FactsAsOf` (valid-time point-in-time query).

### 7.3 Living documentation graph (the intended use)

The graph doubles as **auto-maintained project documentation** — structure + rationale +
evolution — unifying temporal memory (Graphiti-style) and code-structure (codegraph-style).
It **auto-populates from the pipeline** so docs never rot.

Ontology:

```
NODE kinds:  program, feat, spec, requirement, component, file,
             decision/adr, test, agent, skill, concept
RELATIONS:   feat ─DEPENDS_ON→ feat        spec ─HAS_REQ→ requirement
             feat ─HAS_COMPONENT→ component  component ─DEPENDS_ON→ component
             task ─TRACES_TO→ requirement    file ─IMPLEMENTS→ component
             test ─VERIFIES→ requirement     decision ─JUSTIFIES→ component|feat
             agent ─WORKED_ON→ component      concept ─RELATES_TO→ concept
```

Population by phase: decompose→`feat`+`DEPENDS_ON`; spec-up→`requirement`/`component`+
`HAS_REQ`/`HAS_COMPONENT`/`TRACES_TO`; build→`file IMPLEMENTS`, `test VERIFIES`, learnings as
temporal facts; research→`concept`+`RELATES_TO`; decisions→`JUSTIFIES`.

Consulted by **agents** (via the graph MCP: `search_facts`/`neighbors`) and **humans** (via
`rl graph export` → Mermaid/DOT — architecture drawn from reality, not a diagram that rots).

---

## 8. Memory layering

| Horizon | Where |
|---------|-------|
| Relational + temporal + domain/external + research content | **our graph KB** (`internal/graph`) |
| Project always-on | csdd steering (`.claude/steering/`) |
| Per-module | `AGENTS.md` (auto-read by the brain) |
| Per-iteration learnings | `.ralph/progress.md` + tasks.md Implementation Notes |
| Program (feats) | `.ralph/prd.json` |

**MCP layer (the brain's organs):** **Context7** (fetch external docs) + **Playwright**
(E2E hands/eyes) + **our graph MCP** (the single KB). context-mode is NOT used.

---

## 9. Decisions log

- **D1.** Single per-feat state tracker = csdd `spec.json` + `tasks.md`; no separate
  per-task tracker. Program-level tracker = our `.ralph/prd.json` (feats).
- **D2.** Quality gate = the csdd contract (`spec validate`, exit 2 = violation) + tests,
  not an ad-hoc check string.
- **D3.** Ralph's bash is only a methodology reference — reimplement natively in Go.
- **D4.** csdd integration = **binary-as-oracle** now (shell out); evolve to in-process Go
  package import once csdd exposes public packages (its core is under `internal/` today).
- **D5.** Inner-loop unit = **RED+GREEN pair**; **the AI owns the checkbox + commit**, the
  driver verifies the gate and reverts on failure (atomic all-or-nothing).
- **D6.** Orchestrator stack = **Go** (matches csdd; possible future csdd subcommand).
- **D7.** **No human in the loop** — fully autonomous end-to-end; human gets only the final
  result. SDD human approval → autonomous approval (contextual brain review + csdd approve).
- **D8.** Autonomous approval judges the **general context + what was created for the spec**
  (`reviewSpec`), not just mechanical validation.
- **D9.** Research uses **Context7 AND open web search**, indexed into our graph KB.
- **D10.** Graph memory layer: chose **Graphiti's model**, but... (see D11–D13).
- **D11.** **No Docker / no external service** is a hard constraint. Every viable Graphiti
  backend needs Docker/a service (FalkorDB, Neo4j) or is dead (Kuzu, archived Oct 2025 after
  Apple acquisition) → **Graphiti the tool is dropped**.
- **D12.** Build **our own** graph in Go, embedded SQLite (pure-Go, no cgo), bi-temporal,
  mirroring Graphiti's proven model. (`internal/graph`.)
- **D13.** **context-mode dropped** — our graph KB replaces its FTS5 KB (more powerful,
  temporal); its context-window protection is covered by our fresh-context design; its
  session continuity by our file-based state. Tradeoff: lose auto tool-output redirection
  (mitigated by short focused iterations + index-then-search).
- **D14.** The graph KB doubles as the **living documentation graph** of the project,
  auto-populated by the pipeline.

---

## 10. Current state

**ALL PHASES ARE REAL — no seams remain.** Green `go build/vet/test`:
- `internal/spec` — parses csdd `spec.json` + `tasks.md` into a task tree; `NextActionable`
  (dependency-aware) + `NextUnit` (RED+GREEN pairing).
- `internal/tool` — fresh-process AI runner; `Run` (agentic) + `RunJSON` (structured
  brain activation via `claude -p --output-format json`) + `ExtractJSON`.
- `internal/gate` — csdd binary-as-oracle: `Validate`, `Status`, `Approve`, plus `RunIn`
  (factory surface: `agent create` / `skill create`, cwd-scoped) and `Command`.
- `internal/gitx` — `Head`, `ResetHard`, `HeadMoved`, `Dirty` for atomic revert.
- `internal/loop` — the inner build loop (verify + revert + retry).
- `internal/program` — `.ralph/prd.json` + `.ralph/progress.md`; Feat/Status; `NextFeat`
  (dep-aware); `Bootstrap` (writes a self-ignoring `.ralph/.gitignore` so runtime state
  never dirties the tree the gate checks); `Slug`.
- `internal/graph` — the graph KB store (bi-temporal, tested) + **ontology constants**
  (§7.3), **`ProjectPRD`** (prd.json → program/feat nodes, HAS_FEAT/DEPENDS_ON edges,
  status as temporal facts that supersede) and **`Export`** (Mermaid/DOT).
- `internal/mcp` — dependency-free MCP stdio server (JSON-RPC 2.0) exposing the graph:
  `add_fact` (upserts endpoints by kind+name), `add_episode`, `upsert_entity`,
  `search_nodes`, `search_facts`, `neighbors`, `facts_as_of`.
- `internal/manager` — the outer loop, all phases wired (phases.go) + brain plumbing
  (brain.go: `writeMCPConfig`, `brainJSON`, `programContext`, `projectGraph`).
- `main.go` — `rl plan`, `rl run`, `rl graph export [--format mermaid|dot]`,
  `rl graph mcp` (alias `rl graph-mcp`).

**Validated end-to-end offline:** `internal/manager/pipeline_test.go` drives
`manager.Run` through every phase against fake `claude`/`csdd` binaries: staff →
decompose → research → spec-up with one review rejection (proving the refine loop) →
approve → build (RED+GREEN, git-gated) → E2E → done, with the graph projection
following each status change. Earlier live validation: `① decompose` on the real
brain produced 8 correctly-ordered feats.

**Phase status:** ⓪ staff = REAL (soft-fail) · ① decompose = REAL · ② research = REAL
(soft-fail) · ③ spec-up = REAL (author→review→approve per csdd phase, refine loop) ·
③b approve = REAL (final latch) · ④ build = REAL · ⑤ E2E = REAL.

---

## 11. Roadmap — what needs to be done

**Done (2026-07-03):** roadmap items 1–6 + housekeeping — graph ontology/projector/
`rl graph export`, the graph MCP server (`rl graph mcp`), real ② research + ③ spec-up
(with per-phase reviewSpec + refine loop, covering the spec-up half of item 8),
③b final latch, ⑤ E2E, ⓪ staff, `.gitignore`/README, plus an offline full-pipeline
integration test. Nothing committed yet (still on `main`).

Remaining, highest leverage first:

1. **Live run against the real brain + csdd.** The pipeline is proven against fakes;
   drive one small real challenge end-to-end (`rl run "..." --csdd "npx -y @protonspy/csdd"`)
   and fix whatever friction the real `claude`/csdd surfaces (flag drift, prompt tuning,
   csdd template expectations for requirements/design/tasks content).
2. **Refine loops, remaining half:** on a negative ⑤ E2E verdict, feed the reasons back
   into ④ build (or ③ spec-up when the spec is wrong) and retry bounded — today E2E
   failure stops the feat. Same for repeated build-gate failures.
3. **Graph enrichments:** full-text (FTS5) + vector/embeddings search (for research
   content), deeper projector (requirements/components/files as spec-up/build produce
   them — the brain already writes these via MCP; project the csdd artifacts too).
4. **Staffed activations:** pass `--agent <name>` (researcher/tech-lead/e2e-qa from ⓪)
   to the corresponding activations once agent quality is validated live.
5. **Housekeeping:** consider making `rl` a csdd subcommand; `go build` release; commit.

---

## 12. Code layout

```
ralph-loop/
├── ARCHITECTURE.md          # this document
├── go.mod                   # module github.com/protonspy/ralph-loop (go 1.26)
├── main.go                  # rl plan | rl run | rl graph export | rl graph mcp
├── docs/                    # external reference dumps (DeepWiki): csdd, ralph,
│                            #   context-mode, getzep-graphiti (graph blueprint)
└── internal/
    ├── spec/                # parse csdd spec.json + tasks.md; NextUnit (RED+GREEN)
    ├── program/             # .ralph/prd.json + progress.md; feats; NextFeat
    ├── manager/             # OUTER loop, all phases real (manager.go, phases.go,
    │                        #   brain.go: MCP wiring + JSON activations; pipeline_test.go)
    ├── loop/                # INNER build loop (loop.go, prompt.go, helpers.go)
    ├── tool/                # claude runner: Run + RunJSON (structured)
    ├── gate/                # csdd as oracle: validate/status/approve + RunIn (factory)
    ├── gitx/                # git snapshot/revert for atomic iterations
    ├── graph/               # temporal graph KB (SQLite, bi-temporal) + ontology,
    │                        #   ProjectPRD projector, Mermaid/DOT Export
    └── mcp/                 # dependency-free MCP stdio server over the graph
```

## 13. How to run / dev

```
go build ./... && go vet ./... && go test ./...

# plan a single csdd spec (debug)
rl plan <spec-dir>

# full pipeline from a challenge (csdd not on PATH → use npx)
rl run "fazer um jogo estilo wow" --csdd "npx -y @protonspy/csdd"

# see the end-to-end plan without spawning/mutating
rl run "<challenge>" --dry-run

# living documentation graph: render / serve to agents
rl graph export [--root .] [--format mermaid|dot]
rl graph mcp    [--root .] [--group NAME]     # stdio MCP server (alias: rl graph-mcp)
```

Environment notes: `claude` on PATH (brain, default tool); `amp` optional; csdd via
`npx -y @protonspy/csdd` (not installed globally here); `jq`, `git` available.

## 14. Open questions

- Graphiti extraction needs an LLM; our store is mechanical (brain extracts). Confirm the
  brain-side extraction prompts/tools when wiring research.
- LLM for any future embedding/rerank in the graph — reuse Anthropic vs local (Ollama).
- When to promote csdd `internal/` packages to public so `rl` can import them (D4).
- ~~Exact `.mcp.json` shape for registering `rl graph-mcp` as a stdio MCP server.~~
  Resolved: rl writes `.ralph/mcp-graph.json` / `.ralph/mcp-e2e.json` per activation
  (`{"mcpServers":{"graph":{"command":"<rl>","args":["graph","mcp","--root","<root>"]}}}`,
  E2E adds `playwright` via `npx -y @playwright/mcp@latest`) and passes `--mcp-config`.
```
