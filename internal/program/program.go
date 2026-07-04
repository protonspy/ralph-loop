// Package program is ralph-loop's OWN state layer — the altitude above csdd.
// It tracks a challenge decomposed into feats (each feat = one csdd spec) in
// .ralph/prd.json, plus a .ralph/progress.md learnings log. This is Ralph's
// prd.json/progress.txt idea moved up one level: the tracker's item is a whole
// feature, whose implementation is delegated to the csdd contract.
package program

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Status is a feat's lifecycle stage.
type Status string

const (
	StatusPlanned       Status = "planned"        // decomposed, no spec yet
	StatusSpecGenerated Status = "spec_generated" // csdd artifacts authored, not approved
	StatusApproved      Status = "approved"       // ready_for_implementation
	StatusImplementing  Status = "implementing"   // inner build loop running
	StatusDone          Status = "done"           // built AND E2E-accepted
)

// Feat is one unit of the program: a feature that maps 1:1 to a csdd spec.
type Feat struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Spec    string   `json:"spec"`    // specs/<id>, relative to root
	Depends []string `json:"depends"` // feat IDs that must be done first
	Status  Status   `json:"status"`
}

// PRD is the whole program: the challenge and its feats.
type PRD struct {
	Program   string   `json:"program"`
	Challenge string   `json:"challenge"`
	PRDPath   string   `json:"prd,omitempty"`  // path to the full PRD markdown, once written
	Branch    string   `json:"branch"`
	Team      []string `json:"team,omitempty"` // staffed agent slugs (⓪), activated via --agent in later phases
	Feats     []*Feat  `json:"feats"`
}

// Dir returns the .ralph directory for a workspace root.
func Dir(root string) string { return filepath.Join(root, ".ralph") }

// Path returns the .ralph/prd.json path.
func Path(root string) string { return filepath.Join(Dir(root), "prd.json") }

// ProgressPath returns the .ralph/progress.md path.
func ProgressPath(root string) string { return filepath.Join(Dir(root), "progress.md") }

// Exists reports whether a program has been bootstrapped at root.
func Exists(root string) bool {
	_, err := os.Stat(Path(root))
	return err == nil
}

// Load reads .ralph/prd.json.
func Load(root string) (*PRD, error) {
	raw, err := os.ReadFile(Path(root))
	if err != nil {
		return nil, err
	}
	var p PRD
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("parse %s: %w", Path(root), err)
	}
	return &p, nil
}

// Save writes .ralph/prd.json atomically-ish (write temp, rename).
func Save(root string, p *PRD) error {
	if err := os.MkdirAll(Dir(root), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := Path(root) + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, Path(root))
}

// Bootstrap creates .ralph/ and an initial prd.json for a challenge, with no
// feats yet (decomposition fills them). It is a no-op-safe: if a program already
// exists it is loaded and returned unchanged.
func Bootstrap(root, challenge string) (*PRD, error) {
	if Exists(root) {
		return Load(root)
	}
	slug := Slug(challenge)
	p := &PRD{
		Program:   slug,
		Challenge: challenge,
		Branch:    "ralph/" + slug,
		Feats:     []*Feat{},
	}
	if err := Save(root, p); err != nil {
		return nil, err
	}
	header := fmt.Sprintf("# Ralph-Loop Progress — %s\n\nChallenge: %s\n\n---\n", slug, challenge)
	if err := os.WriteFile(ProgressPath(root), []byte(header), 0o644); err != nil {
		return nil, err
	}
	// .ralph is runtime state (prd, progress, graph.db, mcp configs) — make the
	// directory self-ignoring so it never dirties the workspace's git status,
	// which the build gate checks after every iteration.
	if err := os.WriteFile(filepath.Join(Dir(root), ".gitignore"), []byte("*\n"), 0o644); err != nil {
		return nil, err
	}
	// ralph-loop scaffolds the workspace ITSELF instead of running `csdd init`:
	// a .claude/ directory (the anchor csdd uses to recognize a workspace) and a
	// self-contained CLAUDE.md carrying every durable instruction. Both are
	// create/write-if-absent, so a user-provided csdd workspace or CLAUDE.md wins.
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		return nil, err
	}
	if err := EnsureCLAUDEMD(root); err != nil {
		return nil, err
	}
	return p, nil
}

