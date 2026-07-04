package mcp

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/protonspy/ralph-loop/internal/graph"
)

// The graph tools the brain sees. Ergonomics rule: the brain speaks in
// (kind, name) pairs, never UUIDs — add_fact upserts its endpoints, so one call
// records entities and the edge between them.

func toolDefs() []map[string]any {
	obj := func(required []string, props map[string]any) map[string]any {
		schema := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	num := func(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }

	kinds := "one of: program|feat|spec|requirement|component|file|decision|test|agent|skill|concept"
	return []map[string]any{
		{
			"name":        "add_fact",
			"description": "Record a fact as an edge src —REL→ dst in the temporal knowledge graph. Endpoints are upserted by (kind,name). Use supersedes to temporally invalidate contradicted fact UUIDs (history is preserved).",
			"inputSchema": obj([]string{"src_kind", "src_name", "dst_kind", "dst_name", "rel", "fact"}, map[string]any{
				"src_kind":   str("source entity kind, " + kinds),
				"src_name":   str("source entity name"),
				"dst_kind":   str("destination entity kind, " + kinds),
				"dst_name":   str("destination entity name"),
				"rel":        str("relation type, e.g. DEPENDS_ON|HAS_REQ|HAS_COMPONENT|TRACES_TO|IMPLEMENTS|VERIFIES|JUSTIFIES|WORKED_ON|RELATES_TO"),
				"fact":       str("the fact as one self-contained sentence"),
				"valid_at":   str("RFC3339 time the fact became true in the real world (default: now)"),
				"episode":    str("uuid of the source episode (provenance), from add_episode"),
				"supersedes": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "uuids of facts this one contradicts/replaces"},
			}),
		},
		{
			"name":        "add_episode",
			"description": "Store a raw observation/document (research finding, doc excerpt, decision context) as provenance. Returns its uuid to link from add_fact.",
			"inputSchema": obj([]string{"name", "content"}, map[string]any{
				"name":     str("short episode title"),
				"source":   str("origin, e.g. websearch|context7|file|conversation (default text)"),
				"content":  str("the raw content"),
				"valid_at": str("RFC3339 time the observation refers to (default: now)"),
			}),
		},
		{
			"name":        "upsert_entity",
			"description": "Ensure an entity node (kind, name) exists and return it. add_fact already upserts endpoints; use this only for standalone nodes.",
			"inputSchema": obj([]string{"kind", "name"}, map[string]any{
				"kind": str(kinds),
				"name": str("canonical entity name"),
			}),
		},
		{
			"name":        "search_nodes",
			"description": "Find entities by name/summary substring.",
			"inputSchema": obj([]string{"query"}, map[string]any{
				"query": str("substring to match"),
				"limit": num("max results (default 20)"),
			}),
		},
		{
			"name":        "search_facts",
			"description": "Find CURRENT facts whose text or relation matches the query (full-text, ranked). Use before adding facts, to ground answers and to discover uuids to supersede.",
			"inputSchema": obj([]string{"query"}, map[string]any{
				"query": str("terms to match (implicitly AND-ed; blank = most recent)"),
				"limit": num("max results (default 20)"),
			}),
		},
		{
			"name":        "search_episodes",
			"description": "Full-text search over raw episodes/documents (research findings, doc excerpts) by name/content. Use to recall the source material behind facts.",
			"inputSchema": obj([]string{"query"}, map[string]any{
				"query": str("terms to match (implicitly AND-ed; blank = most recent)"),
				"limit": num("max results (default 20)"),
			}),
		},
		{
			"name":        "neighbors",
			"description": "Walk the current graph around an entity: all facts within N hops.",
			"inputSchema": obj([]string{"kind", "name"}, map[string]any{
				"kind":  str(kinds),
				"name":  str("entity name"),
				"depth": num("hops to expand (default 1)"),
			}),
		},
		{
			"name":        "facts_as_of",
			"description": "Point-in-time view: facts that were true in the real world at the given time, including later-superseded ones.",
			"inputSchema": obj([]string{"time"}, map[string]any{
				"time": str("RFC3339 timestamp"),
			}),
		},
	}
}

