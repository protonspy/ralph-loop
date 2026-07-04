package graph

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/protonspy/ralph-loop/internal/program"
)

func testPRD() *program.PRD {
	return &program.PRD{
		Program:   "wow-game",
		Challenge: "fazer um jogo estilo wow",
		Feats: []*program.Feat{
			{ID: "walking-skeleton", Title: "Playable canvas", Status: program.StatusPlanned},
			{ID: "tile-map", Title: "Tile map + movement", Depends: []string{"walking-skeleton"}, Status: program.StatusPlanned},
		},
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "graph.db"), "wow-game")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestProjectPRDIdempotent(t *testing.T) {
	s := openTestStore(t)
	p := testPRD()
	if err := ProjectPRD(s, p); err != nil {
		t.Fatal(err)
	}
	if err := ProjectPRD(s, p); err != nil {
		t.Fatal(err)
	}
	ents, err := s.Entities()
	if err != nil {
		t.Fatal(err)
	}
	// 1 program + 2 feats, no duplicates on reprojection.
	if len(ents) != 3 {
		t.Fatalf("entities = %d, want 3: %+v", len(ents), ents)
	}
	facts, err := s.CurrentFacts()
	if err != nil {
		t.Fatal(err)
	}
	// 2 HAS_FEAT + 1 DEPENDS_ON + 2 HAS_STATUS
	if len(facts) != 5 {
		t.Fatalf("current facts = %d, want 5: %+v", len(facts), facts)
	}
}

func TestProjectPRDStatusIsTemporal(t *testing.T) {
	s := openTestStore(t)
	p := testPRD()
	if err := ProjectPRD(s, p); err != nil {
		t.Fatal(err)
	}
	p.Feats[0].Status = program.StatusDone
	if err := ProjectPRD(s, p); err != nil {
		t.Fatal(err)
	}

	facts, _ := s.SearchFacts("walking-skeleton has status", 10)
	if len(facts) != 1 || !strings.Contains(facts[0].Fact, "done") {
		t.Fatalf("current status facts = %+v, want exactly the done fact", facts)
	}
	// The superseded planned fact must survive outside the current view.
	all, err := s.queryFacts(`SELECT `+edgeCols+` FROM entity_edges WHERE group_id=? AND rel=?`, s.group, RelHasStatus)
	if err != nil {
		t.Fatal(err)
	}
	var expired int
	for _, f := range all {
		if !f.Current() {
			expired++
		}
	}
	if expired != 1 {
		t.Fatalf("expired status facts = %d, want 1 (history preserved)", expired)
	}
}

func TestExportMermaidAndDOT(t *testing.T) {
	s := openTestStore(t)
	if err := ProjectPRD(s, testPRD()); err != nil {
		t.Fatal(err)
	}
	m, err := Export(s, "mermaid")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"graph LR", "feat: tile-map", "DEPENDS_ON", "[planned]"} {
		if !strings.Contains(m, want) {
			t.Errorf("mermaid export missing %q:\n%s", want, m)
		}
	}
	d, err := Export(s, "dot")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"digraph G", "DEPENDS_ON"} {
		if !strings.Contains(d, want) {
			t.Errorf("dot export missing %q:\n%s", want, d)
		}
	}
	if _, err := Export(s, "png"); err == nil {
		t.Error("Export(png) should fail")
	}
}
