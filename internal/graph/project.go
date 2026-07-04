package graph

import (
	"fmt"
	"path/filepath"

	"github.com/protonspy/ralph-loop/internal/program"
)

// DBPath is the canonical location of a workspace's graph database.
func DBPath(root string) string { return filepath.Join(program.Dir(root), "graph.db") }

// ProjectPRD mirrors the program state (.ralph/prd.json) into the graph — the
// mechanical half of the living documentation graph. It is idempotent: entities
// and identical facts dedupe in the store, and a feat's status is a TEMPORAL
// fact — when the status changes, the previous status fact is superseded
// (invalid_at + expired_at), never deleted, so `FactsAsOf` can replay history.
func ProjectPRD(s *Store, p *program.PRD) error {
	prog, err := s.UpsertEntity(KindProgram, p.Program)
	if err != nil {
		return err
	}

	// First pass: every feat node exists before we draw dependency edges.
	nodes := make(map[string]Entity, len(p.Feats))
	for _, f := range p.Feats {
		n, err := s.UpsertEntity(KindFeat, f.ID)
		if err != nil {
			return err
		}
		nodes[f.ID] = n
	}

	for _, f := range p.Feats {
		n := nodes[f.ID]
		if _, err := s.AddFact(FactInput{
			Src: prog.UUID, Dst: n.UUID, Rel: RelHasFeat,
			Fact: fmt.Sprintf("program %s includes feat %s: %s", p.Program, f.ID, f.Title),
		}); err != nil {
			return err
		}
		for _, dep := range f.Depends {
			d, ok := nodes[dep]
			if !ok {
				continue // dangling dependency; prd.json is the source of truth
			}
			if _, err := s.AddFact(FactInput{
				Src: n.UUID, Dst: d.UUID, Rel: RelDependsOn,
				Fact: fmt.Sprintf("feat %s depends on feat %s", f.ID, dep),
			}); err != nil {
				return err
			}
		}
		if err := s.setStatus(n, f); err != nil {
			return err
		}
	}
	return nil
}

// setStatus records "feat X has status S" as a temporal fact, superseding the
// previous status fact when the status changed.
func (s *Store) setStatus(n Entity, f *program.Feat) error {
	text := fmt.Sprintf("feat %s has status %s", f.ID, f.Status)
	prev, err := s.queryFacts(
		`SELECT `+edgeCols+` FROM entity_edges
		 WHERE group_id=? AND src=? AND dst=? AND rel=? AND expired_at IS NULL`,
		s.group, n.UUID, n.UUID, RelHasStatus)
	if err != nil {
		return err
	}
	in := FactInput{Src: n.UUID, Dst: n.UUID, Rel: RelHasStatus, Fact: text}
	for _, p := range prev {
		if normalize(p.Fact) == normalize(text) {
			return nil // unchanged; AddFact would dedupe anyway
		}
		in.Supersedes = append(in.Supersedes, p.UUID)
	}
	_, err = s.AddFact(in)
	return err
}
