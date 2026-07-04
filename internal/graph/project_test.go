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

func TestProjectSpec(t *testing.T) {
	s := openTestStore(t)
	in := SpecProjection{
		FeatID:       "tile-map",
		Requirements: []string{"1.1", "1.2"},
		Components:   []string{"Renderer"},
		Traces:       []ReqTrace{{"Renderer", "1.1"}, {"Renderer", "1.2"}},
	}
	if err := ProjectSpec(s, in); err != nil {
		t.Fatal(err)
	}
	if err := ProjectSpec(s, in); err != nil { // idempotent
		t.Fatal(err)
	}

	// feat + spec + 2 requirements + 1 component = 5 entities, no dupes.
	ents, err := s.Entities()
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, e := range ents {
		kinds[e.Kind]++
	}
	if kinds[KindFeat] != 1 || kinds[KindSpec] != 1 || kinds[KindRequirement] != 2 || kinds[KindComponent] != 1 {
		t.Fatalf("entity kinds = %v (want feat1 spec1 req2 comp1)", kinds)
	}

	// HAS_SPEC + 2 HAS_REQ + HAS_COMPONENT + 2 TRACES_TO = 6 facts, no dupes.
	facts, err := s.CurrentFacts()
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 6 {
		t.Fatalf("current facts = %d, want 6: %+v", len(facts), facts)
	}
	// FTS matches words, not exact IDs (1.1 vs 1.2 tokenize the same), so both
	// TRACES_TO facts surface; assert the 1.1 one is present.
	got, _ := s.SearchFacts("traces to requirement tile-map", 5)
	var has11 bool
	for _, f := range got {
		if f.Rel == RelTracesTo && strings.Contains(f.Fact, "tile-map/1.1") {
			has11 = true
		}
	}
	if !has11 {
		t.Errorf("expected a TRACES_TO fact for 1.1 among %+v", got)
	}
}

func TestProjectBuildUnit(t *testing.T) {
	s := openTestStore(t)
	// Spec tier first, so the build tier links to the same nodes.
	if err := ProjectSpec(s, SpecProjection{
		FeatID: "tile-map", Requirements: []string{"1.1"}, Components: []string{"Renderer"},
		Traces: []ReqTrace{{"Renderer", "1.1"}},
	}); err != nil {
		t.Fatal(err)
	}
	unit := BuildUnit{
		FeatID: "tile-map", Component: "Renderer", Requirements: []string{"1.1"},
		ImplFiles: []string{"src/renderer.go"}, TestFiles: []string{"src/renderer_test.go"},
	}
	if err := ProjectBuildUnit(s, unit); err != nil {
		t.Fatal(err)
	}
	if err := ProjectBuildUnit(s, unit); err != nil { // idempotent
		t.Fatal(err)
	}

	if got, _ := s.SearchFacts("implements component Renderer", 5); len(got) != 1 {
		t.Errorf("IMPLEMENTS facts = %+v, want 1", got)
	}
	if got, _ := s.SearchFacts("verifies requirement tile-map/1.1", 5); len(got) != 1 {
		t.Errorf("VERIFIES facts = %+v, want 1", got)
	}

	// The file/test nodes reuse the shared Renderer component (no duplicate).
	kinds := map[string]int{}
	ents, _ := s.Entities()
	for _, e := range ents {
		kinds[e.Kind]++
	}
	if kinds[KindComponent] != 1 || kinds[KindFile] != 1 || kinds[KindTest] != 1 {
		t.Fatalf("entity kinds = %v (want comp1 file1 test1)", kinds)
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
