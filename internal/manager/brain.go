package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/protonspy/ralph-loop/internal/graph"
	"github.com/protonspy/ralph-loop/internal/program"
	"github.com/protonspy/ralph-loop/internal/tool"
)

// This file is the plumbing between the deterministic body and the brain:
// MCP wiring (which organs a given activation gets) and the JSON activation
// helper every brain phase goes through.

// writeMCPConfig materializes an --mcp-config file under .ralph/ giving the
// activation our graph KB (served by this same rl binary over stdio) and,
// for E2E, Playwright as hands/eyes. Rewritten every time: the rl path or root
// may change between runs.
func writeMCPConfig(root string, playwright bool) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate rl binary for graph MCP: %w", err)
	}
	servers := map[string]any{
		"graph": map[string]any{"command": exe, "args": []string{"graph", "mcp", "--root", root}},
	}
	name := "mcp-graph.json"
	if playwright {
		servers["playwright"] = map[string]any{"command": "npx", "args": []string{"-y", "@playwright/mcp@latest"}}
		name = "mcp-e2e.json"
	}
	raw, err := json.MarshalIndent(map[string]any{"mcpServers": servers}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(program.Dir(root), 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(program.Dir(root), name)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return "", err
	}
	return path, nil
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
