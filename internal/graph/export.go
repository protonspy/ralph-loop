package graph

import (
	"fmt"
	"regexp"
	"strings"
)

// Export renders the CURRENT view of the graph (entities + non-superseded
// facts) for humans — architecture drawn from reality, not a diagram that rots.
// format: "mermaid" (default) or "dot".
func Export(s *Store, format string) (string, error) {
	ents, err := s.Entities()
	if err != nil {
		return "", err
	}
	facts, err := s.CurrentFacts()
	if err != nil {
		return "", err
	}

	// HAS_STATUS self-loops annotate their node instead of drawing an edge.
	status := map[string]string{}
	var edges []Fact
	for _, f := range facts {
		if f.Rel == RelHasStatus && f.Src == f.Dst {
			if i := strings.LastIndex(f.Fact, " has status "); i >= 0 {
				status[f.Src] = f.Fact[i+len(" has status "):]
			}
			continue
		}
		edges = append(edges, f)
	}

	switch format {
	case "", "mermaid":
		return exportMermaid(ents, edges, status), nil
	case "dot":
		return exportDOT(ents, edges, status), nil
	default:
		return "", fmt.Errorf("unknown export format %q (want mermaid|dot)", format)
	}
}

func exportMermaid(ents []Entity, edges []Fact, status map[string]string) string {
	var b strings.Builder
	b.WriteString("graph LR\n")
	id := shortIDs(ents)
	for _, e := range ents {
		label := e.Kind + ": " + e.Name
		if st := status[e.UUID]; st != "" {
			label += " [" + st + "]"
		}
		fmt.Fprintf(&b, "  %s[\"%s\"]\n", id[e.UUID], mermaidEscape(label))
	}
	for _, f := range edges {
		s, okS := id[f.Src]
		d, okD := id[f.Dst]
		if !okS || !okD {
			continue
		}
		fmt.Fprintf(&b, "  %s -->|%s| %s\n", s, mermaidEscape(f.Rel), d)
	}
	return b.String()
}

func exportDOT(ents []Entity, edges []Fact, status map[string]string) string {
	var b strings.Builder
	b.WriteString("digraph G {\n  rankdir=LR;\n  node [shape=box];\n")
	id := shortIDs(ents)
	for _, e := range ents {
		label := e.Kind + ": " + e.Name
		if st := status[e.UUID]; st != "" {
			label += "\n[" + st + "]" // %q renders this as \n, DOT's line break
		}
		fmt.Fprintf(&b, "  %s [label=%q];\n", id[e.UUID], label)
	}
	for _, f := range edges {
		s, okS := id[f.Src]
		d, okD := id[f.Dst]
		if !okS || !okD {
			continue
		}
		fmt.Fprintf(&b, "  %s -> %s [label=%q];\n", s, d, f.Rel)
	}
	b.WriteString("}\n")
	return b.String()
}

var unsafeID = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// shortIDs gives every entity a stable, human-scannable diagram id
// (kind_name, uniquified with a uuid prefix on collision).
func shortIDs(ents []Entity) map[string]string {
	out := make(map[string]string, len(ents))
	taken := map[string]bool{}
	for _, e := range ents {
		id := unsafeID.ReplaceAllString(e.Kind+"_"+e.Name, "_")
		if len(id) > 40 {
			id = id[:40]
		}
		if taken[id] {
			id += "_" + e.UUID[:6]
		}
		taken[id] = true
		out[e.UUID] = id
	}
	return out
}

func mermaidEscape(s string) string {
	s = strings.ReplaceAll(s, `"`, "'")
	s = strings.ReplaceAll(s, "|", "/")
	return s
}
