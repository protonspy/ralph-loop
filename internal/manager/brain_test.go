package manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/protonspy/ralph-loop/internal/gate"
	"github.com/protonspy/ralph-loop/internal/program"
)

func testProgram() (*program.PRD, *program.Feat) {
	p := &program.PRD{
		Program:   "wow-game",
		Challenge: "fazer um jogo estilo wow",
		Feats: []*program.Feat{
			{ID: "walking-skeleton", Title: "Playable canvas", Spec: "specs/walking-skeleton", Status: program.StatusDone},
			{ID: "tile-map", Title: "Tile map + movement", Spec: "specs/tile-map", Depends: []string{"walking-skeleton"}, Status: program.StatusPlanned},
		},
	}
	return p, p.Feats[1]
}

func TestWriteMCPConfig(t *testing.T) {
	root := t.TempDir()
	path, err := writeMCPConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("config is not valid JSON: %v\n%s", err, raw)
	}
	// The graph is the ONLY MCP ralph wires — no playwright, no context7.
	if len(cfg.MCPServers) != 1 {
		t.Fatalf("want exactly the graph server, got %v", cfg.MCPServers)
	}
	g, ok := cfg.MCPServers["graph"]
	if !ok {
		t.Fatalf("config missing graph server: %s", raw)
	}
	// Claude Code requires the stdio type on a command-based server.
	if g.Type != "stdio" {
		t.Errorf("graph server type = %q, want stdio", g.Type)
	}
	if len(g.Args) != 4 || g.Args[0] != "graph" || g.Args[1] != "mcp" || g.Args[3] != root {
		t.Errorf("graph server args = %v", g.Args)
	}
}

func TestProgramContextMarksCurrentFeat(t *testing.T) {
	p, feat := testProgram()
	ctx := programContext(p, feat)
	for _, want := range []string{"fazer um jogo estilo wow", "→ [planned] tile-map", "[done] walking-skeleton"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("programContext missing %q:\n%s", want, ctx)
		}
	}
}

func TestPromptsCarryTheirContracts(t *testing.T) {
	p, feat := testProgram()
	o := Options{Csdd: gate.Resolver{"npx", "-y", "@protonspy/csdd"}}

	// csdd's order is design → requirements → tasks, so `spec init` lives in the
	// FIRST (design) phase, not requirements.
	design := authorPrompt(o, p, feat, "design", "", graphToolsHint)
	for _, want := range []string{
		"npx -y @protonspy/csdd spec init tile-map",
		"--artifact design",
		"specs/tile-map/design.md",
	} {
		if !strings.Contains(design, want) {
			t.Errorf("authorPrompt(design) missing %q", want)
		}
	}

	author := authorPrompt(o, p, feat, "requirements", "fix the acceptance criteria", graphToolsHint)
	for _, want := range []string{
		"--artifact requirements",
		"specs/tile-map/requirements.md",
		"EARS",
		"REVIEWER FEEDBACK",
		"fix the acceptance criteria",
		"mcp__graph__search_facts",
	} {
		if !strings.Contains(author, want) {
			t.Errorf("authorPrompt(requirements) missing %q", want)
		}
	}
	tasks := authorPrompt(o, p, feat, "tasks", "", graphToolsHint)
	for _, want := range []string{"--artifact tasks", "RED/GREEN", "E2E task"} {
		if !strings.Contains(tasks, want) {
			t.Errorf("authorPrompt(tasks) missing %q", want)
		}
	}
	if strings.Contains(tasks, "REVIEWER FEEDBACK") {
		t.Error("authorPrompt without feedback must not carry a feedback section")
	}

	review := reviewPrompt(p, feat, "design", graphToolsHint)
	for _, want := range []string{"specs/tile-map/design.md", `"approve"`, "general context", "scope creep"} {
		if !strings.Contains(strings.ToLower(review), strings.ToLower(want)) {
			t.Errorf("reviewPrompt missing %q", want)
		}
	}

	e2e := e2ePrompt(p, feat)
	for _, want := range []string{"specs/tile-map/requirements.md", `"passed"`, "Bash"} {
		if !strings.Contains(e2e, want) {
			t.Errorf("e2ePrompt missing %q", want)
		}
	}
}

func TestMCPGuidanceValidatesContext7(t *testing.T) {
	root := t.TempDir()

	// No .mcp.json → graph only, no context7 mention.
	g := mcpGuidance(root)
	if !strings.Contains(g, "graph") {
		t.Error("guidance should always describe the native graph MCP")
	}
	if strings.Contains(g, "context7") {
		t.Errorf("context7 must not be mentioned when absent from .mcp.json:\n%s", g)
	}

	// User configured context7 in .mcp.json → it is validated in and described.
	cfg := `{"mcpServers":{"context7":{"type":"http","url":"https://mcp.upstash.com/context7"}}}`
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if !mcpConfigured(root, "context7") {
		t.Fatal("mcpConfigured should detect context7 in .mcp.json")
	}
	if g := mcpGuidance(root); !strings.Contains(g, "context7") {
		t.Errorf("guidance should describe context7 once configured:\n%s", g)
	}
}

func TestPickAgentMatchesImplementer(t *testing.T) {
	team := []string{"implementer", "code-reviewer", "security-reviewer"}
	if got := pickAgent(team, "implement", "developer", "coder"); got != "implementer" {
		t.Errorf("build activation = %q, want implementer", got)
	}
	if got := pickAgent(team, "research", "analyst"); got != "" {
		t.Errorf("research should fall back to the default brain, got %q", got)
	}
}