func (s *Server) callTool(name string, args json.RawMessage) map[string]any {
	switch name {
	case "add_fact":
		var a struct {
			SrcKind, SrcName, DstKind, DstName, Rel, Fact, ValidAt, Episode string
			Supersedes                                                      []string
		}
		if err := unmarshalArgs(args, &a); err != nil {
			return toolResult(nil, err)
		}
		src, err := s.store.UpsertEntity(a.SrcKind, a.SrcName)
		if err != nil {
			return toolResult(nil, err)
		}
		dst, err := s.store.UpsertEntity(a.DstKind, a.DstName)
		if err != nil {
			return toolResult(nil, err)
		}
		validAt, err := parseTime(a.ValidAt)
		if err != nil {
			return toolResult(nil, err)
		}
		f, err := s.store.AddFact(graph.FactInput{
			Src: src.UUID, Dst: dst.UUID, Rel: a.Rel, Fact: a.Fact,
			ValidAt: validAt, Episode: a.Episode, Supersedes: a.Supersedes,
		})
		return toolResult(f, err)

	case "add_episode":
		var a struct{ Name, Source, Content, ValidAt string }
		if err := unmarshalArgs(args, &a); err != nil {
			return toolResult(nil, err)
		}
		validAt, err := parseTime(a.ValidAt)
		if err != nil {
			return toolResult(nil, err)
		}
		uuid, err := s.store.AddEpisode(a.Name, a.Source, a.Content, validAt)
		return toolResult(map[string]string{"uuid": uuid}, err)

	case "upsert_entity":
		var a struct{ Kind, Name string }
		if err := unmarshalArgs(args, &a); err != nil {
			return toolResult(nil, err)
		}
		e, err := s.store.UpsertEntity(a.Kind, a.Name)
		return toolResult(e, err)

	case "search_nodes":
		var a struct {
			Query string
			Limit int
		}
		if err := unmarshalArgs(args, &a); err != nil {
			return toolResult(nil, err)
		}
		out, err := s.store.SearchNodes(a.Query, a.Limit)
		return toolResult(out, err)

	case "search_facts":
		var a struct {
			Query string
			Limit int
		}
		if err := unmarshalArgs(args, &a); err != nil {
			return toolResult(nil, err)
		}
		out, err := s.store.SearchFacts(a.Query, a.Limit)
		return toolResult(out, err)

	case "search_episodes":
		var a struct {
			Query string
			Limit int
		}
		if err := unmarshalArgs(args, &a); err != nil {
			return toolResult(nil, err)
		}
		out, err := s.store.SearchEpisodes(a.Query, a.Limit)
		return toolResult(out, err)

	case "neighbors":
		var a struct {
			Kind, Name string
			Depth      int
		}
		if err := unmarshalArgs(args, &a); err != nil {
			return toolResult(nil, err)
		}
		e, found, err := s.store.FindEntity(a.Kind, a.Name)
		if err != nil {
			return toolResult(nil, err)
		}
		if !found {
			return toolResult(nil, fmt.Errorf("no entity (%s, %s)", a.Kind, a.Name))
		}
		out, err := s.store.Neighbors(e.UUID, a.Depth)
		return toolResult(out, err)

	case "facts_as_of":
		var a struct{ Time string }
		if err := unmarshalArgs(args, &a); err != nil {
			return toolResult(nil, err)
		}
		t, err := parseTime(a.Time)
		if err != nil {
			return toolResult(nil, err)
		}
		if t.IsZero() {
			return toolResult(nil, fmt.Errorf("time is required (RFC3339)"))
		}
		out, err := s.store.FactsAsOf(t)
		return toolResult(out, err)

	default:
		return toolResult(nil, fmt.Errorf("unknown tool %q", name))
	}
}

// unmarshalArgs decodes snake_case tool arguments into the CamelCase struct via
// a field-name mapping pass (encoding/json is case-insensitive but not
// underscore-insensitive).
func unmarshalArgs(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	flat := make(map[string]json.RawMessage, len(m))
	for k, val := range m {
		flat[stripUnderscores(k)] = val
	}
	re, _ := json.Marshal(flat)
	return json.Unmarshal(re, v)
}

func stripUnderscores(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '_' {
			out = append(out, s[i])
		}
	}
	return string(out)
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid RFC3339 time %q: %w", s, err)
	}
	return t, nil
}
