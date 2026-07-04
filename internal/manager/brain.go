package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/protonspy/ralph-loop/internal/graph"
	"github.com/protonspy/ralph-loop/internal/loop"
	"github.com/protonspy/ralph-loop/internal/program"
	"github.com/protonspy/ralph-loop/internal/spec"
	"github.com/protonspy/ralph-loop/internal/tool"
)

// This file is the plumbing between the deterministic body and the brain:
// MCP wiring (which organs a given activation gets) and the JSON activation
// helper every brain phase goes through.

// writeMCPConfig materializes an --mcp-config file under .ralph/ wiring the graph
// KB — ralph-loop's OWN native MCP (served by this same rl binary over stdio),
// which needs no external setup. The graph is the ONLY MCP ralph wires; anything
// carrying its own setup cost (Context7's token, Playwright's browser deps) is
// deliberately left out.
func writeMCPConfig(root string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate rl binary for graph MCP: %w", err)
	}
	servers := map[string]any{
		"graph": map[string]any{"command": exe, "args": []string{"graph", "mcp", "--root", root}},
	}
	raw, err := json.MarshalIndent(map[string]any{"mcpServers": servers}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(program.Dir(root), 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(program.Dir(root), "mcp-graph.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// mcpGuidance describes the MCP servers a brain activation can use: the native
// graph (always wired) plus any recognized OPTIONAL server the user configured
// in .mcp.json. ralph only VALIDATES presence here — it never installs MCPs
// (that is the user's job via `csdd mcp add`/`enable`), and the prompt mentions
// only what is actually available.
func mcpGuidance(root string) string {
	hints := []string{graphToolsHint}
	if mcpConfigured(root, "context7") {
		hints = append(hints, context7Hint)
	}
	return strings.Join(hints, "\n\n")
}

// mcpConfigured reports whether a server named `name` is present in the
// workspace's .mcp.json (the user's MCP config, managed via `csdd mcp`).
func mcpConfigured(root, name string) bool {
	raw, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		return false
	}
	var parsed struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal(raw, &parsed) != nil {
		return false
	}
	_, ok := parsed.MCPServers[name]
	return ok
}

// brainArgs assembles the common activation flags: the graph/E2E MCP config and,
// when a bespoke specialist was staffed for the role, --agent to activate it.
func brainArgs(cfg, agent string) []string {
	var args []string
	if cfg != "" {
		args = append(args, "--mcp-config", cfg)
	}
	if agent != "" {
		args = append(args, "--agent", agent)
	}
	return args
}

// pickAgent returns the staffed agent whose slug contains any of the role
// keywords (first keyword, then first team match wins), or "" to fall back to
// the default brain. This is how ⓪ staffing actually reshapes later phases:
// research/spec-up/review/E2E activate their matching specialist.
func pickAgent(team []string, keywords ...string) string {
	for _, kw := range keywords {
		for _, a := range team {
			if strings.Contains(strings.ToLower(a), kw) {
				return a
			}
		}
	}
	return ""
}

// brainJSON is one structured brain activation: fresh context, headless JSON
// output, decoded into out. extraArgs typically carry --mcp-config / --agent.
func brainJSON(ctx context.Context, o Options, prompt string, out any, extraArgs ...string) error {
	raw, err := o.Tool.RunJSON(ctx, o.Root, prompt, extraArgs...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(tool.ExtractJSON(raw)), out); err != nil {
		return fmt.Errorf("parse brain JSON: %w\n--- model said ---\n%s", err, raw)
	}
	return nil
}

// programContext renders the shared preamble every per-feat activation gets:
// the challenge and the feat map with statuses — the "general context" of D8.
func programContext(p *program.PRD, feat *program.Feat) string {
	var b strings.Builder
	fmt.Fprintf(&b, "PROGRAM: %s\nCHALLENGE: %s\n", p.Program, p.Challenge)
	b.WriteString("FEATS (dependency order, with status):\n")
	for _, f := range p.Feats {
		marker := "  "
		if feat != nil && f.ID == feat.ID {
			marker = "→ "
		}
		fmt.Fprintf(&b, "%s[%s] %s — %s (deps: %s)\n", marker, f.Status, f.ID, f.Title, strings.Join(f.Depends, ", "))
	}
	return b.String()
}

// graphToolsHint tells an activation how to use the knowledge graph organs.
const graphToolsHint = `KNOWLEDGE GRAPH (MCP server "graph" — the project's living memory):
  - BEFORE working: mcp__graph__search_facts / search_nodes with your key terms,
    and mcp__graph__neighbors around this feat, to reuse what is already known.
  - AFTER learning something durable: mcp__graph__add_episode for the raw source,
    then mcp__graph__add_fact (src/dst as kind+name, one sentence per fact,
    episode = the uuid you got). If a fact contradicts an existing one, pass its
    uuid in supersedes — never assume deletion.`

// context7Hint is added ONLY when the user configured context7 in .mcp.json.
const context7Hint = `LIBRARY DOCS (MCP server "context7" — the user configured it in .mcp.json):
  - Fetch authoritative, version-correct library/API documentation via context7
    before relying on an API, instead of guessing from memory.`

// projectGraph refreshes the living documentation graph from the program state.
// Best-effort by design: the graph is documentation, not a gate.
func projectGraph(o Options, p *program.PRD) {
	store, err := graph.Open(graph.DBPath(o.Root), p.Program)
	if err != nil {
		fmt.Fprintf(o.Out, "  ⚠ graph: %v\n", err)
		return
	}
	defer store.Close()
	if err := graph.ProjectPRD(store, p); err != nil {
		fmt.Fprintf(o.Out, "  ⚠ graph projection: %v\n", err)
	}
}

// projectSpecGraph mirrors an approved feat's csdd contract (requirements,
// components, traceability) into the living documentation graph. Best-effort,
// like projectGraph: the graph is documentation, not a gate.
func projectSpecGraph(o Options, p *program.PRD, feat *program.Feat) error {
	s, err := spec.Load(filepath.Join(o.Root, feat.Spec))
	if err != nil {
		return err
	}
	store, err := graph.Open(graph.DBPath(o.Root), p.Program)
	if err != nil {
		return err
	}
	defer store.Close()
	return graph.ProjectSpec(store, specProjection(feat.ID, s))
}

// projectBuildUnit records what one gate-passed build iteration produced into
// the graph: impl files IMPLEMENT the unit's component, test files VERIFY its
// requirements. Best-effort — the graph is documentation, not a gate.
func projectBuildUnit(o Options, p *program.PRD, feat *program.Feat, u loop.UnitResult) {
	comp := unitComponent(u.Tasks)
	reqs := unitRequirements(u.Tasks)
	var impl, tests []string
	for _, f := range u.Files {
		if strings.HasPrefix(f, feat.Spec) {
			continue // contract artifacts (e.g. the tasks.md checkbox tick), not code
		}
		if isTestFile(f) {
			tests = append(tests, f)
		} else {
			impl = append(impl, f)
		}
	}
	if comp == "" && len(reqs) == 0 {
		return // nothing to link
	}
	store, err := graph.Open(graph.DBPath(o.Root), p.Program)
	if err != nil {
		fmt.Fprintf(o.Out, "  ⚠ graph build projection: %v\n", err)
		return
	}
	defer store.Close()
	if err := graph.ProjectBuildUnit(store, graph.BuildUnit{
		FeatID: feat.ID, Component: comp, Requirements: reqs, ImplFiles: impl, TestFiles: tests,
	}); err != nil {
		fmt.Fprintf(o.Out, "  ⚠ graph build projection: %v\n", err)
	}
}

func unitComponent(tasks []*spec.Task) string {
	for _, t := range tasks {
		if t.Boundary != "" {
			return t.Boundary
		}
	}
	return ""
}

func unitRequirements(tasks []*spec.Task) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tasks {
		for _, r := range t.Requirements {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	return out
}

// isTestFile classifies a changed path as a test via cross-language conventions.
func isTestFile(path string) bool {
	lp := strings.ToLower(path)
	b := strings.ToLower(filepath.Base(path))
	switch {
	case strings.HasSuffix(b, "_test.go"),
		strings.Contains(b, ".test."),
		strings.Contains(b, ".spec."),
		strings.HasPrefix(b, "test_"),
		strings.Contains(lp, "/tests/"),
		strings.Contains(lp, "/test/"),
		strings.Contains(lp, "__tests__"):
		return true
	}
	return false
}

// specProjection extracts the graph-projectable slice of a parsed spec: the
// requirements it references, the components (task boundaries) it introduces,
// and the component→requirement traces those tasks encode.
func specProjection(featID string, s *spec.Spec) graph.SpecProjection {
	var reqs, comps []string
	var traces []graph.ReqTrace
	seenReq, seenComp := map[string]bool{}, map[string]bool{}
	for _, leaf := range s.Leaves() {
		comp := leaf.Boundary
		if comp != "" && !seenComp[comp] {
			seenComp[comp] = true
			comps = append(comps, comp)
		}
		for _, r := range leaf.Requirements {
			if !seenReq[r] {
				seenReq[r] = true
				reqs = append(reqs, r)
			}
			if comp != "" {
				traces = append(traces, graph.ReqTrace{Component: comp, Requirement: r})
			}
		}
	}
	return graph.SpecProjection{FeatID: featID, Requirements: reqs, Components: comps, Traces: traces}
}
