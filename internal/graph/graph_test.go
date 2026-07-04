package graph

import (
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "graph.db"), "wow")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestEntityDedup(t *testing.T) {
	s := openTest(t)
	a1, err := s.UpsertEntity("person", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	a2, err := s.UpsertEntity("person", "  alice ") // normalized-equal
	if err != nil {
		t.Fatal(err)
	}
	if a1.UUID != a2.UUID {
		t.Fatalf("expected dedup to same uuid, got %s vs %s", a1.UUID, a2.UUID)
	}
	// different kind = different entity
	other, _ := s.UpsertEntity("org", "Alice")
	if other.UUID == a1.UUID {
		t.Fatal("different kind should not dedup")
	}
}

func TestFactDedupAppendsEpisode(t *testing.T) {
	s := openTest(t)
	alice, _ := s.UpsertEntity("person", "Alice")
	acme, _ := s.UpsertEntity("org", "Acme")

	f1, err := s.AddFact(FactInput{Src: alice.UUID, Dst: acme.UUID, Rel: "WORKS_AT",
		Fact: "Alice works at Acme", ValidAt: date(2024, 1, 1), Episode: "ep1"})
	if err != nil {
		t.Fatal(err)
	}
	f2, err := s.AddFact(FactInput{Src: alice.UUID, Dst: acme.UUID, Rel: "WORKS_AT",
		Fact: "Alice works at Acme", Episode: "ep2"}) // identical → dedup
	if err != nil {
		t.Fatal(err)
	}
	if f1.UUID != f2.UUID {
		t.Fatalf("identical current fact should dedup, got %s vs %s", f1.UUID, f2.UUID)
	}
	if len(f2.Episodes) != 2 {
		t.Fatalf("expected 2 episodes after dedup, got %v", f2.Episodes)
	}
}

func TestBiTemporalSupersession(t *testing.T) {
	s := openTest(t)
	alice, _ := s.UpsertEntity("person", "Alice")
	acme, _ := s.UpsertEntity("org", "Acme")
	beta, _ := s.UpsertEntity("org", "Beta")

	f1, _ := s.AddFact(FactInput{Src: alice.UUID, Dst: acme.UUID, Rel: "WORKS_AT",
		Fact: "Alice works at Acme", ValidAt: date(2024, 1, 1)})
	f2, err := s.AddFact(FactInput{Src: alice.UUID, Dst: beta.UUID, Rel: "WORKS_AT",
		Fact: "Alice works at Beta", ValidAt: date(2024, 6, 1), Supersedes: []string{f1.UUID}})
	if err != nil {
		t.Fatal(err)
	}

	// current view: only the new fact
	cur, err := s.SearchFacts("Alice", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(cur) != 1 || cur[0].UUID != f2.UUID {
		t.Fatalf("current view should be only f2, got %d facts", len(cur))
	}

	// f1 preserved but expired, with invalid_at = f2.valid_at
	got, err := s.factByUUID(f1.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Current() {
		t.Fatal("f1 should be expired (not current)")
	}
	if got.InvalidAt == nil || !got.InvalidAt.Equal(date(2024, 6, 1)) {
		t.Fatalf("f1.invalid_at should be 2024-06-01, got %v", got.InvalidAt)
	}

	// as-of the real world: Feb 2024 → Acme was true; Jul 2024 → Beta
	feb, _ := s.FactsAsOf(date(2024, 2, 1))
	if len(feb) != 1 || feb[0].UUID != f1.UUID {
		t.Fatalf("as-of Feb should show f1 (Acme), got %d", len(feb))
	}
	jul, _ := s.FactsAsOf(date(2024, 7, 1))
	if len(jul) != 1 || jul[0].UUID != f2.UUID {
		t.Fatalf("as-of Jul should show f2 (Beta), got %d", len(jul))
	}
}

func TestNeighborsCurrentOnly(t *testing.T) {
	s := openTest(t)
	alice, _ := s.UpsertEntity("person", "Alice")
	acme, _ := s.UpsertEntity("org", "Acme")
	beta, _ := s.UpsertEntity("org", "Beta")

	f1, _ := s.AddFact(FactInput{Src: alice.UUID, Dst: acme.UUID, Rel: "WORKS_AT",
		Fact: "Alice works at Acme", ValidAt: date(2024, 1, 1)})
	_, _ = s.AddFact(FactInput{Src: alice.UUID, Dst: beta.UUID, Rel: "WORKS_AT",
		Fact: "Alice works at Beta", ValidAt: date(2024, 6, 1), Supersedes: []string{f1.UUID}})

	n, err := s.Neighbors(alice.UUID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(n) != 1 || n[0].Dst != beta.UUID {
		t.Fatalf("neighbors should be only the current Beta fact, got %d", len(n))
	}
}