// WritePRDDoc renders the decomposed program as a human-readable PRD markdown at
// .ralph/prd.md and returns its root-relative path (for PRD.PRDPath).
func WritePRDDoc(root string, p *PRD, summary string) (string, error) {
	if err := os.MkdirAll(Dir(root), 0o755); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# PRD — %s\n\n**Challenge:** %s\n\n", p.Program, p.Challenge)
	if s := strings.TrimSpace(summary); s != "" {
		fmt.Fprintf(&b, "## Summary\n\n%s\n\n", s)
	}
	b.WriteString("## Feats (dependency order)\n\n")
	for _, f := range p.Feats {
		deps := "—"
		if len(f.Depends) > 0 {
			deps = strings.Join(f.Depends, ", ")
		}
		fmt.Fprintf(&b, "- **%s** — %s _(depends: %s)_\n", f.ID, f.Title, deps)
	}
	if err := os.WriteFile(filepath.Join(Dir(root), "prd.md"), []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return filepath.Join(".ralph", "prd.md"), nil
}

// EnsureCLAUDEMD writes a fallback CLAUDE.md holding ralph-loop's durable,
// cross-cutting conventions — but only if one does not already exist. Since
// `csdd init` now owns a consolidated CLAUDE.md (csdd PR #38, which also removed
// csdd.md), this is the fallback for when csdd init was unavailable or skipped.
func EnsureCLAUDEMD(root string) error {
	path := filepath.Join(root, "CLAUDE.md")
	if _, err := os.Stat(path); err == nil {
		return nil // csdd (or a human) already provided one
	}
	return os.WriteFile(path, []byte(claudeMD), 0o644)
}

// claudeMD is auto-loaded by every `claude` activation (build loop AND csdd
// spec authoring). ralph-loop writes it in lieu of `csdd init`, so it must be
// self-contained: every durable, cross-cutting instruction the brain needs.
// Per-task direction still arrives in each iteration's prompt.
const claudeMD = `# CLAUDE.md — project operating instructions

This workspace is built autonomously by **ralph-loop**: stateless, fresh-context
iterations over a **csdd** SDD+TDD contract. Each activation has a FRESH context —
everything you need is in files and the knowledge graph, not memory. This file is
the durable, cross-cutting layer every activation inherits; per-task direction
arrives in the iteration's prompt.

## Workspace layout
- ` + "`specs/<feat>/`" + ` — the csdd contract per feature: ` + "`requirements.md`" + ` (EARS),
  ` + "`design.md`" + `, ` + "`tasks.md`" + ` (RED/GREEN TDD checklist), ` + "`spec.json`" + ` (state).
  csdd is the authority: validate with ` + "`csdd spec validate <feat>`" + `.
- ` + "`.ralph/`" + ` — ralph-loop runtime state (program tracker, progress log, graph.db,
  MCP configs). Git-ignored; do not edit by hand.
- ` + "`.claude/`" + ` — agents/skills and Claude config.

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
- BEFORE working, query it (` + "`search_facts` / `search_nodes` / `neighbors`" + `) to reuse
  what is already known instead of guessing.
- AFTER learning something durable, record it: ` + "`add_episode`" + ` for the raw source,
  then ` + "`add_fact`" + ` (endpoints as kind+name, one self-contained sentence per fact,
  linked to the episode). If a fact contradicts an existing one, pass its uuid in
  ` + "`supersedes`" + ` — history is temporal; never assume deletion.

## Commits
- Conventional Commits (` + "`feat:`, `fix:`, `docs:`, `chore:`, `test:`" + `). One behavior per commit.
- Commit ALL your changes; never leave the working tree dirty. Never commit on a red suite.
- Do NOT push.
`

// NextFeat returns the first not-done feat whose dependencies are all done.
// The bool reports whether the whole program is complete.
func (p *PRD) NextFeat() (*Feat, bool) {
	done := map[string]bool{}
	for _, f := range p.Feats {
		done[f.ID] = f.Status == StatusDone
	}
	allDone := true
	for _, f := range p.Feats {
		if f.Status == StatusDone {
			continue
		}
		allDone = false
		if depsMet(f, done) {
			return f, false
		}
	}
	return nil, allDone
}

func depsMet(f *Feat, done map[string]bool) bool {
	for _, d := range f.Depends {
		if !done[d] {
			return false
		}
	}
	return true
}

// Find returns the feat with the given ID, or nil.
func (p *PRD) Find(id string) *Feat {
	for _, f := range p.Feats {
		if f.ID == id {
			return f
		}
	}
	return nil
}

// AppendProgress adds a section to .ralph/progress.md. Append-only, like Ralph.
func AppendProgress(root, heading, body string) error {
	f, err := os.OpenFile(ProgressPath(root), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\n## %s\n\n%s\n", heading, strings.TrimSpace(body))
	return err
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slug turns a free-text challenge into a kebab-case program/branch name,
// matching csdd's KebabCheck (^[a-z][a-z0-9]*(-[a-z0-9]+)*$).
func Slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonSlug.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "program"
	}
	// keep it short-ish
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	// must start with a letter
	if s[0] < 'a' || s[0] > 'z' {
		s = "p-" + s
	}
	return s
}
