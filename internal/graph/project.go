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

// ReqTrace is a component→requirement traceability link, derived from a task's
// _Boundary_ (the component) and its _Requirements_ annotation.
type ReqTrace struct{ Component, Requirement string }

// SpecProjection is the structured slice of a csdd spec the projector needs.
// The caller (which owns the tasks.md parser) extracts it, keeping this package
// free of the spec parser.
type SpecProjection struct {
	FeatID       string
	Requirements []string   // spec-local requirement IDs, e.g. "1.1"
	Components   []string   // component/boundary names from the design
	Traces       []ReqTrace // component → requirement links
}

// ProjectSpec mirrors a feat's approved csdd contract into the graph — the
// requirement/component tier of the living documentation graph (feat→spec,
// spec→requirement, feat→component, component→requirement). Idempotent:
// entities and identical facts dedupe in the store. Requirement names are
// namespaced by feat (IDs are spec-local and would otherwise collide); component
// names are shared across feats on purpose, so cross-feat reuse surfaces.
func ProjectSpec(s *Store, in SpecProjection) error {
	if in.FeatID == "" {
		return fmt.Errorf("feat id is required")
	}
	feat, err := s.UpsertEntity(KindFeat, in.FeatID)
	if err != nil {
		return err
	}
	sp, err := s.UpsertEntity(KindSpec, in.FeatID)
	if err != nil {
		return err
	}
	if _, err := s.AddFact(FactInput{
		Src: feat.UUID, Dst: sp.UUID, Rel: RelHasSpec,
		Fact: fmt.Sprintf("feat %s is specified by spec %s", in.FeatID, in.FeatID),
	}); err != nil {
		return err
	}

	reqNode := make(map[string]Entity, len(in.Requirements))
	for _, r := range in.Requirements {
		n, err := s.UpsertEntity(KindRequirement, in.FeatID+"/"+r)
		if err != nil {
			return err
		}
		reqNode[r] = n
		if _, err := s.AddFact(FactInput{
			Src: sp.UUID, Dst: n.UUID, Rel: RelHasReq,
			Fact: fmt.Sprintf("spec %s has requirement %s", in.FeatID, r),
		}); err != nil {
			return err
		}
	}

	compNode := make(map[string]Entity, len(in.Components))
	for _, c := range in.Components {
		n, err := s.UpsertEntity(KindComponent, c)
		if err != nil {
			return err
		}
		compNode[c] = n
		if _, err := s.AddFact(FactInput{
			Src: feat.UUID, Dst: n.UUID, Rel: RelHasComponent,
			Fact: fmt.Sprintf("feat %s has component %s", in.FeatID, c),
		}); err != nil {
			return err
		}
	}

	for _, t := range in.Traces {
		c, ok := compNode[t.Component]
		if !ok {
			continue
		}
		r, ok := reqNode[t.Requirement]
		if !ok {
			continue
		}
		if _, err := s.AddFact(FactInput{
			Src: c.UUID, Dst: r.UUID, Rel: RelTracesTo,
			Fact: fmt.Sprintf("component %s traces to requirement %s/%s", t.Component, in.FeatID, t.Requirement),
		}); err != nil {
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
