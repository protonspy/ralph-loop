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
	for _, playwright := range []bool{false, true} {
		path, err := writeMCPConfig(root, playwright)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var cfg struct {
			MCPServers map[string]struct {
				Command string   `json:"command"`
				Args    []string `json:"args"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatalf("config is not valid JSON: %v\n%s", err, raw)
		}
		g, ok := cfg.MCPServers["graph"]
		if !ok {
			t.Fatalf("config missing graph server: %s", raw)
		}
		if len(g.Args) != 4 || g.Args[0] != "graph" || g.Args[1] != "mcp" || g.Args[3] != root {
			t.Errorf("graph server args = %v", g.Args)
		}
		if _, ok := cfg.MCPServers["playwright"]; ok != playwright {
			t.Errorf("playwright present=%v, want %v", ok, playwright)
		}
	}
	if filepath.Dir(program.Dir(t.TempDir())) == "" {
		t.Fatal("unreachable")
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

	author := authorPrompt(o, p, feat, "requirements", "fix the acceptance criteria")
	for _, want := range []string{
		"npx -y @protonspy/csdd spec init tile-map",
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
	tasks := authorPrompt(o, p, feat, "tasks", "")
	for _, want := range []string{"--artifact tasks", "RED/GREEN", "E2E task"} {
		if !strings.Contains(tasks, want) {
			t.Errorf("authorPrompt(tasks) missing %q", want)
		}
	}
	if strings.Contains(tasks, "REVIEWER FEEDBACK") {
		t.Error("authorPrompt without feedback must not carry a feedback section")
	}

	review := reviewPrompt(p, feat, "design")
	for _, want := range []string{"specs/tile-map/design.md", `"approve"`, "general context", "scope creep"} {
		if !strings.Contains(strings.ToLower(review), strings.ToLower(want)) {
			t.Errorf("reviewPrompt missing %q", want)
		}
	}

	e2e := e2ePrompt(p, feat)
	for _, want := range []string{"specs/tile-map/requirements.md", `"passed"`, "Playwright"} {
		if !strings.Contains(e2e, want) {
			t.Errorf("e2ePrompt missing %q", want)
		}
	}

	staff := staffPrompt(p.Challenge)
	for _, want := range []string{"fazer um jogo estilo wow", "e2e-qa", `"agents"`} {
		if !strings.Contains(staff, want) {
			t.Errorf("staffPrompt missing %q", want)
		}
	}
}
