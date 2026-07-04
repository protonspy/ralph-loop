# CLAUDE.md — project operating instructions

This workspace is built autonomously by **ralph-loop**: stateless, fresh-context
iterations over a **csdd** SDD+TDD contract. Each activation has a FRESH context —
everything you need is in files and the knowledge graph, not memory. This file is
the durable, cross-cutting layer every activation inherits; per-task direction
arrives in the iteration's prompt.

## Workspace layout
- `specs/<feat>/` — the csdd contract per feature: `requirements.md` (EARS),
  `design.md`, `tasks.md` (RED/GREEN TDD checklist), `spec.json` (state).
  csdd is the authority: validate with `csdd spec validate <feat>`.
- `.ralph/` — ralph-loop runtime state (program tracker, progress log, graph.db,
  MCP configs). Git-ignored; do not edit by hand.
- `.claude/` — agents/skills and Claude config.

## SDD contract (per feature)
- Author strictly in order: requirements → design → tasks; each phase is validated
  and approved before the next. Requirements are EARS-form, with observable
  acceptance criteria. Tasks are small RED/GREEN pairs, each tracing to requirement
  IDs and carrying a component boundary from the design.
- Scope every change to ONE feature; never leak work into a sibling feature's spec.

## TDD workflow (per task)
- On a RED/GREEN pair: write the failing test FIRST, run it, confirm it fails for
  the EXPECTED reason (not a syntax/import error); then the minimal code to pass;
  refactor under green. Keep the full suite green — no regressions.
- Stay strictly inside the task's declared component boundary; never edit outside it.

## Knowledge graph (MCP server "graph" — the project's living memory)
- BEFORE working, query it (`search_facts` / `search_nodes` / `neighbors`) to reuse
  what is already known instead of guessing.
- AFTER learning something durable, record it: `add_episode` for the raw source,
  then `add_fact` (endpoints as kind+name, one self-contained sentence per fact,
  linked to the episode). If a fact contradicts an existing one, pass its uuid in
  `supersedes` — history is temporal; never assume deletion.

## Commits
- Conventional Commits (`feat:`, `fix:`, `docs:`, `chore:`, `test:`). One behavior per commit.
- Commit ALL your changes; never leave the working tree dirty. Never commit on a red suite.
- Do NOT push.
