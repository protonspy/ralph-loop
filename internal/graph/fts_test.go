package graph

import (
	"strings"
	"testing"
	"time"
)

// seedFacts adds a couple of entities and facts for search tests.
func seedForSearch(t *testing.T, s *Store) {
	t.Helper()
	a, _ := s.UpsertEntity(KindComponent, "collision-system")
	b, _ := s.UpsertEntity(KindConcept, "tile-map")
	if _, err := s.AddFact(FactInput{Src: a.UUID, Dst: b.UUID, Rel: RelDependsOn,
		Fact: "the collision-system reads the tile-map grid for AABB checks"}); err != nil {
		t.Fatal(err)
	}
	c, _ := s.UpsertEntity(KindConcept, "audio")
	if _, err := s.AddFact(FactInput{Src: c.UUID, Dst: b.UUID, Rel: RelRelatesTo,
		Fact: "audio playback is unrelated to grid math"}); err != nil {
		t.Fatal(err)
	}
}

func TestSearchFactsIsTermAndNotOrder(t *testing.T) {
	s := openTestStore(t)
	seedForSearch(t, s)

	// Terms match anywhere, in any order (AND recall), unlike a rigid substring.
	got, err := s.SearchFacts("grid collision", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !containsFact(got, "AABB") {
		t.Fatalf("want only the collision fact, got %+v", got)
	}

	// A term present in neither fact narrows to nothing.
	if got, _ := s.SearchFacts("collision netcode", 10); len(got) != 0 {
		t.Fatalf("unexpected matches: %+v", got)
	}

	// Hyphenated term stays whole (tokenchars): "collision-system" is one token.
	if got, _ := s.SearchFacts("collision-system", 10); len(got) != 1 {
		t.Fatalf("hyphen term search = %+v, want 1", got)
	}

	// Blank query lists recent current facts, never errors.
	if got, _ := s.SearchFacts("", 10); len(got) != 2 {
		t.Fatalf("blank query = %d facts, want 2", len(got))
	}
}

func TestSearchFactsExcludesSuperseded(t *testing.T) {
	s := openTestStore(t)
	feat, _ := s.UpsertEntity(KindFeat, "f")
	old, _ := s.AddFact(FactInput{Src: feat.UUID, Dst: feat.UUID, Rel: RelHasStatus, Fact: "feat f has status planned"})
	if _, err := s.AddFact(FactInput{Src: feat.UUID, Dst: feat.UUID, Rel: RelHasStatus,
		Fact: "feat f has status done", Supersedes: []string{old.UUID}}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.SearchFacts("feat f has status", 10)
	if len(got) != 1 || !containsFact(got, "done") {
		t.Fatalf("search must return only the current status fact, got %+v", got)
	}
}

func TestSearchEpisodesContent(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.AddEpisode("phaser docs", "context7", "Phaser 3 organizes a game into Scenes with a lifecycle.", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEpisode("aabb note", "web", "Axis-aligned bounding boxes are cheap to test.", time.Time{}); err != nil {
		t.Fatal(err)
	}
	got, err := s.SearchEpisodes("scenes lifecycle", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "phaser docs" {
		t.Fatalf("episode content search = %+v, want the phaser episode", got)
	}
	if got, _ := s.SearchEpisodes("", 10); len(got) != 2 {
		t.Fatalf("blank episode query = %d, want 2", len(got))
	}
}

func containsFact(fs []Fact, sub string) bool {
	for _, f := range fs {
		if strings.Contains(f.Fact, sub) {
			return true
		}
	}
	return false
}
